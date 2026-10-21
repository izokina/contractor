package expr

import (
	"math"
	"math/big"
)

// Rational is an exact fraction with an int32 fast path and a math/big
// fallback. Construct via NewRational or arithmetic; the zero value is not a
// valid input. Methods maintain den > 0 and produce fully-reduced (gcd 1)
// outputs.
//
// big is set iff the value does not fit int32 (num/den are then ignored), so
// each value has one representation. == is valid only against small values.
type Rational struct {
	num int32
	den int32
	big *big.Rat
}

// NewRational lifts an integer to the rational n/1.
func NewRational(n int32) Rational {
	return Rational{num: n, den: 1}
}

// Add returns r+s reduced.
func (r Rational) Add(s Rational) Rational {
	if r.big == nil && s.big == nil {
		num := int64(r.num)*int64(s.den) + int64(s.num)*int64(r.den)
		den := int64(r.den) * int64(s.den)
		return reduce64(num, den)
	}
	return bigRational(new(big.Rat).Add(r.toBigRat(), s.toBigRat()))
}

// Sub returns r-s reduced.
func (r Rational) Sub(s Rational) Rational {
	if r.big == nil && s.big == nil {
		num := int64(r.num)*int64(s.den) - int64(s.num)*int64(r.den)
		den := int64(r.den) * int64(s.den)
		return reduce64(num, den)
	}
	return bigRational(new(big.Rat).Sub(r.toBigRat(), s.toBigRat()))
}

// Neg returns -r.
func (r Rational) Neg() Rational {
	if r.big == nil {
		return finalize(-int64(r.num), int64(r.den))
	}
	return bigRational(new(big.Rat).Neg(r.big))
}

// Mul returns r*s reduced.
func (r Rational) Mul(s Rational) Rational {
	if r.big == nil && s.big == nil {
		num := int64(r.num) * int64(s.num)
		den := int64(r.den) * int64(s.den)
		return reduce64(num, den)
	}
	return bigRational(new(big.Rat).Mul(r.toBigRat(), s.toBigRat()))
}

// Div returns r/s reduced. ok is false when s is zero.
func (r Rational) Div(s Rational) (Rational, bool) {
	if s.IsZero() {
		return Rational{}, false
	}
	if r.big == nil && s.big == nil {
		num := int64(r.num) * int64(s.den)
		den := int64(r.den) * int64(s.num)
		return reduce64(num, den), true
	}
	return bigRational(new(big.Rat).Quo(r.toBigRat(), s.toBigRat())), true
}

// Big returns the big.Rat backing, or nil when r fits the int32 fast path.
func (r Rational) Big() *big.Rat { return r.big }

// Num returns the int32 numerator, valid only when Big is nil.
func (r Rational) Num() int32 { return r.num }

// Den returns the int32 denominator, valid only when Big is nil.
func (r Rational) Den() int32 { return r.den }

// IsZero reports whether r is exactly zero.
func (r Rational) IsZero() bool {
	if r.big != nil {
		return r.big.Sign() == 0
	}
	return r.num == 0
}

func (r Rational) toBigRat() *big.Rat {
	if r.big != nil {
		return r.big
	}
	return big.NewRat(int64(r.num), int64(r.den))
}

// reduce64 returns the fully reduced Rational for num/den. den must be nonzero.
func reduce64(num, den int64) Rational {
	if den < 0 {
		num = -num
		den = -den
	}
	absNum := num
	if absNum < 0 {
		absNum = -absNum
	}
	g := gcd64(absNum, den)
	return finalize(num/g, den/g)
}

// finalize wraps a reduced num/den (den > 0), promoting to big past int32.
func finalize(num, den int64) Rational {
	if num == 0 {
		return Rational{num: 0, den: 1}
	}
	if num < math.MinInt32 || num > math.MaxInt32 || den > math.MaxInt32 {
		return Rational{big: big.NewRat(num, den)}
	}
	return Rational{num: int32(num), den: int32(den)}
}

// bigRational demotes q to the int32 fast path when it fits.
func bigRational(q *big.Rat) Rational {
	n, okN := fitsInt32(q.Num())
	d, okD := fitsInt32(q.Denom())
	if okN && okD {
		if n == 0 {
			return Rational{num: 0, den: 1}
		}
		return Rational{num: n, den: d}
	}
	return Rational{big: q}
}

func fitsInt32(x *big.Int) (int32, bool) {
	if !x.IsInt64() {
		return 0, false
	}
	v := x.Int64()
	if v < math.MinInt32 || v > math.MaxInt32 {
		return 0, false
	}
	return int32(v), true
}

// gcd64 returns gcd(a, b) for non-negative a, b; gcd(0, 0) = 0.
func gcd64(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
