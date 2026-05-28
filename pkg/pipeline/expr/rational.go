package expr

import "math"

// Rational is an int32-bounded fraction. Construct via NewRational or
// arithmetic; the zero value of Rational is not a valid input. Methods
// maintain den > 0 and produce fully-reduced (gcd 1) outputs.
//
// Each operation returns the result and an ok flag; ok is false when the
// result cannot be represented in int32 (overflow) or when the operation is
// mathematically undefined (division by zero).
type Rational struct {
	Num int32
	Den int32
}

// NewRational lifts an integer to the rational n/1.
func NewRational(n int32) Rational {
	return Rational{Num: n, Den: 1}
}

// Add returns r+s reduced. ok is false on overflow.
func (r Rational) Add(s Rational) (Rational, bool) {
	num := int64(r.Num)*int64(s.Den) + int64(s.Num)*int64(r.Den)
	den := int64(r.Den) * int64(s.Den)
	return reduce64(num, den)
}

// Sub returns r-s reduced. ok is false on overflow.
func (r Rational) Sub(s Rational) (Rational, bool) {
	num := int64(r.Num)*int64(s.Den) - int64(s.Num)*int64(r.Den)
	den := int64(r.Den) * int64(s.Den)
	return reduce64(num, den)
}

// Neg returns -r. ok is false on overflow (r.Num = math.MinInt32).
func (r Rational) Neg() (Rational, bool) {
	return finalize(-int64(r.Num), int64(r.Den))
}

// Mul returns r*s reduced. ok is false on overflow.
func (r Rational) Mul(s Rational) (Rational, bool) {
	num := int64(r.Num) * int64(s.Num)
	den := int64(r.Den) * int64(s.Den)
	return reduce64(num, den)
}

// Div returns r/s reduced. ok is false on overflow or when s is zero.
func (r Rational) Div(s Rational) (Rational, bool) {
	if s.Num == 0 {
		return Rational{}, false
	}
	num := int64(r.Num) * int64(s.Den)
	den := int64(r.Den) * int64(s.Num)
	return reduce64(num, den)
}

// reduce64 returns the fully reduced Rational for num/den. den must be nonzero.
func reduce64(num, den int64) (Rational, bool) {
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

// finalize wraps num/den as a Rational with bounds and zero canonicalisation;
// performs no gcd reduction. den must be positive.
func finalize(num, den int64) (Rational, bool) {
	if num == 0 {
		return Rational{Num: 0, Den: 1}, true
	}
	if num < math.MinInt32 || num > math.MaxInt32 || den > math.MaxInt32 {
		return Rational{}, false
	}
	return Rational{Num: int32(num), Den: int32(den)}, true
}

// gcd64 returns gcd(a, b) for non-negative a, b; gcd(0, 0) = 0.
func gcd64(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
