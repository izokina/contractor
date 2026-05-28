// Package eval folds an Expr tree into a Scalar by walking the expanded
// sum-of-monomials and grouping like terms by canonical opaque-slice
// signature. Scalar arithmetic is performed in the int32-bounded
// Rational/Complex ring with overflow recovery via atomization.
package eval

import (
	"bytes"
	"cmp"
	"slices"
	"unsafe"

	"github.com/izokina/contractor/pkg/pipeline/codec"
	"github.com/izokina/contractor/pkg/pipeline/expr"
)

// Calculator combines two Scalars term-by-term, grouping by canonical
// opaque-slice signature and folding coefficients in the Complex ring.
// When a Complex operation overflows, recovery atomizes one coefficient as
// an Opaque (appended to opBuf) and the step is reattempted; the algebraic
// value is preserved because the Opaque stands in for that exact Complex.
// Methods reuse internal scratch buffers; not safe for concurrent use.
type Calculator struct {
	groups map[string]*expr.Monomial
	keys   []string
	opBuf  []expr.Opaque
	sigBuf bytes.Buffer
	signer codec.Signer
}

// NewCalculator returns an empty Calculator.
func NewCalculator() *Calculator {
	return &Calculator{groups: make(map[string]*expr.Monomial)}
}

// Add returns a + b.
func (c *Calculator) Add(a, b expr.Scalar) expr.Scalar {
	c.reset()
	c.foldMonomials(a.Monomials)
	c.foldMonomials(b.Monomials)
	return c.materialize()
}

// Mul returns a * b.
func (c *Calculator) Mul(a, b expr.Scalar) expr.Scalar {
	c.reset()
	for _, ma := range a.Monomials {
		for _, mb := range b.Monomials {
			c.opBuf = c.opBuf[:0]
			c.opBuf = append(c.opBuf, ma.Opaques...)
			c.opBuf = append(c.opBuf, mb.Opaques...)
			coeff, ok := ma.Coeff.Mul(mb.Coeff)
			if !ok {
				c.opBuf = append(c.opBuf, c.atomizeComplex(mb.Coeff))
				coeff = ma.Coeff
			}
			c.sortOpBuf()
			c.foldBuf(coeff)
		}
	}
	return c.materialize()
}

func (c *Calculator) reset() {
	clear(c.groups)
}

func (c *Calculator) foldMonomials(monomials []expr.Monomial) {
	for _, m := range monomials {
		c.opBuf = append(c.opBuf[:0], m.Opaques...)
		c.sortOpBuf()
		c.foldBuf(m.Coeff)
	}
}

// foldBuf folds (coeff, c.opBuf) into c.groups, summing coefficients on
// signature collision. On addition overflow, the incoming coeff is atomized
// into c.opBuf and the fold is reattempted with coefficient 1+0i; the new
// signature differs from the colliding one, so the recursion terminates.
// Caller must have sorted c.opBuf.
//
// signature() returns an unsafe view of c.sigBuf valid only until the next
// mutation; the lookup must finish before any further write to c.sigBuf.
// On insert we materialise a real string copy as the map key.
func (c *Calculator) foldBuf(coeff expr.Complex) {
	sig := c.signature()
	if existing, ok := c.groups[sig]; ok {
		sum, ok := existing.Coeff.Add(coeff)
		if !ok {
			c.opBuf = append(c.opBuf, c.atomizeComplex(coeff))
			c.sortOpBuf()
			c.foldBuf(expr.OneComplex)
			return
		}
		existing.Coeff = sum
		return
	}
	ops := append(make([]expr.Opaque, 0, len(c.opBuf)), c.opBuf...)
	c.groups[string(c.sigBuf.Bytes())] = &expr.Monomial{Coeff: coeff, Opaques: ops}
}

func (c *Calculator) sortOpBuf() {
	slices.SortFunc(c.opBuf, func(a, b expr.Opaque) int {
		return cmp.Compare(a.Signature, b.Signature)
	})
}

func (c *Calculator) signature() string {
	c.sigBuf.Reset()
	for i, o := range c.opBuf {
		if i > 0 {
			c.sigBuf.WriteByte('\x00')
		}
		c.sigBuf.WriteString(o.Signature)
	}
	b := c.sigBuf.Bytes()
	return unsafe.String(unsafe.SliceData(b), len(b))
}

func (c *Calculator) materialize() expr.Scalar {
	c.keys = c.keys[:0]
	for k := range c.groups {
		c.keys = append(c.keys, k)
	}
	slices.Sort(c.keys)
	out := make([]expr.Monomial, 0, len(c.keys))
	for _, k := range c.keys {
		g := c.groups[k]
		if g.Coeff.IsZero() {
			continue
		}
		out = append(out, *g)
	}
	return expr.Scalar{Monomials: out}
}

// atomizeComplex wraps an expr.Complex value as an Opaque whose Val is the
// matching Expr (Atom / RationalExpr / ComplexExpr) and whose Signature is
// the Expr's canonical encoding via the embedded Signer.
func (c *Calculator) atomizeComplex(z expr.Complex) expr.Opaque {
	n := expr.ComplexToExpr(z)
	return expr.Opaque{
		Signature: c.signer.Expr(n),
		Val:       n,
	}
}
