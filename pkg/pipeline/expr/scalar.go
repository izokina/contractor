package expr

// Monomial is a Complex coefficient times its opaque factors. eval keeps them
// in key order (KeyOf) until the merger's flush.
type Monomial struct {
	Coeff   Complex
	Opaques []Factor
}

// Factor is an opaque factor, Node, to the positive power Exp. Node is the
// one node of its value (intern.Keys.Opaque), so equal factors are the same
// pointer.
type Factor struct {
	Node Expr
	Exp  int32
}

// Scalar is a sum of Monomials — the working representation for normal-form
// arithmetic in the eval package.
type Scalar struct {
	Monomials []Monomial
}

// OneScalar is the multiplicative identity (a single monomial with coefficient
// 1+0i and no opaques). Used as the starting accumulator for product folds.
var OneScalar = Scalar{Monomials: []Monomial{{Coeff: OneComplex}}}

func ScalarOf(c Complex) Scalar {
	switch {
	case c == OneComplex:
		return OneScalar
	case c.IsZero():
		return Scalar{}
	}
	return Scalar{Monomials: []Monomial{{Coeff: c}}}
}
