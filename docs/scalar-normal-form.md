# Scalar Normal Form

Mathematical model for the scalar coefficients (`Term.Coeff`) and the explicit
trade-off decisions that bound the model. This document is descriptive only —
it specifies *what* a normalised scalar is and *what equalities* must (and must
not) be detected. No file references, no implementation steps.

---

## 1. Scope

The scalar layer covers the `Term.Coeff` field — the part of an expression
that is **not** a Lorentz/Momentum tensor structure. Tensor `Pair`s live
outside this model and are handled by the contractor.

The atoms the scalar layer sees fall into three categories:

- **Numeric atoms.** `int32` integers (e.g. `0`, `1`, and the `4` emitted by contraction), and `*big.Int` for integers beyond int32 range (see §6).
- **Symbolic / opaque atoms.** Strings (`"D"`, `"kappa"`, …), `jsontext.Value`
  blobs (floats and other non-integer numeric literals, named constants like `Pi`),
  and reassembled `[]any` for unknown head-objects (`FeynAmpDenominator[…]`,
  `PropagatorDenominator[…]`, anything else the parser doesn't recognise).
- **Structured scalars.** `Power`, `Rational`, `Complex`. These are
  *sometimes* foldable into the normal form and *sometimes* opaque, depending
  on their arguments — see §4 and §5.

---

## 2. The normal form

A normalised scalar is a finite sum of monomials over a Gaussian-rational-like
coefficient ring:

```
S  =  Σ_k  c_k · M_k
```

with the following invariants:

1. **Coefficient.** Each `c_k` is a *coefficient value* in the sense of §3
   (integer / Rational / Complex of the same), and `c_k ≠ 0`.
2. **Monomial.** Each `M_k` is a product of distinct atom-powers
   `a_1^{e_1} · a_2^{e_2} · … · a_n^{e_n}` where:
   - the `a_i` are **non-coefficient** atoms (symbolic / opaque / structured-but-opaque, see §5);
   - their `Signature`s are pairwise distinct within the monomial;
   - exponents `e_i` are non-zero integers (positive; see §4 for why negative exponents do not occur);
   - the `(Signature, e_i)` entries are sorted by `Signature`.
3. **Distinct monomials.** The signatures of the `M_k` are pairwise distinct.
   No two terms share a monomial signature.
4. **Canonical order.** The `M_k` are sorted by some total order on monomial
   signatures (lexicographic on the joint atom-signature concatenation is
   sufficient).
5. **Empty sum.** The empty sum is the additive zero, written `0` (a single
   `int32` atom of value 0). The sum with a single empty monomial and
   coefficient `1` is the multiplicative identity, written `1`.

Equality: two scalars are equal under this model iff their canonical
serialisations (via `Signer` over the normalised tree) are byte-identical.

---

## 3. Coefficient values

A **coefficient value** `c` is one of:

- `Integer` — an integer atom (`int32`, or `*big.Int` beyond int32 range; in particular, `0` and `1`);
- `Rational[p, q]` — a `Rational` whose numerator `p` and denominator `q`
  are both integers (int32 fast path, big fallback), in canonical reduced form:
  - `q > 0`,
  - `gcd(|p|, q) = 1`,
  - `q ≠ 1` (otherwise the rational collapses to the integer `p`),
  - `p ≠ 0` (otherwise the rational collapses to the integer `0`);
- `Complex[a, b]` — a `Complex` whose real part `a` and imaginary part `b`
  are each either `Integer` or `Rational`, with
  `b ≠ 0` (otherwise the complex collapses to its real part).

This is the ring `ℚ[i]` of Gaussian rationals, represented with an int32 fast
path and a `math/big` fallback (§6). It is closed under addition, subtraction,
and multiplication; division is defined except by zero.

Coefficients are exact at any magnitude; the int32 representation is a
performance fast path, not a semantic bound. See §6 and §7.

---

## 4. Composition rules

Inputs are normalised; outputs must be normalised. The following rules define
each composer's behaviour. Where a rule cannot be carried out (non-monomial
denominator, non-integer exponent, symbolic argument), the offending node is
**atomized** — wrapped as a single opaque atom whose signature is derived from
the normalised sub-trees (§5).

### 4.1 `Plus(S, T)`

Merge the two monomial multisets keyed by monomial signature; for like
monomials, add coefficients in the coefficient ring (§3). Drop any monomial
whose coefficient becomes the integer `0`. Re-sort by monomial signature.

### 4.2 `Times(S, T)`

Distribute: for every pair `(c_k M_k, d_l N_l)` produce
`(c_k · d_l) · (M_k · N_l)`. The product of monomials merges atom-power
maps by **adding exponents**; entries whose exponent becomes zero are
dropped (this gives `Power[a, 0] = 1` for free). The product of
coefficients is computed in the coefficient ring (§6).

After distribution, collect like monomials by signature, summing
coefficients (§4.1 rules apply). Any monomial with coefficient `0` is
dropped, which gives **zero-in-product propagation** for free: if either
operand is the zero polynomial, no monomial pairs survive and the product
is zero.

### 4.3 `Power(S, n)` for integer `n`

- `n > 0`: compute by repeated `Times` (§4.2). Closed under the model.
- `n = 0`: **atomized** (§5), *not* reduced to `1`. `Power[0, 0]` is
  undefined and the model can't tell at fold time whether `S = 0`.
- `n < 0`: not produced by the composition rules.
  Negative-exponent Power is not synthesised internally; if it arrives in
  the input, see §5.

### 4.4 `Rational[A, B]`

If `A` and `B` are both integers, reduce as in §3 (any magnitude — the ring
promotes to `math/big` past int32). The result is a coefficient value, lifted
to a single monomial `c · 1`.

In all other cases — `A` or `B` containing atoms; `B` a multi-monomial sum;
`B = 0` — the entire `Rational[A, B]` is **atomized** (§5). Decision **B1**.

### 4.5 `Complex[A, B]`

If `A` and `B` are both integer-or-Rational coefficients, the result is a
single coefficient value `a + bi` per §3.

Otherwise, if the imaginary part is itself a *normalised polynomial* `T`,
the result is `A + i · T` computed by `Plus` and `Times`: every monomial of
`T` has its coefficient multiplied by `i` (Gaussian-rational arithmetic at
the coefficient level), and the result is `Plus`-merged with the normalisation of `A`.

### 4.6 `Power(S, e)` with non-integer `e`

`Power[S, e]` where `e` is anything other than a positive `int32` is
**atomized** as a single opaque atom (§5). This includes negative integer
exponents in the input, symbolic exponents, rational exponents, and
exponents involving atoms.

---

## 5. Atomisation (Decision B1 + C1)

When the model cannot fold a node into a coefficient or expand it into
monomials, the node becomes a **single opaque atom** which then enters
monomials like any other indeterminate.

The atomization rule:

- **Decision B1 — what gets atomized.** The following are atomized:
  - `Rational[A, B]` whenever it is not a reduced integer-pair coefficient;
  - `Power[S, e]` whenever `e` is not a positive int32 (including negative integers, since `1/(x+y)` is not a Laurent polynomial);
  - `Complex[A, B]` whose normal form is not expressible per §4.5.

- **Decision C1 — how the atom's signature is built.** The opaque atom's
  `Signature` is the canonical encoding (`Signer` output) of the
  **normalised** sub-trees, *not* of the original parser output. That is:
  before atomizing `Rational[A, B]`, we first normalise `A` and `B`, then
  compute the signature of `Rational[A_norm, B_norm]`. Same for `Power`
  and `Complex`. This guarantees that two structurally-different but
  algebraically-equal opaque expressions receive the same signature and
  combine in `Plus`/`Times` as expected.

  Concretely: `Power[Plus[a, b], 1/2]` and `Power[Plus[b, a], 1/2]`
  produce the same atom. `Rational[Plus[2, 3], x]` and `Rational[5, x]`
  produce the same atom. `Power[a, -2]` and `Rational[1, Power[a, 2]]`
  do **not** produce the same atom (the model does not unify these
  presentations, since doing so would require the negative-exponent
  algebra we have explicitly chosen not to support).

Once atomized, the node behaves as any opaque atom: it occupies one
monomial slot, can be multiplied by coefficient values, can multiply with
other monomials, and combines additively with other atoms of the same
signature.

---

## 5b. Component contract: render faithfully, normalize optionally

The normal form above describes the *target* shape, not a contract every component must rely on. Pipeline components (writer, signer, merger, contractor) **must process whatever Scalar they receive** without dropping data, even when the input is not in §2-§5 normal form. The atomisation gap (§5 Decision C1) is the concrete case: an opaque atom's signature is built from the sub-trees as they arrived, so a monomial can carry an opaque `Val` (e.g. a parsed `Rational[Plus[2,3], x]` or `Times[1, x]`) that is not itself in normal form. Downstream components must render that `Val` faithfully rather than re-interpreting or suppressing parts of it.

The writer satisfies this contract structurally: `writeMonomial` only suppresses its `Coeff` factor when `m.Coeff == expr.OneComplex`, and opaque `Val`s flow through `writeExpr` verbatim. **Open gap (will revisit):** the multiplicative-identity collapse for parser-side `Times` children is absent, so a parsed `Times[1, x]` embedded in an opaque sub-tree round-trips through `writeExpr` as `["Times", 1, x]` rather than `x`. Any reintroduction of suppression must restrict it to parsed `*expr.TimesExpr` children only, never a monomial's opaque `Val`s — otherwise the render-faithfully contract breaks.

Normalization is an optimization the calculator performs where it can. When it can't (atomised nodes whose sub-trees aren't canonicalised), the resulting Scalar is non-canonical but still algebraically equivalent to the input. Two algebraically equal Scalars produced by different paths may then have different signatures and serialize to different (but equivalent) JSON — that is the §5 Decision C1 loss-of-uniqueness, not a correctness bug.

---

## 6. Exact coefficient arithmetic (int32 fast path + math/big fallback)

Coefficient arithmetic is **exact at any magnitude**. The representation has
two tiers, and a value's tier is purely a storage detail, never a semantic
bound.

**Policy.** Each `Rational` carries an int32 numerator/denominator pair *and*
an optional `*big.Rat`. Operations run on the int32 fast path (with `int64`
used for safe intermediate computation); when a reduced result does not fit
int32, it **promotes** to `big.Rat`. When a later result fits int32 again, it
**demotes** back. `Complex` is a pair of such `Rational`s, so it promotes
component-wise.

**Canonical invariant.** A `Rational` uses its big field **iff** its value does
not fit int32. This gives every value a single representation regardless of the
arithmetic path that produced it, so:

- `Add`, `Sub`, `Mul`, and `Neg` never fail — there is no overflow signal. Only
  division carries an `ok` flag, and solely for division by zero.
- The representation is canonical at *all* coefficient magnitudes: a value
  computed two different ways (one staying in int32, one passing through big
  and demoting) lands in the same representation, so it serialises identically
  and a coefficient of exactly `1` is recognised as the identity and suppressed.

**Serialisation.** A big integer is emitted as a bare JSON integer of full
precision; a big `Rational` as `["Rational", num, den]` with full-precision
operands; `Complex` as `["Complex", re, im]`. Input integer literals that
exceed int32 are parsed straight into the ring (as `*big.Int` atoms) and fold
like any other integer.

**Non-integers stay opaque.** There is no float coefficient ring: a decimal or
scientific-notation literal (`0.5`, `1e20`) is kept as a `jsontext.Value` atom
and never participates in arithmetic (§7).

---

## 7. Trade-off summary

What this model **does** give you:

- A unique canonical form for every scalar within the coefficient ring
  (exact at any magnitude) and the Laurent-polynomial-with-opaque-atoms
  structure.
- Exact integer and Gaussian-rational arithmetic of unbounded magnitude:
  the int32 fast path promotes to `math/big` and demotes back transparently
  (§6), so no result is ever lost to overflow.
- Free resolution of three classes of arithmetic gap: numeric factor
  ordering, Rational/Complex folding among coefficient values, and
  zero-in-product propagation.
- Byte-canonical equality via `Signer`: two scalars are equal iff their
  signatures match.

What this model **does not** give you, and why each loss is acceptable:

- **No cancellation across multi-monomial denominators.** Identities
  like `(x + y) · 1/(x + y) = 1` and `(x² − y²)/(x − y) = x + y` are
  **not** recognised. `Rational[A, x+y]` is an opaque atom; multiplying
  it by `(x + y)` yields `(x + y) · atom`, not `A`. Justification:
  detecting these requires multivariate polynomial GCD over a
  rational-function field, which is significantly more machinery than
  any failing sample requires, and the input language does not currently
  contain shapes that would exercise these identities meaningfully.
- **No identities on real-valued constants.** `2 · 0.5` does **not**
  fold to `1`. Floats and named real constants (`Pi`, `0.5`, …) live
  outside the coefficient ring and remain opaque atoms. Users who want
  exact arithmetic should write `Rational[1, 2]`, not `0.5`.
- **No simplification of negative-power identities.** `Power[a, -2]`
  and `Rational[1, Power[a, 2]]` are distinct opaque atoms even though
  they are algebraically equal. Justification: same as the
  multi-monomial-denominator case — we have chosen not to operate inside
  a true rational-function field.
- **No `Power[_, 0] → 1` reduction.** `Power[0, 0]` is undefined; the
  model keeps `Power[S, 0]` opaque rather than risk changing semantics
  on the `S = 0` path. See §4.3.

These losses are the price of the Laurent-polynomial-with-atoms
design. They are bounded, predictable, and orthogonal to the kinds of
simplification the contractor's input language was designed to express.
