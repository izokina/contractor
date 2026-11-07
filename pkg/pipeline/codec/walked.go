package codec

import (
	"cmp"
	"slices"

	"github.com/izokina/contractor/pkg/pipeline/expr"
)

// walked returns what the walk reads in place of a, a root summand or a node
// the walk reaches in one, reusing a's slices. In it a number is a product
// with only a coefficient, a read Rational or Complex is what it stands for,
// a Pair of a combination of momenta is the sum it stands for, and an opaque
// factor is the one node of its value. A node inside an opaque factor or a
// momentum source keeps its written form. walked runs once the summand is
// read, since a Power's exponent, which decides whether its base is walked,
// arrives after the base.
func (p *Parser) walked(a expr.Arg) expr.Arg {
	if a.Expr == nil {
		return p.expandPair(a.Pair.Slots(), 0)
	}
	if z, ok := expr.ComplexFromExpr(a.Expr); ok {
		return constant(z)
	}
	if !expr.Expands(a.Expr) {
		return expr.Arg{Expr: p.keys.Opaque(a.Expr)}
	}
	switch n := a.Expr.(type) {
	case *expr.PlusExpr:
		for i, c := range n.Children {
			n.Children[i] = p.walked(c)
		}
		return expr.Arg{Expr: expr.NewPlusExpr(n.Children)}
	case *expr.TimesExpr:
		return p.product(expr.OneComplex, n.Children)
	case *expr.PowerExpr:
		base := p.walked(n.Child)
		if z, ok := number(base); ok {
			return constant(z.Pow(n.ExpInt))
		}
		return expr.Arg{Expr: expr.NewPowerExprInt(base, n.ExpInt)}
	case *expr.RationalExpr:
		if d, ok := expr.ComplexFromExpr(n.Den.Expr); ok {
			inv, _ := d.Inverse()
			return p.product(inv, []expr.Arg{n.Num})
		}
		return p.product(expr.OneComplex, []expr.Arg{n.Num, {Expr: expr.NewPowerExprInt(n.Den, -1)}})
	default:
		c := n.(*expr.ComplexExpr)
		return expr.Arg{Expr: expr.NewPlusExpr([]expr.Arg{p.walked(c.Re), p.product(imaginaryUnit, []expr.Arg{c.Im})})}
	}
}

var imaginaryUnit = expr.NewComplex(expr.NewRational(0), expr.NewRational(1))

// product returns the product of coeff and factors, reusing their slice: its
// numbers fold into the coefficient, and its other factors are sorted by the
// length each counts in its Len, so that the one with the most terms, which
// the walk streams slowest, comes last. None is walked once the coefficient
// is 0.
func (p *Parser) product(coeff expr.Complex, factors []expr.Arg) expr.Arg {
	rest := factors[:0]
	for _, f := range factors {
		if z, ok := expr.ComplexFromExpr(f.Expr); ok {
			coeff = coeff.Mul(z)
		} else {
			rest = append(rest, f)
		}
	}
	kept := rest[:0]
	for _, f := range rest {
		if coeff.IsZero() {
			break
		}
		f = p.walked(f)
		if z, ok := number(f); ok {
			coeff = coeff.Mul(z)
			continue
		}
		kept = append(kept, f)
	}
	clear(factors[len(kept):])
	if coeff.IsZero() {
		return constant(coeff)
	}
	t := expr.NewTimesExpr(coeff, kept)
	slices.SortStableFunc(kept, func(a, b expr.Arg) int {
		return cmp.Compare(expr.ChildLen(a, t.IsScalar()), expr.ChildLen(b, t.IsScalar()))
	})
	return expr.Arg{Expr: t}
}

// number returns the value of a walked node that is a number.
func number(a expr.Arg) (expr.Complex, bool) {
	if t, ok := a.Expr.(*expr.TimesExpr); ok && len(t.Children) == 0 {
		return t.Coeff, true
	}
	return expr.Complex{}, false
}

func constant(z expr.Complex) expr.Arg {
	return expr.Arg{Expr: expr.NewTimesExpr(z, nil)}
}

// expandPair returns the Pair of the slots s or, if the source of one of its
// momenta from slot i on is a linear combination of labels with exact numeric
// coefficients, the sum it stands for, since a Pair is linear in each slot.
func (p *Parser) expandPair(s [2]expr.Slot, i int) expr.Arg {
	for ; i < len(s); i++ {
		if s[i].Index() {
			continue
		}
		if e, ok := p.distribute(p.t.Value(s[i].ID()).(expr.Arg), s, i); ok {
			return e
		}
	}
	return expr.Arg{Pair: expr.NewPair(s[0], s[1])}
}

// distribute returns the Pair of the slots s, with source in slot i, written
// with one Pair per label of source, and reports whether source is a
// combination: a sum, or a product of numbers and one other factor.
func (p *Parser) distribute(source expr.Arg, s [2]expr.Slot, i int) (expr.Arg, bool) {
	switch n := source.Expr.(type) {
	case *expr.PlusExpr:
		terms := make([]expr.Arg, len(n.Children))
		for j, c := range n.Children {
			terms[j] = p.label(c, s, i)
		}
		return expr.Arg{Expr: expr.NewPlusExpr(terms)}, true
	case *expr.TimesExpr:
		if coeff, j := scaled(n.Children); j >= 0 {
			return expr.Arg{Expr: expr.NewTimesExpr(coeff, []expr.Arg{p.label(n.Children[j], s, i)})}, true
		}
	}
	return expr.Arg{}, false
}

// label returns the Pair of the slots s, with the momentum source in slot i,
// distributed if it is a combination, and keyed as one label otherwise, with
// the slot after i expanded.
func (p *Parser) label(source expr.Arg, s [2]expr.Slot, i int) expr.Arg {
	if e, ok := p.distribute(source, s, i); ok {
		return e
	}
	s[i] = slot(p.keys.Label(source), false, s[i].Dim())
	return p.expandPair(s, i+1)
}

// scaled returns the product of the factors that are numbers and the index of
// the one that is not, or -1 unless exactly one is not.
func scaled(factors []expr.Arg) (expr.Complex, int) {
	coeff, j := expr.OneComplex, -1
	for i, f := range factors {
		if z, ok := expr.ComplexFromExpr(f.Expr); ok {
			coeff = coeff.Mul(z)
			continue
		}
		if j >= 0 {
			return coeff, -1
		}
		j = i
	}
	return coeff, j
}
