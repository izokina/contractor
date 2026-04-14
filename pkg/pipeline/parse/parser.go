package parse

import (
	"bytes"
	"cmp"
	"encoding/json/jsontext"
	"fmt"
	"slices"
	"strconv"
	"sync"

	"github.com/izokina/contractor/pkg/literal"
	"github.com/izokina/contractor/pkg/pipeline/node"
)

type termSource struct {
	Len     int
	Iterate func(yield func(node.Term) bool)
}

type Parser struct {
	input *jsontext.Decoder

	lorentz  []node.LorentzIndex
	momentum []node.Momentum

	sBuf []termSource
	aBuf []any

	mu sync.Mutex
}

func NewParser() *Parser {
	return &Parser{
		lorentz:  make([]node.LorentzIndex, 0, 2),
		momentum: make([]node.Momentum, 0, 2),
	}
}

type wrappedError struct {
	error
}

func (p *Parser) ParseJson(input *jsontext.Decoder) (<-chan node.Term, func() error) {
	p.mu.Lock()
	var err error
	cout := make(chan node.Term)
	coutWg := sync.WaitGroup{}
	coutWg.Add(1)
	go func() {
		coutWg.Wait()
		p.mu.Unlock()
		close(cout)
	}()

	errWg := sync.WaitGroup{}
	errWg.Go(func() {
		defer func() {
			coutWg.Done()
			p.input = nil
			if panicError := recover(); panicError != nil {
				if wrapped, ok := panicError.(wrappedError); ok {
					err = wrapped.error
					return
				}
				panic(panicError)
			}
		}()

		p.input = input
		if p.peekKind() != jsontext.KindBeginArray {
			scalar := p.readValue("parsing root scalar")
			coutWg.Go(func() {
				cout <- node.Term{
					Scalars: []any{scalar},
				}
			})
			return
		}

		p.readToken(jsontext.KindBeginArray, "parsing root object")

		name := p.readToken(jsontext.KindString, "expected object name").String()
		if name != literal.Plus {
			source := p.parseNamedExpr(name)
			coutWg.Go(func() {
				for term := range source.Iterate {
					cout <- term
				}
			})
			return
		}

		for p.peekKind() != jsontext.KindEndArray {
			source := p.parseExpr()
			for term := range source.Iterate {
				cout <- term
			}
		}
		p.closeArray()
	})

	return cout, func() error {
		errWg.Wait()
		return err
	}
}

func (p *Parser) parseExpr() termSource {
	if p.peekKind() != jsontext.KindBeginArray {
		return newScalarTermSource(p.readValue("parsing scalar"))
	}

	p.readToken(jsontext.KindBeginArray, "expected array start")
	name := p.readToken(jsontext.KindString, "expected object name").String()
	return p.parseNamedExpr(name)
}

func (p *Parser) parseNamedExpr(name string) termSource {
	switch name {
	case literal.Plus:
		return p.parsePlus()
	case literal.Times:
		return p.parseTimes()
	case literal.Power:
		return p.parsePower()
	case literal.Pair:
		return p.parsePair()
	default:
		return newScalarTermSource(p.parsePartialObject(name))
	}
}

func (p *Parser) parsePlus() termSource {
	sources := p.collectSources()
	totalLen := 0
	for _, source := range sources {
		totalLen += source.Len
	}
	return termSource{
		Len: totalLen,
		Iterate: func(yield func(node.Term) bool) {
			alive := false
			for _, source := range sources {
				source.Iterate(func(term node.Term) bool {
					alive = yield(term)
					return alive
				})
				if !alive {
					return
				}
			}
		},
	}
}

func (p *Parser) parseTimes() termSource {
	sources := p.collectSources()
	totalLen := 1
	for _, source := range sources {
		totalLen *= source.Len
	}

	slices.SortFunc(sources, func(a, b termSource) int {
		return cmp.Compare(a.Len, b.Len)
	})

	return termSource{
		Len: totalLen,
		Iterate: func(yield func(node.Term) bool) {
			multiplySources(node.Term{}, sources, yield)
		},
	}
}

func multiplySources(current node.Term, sources []termSource, yield func(node.Term) bool) bool {
	if len(sources) == 0 {
		return yield(current)
	}
	for term := range sources[0].Iterate {
		next := multiplyTerms(current, term)
		if !multiplySources(next, sources[1:], yield) {
			return false
		}
	}
	return true
}

func multiplyTerms(a, b node.Term) node.Term {
	return node.Term{
		Pairs:   append(append(make([]node.Pair, 0, len(a.Pairs)+len(b.Pairs)), a.Pairs...), b.Pairs...),
		Scalars: append(append(make([]any, 0, len(a.Scalars)+len(b.Scalars)), a.Scalars...), b.Scalars...),
	}
}

func (p *Parser) parsePower() termSource {
	arg := p.readValue("parsing Power object")
	if p.peekKind() != jsontext.KindNumber {
		arg2 := p.readValue("parsing exponent")
		p.readToken(jsontext.KindEndArray, "Power object is expected to have 2 arguments")
		return newScalarTermSource([]any{literal.Power, arg, arg2})
	}
	expString := p.readToken(jsontext.KindNumber, "parsing exponent").String()
	p.readToken(jsontext.KindEndArray, "Power object is expected to have 2 arguments")

	exp := 0
	var err error
	if exp, err = strconv.Atoi(expString); err != nil || exp <= 0 {
		return newScalarTermSource([]any{literal.Power, arg, jsontext.Value(expString)})
	}

	oldInput := p.input
	p.input = jsontext.NewDecoder(bytes.NewReader(arg))
	argExpr := p.parseExpr()
	p.input = oldInput

	totalLen := 1
	for range exp {
		totalLen *= argExpr.Len
	}
	return termSource{
		Len: totalLen,
		Iterate: func(yield func(node.Term) bool) {
			expSource(node.Term{}, argExpr, exp, yield)
		},
	}
}

func expSource(current node.Term, source termSource, power int, yield func(node.Term) bool) bool {
	if power == 0 {
		return yield(current)
	}
	for term := range source.Iterate {
		next := multiplyTerms(current, term)
		if !expSource(next, source, power-1, yield) {
			return false
		}
	}
	return true
}

func (p *Parser) parsePair() termSource {
	for p.peekKind() != jsontext.KindEndArray {
		val := p.parseRaw()
		obj, ok := node.ParseObject(val)
		if !ok {
			p.panicf("Pair argument is not an object")
		}

		switch obj.Name {
		case literal.LorentzIndex:
			l, err := obj.ToLorentzIndex()
			p.assert("parsing LorentzIndex", err)
			p.lorentz = append(p.lorentz, l)
		case literal.Momentum:
			m, err := obj.ToMomentum()
			p.assert("parsing Momentum", err)
			p.momentum = append(p.momentum, m)
		default:
			p.panicf("unknown object in Pair: %s", obj.Name)
		}
	}
	p.closeArray()

	if len(p.lorentz)+len(p.momentum) != 2 {
		p.panicf("Pair object is expected to have 2 arguments")
	}

	pair := node.Pair{}
	pair.Lorentz = append(pair.Lorentz, p.lorentz...)
	pair.Momentum = append(pair.Momentum, p.momentum...)
	term := node.Term{Pairs: []node.Pair{pair}}

	p.lorentz = p.lorentz[:0]
	p.momentum = p.momentum[:0]

	return termSource{
		Len: 1,
		Iterate: func(yield func(node.Term) bool) {
			yield(term)
		},
	}
}

func (p *Parser) parsePartialObject(obj ...any) []any {
	offset := len(p.aBuf)
	for p.peekKind() != jsontext.KindEndArray {
		p.aBuf = append(p.aBuf, p.parseRaw())
	}
	obj = append(obj, p.aBuf[offset:]...)
	for i := offset; i < len(p.aBuf); i++ {
		p.aBuf[i] = nil
	}
	p.aBuf = p.aBuf[:offset]
	p.closeArray()
	return obj
}

func (p *Parser) parseRaw() any {
	switch p.peekKind() {
	case jsontext.KindBeginArray:
		p.readToken(jsontext.KindBeginArray, "expected array")
		return p.parsePartialObject()
	case jsontext.KindString:
		return p.readToken(jsontext.KindString, "parsing string").String()
	}
	return p.readValue("parse number")
}

func (p *Parser) collectSources() []termSource {
	offset := len(p.sBuf)
	for p.peekKind() != jsontext.KindEndArray {
		p.sBuf = append(p.sBuf, p.parseExpr())
	}
	if offset == len(p.sBuf) {
		return nil
	}
	sources := append(make([]termSource, 0, len(p.sBuf)-offset), p.sBuf[offset:]...)
	for i := offset; i < len(p.sBuf); i++ {
		p.sBuf[i] = termSource{}
	}
	p.sBuf = p.sBuf[:offset]
	p.closeArray()
	return sources
}

func (p *Parser) peekKind() jsontext.Kind {
	return p.input.PeekKind()
}

func (p *Parser) readAnyToken(msg string) jsontext.Token {
	token, err := p.input.ReadToken()
	p.assert(msg, err)
	return token
}

func (p *Parser) readToken(kind jsontext.Kind, msg string) jsontext.Token {
	token := p.readAnyToken(msg)
	if token.Kind() != kind {
		p.panicf("%s: unexpected token kind", msg)
	}
	return token
}

func (p *Parser) readValue(msg string) jsontext.Value {
	value, err := p.input.ReadValue()
	p.assert(msg, err)
	return value.Clone()
}

func (p *Parser) closeArray() {
	p.readToken(jsontext.KindEndArray, "closing array bracket")
}

func (p *Parser) assert(msg string, err error) {
	if err != nil {
		p.panicf("%s: %w", msg, err)
	}
}

func (p *Parser) panicf(format string, args ...any) {
	panic(wrappedError{
		error: fmt.Errorf(format, args...),
	})
}

func newScalarTermSource(scalar any) termSource {
	term := node.Term{
		Scalars: []any{scalar},
	}
	return termSource{
		Len: 1,
		Iterate: func(yield func(node.Term) bool) {
			yield(term)
		},
	}
}
