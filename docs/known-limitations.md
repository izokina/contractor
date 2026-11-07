# Known Limitations

The contractor's core path — index contraction, like-monomial collapse, structural opaque carry-through — is reliable. The limitations below are the parsing scope (what is recognised, and which input is rejected), malformed index use, which is not checked, the cost of the walk and of large exponents, the merger's memory, which momentum sources are expanded, open gaps that produce non-canonical-but-data-preserving output (opaque normalisation, parsed-`Times[1, x]` collapse), and the treatment of non-integer literals; a short section states the output order. This file is the user-facing index; underlying mechanics are documented in [architecture.md](architecture.md) and [scalar-normal-form.md](scalar-normal-form.md).

---

## Parsing limitations

What's recognised as a structured head: `Plus`, `Times`, `Power`, `Pair`, `Rational`, `Complex`. Everything else — `FeynAmpDenominator`, `PropagatorDenominator`, `DiracGamma`, any other Mathematica head not in that list — is an *unknown head*: a single opaque factor, written back verbatim. Two unknown heads that render identically merge cleanly; that is the only equivalence they participate in.

Inside an unknown head, a `Plus`, `Times`, `Power`, `Rational` or `Complex` is parsed as anywhere else, including the check that `Power`, `Rational` and `Complex` have two arguments, but nothing is multiplied out, and a `Times` keeps its input order (sample 625). A `Pair`, `LorentzIndex` or `Momentum` there is carried as written, without the checks on dimension labels, mixed dimensions and index names below (192), and its indices never contract with those outside (400). A compound head such as `f[a][b]` (`[["f", "a"], "b"]`) is accepted only inside an unknown head.

`Rational` and `Complex` **fold into the coefficient ring when their arguments are integers** (for `Complex`, each part may also be a `Rational` of integers). Integer arguments of any magnitude fold — the ring promotes to `math/big` past int32. Any other `Rational[a, b]` is read as the product `a · b⁻¹` and any other `Complex[a, b]` as the sum `a + i·b`, where `b⁻¹` is a number when `b` is one and otherwise the opaque factor `Power[b, -1]`, against which nothing cancels (`Rational[y, y]` is `y · Power[y, -1]`; samples 297–301, 304, 305). A division by an exact 0 (99, 302, 310) and a `Rational` or `Complex` with a tensor part (303, 306) stay one opaque factor, as written; the indices of the latter never contract.

### `FeynAmpDenominator` / `PropagatorDenominator`

```json
["FeynAmpDenominator",
  ["PropagatorDenominator",
    ["Plus", ["Momentum", "p1", "D"], ["Momentum", "p2", "D"]],
    0
  ]
]
```

`FeynAmpDenominator` is a product of Feynman propagator denominators. Each nested `PropagatorDenominator[q, m]` represents `1 / (q² − m²)` where `q` is a momentum expression and `m` is the mass. `PropagatorDenominator` only ever appears nested inside `FeynAmpDenominator`. These are loop-integral denominators handled by downstream integration routines (e.g. FeynCalc/FIRE); the contraction stage carries them through as opaque factors, written as they came, so two terms with the same `FeynAmpDenominator` structure still merge.

→ Technical detail: [architecture.md](architecture.md) §Component Invariants > Parser; `parseNamedExpr` in `pkg/pipeline/codec/parser.go`.

### Three dimension labels

A `LorentzIndex` or `Momentum` dimension argument must be absent (the 4-dimensional case), the string `"D"`, or the *evanescent* dimension `D − 4` of FeynCalc's `MTE`, `FVE` and `SPE`, written `["Plus", -4, "D"]`. The compound label is read in exactly this form, and only in a dimension slot; elsewhere the same `Plus` is an ordinary sum. Any other label is rejected (clean non-zero exit, empty stdout): a symbol other than `D` (sample 122), and any other compound label, even `D − 4` written as `["Plus", "D", -4]` (231).

### Pairs that mix dimensions are rejected

A `Pair` whose arguments carry different dimension labels, such as `Pair[LorentzIndex[a], LorentzIndex[b, D]]`, is rejected (non-zero exit, empty stdout; samples 06, 123, 124, 187). Contracting it would depend on argument order, and such input indicates an error in the source expression. One index name in two dimensions, in different `Pair`s, contracts in the dimension both share: `D` with 4 gives 4, `D` with `D − 4` gives `D − 4`, and a term in which 4 meets `D − 4` is 0 and is dropped ([feyncalc-notation.md](feyncalc-notation.md) §One index name in two dimensions).

A `LorentzIndex` name must be a string: compound names, such as FeynCalc's dummy indices `$AL[1]`, are rejected (139).

### Input is one ExpressionJSON expression

The contractor reads stdin to its end, and the input must be exactly one ExpressionJSON expression. It rejects (non-zero exit, empty stdout) anything but whitespace after the expression (142); JSON objects, `true`, `false`, `null` and the empty array `[]`, at any depth (141, 147–150); and input nested more than 9,996 arrays deep (182, 191), so that the output, which places a part of the input up to four levels deeper, stays within 10,000. It reads no file: an argument that is not a flag is a usage error (exit status 2).

---

## Malformed index use

The contractor expects the plain Einstein convention: in each term of the expansion, an index name appears at most twice. It does not check this, and input that uses one name three or more times in a term is not rejected. The contractor pairs the occurrences greedily, in the order of the factors, and leaves an odd occurrence free (samples 07, 08, 34, 126). What the walk multiplies out first — a factor that closes a name inside itself, a group of factors that share index names, the copies of a power — pairs its own occurrences before it meets the rest (§Walk memory and time; 127–129), so two orders of the same malformed product can give different results. Input that follows the convention gives the same result in any factor order.

---

## Walk memory and time

The walk expands each summand into terms one at a time. It holds whole, merged, only the terms of a factor that closes an index name inside itself, of a group of factors that share the index names they leave open, and of a tensor power's copies. Every other factor it streams, term by term, walked again for each combination of the factors walked more slowly, so memory stays small however many terms a product of unrelated sums has, and time grows with the terms walked. So:
- a group's terms merge only where its contracted products coincide: two factors that share one name but whose products are all distinct hold the product of their sizes;
- a streamed factor whose terms cancel only once merged with each other, such as `SP[a,b] − SP[a,b]` beside sums it shares nothing with, does not end its product: the product is walked whole, and the merger holds each term it yields until the output. A factor that vanishes once contracted inside itself is multiplied out first and ends the product (samples 294, 322);
- a product nested through a sum, such as the Horner form `K·(K·(K·… + x·g) + x·g)` of a chain, merges only within each product: it is walked term by term, where the same chain written as one product merges as it multiplies (312, 318).

---

## Large exponents

`Power[b, n]` with a positive int32 `n` and a number `b` is folded into one number as the input is read, by repeated squaring: `Power[-1, 2147483647]` is `-1` at once (samples 244–247), and a product with a factor `0` is `0` without folding the powers beside it (249, 250); a power whose value is huge, such as `Power[2, 2147483647]`, is computed in full while the input is read, which is serial. A scalar power whose base is one monomial, such as `x` or `2·x·y`, is raised at once, its coefficient by repeated squaring and each exponent times `n` (484, 490). Any other `Power[b, n]`, a power of a sum or one that holds a Pair, is the product of `n` copies of `b`, multiplied in one at a time with like terms merged after each, and stopping as soon as the product is 0 (290, 291, 296): time grows with `n`, and so does memory where each copy leaves something, such as a Pair that does not contract.

An exponent is an int32. The exponents of a factor add as monomials multiply, and a sum past 2147483647 keeps the two factors apart: `Power[x, 2147483647]·x` is `Times[Power[x, 2147483647], x]` (486). Nothing wraps, and the value is right, but past the bound one value can have more than one form, and the forms do not merge: `Power[x, 2147483647]·x − Power[x, 2147483646]·Power[x, 2]` is not 0 (487), and `x·Power[x, 2147483647] + Power[x, 2147483647]·x` stays two addends (501). A power of a monomial whose exponents would pass the bound is multiplied copy by copy (489).

---

## Output order

The output is byte-identical across runs, thread counts and permutations of the input's root summands: its order comes from content alone. The pair-free monomials come first, each its own addend of the root `Plus`, then the tensor terms ordered by their Pairs. Inside a term, Pairs go metrics, then vectors, then scalar products, each kind ordered by its slots (an index by name, a momentum by source, then either by dimension: no label, `D`, `D-4`; sample 230); opaque factors go numbers, then symbols, then composite expressions. A factor with an exponent `n` above 1 is written `Power[x, n]`, and monomials rank as if it were `n` copies of `x`: `x` before `x²` before `x·y` (498), and `x²·y` before `x·y²` (499); past the int32 bound ([Large exponents](#large-exponents)) two powers of one factor are compared as they stand. The inside of an opaque factor keeps its parsed form (see [Opaque normalisation gap](#opaque-normalisation-gap)).

→ Technical detail: [architecture.md](architecture.md) §Output order.

---

## Memory and temporary files

The merger keeps each distinct term it has merged until it writes the output. `-merge-memory N` bounds that to about `N` bytes: whenever the merger holds more, it moves its terms to a temporary file, and before it writes the output it merges them again, a part at a time, each part under the same bound. The output is the same whatever the bound; a small one moves terms often and makes the run slower. The default, `0`, keeps every term in memory. The bound counts the merged terms alone, approximately and without coefficients beyond int32; it does not cover the summands the workers hold, the products the walk multiplies out, the table of names, momentum sources and opaque factors, which grows with the distinct values in the input, or the first addend of the output, which the writer holds until the second begins.

A run makes one file, named `contractor-*`, in `TMPDIR` or the system's default. Where the system allows it for an open file, the contractor removes the file's name as soon as it has made it, so nothing is left behind even if it is killed; elsewhere it removes the file before it exits, and a killed contractor leaves it behind. If the file cannot be made or written while the input is read, the contractor exits 1 with nothing on stdout; while the output is written, stdout keeps the part already written. When the input is also malformed, which of the two errors is reported depends on timing.

---

## Momentum sources

Physics framing: `Momentum[p]` labels a momentum object whose dimension is set by an optional second argument (see [feyncalc-notation.md](feyncalc-notation.md)); a `Pair` of two momenta is the scalar product, and a `Pair` of a `LorentzIndex` and a `Momentum` is a Lorentz vector. A `Pair` is linear in each slot, and the parser applies that: a `Pair` that the expansion reaches, and that holds a momentum whose first argument, its *source*, is a linear combination of labels with exact numeric coefficients, is read as the sum of the Pairs of its labels, each with its coefficient (samples 10, 199–203). So every Pair that contracts holds single labels.

A sum splits into its addends at any depth (201), and a product of exact numbers and one other factor scales that factor; anything else is one opaque label, keyed and written as it came: a symbol, a product with a symbolic or non-integer factor such as `Momentum[x p]` or `Momentum[0.5 p]` (205, 216), an unknown head such as `Polarization[...]`, a `Power` (217). Two labels merge only when they render identically, so `Momentum[x (p + q)]` and `Momentum[x (q + p)]` stay two momenta.

Nothing is expanded inside an unknown head, where a `Pair` or `Momentum` is an unknown head too (206), nor inside an opaque `Power`, `Rational` or `Complex` or a momentum label, where a Pair keeps its arguments as written, except that a vector's index comes first (204, 207, 209, 214, 215, 218). A `Pair` slot that is itself a sum of `Momentum`s (`Pair[Momentum[p1] + Momentum[p2], k]`) is rejected, as any head other than `LorentzIndex` and `Momentum` in a slot is (140, 219).

Each expanded Pair multiplies the terms of its summand by the number of its labels before the walk, and a term whose dimensions have an empty meet vanishes only once it is contracted, after it has been expanded.

---

## Opaque normalisation gap

When the calculator can't fold a node into a coefficient and atomizes it instead (a non-positive or symbolic exponent in `Power`, the `Power[b, -1]` that a `Rational` stands for, a division by an exact 0), the atom's key is built from the **literal sub-trees as they arrived**, not from canonicalised forms. Two structurally-different-but-algebraically-equal expressions therefore get different keys and fail to merge.

The canonical illustrating sample is `44-power-times-permuted`:

```json
["Plus",
  ["Times", ["Power", ["Times", "a", "b"], -1], "κ"],
  ["Times", ["Power", ["Times", "b", "a"], -1], "κ"]
]
```

emits `Plus[Times[κ, Power[Times[a,b], -1]], Times[κ, Power[Times[b,a], -1]]]` rather than the expected `Times[2, κ, Power[Times[a,b], -1]]`. The inner `Times` factors should be canonically ordered before keying the surrounding atomized `Power`; they aren't, so the two terms get distinct keys.

The same root cause applies anywhere a sub-tree of an atomized node could be canonicalised (Power base/exponent, the arguments of an unknown head) — fixing it in one place fixes the family.

Closely related, distinct fix site: a parsed `Times[1, x]` embedded in an opaque sub-tree round-trips through `writeExpr` as `["Times", 1, x]` rather than `x`. The multiplicative-identity collapse for parsed-`Times` children isn't implemented (`writeExpr`'s `*expr.TimesExpr` arm). Any fix must restrict the collapse to parsed-`Times` children, never to a monomial's opaque factors, so the [scalar-normal-form.md](scalar-normal-form.md) §5b render-faithfully contract holds.

→ Technical detail: [scalar-normal-form.md](scalar-normal-form.md) §5 and §5b; the concrete failing row in [samples.md](samples.md) *Known implementation flaws* (`44-power-times-permuted`).

---

## Non-integer literals

Integer and exact-rational coefficients fold exactly at any magnitude. What does **not** fold is a *non-integer* numeric literal — a decimal or scientific-notation number such as `1.5` or `1e20`. There is no float coefficient ring; the parser keeps these as opaque `jsontext.Value` atoms, so they carry through verbatim and participate only in byte-identical merging, never in arithmetic. A `Rational` / `Complex` with a non-integer argument is read as the product or sum it stands for, in which the literal is one opaque factor: `Rational[1.5, 3]` is `Rational[1, 3] · 1.5`.

→ Technical detail: [scalar-normal-form.md](scalar-normal-form.md) §6 (exact arithmetic); [architecture.md](architecture.md) §Component Invariants > Parser.
