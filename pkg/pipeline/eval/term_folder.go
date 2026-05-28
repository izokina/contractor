package eval

import (
	"github.com/izokina/contractor/pkg/pipeline/expr"
	"github.com/izokina/contractor/pkg/pipeline/walk"
)

// TermFolder is the walk.Folder[Scalar, Pair] implementation that emits
// parsed Terms with normalised coefficients: each scalar Expr leaf is folded
// into the running Scalar via the inner scalarFolder Walker, Pair leaves
// accumulate as the term's Pairs slice, and each completed monomial flows
// out through sink.
//
// One Calculator is shared between the inner Walker (Expr → Scalar) and the
// outer Mul step. This is safe because Calculator.materialize copies
// Monomials by value into a fresh slice and never mutates a previously
// allocated Monomial.Opaques backing array — so Scalars returned from
// earlier Folds remain valid across the next Mul.reset().
type TermFolder struct {
	sink       func(expr.Term)
	calc       *Calculator
	scalarWalk *walk.Walker[expr.Complex, expr.Opaque]
}

// NewTermFolder constructs a TermFolder that emits via sink.
func NewTermFolder(sink func(expr.Term)) *TermFolder {
	calc := NewCalculator()
	return &TermFolder{
		sink:       sink,
		calc:       calc,
		scalarWalk: walk.NewWalker(&scalarFolder{calc: calc}),
	}
}

func (f *TermFolder) Identity() expr.Scalar { return expr.OneScalar }

func (f *TermFolder) Compound(e expr.Expr) bool {
	return !e.IsScalar()
}

func (f *TermFolder) Fold(prev expr.Scalar, pairs []expr.Pair, e expr.Expr) (expr.Scalar, []expr.Pair) {
	if p, ok := e.(expr.Pair); ok {
		return prev, append(pairs, p)
	}
	f.calc.reset()
	f.scalarWalk.Walk(e)
	return f.calc.Mul(prev, f.calc.materialize()), pairs
}

func (f *TermFolder) Emit(coeff expr.Scalar, pairs []expr.Pair) {
	f.sink(expr.Term{
		Pairs: append(make([]expr.Pair, 0, len(pairs)), pairs...),
		Coeff: coeff,
	})
}
