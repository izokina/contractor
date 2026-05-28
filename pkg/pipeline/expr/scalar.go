package expr

// Opaque is an atom carried symbolically through arithmetic. Signature is
// the canonical key used to group like factors; Val is the Expr the
// signature was computed from.
type Opaque struct {
	Signature string
	Val       Expr
}

// Monomial is a Complex coefficient times a slice of Opaques.
// Within a Calculator, Opaques are stored sorted by Signature.
type Monomial struct {
	Coeff   Complex
	Opaques []Opaque
}

// Scalar is a sum of Monomials — the working representation for normal-form
// arithmetic in the eval package.
type Scalar struct {
	Monomials []Monomial
}

// OneScalar is the multiplicative identity (a single monomial with coefficient
// 1+0i and no opaques). Used as the starting accumulator for product folds.
var OneScalar = Scalar{Monomials: []Monomial{{Coeff: OneComplex}}}
