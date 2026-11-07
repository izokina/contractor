package eval

import (
	"slices"

	"github.com/izokina/contractor/pkg/pipeline/expr"
	"github.com/izokina/contractor/pkg/pipeline/ident"
)

// ScratchLimit is the most entries a scratch map keeps between uses. A map
// never shrinks, and clearing or ranging over it costs its peak size.
const ScratchLimit = 64

// Sum merges monomials and tensor terms whose words are equal: a term's Pair
// list, then the opaque factors of one of its monomials, in key order. It
// finds a word by the hashes of its two parts and confirms a match against
// what it stored. An entry whose hash another one holds takes the next free
// value, and lookups step the same way, so a Sum never deletes a single entry.
// It copies the Pairs and the opaque factors it keeps. Not safe for concurrent
// use.
type Sum struct {
	groups map[ident.Hash]int
	words  map[word]int
	terms  []expr.Term // terms[0] is pair-free
	bytes  int
}

// The approximate bytes that a Sum keeps for a group and for a monomial,
// besides their Pairs and opaque factors, and for each of those.
const (
	groupBytes    = 64
	monomialBytes = 96
	itemBytes     = 8
)

type word struct {
	group int // its term's index in Sum.terms
	hash  ident.Hash
}

func NewSum() *Sum {
	return &Sum{groups: make(map[ident.Hash]int), words: make(map[word]int), terms: make([]expr.Term, 1)}
}

// AddSorted adds the pair-free monomial coeff × ops, whose factors must be in
// key order.
func (s *Sum) AddSorted(coeff expr.Complex, ops []expr.Factor) {
	s.add(0, coeff, ops, hashOpaques(ops))
}

func (s *Sum) AddKeyed(k *KeyedTerm) {
	var g int
	if len(k.term.Pairs) != 0 {
		g = s.group(k.pairs, k.term.Pairs)
	}
	for i, m := range k.term.Coeff.Monomials {
		s.add(g, m.Coeff, m.Opaques, k.words[i])
	}
}

// Bytes returns the approximate memory that the Sum keeps for what it holds,
// not counting coefficients past int32.
func (s *Sum) Bytes() int {
	return s.bytes
}

// Scalar returns the sum of a Sum that holds only pair-free monomials, without
// those that cancelled, and empties it.
func (s *Sum) Scalar() expr.Scalar {
	monomials := s.terms[0].Coeff.Monomials
	out := make([]expr.Monomial, 0, len(monomials))
	for _, m := range monomials {
		if !m.Coeff.IsZero() {
			out = append(out, m)
		}
	}
	clear(monomials)
	if len(monomials) > ScratchLimit {
		monomials = nil
	}
	s.terms[0].Coeff.Monomials = monomials[:0]
	s.words = emptied(s.words)
	s.bytes = 0
	return expr.Scalar{Monomials: out}
}

// Terms returns the terms added, merged, without the monomials that
// cancelled and the terms left with none, and empties the Sum. The caller
// owns their slices.
func (s *Sum) Terms() []expr.Term {
	out := s.terms[:0]
	for _, t := range s.terms {
		t.Coeff.Monomials = slices.DeleteFunc(t.Coeff.Monomials, func(m expr.Monomial) bool { return m.Coeff.IsZero() })
		if len(t.Coeff.Monomials) != 0 {
			out = append(out, t)
		}
	}
	clear(s.terms[len(out):])
	s.terms = make([]expr.Term, 1)
	s.groups = emptied(s.groups)
	s.words = emptied(s.words)
	s.bytes = 0
	return out
}

// add merges coeff × ops into group g; h is the hash of ops.
func (s *Sum) add(g int, coeff expr.Complex, ops []expr.Factor, h ident.Hash) {
	monomials := s.terms[g].Coeff.Monomials
	w := word{group: g, hash: h}
	for ; ; w.hash++ {
		i, ok := s.words[w]
		if !ok {
			break
		}
		if slices.Equal(monomials[i].Opaques, ops) {
			monomials[i].Coeff = monomials[i].Coeff.Add(coeff)
			return
		}
	}
	s.words[w] = len(monomials)
	s.bytes += monomialBytes + itemBytes*len(ops)
	ops = append(make([]expr.Factor, 0, len(ops)), ops...)
	s.terms[g].Coeff.Monomials = append(monomials, expr.Monomial{Coeff: coeff, Opaques: ops})
}

func (s *Sum) group(h ident.Hash, pairs []expr.Pair) int {
	for ; ; h++ {
		g, ok := s.groups[h]
		if !ok {
			g = len(s.terms)
			s.groups[h] = g
			s.bytes += groupBytes + itemBytes*len(pairs)
			pairs = append(make([]expr.Pair, 0, len(pairs)), pairs...)
			s.terms = append(s.terms, expr.Term{Pairs: pairs})
			return g
		}
		if slices.Equal(s.terms[g].Pairs, pairs) {
			return g
		}
	}
}

// KeyedTerm holds a term with the hashes of its words, made apart from adding
// it so that goroutines that share a Sum key their terms before they lock it.
// The zero value is ready to use.
type KeyedTerm struct {
	term  expr.Term
	pairs ident.Hash
	words []ident.Hash
}

// Set keys t in place of the term k held. Equal terms get equal keys only if
// every term a Sum gets is in one canonical order of its Pairs, their slots
// and each monomial's factors: the identity order of contract.Contractor's
// terms and of this package's Scalars, or the output order of the mergers
// below the root.
func (k *KeyedTerm) Set(t expr.Term) {
	k.term, k.pairs = t, 0
	for _, p := range t.Pairs {
		s := p.Slots()
		k.pairs = k.pairs.Add(ident.ID(s[0])).Add(ident.ID(s[1]))
	}
	k.words = k.words[:0]
	for _, m := range t.Coeff.Monomials {
		k.words = append(k.words, hashOpaques(m.Opaques))
	}
}

func hashOpaques(ops []expr.Factor) ident.Hash {
	var h ident.Hash
	for _, o := range ops {
		h = h.Add(expr.KeyOf(o.Node)).Add(ident.ID(o.Exp))
	}
	return h
}

func emptied[K comparable, V any](m map[K]V) map[K]V {
	if len(m) > ScratchLimit {
		return make(map[K]V)
	}
	clear(m)
	return m
}
