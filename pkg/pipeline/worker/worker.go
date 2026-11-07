// Package worker runs the pipeline on a fixed number of generic workers. Each
// worker does the earliest work available: parsing, then walking parsed
// summands, then contracting terms into the merger.
package worker

import (
	"encoding/json/jsontext"
	"io"
	"iter"
	"sync"
	"sync/atomic"

	"github.com/izokina/contractor/pkg/pipeline/codec"
	"github.com/izokina/contractor/pkg/pipeline/contract"
	"github.com/izokina/contractor/pkg/pipeline/expr"
	"github.com/izokina/contractor/pkg/pipeline/ident"
	"github.com/izokina/contractor/pkg/pipeline/merge"
	"github.com/izokina/contractor/pkg/pipeline/walk"
)

// A batch holds batchSize terms, or summands expected to yield as many, each
// counted as at least one so that a batch stays bounded. Each queue holds a
// few batches: a summand is held whole, and handing off a batch at a time
// keeps the workers off the queues.
const (
	batchSize     = 64
	itemsBuffer   = 4
	batchesBuffer = 4
)

// batch holds terms, and their Pairs, monomials and opaque factors in slices
// of its own. It has one owner at a time, which hands it on through the
// batches queue or, once its terms are contracted, puts it back on the free
// list; nothing refers to it after that.
type batch struct {
	terms     []expr.Term
	pairs     []expr.Pair
	monomials []expr.Monomial
	factors   []expr.Factor
}

func (b *batch) add(t expr.Term) {
	from := len(b.pairs)
	b.pairs = append(b.pairs, t.Pairs...)
	t.Pairs = b.pairs[from:]
	from = len(b.monomials)
	for _, m := range t.Coeff.Monomials {
		f := len(b.factors)
		b.factors = append(b.factors, m.Opaques...)
		m.Opaques = b.factors[f:]
		b.monomials = append(b.monomials, m)
	}
	t.Coeff.Monomials = b.monomials[from:]
	b.terms = append(b.terms, t)
}

type stages struct {
	// next resumes the parser up to the next summand. Only the holder of
	// token calls it and sends on items, so a send after a room check never
	// blocks.
	next  func() (expr.Arg, bool)
	token chan struct{}
	items chan []expr.Arg

	batches chan *batch
	// free holds contracted batches for the next batch any worker starts. A
	// worker holds its own batch and at most one it contracts, so at most
	// 2*threads + batchesBuffer batches are ever made.
	free      chan *batch
	producers atomic.Int64

	failed atomic.Bool
	mu     sync.Mutex
	err    error
}

// Run parses in, keying what it parses in t, walks each root summand and
// contracts its terms into merger on threads workers. It returns the merger's
// error if there is one, or else the parser's.
func Run(in io.Reader, t *ident.Table, merger *merge.Merger, threads int) error {
	s := &stages{
		token:   make(chan struct{}, 1),
		items:   make(chan []expr.Arg, itemsBuffer),
		batches: make(chan *batch, batchesBuffer),
		free:    make(chan *batch, 2*threads+batchesBuffer),
	}
	s.producers.Store(int64(threads))
	parser := codec.NewParser(t)
	var parseErr error
	var stop func()
	s.next, stop = iter.Pull(func(yield func(expr.Arg) bool) {
		if parseErr = parser.ParseJson(jsontext.NewDecoder(in), yield); parseErr != nil {
			s.failed.Store(true)
		}
	})
	s.token <- struct{}{}

	var wg sync.WaitGroup
	for range threads {
		wg.Go(newWorker(s, parser.Dimension(), merger.NewInput()).run)
	}
	wg.Wait()
	// The merger's failure may leave the parse waiting at its next summand.
	stop()
	if s.err != nil {
		return s.err
	}
	return parseErr
}

func (s *stages) fail(err error) {
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.mu.Unlock()
	s.failed.Store(true)
}

func (s *stages) get() *batch {
	select {
	case b := <-s.free:
		return b
	default:
		return &batch{terms: make([]expr.Term, 0, batchSize)}
	}
}

// put clears b, so that it keeps no coefficient or node alive, and puts it
// on the free list.
func (s *stages) put(b *batch) {
	clear(b.terms)
	clear(b.monomials)
	clear(b.factors)
	b.terms, b.pairs, b.monomials, b.factors = b.terms[:0], b.pairs[:0], b.monomials[:0], b.factors[:0]
	select {
	case s.free <- b:
	default:
	}
}

type worker struct {
	*stages
	walker     *walk.Walker
	contractor *contract.Contractor
	input      *merge.Input
	batch      *batch
}

func newWorker(s *stages, d *expr.Symbol, input *merge.Input) *worker {
	return &worker{
		stages:     s,
		walker:     walk.NewWalker(contract.NewContractor(d)),
		contractor: contract.NewContractor(d),
		input:      input,
		batch:      s.get(),
	}
}

func (w *worker) run() {
	w.produce()
	if w.producers.Add(-1) == 0 {
		close(w.batches)
	}
	for b := range w.batches {
		w.contract(b)
	}
}

// produce returns once items is closed and drained. It blocks, and returns,
// only with its own batch contracted, so no term is left behind when the last
// producer closes batches.
func (w *worker) produce() {
	for {
		if w.feed(false) {
			continue
		}
		select {
		case items, ok := <-w.items:
			if !ok {
				w.flush()
				return
			}
			w.walk(items)
			continue
		default:
		}
		select {
		case b := <-w.batches:
			w.contract(b)
			continue
		default:
		}
		if len(w.batch.terms) > 0 {
			w.flush()
			continue
		}
		select {
		case <-w.token:
			w.parse(false)
		case items, ok := <-w.items:
			if !ok {
				return
			}
			w.walk(items)
		case b := <-w.batches:
			w.contract(b)
		}
	}
}

func (w *worker) feed(inWalk bool) bool {
	if len(w.items) == cap(w.items) {
		return false
	}
	select {
	case <-w.token:
		w.parse(inWalk)
		return true
	default:
		return false
	}
}

// parse queues batches of summands while items has room, then hands the token
// back; inside a walk it queues one, to return to the walk soon. At the end of
// input, or once the run has failed, it closes items and keeps the token.
func (w *worker) parse(inWalk bool) {
	for len(w.items) < cap(w.items) {
		var items []expr.Arg
		for size := 0; size < batchSize; {
			e, ok := w.next()
			if !ok || w.failed.Load() {
				if len(items) > 0 {
					w.items <- items
				}
				close(w.items)
				return
			}
			items = append(items, e)
			size += max(1, min(e.Len(), batchSize))
		}
		w.items <- items
		if inWalk {
			break
		}
	}
	w.token <- struct{}{}
}

// walk takes each summand's terms into the worker's batch. After each full
// batch it feeds the serial stage, since a walk may be long, and it stops
// there once the run has failed.
func (w *worker) walk(items []expr.Arg) {
	for _, e := range items {
		if w.failed.Load() {
			return
		}
		w.walker.Walk(e)
		for t, ok := w.walker.Next(); ok; t, ok = w.walker.Next() {
			if w.batch.add(t); len(w.batch.terms) < batchSize {
				continue
			}
			if w.failed.Load() {
				return
			}
			select {
			case w.batches <- w.batch:
				w.batch = w.get()
			default:
				w.flush()
			}
			w.feed(true)
		}
	}
}

func (w *worker) flush() {
	w.contract(w.batch)
	w.batch = w.get()
}

// contract contracts the terms of b into the merger and puts b back. The
// contractor's Pairs are valid only until its next term, so each term that
// survives takes a copy after b's own; its coefficient is b's, or made for it
// alone.
func (w *worker) contract(b *batch) {
	defer w.put(b)
	if w.failed.Load() {
		return
	}
	kept := b.terms[:0]
	for _, t := range b.terms {
		if u, ok := w.contractor.ContractAndNormalize(t); ok {
			from := len(b.pairs)
			b.pairs = append(b.pairs, u.Pairs...)
			u.Pairs = b.pairs[from:]
			kept = append(kept, u)
		}
	}
	if len(kept) == 0 {
		return
	}
	if err := w.input.Add(kept); err != nil {
		w.fail(err)
	}
}
