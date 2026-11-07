// Package codec is the JSON I/O layer for the contractor pipeline: the
// streaming Expr parser, which keys what it parses, and the Writer.
package codec

import (
	"encoding/json/jsontext"
	"math/big"
	"slices"

	"github.com/izokina/contractor/pkg/pipeline/expr"
	"github.com/izokina/contractor/pkg/pipeline/ident"
)

// Writer renders contractor values as Mathematica ExpressionJSON, with the
// names and momentum sources that t holds.
type Writer struct {
	enc     *jsontext.Encoder
	t       *ident.Table
	scratch []byte // reused by writeBigInt; makes Writer single-goroutine

	addends int
	pairs   []expr.Pair
	held    []expr.Monomial
	open    bool
}

func NewWriter(enc *jsontext.Encoder, t *ident.Table) *Writer {
	return &Writer{enc: enc, t: t}
}

// Add writes the monomial m of the term whose Pairs are pairs. The output is
// a sum, 0 for no addends, the bare addend for one and Plus[addend...]
// otherwise: each pair-free monomial is an addend, and so is each tensor
// term, its monomials given one after another. The first addend is held
// until a second one begins, and each later one until its second monomial,
// so neither pairs nor m may change until Close.
func (w *Writer) Add(pairs []expr.Pair, m expr.Monomial) (err error) {
	defer recoverWrapped(&err)

	if w.addends == 0 || len(pairs) == 0 || !slices.Equal(pairs, w.pairs) {
		if w.addends == 1 {
			w.openArray(headPlus)
		}
		if w.addends > 0 {
			w.endAddend()
		}
		w.addends++
		w.pairs = pairs
	}
	if w.open {
		w.writeMonomial(m)
		return nil
	}
	if w.held = append(w.held, m); w.addends > 1 && len(w.held) == 2 {
		w.openTimes()
	}
	return nil
}

func (w *Writer) Close() (err error) {
	defer recoverWrapped(&err)

	switch w.addends {
	case 0:
		w.writeToken(jsontext.Int(0))
	case 1:
		w.endAddend()
	default:
		w.endAddend()
		w.writeToken(jsontext.EndArray)
	}
	return nil
}

func (w *Writer) openTimes() {
	w.openArray(headTimes)
	w.openArray(headPlus)
	for _, m := range w.held {
		w.writeMonomial(m)
	}
	clear(w.held)
	w.held, w.open = w.held[:0], true
}

// endAddend writes the rest of the current addend: a single monomial with the
// Pairs among its factors, or the end of Times[Plus[...], pair...].
func (w *Writer) endAddend() {
	if len(w.held) > 1 {
		w.openTimes()
	}
	if !w.open {
		w.writeMonomial(w.held[0], w.pairs...)
		clear(w.held)
		w.held = w.held[:0]
		return
	}
	w.writeToken(jsontext.EndArray)
	for _, p := range w.pairs {
		w.writePair(p)
	}
	w.writeToken(jsontext.EndArray)
	w.open = false
}

// writeExpr emits n as a fixed-shape JSON value, with no flatten and no
// identity collapse. Canonical Mathematica never nests Times in Times or Plus
// in Plus, so straight emission is the canonical form.
func (w *Writer) writeExpr(n expr.Expr) {
	switch v := n.(type) {
	case *expr.Symbol:
		w.writeToken(jsontext.String(w.t.Name(v.Key)))
	case *expr.Atom:
		w.writeAtom(v)
	case *expr.HeadExpr:
		w.writeHead(v)
	case *expr.PowerExpr:
		w.writePower(v)
	case *expr.RationalExpr:
		w.writeArray(headRational, v.Num, v.Den)
	case *expr.ComplexExpr:
		w.writeArray(headComplex, v.Re, v.Im)
	case *expr.TimesExpr:
		w.writeArray(headTimes, v.Children...)
	case *expr.PlusExpr:
		w.writeArray(headPlus, v.Children...)
	default:
		panicf("writer: unrecognized Expr %T", n)
	}
}

// writeMonomial writes Times[coefficient unless 1, factor..., extraPair...],
// picking 1, the bare item or Times from their count. extraPairs are the
// Pairs of an addend that is this one monomial.
func (w *Writer) writeMonomial(m expr.Monomial, extraPairs ...expr.Pair) {
	count := len(m.Opaques) + len(extraPairs)
	if m.Coeff != expr.OneComplex {
		count++
	}

	if count == 0 {
		w.writeToken(jsontext.Int(1))
		return
	}
	if count == 1 {
		if m.Coeff != expr.OneComplex {
			w.writeComplex(m.Coeff)
			return
		}
		if len(m.Opaques) == 1 {
			w.writeFactor(m.Opaques[0])
			return
		}
		w.writePair(extraPairs[0])
		return
	}

	w.openArray(headTimes)
	if m.Coeff != expr.OneComplex {
		w.writeComplex(m.Coeff)
	}
	for _, o := range m.Opaques {
		w.writeFactor(o)
	}
	for _, p := range extraPairs {
		w.writePair(p)
	}
	w.writeToken(jsontext.EndArray)
}

func (w *Writer) writeFactor(f expr.Factor) {
	if f.Exp == 1 {
		w.writeExpr(f.Node)
		return
	}
	w.openArray(headPower)
	w.writeExpr(f.Node)
	w.writeToken(jsontext.Int(int64(f.Exp)))
	w.writeToken(jsontext.EndArray)
}

func (w *Writer) openArray(name string) {
	w.writeToken(jsontext.BeginArray)
	w.writeToken(jsontext.String(name))
}

func (w *Writer) writeAtom(a *expr.Atom) {
	switch {
	case a.Big != nil:
		w.writeBigInt(a.Big)
	case a.Text != nil:
		w.writeValue(a.Text)
	default:
		w.writeToken(jsontext.Int(int64(a.Int)))
	}
}

func (w *Writer) writeHead(h *expr.HeadExpr) {
	w.writeToken(jsontext.BeginArray)
	if h.Head != nil {
		w.writeExpr(h.Head)
	} else {
		w.writeToken(jsontext.String(w.t.Name(h.Name)))
	}
	for _, a := range h.Args {
		w.writeArg(a)
	}
	w.writeToken(jsontext.EndArray)
}

func (w *Writer) writePower(p *expr.PowerExpr) {
	if p.Exp != (expr.Arg{}) {
		w.writeArray(headPower, p.Child, p.Exp)
		return
	}

	w.openArray(headPower)
	w.writeArg(p.Child)
	w.writeToken(jsontext.Int(int64(p.ExpInt)))
	w.writeToken(jsontext.EndArray)
}

func (w *Writer) writeComplex(z expr.Complex) {
	if z.Im.IsZero() {
		w.writeRational(z.Re)
		return
	}

	w.openArray(headComplex)
	w.writeRational(z.Re)
	w.writeRational(z.Im)
	w.writeToken(jsontext.EndArray)
}

func (w *Writer) writeRational(r expr.Rational) {
	if q := r.Big(); q != nil {
		if q.IsInt() {
			w.writeBigInt(q.Num())
			return
		}
		w.openArray(headRational)
		w.writeBigInt(q.Num())
		w.writeBigInt(q.Denom())
		w.writeToken(jsontext.EndArray)
		return
	}

	if r.Den() == 1 {
		w.writeToken(jsontext.Int(int64(r.Num())))
		return
	}

	w.openArray(headRational)
	w.writeToken(jsontext.Int(int64(r.Num())))
	w.writeToken(jsontext.Int(int64(r.Den())))
	w.writeToken(jsontext.EndArray)
}

func (w *Writer) writeBigInt(x *big.Int) {
	w.scratch = x.Append(w.scratch[:0], 10)
	w.writeValue(jsontext.Value(w.scratch))
}

func (w *Writer) writePair(p expr.Pair) {
	w.openArray(headPair)
	for _, s := range p.Slots() {
		if s.Index() {
			w.openArray(headLorentzIndex)
			w.writeToken(jsontext.String(w.t.Name(s.ID())))
		} else {
			w.openArray(headMomentum)
			w.writeArg(w.t.Value(s.ID()).(expr.Arg))
		}
		w.writeDimension(s.Dim())
		w.writeToken(jsontext.EndArray)
	}
	w.writeToken(jsontext.EndArray)
}

func (w *Writer) writeDimension(d expr.Dimension) {
	switch d {
	case expr.DimD:
		w.writeToken(jsontext.String(DimensionD))
	case expr.DimDMinus4:
		w.openArray(headPlus)
		w.writeToken(jsontext.Int(-4))
		w.writeToken(jsontext.String(DimensionD))
		w.writeToken(jsontext.EndArray)
	}
}

func (w *Writer) writeArray(name string, args ...expr.Arg) {
	w.openArray(name)
	for _, a := range args {
		w.writeArg(a)
	}
	w.writeToken(jsontext.EndArray)
}

func (w *Writer) writeArg(a expr.Arg) {
	if a.Expr == nil {
		w.writePair(a.Pair)
		return
	}
	w.writeExpr(a.Expr)
}

func (w *Writer) writeToken(t jsontext.Token) {
	wrap(w.enc.WriteToken(t))
}

func (w *Writer) writeValue(v jsontext.Value) {
	wrap(w.enc.WriteValue(v))
}
