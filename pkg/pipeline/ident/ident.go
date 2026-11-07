// Package ident gives the values of the pipeline their keys, which decide
// identity and never output order, built from the keys of their parts.
package ident

import (
	"encoding/binary"
	"math/big"
	"math/bits"
)

// ID is the key of a value that a Table holds: within one run, two values get
// the same ID exactly when their contents are equal. IDs follow arrival
// order, so they decide identity only. No value has the zero ID.
type ID uint32

// Kind tags the content of a composite key.
type Kind byte

const (
	bigInt Kind = iota
	number
	Plus
	Times
	Power
	Rational
	Complex
	Head
	Pair
	Momentum
)

// Table holds the values keyed in one run, and forgets none. For some IDs it
// also holds a value: a symbol's name, a momentum's source, an opaque factor's
// one node. Only the parse stage keys and stores, through one Keyer, while
// other goroutines read names and values.
type Table struct {
	symbols map[string]ID
	ints    map[int32]ID
	tuples  map[string]ID
	last    ID

	names  column[string]
	values column[any]
}

// column holds an element for each ID, in chunks that are made at full size
// and never move: chunk k holds the IDs of bit length k. Only the parse stage
// sets an element, once, before the ID leaves it, so a goroutine that got the
// ID from the parse stage reads the element without a lock.
type column[T any] [33][]T

// at returns the chunk of id and its index there.
func at(id ID) (int, int) {
	k := bits.Len32(uint32(id))
	return k, int(id) - 1<<k>>1
}

func (c *column[T]) get(id ID) (v T) {
	if k, i := at(id); c[k] != nil {
		v = c[k][i]
	}
	return v
}

func (c *column[T]) set(id ID, v T) {
	k, i := at(id)
	if c[k] == nil {
		c[k] = make([]T, 1<<k>>1)
	}
	c[k][i] = v
}

func NewTable() *Table {
	return &Table{symbols: make(map[string]ID), ints: make(map[int32]ID), tuples: make(map[string]ID)}
}

func (t *Table) Value(id ID) any {
	return t.values.get(id)
}

func (t *Table) Name(id ID) string {
	return t.names.get(id)
}

// Keyer keys values in a Table and stores them.
type Keyer struct {
	t   *Table
	buf []byte
}

func (t *Table) NewKeyer() *Keyer {
	return &Keyer{t: t}
}

func (k *Keyer) Symbol(name []byte) ID {
	id, ok := k.t.symbols[string(name)]
	if !ok {
		s := string(name)
		id = k.t.next()
		k.t.symbols[s] = id
		k.t.names.set(id, s)
	}
	return id
}

func (k *Keyer) Value(id ID) any {
	return k.t.Value(id)
}

// Store gives id the value v. An ID gets a value at most once.
func (k *Keyer) Store(id ID, v any) {
	k.t.values.set(id, v)
}

func (k *Keyer) Int(v int32) ID {
	id, ok := k.t.ints[v]
	if !ok {
		id = k.t.next()
		k.t.ints[v] = id
	}
	return id
}

// BigInt keys an integer beyond int32.
func (k *Keyer) BigInt(v *big.Int) ID {
	k.buf = v.Append(append(k.buf[:0], byte(bigInt)), 10)
	return k.tuple()
}

// Number keys a number that is not an integer by its text.
func (k *Keyer) Number(text []byte) ID {
	k.buf = append(append(k.buf[:0], byte(number)), text...)
	return k.tuple()
}

func (k *Keyer) Node(kind Kind, parts ...ID) ID {
	k.buf = append(k.buf[:0], byte(kind))
	for _, p := range parts {
		k.buf = binary.LittleEndian.AppendUint32(k.buf, uint32(p))
	}
	return k.tuple()
}

func (k *Keyer) tuple() ID {
	if id, ok := k.t.tuples[string(k.buf)]; ok {
		return id
	}
	id := k.t.next()
	k.t.tuples[string(k.buf)] = id
	return id
}

func (t *Table) next() ID {
	t.last++
	return t.last
}

// Hash is the key of a sequence of IDs that no Table holds, such as a word of
// factors or a Pair list. Unlike an ID it is not unique: a user confirms a
// match against the sequence itself.
type Hash uint64

// Add returns the Hash of the sequence that h keys, followed by id.
func (h Hash) Add(id ID) Hash {
	x := (uint64(h) ^ uint64(id)) * 0x9e3779b97f4a7c15
	return Hash(x ^ x>>32)
}
