package eval

import (
	"math"

	"github.com/izokina/contractor/pkg/pipeline/expr"
)

// Folder computes the value of a scalar node of a walked summand (codec's
// Parser.walked). There the only nodes that expr.Expands expands are sums,
// products and positive integer powers, and any other node is the one node of
// its value. Not safe for concurrent use.
type Folder struct {
	calc *Calculator
	sum  *Sum
	held []expr.Scalar
}

func NewFolder() *Folder {
	return &Folder{calc: NewCalculator(), sum: NewSum()}
}

func (f *Folder) Fold(e expr.Expr) expr.Scalar {
	if !expr.Expands(e) {
		return expr.Scalar{Monomials: []expr.Monomial{{Coeff: expr.OneComplex, Opaques: []expr.Factor{{Node: e, Exp: 1}}}}}
	}
	switch n := e.(type) {
	case *expr.PlusExpr:
		// Folding an addend may use held and the Sum, so every addend is
		// folded before any is added.
		from := len(f.held)
		for _, c := range n.Children {
			v := f.Fold(c.Expr)
			f.held = append(f.held, v)
		}
		for _, v := range f.held[from:] {
			for _, m := range v.Monomials {
				f.sum.AddSorted(m.Coeff, m.Opaques)
			}
		}
		clear(f.held[from:])
		f.held = f.held[:from]
		return f.sum.Scalar()
	case *expr.TimesExpr:
		v := expr.ScalarOf(n.Coeff)
		for _, c := range n.Children {
			if len(v.Monomials) == 0 {
				break
			}
			v = f.calc.Mul(v, f.Fold(c.Expr))
		}
		return v
	default:
		p := n.(*expr.PowerExpr)
		base := f.Fold(p.Child.Expr)
		if len(base.Monomials) == 1 {
			if m, ok := power(base.Monomials[0], p.ExpInt); ok {
				return expr.Scalar{Monomials: []expr.Monomial{m}}
			}
		}
		v := base
		for i := int32(1); i < p.ExpInt && len(v.Monomials) != 0; i++ {
			v = f.calc.Mul(v, base)
		}
		return v
	}
}

// power returns m to the n, unless an exponent would pass the int32 range.
func power(m expr.Monomial, n int32) (expr.Monomial, bool) {
	out := make([]expr.Factor, len(m.Opaques))
	for i, x := range m.Opaques {
		if x.Exp > math.MaxInt32/n {
			return expr.Monomial{}, false
		}
		out[i] = expr.Factor{Node: x.Node, Exp: x.Exp * n}
	}
	return expr.Monomial{Coeff: m.Coeff.Pow(n), Opaques: out}, true
}
