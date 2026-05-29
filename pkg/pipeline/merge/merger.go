package merge

import (
	"encoding/json/jsontext"
	"sync"

	"github.com/izokina/contractor/pkg/literal"
	"github.com/izokina/contractor/pkg/pipeline/codec"
	"github.com/izokina/contractor/pkg/pipeline/eval"
	"github.com/izokina/contractor/pkg/pipeline/expr"
)

type termSet struct {
	Pairs []expr.Pair
	Coeff expr.Scalar
}

type Merger struct {
	mu     sync.Mutex
	terms  map[string]termSet
	signer codec.Signer
	calc   *eval.Calculator
}

func NewMerger() *Merger {
	return &Merger{
		terms: make(map[string]termSet),
		calc:  eval.NewCalculator(),
	}
}

func (m *Merger) Add(term expr.Term) {
	m.mu.Lock()
	defer m.mu.Unlock()

	signature := m.signer.Pairs(term.Pairs)

	old := m.terms[signature]
	old.Pairs = term.Pairs
	old.Coeff = m.calc.Add(old.Coeff, term.Coeff)
	m.terms[signature] = old
}

// Flush writes all accumulated terms to enc as a single JSON value and clears the merger.
func (m *Merger) Flush(enc *jsontext.Encoder) error {
	for sig, term := range m.terms {
		if len(term.Coeff.Monomials) == 0 {
			delete(m.terms, sig)
		}
	}

	n := len(m.terms)
	if n == 0 {
		return enc.WriteToken(jsontext.Int(0))
	}
	if n > 1 {
		if err := enc.WriteToken(jsontext.BeginArray); err != nil {
			return err
		}
		if err := enc.WriteToken(jsontext.String(literal.Plus)); err != nil {
			return err
		}
	}
	w := codec.NewWriter(enc)
	for _, term := range m.terms {
		if err := w.WriteTerm(expr.Term{Pairs: term.Pairs, Coeff: term.Coeff}); err != nil {
			return err
		}
	}
	clear(m.terms)
	if n > 1 {
		return enc.WriteToken(jsontext.EndArray)
	}
	return nil
}
