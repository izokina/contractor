# Known Limitations

The contractor's core path — index contraction, like-monomial collapse, structural opaque carry-through — is reliable. The limitations below are deliberate scope decisions (overflow fail-soft, Momentum opacity), open gaps that produce non-canonical-but-data-preserving output (opaque normalisation, parsed-`Times[1, x]` collapse), and an inherent property of the streaming pipeline (output ordering). This file is the user-facing index; underlying mechanics are documented in [architecture.md](architecture.md) and [scalar-normal-form.md](scalar-normal-form.md).

---

## Parsing limitations

What's recognised as a structured head: `Plus`, `Times`, `Power`, `Pair`, `Rational`, `Complex`. Everything else — `FeynAmpDenominator`, `PropagatorDenominator`, any other Mathematica head not in that list — is reassembled as a single opaque `Atom` keyed by its canonical JSON encoding. Two opaque atoms with byte-identical encodings still merge cleanly; that is the only equivalence they participate in.

`Rational` and `Complex` are recognised but **only fold into the coefficient ring when their arguments are pure int32 integers** (or, recursively, a nested `Rational`/`Complex` whose own arguments fold). Anything symbolic in the numerator, denominator, real, or imaginary slot atomizes the whole node — see [Opaque normalisation gap](#opaque-normalisation-gap) for the consequence that two algebraically-equivalent atomized nodes may fail to merge, and [Integer overflow](#integer-overflow) for the consequence when operands fold but the result doesn't fit int32.

### `FeynAmpDenominator` / `PropagatorDenominator`

```json
["FeynAmpDenominator",
  ["PropagatorDenominator",
    ["Plus", ["Momentum", "p1", "D"], ["Momentum", "p2", "D"]],
    0
  ]
]
```

`FeynAmpDenominator` is a product of Feynman propagator denominators. Each nested `PropagatorDenominator[q, m]` represents `1 / (q² − m²)` where `q` is a momentum expression and `m` is the mass. `PropagatorDenominator` only ever appears nested inside `FeynAmpDenominator`. These are loop-integral denominators handled by downstream integration routines (e.g. FeynCalc/FIRE); the contraction stage carries them through as opaque atoms keyed by canonical encoding, so two terms with the same `FeynAmpDenominator` structure still merge.

→ Technical detail: [architecture.md](architecture.md) §Component Invariants > Parser; `pkg/pipeline/codec/parser.go:63-82`.

### Evanescent dimension labels (`MTE` / `FVE`) are rejected

A `LorentzIndex` or `Momentum` dimension argument must be either absent (`HasD=false`, the 4-dimensional case) or the bare string `"D"` (`HasD=true`). FeynCalc's *evanescent* objects `MTE` / `FVE` carry the dimension `D − 4`, which `FeynCalcInternal` serialises as the compound `["Plus", -4, "D"]` rather than a bare string. `parseLorentzIndex` / `parseMomentum` (`pkg/pipeline/codec/parser.go:141-142, 155-156`) hard-require the string `"D"` and `panicf` on anything else, so the contractor *rejects* such input (clean non-zero exit, empty stdout) — it does not produce a wrong answer. Inputs carrying `["Plus", -4, "D"]` dimension labels cannot currently be contracted.

---

## Non-deterministic output ordering

The pipeline is streaming and concurrent — there is no global sort at the end. Several stages mean the final byte sequence of the output `Plus` is not reproducible run-to-run for the same input:

- The merger groups terms in a Go map keyed by pair-signature. Iterating that map at flush time has no guaranteed order, so the addends of the outermost `Plus` come out shuffled.
- Terms arrive at the merger from N pool-worker goroutines in scheduling-dependent order. For most inputs this affects only which merger key gets created first (invisible after the map shuffle above), but in overflow paths it determines which operand atomizes and which gets folded — visible in sample `45-plus-int32-overflow`.
- Within a single Scalar the calculator's monomial grouping is deterministic (signature-sorted), and the contractor's per-pair sort is deterministic, so **drift only ever occurs across top-level `Plus` addends**.

Downstream consumers see no functional difference: `scripts/check-fixtures.py` compares the top-level `Plus` as a multiset, and Mathematica re-canonicalises `Plus` on import. Do not rely on byte-equality of the outermost `Plus`'s addend order.

→ Technical detail: [architecture.md](architecture.md) §Pipeline and §Component Invariants > Merger.

---

## Momentum sources are not expanded

Physics framing: `Momentum[p]` labels a momentum object whose dimension is set by an optional second argument (see [feyncalc-notation.md](feyncalc-notation.md)); a `Pair` of two momenta is the scalar product, and a `Pair` of a `LorentzIndex` and a `Momentum` is a Lorentz vector. The limitation below concerns what happens when the `Momentum`'s first argument is itself a composite expression (e.g. `Plus[p1, p2]`).

The parser accepts any `Expr` as `Momentum.Source` — `["Momentum", ["Plus", "p1", "p2"]]`, `["Momentum", ["Times", 2, "p"]]`, and arbitrarily nested shapes all parse without error. Past the parser, **no stage expands or distributes anything through the Source**. The contractor reads each Momentum purely by its `Signature` (a canonical JSON encoding of the whole `[Source, HasD]` tuple), and the merger groups Pairs by their Momentum signatures.

Practical consequences:

- Two `Momentum` values whose Sources serialise to byte-identical JSON merge as expected.
- Two `Momentum` values with structurally different but algebraically equivalent Sources (e.g. `Momentum[Plus[p, q]]` and `Momentum[Plus[q, p]]`) **do not merge** — distinct signatures.
- No relation like `Pair[Momentum[a + b], k] = Pair[Momentum[a], k] + Pair[Momentum[b], k]` is applied. If the caller wants such expansions, they have to perform them in Mathematica before serialising.

Real-world inputs do contain composite Momentum sources (e.g. propagator momenta inside `FeynAmpDenominator` / `PropagatorDenominator`); the contractor carries them through opaquely. A caller wanting distribution through such sources must perform it in Mathematica before serialising.

---

## Opaque normalisation gap

When the calculator can't fold a node into a coefficient and atomizes it instead (symbolic argument inside `Rational`/`Complex`, non-positive or symbolic exponent in `Power`, overflow), the atom's signature is built from the **literal sub-trees as they arrived**, not from canonicalised forms. Two structurally-different-but-algebraically-equal expressions therefore get different signatures and fail to merge.

The canonical illustrating sample is `44-power-times-permuted`:

```json
["Plus",
  ["Times", ["Power", ["Times", "a", "b"], -1], "κ"],
  ["Times", ["Power", ["Times", "b", "a"], -1], "κ"]
]
```

emits `Plus[Times[κ, Power[Times[a,b], -1]], Times[κ, Power[Times[b,a], -1]]]` rather than the expected `Times[2, κ, Power[Times[a,b], -1]]`. The inner `Times` factors should be canonically ordered before signing the surrounding atomized `Power`; they aren't, so the two terms see distinct signatures.

The same root cause applies anywhere a sub-tree of an atomized node could be canonicalised (Rational numerator/denominator, Complex real/imag, Power base/exponent) — fixing it in one place fixes the family.

Closely related, distinct fix site: parsed `Times[1, x]` round-trips as `["Times", 1, x]` rather than `x`. The multiplicative-identity collapse for parsed-`Times` children isn't implemented. Any fix must restrict the collapse to parsed-Times children only, never to opaque `Atom{int32(1)}` produced by overflow recovery — those must continue to render literally, or the [scalar-normal-form.md](scalar-normal-form.md) §5b render-faithfully contract breaks and overflow output silently loses data.

→ Technical detail: [scalar-normal-form.md](scalar-normal-form.md) §5 and §5b; concrete failing rows in [samples.md](samples.md) *Known implementation flaws* (`41-atomized-rational-numer-folds`, `44-power-times-permuted`).

---

## Integer overflow

Coefficient arithmetic runs in an int32-bounded Gaussian-rational ring. When an `Add` / `Mul` / Rational reduction would produce a value that doesn't fit int32, the calculator does **not** promote to extended precision — it atomizes the offending node (`Atom.Value` of type `int64`, or `jsontext.Value` for shapes that don't fit even int64) and continues. The result preserves all the input data but is not in canonical normal form, so the output is wider than it would otherwise be and may surface residual artefacts:

- `42-int32-overflow`: `Times[100000, 100000]` emits `["Times", 100000, 100000]` instead of `10000000000`.
- `45-plus-int32-overflow`: `Plus[INT32_MAX, 1]` emits `["Plus", 2147483647, 1]` (with addend order non-deterministic) instead of `2147483648`.
- `48-rational-mul-overflow`: asymmetric "split-flush" — one factor flushed as int, the other left as a `Rational[100000, 1]` that violates the [scalar-normal-form.md](scalar-normal-form.md) §3 `den ≠ 1` invariant.
- `49-complex-mul-real-overflow`: the whole `Times` left unevaluated, both Complex operands kept as-is.
- `50-times-plus-overflow-symbol`: distribution + overflow leaves a `Plus`-of-two-`Times` with a literal `1` opaque on one monomial.

Once a value escapes to an `int64` / `jsontext.Value` atom it stays opaque — subsequent arithmetic that touches it stays symbolic. Canonical uniqueness across algebraically-equivalent inputs that reach the int32 boundary by different paths is not guaranteed.

Rationale for the fail-soft choice (not a bug): Feynman-amplitude coefficients in practice are small integers (vertex factors, low factorials, low powers of two). Overflow is a corner case, and preserving the input data symbolically lets downstream Mathematica re-fold without surprise.

→ Technical detail: [scalar-normal-form.md](scalar-normal-form.md) §6 (policy) and §5b (render-faithfully consequences); concrete rows in [samples.md](samples.md) *Known implementation flaws* (42, 45, 48, 49, 50).
