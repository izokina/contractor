// Package walk drives a generic cyclic-pull-generator traversal of an
// expr.Expr tree. Concrete behaviour lives in a Folder[Coeff, Leaf]
// supplied by the caller.
package walk

import (
	"github.com/izokina/contractor/pkg/pipeline/expr"
)

// Folder customises Walker for a use-site. Fold must NOT mutate elements
// already in leaves — only the tail it appends is its own; Walker reslices
// on rollback.
type Folder[Coeff, Leaf any] interface {
	Identity() Coeff
	Compound(e expr.Expr) bool
	Fold(prev Coeff, leaves []Leaf, e expr.Expr) (next Coeff, newLeaves []Leaf)
	Emit(coeff Coeff, leaves []Leaf)
}

type Walker[Coeff, Leaf any] struct {
	folder Folder[Coeff, Leaf]
	coeff  Coeff
	leaves []Leaf
}

func NewWalker[Coeff, Leaf any](f Folder[Coeff, Leaf]) *Walker[Coeff, Leaf] {
	return &Walker[Coeff, Leaf]{folder: f}
}

// stream is a cyclic pull generator: true folds one monomial into the
// Walker, false unfolds it back to round-start state. After false the next
// call begins another identical round — which lets mulStreams drive its
// children across multiple rounds without reallocating.
type stream = func() bool

func (w *Walker[Coeff, Leaf]) Walk(e expr.Expr) {
	w.leaves = w.leaves[:0]
	w.coeff = w.folder.Identity()
	s := w.walk(e)
	for s() {
		w.folder.Emit(w.coeff, w.leaves)
	}
}

// walk falls through to walkLeaf for non-Compound nodes, *PowerExpr with
// ExpInt <= 0, and any unrecognised type.
func (w *Walker[Coeff, Leaf]) walk(e expr.Expr) stream {
	if w.folder.Compound(e) {
		switch n := e.(type) {
		case *expr.PlusExpr:
			return w.walkPlus(n.Children)
		case *expr.TimesExpr:
			return w.walkTimes(n.Children)
		case *expr.PowerExpr:
			if n.ExpInt > 0 {
				return w.walkPower(n.Child, n.ExpInt)
			}
		}
	}
	return w.walkLeaf(e)
}

func (w *Walker[Coeff, Leaf]) walkPlus(children []expr.Expr) stream {
	streams := make([]stream, len(children))
	for j, c := range children {
		streams[j] = w.walk(c)
	}
	var i int
	return func() bool {
		for i < len(streams) {
			if streams[i]() {
				return true
			}
			i++
		}
		i = 0
		return false
	}
}

func (w *Walker[Coeff, Leaf]) walkTimes(children []expr.Expr) stream {
	streams := make([]stream, len(children))
	for j, c := range children {
		streams[j] = w.walk(c)
	}
	return mulStreams(streams)
}

// walkPower needs n independent walk(child) closures: each tracks its own
// fold state inside the Walker as the Cartesian product steps through it.
func (w *Walker[Coeff, Leaf]) walkPower(child expr.Expr, n int32) stream {
	streams := make([]stream, n)
	for j := range streams {
		streams[j] = w.walk(child)
	}
	return mulStreams(streams)
}

// walkLeaf defers the unfold to the false branch so prevCoeff/prevLen
// reflect Walker state at fold time — required for correct nested
// restore under Times/Power.
func (w *Walker[Coeff, Leaf]) walkLeaf(e expr.Expr) stream {
	var (
		prevCoeff Coeff
		prevLen   int
		folded    bool
	)
	return func() bool {
		if folded {
			w.coeff = prevCoeff
			w.leaves = w.leaves[:prevLen]
			folded = false
			return false
		}
		prevCoeff = w.coeff
		prevLen = len(w.leaves)
		w.coeff, w.leaves = w.folder.Fold(w.coeff, w.leaves, e)
		folded = true
		return true
	}
}

// mulStreams enumerates the Cartesian product of streams. cur is the prefix
// length currently folded; advance retries rightmost first, falling back
// leftward when a child's round ends.
func mulStreams(streams []stream) stream {
	if len(streams) == 0 {
		var folded bool
		return func() bool {
			if folded {
				folded = false
				return false
			}
			folded = true
			return true
		}
	}
	var cur int
	return func() bool {
		if cur == len(streams) {
			cur--
		}
		for {
			if cur < 0 {
				cur = 0
				return false
			}
			if streams[cur]() {
				cur++
				if cur == len(streams) {
					return true
				}
			} else {
				cur--
			}
		}
	}
}
