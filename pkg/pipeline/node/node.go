package node

import (
	"encoding/json"
	"fmt"
	"unsafe"

	"github.com/izokina/contractor/pkg/literal"
)

type Object struct {
	Name   string
	Args   []any
	Source any
}

type LorentzIndex struct {
	Index     string
	HasD      bool
	Signature string
}

type Momentum struct {
	Source    any
	Signature string
}

type Pair struct {
	Lorentz  []LorentzIndex
	Momentum []Momentum
}

type Term struct {
	Pairs   []Pair
	Scalars [][]any
}

func ParseObject(source any) (Object, bool) {
	list, ok := source.([]any)
	if !ok || len(list) == 0 {
		return Object{}, false
	}
	name, ok := list[0].(string)
	if !ok {
		return Object{}, false
	}
	return Object{
		Name:   name,
		Args:   list[1:],
		Source: source,
	}, true
}

func (o *Object) stringArgs() ([]string, error) {
	var res []string
	for _, a := range o.Args {
		s, ok := a.(string)
		if !ok {
			return nil, fmt.Errorf("Expected a string arg, got '%v'", a)
		}
		res = append(res, s)
	}
	return res, nil
}

func (o *Object) signature() string {
	bytes, err := json.Marshal(o.Source)
	if err != nil {
		panic(err)
	}
	return unsafe.String(unsafe.SliceData(bytes), len(bytes))
}

func (o *Object) ToLorentzIndex() (LorentzIndex, error) {
	if o.Name != literal.LorentzIndex {
		return LorentzIndex{}, fmt.Errorf("Internal error: Incorrect cast to LorentzIndex")
	}
	args, err := o.stringArgs()
	if err != nil {
		return LorentzIndex{}, fmt.Errorf("Failed to parse LorentzIndex: %w", err)
	}
	if len(args) == 1 {
		return LorentzIndex{
			Index:     args[0],
			HasD:      false,
			Signature: o.signature(),
		}, nil
	}
	if len(args) != 2 {
		return LorentzIndex{}, fmt.Errorf("Unexpected number of args for LorentzIndex: %d", len(args))
	}
	if args[1] != "D" {
		return LorentzIndex{}, fmt.Errorf("Unexpected second arg for LorentzIndex: %s", args[1])
	}
	return LorentzIndex{
		Index:     args[0],
		HasD:      true,
		Signature: o.signature(),
	}, nil
}

func (o *Object) ToMomentum() (Momentum, error) {
	if o.Name != literal.Momentum {
		return Momentum{}, fmt.Errorf("Internal error: Incorrect cast to Momentum")
	}
	return Momentum{
		Source:    o.Source,
		Signature: o.signature(),
	}, nil
}
