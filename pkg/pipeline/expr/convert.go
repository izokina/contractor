package expr

import "math/big"

// int32FromExpr returns the int32 value when n is an atom holding int32.
func int32FromExpr(n Expr) (int32, bool) {
	if a, ok := n.(*Atom); ok {
		if v, ok := a.Value.(int32); ok {
			return v, true
		}
	}
	return 0, false
}

func bigIntFromExpr(n Expr) (*big.Int, bool) {
	if a, ok := n.(*Atom); ok {
		if v, ok := a.Value.(*big.Int); ok {
			return v, true
		}
	}
	return nil, false
}

func intFromExpr(n Expr) (*big.Int, bool) {
	if v, ok := int32FromExpr(n); ok {
		return big.NewInt(int64(v)), true
	}
	return bigIntFromExpr(n)
}

// rationalFromExpr extracts a Rational when n is an integer atom (int32 or
// *big.Int) or a RationalExpr whose numerator and denominator are such atoms.
func rationalFromExpr(n Expr) (Rational, bool) {
	if v, ok := int32FromExpr(n); ok {
		return NewRational(v), true
	}
	if v, ok := bigIntFromExpr(n); ok {
		return bigRational(new(big.Rat).SetInt(v)), true
	}
	if r, ok := n.(*RationalExpr); ok {
		// The common int32/int32 case stays off math/big.
		if num, ok := int32FromExpr(r.Num); ok {
			if den, ok := int32FromExpr(r.Den); ok {
				if den == 0 {
					return Rational{}, false
				}
				return reduce64(int64(num), int64(den)), true
			}
		}
		num, ok := intFromExpr(r.Num)
		if !ok {
			return Rational{}, false
		}
		den, ok := intFromExpr(r.Den)
		if !ok || den.Sign() == 0 {
			return Rational{}, false
		}
		return bigRational(new(big.Rat).SetFrac(num, den)), true
	}
	return Rational{}, false
}

// ComplexFromExpr extracts a Complex when n represents a numeric value:
// an integer atom, a RationalExpr of integer atoms, or a ComplexExpr with both
// parts being one of those.
func ComplexFromExpr(n Expr) (Complex, bool) {
	if r, ok := rationalFromExpr(n); ok {
		return Complex{Re: r, Im: NewRational(0)}, true
	}
	if c, ok := n.(*ComplexExpr); ok {
		re, ok := rationalFromExpr(c.Re)
		if !ok {
			return Complex{}, false
		}
		im, ok := rationalFromExpr(c.Im)
		if !ok {
			return Complex{}, false
		}
		return Complex{Re: re, Im: im}, true
	}
	return Complex{}, false
}
