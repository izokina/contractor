package contract

import (
	"slices"

	"github.com/izokina/contractor/pkg/pipeline/eval"
	"github.com/izokina/contractor/pkg/pipeline/expr"
	"github.com/izokina/contractor/pkg/pipeline/ident"
)

type Contractor struct {
	indexPairs map[ident.ID]expr.Pair

	coeff expr.Scalar
	pairs []expr.Pair

	indices []expr.Slot
	momenta []expr.Slot

	calc   *eval.Calculator
	traces [expr.DimD + 1]expr.Scalar
}

// NewContractor returns a contractor whose traces give d, the parser's symbol
// D, in D and D-4 dimensions.
func NewContractor(d *expr.Symbol) *Contractor {
	dim := []expr.Factor{{Node: d, Exp: 1}}
	c := &Contractor{
		indexPairs: make(map[ident.ID]expr.Pair),
		calc:       eval.NewCalculator(),
	}
	c.traces[expr.Dim4] = expr.Scalar{Monomials: []expr.Monomial{{Coeff: integer(4)}}}
	c.traces[expr.DimD] = expr.Scalar{Monomials: []expr.Monomial{{Coeff: expr.OneComplex, Opaques: dim}}}
	c.traces[expr.DimDMinus4] = expr.Scalar{Monomials: []expr.Monomial{{Coeff: integer(-4)}, {Coeff: expr.OneComplex, Opaques: dim}}}
	return c
}

func integer(n int32) expr.Complex {
	return expr.NewComplex(expr.NewRational(n), expr.NewRational(0))
}

// ContractAndNormalize contracts the term's indices, and reports whether the
// term survives: it vanishes when a contraction joins 4 and D-4 dimensions.
// The term's Pairs are valid until the next call.
func (c *Contractor) ContractAndNormalize(term expr.Term) (expr.Term, bool) {
	c.coeff = expr.OneScalar
	c.pairs = c.pairs[:0]

	vanishes := false
	for _, pair := range term.Pairs {
		if !c.addPair(pair) {
			vanishes = true
			break
		}
	}
	for _, pair := range c.indexPairs {
		c.pairs = append(c.pairs, pair)
		s := pair.Slots()
		for _, l := range s[:pair.Indices()] {
			delete(c.indexPairs, l.ID())
		}
	}
	// Each Pair adds at most two entries.
	if 2*len(term.Pairs) > eval.ScratchLimit {
		c.indexPairs = make(map[ident.ID]expr.Pair)
	}
	if vanishes {
		return term, false
	}
	term.Pairs = c.pairs
	term.Coeff = c.calc.Mul(term.Coeff, c.coeff)
	slices.Sort(term.Pairs)
	return term, true
}

// addPair contracts pair with the Pairs that share an index name with it, and
// reports whether the term survives. The Pair they make spans the dimensions
// that all of them span, in both slots; a term whose Pairs share none
// vanishes.
func (c *Contractor) addPair(pair expr.Pair) bool {
	s, n := pair.Slots(), pair.Indices()
	c.indices = append(c.indices[:0], s[:n]...)
	c.momenta = append(c.momenta[:0], s[n:]...)
	dim := pair.Dim()

	for i := 0; i < len(c.indices); {
		name := c.indices[i].ID()
		oldPair, ok := c.indexPairs[name]
		if !ok {
			i++
			continue
		}
		if dim &= oldPair.Dim(); dim == 0 {
			return false
		}

		j := len(c.indices) - 1
		c.indices[i] = c.indices[j]
		c.indices = c.indices[:j]

		old, m := oldPair.Slots(), oldPair.Indices()
		for _, l := range old[:m] {
			delete(c.indexPairs, l.ID())
			if l.ID() != name {
				c.indices = append(c.indices, l)
			}
		}
		c.momenta = append(c.momenta, old[m:]...)
	}

	if len(c.indices) == 2 && c.indices[0].ID() == c.indices[1].ID() {
		c.coeff = c.calc.Mul(c.coeff, c.traces[dim])
		return true
	}

	// Contraction keeps two slots: a matched index takes the place of the
	// other slot of its partner. Both span the meet, and two of a kind go in
	// value order.
	copy(s[copy(s[:], c.indices):], c.momenta)
	a, b := s[0].WithDim(dim), s[1].WithDim(dim)
	if a.Index() == b.Index() && a > b {
		a, b = b, a
	}
	pair = expr.NewPair(a, b)
	for _, l := range c.indices {
		c.indexPairs[l.ID()] = pair
	}
	if len(c.indices) == 0 {
		c.pairs = append(c.pairs, pair)
	}
	return true
}
