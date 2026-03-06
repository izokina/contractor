package contract

import (
	"cmp"
	"slices"
	"sync"

	"github.com/izokina/contractor/pkg/pipeline/node"
)

type Contractor struct {
	indexPairs map[string]node.Pair

	scalar []any
	pairs  []node.Pair

	lorentz  []node.LorentzIndex
	momentum []node.Momentum

	mu sync.Mutex
}

func NewContractor() *Contractor {
	return &Contractor{
		indexPairs: make(map[string]node.Pair),
	}
}

func (c *Contractor) ContractAndNormalize(term node.Term) node.Term {
	c.mu.Lock()

	c.scalar = c.scalar[:0]
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
	term.Pairs = append(make([]node.Pair, 0, len(c.pairs)), c.pairs...)
	if len(c.scalar) != 0 {
		newScalars := make([][]any, len(term.Scalars))
		for i, scalar := range term.Scalars {
			newScalar := make([]any, 0, len(scalar)+len(c.scalar))
			newScalar = append(newScalar, scalar...)
			newScalar = append(newScalar, c.scalar...)
			newScalars[i] = newScalar
		}
		term.Scalars = newScalars
	}

	c.mu.Unlock()

	slices.SortFunc(term.Pairs, func(l, r node.Pair) int {
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

func (c *Contractor) addPair(pair node.Pair) {
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
			c.scalar = append(c.scalar, "D")
		} else {
			c.scalar = append(c.scalar, 4)
		}
		return
	}

	if len(c.lorentz) == 2 && c.lorentz[0].Signature > c.lorentz[1].Signature {
		c.lorentz[0], c.lorentz[1] = c.lorentz[1], c.lorentz[0]
	}
	if len(c.momentum) == 2 && c.momentum[0].Signature > c.momentum[1].Signature {
		c.momentum[0], c.momentum[1] = c.momentum[1], c.momentum[0]
	}
	pair = node.Pair{}
	pair.Lorentz = append(pair.Lorentz, c.lorentz...)
	pair.Momentum = append(pair.Momentum, c.momentum...)
	for _, l := range c.lorentz {
		c.indexPairs[l.Signature] = pair
	}
	if len(c.lorentz) == 0 {
		c.pairs = append(c.pairs, pair)
	}
}
