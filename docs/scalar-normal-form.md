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

- **Numeric atoms.** `int32` integers (e.g. `0`, `1`, and the `4` emitted by contraction).
- **Symbolic / opaque atoms.** Strings (`"D"`, `"kappa"`, …), `jsontext.Value`
  blobs (floats, big-int literals, named constants like `Pi`),
  reassembled `[]any` for unknown head-objects (`FeynAmpDenominator[…]`,
  `PropagatorDenominator[…]`, anything else the parser doesn't recognise),
  and `int64` atoms produced by overflow (see §6).
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
   - exponents `e_i` are non-zero integers (positive in the int32-only world; see §4 for why negative exponents do not occur);
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

- `Integer` — an `int32` atom (in particular, `0` and `1`);
- `Rational[p, q]` — a `Rational` whose numerator `p` and denominator `q`
  are both `int32` atoms, in canonical reduced form:
  - `q > 0`,
  - `gcd(|p|, q) = 1`,
  - `q ≠ 1` (otherwise the rational collapses to the integer `p`),
  - `p ≠ 0` (otherwise the rational collapses to the integer `0`);
- `Complex[a, b]` — a `Complex` whose real part `a` and imaginary part `b`
  are each either `Integer` or `Rational[int32, int32]`, with
  `b ≠ 0` (otherwise the complex collapses to its real part).

This is the **fragment of `ℚ[i]` representable in int32**. It is *not* a
field — it is not closed under addition or multiplication — and the failure
mode is overflow (§6).

The choice to keep coefficients in this restricted set, rather than promoting
to arbitrary-precision integers, is **intentional**. See §7.

---

## 4. Composition rules

Inputs are normalised; outputs must be normalised. The following rules define
each composer's behaviour. Where a rule cannot be carried out (overflow,
non-monomial denominator, non-integer exponent), the offending node is
**atomized** — wrapped as a single opaque atom whose signature is derived from
the normalised sub-trees (§5).

### 4.1 `Plus(S, T)`

Merge the two monomial multisets keyed by monomial signature; for like
monomials, add coefficients in the coefficient ring (§3). Drop any monomial
whose coefficient becomes the integer `0`. Re-sort by monomial signature.

If a coefficient addition overflows the int32-bounded coefficient set
(§6), the corresponding monomial is **atomized**: the offending sum
`c_a · M + c_b · M` becomes a single opaque atom with the canonicalised
form of its un-foldable subexpression, and that atom enters its own
monomial.

### 4.2 `Times(S, T)`

Distribute: for every pair `(c_k M_k, d_l N_l)` produce
`(c_k · d_l) · (M_k · N_l)`. The product of monomials merges atom-power
maps by **adding exponents**; entries whose exponent becomes zero are
dropped (this gives `Power[a, 0] = 1` for free). The product of
coefficients is computed in the coefficient ring; on overflow, atomize (§6).

After distribution, collect like monomials by signature, summing
coefficients (§4.1 rules apply). Any monomial with coefficient `0` is
dropped, which gives **zero-in-product propagation** for free: if either
operand is the zero polynomial, no monomial pairs survive and the product
is zero.

### 4.3 `Power(S, n)` for integer `n`

- `n > 0`: compute by repeated `Times` (§4.2). Closed under the model.
- `n = 0`: **atomized** (§5), *not* reduced to `1`. `Power[0, 0]` is
  undefined and the model can't tell at fold time whether `S = 0`.
- `n < 0`: not produced by the composition rules in the int32-only world.
  Negative-exponent Power is not synthesised internally; if it arrives in
  the input, see §5.

### 4.4 `Rational[A, B]`

If `A` and `B` are both integer coefficients (the only case where the int32
coefficient ring even applies), reduce as in §3. The result is a coefficient
value, lifted to a single monomial `c · 1`.

In all other cases — `A` or `B` containing atoms; `B` a multi-monomial sum;
`B = 0`; reduction overflowing int32 — the entire `Rational[A, B]` is
**atomized** (§5). Decision **B1**.

### 4.5 `Complex[A, B]`

If `A` and `B` are both integer-or-Rational coefficients, the result is a
single coefficient value `a + bi` per §3.

Otherwise, if the imaginary part is itself a *normalised polynomial* `T`,
the result is `A + i · T` computed by `Plus` and `Times`: every monomial of
`T` has its coefficient multiplied by `i` (Gaussian-rational arithmetic at
the coefficient level), and the result is `Plus`-merged with the normalisation of `A`.

If any required arithmetic overflows int32, the offending node is
atomized.

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
  - `Rational[A, B]` whenever it is not a reduced int32-pair coefficient;
  - `Power[S, e]` whenever `e` is not a positive int32 (including negative integers, since `1/(x+y)` is not a Laurent polynomial);
  - `Complex[A, B]` whose normal form is not expressible per §4.5;
  - any node whose evaluation overflows the int32 coefficient ring (§6).

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

The normal form above describes the *target* shape, not a contract every component must rely on. Pipeline components (writer, signer, merger, contractor) **must process whatever Scalar they receive** without dropping data, even when the input is not in §2-§6 normal form. The calculator's overflow recovery (§6) is a concrete case: it can leave a Scalar with two monomials whose factor sets differ only by an `Atom{int32(N)}` opaque that, in normal form, would have folded into the coefficient. Downstream components must render that opaque as a literal factor rather than silently suppressing it.

The writer satisfies this contract structurally: `writeMonomial` only suppresses its `Coeff` factor when `m.Coeff == expr.OneComplex`, and opaque `Atom{int32(1)}` Vals from overflow recovery flow through `writeExpr → writeAtom` and render as literal `1`. The fail-soft policy is honored because the renderer doesn't drop anything. **Open gap (will revisit):** the corresponding multiplicative-identity collapse for parser-side `Atom{int32(1)}` factors is also absent, so `Times[1, x]` round-trips as `["Times", 1, x]` rather than `x`. Any reintroduction of suppression must restrict it to parsed `*expr.TimesExpr` children only, never opaque Vals — otherwise the §6 fail-soft policy breaks.

Normalization is an optimization the calculator performs where it can. When it can't (overflow paths), the resulting Scalar is non-canonical but still algebraically equivalent to the input. Two algebraically equal Scalars produced by different paths may have different signatures and serialize to different (but equivalent) JSON — that is a documented loss-of-uniqueness in §6, not a correctness bug.

---

## 6. Overflow policy (int32-bounded coefficients)

This is the single most important trade-off: coefficient arithmetic stays
int32-bounded rather than promoting to arbitrary precision.

**Policy.** Coefficient arithmetic is performed in `int32` (with `int64`
used internally for safe intermediate computation). When the result of an
addition, multiplication, or rational reduction does not fit in `int32`,
the result is **not** stored as an extended-precision number. Instead the
offending node is atomized: the un-foldable expression is wrapped as a
single opaque atom of `Atom.Value` type `int64` (or, if even `int64`
would overflow, of `jsontext.Value`), and that atom enters a monomial as
an indeterminate.

**Consequences.**

- Folding is **fail-soft**: when the coefficient ring isn't large enough,
  the model gracefully falls back to symbolic representation rather than
  raising an error.
- Once a value escapes to an `int64` (or `jsontext.Value`) atom, it is
  **non-participating** in further coefficient folding. Subsequent
  arithmetic that touches it stays symbolic. This matches the existing
  `int64`-non-participation invariant.
- Strict canonical uniqueness across **algebraically equivalent** inputs
  is **not** guaranteed when overflow is possible. Two algebraically
  equivalent inputs that reach the int32 boundary by different paths can
  produce different normalised outputs (one may stay folded, the other
  may have atomized intermediates). For inputs that stay within int32,
  uniqueness is preserved.
- This bound matches the practical regime of the inputs this code
  processes: Feynman-amplitude coefficients are typically small integers
  (vertex factors, low factorials, low powers of two), and overflow is a
  corner case rather than the common path.

---

## 7. Trade-off summary

What this model **does** give you:

- A unique canonical form for every scalar that stays within the int32
  coefficient ring and the Laurent-polynomial-with-opaque-atoms
  structure.
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
- **No arbitrary-precision integers.** Once a coefficient computation
  exceeds int32, the result becomes opaque. Justification: practical
  inputs do not exercise this regime, and big-int arithmetic adds
  per-operation allocation cost that this codebase has explicit
  preferences against (see CLAUDE.md §6 — "Optimize allocations
  aggressively"). The fail-soft policy means correctness is preserved;
  only simplification opportunity is lost on the overflow path.
- **No simplification of negative-power identities.** `Power[a, -2]`
  and `Rational[1, Power[a, 2]]` are distinct opaque atoms even though
  they are algebraically equal. Justification: same as the
  multi-monomial-denominator case — we have chosen not to operate inside
  a true rational-function field.
- **No `Power[_, 0] → 1` reduction.** `Power[0, 0]` is undefined; the
  model keeps `Power[S, 0]` opaque rather than risk changing semantics
  on the `S = 0` path. See §4.3.

These losses are the price of the int32 / Laurent-polynomial-with-atoms
design. They are bounded, predictable, and orthogonal to the kinds of
simplification the contractor's input language was designed to express.
