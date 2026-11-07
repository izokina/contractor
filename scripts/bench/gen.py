#!/usr/bin/env python3
"""Write one benchmark input to stdout as ExpressionJSON.

Usage:
  scripts/bench/gen.py CASE [SIZE]

Each case reproduces a known performance cliff of the contractor; run without
arguments to list them. SIZE scales a case, and its default runs in seconds.
The cases times-scalar / times-scalar-nf, root-bare / root-prefactor and
coeff-small / coeff-big are pairs: the members differ in one aspect and are
meant to be compared with each other.
"""

import json
import random
import sys
from fractions import Fraction
from itertools import combinations, product
from math import factorial, log, log2

KAPPA = "κ"
INV_D_MINUS_2 = ["Power", ["Plus", -2, "D"], -1]


def li(name):
    return ["LorentzIndex", name, "D"]


def mom(source):
    return ["Momentum", source, "D"]


def pair(a, b):
    return ["Pair", a, b]


def times(*xs):
    return ["Times", *xs]


def plus(*xs):
    return ["Plus", *xs]


def number(f):
    return f.numerator if f.denominator == 1 else ["Rational", f.numerator, f.denominator]


def imag(f):
    return ["Complex", 0, number(f)]


def merge_one_key(n):
    return plus(*(times(f"a{i}", pair(li("mu"), li("nu"))) for i in range(n)))


SCALAR_EXP = 10


def times_scalar(n, normalised=False):
    if normalised:
        monomials = []
        for i in range(SCALAR_EXP, -1, -1):
            for j in range(SCALAR_EXP - i, -1, -1):
                k = SCALAR_EXP - i - j
                c = factorial(SCALAR_EXP) // (factorial(i) * factorial(j) * factorial(k))
                factors = ["x"] * i + ["y"] * j + ["z"] * k
                monomials.append(times(*factors) if c == 1 else times(c, *factors))
        scalar = plus(*monomials)
    else:
        scalar = ["Power", plus("x", "y", "z"), SCALAR_EXP]
    vectors = plus(*(times(i + 1, pair(li("mu"), mom(f"p{i}"))) for i in range(n)))
    return times(scalar, vectors)


def power_sum(e):
    return ["Power", plus("a", "b"), e]


def momentum_chain(n):
    core = '["Pair",["LorentzIndex","a","D"],["LorentzIndex","b","D"]]'
    tails = "".join(f',"D"],["LorentzIndex","mu{i}","D"]]' for i in range(n))
    return '["Pair",["Momentum",' * n + core + tails


def chain_link(a, b):
    """K[a,b] = g[a,b] + p[a] p[b] + 2 q[a] q[b], vectors written Momentum first."""
    return plus(pair(li(a), li(b)), times(pair(mom("p"), li(a)), pair(mom("p"), li(b))),
                times(2, pair(mom("q"), li(a)), pair(mom("q"), li(b))))


def chain(n):
    """The closed chain K[i1,i2] K[i2,i3] ... K[in,i1], a product of n sums;
    for n = 2 as Mathematica writes it, Power[K[i1,i2], 2]."""
    if n == 2:
        return ["Power", chain_link("i1", "i2"), 2]
    return times(*(chain_link(f"i{k}", f"i{k % n + 1}") for k in range(1, n + 1)))


def tensor_terms(n):
    rng = random.Random(1)
    free = ["m1", "n1", "m2", "n2", "m3", "n3"]
    coeffs = [Fraction(1, 2), Fraction(-1, 4), Fraction(1), Fraction(-1), Fraction(2), Fraction(1, 8)]
    terms = []
    for _ in range(n):
        a, b = rng.sample(free, 2)
        r = rng.choice(["a1", "b1"])
        p, q = rng.choice(["p1", "p2", "p3"]), rng.choice(["p1", "p2", "p3"])
        terms.append(times(number(rng.choice(coeffs)), INV_D_MINUS_2, KAPPA,
                           pair(li(a), li(r)), pair(li(r), mom(p)), pair(li(b), mom(q))))
    return terms


def root_bare(n):
    return plus(*tensor_terms(n))


def root_prefactor(n):
    return times(imag(Fraction(1)), KAPPA, plus(*tensor_terms(n)))


def matchings(idx):
    if not idx:
        return [()]
    first, rest = idx[0], idx[1:]
    return [(first, m) + tail
            for m in rest
            for tail in matchings([i for i in rest if i != m])]


VERTEX_COEFFS = [Fraction(1, 4), Fraction(-1, 4), Fraction(1, 2), Fraction(-1, 2), Fraction(1, 8)]


def vertex(rng, n, idx, moms):
    """A three-graviton-like vertex: n terms over six indices (three symmetric
    pairs) and three momenta, symmetrised within each index pair like the real
    one, so that contracting a pair merges terms."""
    bases = []
    for x, y in combinations(idx, 2):
        rest = [i for i in idx if i not in (x, y)]
        for (k1, k2), m in product(product(moms, repeat=2), matchings(rest)):
            bases.append([("g", m[0], m[1]), ("g", m[2], m[3]), ("v", x, k1), ("v", y, k2)])
    for m, (k1, k2) in product(matchings(idx), combinations(moms, 2)):
        bases.append([("g", m[0], m[1]), ("g", m[2], m[3]), ("g", m[4], m[5]), ("s", k1, k2)])
    rng.shuffle(bases)
    seen, terms = set(), []
    for base in bases:
        c = rng.choice(VERTEX_COEFFS)
        for flips in product([False, True], repeat=3):
            swap = {}
            for k, flip in enumerate(flips):
                x, y = idx[2 * k], idx[2 * k + 1]
                swap[x], swap[y] = (y, x) if flip else (x, y)
            image = [("g", *sorted((swap[a], swap[b]))) if t == "g" else
                     ("v", swap[a], b) if t == "v" else (t, a, b)
                     for t, a, b in base]
            key = tuple(sorted(json.dumps(f) for f in image))
            if key not in seen:
                seen.add(key)
                terms.append((c, image))
    render = {"g": lambda a, b: pair(li(a), li(b)), "v": lambda a, k: pair(li(a), mom(k)),
              "s": lambda k1, k2: pair(mom(k1), mom(k2))}
    return plus(*(times(imag(c), KAPPA, *(render[t](a, b) for t, a, b in image))
                  for c, image in (terms[i % len(terms)] for i in range(n))))


V1_MOMENTA = ["p1", "p2", plus(times(-1, "p1"), times(-1, "p2"))]
V2_MOMENTA = ["p3", "p4", plus("p1", "p2")]


def factored(n):
    """Two vertices of n terms each joined by a three-term propagator, so
    n = 231 expands into 160,083 terms."""
    rng = random.Random(1)
    v1 = vertex(rng, n, ["a1", "b1", "m1", "n1", "m2", "n2"], V1_MOMENTA)
    v2 = vertex(rng, n, ["a2", "b2", "m3", "n3", "m4", "n4"], V2_MOMENTA)
    propagator = plus(
        times(plus(-2, "D"), pair(li("a1"), li("b2")), pair(li("a2"), li("b1"))),
        times(-2, pair(li("a1"), li("b1")), pair(li("a2"), li("b2"))),
        times(plus(-2, "D"), pair(li("a1"), li("a2")), pair(li("b1"), li("b2"))),
    )
    denominator = ["FeynAmpDenominator", ["PropagatorDenominator", plus(mom("p1"), mom("p2")), 0]]
    return times(imag(Fraction(1, 2)), INV_D_MINUS_2, denominator, propagator, v1, v2)


def factored_distinct(n):
    """factored's vertices sharing one index instead of a propagator: distinct
    pairs of vertex terms contract to distinct structures, so nothing merges."""
    rng = random.Random(1)
    v1 = vertex(rng, n, ["a1", "b1", "m1", "n1", "m2", "n2"], V1_MOMENTA)
    v2 = vertex(rng, n, ["a1", "b2", "m3", "n3", "m4", "n4"], V2_MOMENTA)
    return times(imag(Fraction(1, 2)), KAPPA, v1, v2)


WIDE = (Fraction(10**30 + 1, 10**30 - 1), Fraction(10**25 + 3, 10**25 + 9))


def coeff_terms(n, scale):
    rng = random.Random(1)
    reals = [Fraction(1, 2), Fraction(-1, 4), Fraction(3, 8), Fraction(-5, 16), Fraction(1)]
    imags = [Fraction(1, 2), Fraction(-1, 4), Fraction(1, 8)]
    terms = []
    for _ in range(n):
        r = rng.choice(["a1", "b1"])
        p, q = rng.choice(["p1", "p2", "p3", "p4"]), rng.choice(["p1", "p2", "p3", "p4"])
        terms.append(times(number(rng.choice(reals) * scale[0]), imag(rng.choice(imags) * scale[1]),
                           INV_D_MINUS_2, KAPPA,
                           pair(li(rng.choice(["m1", "m2"])), li(r)), pair(li(r), mom(p)),
                           pair(li(rng.choice(["n1", "n2"])), mom(q))))
    return plus(*terms)


def coeff_small(n):
    return coeff_terms(n, (Fraction(1), Fraction(1)))


def coeff_big(n):
    return coeff_terms(n, WIDE)


# name: (generator, default SIZE, input shape and the cost it exposes)
CASES = {
    "merge-one-key": (merge_one_key, 2000, "SIZE terms sharing one tensor structure: merging one group"),
    "times-scalar": (times_scalar, 40, "Power[x+y+z, 10] times SIZE vectors: re-folding scalar factors"),
    "times-scalar-nf": (lambda n: times_scalar(n, True), 40, "times-scalar with the scalar pre-expanded"),
    "power-sum": (power_sum, 20, "Power[a+b, SIZE]: expanding a power of a sum"),
    "chain": (chain, 10, "a closed chain of SIZE three-term tensors: 3^SIZE terms merge into a small polynomial"),
    "momentum-chain": (momentum_chain, 2000, "Momentum sources nested SIZE deep (at most 4997): keying"),
    "root-bare": (root_bare, 40000, "a root Plus of SIZE tensor terms: the memory baseline"),
    "root-prefactor": (root_prefactor, 40000, "root-bare's sum under Times[I, kappa, ...]: a non-bare root"),
    "factored": (factored, 160, "vertex x propagator x vertex, SIZE terms per vertex: a product that merges"),
    "factored-distinct": (factored_distinct, 160, "two vertices sharing one index: a product that does not merge"),
    "coeff-small": (coeff_small, 50000, "SIZE terms with small rational coefficients: the int32 path"),
    "coeff-big": (coeff_big, 50000, "coeff-small with ~30-digit coefficients: the math/big path"),
}


def scaled_size(name, scale):
    """SIZE for --scale: the SIZE of power-sum and chain is an exponent, so scale
    multiplies 2^SIZE and 3^SIZE."""
    size = CASES[name][1]
    if name == "power-sum":
        return size + round(log2(scale))
    if name == "chain":
        return size + round(log(scale, 3))
    return max(1, round(size * scale))


def main():
    if len(sys.argv) not in (2, 3) or sys.argv[1] not in CASES:
        print(__doc__.strip() + "\n\nCases:", file=sys.stderr)
        for name, (_, size, meaning) in CASES.items():
            print(f"  {name:17} SIZE={size:<6} {meaning}", file=sys.stderr)
        sys.exit(2)
    fn, size, _ = CASES[sys.argv[1]]
    tree = fn(int(sys.argv[2]) if len(sys.argv) == 3 else size)
    out = tree if isinstance(tree, str) else json.dumps(tree, ensure_ascii=False, separators=(",", ":"))
    sys.stdout.write(out)


if __name__ == "__main__":
    main()
