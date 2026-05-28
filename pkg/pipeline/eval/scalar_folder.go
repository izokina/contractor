package eval

import (
	"github.com/izokina/contractor/pkg/pipeline/expr"
)

// scalarFolder adapts the generic walk.Walker to fold an Expr into a
// Calculator's accumulator: numeric atoms / RationalExpr[int32,int32] /
// ComplexExpr of those collapse into the running Complex coefficient, every
// other non-traversable Expr (and Power[_, n<=0]) becomes an Opaque.
// Coefficient overflow is recovered by atomizing the original Expr as an
// Opaque.
type scalarFolder struct {
	calc *Calculator
}

func (f *scalarFolder) Identity() expr.Complex { return expr.OneComplex }

func (f *scalarFolder) Compound(n expr.Expr) bool {
	_, ok := expr.ComplexFromExpr(n)
	return !ok
}

func (f *scalarFolder) Fold(prev expr.Complex, opaques []expr.Opaque, n expr.Expr) (expr.Complex, []expr.Opaque) {
	if z, ok := expr.ComplexFromExpr(n); ok {
		if next, ok := prev.Mul(z); ok {
			return next, opaques
		}
		// Overflow: fall through to opaque path so the original numeric
		// value is preserved as an Opaque.
	}
	return prev, append(opaques, expr.Opaque{Signature: f.calc.signer.Expr(n), Val: n})
}

func (f *scalarFolder) Emit(coeff expr.Complex, opaques []expr.Opaque) {
	f.calc.opBuf = append(f.calc.opBuf[:0], opaques...)
	f.calc.sortOpBuf()
	f.calc.foldBuf(coeff)
}
