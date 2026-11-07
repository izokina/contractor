// Package expr holds the value types of the contractor pipeline: the Expr
// tree built by the parser and consumed by the writer, the exact arithmetic
// ring (Rational / Complex / Scalar) used by eval to fold coefficients, and
// the conversion helpers that bridge the two.
package expr

import (
	"encoding/json/jsontext"
	"math"
	"math/big"

	"github.com/izokina/contractor/pkg/pipeline/ident"
)

// Expr is a parse-tree node that can serve as a tensor coefficient.
// walk.Walker iterates the Terms it expands to; codec.Writer serializes
// it to JSON.
type Expr interface {
	// Len is the number of terms of the expansion before anything merges,
	// saturating at math.MaxInt. A tensor node counts a scalar child as 1,
	// since the walk takes it as one term.
	Len() int
	IsScalar() bool
}

// Arg is a node as the tree holds it: a Pair inline, with Expr nil, or any
// other node in Expr.
type Arg struct {
	Expr Expr
	Pair Pair
}

func (a Arg) Len() int {
	if a.Expr == nil {
		return 1
	}
	return a.Expr.Len()
}

func (a Arg) IsScalar() bool { return a.Expr != nil && a.Expr.IsScalar() }

// Expands reports whether e is multiplied out rather than taken as one
// opaque factor.
func Expands(e Expr) bool {
	switch n := e.(type) {
	case *PlusExpr, *TimesExpr:
		return true
	case *PowerExpr:
		return n.ExpInt > 0
	case *RationalExpr:
		return n.read
	case *ComplexExpr:
		return n.read
	}
	return false
}

// KeyOf returns the key of an opaque factor, which the parser has keyed.
func KeyOf(e Expr) ident.ID {
	switch n := e.(type) {
	case *Symbol:
		return n.Key
	case *Atom:
		return n.Key
	case *HeadExpr:
		return n.Key
	case *PowerExpr:
		return n.Key
	case *RationalExpr:
		return n.Key
	default:
		return e.(*ComplexExpr).Key
	}
}

// Atom is a number: an int32 in Int, an integer beyond int32 in Big, or the
// raw text of a number that is not an integer in Text. Big and Text are nil
// unless they hold the number.
type Atom struct {
	Int  int32
	Big  *big.Int
	Text jsontext.Value
	Key  ident.ID
}

func (a *Atom) Len() int       { return 1 }
func (a *Atom) IsScalar() bool { return true }

// Symbol is a name, which the ident.Table holds under Key. Each name has one
// Symbol.
type Symbol struct {
	Key ident.ID
}

func (s *Symbol) Len() int       { return 1 }
func (s *Symbol) IsScalar() bool { return true }

// HeadExpr is an expression whose head the contractor does not interpret,
// one opaque scalar factor. Head holds a compound head, such as f[a] in
// f[a][b], and is nil when the head is the symbol Name.
type HeadExpr struct {
	Name ident.ID
	Head Expr
	Args []Arg
	Key  ident.ID
}

func (h *HeadExpr) Len() int       { return 1 }
func (h *HeadExpr) IsScalar() bool { return true }

type Term struct {
	Pairs []Pair
	Coeff Scalar
}

// Dimension is the set of parts of D-dimensional space that a Slot spans:
// Dim4, with no label, DimDMinus4, labelled Plus[-4, D], or both, DimD,
// labelled D. A contraction spans a & b: none for 4 with D-4, which vanishes.
type Dimension uint8

const (
	Dim4 Dimension = 1 << iota
	DimDMinus4
	DimD = Dim4 | DimDMinus4
)

// Slot is an argument of a Pair, a Lorentz index or a momentum, packed into
// one value: the ID of the index's name or of the momentum's source, an index
// bit and the Dimension.
type Slot uint32

const (
	slotIndex = 1 << 2
	slotShift = 3
)

const MaxSlotID = ident.ID(math.MaxUint32 >> slotShift)

func NewSlot(id ident.ID, index bool, dim Dimension) Slot {
	s := Slot(id)<<slotShift | Slot(dim)
	if index {
		s |= slotIndex
	}
	return s
}

func (s Slot) ID() ident.ID { return ident.ID(s >> slotShift) }

func (s Slot) Index() bool { return s&slotIndex != 0 }

func (s Slot) Dim() Dimension { return Dimension(s & (slotIndex - 1)) }

func (s Slot) WithDim(d Dimension) Slot { return s&^(slotIndex-1) | Slot(d) }

// Pair is a metric, a Lorentz vector or a scalar product: its two Slots
// concatenated, the first in the high half, index slots before momenta. The
// slots share their Dimension.
type Pair uint64

func NewPair(a, b Slot) Pair { return Pair(a)<<32 | Pair(b) }

func (p Pair) Slots() [2]Slot { return [2]Slot{Slot(p >> 32), Slot(p)} }

func (p Pair) Indices() int {
	n := 0
	for _, s := range p.Slots() {
		if s.Index() {
			n++
		}
	}
	return n
}

func (p Pair) Dim() Dimension { return Slot(p).Dim() }

type PlusExpr struct {
	Children []Arg
	Key      ident.ID
	len      int
	scalar   bool
}

func (p *PlusExpr) Len() int       { return p.len }
func (p *PlusExpr) IsScalar() bool { return p.scalar }

func NewPlusExpr(children []Arg) *PlusExpr {
	isScalar := allScalar(children)
	totalLen := 0
	for _, c := range children {
		totalLen = addLen(totalLen, ChildLen(c, isScalar))
	}
	return &PlusExpr{Children: children, len: totalLen, scalar: isScalar}
}

// TimesExpr is a product. One that the walk reaches holds the product of its
// numbers in Coeff, and its other factors in Children; one inside an opaque
// factor or a momentum source keeps its factors as written, with Coeff 1.
type TimesExpr struct {
	Coeff    Complex
	Children []Arg
	Key      ident.ID
	len      int
	scalar   bool
}

func (t *TimesExpr) Len() int       { return t.len }
func (t *TimesExpr) IsScalar() bool { return t.scalar }

func NewTimesExpr(coeff Complex, children []Arg) *TimesExpr {
	isScalar := allScalar(children)
	totalLen := 1
	for _, c := range children {
		totalLen = mulLen(totalLen, ChildLen(c, isScalar))
	}
	return &TimesExpr{Coeff: coeff, Children: children, len: totalLen, scalar: isScalar}
}

// PowerExpr holds an int32 exponent in ExpInt, with Exp the zero Arg, and any
// other exponent in Exp, with ExpInt 0. It expands when ExpInt > 0.
type PowerExpr struct {
	Child  Arg
	Exp    Arg
	ExpInt int32
	Key    ident.ID
	len    int
}

func (p *PowerExpr) Len() int       { return p.len }
func (p *PowerExpr) IsScalar() bool { return p.ExpInt <= 0 || p.Child.IsScalar() }

func NewPowerExprInt(child Arg, expInt int32) *PowerExpr {
	totalLen := 1
	if expInt > 0 {
		base := child.Len()
		totalLen = base
		for n := expInt; n > 1 && base > 1 && totalLen < math.MaxInt; n-- {
			totalLen = mulLen(totalLen, base)
		}
	}
	return &PowerExpr{Child: child, ExpInt: expInt, len: totalLen}
}

func NewPowerExpr(child, exp Arg) *PowerExpr {
	return &PowerExpr{Child: child, Exp: exp, len: 1}
}

// RationalExpr represents a Mathematica Rational[num, den]. One that is read
// stands for Num·Den^-1: Den^-1 is a number when Den is one, and otherwise
// the opaque factor Power[Den, -1].
type RationalExpr struct {
	Num, Den Arg
	Key      ident.ID
	read     bool
}

func (r *RationalExpr) Len() int {
	if r.read {
		return r.Num.Len()
	}
	return 1
}

func (r *RationalExpr) IsScalar() bool { return true }

// NewRationalExpr builds Rational[num, den], read unless it is a number, has
// a tensor part or has the denominator 0.
func NewRationalExpr(num, den Arg) *RationalExpr {
	r := &RationalExpr{Num: num, Den: den}
	if isInteger(num.Expr) && isInteger(den.Expr) || !num.IsScalar() || !den.IsScalar() {
		return r
	}
	if d, ok := ComplexFromExpr(den.Expr); ok && d.IsZero() {
		return r
	}
	r.read = true
	return r
}

// ComplexExpr represents a Mathematica Complex[re, im]. One that is read
// stands for Re + I·Im.
type ComplexExpr struct {
	Re, Im Arg
	Key    ident.ID
	read   bool
}

func (c *ComplexExpr) Len() int {
	if c.read {
		return addLen(c.Re.Len(), c.Im.Len())
	}
	return 1
}

func (c *ComplexExpr) IsScalar() bool { return true }

// NewComplexExpr builds Complex[re, im], read unless its parts are integers
// or Rationals of integers, or it has a tensor part.
func NewComplexExpr(re, im Arg) *ComplexExpr {
	c := &ComplexExpr{Re: re, Im: im}
	c.read = !(isRational(re.Expr) && isRational(im.Expr)) && re.IsScalar() && im.IsScalar()
	return c
}

func allScalar(children []Arg) bool {
	for _, c := range children {
		if !c.IsScalar() {
			return false
		}
	}
	return true
}

func ChildLen(c Arg, parentScalar bool) int {
	if !parentScalar && c.IsScalar() {
		return 1
	}
	return c.Len()
}

func addLen(a, b int) int {
	if a > math.MaxInt-b {
		return math.MaxInt
	}
	return a + b
}

func mulLen(a, b int) int {
	if b != 0 && a > math.MaxInt/b {
		return math.MaxInt
	}
	return a * b
}
