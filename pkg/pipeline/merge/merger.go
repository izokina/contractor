package merge

import (
	"encoding/json"
	"fmt"
	"sync"
	"unsafe"

	"github.com/izokina/contractor/pkg/literal"
	"github.com/izokina/contractor/pkg/pipeline/node"
)

type Merger struct {
	terms map[string]node.Term
	mu    sync.Mutex
}

func NewMerger() *Merger {
	return &Merger{
		terms: make(map[string]node.Term),
	}
}

func (m *Merger) Add(term node.Term) error {
	pairsBytes, err := json.Marshal(term.Pairs)
	if err != nil {
		return fmt.Errorf("Failed to marshal pairs: %w", err)
	}
	signature := unsafe.String(unsafe.SliceData(pairsBytes), len(pairsBytes))

	m.mu.Lock()
	defer m.mu.Unlock()

	oldExpr, _ := m.terms[signature]
	oldExpr.Pairs = term.Pairs
	oldExpr.Scalars = append(oldExpr.Scalars, term.Scalars...)
	m.terms[signature] = oldExpr
	return nil
}

func (m *Merger) Flush() <-chan any {
	cout := make(chan any)
	m.mu.Lock()
	go func() {
		defer close(cout)
		defer m.mu.Unlock()

		for signature, term := range m.terms {
			delete(m.terms, signature)

			object := []any{literal.Times}
			switch len(term.Scalars) {
			case 1:
				object = append(object, term.Scalars[0]...)
			default:
				sum := append(make([]any, 0, len(term.Scalars)+1), literal.Plus)
				for _, extra := range term.Scalars {
					switch len(extra) {
					case 0:
						sum = append(sum, 1)
					case 1:
						sum = append(sum, extra[0])
					default:
						sum = append(sum, append(append(make([]any, 0, len(extra)+1), literal.Times), extra...))
					}
				}
				object = append(object, sum)
			}
			for _, pair := range term.Pairs {
				pairRaw := make([]any, 0, 3)
				pairRaw = append(pairRaw, literal.Pair)
				for _, l := range pair.Lorentz {
					pairRaw = append(pairRaw, json.RawMessage(l.Signature))
				}
				for _, m := range pair.Momentum {
					pairRaw = append(pairRaw, m.Source)
				}
				object = append(object, pairRaw)
			}
			switch len(object) {
			case 1:
				cout <- 1
			case 2:
				cout <- object[1]
			default:
				cout <- object
			}
		}
	}()
	return cout
}
