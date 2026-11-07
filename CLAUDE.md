# CLAUDE.md — FeynGrav Index Contractor

<!--
This file is the landing briefing — invariants, gotchas, and pointers.
Authoritative deeper references:
  docs/architecture.md         — pipeline, package layout, identity, output order, component invariants
  docs/samples.md              — worked examples + known-flaw catalog
  docs/scalar-normal-form.md   — coefficient algebra (incl. §5b render-faithfully contract)
  docs/known-limitations.md    — parsing, malformed index use, walk, exponent, output order, merger memory,
                                 momentum, opaque, non-integer-literal gaps
  docs/feyncalc-notation.md    — FeynCalc-grounded physics interpretation of the input
                                 language (dimension labels, Pair = metric / vector / scalar
                                 product, contraction rules) + Mathematica StartProcess wrapper
  docs/contractor.1            — man page
  README.md                    — human-facing onboarding
-->

## Critical Facts — Read First

1. **Index contraction kernel.** Contracts repeated Lorentz tensor indices, folds coefficient arithmetic (exact Rational / Complex — int32 fast path + `math/big` fallback) in the parser and `eval`, merges like terms in `eval.Sum`, drops zero monomials, and writes the output in a content order that `Merger.Flush` imposes at every level, so it is byte-identical across thread counts and summand orders. `Power[_, 0]` is intentionally opaque (`0^0` ambiguity). Index use is not checked: a name used more than twice in a term is paired greedily (`docs/known-limitations.md` §Malformed index use). → `docs/architecture.md` §Pipeline, §Output order, §Why coefficients flow as `expr.Scalar`; `docs/scalar-normal-form.md` §4.3, §6.
2. **The dimension changes the contraction result.** A slot's `Dim()` is an `expr.Dimension`: `Dim4` (no label, traces to `4`), `DimD` (`"D"`) or `DimDMinus4` (`["Plus", -4, "D"]`, a label only in a dimension slot). It mirrors FeynCalc's *dimension label* (`LorentzIndex[mu, D]` / `Momentum[p, D]` — `D = 4 − 2ε` in dimensional regularization); it is **not** a derivative-index marker. `DimD = Dim4 | DimDMinus4`: indices match by name alone, a contraction writes the meet `a & b` into both slots of the Pair it makes (`Slot.WithDim`), and an empty meet (4 with `D-4`) drops the term. The parser rejects a `Pair` whose two arguments differ in dimension. → `docs/feyncalc-notation.md` §Dimension-Aware Lorentz Contractions.
3. **Identity comes from keys, never from rendered text.** A run-scoped `ident.Table` gives each value the parser reads an `ident.ID` (equal content, equal ID); only the parse stage writes it, through `intern.Keys`. A Pair is two packed slots in a `uint64`, not an `expr.Expr`: a node holds it inline in an `expr.Arg`. An opaque factor is the one node of its value (`Keys.Opaque`), so two are equal exactly when they are the same pointer. A term's Pair list and a monomial's word get an `ident.Hash`, confirmed against the parts on a match. Equal keys mean identical rendering (the symbol `"3"` and the integer `3` differ); IDs follow arrival order, so they never order output. Workers read names and momentum sources from the table without a lock, since each element is set once, before its ID leaves the parser, in chunks that never move (`ident.column`). **`expr.Expands` alone decides what is multiplied out**, for both `Parser.walked` (`codec/walked.go`) and `eval.Folder`; change it there, never in the folder's switch. → `docs/architecture.md` §Identity.
4. **Values are shared in flight, never mutated.** No stage mutates a `Scalar`, a `Monomial.Opaques` array or a term's Pairs it has handed on, so a product of two coefficients may share slices with either. `Walker.Next` and `ContractAndNormalize` return terms in buffers valid until their next call; the worker copies each term into its batch (`worker.go`), and an `eval.Sum` copies the Pairs and opaque factors it keeps. Only `Merger.Flush`, after the workers are done, sorts slices in place. → `docs/architecture.md` §Component Invariants > Contractor, > Merger.
5. **Prefer streaming over materialization.** `worker.Run` runs every stage on `-threads` generic workers, each taking the earliest work available; parsing is serial, one worker at a time, and work moves in batches through small fixed queues (`batchSize`, `itemsBuffer`, `batchesBuffer`) that bound memory. A `walk.Walker` is a pull state machine (`Walk`, then `Next`). `Walker.product` is the one place that decides what a product holds: a factor of one term is one row; a factor that closes an index name inside itself, and a group of factors that share open names, is multiplied out and merged; every other factor is streamed, walked again for each combination of the slower ones. Once a summand is read, `Parser.walked` folds each product's numbers into `TimesExpr.Coeff`, expands momentum combinations and orders factors by `Len`, ascending and stable. → `docs/architecture.md` §Pipeline, §Walker design.
6. **Optimize allocations aggressively.** Reuse buffers rather than reallocating. A buffer that recursive code shares is a `scratch` (`codec/scratch.go`): each user appends past the length it found, then `take`s or `truncate`s back to it; one no recursion reaches may be reset with `buf = buf[:0]`. Look a byte key up as `m[string(b)]`, which does not copy (`ident.Keyer`), and read tokens as bytes (`Parser.read`), never through `Token.String()`. Slots and Pairs hold no pointers, so the garbage collector does not scan their slices. Use `append(make([]T, 0, n), ...)` for sized copies.
7. **Build requires Go 1.27.** `encoding/json/v2` and `encoding/json/jsontext` are standard library from 1.27 on, so no `GOEXPERIMENT` is needed and `go vet ./cmd/... ./pkg/...` runs without extra flags. Use `make build` (writes to `./bin/contractor`). A bare `go build ./cmd/contractor` drops the binary in the *current directory* — prefer the Makefile target. CI runs vet and `./scripts/run-tests.sh` on every push; a tag `v<Version>` releases binaries (`.github/workflows/release.yml`), and fails unless it matches `Version` in `main.go`.
8. **I/O is Mathematica ExpressionJSON via stdio.** One JSON expression in, one out; a rejection exits 1 with a message on stderr, which `main.go` keeps ASCII. No Go test files exist — verification goes through the `tester` agent, which runs `./scripts/run-tests.sh` (builds `bin/contractor` and `bin/contractor-race`, then runs `scripts/check-fixtures.py` on both, as is and with `-merge-memory=1`) against the bundled fixture `scripts/sample-fixtures.json`. See `docs/samples.md` for the worked examples (regular + known-flaw rows).
9. **Components must render faithfully; normal form is optional.** The atomisation gap (`scalar-normal-form.md` §5 Decision C1) can leave a monomial carrying an opaque factor whose node is not itself canonical — e.g. a parsed `Power[Plus[2,3], x]` or `Times[1, x]` whose sub-trees were keyed as-is. Downstream components (writer, merger) treat their input as "sum of parts" and render each factor's node verbatim, never silently dropping or re-interpreting a factor; see `docs/scalar-normal-form.md` §5b. The writer satisfies the contract structurally: `writeMonomial` only suppresses its `Coeff` factor when `m.Coeff == expr.OneComplex`, and the node of each opaque factor flows through `writeExpr` unsuppressed. **Open gap (will revisit):** a parsed `Times[1, x]` embedded in an opaque sub-tree round-trips as `["Times", 1, x]` rather than `x`; the multiplicative-identity collapse for parsed-Times children is not implemented. The natural site if it lands is `writeExpr`'s `*expr.TimesExpr` arm (filter `Atom{Int: 1}` children of parsed Times only, never a monomial's opaque factors) — otherwise render-faithfully breaks.

---

## Core Types (`pkg/pipeline/expr/`)

```go
// Minimal interface; all behavior is in walk.Walker / eval.Folder / codec.Writer.
// Other node types: Symbol (a name, one per name), HeadExpr (an unknown head),
// PlusExpr, TimesExpr (with a Coeff where the walk reaches it), RationalExpr, ComplexExpr.
type Expr interface {
    Len() int       // terms of the expansion before anything merges; saturates
    IsScalar() bool // false only for composites that expand to a Pair
}

// A node as the tree holds it: a Pair inline (Expr nil), or any other node.
type Arg struct {
    Expr Expr
    Pair Pair
}

type Term struct {
    Pairs []Pair
    Coeff Scalar // sum-of-monomials normal form; OneScalar is the multiplicative identity
}

// Metric, Lorentz vector or scalar product: two Slots (uint32: an ID, an
// index bit, a Dimension), index slots first. See docs/feyncalc-notation.md.
type Pair uint64

// A Complex coefficient times its opaque factors, in key order until the flush.
type Monomial struct {
    Coeff   Complex
    Opaques []Factor
}

// An opaque factor, the one node of its value, to a positive power.
type Factor struct {
    Node Expr
    Exp  int32
}

// A number: an int32 in Int, an integer beyond int32 in Big, or a non-integer's text.
type Atom struct {
    Int  int32
    Big  *big.Int
    Text jsontext.Value
    Key  ident.ID
}

// PowerExpr: when Exp is the zero Arg, the parser cached the int32 exponent
// into ExpInt. Otherwise ExpInt holds the zero value (0). See PowerExpr bullet below.
type PowerExpr struct {
    Child  Arg
    Exp    Arg   // the zero Arg when the exponent is a cached int32 in ExpInt
    ExpInt int32 // valid when Exp is the zero Arg
    Key    ident.ID
    len    int
}
```

**`Writer` wraps an encoder, the `ident.Table` and a reusable big-int scratch buffer, so it is single-goroutine** (the merger makes one in its single-threaded `Flush`). The scratch backs allocation-free `writeBigInt`; it is safe because `enc.WriteValue` copies its argument before returning. Two exported methods, both panic-protected, stream the whole output: `Add(pairs, monomial)` takes the monomials in output order, each with its term's Pairs, and `Close()` ends it — `0`, the bare addend, or `["Plus", addend...]`. Each pair-free monomial is an addend, and so is each tensor term, a run of monomials with equal Pairs; the writer holds the first addend until a second begins, so the caller must not change what it has handed over. One monomial is `writeMonomial(m, pairs...)`: `Times[(Coeff if != 1), factor..., pair...]` with `0 → 1`, `1 → bare element`, `N → ["Times", ...]`, counted up front, each factor of exponent `n > 1` as `["Power", node, n]`; a tensor term of several is `["Times", ["Plus", monomial...], pair...]`. `writeExpr` emits any Expr as a fixed-shape JSON value (no flatten, no identity collapse — canonical Mathematica never nests Times-in-Times or Plus-in-Plus).

<!--
codec uses a uniform panic-based error model (errors.go) so inner
helpers can stay error-return-free. Parser.ParseJson, Writer.Add
and Writer.Close are recover-shell entry points. New
codec code that needs to fail should panic via the free helpers
(panicf, wrap, assert) defined in errors.go, never grow back into
returning errors from inner helpers.
-->
**Codec uses panic-based control flow internally.** `pkg/pipeline/codec/errors.go` defines `wrappedError`, `recoverWrapped(*err)`, `panicf(format, args)`, `wrap(err)`, and `assert(msg, err)` as free functions. Exported entry points (`Parser.ParseJson`, `Writer.Add`, `Writer.Close`) install `defer recoverWrapped(&err)` and return wrapped errors normally; inner helpers panic via the free functions. Anything called from outside this protection must still return errors.

**`PowerExpr.Exp == expr.Arg{}` means the exponent was a cached int32 in `ExpInt`.** Otherwise `Exp` is a generic Arg (a Pair is never the zero Arg, since a slot's dimension bits are never 0) and `ExpInt` is at zero value. `expr.Expands` and `IsScalar` discriminate via `ExpInt > 0` directly — the zero coincidence covers both "non-positive cached" (don't expand) and "symbolic Exp" (don't expand) in one comparison. The writer emits `["Power", child, exp]` either way: when `Exp` is set it writes the Arg; otherwise it writes `jsontext.Int(int64(ExpInt))` directly (no Atom allocation in this hot path).

**`PowerExpr.IsScalar()` is `p.ExpInt <= 0 || p.Child.IsScalar()`.** Non-expandable (ExpInt ≤ 0) is always scalar; expandable with a scalar child is *also* scalar (e.g. `Power[κ, 2]` where κ is a Symbol). Code that branches on Power scalarity must distinguish all four `(expandable, child-scalar)` cases — do not lump "scalar Power" into "non-expanding Power".

---

## I/O at a glance

```json
["Plus",
  ["Times", 2, ["Pair", ["LorentzIndex", "mu"], ["LorentzIndex", "nu"]]],
  ["Times", 3, ["Pair", ["LorentzIndex", "mu"], ["LorentzIndex", "mu"]]]
]
```

The second term's repeated `mu` contracts and the merger collapses `3 * 4 → 12`; pair-free addends come first:

```json
["Plus",
  12,
  ["Times", 2, ["Pair", ["LorentzIndex", "mu"], ["LorentzIndex", "nu"]]]
]
```

`["LorentzIndex", "mu", "D"]` contracts to `"D"`, `["LorentzIndex", "mu", ["Plus", -4, "D"]]` to `["Plus", -4, "D"]`, and the two-arg form to `4`. For the full catalog see `docs/samples.md`; user-visible limitations and the output order are in `docs/known-limitations.md`.

---

## Coding Guidelines for This Project

### Test discipline
- **Sample verification goes through the `tester` agent — trust its verdict.** Spawn it via the Agent tool (`subagent_type: "tester"`) for any behavioural verification of a code change; never run `make build`, `go build`, `scripts/run-tests.sh`, or `python3 scripts/check-fixtures.py` from the main conversation. The agent runs in isolation: it builds the contractor, runs each fixture input 10 times at each of `-threads 1` and `8` plus a race pass via `scripts/check-fixtures.py`, then all of it again with `-merge-memory=1`, and returns a classified verdict so the raw output stays out of your context. The Python script compares exactly at every level, order included, and JSON types too (the number `5` is not the string `"5"`): the output order is fixed by content (`docs/architecture.md` §Output order), so any reorder is a real change. To make the judgement reliable, brief the agent: state what behavioural change you made (e.g. *"Power[_, 0] now folds to 1"*, *"unknown heads now rank before Power"*) so it can edit the affected `outputs` in `scripts/sample-fixtures.json` before testing. The agent may rewrite fixture `outputs` or `error`, but never inputs, and never edits docs — when it reports a `spec shift` (or graduates a known-flaw row), updating `docs/samples.md` is *your* job in the same change, not the agent's. When it reports a `regression`, take that at face value.
- **Verify input shapes against real samples.** Don't invent test inputs; check `scripts/sample-fixtures.json` and `docs/samples.md` first.
- **Certify a behaviour change with rows that fail under the old behaviour,** on each side of each boundary; a row whose output is the same before and after certifies nothing.
- **The fixture sweep covers the contraction pipeline, not CLI surface.** Flag-only changes (new `-foo` flags, `flag.Usage`/banner text, version output) pass the tester trivially because the suite only exercises stdin→stdout. Certify those with a manual `./bin/contractor -<flag>` check too.

### Workflow
- **Scope tasks to specific files.** State exactly which files to touch. Unbounded tasks cause unnecessary exploration.
- **Use the plan/apply pattern.** For non-trivial changes, ask for a plan first, review it, then execute. Subtle bugs in contraction or scalar logic are hard to spot after the fact.

### Code shape
- **Default to writing no comments.** Function names plus type signatures already document what code does. Add a comment only when removing it would confuse a reader who knows Go and reads the body — a hidden constraint, a non-obvious WHY, an invariant the body alone can't convey. Don't paraphrase the body. Don't sketch the algorithm in prose before writing it. (If the user has scaffolded a function with placeholder comments outlining what to implement, those are a brief from them to you — implement them and remove the comments unless they survive the test above.)
- **Implement exactly what was asked — no speculative helpers.** When the user asks for `Rational.Add/Mul/Div`, don't also add `Sub`, partial reductions, or shortcut constructors. Speculative additions get reverted. If a missing helper is genuinely useful, the user will request it.
- **Plain struct + exported fields beats accessor methods on `Expr` types.** The interface keeps to `Len` / `IsScalar`; concrete-type behaviour is read at the call site (`n.ExpInt > 0`, not `n.Expand()`). Where invariants need protecting, *unexport* the field; don't expose it via a method.
- **No defensive checks for impossible cases.** When a method's invariants are protected by package boundaries (e.g. `den > 0` on unexported Rational fields, or "callers never pass negative to gcd64"), don't add `if g == 0` / `if r.Den == 0` / `if x < 0` "just in case" clauses. They get pruned. If you're tightening an invariant, prune the corresponding checks in the same change.

### Project conventions
- **Always use `codec`'s name constants** (`headTimes`, `headPlus`, etc. in `codec/names.go`, and `codec.DimensionD`) instead of raw strings; `codec` is the only package that knows head names. Typos silently break parsing.
- **The tool's own output is ASCII-only.** The diagnostic text the binary itself emits — help/usage banner, `-version` output, error messages — must be ASCII (use `-` not `—`, `...` not `…`). This does *not* constrain the contracted expression on stdout: that carries input characters through verbatim, so Unicode in the input (e.g. Greek index names) is preserved in the output. Docs and code comments may use Unicode freely.
- **Docs and fixture `name`s are publication-grade and impersonal.** Never write a contributor's name, a local path, the generating toolchain, or a foreign suite's numbering into committed files. Keep domain content (FeynCalc notation, the math); cut provenance — at authoring time, not as a later sweep. Importing externally-supplied tests is the highest-risk trigger, because the who/where/which-tool scaffolding is exactly what you reach for while writing and exactly what must not ship.
- **Read `codec/parser.go` and `codec/walked.go` before writing any scalar-type dispatch.** A symbol is an `*expr.Symbol` and an unknown head an `*expr.HeadExpr`, not an `Atom`; a Pair is never an `Expr`. Inside an unknown head a `Pair`, `LorentzIndex` or `Momentum` is a `HeadExpr` too, unchecked, so code that looks for indices in opaque factors must look for those. A `Rational` or `Complex` that is not a number is read as `a · b⁻¹` or `a + I·b` (`expr.Expands`), `b⁻¹` a number or the opaque factor `Power[b, -1]`, and keeps its parts as written for rendering. The part of a summand the walk reaches holds no number node and no read `Rational` or `Complex`: numbers are coefficients there (`TimesExpr.Coeff`). The Atom type map in `docs/architecture.md` §Component Invariants > Parser says which node arrives where.

---

## Profiling

`cmd/contractor` accepts `-cpuprofile`, `-memprofile`, `-blockprofile`, and `-mutexprofile` (each takes a file; stdlib `runtime/pprof`). The memory profile runs after the pipeline has drained, so inspect it with `-alloc_space`, not the default `-inuse_space` — this is a throughput pipeline with near-zero live data at the snapshot point:

```
./bin/contractor -cpuprofile cpu.prof -memprofile mem.prof < input.json > /dev/null
go tool pprof -top -alloc_space mem.prof
```

Last captured shape (`test-input-2.dat`): GC scanning ~half of CPU; parser drove about half of heap allocation, contractor and the rest cheap. Start optimization in the parser, but rerun before relying on these numbers.

`scripts/bench/run.py` runs generated inputs that reproduce the known performance cliffs (`scripts/bench/gen.py` lists them), or any `--input FILE`, to find a case that hangs, blows up memory or gives a wrong result; it is not part of `scripts/run-tests.sh`. `--compare REF` checks that stdout is byte-identical to that of `REF`, a build of the commit before a change: it certifies an output-preserving change on inputs too large to be fixtures.
