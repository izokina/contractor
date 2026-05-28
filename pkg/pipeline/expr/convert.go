package expr

// Int32FromExpr returns the int32 value when n is an atom holding int32.
func Int32FromExpr(n Expr) (int32, bool) {
	if a, ok := n.(*Atom); ok {
		if v, ok := a.Value.(int32); ok {
			return v, true
		}
	}
	return 0, false
}

// RationalFromExpr extracts a Rational when n is either an int32 atom or a
// RationalExpr with int32 numerator and denominator atoms.
func RationalFromExpr(n Expr) (Rational, bool) {
	if v, ok := Int32FromExpr(n); ok {
		return NewRational(v), true
	}
	if r, ok := n.(*RationalExpr); ok {
		num, ok := Int32FromExpr(r.Num)
		if !ok {
			return Rational{}, false
		}
		den, ok := Int32FromExpr(r.Den)
		if !ok {
			return Rational{}, false
		}
		return reduce64(int64(num), int64(den))
	}
	return Rational{}, false
}

// ComplexFromExpr extracts a Complex when n represents a numeric value:
// int32 atom, RationalExpr[int32,int32], or ComplexExpr with both parts
// being one of those.
func ComplexFromExpr(n Expr) (Complex, bool) {
	if r, ok := RationalFromExpr(n); ok {
		return Complex{Re: r, Im: NewRational(0)}, true
	}
	if c, ok := n.(*ComplexExpr); ok {
		re, ok := RationalFromExpr(c.Re)
		if !ok {
			return Complex{}, false
		}
		im, ok := RationalFromExpr(c.Im)
		if !ok {
			return Complex{}, false
		}
		return Complex{Re: re, Im: im}, true
	}
	return Complex{}, false
}

// RationalToExpr converts a Rational into the smallest-shape Expr
// representing the same value: a bare int32 atom when Den == 1, otherwise a
// RationalExpr with int32 atoms in numerator and denominator.
func RationalToExpr(r Rational) Expr {
	if r.Den == 1 {
		return NewAtom(r.Num)
	}
	return &RationalExpr{
		Num: NewAtom(r.Num),
		Den: NewAtom(r.Den),
	}
}

// ComplexToExpr converts a Complex into the smallest-shape Expr: the
// real-part conversion when Im is zero, otherwise a ComplexExpr.
func ComplexToExpr(z Complex) Expr {
	if z.Im.Num == 0 {
		return RationalToExpr(z.Re)
	}
	return &ComplexExpr{
		Re: RationalToExpr(z.Re),
		Im: RationalToExpr(z.Im),
	}
}
