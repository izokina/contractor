package parse

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/izokina/contractor/pkg/literal"
	"github.com/izokina/contractor/pkg/pipeline/node"
)

type Parser struct {
	scalar []any
	pairs  []node.Pair

	lorentz  []node.LorentzIndex
	momentum []node.Momentum

	mu sync.Mutex
}

func NewParser() *Parser {
	return &Parser{
		lorentz:  make([]node.LorentzIndex, 0, 2),
		momentum: make([]node.Momentum, 0, 2),
	}
}

func (p *Parser) ParseAndExpand(source any) (node.Term, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.scalar = p.scalar[:0]
	p.pairs = p.pairs[:0]

	if err := p.addArg(source); err != nil {
		return node.Term{}, err
	}

	return node.Term{
		Pairs: append(make([]node.Pair, 0, len(p.pairs)), p.pairs...),
		Scalars: [][]any{
			append(make([]any, 0, len(p.scalar)), p.scalar...),
		},
	}, nil
}

func (p *Parser) addArg(arg any) error {
	object, ok := node.ParseObject(arg)
	if !ok {
		p.scalar = append(p.scalar, arg)
		return nil
	}
	var err error
	switch object.Name {
	case literal.Pair:
		err = p.addPair(object)
	case literal.Times:
		err = p.addTimes(object)
	case literal.Power:
		err = p.addPower(object)
	default:
		p.scalar = append(p.scalar, arg)
	}
	return err
}

func (p *Parser) addTimes(object node.Object) error {
	for _, arg := range object.Args {
		if err := p.addArg(arg); err != nil {
			return err
		}
	}
	return nil
}

func (p *Parser) addPower(object node.Object) error {
	if len(object.Args) != 2 {
		return fmt.Errorf("Power is expected to have 2 arguments")
	}
	powerStr, ok := object.Args[1].(json.Number)
	if !ok {
		p.scalar = append(p.scalar, object.Source)
		return nil
	}
	power, err := powerStr.Int64()
	if err != nil || power < 1 {
		// don't want to handle Power[0, 0] and other exotic stuff
		p.scalar = append(p.scalar, object.Source)
		return nil
	}
	for range power {
		if err := p.addArg(object.Args[0]); err != nil {
			return err
		}
	}
	return nil
}

func (p *Parser) addPair(object node.Object) error {
	if len(object.Args) != 2 {
		return fmt.Errorf("Pair is expected to have 2 arguments")
	}

	p.lorentz = p.lorentz[:0]
	p.momentum = p.momentum[:0]

	for _, a := range object.Args {
		o, ok := node.ParseObject(a)
		if !ok {
			return fmt.Errorf("Pair is expected to contain object")
		}
		switch o.Name {
		case literal.LorentzIndex:
			l, err := o.ToLorentzIndex()
			if err != nil {
				return err
			}
			p.lorentz = append(p.lorentz, l)
		case literal.Momentum:
			m, err := o.ToMomentum()
			if err != nil {
				return err
			}
			p.momentum = append(p.momentum, m)
		default:
			return fmt.Errorf("Pair is expected to contain either LorentzIndex or Momentum")
		}
	}

	pair := node.Pair{}
	pair.Lorentz = append(pair.Lorentz, p.lorentz...)
	pair.Momentum = append(pair.Momentum, p.momentum...)
	p.pairs = append(p.pairs, pair)
	return nil
}
