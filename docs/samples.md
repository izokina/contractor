# Sample Inputs

Small hand-verifiable inputs encoded as entries in `scripts/sample-fixtures.json`, ordered from simplest to most complex. Each entry has a `name` (the identifier used in the tables below), an `input` ExpressionJSON tree, and an `outputs` array of expected results. Verification runs through `scripts/check-fixtures.py` (driven by `bash scripts/run-tests.sh`, which builds the binary and execs the script), piping each input into `./bin/contractor` 20 times to stress-test the merger map-iteration non-determinism.

Tables with an **Output** column describe samples whose actual output should match that value structurally — byte-identical for everything except the order of addends of the outermost `Plus`. The merger groups terms by pair-key in a Go map (see [architecture.md](architecture.md) §Pipeline), so cross-pair-key addends may appear in any order across runs; that is a pass, not a regression. Order *inside* an addend (Times factors, Pair structure, monomial-sorted coefficient `Plus`) is deterministic and must byte-match.

Coefficients are normalised to sum-of-monomials form (see [scalar-normal-form.md](scalar-normal-form.md)) inside `eval.TermFolder` as the parser-side Walker emits each monomial; the Merger then accumulates Scalars per pair-key via a single shared `eval.Calculator`. Outputs are signature-sorted, like-monomials are summed, and zero-coefficient monomials are dropped.

Samples are organised into topical sections below; numbers are not strictly consecutive — each section's range is listed below. Sections: contraction mechanics (01–07, 67–69), parser behavior (08–18, 51–58, 64–65), coefficient folding (19–22, 62, 72–73), scalar arithmetic (23–35, 46–47, 59–61, 63), output structure (36, 70–71), combinatorial (37–39, 66), intentionally opaque (40), known implementation flaws (41–45, 48–50), FeynCalc internal-notation contractions (74–90).

---

## Contraction mechanics

| Name | Input structure | Output | Tests |
|------|----------------|--------|-------|
| `01-cross-pair-merge` | `Pair[a,b] * Pair[a,c]` | `Pair[b,c]` | Shared index across two pairs merges them; no scalar emitted |
| `02-chained-contraction` | `Pair[a,b,D] * Pair[b,c,D] * Pair[c,a,D]` | `"D"` | Three-pair index chain closes on itself; all indices consumed, single D emitted |
| `03-partial-contraction` | `Pair[a,b] * Pair[a,c] * Pair[d,e]` | `Pair[b,c] * Pair[d,e]` | `a` contracts, `d`/`e` stay free; tests multi-pair output and sort order |
| `04-contracted-scalar-arithmetic` | `2·Pair[mu,mu] + 3·Pair[nu,nu]` | `20` | Both pairs contract to 4; `2·4=8`, `3·4=12`, `8+12=20` via integer mul then add |
| `05-long-open-chain` | `Pair[a,b,D] * Pair[b,c,D] * Pair[c,d,D] * Pair[d,e,D]` | `Pair[a,e,D]` | Length-4 open chain collapses pairwise to a single surviving pair on the endpoints |
| `06-mixed-hasd-self-pair` | `Pair[mu D, mu]` (one `HasD=true`, one `HasD=false`) | `["Pair", ["LorentzIndex", "mu", "D"], ["LorentzIndex", "mu"]]` | Different `HasD` flags give different LorentzIndex signatures, so the two slots are *not* the same index — no contraction fires; the Pair passes through unchanged |
| `07-triple-shared-index` | `Pair[a,b]·Pair[a,c]·Pair[a,d]` (all `HasD=true`) | `Pair[a,d,D] * Pair[b,c,D]` | `a` appears three times; the first two pairs contract on `a`, the third's `a` is "already consumed" and that pair survives free with `a` as a remaining index |
| `67-five-pair-closed-loop` | `Pair[a,b,D]·Pair[b,c,D]·Pair[c,d,D]·Pair[d,e,D]·Pair[e,a,D]` | `"D"` | Length-5 closed loop; extends sample 02 (length-3) to verify chain length doesn't change the single-`D` output |
| `68-pair-two-momenta-passthrough` | `Pair[Momentum p, Momentum q]` (both `HasD=true`) | `["Pair", ["Momentum", "p", "D"], ["Momentum", "q", "D"]]` | Pair with no Lorentz indices; nothing to contract, the pair passes through with momenta sorted by `Signer.Momentum` byte-signature |
| `69-momentum-survives-lorentz-contract` | `Pair[mu, p, D] · Pair[mu, q, D]` | `["Pair", ["Momentum", "p", "D"], ["Momentum", "q", "D"]]` | Lorentz `mu` contracts across the two pairs; both surviving Momenta merge into a single output Pair (analogue of sample 01 with mixed LorentzIndex/Momentum content) |

---

## Parser behavior

| Name | Input structure | Output | Tests |
|------|----------------|--------|-------|
| `08-power-contraction` | `Power[Pair[m,n D], 2] * Pair[m,p D]` | `"D" * Pair[m,p D]` | Parser-side Walker expands `Power[Pair, 2]` into two copies; the doubled pair self-contracts to `"D"`; the third pair survives because its `m` was already consumed by the time it is visited |
| `09-power-zero-base` | `Power[0, 4]` | `0` | `Power[0, n]` for `n > 0` expands to `Times[0, …, 0]`; zero-in-product propagation drops the monomial; output is integer `0` |
| `10-composite-momentum` | `Pair[mu, (-p1-p2) D] * Pair[mu, p3 D]` | `Pair[p3, (-p1-p2) D]` | Composite (sum-expression) momentum argument; LorentzIndex contracted, composite momentum survives as opaque source |
| `11-plus-in-times` | `Times[Plus[R(1/4), -R(1/8)], Pair[mu,nu]]` | `R(1/8) * Pair[mu,nu]` | `Plus` inside `Times` distributes into separate terms in the parser; the two Rational coefficients fold to `R(1/8)` in the merger |
| `12-power-of-sum` | `Power[Plus[1, D], 2]` | `Plus[1, 2·"D", "D"·"D"]` | `Power[scalar-Plus, n]` with positive int32 `n` is expanded by the scalar Walker into a Cartesian product of monomials |
| `13-power-four` | `Power[κ, 4]` | `Times["κ","κ","κ","κ"]` | `Power[_, n]` with `n > 2`; n=4 expansion produces a 4-factor monomial |
| `14-power-plus-cubed` | `Power[Plus[a, b], 3]` | `Plus[a³, 3·a²b, 3·ab², b³]` | Cartesian explosion of size 8 grouping by signature into 4 monomials with multiplicities `{1, 3, 3, 1}` |
| `15-plus-folds-to-int` | `Plus[2, 3]` | `5` | Pure-scalar `Plus` at root with empty-signature monomials folds to a bare integer |
| `16-power-symbolic-exponent` | `Power["a", "n"]` | `["Power", "a", "n"]` | Symbolic exponent is not a positive int32 → atomized per §4.6; opaque atom is emitted as-is |
| `17-algebraic-identity-binomial` | `a² + 2ab + b² − (a+b)²` | `0` | Comprehensive cancellation: Power expansion of `(a+b)²` produces exactly `a² + 2ab + b²`; subtraction gives empty Scalar → integer `0` |
| `18-times-neg-one-times-symbol` | `Times[-1, "κ"]` | `["Times", -1, "κ"]` | Coefficient `-1` is emitted as a literal Times factor (only the multiplicative identity `+1` is omitted: `writeMonomial` suppresses its Coeff factor when `m.Coeff == expr.OneComplex`) |
| `51-power-plus-neg-times-symbol` | `Power[Plus[a, b], -1] · κ` | `["Times", "κ", ["Power", ["Plus", "a", "b"], -1]]` | Plus inside an opaque (negative-exponent) Power inside a Times monomial; verifies the writer doesn't leak Times-context flatten state into the inner Plus child of an atomized Power (Plus name doesn't match Times context, so the flatten path correctly stays inert) |
| `52-power-times-symbolic-exp` | `Power[Times[a, b], "n"] · κ` | `["Times", "κ", ["Power", ["Times", "a", "b"], "n"]]` | Times inside an opaque (symbolic-exponent) Power inside a Times monomial; second tripwire for the Power-child writer flatten leak after sample 44 — same name match (Times-in-Times-context) but a different atomization trigger |
| `53-power-plus-symbolic-exp` | `Power[Plus[a, b], "n"] · κ` | `["Times", "κ", ["Power", ["Plus", "a", "b"], "n"]]` | Plus inside an opaque (symbolic-exponent) Power; complements 52 by confirming the flatten-bug condition is name-match (Times-in-Times) rather than generic "child of opaque Power" |
| `54-power-times-squared` | `Power[Times[a, b], 2]` | `["Times", "a", "a", "b", "b"]` | Positive-int exponent over a multi-factor Times base; the parser-side Walker expands to two copies of the Times, distribution collapses to a 4-opaque monomial sorted by signature |
| `55-power-of-power` | `Power[Power[a, 2], 3]` | `["Times", "a", "a", "a", "a", "a", "a"]` | Nested Power expansion; the outer Power expands to 3 copies of the inner, each itself expands to 2 copies of `a` — total 6 |
| `56-power-int-int` | `Power[2, 5]` | `32` | Pure-integer exponentiation; expansion to `Times[2, 2, 2, 2, 2]` followed by int32 coefficient fold |
| `57-power-one-symbolic` | `Power[1, "n"]` | `["Power", 1, "n"]` | Symbolic exponent → atomized regardless of base; verifies that even `1^n` is preserved opaque (no mathematical-identity collapse like `1^n → 1`) |
| `58-power-zero-zero` | `Power[0, 0]` | `["Power", 0, 0]` | `0^0` is undefined; per §4.3 zero exponent is atomized regardless of base |
| `64-times-of-times-flatten` | `Times[Times[a, b], Times[c, d]]` | `["Times", "a", "b", "c", "d"]` | Walker flattens nested same-context Times during expansion; the four leaves end up in a single monomial's opaque slice |
| `65-plus-of-plus-flatten` | `Plus[Plus[a, b], Plus[c, d]]` | `["Plus", "a", "b", "c", "d"]` | Symmetric to 64 for Plus; each leaf becomes its own monomial in the merger's empty-pair-key Scalar |

---

## Coefficient folding

The `eval.Calculator` groups monomials by their joint atom-signature, folds coefficients in the int32-bounded Gaussian-rational ring, drops zero-coefficient monomials, and sorts by signature. See [scalar-normal-form.md](scalar-normal-form.md) for the algebra.

| Name | Input structure | Output | Tests |
|------|----------------|--------|-------|
| `19-coefficient-sum` | `3 * Pair[mu,nu] + (-1) * Pair[nu,mu]` | `2 * Pair[mu D, nu D]` | Same pair structure after sorting; integer coefficients add (3 + (−1) = 2) |
| `20-string-symbol-grouping` | `D·R(1/4)·Pair + D·R(-1/8)·Pair + R(1/2)·Pair` | `Plus[R(1/2), R(1/8)·"D"] * Pair` | Two monomial signatures (empty, `D`); per-monomial Rationals fold (`R(1/4)+R(-1/8)=R(1/8)`) |
| `21-d-sig-cancellation` | `3·"D"·Pair[a,a,D] + (-3)·Pair[b,b,D]·Pair[c,c,D]` | `0` | After contraction both terms reduce to `"D"·"D"` with coefficients `3` and `-3`; the cancelled monomial is dropped, leaving the empty sum `0` (zero-in-product propagation comes for free from this rule) |
| `22-multi-term-cancellation` | `1·κ + 2·κ + 3·κ + (-4)·κ + (-2)·κ` | `0` | Five terms in a single monomial-signature group; coefficients sum to zero; the empty Scalar emits as integer `0` |
| `62-times-neg-one-neg-one-symbol` | `Times[-1, -1, "κ"]` | `"κ"` | Sign cancellation: `(-1)·(-1) = 1`, the multiplicative identity is suppressed, leaving the bare opaque |
| `72-times-mixed-coeff-symbols` | `Times[2, "a", 3, "b"]` | `["Times", 6, "a", "b"]` | Integer factors fold (`2·3 = 6`) regardless of position among the Times children; opaques sort by signature after the coefficient |
| `73-plus-cancellation-with-survivor` | `2·x + 3·y + (-2)·x` | `["Times", 3, "y"]` | Like-monomial (`x`) coefficients sum to zero and the monomial is dropped; the surviving `3·y` renders as a single-monomial Term |

---

## Scalar arithmetic

`Rational[a, b]` and `Complex[a, b]` are folded into the coefficient ring whenever their arguments fit (int32 numerator/denominator, int32-or-Rational real/imag); see [scalar-normal-form.md](scalar-normal-form.md) §4.4–§4.5. Other scalar atoms (`FeynAmpDenominator`, `PropagatorDenominator`, opaque `Power`, etc.) are carried through as opaque atoms keyed by signature, so two terms with the same opaque structure still merge.

| Name | Input structure | Output | Tests |
|------|----------------|--------|-------|
| `23-rational-reducible-to-int` | `Rational[6, 3]` | `2` | After reduction `q = 1` invariant fires: `6/3 = 2/1` collapses to integer `2` |
| `24-rational-negative-denom` | `Rational[3, -7]` | `Rational[-3, 7]` | `q > 0` invariant: a negative denominator flips the sign of the numerator |
| `25-complex-zero-imag` | `Complex[5, 0]` | `5` | `b = 0` invariant: Complex with zero imaginary part reduces to its real part |
| `26-complex-int-rational` | `Complex[2, R(1/3)] * Pair[mu,mu,D]` | `Complex[2, R(1/3)] * "D"` | Complex with int real and Rational imag survives contraction (covers §4.5 mixed-type Complex) |
| `27-fraction-arithmetic` | `(R(1/2) + R(1/3)) * 12` | `10` | Rational add (`R(5/6)`) and then `R(5/6)·12 = 10` reducing to integer |
| `28-rational-cascade` | `R(1/2) * R(1/2) * R(1/2) * R(1/2) * Pair[mu,nu,D]` | `R(1/16) * Pair[mu,nu,D]` | Cascading Rational multiplication anchored by a non-contracting Pair |
| `29-rational-symbolic-numer` | `Rational["x", 4]` | `["Rational", "x", 4]` | §4.4: symbolic numerator → not in coefficient ring → atomized; opaque atom is emitted as-is |
| `30-float-times-symbol` | `Times[0.5, "κ"]` | `["Times", "κ", 0.5]` | §7: floats are opaque atoms (the coefficient ring is int32-bounded); `0.5` carries through as a `jsontext.Value` atom |
| `31-complex-mul-conjugate` | `(1+i) · (1-i)` | `2` | Complex conjugate product: `(1+i)(1−i) = 1 − i² = 2`; tests Complex multiplication with both nonzero real and imaginary parts |
| `32-rational-mul-needs-gcd` | `Rational[2, 3] · Rational[3, 4]` | `Rational[1, 2]` | Rational multiplication: `(2·3)/(3·4) = 6/12` reduces to `Rational[1, 2]` via gcd |
| `33-rational-mul-collapses-to-int` | `Rational[2, 3] · Rational[3, 2]` | `1` | Rational multiplication collapses to integer when `gcd` reduction yields `q = 1` |
| `34-power-pair-cubed` | `Power[Pair[m,n,D], 3]` | `"D" * Pair[m,n,D]` | Cube of a Pair: parser-side Walker expands to three copies; first two contract on shared `m,n` to `"D"`, third survives free (analogous to sample 08 with one extra copy) |
| `35-zero-distribution` | `Times[Plus[1, -1], "κ"]` | `0` | Inner `Plus[1, -1]` folds to `0`; `Times[0, κ]` gives a zero-coefficient monomial which is dropped → empty Scalar → integer `0` |
| `46-rational-add-overflow` | `Rational[1, 100000] + Rational[1, 100000]` | `Rational[1, 50000]` | Rational addition is followed by gcd reduction (§3 invariant `gcd(\|p\|,q)=1`); after reduction the result `2/100000` collapses to `1/50000`, fitting back inside the int32-bounded ring |
| `47-rational-add-mixed-denom` | `Rational[1, 3] + Rational[1, 6]` | `Rational[1, 2]` | Mixed-denominator Rational addition: lcm of denominators is `6`, sum is `3/6`, gcd reduction yields `1/2`; same code path as sample 46 with mixed denominators |
| `59-rational-zero-numer` | `Rational[0, 5]` | `0` | §3 invariant `p ≠ 0`: a Rational with zero numerator collapses to integer `0` |
| `60-complex-zero-zero` | `Complex[0, 0]` | `0` | §3 invariant `b = 0` collapses Complex to its real part; with real also `0`, the result is integer `0` |
| `61-complex-rational-rational` | `Complex[R(1/2), R(1/4)]` | `["Complex", ["Rational", 1, 2], ["Rational", 1, 4]]` | Both real and imag are reduced Rationals — both inside the coefficient ring per §4.5; result is a single Complex coefficient |
| `63-plus-complex-complex` | `Complex[1, 2] + Complex[3, 4]` | `["Complex", 4, 6]` | Direct Complex addition; both fold to coefficients, sum is computed in the ring |

---

## Output structure

Sample 36 has multiple top-level addends corresponding to distinct pair-keys; their order in the emitted `Plus` is non-deterministic (Go map iteration). The order shown below is one valid arrangement — any permutation is equally valid.

| Name | Input structure | Output | Tests |
|------|----------------|--------|-------|
| `36-three-pair-keys` | `Pair[a,b]·Pair[a,p] + Pair[c,d]·Pair[c,q] + Pair[e,f]·Pair[e,r]` | `Plus[Pair[b,p,D], Pair[d,q,D], Pair[f,r,D]]` | Three distinct surviving pair-keys; verifies merger output cardinality and the multiset comparison rule for ≥3 groups |
| `70-multiletter-symbol-sort` | `Times["abc", "ab", "a"]` | `["Times", "a", "ab", "abc"]` | Verifies opaque sort order is byte-lex on JSON-encoded form: prefix-shorter strings sort before longer ones (`"a"` < `"ab"` < `"abc"`) regardless of input order |
| `71-multi-pair-key-with-opaque-coeffs` | `x · Pair[a,b,D] + y · Pair[c,d,D]` | `Plus[Times[x, Pair[a,b,D]], Times[y, Pair[c,d,D]]]` | Two distinct pair-keys, each with an opaque-symbol coefficient; verifies multi-key Plus output with non-identity coefficients (and the multiset comparison rule on the outer Plus) |

---

## Combinatorial / large-input reductions

Larger inputs that combine multiple mechanisms (Power expansion, distribution, contraction, coefficient folding) and reduce to small canonical outputs.

| Name | Input structure | Output | Tests |
|------|----------------|--------|-------|
| `37-distribute-int-sums` | `Times[Plus[1,2], Plus[3,4], Plus[5,6]]` | `231` | Distribution produces 8 integer monomials, all empty-signature; `(1+2)·(3+4)·(5+6) = 3·7·11 = 231` |
| `38-power-of-d-sum-cubed` | `Power[Plus[1, D], 3]` | `Plus[1, 3·"D", 3·"D"·"D", "D"·"D"·"D"]` | Cartesian explosion of size 8 groups into 4 monomials with multiplicities `{1, 3, 3, 1}`; extends sample 12 to n=3 |
| `39-power-times-contraction` | `Power[Plus[1, D], 2] * Pair[mu,mu,D]` | `Plus["D", 2·"D"·"D", "D"·"D"·"D"]` | Combines scalar `Power` expansion (sample 12) with Lorentz contraction; `(1+D)²·D = D + 2D² + D³` |
| `66-distribute-binomial-pair-contracted` | `Times[Plus[a, b], Plus[c, d], Pair[mu, mu, D]]` | `Plus[D·a·c, D·a·d, D·b·c, D·b·d]` | Distribution of two binomials produces 4 monomials; the contracted Pair multiplies each by `D`; verifies sort-by-joint-signature within a single-pair-key Scalar |

---

## Intentionally opaque

| Name | Input structure | Output | Rationale |
|------|----------------|--------|-----------|
| `40-power-pair-self-zero-exponent` | `Power[Pair[mu,mu,D], 0]` | `["Power", ["Pair", ["LorentzIndex", "mu", "D"], ["LorentzIndex", "mu", "D"]], 0]` | `Power[_, 0]` is kept opaque (`Power[0, 0]` is undefined, so reducing to `1` would change semantics on `S = 0`). Tests CLAUDE.md's `IsScalar()` 4-case warning: a non-scalar child (Pair) inside `Power[_, 0]` makes the Power atomized but `IsScalar=true` (per `ExpInt ≤ 0`). The Pair must be preserved inside the opaque atom, *not* extracted into `Term.Pairs` and contracted. See [scalar-normal-form.md](scalar-normal-form.md) §4.3. |

---

## Known implementation flaws

These samples document behaviours where the contractor's actual output diverges from what `docs/scalar-normal-form.md` prescribes. The **Current (wrong) output** column shows what the binary emits today (also encoded as the `outputs` entry in `scripts/sample-fixtures.json`); the *Spec* column gives the canonical output that the docs predict. The pass criterion for these rows is that the binary's output still matches the current (wrong) form in the fixture — when the bug is fixed, the `tester` agent will report a `spec shift` and (per its scope) edit the fixture's `outputs` to the spec form. Updating *this* table to graduate the row back into the regular sections is the caller's responsibility, not the tester's.

| Name | Input structure | Current (wrong) output | Spec | Bug |
|------|----------------|------------------------|------|-----|
| `41-atomized-rational-numer-folds` | `Rational[Plus[2, 3], "x"]·κ + Rational[5, "x"]·κ` | `Plus[Times["κ", Rational[5, "x"]], Times["κ", Rational[Plus[2, 3], "x"]]]` | `Times[2, "κ", Rational[5, "x"]]` | §5 Decision C1: opaque-atom signature must use *normalised* sub-trees, so `Plus[2, 3]` should fold to `5` before signing. Currently the literal tree is signed and the two atoms have distinct signatures, preventing merge. The same root cause applies to other atomized positions (Rational denominator, Complex real/imag, Power exponent) — fixing it here fixes the family. |
| `42-int32-overflow` | `Times[100000, 100000]` | `["Times", 100000, 100000]` | `10000000000` (int64 atom) | §6 overflow policy: `100000 · 100000 = 10¹⁰` exceeds int32 max (~2.15·10⁹). The docs require the product to be computed at int64 precision and emitted as an int64 atom. Currently the multiplication is skipped and the un-evaluated `Times` is emitted, leaving an unfolded coefficient in the output. Canonical case for the §6 atomization bug. |
| `43-complex-polynomial-imag` | `Complex[0, Plus["x", 1]]` | `["Complex", 0, ["Plus", "x", 1]]` | `Plus[Complex[0, 1], Times[Complex[0, 1], "x"]]` | §4.5 polynomial-imaginary branch unimplemented: when `Im` is a multi-monomial polynomial, the spec requires expanding to `A + i·T = i·(x + 1) = i + i·x`. Currently the `Plus` is left intact inside the `Complex`, so the Complex is not folded into a normal-form sum. Same root cause covers Complex with symbolic real (`Complex["x", 1]`). |
| `44-power-times-permuted` | `Power[Times["a","b"], -1]·κ + Power[Times["b","a"], -1]·κ` | `Plus[Times["κ", Power[Times["a","b"], -1]], Times["κ", Power[Times["b","a"], -1]]]` | `Times[2, "κ", Power[Times["a", "b"], -1]]` | §5 Decision C1 for `Times` inside an atomized `Power` child. Spec: the inner `Times` factors should be canonicalised (sorted) before signing, so the two atoms have the same signature and merge. Currently the input order is preserved (analogous case applies to `Plus`-inside-`Power`). |
| `45-plus-int32-overflow` | `Plus[2147483647, 1]` | `["Plus", 2147483647, 1]` *(addend order non-deterministic across runs — multiset-equal forms)* | `2147483648` (int64 atom) | §6 overflow not canonicalised. The writer renders `Atom{int32(1)}` faithfully (opaques and parsed-Times children alike), so the output preserves all the input data. The residual pattern is the same as 42: the calculator's `foldBuf` overflow path atomizes the incoming operand rather than the int64 sum, so the two addends survive as separate monomials in an un-folded `Plus`. Whichever operand arrives second at the merger ends up wrapped as the int32 opaque, hence the addend-order non-determinism (multiset-equivalent). |
| `48-rational-mul-overflow` | `Rational[100000, 1] · Rational[100000, 1]` | `["Times", 100000, ["Rational", 100000, 1]]` | `10000000000` (int64 atom) | **Asymmetric "split-flush" on Rational-multiplication overflow.** The product numerator `10¹⁰` overflows int32. Per §6 the whole node should atomize as an int64. Instead the implementation flushes the accumulator as an int (after `q=1` collapse) and emits the *un-multiplied* second operand as a raw `Rational[100000, 1]` — which itself violates §3 `q ≠ 1`. The output is internally inconsistent: one factor is an int, the other is a Rational that should have been an int. |
| `49-complex-mul-real-overflow` | `Complex[100000, 1] · Complex[100000, 1]` | `["Times", ["Complex", 100000, 1], ["Complex", 100000, 1]]` | `Complex[9999999999, 200000]` (with int64-atom real, since `100000² − 1 ≈ 10¹⁰` overflows int32) | §6 overflow in the Complex multiplication path. Spec: when the int32 coefficient ring can't represent the product, atomize per §6. Currently the `Times` is left entirely unevaluated — both Complex operands kept as-is (uniformly-unfolded, distinct from the asymmetric "split-flush" of Rational mul in sample 48). Same shape applies to Complex addition overflow. |
| `50-times-plus-overflow-symbol` | `Times[Plus[2147483647, 1], "κ"]` | `["Plus", ["Times", 2147483647, "κ"], ["Times", "κ", 1]]` | `Times[2147483648, "κ"]` (with int64-atom coefficient) | Distribution + §6 overflow. `Times` distributes `Plus` to two monomials sharing signature `[κ]`; on overflow the second monomial carries an extra `Atom{int32(1)}` opaque, which the writer now renders faithfully as the literal `1`. Both operands preserved; the Plus-of-two-Times shape persists because the calculator doesn't fold the residual into a single int64-coefficient monomial. |

---

## FeynCalc internal-notation contractions

These samples exercise the contractor on FeynCalc internal-notation expressions: `Pair`/`LorentzIndex`/`Momentum` trees carrying Unicode Greek indices, `D` dimension labels, and compound momenta such as `Momentum[Plus[p,q]]`. They cover the index-contraction surface end-to-end on realistic tensor expressions, complementing the hand-built atoms above. Each `outputs` value is the contraction's expected result; the binary reproduces it byte-for-byte under the standard 20-runs-per-sample check.

Shorthand: `MTD[μ,ν]` is the D-dimensional metric `Pair[LorentzIndex μ D, LorentzIndex ν D]`, `FVD[p,μ]` the D-dimensional vector `Pair[LorentzIndex μ D, Momentum p D]`, and `SPD[p,q]` the D-dimensional scalar product `Pair[Momentum p D, Momentum q D]`; `MT` (sample 84) is the 4-dimensional metric (no `D` label). Output Pair-slot and Times-factor order follow the contractor's signature sort — so `FVD[p,ν]` prints with the Lorentz slot first and `SPD` momenta print in signature order — and the outermost `Plus` is multiset-compared as elsewhere.

| Name | Input structure | Output | Tests |
|------|----------------|--------|-------|
| `74-fc-metric-trace-d` | `MTD[μ,μ]` | `"D"` | D-dimensional metric trace (single self-paired index) → `D` |
| `75-fc-vector-through-metric` | `MTD[μ,ν]·FVD[p,μ]` | `FVD[p,ν]` | Metric transports a vector index; the surviving Pair is mixed Lorentz/Momentum |
| `76-fc-metric-mediated-sp` | `MTD[μ,ν]·FVD[p,μ]·FVD[q,ν]` | `SPD[p,q]` | Two simultaneous Lorentz contractions through a metric collapse to a scalar product |
| `77-fc-distribute-coeff-signs` | `MTD[ν,ρ]·(2·MTD[μ,ν] − 3·FVD[p,μ]·FVD[q,ν])` | `2·MTD[μ,ρ] − 3·FVD[p,μ]·FVD[q,ρ]` | Linearity: a metric distributes over a sum, preserving numeric coefficients and signs |
| `78-fc-compound-momentum-metric` | `MTD[μ,ν]·FVD[p+q,μ]` | `FVD[p+q,ν]` | Compound momentum `Momentum[Plus[p,q]]` carried through a metric contraction as an opaque source |
| `79-fc-neg-momentum-sp` | `−FVD[p,μ]·FVD[q,μ]` | `["Times", -1, SPD[p,q]]` | Overall `−1` survives the vector-vector contraction as a Times coefficient |
| `80-fc-partial-contract-free-index` | `FVD[p,μ]·MTD[ν,ρ]·FVD[q,ρ]` | `FVD[p,μ]·FVD[q,ν]` | `ρ` contracts the metric with one vector; `μ` stays free, so the product survives as two Pairs |
| `81-fc-spectator-scalar-product` | `MTD[μ,ν]·MTD[ν,ρ]·SPD[p,q]` | `MTD[μ,ρ]·SPD[p,q]` | Pre-existing scalar product is a spectator scalar factor; only the metric chain contracts |
| `82-fc-nonlocal-contraction-graph` | `MTD[α,β]·MTD[α,μ]·MTD[β,ν]·FVD[p,μ]·FVD[q,ν]` | `SPD[p,q]` | Five-pair non-adjacent contraction graph fully collapses to one scalar product |
| `83-fc-independent-dummies-in-sum` | `FVD[p,μ]·FVD[q,μ] + FVD[r,ν]·FVD[s,ν]` | `Plus[SPD[r,s], SPD[p,q]]` | Independent dummy indices in different addends contract separately (outermost `Plus` multiset) |
| `84-fc-metric-trace-4` | `MT[μ,μ]` (no `D` label) | `4` | 4-dimensional metric trace → `4` (`HasD=false`), the counterpart of 74 |
| `85-fc-noop-no-shared-index` | `MTD[μ,ν]·FVD[p,ρ]` | `MTD[μ,ν]·FVD[p,ρ]` | No repeated index → no-op; the product passes through unchanged |
| `86-fc-noop-spectator-sp` | `FVD[r,μ]·SPD[p,q]` | `FVD[r,μ]·SPD[p,q]` | No-op preservation with a free vector and an existing scalar product |
| `87-fc-rational-neg-coeff-distribute` | `(1/2)·MTD[μ,ν]·MTD[ν,ρ] − 3·FVD[p,μ]·FVD[q,ρ]` | `Plus[ −3·FVD[p,μ]·FVD[q,ρ], (1/2)·MTD[μ,ρ] ]` | Rational and negative coefficients survive partial metric contraction |
| `88-fc-symbolic-prefactors` | `x·MTD[μ,ν]·MTD[ν,ρ] + y·FVD[p,μ]·FVD[q,ρ]` | `Plus[ y·FVD[p,μ]·FVD[q,ρ], x·MTD[μ,ρ] ]` | Symbolic scalar prefactors `x`, `y` carried through as opaque coefficients |
| `89-fc-compound-momentum-sp` | `FVD[p+q,μ]·FVD[r,μ]` | `SPD[p+q, r]` | Compound momentum contracted directly into a scalar product (printed in signature order) |
| `90-fc-graviton-propagator-trace` | `(1/2)·(MTD[μ,α]·MTD[ν,β] + MTD[μ,β]·MTD[ν,α] − MTD[μ,ν]·MTD[α,β])·MTD[μ,ν]` | `["Times", ["Plus", 1, ["Times", ["Rational", -1, 2], "D"]], MTD[α,β]]` | The de Donder graviton-propagator numerator traced against a metric, `η^{μν}P_{μν,αβ} = (1 − D/2)·η_{αβ}`; exercises a scalar-`Plus` coefficient on a surviving Pair (cf. sample 20) |
