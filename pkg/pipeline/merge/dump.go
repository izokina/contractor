package merge

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"slices"

	"github.com/izokina/contractor/pkg/pipeline/expr"
	"github.com/izokina/contractor/pkg/pipeline/ident"
)

// dump is the temporary file to which a Merger, and the mergers below it,
// move the terms of their subtrees, a record per term. An opaque factor is
// written as its key, which finds its node in the table again.
type dump struct {
	f       *os.File
	removed bool
	w       *bufio.Writer
	size    int64
	record  []byte
	head    []byte
}

type segment struct {
	offset, length int64
}

// newDump removes the file's name at once where the system allows it while
// the file is open, so that it is gone even if the process is killed;
// elsewhere remove does it.
func newDump() (*dump, error) {
	f, err := os.CreateTemp("", "contractor-*")
	if err != nil {
		return nil, err
	}
	return &dump{f: f, removed: os.Remove(f.Name()) == nil, w: bufio.NewWriter(f)}, nil
}

func (d *dump) write(terms []expr.Term) (segment, error) {
	s := segment{offset: d.size}
	for _, t := range terms {
		d.record = appendTerm(d.record[:0], t)
		d.head = binary.AppendUvarint(d.head[:0], uint64(len(d.record)))
		if _, err := d.w.Write(d.head); err != nil {
			return segment{}, err
		}
		if _, err := d.w.Write(d.record); err != nil {
			return segment{}, err
		}
		s.length += int64(len(d.head) + len(d.record))
	}
	d.size += s.length
	return s, nil
}

func appendTerm(b []byte, t expr.Term) []byte {
	b = binary.AppendUvarint(b, uint64(len(t.Pairs)))
	for _, p := range t.Pairs {
		b = binary.LittleEndian.AppendUint64(b, uint64(p))
	}
	b = binary.AppendUvarint(b, uint64(len(t.Coeff.Monomials)))
	for _, m := range t.Coeff.Monomials {
		b = expr.AppendRational(b, m.Coeff.Re)
		b = expr.AppendRational(b, m.Coeff.Im)
		b = binary.AppendUvarint(b, uint64(len(m.Opaques)))
		for _, o := range m.Opaques {
			b = binary.LittleEndian.AppendUint32(b, uint32(expr.KeyOf(o.Node)))
			b = binary.AppendUvarint(b, uint64(o.Exp))
		}
	}
	return b
}

func (d *dump) remove() error {
	err := d.f.Close()
	if !d.removed {
		err = errors.Join(err, os.Remove(d.f.Name()))
	}
	return err
}

// reader reads the terms of a segment of a dump. A term it returns is valid
// until the next one.
type reader struct {
	r         *bufio.Reader
	t         *ident.Table
	record    []byte
	pairs     []expr.Pair
	monomials []expr.Monomial
	factors   []expr.Factor
}

func newReader(t *ident.Table) *reader {
	return &reader{r: bufio.NewReader(nil), t: t}
}

func (r *reader) reset(d *dump, s segment) {
	r.r.Reset(io.NewSectionReader(d.f, s.offset, s.length))
}

// term returns the next term of the segment, or io.EOF at its end.
func (r *reader) term() (expr.Term, error) {
	size, err := binary.ReadUvarint(r.r)
	if err != nil {
		return expr.Term{}, err
	}
	r.record = slices.Grow(r.record[:0], int(size))[:size]
	if _, err := io.ReadFull(r.r, r.record); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return expr.Term{}, err
	}
	b := r.record
	n, k := binary.Uvarint(b)
	b = b[k:]
	r.pairs = slices.Grow(r.pairs[:0], int(n))[:n]
	for i := range r.pairs {
		r.pairs[i] = expr.Pair(binary.LittleEndian.Uint64(b))
		b = b[8:]
	}
	n, k = binary.Uvarint(b)
	b = b[k:]
	r.monomials, r.factors = r.monomials[:0], r.factors[:0]
	for range n {
		var m expr.Monomial
		m.Coeff.Re, b = expr.ReadRational(b)
		m.Coeff.Im, b = expr.ReadRational(b)
		f, k := binary.Uvarint(b)
		b = b[k:]
		from := len(r.factors)
		for range f {
			node := r.t.Value(ident.ID(binary.LittleEndian.Uint32(b))).(expr.Expr)
			e, k := binary.Uvarint(b[4:])
			r.factors = append(r.factors, expr.Factor{Node: node, Exp: int32(e)})
			b = b[4+k:]
		}
		m.Opaques = r.factors[from:]
		r.monomials = append(r.monomials, m)
	}
	return expr.Term{Pairs: r.pairs, Coeff: expr.Scalar{Monomials: r.monomials}}, nil
}
