package expr

// Complex is a Gaussian rational: Re + Im*i, with both parts represented as
// int32-bounded Rationals.
type Complex struct {
	Re Rational
	Im Rational
}

// NewComplex constructs Complex from real and imaginary rationals.
func NewComplex(re, im Rational) Complex {
	return Complex{Re: re, Im: im}
}

// OneComplex is the multiplicative identity of the Complex ring (1 + 0i).
// Used by Scalar.Expr to suppress trivial coefficients and by the eval
// package's overflow recovery path.
var OneComplex = Complex{Re: NewRational(1), Im: NewRational(0)}

// Add returns c+d. ok is false on overflow.
func (c Complex) Add(d Complex) (Complex, bool) {
	re, ok := c.Re.Add(d.Re)
	if !ok {
		return Complex{}, false
	}
	im, ok := c.Im.Add(d.Im)
	if !ok {
		return Complex{}, false
	}
	return Complex{Re: re, Im: im}, true
}

// Mul returns c*d. ok is false on overflow.
// (a+bi)(c+di) = (ac - bd) + (ad + bc)i
func (c Complex) Mul(d Complex) (Complex, bool) {
	ac, ok := c.Re.Mul(d.Re)
	if !ok {
		return Complex{}, false
	}
	bd, ok := c.Im.Mul(d.Im)
	if !ok {
		return Complex{}, false
	}
	ad, ok := c.Re.Mul(d.Im)
	if !ok {
		return Complex{}, false
	}
	bc, ok := c.Im.Mul(d.Re)
	if !ok {
		return Complex{}, false
	}
	re, ok := ac.Sub(bd)
	if !ok {
		return Complex{}, false
	}
	im, ok := ad.Add(bc)
	if !ok {
		return Complex{}, false
	}
	return Complex{Re: re, Im: im}, true
}

// Inverse returns 1/c. ok is false on overflow or when c is zero.
// 1/(a+bi) = (a - bi) / (a² + b²)
func (c Complex) Inverse() (Complex, bool) {
	aa, ok := c.Re.Mul(c.Re)
	if !ok {
		return Complex{}, false
	}
	bb, ok := c.Im.Mul(c.Im)
	if !ok {
		return Complex{}, false
	}
	denom, ok := aa.Add(bb)
	if !ok {
		return Complex{}, false
	}
	if denom.Num == 0 {
		return Complex{}, false
	}
	re, ok := c.Re.Div(denom)
	if !ok {
		return Complex{}, false
	}
	negIm, ok := c.Im.Neg()
	if !ok {
		return Complex{}, false
	}
	im, ok := negIm.Div(denom)
	if !ok {
		return Complex{}, false
	}
	return Complex{Re: re, Im: im}, true
}

// Div returns c/d. ok is false on overflow or when d is zero.
func (c Complex) Div(d Complex) (Complex, bool) {
	inv, ok := d.Inverse()
	if !ok {
		return Complex{}, false
	}
	return c.Mul(inv)
}

// IsZero reports whether c is exactly zero.
func (c Complex) IsZero() bool {
	return c.Re.Num == 0 && c.Im.Num == 0
}
