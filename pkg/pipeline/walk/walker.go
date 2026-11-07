// Package walk streams the terms of a root summand, one at a time.
package walk

import (
	"slices"

	"github.com/izokina/contractor/pkg/pipeline/contract"
	"github.com/izokina/contractor/pkg/pipeline/eval"
	"github.com/izokina/contractor/pkg/pipeline/expr"
	"github.com/izokina/contractor/pkg/pipeline/ident"
)

// Walker yields the terms of a summand. Its stack holds a frame for each
// tensor sum it is inside, and on top a leaf, a Pair or a scalar, or a
// product, which steps through the combinations of its digits.
type Walker struct {
	folder     *eval.Folder
	calc       *eval.Calculator
	contractor *contract.Contractor
	sum        *eval.Sum
	key        eval.KeyedTerm
	sub        *Walker

	frames []frame
	pairs  []expr.Pair
	coeff  expr.Scalar

	names   []ident.ID
	ends    []int
	sets    [][]expr.Term
	owner   map[ident.ID]int
	root    []int
	size    []int
	multi   []bool
	pending []pending
	members []expr.Arg
	buf     []expr.Pair
}

type kind uint8

const (
	leafFrame kind = iota
	sumFrame
	productFrame
)

type frame struct {
	kind kind

	// A leaf: its one term, until done.
	term expr.Term
	done bool

	// A sum: its addends, and the next to enter.
	addends []expr.Arg
	next    int

	// A product: its digits, the first the fastest, and coeffs[j], in
	// bufs[j], the product of the coefficients of the digits from j on and
	// of coeffs[len(digits)], the product's own. leaves and leafPairs hold
	// the rows of its factors that are a Pair or a scalar; leafPairs holds a
	// leaf's Pair too.
	digits    []digit
	coeffs    []expr.Scalar
	bufs      []eval.Buffer
	fresh     bool
	leaves    []expr.Term
	leafPairs []expr.Pair
	walkers   []*Walker
}

// digit is a factor of a product, or a group of factors multiplied out
// together: the rows it holds and the one it is at, or the walker that
// streams the factor e.
type digit struct {
	rows []expr.Term
	at   int
	walk *Walker
	e    expr.Arg
}

// pending is a group that a product multiplies out last: its first member,
// and the digit it fills.
type pending struct {
	root, digit int
}

// NewWalker returns a walker that contracts with contractor what it
// multiplies out.
func NewWalker(contractor *contract.Contractor) *Walker {
	return &Walker{folder: eval.NewFolder(), calc: eval.NewCalculator(), contractor: contractor, sum: eval.NewSum(), owner: make(map[ident.ID]int)}
}

func (w *Walker) child() *Walker {
	return &Walker{folder: w.folder, calc: w.calc, contractor: w.contractor, sum: eval.NewSum(), owner: make(map[ident.ID]int)}
}

// Walk starts the walk of the summand e, whose terms Next returns.
func (w *Walker) Walk(e expr.Arg) {
	w.pop(0)
	w.enter(e)
}

// Next returns the next term of the summand, and false once there is none. A
// term's Pairs and coefficient are valid until the next call.
func (w *Walker) Next() (expr.Term, bool) {
	if !w.step() {
		return expr.Term{}, false
	}
	return expr.Term{Pairs: w.pairs, Coeff: w.coeff}, true
}

func (w *Walker) step() bool {
	if w.next(0) {
		return true
	}
	w.pop(0)
	return false
}

func (w *Walker) next(i int) bool {
	if i == len(w.frames) {
		return false
	}
	f := &w.frames[i]
	switch f.kind {
	case leafFrame:
		if f.done {
			return false
		}
		f.done = true
		w.pairs, w.coeff = append(w.pairs[:0], f.term.Pairs...), f.term.Coeff
		return true
	case sumFrame:
		for !w.next(i + 1) {
			w.pop(i + 1)
			f := &w.frames[i]
			if f.next == len(f.addends) {
				return false
			}
			f.next++
			w.enter(f.addends[f.next-1])
		}
		return true
	}
	return w.advance(f)
}

func (w *Walker) advance(f *frame) bool {
	j := len(f.digits) - 1
	if f.fresh {
		f.fresh = false
	} else {
		for j = 0; !f.digits[j].advance(); j++ {
			if j == len(f.digits)-1 {
				return false
			}
		}
		for k := range j {
			f.digits[k].restart()
		}
	}
	for ; j >= 0; j-- {
		_, c := f.digits[j].term()
		f.coeffs[j] = w.calc.MulTo(&f.bufs[j], f.coeffs[j+1], c)
	}
	w.coeff = f.coeffs[0]
	w.pairs = w.pairs[:0]
	for k := range f.digits {
		p, _ := f.digits[k].term()
		w.pairs = append(w.pairs, p...)
	}
	return true
}

func (d *digit) advance() bool {
	if d.walk == nil {
		d.at++
		return d.at < len(d.rows)
	}
	return d.walk.step()
}

// restart walks d again from its first term, which it had before.
func (d *digit) restart() {
	if d.walk == nil {
		d.at = 0
		return
	}
	d.walk.Walk(d.e)
	d.walk.step()
}

func (d *digit) term() ([]expr.Pair, expr.Scalar) {
	if d.walk == nil {
		t := d.rows[d.at]
		return t.Pairs, t.Coeff
	}
	return d.walk.pairs, d.walk.coeff
}

// enter pushes the frames of e. A node with no terms pushes nothing.
func (w *Walker) enter(e expr.Arg) {
	switch n := e.Expr.(type) {
	case nil:
		f := w.push(leafFrame)
		f.leafPairs = append(f.leafPairs[:0], e.Pair)
		f.term = expr.Term{Pairs: f.leafPairs, Coeff: expr.OneScalar}
		return
	case *expr.PlusExpr:
		if !n.IsScalar() {
			w.push(sumFrame).addends = n.Children
			return
		}
	case *expr.TimesExpr:
		if !n.IsScalar() {
			w.product(n.Coeff, n.Children)
			return
		}
	case *expr.PowerExpr:
		if !n.IsScalar() {
			w.power(n.Child, n.ExpInt)
			return
		}
	}
	if v := w.folder.Fold(e.Expr); len(v.Monomials) != 0 {
		w.push(leafFrame).term = expr.Term{Coeff: v}
	}
}

// product pushes the product of coeff and factors. A factor of one term is a
// digit of one row, and one that closes an index name inside itself is
// multiplied out first and merged. The factors are grouped by the index names
// they leave open, and those that leave none into one group, all but the last
// of the other factors, which has the most terms and is streamed slowest. A
// group of several factors, one of which has more than one term, is one
// digit, multiplied out and merged, since its terms can merge. Every other
// factor is streamed, walked again for each combination of the slower digits.
// The groups are multiplied out last, so that a factor with no terms ends the
// product first.
func (w *Walker) product(coeff expr.Complex, factors []expr.Arg) {
	c := expr.ScalarOf(coeff)
	if len(c.Monomials) == 0 {
		return
	}
	f := w.push(productFrame)
	k := len(factors)
	f.leaves = slices.Grow(f.leaves[:0], k)[:k]
	f.leafPairs = slices.Grow(f.leafPairs[:0], k)[:k]
	w.sets = slices.Grow(w.sets[:0], k)[:k]
	w.names, w.ends = w.names[:0], w.ends[:0]
	streamed := -1
	for i, x := range factors {
		closes := w.open(x)
		w.ends = append(w.ends, len(w.names))
		var rows []expr.Term
		switch {
		case x.Expr == nil:
			f.leafPairs[i] = x.Pair
			f.leaves[i] = expr.Term{Pairs: f.leafPairs[i : i+1], Coeff: expr.OneScalar}
			rows = f.leaves[i : i+1]
		case x.IsScalar():
			if v := w.folder.Fold(x.Expr); len(v.Monomials) != 0 {
				f.leaves[i] = expr.Term{Coeff: v}
				rows = f.leaves[i : i+1]
			}
		case x.Len() == 1 || closes:
			rows = w.rows(x)
		default:
			streamed = i
			continue
		}
		if len(rows) == 0 {
			w.drop()
			return
		}
		w.sets[i] = rows
	}
	w.group(streamed)
	w.pending = w.pending[:0]
	streams := 0
	for i, x := range factors {
		if r := w.find(i); i != streamed && w.size[r] > 1 && w.multi[r] {
			if r == i {
				w.pending = append(w.pending, pending{root: i, digit: len(f.digits)})
				f.digits = append(f.digits, digit{})
			}
			continue
		}
		d := digit{rows: w.sets[i]}
		if d.rows == nil {
			if streams == len(f.walkers) {
				f.walkers = append(f.walkers, w.child())
			}
			d.walk, d.e = f.walkers[streams], x
			streams++
			d.walk.Walk(x)
			if !d.walk.step() {
				w.drop()
				return
			}
		}
		f.digits = append(f.digits, d)
	}
	for _, p := range w.pending {
		rows := w.merge(factors, p.root, streamed)
		if len(rows) == 0 {
			w.drop()
			return
		}
		f.digits[p.digit].rows = rows
	}
	clear(w.sets)
	w.ready(f, c)
}

// merge multiplies out the group whose first member is root: its members' rows
// in their order, or, where it has a member to stream, the walk of the group
// as a product.
func (w *Walker) merge(factors []expr.Arg, root, streamed int) []expr.Term {
	walk := false
	for j := root; j < len(factors); j++ {
		if j != streamed && w.find(j) == root {
			w.members = append(w.members, factors[j])
			walk = walk || w.sets[j] == nil
		}
	}
	var rows []expr.Term
	if walk {
		w.subWalker().pop(0)
		w.sub.product(expr.OneComplex, w.members)
		rows = w.merged()
	} else {
		for j := root; j < len(factors); j++ {
			switch {
			case j == streamed || w.find(j) != root:
			case rows == nil:
				rows = w.sets[j]
			default:
				rows = w.mul(rows, w.sets[j])
			}
		}
	}
	clear(w.members)
	w.members = w.members[:0]
	return rows
}

func (w *Walker) drop() {
	clear(w.sets)
	w.pop(len(w.frames) - 1)
}

// power pushes base to the power n > 0: two digits, the rows of n - 1 copies,
// multiplied out copy by copy, and those of one, or the walk of base alone.
func (w *Walker) power(base expr.Arg, n int32) {
	if n == 1 {
		w.enter(base)
		return
	}
	b := w.rows(base)
	ts := b
	for i := int32(2); i < n && len(ts) != 0; i++ {
		ts = w.mul(ts, b)
	}
	if len(ts) == 0 {
		return
	}
	f := w.push(productFrame)
	f.digits = append(f.digits, digit{rows: ts}, digit{rows: b})
	w.ready(f, expr.OneScalar)
}

func (w *Walker) ready(f *frame, c expr.Scalar) {
	k := len(f.digits)
	f.coeffs = slices.Grow(f.coeffs[:0], k+1)[:k+1]
	f.coeffs[k] = c
	if n := k - len(f.bufs); n > 0 {
		f.bufs = append(f.bufs, make([]eval.Buffer, n)...)
	}
}

// group links the factors but streamed by the open index names noted in
// w.names, and those that leave none: find returns a factor's group, its
// first member.
func (w *Walker) group(streamed int) {
	k := len(w.ends)
	w.root = slices.Grow(w.root[:0], k)[:k]
	w.size = slices.Grow(w.size[:0], k)[:k]
	w.multi = slices.Grow(w.multi[:0], k)[:k]
	if len(w.owner) > eval.ScratchLimit {
		w.owner = make(map[ident.ID]int)
	}
	clear(w.owner)
	nameless, from := -1, 0
	for i := range k {
		w.root[i] = i
		names := w.names[from:w.ends[i]]
		from = w.ends[i]
		if i == streamed {
			continue
		}
		if len(names) == 0 {
			if nameless < 0 {
				nameless = i
			} else {
				w.union(nameless, i)
			}
		}
		for _, name := range names {
			if o, ok := w.owner[name]; ok {
				w.union(o, i)
			} else {
				w.owner[name] = i
			}
		}
	}
	clear(w.size)
	clear(w.multi)
	for i := range k {
		if i == streamed {
			continue
		}
		r := w.find(i)
		w.size[r]++
		w.multi[r] = w.multi[r] || len(w.sets[i]) != 1
	}
}

func (w *Walker) find(i int) int {
	for w.root[i] != i {
		i = w.root[i]
	}
	return i
}

func (w *Walker) union(a, b int) {
	a, b = w.find(a), w.find(b)
	w.root[max(a, b)] = min(a, b)
}

// open appends to w.names, each once, the index names that e leaves open,
// and reports whether e closes one. A Pair leaves open the names it holds
// once and closes one it holds twice, a sum leaves open what an addend does,
// a product what one factor does and closes what two do, and a power of more
// than one copy closes every name its base leaves open. Each closes what its
// parts close.
func (w *Walker) open(e expr.Arg) bool {
	if e.IsScalar() {
		return false
	}
	from := len(w.names)
	closes := false
	switch n := e.Expr.(type) {
	case nil:
		s := e.Pair.Slots()
		for _, l := range s[:e.Pair.Indices()] {
			w.names = append(w.names, l.ID())
		}
	case *expr.PlusExpr:
		for _, c := range n.Children {
			closes = w.open(c) || closes
		}
		names := w.names[from:]
		slices.Sort(names)
		w.names = append(w.names[:from], slices.Compact(names)...)
		return closes
	case *expr.TimesExpr:
		for _, c := range n.Children {
			closes = w.open(c) || closes
		}
	case *expr.PowerExpr:
		closes = w.open(n.Child)
		if n.ExpInt > 1 && len(w.names) > from {
			w.names = w.names[:from]
			return true
		}
		return closes
	}
	names := w.names[from:]
	slices.Sort(names)
	n := from
	for i := 0; i < len(names); {
		j := i + 1
		for j < len(names) && names[j] == names[i] {
			j++
		}
		if j == i+1 {
			w.names[n] = names[i]
			n++
		} else {
			closes = true
		}
		i = j
	}
	w.names = w.names[:n]
	return closes
}

func (w *Walker) push(k kind) *frame {
	if len(w.frames) == cap(w.frames) {
		w.frames = append(w.frames, frame{})
	} else {
		w.frames = w.frames[:len(w.frames)+1]
	}
	f := &w.frames[len(w.frames)-1]
	f.kind, f.done, f.next, f.fresh = k, false, 0, true
	return f
}

// pop drops the frames from i on, and what they, their walkers and the
// current term hold, keeping their buffers.
func (w *Walker) pop(i int) {
	for j := i; j < len(w.frames); j++ {
		f := &w.frames[j]
		f.term, f.addends = expr.Term{}, nil
		clear(f.digits)
		f.digits = f.digits[:0]
		clear(f.coeffs)
		clear(f.leaves)
		for _, c := range f.walkers {
			c.pop(0)
		}
	}
	w.frames = w.frames[:i]
	w.coeff = expr.Scalar{}
}

func (w *Walker) subWalker() *Walker {
	if w.sub == nil {
		w.sub = w.child()
	}
	return w.sub
}

// rows multiplies out the tensor node e: its terms, contracted and merged.
func (w *Walker) rows(e expr.Arg) []expr.Term {
	w.subWalker().Walk(e)
	return w.merged()
}

func (w *Walker) merged() []expr.Term {
	for w.sub.step() {
		w.add(expr.Term{Pairs: w.sub.pairs, Coeff: w.sub.coeff})
	}
	return w.sum.Terms()
}

func (w *Walker) mul(a, b []expr.Term) []expr.Term {
	for _, x := range a {
		for _, y := range b {
			w.buf = append(append(w.buf[:0], x.Pairs...), y.Pairs...)
			w.add(expr.Term{Pairs: w.buf, Coeff: w.calc.Mul(x.Coeff, y.Coeff)})
		}
	}
	return w.sum.Terms()
}

func (w *Walker) add(t expr.Term) {
	if u, ok := w.contractor.ContractAndNormalize(t); ok {
		w.key.Set(u)
		w.sum.AddKeyed(&w.key)
	}
}
