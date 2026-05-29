package codec

import (
	"bytes"
	"encoding/json/jsontext"

	"github.com/izokina/contractor/pkg/literal"
	"github.com/izokina/contractor/pkg/pipeline/expr"
)

// Signer builds canonical JSON signatures for index types and pairs.
// It reuses an internal buffer and a single jsontext.Encoder across calls;
// not safe for concurrent use.
type Signer struct {
	buf bytes.Buffer
	enc *jsontext.Encoder
	w   *Writer
}

func (s *Signer) reset() *jsontext.Encoder {
	s.buf.Reset()
	if s.enc == nil {
		s.enc = jsontext.NewEncoder(&s.buf)
		s.w = NewWriter(s.enc)
	} else {
		s.enc.Reset(&s.buf)
	}
	return s.enc
}

func (s *Signer) LorentzIndex(index string, hasD bool) string {
	enc := s.reset()
	enc.WriteToken(jsontext.BeginArray)
	enc.WriteToken(jsontext.String(literal.LorentzIndex))
	enc.WriteToken(jsontext.String(index))
	if hasD {
		enc.WriteToken(jsontext.String("D"))
	}
	enc.WriteToken(jsontext.EndArray)
	return s.buf.String()
}

func (s *Signer) Momentum(source expr.Expr, hasD bool) string {
	enc := s.reset()
	enc.WriteToken(jsontext.BeginArray)
	enc.WriteToken(jsontext.String(literal.Momentum))
	s.w.WriteExpr(source)
	if hasD {
		enc.WriteToken(jsontext.String("D"))
	}
	enc.WriteToken(jsontext.EndArray)
	return s.buf.String()
}

func (s *Signer) Pairs(pairs []expr.Pair) string {
	enc := s.reset()
	enc.WriteToken(jsontext.BeginArray)
	for _, p := range pairs {
		s.w.WriteExpr(p)
	}
	enc.WriteToken(jsontext.EndArray)
	return s.buf.String()
}

// Expr returns the canonical JSON encoding of n, suitable for use as a
// signature key. Equivalent to a one-shot Writer.WriteExpr against a
// fresh buffer.
func (s *Signer) Expr(n expr.Expr) string {
	s.reset()
	s.w.WriteExpr(n)
	return s.buf.String()
}
