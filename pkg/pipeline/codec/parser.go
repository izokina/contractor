package codec

import (
	"bytes"
	"encoding/json/jsontext"
	"io"
	"math"
	"math/big"

	"github.com/izokina/contractor/pkg/pipeline/expr"
	"github.com/izokina/contractor/pkg/pipeline/ident"
	"github.com/izokina/contractor/pkg/pipeline/intern"
)

// maxDepth keeps the output within the encoder's nesting limit of 10000: the
// writer places a part of the input at most four levels deeper than the input
// does. It writes an opaque factor under Plus, Times, Plus and Times, and one
// Power more only for a factor the input held in a product or a power, and a
// momentum source under a Pair and a Momentum, as the input does.
const maxDepth = 10000 - 4

type Parser struct {
	input *jsontext.Decoder

	indices scratch[expr.Slot]
	momenta scratch[expr.Slot]

	sBuf scratch[expr.Arg]

	// heads counts the unknown heads around the current position. Inside
	// one, a Pair is an unknown head too: it is not checked, and renders as
	// written.
	heads int

	t    *ident.Table
	keys *intern.Keys
	text []byte

	dim *expr.Symbol
}

// NewParser returns a parser that keys what it parses in t, and stores there
// the sources of momenta and the one node of each opaque factor.
func NewParser(t *ident.Table) *Parser {
	p := &Parser{
		indices: make(scratch[expr.Slot], 0, 2),
		momenta: make(scratch[expr.Slot], 0, 2),
		t:       t,
		keys:    intern.NewKeys(t),
	}
	p.dim = p.symbol([]byte(DimensionD))
	return p
}

// Dimension returns the parser's one symbol D, the trace of a D-dimensional
// metric.
func (p *Parser) Dimension() *expr.Symbol {
	return p.dim
}

// ParseJson parses the one expression of input and hands emit each root
// summand. Once emit returns false it stops, before the next summand, and
// returns nil.
func (p *Parser) ParseJson(input *jsontext.Decoder, emit func(expr.Arg) bool) (err error) {
	p.input = input
	defer recoverWrapped(&err)
	defer p.reset()

	if p.peekKind() != jsontext.KindBeginArray {
		emit(p.walked(expr.Arg{Expr: p.parseAtom("parsing root scalar")}))
	} else if name, _ := p.openExpr("parsing root object"); string(name) != headPlus {
		emit(p.walked(p.parseNamedExpr(name)))
	} else {
		for p.peekKind() != jsontext.KindEndArray {
			if !emit(p.walked(p.parseExpr())) {
				return nil
			}
		}
		p.closeArray()
	}

	p.readEnd()
	return nil
}

func (p *Parser) readEnd() {
	if _, err := p.input.ReadToken(); err != io.EOF {
		assert("reading past the expression", err)
		panicf("unexpected data after the expression")
	}
}

func (p *Parser) reset() {
	p.input = nil
	p.indices.truncate(0)
	p.momenta.truncate(0)
	p.sBuf.truncate(0)
	p.heads = 0
}

func (p *Parser) parseExpr() expr.Arg {
	if p.peekKind() != jsontext.KindBeginArray {
		return expr.Arg{Expr: p.parseAtom("parsing scalar")}
	}
	if name, named := p.openExpr("parsing expression"); named {
		return p.parseNamedExpr(name)
	}
	return expr.Arg{Expr: p.parseHead(0, true)}
}

// parseNamedExpr reads name before it reads on: name is valid only until the
// next read.
func (p *Parser) parseNamedExpr(name []byte) expr.Arg {
	switch string(name) {
	case headPlus:
		return expr.Arg{Expr: p.parsePlus()}
	case headTimes:
		return expr.Arg{Expr: p.parseTimes()}
	case headPower:
		return expr.Arg{Expr: p.parsePower()}
	case headPair:
		if p.heads == 0 {
			return expr.Arg{Pair: p.parsePair()}
		}
	case headRational:
		return expr.Arg{Expr: expr.NewRationalExpr(p.parseBinaryArgs(headRational))}
	case headComplex:
		return expr.Arg{Expr: expr.NewComplexExpr(p.parseBinaryArgs(headComplex))}
	}
	return expr.Arg{Expr: p.parseHead(p.keys.Symbol(name), false)}
}

// parseHead parses the arguments of an unknown head, and first the head
// itself when it is compound: f[a][b] arrives as [["f", "a"], "b"].
func (p *Parser) parseHead(name ident.ID, compound bool) expr.Expr {
	p.heads++
	h := &expr.HeadExpr{Name: name}
	if compound {
		h.Head = p.parseExpr().Expr
	}
	h.Args = p.collectSources()
	p.heads--
	return h
}

func (p *Parser) parsePlus() expr.Expr {
	return expr.NewPlusExpr(p.collectSources())
}

func (p *Parser) parseTimes() expr.Expr {
	return expr.NewTimesExpr(expr.OneComplex, p.collectSources())
}

func (p *Parser) parsePower() expr.Expr {
	child, exp := p.parseBinaryArgs(headPower)
	if atom, ok := exp.Expr.(*expr.Atom); ok && atom.Big == nil && atom.Text == nil {
		return expr.NewPowerExprInt(child, atom.Int)
	}
	return expr.NewPowerExpr(child, exp)
}

func (p *Parser) parsePair() expr.Pair {
	indicesFrom, momentaFrom := len(p.indices), len(p.momenta)
	for p.peekKind() != jsontext.KindEndArray {
		switch name, _ := p.openExpr("parsing Pair argument"); string(name) {
		case headLorentzIndex:
			p.indices = append(p.indices, p.parseLorentzIndex())
		case headMomentum:
			p.momenta = append(p.momenta, p.parseMomentum())
		default:
			panicf("unknown object in Pair: %s", name)
		}
	}
	p.closeArray()

	indices, momenta := p.indices[indicesFrom:], p.momenta[momentaFrom:]
	if n := len(indices) + len(momenta); n != 2 {
		panicf("Pair expects 2 arguments, got %d", n)
	}
	var slots [2]expr.Slot
	copy(slots[copy(slots[:], indices):], momenta)
	p.indices.truncate(indicesFrom)
	p.momenta.truncate(momentaFrom)
	if a, b := slots[0].Dim(), slots[1].Dim(); a != b {
		panicf("Pair arguments mix %s- and %s-dimensional labels", dimensionNames[min(a, b)], dimensionNames[max(a, b)])
	}
	return expr.NewPair(slots[0], slots[1])
}

var dimensionNames = [...]string{expr.Dim4: "4", expr.DimD: "D", expr.DimDMinus4: "(D-4)"}

func (p *Parser) parseLorentzIndex() expr.Slot {
	kind, text := p.read("parsing LorentzIndex")
	switch kind {
	case jsontext.KindString:
	case jsontext.KindBeginArray:
		panicf("LorentzIndex expects a string name: compound index names are not supported")
	case jsontext.KindEndArray:
		panicf("LorentzIndex expects a string name, got none")
	default:
		panicf("LorentzIndex expects a string name, got %s", kind)
	}
	name := p.keys.Symbol(text)
	return slot(name, true, p.parseDimension(headLorentzIndex))
}

func (p *Parser) parseMomentum() expr.Slot {
	source := p.parseExpr()
	dim := p.parseDimension(headMomentum)
	return slot(p.keys.Label(source), false, dim)
}

func slot(id ident.ID, index bool, dim expr.Dimension) expr.Slot {
	if id > expr.MaxSlotID {
		panicf("too many distinct values: an index or a momentum is key %d, past %d", id, expr.MaxSlotID)
	}
	return expr.NewSlot(id, index, dim)
}

// parseDimension reads the optional dimension label that ends a
// LorentzIndex or a Momentum, and its closing bracket.
func (p *Parser) parseDimension(head string) expr.Dimension {
	if p.peekKind() == jsontext.KindBeginArray {
		p.parseEvanescent(head)
		p.closeArray()
		return expr.DimDMinus4
	}
	kind, label := p.read(head)
	switch kind {
	case jsontext.KindEndArray:
		return expr.Dim4
	case jsontext.KindString:
		if string(label) != DimensionD {
			panicf("unsupported dimension label for %s: %s", head, label)
		}
	default:
		panicf("unsupported dimension label for %s: not a string", head)
	}
	p.closeArray()
	return expr.DimD
}

// evanescent is the tokens of the one compound dimension label, D-4, written
// Plus[-4, D].
var evanescent = [...]struct {
	kind jsontext.Kind
	text string
}{{jsontext.KindString, headPlus}, {jsontext.KindNumber, "-4"}, {jsontext.KindString, DimensionD}, {jsontext.KindEndArray, ""}}

func (p *Parser) parseEvanescent(head string) {
	p.openArray(head)
	for _, want := range evanescent {
		if kind, text := p.read(head); kind != want.kind || string(text) != want.text {
			panicf("unsupported dimension label for %s: compound labels other than D-4 are not supported", head)
		}
	}
}

func (p *Parser) parseBinaryArgs(head string) (expr.Arg, expr.Arg) {
	from := p.pushArgs()
	if n := len(p.sBuf) - from; n != 2 {
		panicf("%s expects 2 arguments, got %d", head, n)
	}
	a, b := p.sBuf[from], p.sBuf[from+1]
	p.sBuf.truncate(from)
	return a, b
}

// parseAtom reads a symbol, the one Symbol of its name, or a number, an Atom
// of its own.
func (p *Parser) parseAtom(msg string) expr.Expr {
	kind, text := p.read(msg)
	switch kind {
	case jsontext.KindString:
		return p.symbol(text)
	case jsontext.KindNumber:
	case jsontext.KindBeginObject:
		panicf("%s: a JSON object is not an expression", msg)
	default:
		panicf("%s: %s is not an expression", msg, kind)
	}
	if v, ok := int32Of(text); ok {
		return &expr.Atom{Int: v}
	}
	if b, ok := new(big.Int).SetString(string(text), 10); ok {
		return &expr.Atom{Big: b}
	}
	return &expr.Atom{Text: bytes.Clone(text)}
}

func (p *Parser) symbol(name []byte) *expr.Symbol {
	id := p.keys.Symbol(name)
	if s, ok := p.t.Value(id).(*expr.Symbol); ok {
		return s
	}
	s := &expr.Symbol{Key: id}
	p.keys.Store(id, s)
	return s
}

// int32Of parses a JSON number that is an integer within int32, without
// allocating.
func int32Of(text []byte) (int32, bool) {
	digits := bytes.TrimPrefix(text, []byte("-"))
	if len(digits) == 0 || len(digits) > 10 {
		return 0, false
	}
	var v int64
	for _, c := range digits {
		if c < '0' || c > '9' {
			return 0, false
		}
		v = v*10 + int64(c-'0')
	}
	if len(digits) < len(text) {
		v = -v
	}
	if v < math.MinInt32 || v > math.MaxInt32 {
		return 0, false
	}
	return int32(v), true
}

func (p *Parser) collectSources() []expr.Arg {
	return p.sBuf.take(p.pushArgs())
}

// pushArgs parses the remaining arguments of an expression onto sBuf,
// closes its array, and returns where the arguments start.
func (p *Parser) pushArgs() int {
	from := len(p.sBuf)
	for p.peekKind() != jsontext.KindEndArray {
		p.sBuf = append(p.sBuf, p.parseExpr())
	}
	p.closeArray()
	return from
}

func (p *Parser) peekKind() jsontext.Kind {
	return p.input.PeekKind()
}

func (p *Parser) readAny(msg string) jsontext.Token {
	token, err := p.input.ReadToken()
	assert(msg, err)
	return token
}

// read reads the next token, and the text of a string, unquoted, or of a
// number, valid only until the next read.
func (p *Parser) read(msg string) (jsontext.Kind, []byte) {
	switch p.peekKind() {
	case jsontext.KindString, jsontext.KindNumber:
	default:
		return p.readAny(msg).Kind(), nil
	}
	v, err := p.input.ReadValue()
	assert(msg, err)
	if v.Kind() == jsontext.KindNumber {
		return jsontext.KindNumber, v
	}
	if bytes.IndexByte(v, '\\') < 0 {
		return jsontext.KindString, v[1 : len(v)-1]
	}
	p.text, err = jsontext.AppendUnquote(p.text[:0], v)
	assert(msg, err)
	return jsontext.KindString, p.text
}

func (p *Parser) readToken(kind jsontext.Kind, msg string) {
	if got := p.readAny(msg).Kind(); got != kind {
		panicf("%s: expected %s, got %s", msg, kind, got)
	}
}

// openExpr opens an expression's array and reads its head symbol, which is
// valid only until the next read. Inside an unknown head, the head may be an
// expression instead: openExpr then leaves it unread and returns false.
func (p *Parser) openExpr(msg string) ([]byte, bool) {
	p.openArray(msg)
	if p.heads > 0 {
		switch p.peekKind() {
		case jsontext.KindString, jsontext.KindEndArray:
		default:
			return nil, false
		}
	}
	kind, name := p.read(msg)
	switch kind {
	case jsontext.KindString:
	case jsontext.KindEndArray:
		panicf("%s: an empty array is not an expression", msg)
	default:
		panicf("%s: the head of an expression must be a string", msg)
	}
	return name, true
}

func (p *Parser) openArray(msg string) {
	p.readToken(jsontext.KindBeginArray, msg)
	if p.input.StackDepth() > maxDepth {
		panicf("input nested deeper than %d levels", maxDepth)
	}
}

func (p *Parser) closeArray() {
	p.readToken(jsontext.KindEndArray, "closing array bracket")
}
