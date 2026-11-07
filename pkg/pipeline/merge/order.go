package merge

import (
	"bytes"
	"cmp"
	"slices"

	"github.com/izokina/contractor/pkg/pipeline/expr"
	"github.com/izokina/contractor/pkg/pipeline/ident"
)

// The output order compares content only, never keys, which decide identity.
const (
	rankInteger = iota
	rankNumber
	rankSymbol
	rankRational
	rankComplex
	rankPower
	rankTimes
	rankPlus
	rankPair
	rankUnknown
)

type order struct {
	t *ident.Table
}

func (o order) sort(t expr.Term) {
	for i, p := range t.Pairs {
		t.Pairs[i] = o.oriented(p)
	}
	slices.SortFunc(t.Pairs, o.comparePair)
	// The factors of one node, which only the int32 bound on exponents keeps
	// apart, keep their order, so that the monomials' lists, distinct in key
	// order, stay distinct, and the monomial order is total.
	for _, m := range t.Coeff.Monomials {
		slices.SortStableFunc(m.Opaques, o.compareNode)
	}
	slices.SortFunc(t.Coeff.Monomials, o.compareMonomial)
}

func (o order) oriented(p expr.Pair) expr.Pair {
	if s := p.Slots(); p.Indices() != 1 && o.compareSlot(s[0], s[1]) > 0 {
		return expr.NewPair(s[1], s[0])
	}
	return p
}

// firstPair returns the first of pairs in output order, oriented, or 0, the
// end of the Pairs, which no Pair is.
func (o order) firstPair(pairs []expr.Pair) uint64 {
	var first expr.Pair
	for i, p := range pairs {
		if p = o.oriented(p); i == 0 || o.comparePair(p, first) < 0 {
			first = p
		}
	}
	return uint64(first)
}

func (o order) compareMember(factors bool, a, b uint64) int {
	switch {
	case a == 0 || b == 0:
		return cmp.Compare(a, b)
	case factors:
		fa, la := o.memberFactor(a)
		fb, lb := o.memberFactor(b)
		return o.compareFactor(fa, la, fb, lb)
	default:
		return o.comparePair(expr.Pair(a), expr.Pair(b))
	}
}

// compareAddends orders the addends of the output. Tensor terms have distinct
// Pair lists, so only pair-free addends, one monomial each, tie on them.
func (o order) compareAddends(a, b expr.Term) int {
	if c := slices.CompareFunc(a.Pairs, b.Pairs, o.comparePair); c != 0 {
		return c
	}
	return o.compareMonomial(a.Coeff.Monomials[0], b.Coeff.Monomials[0])
}

func (o order) compareMonomial(a, b expr.Monomial) int {
	x, y := a.Opaques, b.Opaques
	for i := range min(len(x), len(y)) {
		if c := o.compareFactor(x[i], i == len(x)-1, y[i], i == len(y)-1); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(x), len(y))
}

// compareFactor compares a and b, at one position of two monomials, which
// they end if aLast and bLast are set, as the copies they stand for, x^n as n
// copies of x: below the int32 bound on exponents the next factor of a
// monomial ranks after x, and its end before x. So of two powers of x, one
// that ends its monomial comes first, two that do by exponent ascending, and
// two that do not descending. Past the bound the next factor may be x again,
// and this order, still total, is no longer that of the copies.
func (o order) compareFactor(a expr.Factor, aLast bool, b expr.Factor, bLast bool) int {
	if c := o.compareNode(a, b); c != 0 {
		return c
	}
	switch {
	case aLast != bLast:
		if aLast {
			return -1
		}
		return 1
	case aLast:
		return cmp.Compare(a.Exp, b.Exp)
	default:
		return cmp.Compare(b.Exp, a.Exp)
	}
}

func (o order) compareNode(a, b expr.Factor) int {
	return o.compareExpr(a.Node, b.Node)
}

// factorMember returns the member of a word at the factor f, which ends the
// word if last is set: f's key, which is never 0, whether it ends the word,
// and its exponent, which is positive.
func factorMember(f expr.Factor, last bool) uint64 {
	m := uint64(expr.KeyOf(f.Node))<<32 | uint64(f.Exp)
	if last {
		m |= 1 << 31
	}
	return m
}

func (o order) memberFactor(m uint64) (expr.Factor, bool) {
	return expr.Factor{Node: o.t.Value(ident.ID(m >> 32)).(expr.Expr), Exp: int32(m & (1<<31 - 1))}, m&(1<<31) != 0
}

// comparePair orders metrics, then vectors, then scalar products, each by
// its slots.
func (o order) comparePair(a, b expr.Pair) int {
	if c := cmp.Compare(b.Indices(), a.Indices()); c != 0 {
		return c
	}
	x, y := a.Slots(), b.Slots()
	for i := range x {
		if c := o.compareSlot(x[i], y[i]); c != 0 {
			return c
		}
	}
	return 0
}

func (o order) compareSlot(a, b expr.Slot) int {
	var c int
	if a.Index() {
		c = cmp.Compare(o.t.Name(a.ID()), o.t.Name(b.ID()))
	} else {
		c = o.compareArg(o.t.Value(a.ID()).(expr.Arg), o.t.Value(b.ID()).(expr.Arg))
	}
	if c != 0 {
		return c
	}
	return cmp.Compare(dimensionRank[a.Dim()], dimensionRank[b.Dim()])
}

var dimensionRank = [...]int{expr.Dim4: 0, expr.DimD: 1, expr.DimDMinus4: 2}

func (o order) compareArg(a, b expr.Arg) int {
	switch {
	case a.Expr != nil && b.Expr != nil:
		return o.compareExpr(a.Expr, b.Expr)
	case a.Expr != nil:
		return cmp.Compare(exprRank(a.Expr), rankPair)
	case b.Expr != nil:
		return cmp.Compare(rankPair, exprRank(b.Expr))
	default:
		return o.comparePair(a.Pair, b.Pair)
	}
}

func (o order) compareExpr(a, b expr.Expr) int {
	if c := cmp.Compare(exprRank(a), exprRank(b)); c != 0 {
		return c
	}
	switch x := a.(type) {
	case *expr.Atom:
		return compareAtom(x, b.(*expr.Atom))
	case *expr.Symbol:
		return cmp.Compare(o.t.Name(x.Key), o.t.Name(b.(*expr.Symbol).Key))
	case *expr.HeadExpr:
		y := b.(*expr.HeadExpr)
		if c := o.compareHead(x, y); c != 0 {
			return c
		}
		return slices.CompareFunc(x.Args, y.Args, o.compareArg)
	case *expr.RationalExpr:
		y := b.(*expr.RationalExpr)
		if c := o.compareArg(x.Num, y.Num); c != 0 {
			return c
		}
		return o.compareArg(x.Den, y.Den)
	case *expr.ComplexExpr:
		y := b.(*expr.ComplexExpr)
		if c := o.compareArg(x.Re, y.Re); c != 0 {
			return c
		}
		return o.compareArg(x.Im, y.Im)
	case *expr.PowerExpr:
		y := b.(*expr.PowerExpr)
		if c := o.compareArg(x.Child, y.Child); c != 0 {
			return c
		}
		return o.compareArg(exponent(x), exponent(y))
	case *expr.TimesExpr:
		return slices.CompareFunc(x.Children, b.(*expr.TimesExpr).Children, o.compareArg)
	default:
		return slices.CompareFunc(x.(*expr.PlusExpr).Children, b.(*expr.PlusExpr).Children, o.compareArg)
	}
}

func exprRank(e expr.Expr) int {
	switch x := e.(type) {
	case *expr.Atom:
		return atomRank(x)
	case *expr.Symbol:
		return rankSymbol
	case *expr.HeadExpr:
		return rankUnknown
	case *expr.RationalExpr:
		return rankRational
	case *expr.ComplexExpr:
		return rankComplex
	case *expr.PowerExpr:
		return rankPower
	case *expr.TimesExpr:
		return rankTimes
	default:
		return rankPlus
	}
}

func (o order) compareHead(x, y *expr.HeadExpr) int {
	switch {
	case x.Head == nil && y.Head == nil:
		return cmp.Compare(o.t.Name(x.Name), o.t.Name(y.Name))
	case x.Head == nil:
		return -1
	case y.Head == nil:
		return 1
	default:
		return o.compareExpr(x.Head, y.Head)
	}
}

// exponent returns the exponent of p, a cached one as the Atom it renders as.
func exponent(p *expr.PowerExpr) expr.Arg {
	if p.Exp == (expr.Arg{}) {
		return expr.Arg{Expr: &expr.Atom{Int: p.ExpInt}}
	}
	return p.Exp
}

func atomRank(a *expr.Atom) int {
	if a.Text != nil {
		return rankNumber
	}
	return rankInteger
}

// compareAtom orders two integers by value, or two other numbers by their
// text. An integer in Big is beyond int32, so its sign orders it against one
// in Int.
func compareAtom(x, y *expr.Atom) int {
	switch {
	case x.Text != nil:
		return bytes.Compare(x.Text, y.Text)
	case x.Big == nil && y.Big == nil:
		return cmp.Compare(x.Int, y.Int)
	case x.Big == nil:
		return -y.Big.Sign()
	case y.Big == nil:
		return x.Big.Sign()
	default:
		return x.Big.Cmp(y.Big)
	}
}
