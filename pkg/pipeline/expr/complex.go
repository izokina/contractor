package expr

// Complex is a Gaussian rational: Re + Im*i.
type Complex struct {
	Re Rational
	Im Rational
}

// NewComplex constructs Complex from real and imaginary rationals.
func NewComplex(re, im Rational) Complex {
	return Complex{Re: re, Im: im}
}

// OneComplex is the multiplicative identity of the Complex ring (1 + 0i).
// Its parts are small, so == against it is a valid identity test.
var OneComplex = Complex{Re: NewRational(1), Im: NewRational(0)}

// Add returns c+d.
func (c Complex) Add(d Complex) Complex {
	return Complex{Re: c.Re.Add(d.Re), Im: c.Im.Add(d.Im)}
}

// Mul returns c*d.
// (a+bi)(c+di) = (ac - bd) + (ad + bc)i
func (c Complex) Mul(d Complex) Complex {
	ac := c.Re.Mul(d.Re)
	bd := c.Im.Mul(d.Im)
	ad := c.Re.Mul(d.Im)
	bc := c.Im.Mul(d.Re)
	return Complex{Re: ac.Sub(bd), Im: ad.Add(bc)}
}

// Inverse returns 1/c. ok is false when c is zero.
// 1/(a+bi) = (a - bi) / (a² + b²)
func (c Complex) Inverse() (Complex, bool) {
	denom := c.Re.Mul(c.Re).Add(c.Im.Mul(c.Im))
	if denom.IsZero() {
		return Complex{}, false
	}
	re, _ := c.Re.Div(denom)
	im, _ := c.Im.Neg().Div(denom)
	return Complex{Re: re, Im: im}, true
}

// Div returns c/d. ok is false when d is zero.
func (c Complex) Div(d Complex) (Complex, bool) {
	inv, ok := d.Inverse()
	if !ok {
		return Complex{}, false
	}
	return c.Mul(inv), true
}

// IsZero reports whether c is exactly zero.
func (c Complex) IsZero() bool {
	return c.Re.IsZero() && c.Im.IsZero()
}
