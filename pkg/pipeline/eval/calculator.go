// Package eval folds a scalar Expr into a Scalar, multiplies Scalars, and
// merges monomials and terms by their words of factors.
package eval

import (
	"math"
	"slices"

	"github.com/izokina/contractor/pkg/pipeline/expr"
)

// Calculator multiplies Scalars. Not safe for concurrent use.
type Calculator struct {
	sum *Sum
	ops []expr.Factor
}

func NewCalculator() *Calculator {
	return &Calculator{sum: NewSum()}
}

// Buffer holds a product that MulTo writes. The zero value is ready to use.
type Buffer struct {
	monomials []expr.Monomial
	factors   []expr.Factor
}

// Mul returns a * b, which may share slices with a and b.
func (c *Calculator) Mul(a, b expr.Scalar) expr.Scalar {
	var buf Buffer
	return c.MulTo(&buf, a, b)
}

// MulTo returns a * b, which may share slices with a and b, and may lie in
// buf, valid until buf is written again. It writes only into buf, where
// neither a nor b may lie.
func (c *Calculator) MulTo(buf *Buffer, a, b expr.Scalar) expr.Scalar {
	switch {
	case isOne(a):
		return b
	case isOne(b):
		return a
	case len(b.Monomials) == 1:
		return buf.scale(a, b.Monomials[0])
	case len(a.Monomials) == 1:
		return buf.scale(b, a.Monomials[0])
	}
	for _, ma := range a.Monomials {
		for _, mb := range b.Monomials {
			c.ops = mergeOpaques(c.ops[:0], ma.Opaques, mb.Opaques)
			c.sum.AddSorted(ma.Coeff.Mul(mb.Coeff), c.ops)
		}
	}
	return c.sum.Scalar()
}

// scale multiplies each monomial of s by f. Below the bound of mergeOpaques
// the words of s's monomials stay distinct, and no coefficient becomes zero,
// so nothing merges; past it two words can meet, and the Sum that takes the
// result merges them.
func (buf *Buffer) scale(s expr.Scalar, f expr.Monomial) expr.Scalar {
	buf.monomials = slices.Grow(buf.monomials[:0], len(s.Monomials))
	buf.factors = buf.factors[:0]
	for _, m := range s.Monomials {
		buf.monomials = append(buf.monomials, expr.Monomial{Coeff: m.Coeff.Mul(f.Coeff), Opaques: buf.merged(m.Opaques, f.Opaques)})
	}
	return expr.Scalar{Monomials: buf.monomials}
}

func isOne(s expr.Scalar) bool {
	return len(s.Monomials) == 1 && s.Monomials[0].Coeff == expr.OneComplex && len(s.Monomials[0].Opaques) == 0
}

func (buf *Buffer) merged(a, b []expr.Factor) []expr.Factor {
	switch {
	case len(b) == 0:
		return a
	case len(a) == 0:
		return b
	}
	from := len(buf.factors)
	buf.factors = mergeOpaques(slices.Grow(buf.factors, len(a)+len(b)), a, b)
	return buf.factors[from:]
}

// mergeOpaques appends the product of the factors of a and b, each in key
// order, to dst in key order: the exponents of a node add, unless their sum
// would pass the int32 range, where the two stay apart.
func mergeOpaques(dst, a, b []expr.Factor) []expr.Factor {
	for len(a) != 0 && len(b) != 0 {
		ka, kb := expr.KeyOf(a[0].Node), expr.KeyOf(b[0].Node)
		switch {
		case kb < ka:
			dst, b = append(dst, b[0]), b[1:]
		case ka == kb && a[0].Exp <= math.MaxInt32-b[0].Exp:
			dst = append(dst, expr.Factor{Node: a[0].Node, Exp: a[0].Exp + b[0].Exp})
			a, b = a[1:], b[1:]
		default:
			dst, a = append(dst, a[0]), a[1:]
		}
	}
	return append(append(dst, a...), b...)
}
