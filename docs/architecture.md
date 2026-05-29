# Architecture — FeynGrav Index Contractor

Companion to `CLAUDE.md`'s Critical Facts. The briefing there is one-line per invariant; this document carries the structural reasoning, data flow, and per-component invariants behind those one-liners.

---

## Pipeline

```
stdin JSON
    └─► Parser goroutine (synchronous ParseJson, one summand expr.Expr per emit callback)
            └─► cParse channel
                    └─► Walker goroutine (walk.Walker + eval.TermFolder, emits expr.Term per monomial)
                            └─► cTerms channel
                                    └─► Pool × N contractor workers (one Contractor per goroutine)
                                            └─► merger.Add (mutex-protected, fan-in)
                                                    └─► Flush → Writer → stdout JSON
```

The pipeline streams through unbuffered channels end-to-end. Three named goroutines (parser, walker, pool×N), two channels (`cParse`, `cTerms`), one mutex (`merger.mu`). Each contractor worker is lock-free against the others — no shared state, one `*eval.Calculator` per worker. The merger is the only stage with internal locking because it is the only fan-in point: N workers race on `Add`. **Internal locking lives at fan-in points and nowhere else.**

`Parser` and `Contractor` each carry no mutex — the contract is *each instance is owned by one goroutine for its lifetime*. `runPipeline` honours this: one Parser called from the parser goroutine, N Contractors each built per pool-worker. Don't share these instances.

End-of-stream sync uses a captured `parseErr` variable, *not* a channel: the parser goroutine writes `parseErr = parser.ParseJson(...)` before its `defer close(cParse)`; subsequent close-edges (`close(cTerms)` after walker drain, `wg.Done` after worker drain) form the happens-before chain so `main` reads `parseErr` correctly after `wg.Wait()`. There is no `mergerIn` / `mergerDone` channel pair — `merger.Add` is called inline by each worker.

---

## Package Layout

```
cmd/contractor/main.go               # Entry: runPipeline (parser + walker + N pool workers + flush)
pkg/literal/strings.go               # Mathematica name constants (Times, Plus, Pair, Rational, Complex, ...)
pkg/pipeline/expr/expr.go            # Expr interface + concrete Expr types: Atom (+ NewAtom),
                                     # Term, Pair, LorentzIndex, Momentum,
                                     # PlusExpr (+ NewPlusExpr), TimesExpr (+ NewTimesExpr),
                                     # PowerExpr (+ NewPowerExprInt, NewPowerExpr),
                                     # RationalExpr, ComplexExpr; atomSignature
pkg/pipeline/expr/rational.go        # int32-bounded Rational arithmetic (ok-flag overflow); exported Num, Den
pkg/pipeline/expr/complex.go         # Gaussian-rational Complex arithmetic; OneComplex; Complex.IsZero
pkg/pipeline/expr/scalar.go          # Scalar / Monomial / Opaque (sum-of-monomials normal form); OneScalar
pkg/pipeline/expr/convert.go         # Int32FromExpr, RationalFromExpr, ComplexFromExpr (Expr -> arithmetic);
                                     # RationalToExpr, ComplexToExpr (arithmetic -> Expr, used by atomizeComplex)
pkg/pipeline/walk/walker.go          # Walker[Coeff, Leaf] + Folder[Coeff, Leaf]: cyclic-pull-generator traversal
pkg/pipeline/codec/errors.go         # Free helpers wrappedError / recoverWrapped / panicf / wrap / assert
                                     # shared by Parser and Writer for panic-based control flow
pkg/pipeline/codec/parser.go         # Parser: synchronous ParseJson(decoder, emit func(expr.Expr)) error
pkg/pipeline/codec/writer.go         # Writer: stateless. Two entry points (WriteExpr for any Expr,
                                     # WriteTerm for the Term cascade). Internal cascade is three
                                     # input-type-named layers: WriteTerm → writeScalar → writeMonomial.
                                     # Leaf emitters: writeAtom, writePower, writeComplex, writeRational,
                                     # writePair, writeLorentzIndex, writeMomentum, writeArray (Expr-only).
pkg/pipeline/codec/signer.go         # Signer: canonical JSON signatures for LorentzIndex/Momentum/Pairs/Expr
pkg/pipeline/eval/term_folder.go     # TermFolder (Folder[Scalar, Pair]): owns Calculator + inner scalarFolder Walker,
                                     # normalises scalar leaves to Scalar, multiplies into running coeff, sinks each monomial
pkg/pipeline/eval/calculator.go      # Calculator: Add/Mul on Scalar; group by opaque-slice signature, fold + atomize on overflow
pkg/pipeline/eval/scalar_folder.go   # unexported scalarFolder (Folder[Complex, Opaque]) driving the inner Expr→Scalar Walker held by TermFolder
pkg/pipeline/contract/contractor.go  # Per-worker index contraction (owns Calculator + pre-built D/4 Scalars)
pkg/pipeline/merge/merger.go         # Mutex-protected fan-in merger; one shared eval.Calculator; per-pair-key Scalar accumulator;
                                     # Flush drops empty groups, writes via codec.Writer.WriteTerm (streams each Term directly)
```

Dependency direction is strictly `expr ← walk ← codec ← eval`. `expr` owns all value types (Expr tree, arithmetic ring, Scalar normal form, and converters); `walk` owns the generic Walker/Folder pair; `codec` owns JSON I/O; `eval` owns the scalar arithmetic engine plus the term-emission `TermFolder` used by `runPipeline`'s Walker stage. Singleton-collapse and Expr-materialisation do not happen inside `expr`; the writer streams `expr.Term` directly via `WriteTerm`, picking 0/1/N at each cascade level from a precomputed item count.

Naming: types ending in `Expr` (`PlusExpr`, `TimesExpr`, `PowerExpr`, `RationalExpr`, `ComplexExpr`) disambiguate from same-named Mathematica heads and signal "implements `Expr`". `Atom` / `Pair` / `LorentzIndex` / `Momentum` / `Term` carry no such suffix because there is no Mathematica head to collide with.

---

## Why coefficients flow as `expr.Scalar`

`expr.Scalar` is the sum-of-monomials normal form (see `docs/scalar-normal-form.md`). Each `Term.Coeff` carries one. The flow:

1. **Normalise at the parser-side Walker stage.** `eval.TermFolder` is a `walk.Folder[expr.Scalar, expr.Pair]` driving the outer Walker. It holds an inner `*walk.Walker` over `scalarFolder` against a shared `*Calculator`; each scalar leaf is normalised to a `Scalar` by `calc.reset(); scalarWalk.Walk(e); calc.materialize()`, then multiplied into the running coefficient via `Calculator.Mul`. Each completed monomial calls `Folder.Emit`, which sends a fresh `expr.Term{Pairs, Coeff: scalar}` onto the contractor channel.
2. **Multiply through D / 4 in the contractor.** `NewContractor` assembles `dScalar` (`OneComplex` × `Opaque{"D"}`) and `fourScalar` (`Complex{4, 0}`, no opaques) as `expr.Scalar` literals — both are trivial single-Monomial shapes that don't need the Walker pipeline. In the hot path, each contracted pair multiplies the worker's running coefficient by the appropriate one through its private `Calculator.Mul`. No further parsing of "what 4 means" — it's already in normal form.
3. **Accumulate in the merger.** The merger holds one shared `*Calculator`. `Add(term)` is `m.terms[sig].Coeff = m.calc.Add(old.Coeff, term.Coeff)` under `m.mu` — pure Scalar+Scalar arithmetic, grouped by `Signer.Pairs(term.Pairs)`. The empty `Scalar{}` (no monomials) is the additive identity, so a missing key works as the zero accumulator. There is no Walker at this stage — all walking happened upstream.
4. **Sort on output.** `Calculator.materialize` sorts groups by joint-sorted `Opaque.Signature` concatenation and drops zero-coefficient terms. Per-pair-key, the inner ordering is deterministic. Across pair-keys, map iteration order is non-deterministic — `docs/samples.md` documents this for cross-pair examples.

Memory is bounded by distinct `(pair-key × monomial-signature)` pairs, not by total incoming terms.

---

## Signatures

Tensor identity comparisons go through canonical JSON strings produced by `codec.Signer`, never `json.Marshal` on internal structs.

`codec.Parser`, `merge.Merger`, and `eval.Calculator` each embed a `codec.Signer` (reusing one `bytes.Buffer` per instance). Atom signatures don't go through `Signer` at all — `expr.atomSignature` is a free function called by `NewAtom`, so per-atom allocation stays minimal.

Three sites use signatures for grouping:
- **`LorentzIndex.Signature` / `Momentum.Signature`** — set at construction; consumed by sorting in the contractor and by pair-signature building in the merger.
- **`Signer.Pairs(term.Pairs)`** — the merger's per-pair-key for grouping incoming terms.
- **Joint-sorted `Opaque.Signature` concatenation** — the calculator's per-monomial key for grouping coefficients within one Scalar.

`Signer` is the zero-init type — no `New`, just declare or embed.

`Atom.Signature` and `Opaque.Signature` are different strings. `Atom.Signature` is `expr.atomSignature(value)` — a raw key (e.g. `"κ"` for a string atom, `"1"` for `int32(1)`) used internally for `Atom`-level identity. `Opaque.Signature` is `Signer.Expr(val)` — the JSON-encoded form, **with a trailing `\n` because `jsontext.Encoder` terminates every top-level value with a newline** (e.g. `"\"κ\"\n"` with quote bytes + LF, `"1\n"` for int32(1), `"[\"Power\",\"a\",2]\n"` for a Power Expr). Sorting opaques by signature is *byte* sort over JSON-encoded bytes, so the leading `"` (0x22) sorts before any digit (0x30+). Don't predict opaque sort order from the atom's value-string.

The bug class to keep ruled out: a code path that stored `jsontext.Value` for a symbol name (instead of a Go `string`) made the same name (e.g. `"D"`) hash to different signatures depending on its source, so contraction's `"D"` and the input's `"D"` landed in different merger groups. The cure is `parseScalar`: every JSON string token goes through it and arrives at `NewAtom` as a bare Go `string`. Don't reintroduce a parser shortcut that bypasses it.

---

## Component Invariants

### Parser (`codec/parser.go`)
- **Single-goroutine, no mutex.** `ParseJson(decoder, emit func(expr.Expr)) error` is synchronous: each top-level summand is handed to the `emit` callback inline. The caller spawns the goroutine; the parser doesn't. Top-level `Plus` is split addend-by-addend via the loop at `parseJson`'s root branch, so each addend reaches the Walker as its own Expr — that's where multi-addend top-level inputs become parallel work.
- **Panic-based errors** (shared with Writer via `codec/errors.go`): inner helpers call the free functions `panicf(fmt, args)` / `assert(msg, err)` which panic with `wrappedError`. `ParseJson` installs `defer recoverWrapped(&err)` and returns the wrapped `error`. Do not add `error` returns to inner helpers.
- **Streaming, no intermediate `[]any` for known shapes.** `parsePair` consumes `BeginArray + name` and dispatches directly to `parseLorentzIndex` / `parseMomentum`. There is no `Object` materialization step.
- **Buffer reuse**: `p.lorentz`, `p.momentum`, `p.sBuf`, `p.aBuf` are cleared with `[:0]` between uses. Don't replace with fresh allocations.
- **`parseNamedExpr`** is the dispatch point. Handles `Plus`, `Times`, `Power`, `Pair`, `Rational` (→ `*expr.RationalExpr`), `Complex` (→ `*expr.ComplexExpr`), and anything else as opaque scalar atoms via `parsePartialObject`.
- **`parseTimes` sorts children by `Len()` ascending** — required by `walk.mulStreams` to place large-fanout factors at innermost loop positions, minimizing total fold/unfold count. The sort is in-place on a slice the parser owns. Do not remove without relocating the optimization to the Walker; the Walker version would re-sort and re-allocate per `Walk` call.
- **Atom.Value type map (unified as of `parseScalar`).** JSON string tokens go through `parseScalar`, which calls `readToken(KindString).String()` — so `NewAtom` always sees a bare Go `string` for symbolic names. Integer literals that fit `int32` become `int32`; anything else (floats, bigints, opaque bytes) arrives as `jsontext.Value`. Reconstructed unknown objects arrive as `[]any`. Net invariant on `Atom.Value`: `string` = symbolic name; `jsontext.Value` = non-int32 numeric literal or opaque bytes; `int32` = participating integer (parser literal, the `NewAtom(int32(4))` and `NewAtom("D")` built once in `NewContractor`, or `RationalToExpr` / `ComplexToExpr` outputs called by `Calculator.atomizeComplex` on overflow recovery); `[]any` = reassembled opaque object. Note: `*RationalExpr` and `*ComplexExpr` are NOT `Atom.Value` — they are standalone `Expr` types. (`int64` is still accepted by `NewAtom` but no in-tree caller produces it.) See §Signatures for why symbol-name strings must arrive as bare Go `string`, never `jsontext.Value`.
- **`parsePower` always returns `*PowerExpr`.** When the exponent parses as a positive `int32` Atom, the parser calls `expr.NewPowerExprInt(child, v)` — caches `Child + ExpInt`, leaves `Exp = nil`. Otherwise (zero, negative, symbolic, or any non-int32 atom), the parser calls `expr.NewPowerExpr(child, exp)` — sets `Child + Exp = exp`, leaves `ExpInt = 0`. The Walker expands when `ExpInt > 0` and folds opaquely otherwise. The writer always emits `["Power", child, exp]`. Zero-exponent simplification to `1` is intentionally NOT performed (`0^0` ambiguity, sample 40).
- **`Rational`/`Complex` parse via `parseBinaryArgs`.** Both call `parseExpr` for each argument and `closeArray()` — no special arg-parser. Any `Expr` is valid in numerator/denominator/Re/Im (e.g. `Rational[Plus[a,b], c]` works).

### Contractor (`contract/contractor.go`)
- **Per-worker, no shared state, no mutex.** `main.go` creates one `Contractor` per goroutine. Never share across workers. The contract is the same as Parser's — each instance is owned by one goroutine for its lifetime.
- **`addPair` is stateful across a single term's pairs**: `c.indexPairs` accumulates unmatched pairs during one call to `ContractAndNormalize`. It is implicitly drained (not reset) — pairs remaining in `indexPairs` after the loop are moved to `c.pairs`.
- **Owns one `*eval.Calculator` and pre-built `dScalar` / `fourScalar`**. The contraction multipliers (`"D"` for `HasD=true` pairs, `4` for `HasD=false` pairs) are assembled once in `NewContractor` as `expr.Scalar` literals. The "D" opaque's signature is hardcoded as `"\"D\"\n"` (4 bytes — `jsontext.Encoder` terminates each top-level value with a newline, so that's what `codec.Signer.Expr` returns for a string atom; see §Signatures). The Scalars are folded into `c.coeff` with `c.calc.Mul(c.coeff, c.dScalar/c.fourScalar)` in the hot path. The Calculator is private to one worker — matches the per-worker invariant.
- **Output slice is freshly allocated**: `term.Pairs` is replaced with a new slice, so the original `Term` slice is safe to reuse. `term.Coeff` is `c.calc.Mul(term.Coeff, c.coeff)` — `Calculator.Mul` returns a fresh `Scalar` whose `Monomials` slice and per-monomial `Opaques` arrays are newly allocated. This is what makes `Term` immutability-in-flight work: workers can hand the original `Term` along without coordination.
- **Output pairs are sorted** by `(len(Momentum), Lorentz signatures..., Momentum signatures...)` for deterministic output.
- **Buffer reuse**: `c.pairs`, `c.lorentz`, `c.momentum` are cleared with `[:0]`. `c.coeff` is reset to `expr.OneScalar` (= multiplicative-identity Scalar).

### Walker / Folder (`walk/walker.go`, `eval/term_folder.go`, `eval/scalar_folder.go`)
- **`walk.Walker[Coeff, Leaf]` is the generic pull-generator traversal driver.** It owns the running coefficient, the leaves slice, and the rollback frames around Plus / Times / Power. The `walk.Folder[Coeff, Leaf]` interface customises classification (`Compound`), the multiplicative step (`Fold(prev, leaves, e) → (next, newLeaves)`), and per-monomial emit (`Emit(coeff, leaves)`). Folder must not mutate elements already in `leaves` — only the tail it appends is its own; Walker captures `prevLen := len(leaves)` and reslices on rollback. Each subtree is turned into a `stream = func() bool` by `walk` / `walkPlus` / `walkTimes` / `walkPower` / `walkLeaf`; the stream is **cyclic** — `true` per yielded monomial, `false` at end-of-round, and the next call begins a fresh round with identical monomials. The exported `Walk` is a single `for s := w.walk(e); s(); { w.folder.Emit(...) }` loop. Closures are allocated **bottom-up at construction** (one per Expr, plus one slice per Plus/Times/Power for child streams) and reused across rounds — no per-step allocation, no per-yield closure inside the stream type. `Walk` resets the Walker's own coeff/leaves at entry but never touches the bound `folder` — successive `Walk(e1); Walk(e2)` calls share whatever state the Folder carries (e.g. `TermFolder`'s shared `*Calculator.groups` map across the inner scalarFolder Walker and the outer Mul step), which is what makes streaming accumulation correct.
- **Walker fall-through guards both unrecognised types and non-positive `PowerExpr.ExpInt`.** Any Expr the Folder marks non-compound lands in `walkLeaf` (which folds via `Fold`, yields once, then unfolds); a `*PowerExpr` with `ExpInt <= 0` (or any unrecognised type reached when `Compound` returns true) does the same — the type switch in `walk` falls out without `return` and the function reaches `walkLeaf(e)`. This means Folders' `Compound` answers semantic questions ("is this opaque to me?") rather than structural ones ("is this a Plus?"). `eval.TermFolder.Compound` is just `!e.IsScalar()`; `eval.scalarFolder.Compound` checks `expr.ComplexFromExpr(e)`.
- **`eval.TermFolder` is the term-emission Folder** (Coeff=`expr.Scalar`, Leaf=`expr.Pair`). It owns one `*Calculator` shared between a persistent inner `*walk.Walker` over `scalarFolder` (Expr→Scalar of each scalar leaf) and the outer Scalar product step. `Fold` routes `Pair` to the leaves slice; for any other (scalar) leaf `e`, it runs `f.calc.reset(); f.scalarWalk.Walk(e)` and returns `(f.calc.Mul(prev, f.calc.materialize()), pairs)`. `Emit` allocates a fresh `[]Pair` and pushes `expr.Term{Pairs, Coeff: scalar}` down the constructor-bound `sink`. `runPipeline` builds `walk.NewWalker(eval.NewTermFolder(func(t expr.Term) { cTerms <- t }))` and calls `Walk(node)` per summand drained from the parser's `cParse` channel. Sharing one Calculator between the inner Walker and the outer Mul is safe because `Calculator.materialize` returns a fresh `Monomial`-value slice and the only writes performed by `foldBuf`/`reset`/`Mul` are to `c.groups` and `c.opBuf` — never to a previously allocated `Monomial.Opaques` backing array.

### Expr-side coefficient layer (Expr constructors / Writer)
- **`Expr` is minimal: `Len() int` and `IsScalar() bool`.** `Len` is the term-expansion count (used to size buffers and order Times children); `IsScalar` distinguishes coefficients from pair-bearing nodes. All other behavior (serialization, traversal, grouping) lives in dedicated visitor types. Adding behaviour back to the interface is a regression — each new method either belongs on a dedicated visitor or is derivable from the existing two.
- **`NewPlusExpr` / `NewTimesExpr` / `NewPowerExprInt` / `NewPowerExpr`** are the only constructors. They compute and cache `len` / `scalar` from children but do **no** singleton or empty-array collapse — `NewPlusExpr([])` yields a `*PlusExpr` with `len = 0`, `NewTimesExpr([x])` yields a `*TimesExpr` with one child. The parser builds these "as-is"; the *Term* cascade (`WriteTerm`/`writeScalar`/`writeMonomial`) handles 0/1/N at output time from a precomputed count. Parser-built `*PlusExpr` / `*TimesExpr` *Exprs* themselves render fixed-shape via `writeExpr → writeArray` (no singleton collapse on Expr-rendered Plus/Times — the cascade-shape collapse is a *Term* property, not an *Expr* one).
- **Heap-allocated, persistent.** Expr values live for the lifetime of the term. Don't pool or reuse them. (Compare: `p.lorentz` / `c.pairs` buffers in the parser/contractor, and `Calculator.opBuf` / `sigBuf` in eval, all DO follow `[:0]` recycling.)
- **Numeric folding lives in `eval.Calculator` only.** Coefficient arithmetic (int32 / Rational / Complex) is performed against the Gaussian-rational ring inside `Calculator.Add` / `Calculator.Mul`, with overflow recovery via opaque atomization (see Scalar normal form below). No int32-fold code path exists outside that ring; the Expr constructors deliberately skip folding. Adding a new arithmetic rule means touching `Calculator` (and possibly `expr.Rational` / `expr.Complex`), never the Expr constructors.
- **`Writer` is stateless** (only field: the encoder). Two exported entry points, each `defer recoverWrapped(&err)`-protected:
  - `WriteExpr(n expr.Expr) error` — fixed-shape, no flatten and no identity collapse. The internal `writeExpr` is a seven-arm type switch over `*Atom`, `Pair`, `*PowerExpr`, `*RationalExpr`, `*ComplexExpr`, `*TimesExpr`, `*PlusExpr`. Rational/Complex/Times/Plus go through `writeArray(name, nodes...)` (Expr-only variadic); Atom/Pair/Power are per-type emitters. Canonical Mathematica never nests Times-in-Times or Plus-in-Plus, so straight emission is canonical.
  - `WriteTerm(t expr.Term) error` — the merger's path. Renders the cascade `Times[Coeff_factors..., Pair...]`: single-Monomial Coeff delegates to `writeMonomial(t.Coeff.Monomials[0], t.Pairs...)`; no-Pairs delegates to `writeScalar(t.Coeff)`; otherwise emits `["Times", scalar, pair, ...]`.
  - Cascade layers `writeScalar(expr.Scalar)` and `writeMonomial(expr.Monomial, extraPairs ...expr.Pair)` each pre-compute their item count and pick `0 → identity`, `1 → bare`, `N → ["name", ...]`. `extraPairs` on `writeMonomial` is variadic and only non-empty at `WriteTerm`'s single-Monomial call site (so the in-aggregate splat happens once, no allocation when empty).
  - **Render-faithfully contract.** `writeMonomial` only suppresses its `Coeff` factor when `m.Coeff == expr.OneComplex`; opaque `Atom{int32(1)}` Vals from overflow recovery flow through `writeExpr → writeAtom` and render as literal `1` (correct, per `docs/scalar-normal-form.md` §5b). **Open gap**: parsed `Times[1, x]` therefore round-trips as `["Times", 1, x]` instead of `x`. The natural site to add collapse is `writeExpr`'s `*expr.TimesExpr` arm; restrict suppression to parsed Times children only, never opaque `Val`s.
  - The expr package carries no `Multipliable`/`Summable` interface or `Factors`/`Summands` method — the cascade is entirely in `writer.go`.

### Scalar normal form (`pkg/pipeline/expr/scalar.go`, `pkg/pipeline/eval/`)
- **`expr.Scalar` is a sum of `expr.Monomial`s; each `Monomial` is `Complex` × `[]Opaque`.** `Opaque` is `{Signature string; Val expr.Expr}` — a symbolic atom carried through arithmetic, keyed by canonical encoding via `codec.Signer`. Within a Calculator group, opaques are sorted by Signature; the joint sorted-Signature concatenation is the monomial signature.
- **`eval.Calculator` groups monomials by signature in a map and folds Coeffs with overflow recovery.** When `Complex.Add` or `Complex.Mul` returns `ok=false`, the offending coefficient is wrapped as an `Opaque` (`atomizeComplex`) and the fold is reattempted with `expr.OneComplex`; the new monomial signature differs from the colliding one, so `foldBuf` recursion terminates. `materialize` sorts groups by signature for deterministic output and drops zero-coefficient terms (using `Complex.IsZero`).
- **`expr.Rational` and `expr.Complex` are int32-bounded with `(_, ok bool)` returns.** `Rational` maintains `den > 0` and `gcd(|Num|, Den) = 1`; `Add`/`Sub`/`Mul`/`Div` all go through `reduce64` (full GCD reduction on the int64 brute pair), `Neg` through `finalize` (sign + zero canonicalisation, no GCD — safe because negation of a reduced fraction stays reduced). The `ok=false` path is the *only* error signal — there are no defensive checks beyond it; callers either propagate or atomize.
- **`eval.scalarFolder` is the inner `walk.Folder[expr.Complex, expr.Opaque]`** that drives Expr→Scalar reduction. Folds numeric atoms / `*expr.RationalExpr` / `*expr.ComplexExpr` into the running Complex coefficient via `expr.ComplexFromExpr`; everything else becomes an `Opaque`. The Walker expands every `Power` with `ExpInt > 0` regardless of child shape — so `Power[x, 3]` becomes `x*x*x` in the opaque slice. This is the only correct rule in the scalar-only world. The outer `eval.TermFolder` Walker treats scalar Powers as leaves (because `IsScalar` is true), but it then routes each such leaf through its inner scalarFolder Walker — which expands it via this same rule. So the end-to-end behaviour is "scalar Powers always expand"; the two-level Walker hierarchy is purely a separation between term-emission (outer) and Scalar normalisation (inner).
- **`Scalar` exits via the writer's `WriteTerm` entry.** `WriteTerm` walks the cascade `Times[Coeff_factors..., Pair...]`, with single-Monomial Coeff inlined alongside Pairs (so `Coeff = Plus[m]` and `Pairs = [p1, p2]` becomes `Times[Coeff_factors_of_m..., p1, p2]`, no intermediate Plus wrapping). Multi-Monomial Coeff renders as `["Plus", monomial, ...]`. No intermediate Expr tree per coefficient. `expr.Rational.Num` / `Den` and `expr.Complex.Re` / `Im` are exported so `writeRational` / `writeComplex` emit JSON tokens directly without going through `RationalToExpr` / `ComplexToExpr`. The Expr-conversion helpers are still used by `Calculator.atomizeComplex` on overflow recovery.

### Merger (`merge/merger.go`)
- **Mutex-protected fan-in.** `Add(term)` locks `m.mu` for the duration of one update; pool workers call it inline. The merger has no goroutine of its own — `Flush` runs from `main` after `wg.Wait()`. There is no `Run` method, no `mergerIn` / `mergerDone` channel pair.
- **Groups by `codec.Signer.Pairs(term.Pairs)`.** The key is a canonical JSON string built by `Signer` via `Writer`. Each group's coefficient is stored as an `expr.Scalar` value, not a pointer.
- **One shared `*eval.Calculator`.** `m.terms[sig].Coeff = m.calc.Add(old.Coeff, term.Coeff)`. The empty `Scalar{}` (no monomials) is the additive identity, so a missing key works as the zero accumulator. All walking happens upstream in `eval.TermFolder`; the merger performs pure Scalar arithmetic. Memory is bounded by distinct (pair-key × monomial-signature) pairs, not by total incoming terms.
- **`Flush(enc *jsontext.Encoder)` is two-pass.** Pass 1 walks `m.terms` (deleting each entry as it visits — `Flush` is destructive), drops empty (fully cancelled) groups, and stages the rest as `expr.Term` values in a local slice. Pass 2 picks the wrapper: `0 → write int32(0)`, `1 → w.WriteTerm(t) directly`, `≥2 → wrap in ["Plus", …]` and `w.WriteTerm(t)` each. `WriteTerm` streams the Scalar's monomials directly through the cascade — no intermediate Expr materialisation. Map iteration order across pair-keys is non-deterministic, so the cross-pair ordering in `docs/samples.md` may drift between runs / refactors. Per-pair-key, the inner ordering IS deterministic — `Calculator.materialize` sorts by monomial signature when the Scalar is built.

---

## Walker / Folder design

`walk.Walker` is the canonical cyclic-pull-generator shape in this codebase. New tree traversals should write a `Folder[Coeff, Leaf]` against the existing Walker — do not hand-roll a parallel visitor.

How the streams compose:
- Each subtree's `stream` (`func() bool`) is allocated once at construction and reused across rounds — no per-step allocation, no per-yield closure inside the stream type.
- `walkPlus` walks children sequentially with one shared `i` cursor.
- `walkTimes` and `walkPower` both delegate to `mulStreams` over a pre-built `[]stream`. Power treats `child^n` as n independent copies.
- `walkLeaf` flips between fold-and-yield-true / unfold-and-yield-false using a `folded` flag to mark the held-folded state.
- The `Compound` predicate on the Folder is a semantic check ("is this opaque to me?"), not a structural one. Walker handles the type taxonomy.

Two folders exist today: `eval.TermFolder` (term-emission, Coeff=`expr.Scalar`, Leaf=`expr.Pair`) and the unexported `eval.scalarFolder` (scalar-only, Coeff=`expr.Complex`, Leaf=`expr.Opaque`). They demonstrate the two-level Walker hierarchy: outer term emission, inner Scalar normalisation.

---

## Naming convention for strategy interfaces

Strategy interfaces use Go-idiomatic role nouns, not Java-style suffixes. The closest stdlib analogues — `go/ast.Walk(v Visitor, node Node)`, `path/filepath.WalkDir`, `database/sql.Driver` — name the strategy with a noun describing what it *is* in the algorithm's vocabulary. The algorithm here is the `Walker`; the strategy is a `Folder`. Avoid `Delegate` / `Listener` / `Strategy` / `Handler` for new strategy interfaces.
