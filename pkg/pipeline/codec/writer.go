// Package codec is the JSON I/O layer for the contractor pipeline:
// streaming Expr parser, Writer, and the canonical-signature builder.
package codec

import (
	"encoding/json"
	"encoding/json/jsontext"

	"github.com/izokina/contractor/pkg/literal"
	"github.com/izokina/contractor/pkg/pipeline/expr"
)

// Writer renders contractor values as Mathematica ExpressionJSON.
// WriteExpr emits any Expr as a fixed-shape JSON value; WriteTerm renders
// a Term's synthetic Times/Plus cascade with the flatten and
// identity-collapse needed to produce canonical output.
type Writer struct {
	enc *jsontext.Encoder
}

func NewWriter(enc *jsontext.Encoder) *Writer {
	return &Writer{enc: enc}
}

// WriteExpr emits n as a self-contained JSON value with no flatten and no
// identity collapse. Children recurse via writeExpr. Canonical Mathematica
// never nests Times in Times or Plus in Plus, so straight emission is the
// canonical form.
func (w *Writer) WriteExpr(n expr.Expr) (err error) {
	defer recoverWrapped(&err)
	w.writeExpr(n)
	return nil
}

// WriteTerm emits a Term as the cascade
//
//	Term = Times[Coeff_factors..., Pair...]
//
// Each level picks 0-identity / 1-bare / N-array from a count computed
// up-front.
func (w *Writer) WriteTerm(t expr.Term) (err error) {
	defer recoverWrapped(&err)

	if len(t.Coeff.Monomials) == 1 {
		w.writeMonomial(t.Coeff.Monomials[0], t.Pairs...)
		return nil
	}
	if len(t.Pairs) == 0 {
		w.writeScalar(t.Coeff)
		return nil
	}

	w.openArray(literal.Times)
	w.writeScalar(t.Coeff)
	for _, p := range t.Pairs {
		w.writeExpr(p)
	}
	w.writeToken(jsontext.EndArray)
	return nil
}

func (w *Writer) writeExpr(n expr.Expr) {
	switch v := n.(type) {
	case *expr.Atom:
		w.writeAtom(v)
	case expr.Pair:
		w.writePair(v)
	case *expr.PowerExpr:
		w.writePower(v)
	case *expr.RationalExpr:
		w.writeArray(literal.Rational, v.Num, v.Den)
	case *expr.ComplexExpr:
		w.writeArray(literal.Complex, v.Re, v.Im)
	case *expr.TimesExpr:
		w.writeArray(literal.Times, v.Children...)
	case *expr.PlusExpr:
		w.writeArray(literal.Plus, v.Children...)
	default:
		panicf("writer: WriteExpr received unrecognized Expr %T", n)
	}
}

func (w *Writer) writeScalar(s expr.Scalar) {
	if len(s.Monomials) == 0 {
		w.writeToken(jsontext.Int(0))
		return
	}
	if len(s.Monomials) == 1 {
		w.writeMonomial(s.Monomials[0])
		return
	}

	w.openArray(literal.Plus)
	for _, m := range s.Monomials {
		w.writeMonomial(m)
	}
	w.writeToken(jsontext.EndArray)
}

// extraPairs is supplied only when inlining a single-Monomial Coeff
// alongside Pairs at the Term outer level.
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
			w.writeExpr(m.Opaques[0].Val)
			return
		}
		w.writeExpr(extraPairs[0])
		return
	}

	w.openArray(literal.Times)
	if m.Coeff != expr.OneComplex {
		w.writeComplex(m.Coeff)
	}
	for _, o := range m.Opaques {
		w.writeExpr(o.Val)
	}
	for _, p := range extraPairs {
		w.writeExpr(p)
	}
	w.writeToken(jsontext.EndArray)
}

func (w *Writer) openArray(name string) {
	w.writeToken(jsontext.BeginArray)
	w.writeToken(jsontext.String(name))
}

func (w *Writer) writeAtom(a *expr.Atom) {
	switch v := a.Value.(type) {
	case string:
		w.writeToken(jsontext.String(v))
	case int32:
		w.writeToken(jsontext.Int(int64(v)))
	case int64:
		w.writeToken(jsontext.Int(v))
	case jsontext.Value:
		w.writeValue(v)
	default:
		b, err := json.Marshal(v)
		wrap(err)
		w.writeValue(jsontext.Value(b))
	}
}

func (w *Writer) writePower(p *expr.PowerExpr) {
	if p.Exp != nil {
		w.writeArray(literal.Power, p.Child, p.Exp)
		return
	}

	w.openArray(literal.Power)
	w.writeExpr(p.Child)
	w.writeToken(jsontext.Int(int64(p.ExpInt)))
	w.writeToken(jsontext.EndArray)
}

func (w *Writer) writeComplex(z expr.Complex) {
	if z.Im.Num == 0 {
		w.writeRational(z.Re)
		return
	}

	w.openArray(literal.Complex)
	w.writeRational(z.Re)
	w.writeRational(z.Im)
	w.writeToken(jsontext.EndArray)
}

func (w *Writer) writeRational(r expr.Rational) {
	if r.Den == 1 {
		w.writeToken(jsontext.Int(int64(r.Num)))
		return
	}

	w.openArray(literal.Rational)
	w.writeToken(jsontext.Int(int64(r.Num)))
	w.writeToken(jsontext.Int(int64(r.Den)))
	w.writeToken(jsontext.EndArray)
}

func (w *Writer) writePair(p expr.Pair) {
	w.openArray(literal.Pair)
	for _, l := range p.Lorentz {
		w.writeLorentzIndex(l)
	}
	for _, m := range p.Momentum {
		w.writeMomentum(m)
	}
	w.writeToken(jsontext.EndArray)
}

func (w *Writer) writeLorentzIndex(l expr.LorentzIndex) {
	w.openArray(literal.LorentzIndex)
	w.writeToken(jsontext.String(l.Index))
	if l.HasD {
		w.writeToken(jsontext.String("D"))
	}
	w.writeToken(jsontext.EndArray)
}

func (w *Writer) writeMomentum(m expr.Momentum) {
	w.openArray(literal.Momentum)
	w.writeExpr(m.Source)
	if m.HasD {
		w.writeToken(jsontext.String("D"))
	}
	w.writeToken(jsontext.EndArray)
}

func (w *Writer) writeArray(name string, nodes ...expr.Expr) {
	w.openArray(name)
	for _, n := range nodes {
		w.writeExpr(n)
	}
	w.writeToken(jsontext.EndArray)
}

func (w *Writer) writeToken(t jsontext.Token) {
	wrap(w.enc.WriteToken(t))
}

func (w *Writer) writeValue(v jsontext.Value) {
	wrap(w.enc.WriteValue(v))
}
