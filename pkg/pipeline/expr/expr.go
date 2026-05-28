// Package expr holds the value types of the contractor pipeline: the Expr
// tree built by the parser and consumed by the writer, the int32-bounded
// arithmetic ring (Rational / Complex / Scalar) used by the merger to fold
// coefficients, and the conversion helpers that bridge the two.
package expr

import (
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"strconv"
	"unsafe"
)

// Expr is a parse-tree node that can serve as a tensor coefficient.
// walk.Walker iterates the Terms it expands to; codec.Writer serializes
// it to JSON.
type Expr interface {
	Len() int
	IsScalar() bool
}

// Atom is an opaque, indivisible scalar factor.
// Value holds the original Go value; Signature is its canonical string key for comparison.
type Atom struct {
	Value     any
	Signature string
}

func (a *Atom) Len() int       { return 1 }
func (a *Atom) IsScalar() bool { return true }

// NewAtom wraps a raw scalar value as an Atom with a precomputed signature.
// Accepted types: int32, int64, string, jsontext.Value, []any.
func NewAtom(value any) *Atom {
	return &Atom{Value: value, Signature: atomSignature(value)}
}

type Term struct {
	Pairs []Pair
	Coeff Scalar
}

type LorentzIndex struct {
	Index     string
	HasD      bool
	Signature string
}

type Momentum struct {
	Source    Expr
	HasD      bool
	Signature string
}

type Pair struct {
	Lorentz  []LorentzIndex
	Momentum []Momentum
}

func (p Pair) Len() int       { return 1 }
func (p Pair) IsScalar() bool { return false }

type PlusExpr struct {
	Children []Expr
	len      int
	scalar   bool
}

func (p *PlusExpr) Len() int       { return p.len }
func (p *PlusExpr) IsScalar() bool { return p.scalar }

func NewPlusExpr(children []Expr) *PlusExpr {
	isScalar, totalLen := true, 0
	for _, c := range children {
		isScalar = isScalar && c.IsScalar()
		totalLen += c.Len()
	}
	return &PlusExpr{Children: children, len: totalLen, scalar: isScalar}
}

type TimesExpr struct {
	Children []Expr
	len      int
	scalar   bool
}

func (t *TimesExpr) Len() int       { return t.len }
func (t *TimesExpr) IsScalar() bool { return t.scalar }

func NewTimesExpr(children []Expr) *TimesExpr {
	isScalar, totalLen := true, 1
	for _, c := range children {
		isScalar = isScalar && c.IsScalar()
		totalLen *= c.Len()
	}
	return &TimesExpr{Children: children, len: totalLen, scalar: isScalar}
}

type PowerExpr struct {
	Child  Expr
	Exp    Expr  // nil when the exponent was parsed as an int32 atom; ExpInt holds the value
	ExpInt int32 // valid when Exp is nil
	len    int
}

func (p *PowerExpr) Len() int       { return p.len }
func (p *PowerExpr) IsScalar() bool { return p.ExpInt <= 0 || p.Child.IsScalar() }

// NewPowerExprInt builds a PowerExpr whose exponent is a cached positive
// int32. The Walker will expand it as the Cartesian product of n copies of
// child; the precomputed len reflects that expansion (or 1 when n <= 0).
func NewPowerExprInt(child Expr, expInt int32) *PowerExpr {
	totalLen := 1
	if expInt > 0 {
		for range int(expInt) {
			totalLen *= child.Len()
		}
	}
	return &PowerExpr{Child: child, ExpInt: expInt, len: totalLen}
}

// NewPowerExpr builds a PowerExpr whose exponent is a non-int32 Expr (zero,
// negative, symbolic, or any non-int32 atom). The Walker treats it as
// opaque, so len is 1.
func NewPowerExpr(child Expr, exp Expr) *PowerExpr {
	return &PowerExpr{Child: child, Exp: exp, len: 1}
}

// RationalExpr represents a Mathematica Rational[num, den] (exact fraction).
type RationalExpr struct{ Num, Den Expr }

func (r *RationalExpr) Len() int       { return 1 }
func (r *RationalExpr) IsScalar() bool { return true }

// ComplexExpr represents a Mathematica Complex[re, im] (exact complex number).
type ComplexExpr struct{ Re, Im Expr }

func (c *ComplexExpr) Len() int       { return 1 }
func (c *ComplexExpr) IsScalar() bool { return true }

// atomSignature renders value as a canonical string key. Mirrors the leaf
// branches of codec.Writer for Atom values, but skips JSON quoting on
// strings — the result is used for map keys and comparisons, not as JSON.
//
// The unsafe.String on the []any branch is deliberate zero-copy: the byte
// slice is freshly allocated by json.Marshal and never shared.
func atomSignature(value any) string {
	switch v := value.(type) {
	case int32:
		return strconv.FormatInt(int64(v), 10)
	case int64:
		return strconv.FormatInt(v, 10)
	case string:
		return v
	case jsontext.Value:
		return string(v)
	case []any:
		b, err := json.Marshal(v)
		if err != nil {
			panic(fmt.Sprintf("expr: failed to marshal atom: %v", err))
		}
		return unsafe.String(unsafe.SliceData(b), len(b))
	default:
		panic(fmt.Sprintf("expr: unknown atom type %T", value))
	}
}
