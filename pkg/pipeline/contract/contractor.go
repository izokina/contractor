package contract

import (
	"cmp"
	"slices"

	"github.com/izokina/contractor/pkg/pipeline/eval"
	"github.com/izokina/contractor/pkg/pipeline/expr"
)

type Contractor struct {
	indexPairs map[string]expr.Pair

	coeff expr.Scalar
	pairs []expr.Pair

	lorentz  []expr.LorentzIndex
	momentum []expr.Momentum

	calc       *eval.Calculator
	dScalar    expr.Scalar
	fourScalar expr.Scalar
}

func NewContractor() *Contractor {
	atomD := expr.NewAtom("D")
	return &Contractor{
		indexPairs: make(map[string]expr.Pair),
		calc:       eval.NewCalculator(),
		dScalar: expr.Scalar{Monomials: []expr.Monomial{{
			Coeff:   expr.OneComplex,
			Opaques: []expr.Opaque{{Signature: "\"D\"\n", Val: atomD}},
		}}},
		fourScalar: expr.Scalar{Monomials: []expr.Monomial{{
			Coeff: expr.NewComplex(expr.NewRational(4), expr.NewRational(0)),
		}}},
	}
}

func (c *Contractor) ContractAndNormalize(term expr.Term) expr.Term {
	c.coeff = expr.OneScalar
	c.pairs = c.pairs[:0]

	for _, pair := range term.Pairs {
		c.addPair(pair)
	}
	for _, pair := range c.indexPairs {
		c.pairs = append(c.pairs, pair)
		for _, l := range pair.Lorentz {
			delete(c.indexPairs, l.Signature)
		}
	}
	term.Pairs = append(make([]expr.Pair, 0, len(c.pairs)), c.pairs...)
	term.Coeff = c.calc.Mul(term.Coeff, c.coeff)

	slices.SortFunc(term.Pairs, func(l, r expr.Pair) int {
		c := cmp.Compare(len(l.Momentum), len(r.Momentum))
		if c != 0 {
			return c
		}
		for i := range len(l.Lorentz) {
			c = cmp.Compare(l.Lorentz[i].Signature, r.Lorentz[i].Signature)
			if c != 0 {
				return c
			}
		}
		for i := range len(l.Momentum) {
			c = cmp.Compare(l.Momentum[i].Signature, r.Momentum[i].Signature)
			if c != 0 {
				return c
			}
		}
		return 0
	})

	return term
}

func (c *Contractor) addPair(pair expr.Pair) {
	c.lorentz = append(c.lorentz[:0], pair.Lorentz...)
	c.momentum = append(c.momentum[:0], pair.Momentum...)

	for i := 0; i < len(c.lorentz); {
		signature := c.lorentz[i].Signature
		oldPair, ok := c.indexPairs[signature]
		if !ok {
			i++
			continue
		}

		j := len(c.lorentz) - 1
		c.lorentz[i] = c.lorentz[j]
		c.lorentz = c.lorentz[:j]

		for _, l := range oldPair.Lorentz {
			delete(c.indexPairs, l.Signature)
			if l.Signature != signature {
				c.lorentz = append(c.lorentz, l)
			}
		}
		for _, m := range oldPair.Momentum {
			c.momentum = append(c.momentum, m)
		}
	}

	if len(c.lorentz) == 2 && c.lorentz[0].Signature == c.lorentz[1].Signature {
		if c.lorentz[0].HasD {
			c.coeff = c.calc.Mul(c.coeff, c.dScalar)
		} else {
			c.coeff = c.calc.Mul(c.coeff, c.fourScalar)
		}
		return
	}

	if len(c.lorentz) == 2 && c.lorentz[0].Signature > c.lorentz[1].Signature {
		c.lorentz[0], c.lorentz[1] = c.lorentz[1], c.lorentz[0]
	}
	if len(c.momentum) == 2 && c.momentum[0].Signature > c.momentum[1].Signature {
		c.momentum[0], c.momentum[1] = c.momentum[1], c.momentum[0]
	}
	pair = expr.Pair{}
	pair.Lorentz = append(pair.Lorentz, c.lorentz...)
	pair.Momentum = append(pair.Momentum, c.momentum...)
	for _, l := range c.lorentz {
		c.indexPairs[l.Signature] = pair
	}
	if len(c.lorentz) == 0 {
		c.pairs = append(c.pairs, pair)
	}
}
