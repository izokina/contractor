// Package intern keys expressions: it builds the key of an expr node from the
// keys of its parts, once, and stores the key on the node.
package intern

import (
	"github.com/izokina/contractor/pkg/pipeline/expr"
	"github.com/izokina/contractor/pkg/pipeline/ident"
)

type Keys struct {
	*ident.Keyer
	parts []ident.ID
}

func NewKeys(t *ident.Table) *Keys {
	return &Keys{Keyer: t.NewKeyer()}
}

// Opaque returns the one node of the value of e, an opaque factor: the first
// node keyed with e's key, which the table stores under it.
func (x *Keys) Opaque(e expr.Expr) expr.Expr {
	id := x.Expr(e)
	if v := x.Value(id); v != nil {
		return v.(expr.Expr)
	}
	x.Store(id, e)
	return e
}

// Expr returns e's key, keying e and the nodes under it that have none yet.
// A key mirrors how its node renders, so that within one context two nodes
// get equal keys exactly when they render identically.
func (x *Keys) Expr(e expr.Expr) ident.ID {
	switch n := e.(type) {
	case *expr.Symbol:
		return n.Key
	case *expr.Atom:
		if n.Key == 0 {
			switch {
			case n.Big != nil:
				n.Key = x.BigInt(n.Big)
			case n.Text != nil:
				n.Key = x.Number(n.Text)
			default:
				n.Key = x.Int(n.Int)
			}
		}
		return n.Key
	case *expr.HeadExpr:
		if n.Key == 0 {
			head := n.Name
			if n.Head != nil {
				head = x.Expr(n.Head)
			}
			from := len(x.parts)
			x.parts = append(x.parts, head)
			x.push(n.Args)
			n.Key = x.node(ident.Head, from)
		}
		return n.Key
	case *expr.PlusExpr:
		if n.Key == 0 {
			from := len(x.parts)
			x.push(n.Children)
			n.Key = x.node(ident.Plus, from)
		}
		return n.Key
	case *expr.TimesExpr:
		if n.Key == 0 {
			from := len(x.parts)
			x.push(n.Children)
			n.Key = x.node(ident.Times, from)
		}
		return n.Key
	case *expr.PowerExpr:
		if n.Key == 0 {
			exp := x.Int(n.ExpInt)
			if n.Exp != (expr.Arg{}) {
				exp = x.Arg(n.Exp)
			}
			n.Key = x.Node(ident.Power, x.Arg(n.Child), exp)
		}
		return n.Key
	case *expr.RationalExpr:
		if n.Key == 0 {
			n.Key = x.Node(ident.Rational, x.Arg(n.Num), x.Arg(n.Den))
		}
		return n.Key
	default:
		c := e.(*expr.ComplexExpr)
		if c.Key == 0 {
			c.Key = x.Node(ident.Complex, x.Arg(c.Re), x.Arg(c.Im))
		}
		return c.Key
	}
}

func (x *Keys) Arg(a expr.Arg) ident.ID {
	if a.Expr != nil {
		return x.Expr(a.Expr)
	}
	s := a.Pair.Slots()
	return x.Node(ident.Pair, ident.ID(s[0]), ident.ID(s[1]))
}

// Label keys the momentum with the given source as it renders, apart from its
// dimension, which contraction can lower, and stores the source under that
// key.
func (x *Keys) Label(source expr.Arg) ident.ID {
	key := x.Node(ident.Momentum, x.Arg(source))
	if x.Value(key) == nil {
		x.Store(key, source)
	}
	return key
}

func (x *Keys) push(args []expr.Arg) {
	for _, a := range args {
		id := x.Arg(a)
		x.parts = append(x.parts, id)
	}
}

// node keys the node of the given kind whose parts are x.parts[from:], and
// drops them. Keying nests, so each level appends past where it started.
func (x *Keys) node(kind ident.Kind, from int) ident.ID {
	id := x.Node(kind, x.parts[from:]...)
	x.parts = x.parts[:from]
	return id
}
