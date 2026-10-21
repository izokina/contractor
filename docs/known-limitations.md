# Known Limitations

The contractor's core path — index contraction, like-monomial collapse, structural opaque carry-through — is reliable. The limitations below are a deliberate scope decision (Momentum opacity), open gaps that produce non-canonical-but-data-preserving output (opaque normalisation, parsed-`Times[1, x]` collapse), the treatment of non-integer literals, and an inherent property of the streaming pipeline (output ordering). This file is the user-facing index; underlying mechanics are documented in [architecture.md](architecture.md) and [scalar-normal-form.md](scalar-normal-form.md).

---

## Parsing limitations

What's recognised as a structured head: `Plus`, `Times`, `Power`, `Pair`, `Rational`, `Complex`. Everything else — `FeynAmpDenominator`, `PropagatorDenominator`, any other Mathematica head not in that list — is reassembled as a single opaque `Atom` keyed by its canonical JSON encoding. Two opaque atoms with byte-identical encodings still merge cleanly; that is the only equivalence they participate in.

`Rational` and `Complex` are recognised but **only fold into the coefficient ring when their arguments are integers** (for `Complex`, each part may also be a `Rational` of integers; nesting goes no deeper). Integer arguments of any magnitude fold — the ring promotes to `math/big` past int32. Anything symbolic in the numerator, denominator, real, or imaginary slot atomizes the whole node — see [Opaque normalisation gap](#opaque-normalisation-gap) for the consequence that two algebraically-equivalent atomized nodes may fail to merge, and [Non-integer literals](#non-integer-literals) for non-integer numeric arguments.

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

→ Technical detail: [architecture.md](architecture.md) §Component Invariants > Parser; `parseNamedExpr` in `pkg/pipeline/codec/parser.go`.

### Evanescent dimension labels (`MTE` / `FVE`) are rejected

A `LorentzIndex` or `Momentum` dimension argument must be either absent (`HasD=false`, the 4-dimensional case) or the bare string `"D"` (`HasD=true`). FeynCalc's *evanescent* objects `MTE` / `FVE` carry the dimension `D − 4`, which `FeynCalcInternal` serialises as the compound `["Plus", -4, "D"]` rather than a bare string. `parseLorentzIndex` / `parseMomentum` (`pkg/pipeline/codec/parser.go`) hard-require the string `"D"` and `panicf` on anything else, so the contractor *rejects* such input (clean non-zero exit, empty stdout) — it does not produce a wrong answer. Inputs carrying `["Plus", -4, "D"]` dimension labels cannot currently be contracted.

---

## Non-deterministic output ordering

The pipeline is streaming and concurrent — there is no global sort at the end. Several stages mean the final byte sequence of the output `Plus` is not reproducible run-to-run for the same input:

- The merger groups terms in a Go map keyed by pair-signature. Iterating that map at flush time has no guaranteed order, so the addends of the outermost `Plus` come out shuffled.
- Terms arrive at the merger from N pool-worker goroutines in scheduling-dependent order. This affects only which merger key gets created first, which is invisible after the map shuffle above.
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

When the calculator can't fold a node into a coefficient and atomizes it instead (symbolic argument inside `Rational`/`Complex`, non-positive or symbolic exponent in `Power`), the atom's signature is built from the **literal sub-trees as they arrived**, not from canonicalised forms. Two structurally-different-but-algebraically-equal expressions therefore get different signatures and fail to merge.

The canonical illustrating sample is `44-power-times-permuted`:

```json
["Plus",
  ["Times", ["Power", ["Times", "a", "b"], -1], "κ"],
  ["Times", ["Power", ["Times", "b", "a"], -1], "κ"]
]
```

emits `Plus[Times[κ, Power[Times[a,b], -1]], Times[κ, Power[Times[b,a], -1]]]` rather than the expected `Times[2, κ, Power[Times[a,b], -1]]`. The inner `Times` factors should be canonically ordered before signing the surrounding atomized `Power`; they aren't, so the two terms see distinct signatures.

The same root cause applies anywhere a sub-tree of an atomized node could be canonicalised (Rational numerator/denominator, Complex real/imag, Power base/exponent) — fixing it in one place fixes the family.

Closely related, distinct fix site: a parsed `Times[1, x]` embedded in an opaque sub-tree round-trips through `writeExpr` as `["Times", 1, x]` rather than `x`. The multiplicative-identity collapse for parsed-`Times` children isn't implemented (`writeExpr`'s `*expr.TimesExpr` arm). Any fix must restrict the collapse to parsed-`Times` children, never to a monomial's opaque `Val`s, so the [scalar-normal-form.md](scalar-normal-form.md) §5b render-faithfully contract holds.

→ Technical detail: [scalar-normal-form.md](scalar-normal-form.md) §5 and §5b; concrete failing rows in [samples.md](samples.md) *Known implementation flaws* (`41-atomized-rational-numer-folds`, `44-power-times-permuted`).

---

## Non-integer literals

Coefficient arithmetic is exact at any magnitude. The Gaussian-rational ring uses an int32 fast path and transparently promotes to `math/big` (`big.Rat`) when a result exceeds int32, demoting back when a later result fits again. Integer and exact-rational inputs of any size therefore fold and round-trip losslessly — `Times[100000, 100000]` → `10000000000`, `Plus[2147483647, 1]` → `2147483648`, `Complex[100000, 1]·Complex[100000, 1]` → `Complex[9999999999, 200000]`.

What does **not** fold is a *non-integer* numeric literal — a decimal or scientific-notation number such as `1.5` or `1e20`. There is no float coefficient ring; the parser keeps these as opaque `jsontext.Value` atoms, so they carry through verbatim and participate only in byte-identical merging, never in arithmetic. A `Rational` / `Complex` with a non-integer argument therefore atomizes the whole node (see [Opaque normalisation gap](#opaque-normalisation-gap)).

→ Technical detail: [scalar-normal-form.md](scalar-normal-form.md) §6 (exact arithmetic); [architecture.md](architecture.md) §Component Invariants > Parser.
