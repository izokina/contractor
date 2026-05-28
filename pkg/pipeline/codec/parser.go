package codec

import (
	"cmp"
	"encoding/json/jsontext"
	"slices"
	"strconv"

	"github.com/izokina/contractor/pkg/literal"
	"github.com/izokina/contractor/pkg/pipeline/expr"
)

type Parser struct {
	input *jsontext.Decoder

	lorentz  []expr.LorentzIndex
	momentum []expr.Momentum

	sBuf   []expr.Expr
	aBuf   []any
	signer Signer
}

func NewParser() *Parser {
	return &Parser{
		lorentz:  make([]expr.LorentzIndex, 0, 2),
		momentum: make([]expr.Momentum, 0, 2),
	}
}

func (p *Parser) ParseJson(input *jsontext.Decoder, emit func(expr.Expr)) (err error) {
	p.input = input
	defer recoverWrapped(&err)
	defer func() { p.input = nil }()

	if p.peekKind() != jsontext.KindBeginArray {
		emit(expr.NewAtom(p.parseScalar("parsing root scalar")))
		return nil
	}
	p.readToken(jsontext.KindBeginArray, "parsing root object")
	name := p.readToken(jsontext.KindString, "expected object name").String()
	if name != literal.Plus {
		emit(p.parseNamedExpr(name))
		return nil
	}
	for p.peekKind() != jsontext.KindEndArray {
		emit(p.parseExpr())
	}
	p.closeArray()
	return nil
}

func (p *Parser) parseExpr() expr.Expr {
	if p.peekKind() != jsontext.KindBeginArray {
		return expr.NewAtom(p.parseScalar("parsing scalar"))
	}

	p.readToken(jsontext.KindBeginArray, "expected array start")
	name := p.readToken(jsontext.KindString, "expected object name").String()
	return p.parseNamedExpr(name)
}

func (p *Parser) parseNamedExpr(name string) expr.Expr {
	switch name {
	case literal.Plus:
		return p.parsePlus()
	case literal.Times:
		return p.parseTimes()
	case literal.Power:
		return p.parsePower()
	case literal.Pair:
		return p.parsePair()
	case literal.Rational:
		num, den := p.parseBinaryArgs()
		return &expr.RationalExpr{Num: num, Den: den}
	case literal.Complex:
		re, im := p.parseBinaryArgs()
		return &expr.ComplexExpr{Re: re, Im: im}
	default:
		return expr.NewAtom(p.parsePartialObject(name))
	}
}

func (p *Parser) parsePlus() expr.Expr {
	return expr.NewPlusExpr(p.collectSources())
}

func (p *Parser) parseTimes() expr.Expr {
	sources := p.collectSources()
	slices.SortFunc(sources, func(a, b expr.Expr) int { return cmp.Compare(a.Len(), b.Len()) })
	return expr.NewTimesExpr(sources)
}

func (p *Parser) parsePower() expr.Expr {
	child := p.parseExpr()
	exp := p.parseExpr()
	p.closeArray()

	if atom, ok := exp.(*expr.Atom); ok {
		if v, ok := atom.Value.(int32); ok {
			return expr.NewPowerExprInt(child, v)
		}
	}
	return expr.NewPowerExpr(child, exp)
}

func (p *Parser) parsePair() expr.Expr {
	for p.peekKind() != jsontext.KindEndArray {
		p.readToken(jsontext.KindBeginArray, "parsing Pair element")
		name := p.readToken(jsontext.KindString, "expected object name in Pair").String()
		switch name {
		case literal.LorentzIndex:
			p.lorentz = append(p.lorentz, p.parseLorentzIndex())
		case literal.Momentum:
			p.momentum = append(p.momentum, p.parseMomentum())
		default:
			panicf("unknown object in Pair: %s", name)
		}
	}
	p.closeArray()

	if len(p.lorentz)+len(p.momentum) != 2 {
		panicf("Pair object is expected to have 2 arguments")
	}

	pair := expr.Pair{}
	pair.Lorentz = append(pair.Lorentz, p.lorentz...)
	pair.Momentum = append(pair.Momentum, p.momentum...)

	p.lorentz = p.lorentz[:0]
	p.momentum = p.momentum[:0]

	return pair
}

func (p *Parser) parseLorentzIndex() expr.LorentzIndex {
	index := p.readToken(jsontext.KindString, "parsing LorentzIndex.Index").String()
	hasD := false
	if p.peekKind() == jsontext.KindString {
		second := p.readToken(jsontext.KindString, "parsing LorentzIndex second arg").String()
		if second != "D" {
			panicf("unexpected second arg for LorentzIndex: %s", second)
		}
		hasD = true
	}
	p.closeArray()
	return expr.LorentzIndex{Index: index, HasD: hasD, Signature: p.signer.LorentzIndex(index, hasD)}
}

func (p *Parser) parseMomentum() expr.Momentum {
	source := p.parseExpr()
	hasD := false
	if p.peekKind() == jsontext.KindString {
		second := p.readToken(jsontext.KindString, "parsing Momentum second arg").String()
		if second != "D" {
			panicf("unexpected second arg for Momentum: %s", second)
		}
		hasD = true
	}
	p.closeArray()
	return expr.Momentum{Source: source, HasD: hasD, Signature: p.signer.Momentum(source, hasD)}
}

func (p *Parser) parseBinaryArgs() (expr.Expr, expr.Expr) {
	a := p.parseExpr()
	b := p.parseExpr()
	p.closeArray()
	return a, b
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

func (p *Parser) parseScalar(msg string) any {
	switch p.peekKind() {
	case jsontext.KindString:
		return p.readToken(jsontext.KindString, msg).String()
	case jsontext.KindNumber:
		tok := p.readToken(jsontext.KindNumber, msg)
		if v, err := strconv.ParseInt(tok.String(), 10, 32); err == nil {
			return int32(v)
		}
		return jsontext.Value(tok.String())
	default:
		return p.readValue(msg)
	}
}

func (p *Parser) parseRaw() any {
	if p.peekKind() == jsontext.KindBeginArray {
		p.readToken(jsontext.KindBeginArray, "expected array")
		return p.parsePartialObject()
	}
	return p.parseScalar("parsing raw value")
}

func (p *Parser) collectSources() []expr.Expr {
	offset := len(p.sBuf)
	for p.peekKind() != jsontext.KindEndArray {
		p.sBuf = append(p.sBuf, p.parseExpr())
	}
	if offset == len(p.sBuf) {
		return nil
	}
	sources := append(make([]expr.Expr, 0, len(p.sBuf)-offset), p.sBuf[offset:]...)
	for i := offset; i < len(p.sBuf); i++ {
		p.sBuf[i] = nil
	}
	p.sBuf = p.sBuf[:offset]
	p.closeArray()
	return sources
}

func (p *Parser) peekKind() jsontext.Kind {
	return p.input.PeekKind()
}

func (p *Parser) readToken(kind jsontext.Kind, msg string) jsontext.Token {
	token, err := p.input.ReadToken()
	assert(msg, err)
	if token.Kind() != kind {
		panicf("%s: unexpected token kind", msg)
	}
	return token
}

func (p *Parser) readValue(msg string) jsontext.Value {
	value, err := p.input.ReadValue()
	assert(msg, err)
	return value.Clone()
}

func (p *Parser) closeArray() {
	p.readToken(jsontext.KindEndArray, "closing array bracket")
}
