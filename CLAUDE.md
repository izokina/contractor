# CLAUDE.md — FeynGrav Index Contractor

<!--
This file is the landing briefing — invariants, gotchas, and pointers.
Authoritative deeper references:
  docs/architecture.md         — pipeline, package layout, component invariants
  docs/samples.md              — worked examples + known-flaw catalog
  docs/scalar-normal-form.md   — coefficient algebra (incl. §5b render-faithfully contract)
  docs/known-limitations.md    — parsing, output-ordering, momentum, opaque, overflow gaps
  docs/feyncalc-notation.md    — FeynCalc-grounded physics interpretation of the input
                                 language (dimension labels, Pair = metric / vector / scalar
                                 product, contraction rules) + Mathematica StartProcess wrapper
  README.md                    — human-facing onboarding
-->

## Critical Facts — Read First

1. **Index contraction kernel.** Contracts repeated Lorentz tensor indices, folds coefficient arithmetic (int32 / Rational / Complex) via `eval.Calculator`, drops zero monomials, sorts output by canonical signature. `Power[_, 0]` is intentionally opaque (`0^0` ambiguity). → `docs/architecture.md` §Pipeline, §Why coefficients flow as `expr.Scalar`; `docs/scalar-normal-form.md` §4.3.
2. **`HasD` flag changes contraction result.** `LorentzIndex{HasD: false}` → contracted pair emits `4`. `LorentzIndex{HasD: true}` → emits `"D"`. `Momentum.HasD` carries through to output but does not affect contraction. `HasD` mirrors FeynCalc's *dimension label* (`LorentzIndex[mu, D]` / `Momentum[p, D]` — `D = 4 − 2ε` in dimensional regularization); it is **not** a derivative-index marker. → `docs/feyncalc-notation.md` §Dimension-Aware Lorentz Contractions.
3. **Signatures, not custom equality.** `LorentzIndex.Signature`, `Momentum.Signature`, and merger keys are canonical JSON strings produced by `codec.Signer` from the *output* representation. Never use `json.Marshal` on internal structs for grouping; call `Signer.Pairs(...)`. → `docs/architecture.md` §Signatures.
4. **`Term` is effectively immutable in flight.** `ContractAndNormalize` allocates a fresh `term.Pairs` slice and a fresh `Coeff` Scalar (via `Calculator.Mul`), so workers can hand the original `Term` along without coordination. `Monomial.Opaques` arrays are never mutated after creation. → `docs/architecture.md` §Component Invariants > Contractor.
5. **Prefer streaming over materialization.** The pipeline streams through unbuffered channels end-to-end. `Times` and `Power` expand lazily inside `walk.Walker[expr.Scalar, expr.Pair]` driven by `eval.TermFolder`; closures are allocated bottom-up at construction and reused across rounds. Don't replace the Walker's recursive yields with pre-built slices. → `docs/architecture.md` §Walker / Folder design.
6. **Optimize allocations aggressively.** Reuse buffers with `buf = buf[:0]` rather than reallocating. Use `unsafe.String(unsafe.SliceData(b), len(b))` for zero-copy `[]byte → string` (one site: `expr/expr.go`'s `atomSignature`). Use `append(make([]T, 0, n), ...)` for sized copies. GC scanning currently dominates CPU; the parser is the top alloc source — optimise parser first.
7. **Build requires `GOEXPERIMENT=jsonv2`.** Use `make build` (writes to `./bin/contractor`). A bare `GOEXPERIMENT=jsonv2 go build ./cmd/contractor` drops the binary in the *current directory* — prefer the Makefile target. NOTE: `json.MarshalEncode(enc, v)` does not exist in this jsonv2 surface; use `json.Marshal(v)` + `enc.WriteValue(jsontext.Value(b))` instead.
8. **I/O is Mathematica ExpressionJSON via stdio.** One JSON expression in, one out. No Go test files exist — verification goes through the `tester` agent, which runs `./scripts/run-tests.sh` (build + `scripts/check-fixtures.py`) against the bundled fixture `scripts/sample-fixtures.json`. See `docs/samples.md` for the worked examples (regular + known-flaw rows).
9. **Components must render faithfully; normal form is optional.** `eval.Calculator`'s overflow recovery (per `scalar-normal-form.md` §6) can produce non-canonical Scalars — e.g. an `Atom{int32(1)}` opaque that, in normal form, would have folded into the coefficient. Downstream components (writer, signer, merger) treat their input as "sum of parts" and never silently drop a factor; see `docs/scalar-normal-form.md` §5b. The known residuals are samples 42, 45, 48, 49, 50 (all data-preserving but non-canonical). The writer satisfies the contract structurally: `writeMonomial` only suppresses its `Coeff` factor when `m.Coeff == expr.OneComplex`, and `Atom{int32(1)}` opaques flow through `writeExpr → writeAtom` unsuppressed. **Open gap (will revisit):** parsed `Times[1, x]` therefore round-trips as `["Times", 1, x]` rather than `x`; the multiplicative-identity collapse for parsed-Times children is not implemented. The natural site if it lands is `writeExpr`'s `*expr.TimesExpr` arm (filter `Atom{int32(1)}` children of parsed Times only, never opaque `Val`s) — otherwise fail-soft breaks.

---

## Core Types (`pkg/pipeline/expr/expr.go`)

```go
// Minimal interface; all behavior is in walk.Walker / codec.Writer / codec.Signer.
type Expr interface {
    Len() int       // term-expansion count (Plus sums; Times products)
    IsScalar() bool // false only for Pair and composites that contain a Pair
}

type Term struct {
    Pairs []Pair
    Coeff Scalar // sum-of-monomials normal form; OneScalar is the multiplicative identity
}

// Metric tensor (two Lorentz), Lorentz vector (Lorentz + Momentum),
// or scalar product (two Momentum), depending on contents.
// See docs/feyncalc-notation.md for the FeynCalc-grounded interpretation.
type Pair struct {
    Lorentz  []LorentzIndex
    Momentum []Momentum
}

// HasD=false → contracts to 4; HasD=true → contracts to "D"
type LorentzIndex struct {
    Index     string
    HasD      bool
    Signature string // canonical JSON via codec.Signer.LorentzIndex
}

// Source/HasD mirror LorentzIndex's Index/HasD pattern.
// HasD here is purely carry-through for output (no contraction effect).
type Momentum struct {
    Source    Expr
    HasD      bool
    Signature string // canonical JSON via codec.Signer.Momentum
}

// Opaque scalar leaf. Value ∈ {string, int32, int64, jsontext.Value, []any}.
type Atom struct {
    Value     any
    Signature string // canonical via expr.atomSignature
}

// PowerExpr: when Exp == nil, the parser cached the int32 exponent into ExpInt.
// When Exp != nil, ExpInt holds the zero value (0). See PowerExpr bullet below.
type PowerExpr struct {
    Child  Expr
    Exp    Expr  // nil when the exponent is a cached int32 in ExpInt
    ExpInt int32 // valid when Exp == nil
    len    int
}
```

**`Writer` is stateless: just an encoder.** Two exported methods, both panic-protected at the entry: `WriteExpr(expr.Expr) error` emits any Expr as a fixed-shape JSON value (no flatten, no identity collapse — canonical Mathematica never nests Times-in-Times or Plus-in-Plus, so straight emission is canonical). `WriteTerm(expr.Term) error` renders the Term cascade `Times[Coeff_factors..., Pair...]` and is the merger's path to output. The Scalar-arithmetic multiplicative identity is `expr.OneScalar` (one monomial with coefficient `1+0i` and no opaques); the multiplicative-identity `Complex` is `expr.OneComplex`.

**The Term cascade is three input-type-named layers.** `WriteTerm(t)` picks one of: single-Monomial Coeff → delegate to `writeMonomial(t.Coeff.Monomials[0], t.Pairs...)`; no Pairs → delegate to `writeScalar(t.Coeff)`; otherwise emit `["Times", scalar, pair, ...]`. `writeScalar(s)` emits `Plus[Monomial...]` with `0 → 0`, `1 → writeMonomial`, `N → ["Plus", ...]`. `writeMonomial(m, extraPairs ...expr.Pair)` emits `Times[(Coeff if != 1), Opaque..., extraPair...]` with `0 → 1`, `1 → bare element`, `N → ["Times", ...]` — `extraPairs` is only non-empty at WriteTerm's single-Monomial call. Each level pre-counts items from `len(m.Opaques) + len(extraPairs) + (Coeff != OneComplex ? 1 : 0)`; no lookahead, no state.

<!--
codec uses a uniform panic-based error model (errors.go) so inner
helpers can stay error-return-free. Both Parser.ParseJson and
Writer.{WriteExpr, WriteTerm} are recover-shell entry points. New
codec code that needs to fail should panic via the free helpers
(panicf, wrap, assert) defined in errors.go, never grow back into
returning errors from inner helpers.
-->
**Codec uses panic-based control flow internally.** `pkg/pipeline/codec/errors.go` defines `wrappedError`, `recoverWrapped(*err)`, `panicf(format, args)`, `wrap(err)`, and `assert(msg, err)` as free functions. Exported entry points (`Parser.ParseJson`, `Writer.WriteExpr`, `Writer.WriteTerm`) install `defer recoverWrapped(&err)` and return wrapped errors normally; inner helpers panic via the free functions. Anything called from outside this protection must still return errors — `Signer`'s embedded `Writer.WriteExpr` calls discard the error return because every reachable signer payload is a leaf-shape Expr whose write paths cannot fail.

**`PowerExpr.Exp == nil` means the exponent was a cached int32 in `ExpInt`.** Otherwise `Exp` is a generic Expr and `ExpInt` is at zero value. `Walker.walk` and `IsScalar` discriminate via `ExpInt > 0` directly — the zero coincidence covers both "non-positive cached" (don't expand) and "symbolic Exp" (don't expand) in one comparison. The writer emits `["Power", child, exp]` either way: when `Exp != nil` it writes the Expr; when `Exp == nil` it writes `jsontext.Int(int64(ExpInt))` directly (no Atom allocation in this hot path).

**`PowerExpr.IsScalar()` is `p.ExpInt <= 0 || p.Child.IsScalar()`.** Non-expandable (ExpInt ≤ 0) is always scalar; expandable with a scalar child is *also* scalar (e.g. `Power[κ, 2]` where κ is a string Atom). Code that branches on Power scalarity must distinguish all four `(expandable, child-scalar)` cases — do not lump "scalar Power" into "non-expanding Power".

---

## I/O at a glance

```json
["Plus",
  ["Times", 2, ["Pair", ["LorentzIndex", "mu"], ["LorentzIndex", "nu"]]],
  ["Times", 3, ["Pair", ["LorentzIndex", "mu"], ["LorentzIndex", "mu"]]]
]
```

The second term's repeated `mu` contracts and the merger collapses `3 * 4 → 12`:

```json
["Plus",
  ["Times", 2, ["Pair", ["LorentzIndex", "mu"], ["LorentzIndex", "nu"]]],
  12
]
```

`["LorentzIndex", "mu", "D"]` sets `HasD=true` (contracts to `"D"`); the two-arg form sets `HasD=false` (contracts to `4`). For the full catalog see `docs/samples.md`. User-visible limitations of the contractor (parsing scope, output-ordering non-determinism, Momentum non-expansion, opaque normalisation gap, integer overflow) are catalogued in `docs/known-limitations.md`.

---

## Coding Guidelines for This Project

### Test discipline
- **Sample verification goes through the `tester` agent — trust its verdict.** Spawn it via the Agent tool (`subagent_type: "tester"`) for any behavioural verification of a code change; never run `make build`, `go build`, `scripts/run-tests.sh`, or `python3 scripts/check-fixtures.py` from the main conversation. The agent runs in isolation: it builds the contractor, runs each fixture entry 20 times via `scripts/check-fixtures.py`, and returns a classified verdict so the raw output stays out of your context. The Python script enforces byte-correct comparison on numbers and strings; the only carve-out is that the outermost `Plus`'s addends are compared as a multiset (the merger's pair-key map iteration order is non-deterministic — see `docs/architecture.md` §Pipeline). Apply the same rule when you read raw contractor output yourself — don't flag pure addend reorders as failures. To make the judgement reliable, brief the agent: state what behavioural change you made (e.g. *"Power[_, 0] now folds to 1"*, *"factor ordering switched to length-then-signature"*) so it can edit the affected `outputs` in `scripts/sample-fixtures.json` before testing. The agent may rewrite fixture `outputs` but never `input`s, and never edits docs — when it reports a `spec shift` (or graduates a known-flaw row), updating `docs/samples.md` is *your* job in the same change, not the agent's. When it reports a `regression`, take that at face value.
- **Verify input shapes against real samples.** Don't invent test inputs; check `scripts/sample-fixtures.json` and `docs/samples.md` first.
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
- **Always use `literal.*` constants** (`literal.Times`, `literal.Plus`, etc.) instead of raw strings. Typos silently break parsing.
- **The tool's own output is ASCII-only.** The diagnostic text the binary itself emits — help/usage banner, `-version` output, error messages — must be ASCII (use `-` not `—`, `...` not `…`). This does *not* constrain the contracted expression on stdout: that carries input characters through verbatim, so Unicode in the input (e.g. Greek index names) is preserved in the output. Docs and code comments may use Unicode freely.
- **Docs and fixture `name`s are publication-grade and impersonal.** Never write a contributor's name, a local path, the generating toolchain, or a foreign suite's numbering into committed files. Keep domain content (FeynCalc notation, the math); cut provenance — at authoring time, not as a later sweep. Importing externally-supplied tests is the highest-risk trigger, because the who/where/which-tool scaffolding is exactly what you reach for while writing and exactly what must not ship.
- **Unsafe string ops are intentional.** The `unsafe.String(unsafe.SliceData(b), len(b))` pattern in `expr/expr.go`'s `atomSignature` is deliberate zero-copy. The backing byte slice must not be modified after conversion — safety comes from `json.Marshal`'s returned slice being a fresh local allocation never shared with another goroutine, not from any mutex scope.
- **Read `codec/parser.go` before writing any scalar-type dispatch.** `expr/expr.go` says `NewAtom` accepts `int32 | int64 | string | jsontext.Value | []any`; the Atom.Value type map in `docs/architecture.md` §Component Invariants > Parser says which of those actually arrives where. Both must be understood to write correct type-switch logic anywhere in `expr` / `codec` / `eval`.

---

## Profiling

`cmd/contractor` accepts `-cpuprofile`, `-memprofile`, `-blockprofile`, and `-mutexprofile` (each takes a file; stdlib `runtime/pprof`). The memory profile runs after the pipeline has drained, so inspect it with `-alloc_space`, not the default `-inuse_space` — this is a throughput pipeline with near-zero live data at the snapshot point:

```
./bin/contractor -cpuprofile cpu.prof -memprofile mem.prof < input.json > /dev/null
go tool pprof -top -alloc_space mem.prof
```

Last captured shape (`test-input-2.dat`): GC scanning ~half of CPU; parser drove nearly all heap allocation, contractor and the rest cheap. Start optimization in the parser, but rerun before relying on these numbers.
