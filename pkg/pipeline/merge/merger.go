package merge

import (
	"encoding/json/jsontext"
	"errors"
	"io"
	"maps"
	"slices"
	"sync"

	"github.com/izokina/contractor/pkg/pipeline/codec"
	"github.com/izokina/contractor/pkg/pipeline/eval"
	"github.com/izokina/contractor/pkg/pipeline/expr"
	"github.com/izokina/contractor/pkg/pipeline/ident"
)

// Merger merges terms in subtrees, one for each member of their words at one
// position. A monomial's word is its term's Pairs, the end of the Pairs, then
// its opaque factors, each with its exponent and whether it ends the word, all
// in output order. The output is in word order, so it is the subtrees in
// member order, each in output order. Past its limit a merger moves its
// subtrees' terms to a temporary file, and at flush merges each moved subtree
// again in a merger one position deeper, which shares the file.
//
// Input.Add changes the root only with mu held, and outside it reads only the
// table, which needs no lock. Flush, and every merger below the root, runs
// after the last Add.
type Merger struct {
	mu       sync.Mutex
	o        order
	pos      position
	subtrees map[uint64]*subtree
	key      eval.KeyedTerm

	limit  int
	held   int
	dirty  []*subtree
	file   *dump
	dumped bool
}

// position is where the members of a Merger's subtrees stand in a word: the
// Pair of index at, or, when factors is set, the opaque factor of index at.
// Only the root's terms are in identity order, so its members come from
// firstPair.
type position struct {
	factors bool
	at      int
}

// below is the position of the members of the merger of the subtree of
// member. The end of a word is never moved, so it has none.
func (p position) below(member uint64) position {
	if !p.factors && member == 0 {
		return position{factors: true}
	}
	return position{factors: p.factors, at: p.at + 1}
}

// subtree merges the terms whose words have its member. bytes is what its Sum
// held when last counted, nonzero exactly when the merger lists it as dirty,
// and segments are where its terms lie in the file.
type subtree struct {
	member   uint64
	sum      *eval.Sum
	bytes    int
	segments []segment
}

// NewMerger returns a merger that moves its terms to a temporary file whenever
// it holds more than about limit bytes of them, or never if limit is 0.
func NewMerger(t *ident.Table, limit int) *Merger {
	return &Merger{o: order{t}, subtrees: make(map[uint64]*subtree), limit: limit}
}

// Input feeds one goroutine's batches of terms into a Merger.
type Input struct {
	m       *Merger
	keys    []eval.KeyedTerm
	members []uint64
}

func (m *Merger) NewInput() *Input {
	return &Input{m: m}
}

// Add merges terms, which must be in the identity order that
// Contractor.ContractAndNormalize leaves. It fails only if the merger cannot
// write its file.
func (in *Input) Add(terms []expr.Term) error {
	in.keys = slices.Grow(in.keys[:0], len(terms))[:len(terms)]
	in.members = in.members[:0]
	for i, t := range terms {
		in.keys[i].Set(t)
		in.members = append(in.members, in.m.o.firstPair(t.Pairs))
	}
	in.m.mu.Lock()
	var err error
	for i, member := range in.members {
		if err = in.m.add(member, &in.keys[i]); err != nil {
			break
		}
	}
	in.m.mu.Unlock()
	// Keep the keys' buffers, but no reference to the caller's terms.
	for i := range in.keys {
		in.keys[i].Set(expr.Term{})
	}
	return err
}

// add merges the term that k holds into the subtree of member. The end of a
// word holds one word, so it is neither counted nor moved, and a remerge
// always goes deeper.
func (m *Merger) add(member uint64, k *eval.KeyedTerm) error {
	s, ok := m.subtrees[member]
	if !ok {
		s = &subtree{member: member, sum: eval.NewSum()}
		m.subtrees[member] = s
	}
	s.sum.AddKeyed(k)
	if m.limit == 0 || m.pos.factors && member == 0 {
		return nil
	}
	if s.bytes == 0 && s.sum.Bytes() != 0 {
		m.dirty = append(m.dirty, s)
	}
	m.held += s.sum.Bytes() - s.bytes
	s.bytes = s.sum.Bytes()
	if m.held > m.limit {
		return m.dump()
	}
	return nil
}

func (m *Merger) dump() error {
	if m.file == nil {
		f, err := newDump()
		if err != nil {
			return err
		}
		m.file = f
	}
	m.dumped = true
	for _, s := range m.dirty {
		seg, err := m.file.write(s.sum.Terms())
		if err != nil {
			return err
		}
		s.segments = append(s.segments, seg)
		s.bytes = 0
	}
	clear(m.dirty)
	m.dirty, m.held = m.dirty[:0], 0
	return m.file.w.Flush()
}

func (m *Merger) Close() error {
	if m.file == nil {
		return nil
	}
	err := m.file.remove()
	m.file = nil
	return err
}

// Flush writes all accumulated terms to enc as a single JSON value, empties
// the merger and removes its file.
func (m *Merger) Flush(enc *jsontext.Encoder) (err error) {
	defer func() { err = errors.Join(err, m.Close()) }()
	w := codec.NewWriter(enc, m.o.t)
	if err := m.flush(w); err != nil {
		return err
	}
	return w.Close()
}

// flush writes the merger's terms to w in member order. A merger that has
// moved terms first moves the rest, so that a subtree with segments lies whole
// in the file, and its children have the memory.
func (m *Merger) flush(w *codec.Writer) error {
	if m.dumped {
		if err := m.dump(); err != nil {
			return err
		}
	}
	subtrees := slices.SortedFunc(maps.Values(m.subtrees), func(a, b *subtree) int {
		return m.o.compareMember(m.pos.factors, a.member, b.member)
	})
	clear(m.subtrees)
	for _, s := range subtrees {
		var err error
		if len(s.segments) == 0 {
			err = m.write(w, s.sum.Terms())
		} else {
			err = m.remerge(w, s)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// remerge merges the subtree s again from the file, in a merger one position
// deeper, and writes it to w. Then it cuts the file back to start: a merger
// writes nothing after the dump that starts its flush, so all past start is
// the child's, and read.
func (m *Merger) remerge(w *codec.Writer, s *subtree) error {
	start := m.file.size
	c := &Merger{o: m.o, pos: m.pos.below(s.member), subtrees: make(map[uint64]*subtree), limit: m.limit, file: m.file}
	r := newReader(m.o.t)
	for _, seg := range s.segments {
		r.reset(m.file, seg)
		for {
			t, err := r.term()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if err := c.addTerm(t); err != nil {
				return err
			}
		}
	}
	if err := c.flush(w); err != nil {
		return err
	}
	if err := m.file.f.Truncate(start); err != nil {
		return err
	}
	m.file.size = start
	_, err := m.file.f.Seek(start, io.SeekStart)
	return err
}

// addTerm merges t, read from the file, into a merger below the root.
func (m *Merger) addTerm(t expr.Term) error {
	m.o.sort(t)
	if !m.pos.factors {
		var member uint64
		if m.pos.at < len(t.Pairs) {
			member = uint64(t.Pairs[m.pos.at])
		}
		m.key.Set(t)
		return m.add(member, &m.key)
	}
	for i, mono := range t.Coeff.Monomials {
		var member uint64
		if m.pos.at < len(mono.Opaques) {
			member = factorMember(mono.Opaques[m.pos.at], m.pos.at == len(mono.Opaques)-1)
		}
		m.key.Set(expr.Term{Pairs: t.Pairs, Coeff: expr.Scalar{Monomials: t.Coeff.Monomials[i : i+1]}})
		if err := m.add(member, &m.key); err != nil {
			return err
		}
	}
	return nil
}

// write hands the monomials of terms, which the merger owns, to w in output
// order, each pair-free monomial as an addend of its own.
func (m *Merger) write(w *codec.Writer, terms []expr.Term) error {
	addends := make([]expr.Term, 0, len(terms))
	for _, t := range terms {
		m.o.sort(t)
		if len(t.Pairs) != 0 {
			addends = append(addends, t)
			continue
		}
		for i := range t.Coeff.Monomials {
			addends = append(addends, expr.Term{Coeff: expr.Scalar{Monomials: t.Coeff.Monomials[i : i+1]}})
		}
	}
	slices.SortFunc(addends, m.o.compareAddends)
	for _, t := range addends {
		for _, mono := range t.Coeff.Monomials {
			if err := w.Add(t.Pairs, mono); err != nil {
				return err
			}
		}
	}
	return nil
}
