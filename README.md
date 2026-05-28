# FeynGrav Index Contractor

Go implementation of tensor index contraction for FeynGrav, roughly 100× faster than the native Mathematica version. Takes a Mathematica expression serialized as ExpressionJSON on stdin, contracts all repeated Lorentz indices, groups terms by remaining index structure, and writes the result to stdout.

---

## Background

The input language follows FeynCalc's internal representation. Repeated compatible Lorentz indices on `Pair` objects contract via the Einstein convention: `g^{μν} g_{μν} = 4` in 4D spacetime, or the symbol `D` in `D` dimensions (FeynCalc's dimensional-regularization convention, `D = 4 − 2ε`).

The second argument of `LorentzIndex` or `Momentum` is a **dimension label**, not a derivative marker:

- `LorentzIndex["mu"]` — four-dimensional Lorentz index, contracts to **4**
- `LorentzIndex["mu", "D"]` — `D`-dimensional Lorentz index, contracts to **`D`**
- `Momentum["p"]` / `Momentum["p", "D"]` — four-dimensional / `D`-dimensional momentum

Depending on its arguments, `Pair[…]` represents a metric tensor (two `LorentzIndex`), a Lorentz vector (`LorentzIndex` × `Momentum`), or a scalar product (two `Momentum`). A scalar-product `Pair[Momentum[p], Momentum[q]]` is already a scalar — it is not an index contraction, and the contractor passes it through unchanged.

See [docs/feyncalc-notation.md](docs/feyncalc-notation.md) for the full FeynCalc-grounded interpretation and the worked Lorentz / `D`-dimensional contraction examples.

---

## Pipeline

```
stdin JSON → Parser → chan Expr → Walker → chan Term → Contractor × N → Merger.Add → stdout JSON
```

A `Parser` goroutine streams parsed `expr.Expr` summands into `cParse`. A Walker goroutine normalises each summand into `expr.Term` values whose coefficients are already in Gaussian-rational normal form, sending them on `cTerms`. Each pool worker runs its own `Contractor` with no shared state between workers, so contraction is lock-free. Workers call `merger.Add` directly under a single mutex (the only fan-in point in the pipeline); when all workers exit, `main` calls `merger.Flush` to write output. The merger groups contracted terms by remaining pair structure and accumulates coefficients via one shared `eval.Calculator` (int32 / Rational / Complex folded, zero terms dropped).

---

## I/O Format

ExpressionJSON (Mathematica's JSON serialization). One expression in, one out.

```
Expr:   ["Plus",  expr, ...]       -- sum; can appear anywhere, not only at root
        ["Times", expr, ...]       -- product; args are themselves full Exprs
        ["Power", expr, n]         -- integer n ≥ 1 expanded; n ≤ 0 or symbolic → opaque
        ["Pair",  arg, arg]        -- tensor pair with exactly 2 index/momentum args
        scalar                     -- string symbol, number, or opaque object (passthrough)
Arg:    ["LorentzIndex", "name"]
        ["LorentzIndex", "name", "D"]
        ["Momentum", ...]
```

See [docs/samples.md](docs/samples.md) for the worked examples and *Known implementation flaws* catalog. Inputs and expected outputs are bundled as a machine-readable fixture in [scripts/sample-fixtures.json](scripts/sample-fixtures.json), verified by [scripts/check-fixtures.py](scripts/check-fixtures.py) (run via `bash scripts/run-tests.sh`). The scalar normal form used by the merger (Gaussian-rational coefficients with opaque atoms) is specified in [docs/scalar-normal-form.md](docs/scalar-normal-form.md); user-visible limitations (parsing scope, output-ordering non-determinism, Momentum non-expansion, opaque normalisation, integer overflow) are catalogued in [docs/known-limitations.md](docs/known-limitations.md).

---

## Usage

```bash
make build

# Run with all CPU cores (default)
./bin/contractor < input.json > output.json

# Specify thread count
./bin/contractor -threads 4 < input.json > output.json
```

From Mathematica, drive the contractor via `StartProcess` (not via the shell-pipe `OpenWrite["!…"]` form) so that `stderr` and exit codes remain accessible:

```mathematica
proc = StartProcess[{"/absolute/path/to/bin/contractor"}];
WriteString[ProcessConnection[proc, "StandardInput"],
            ExportString[FeynCalcInternal[expr], "ExpressionJSON", "Compact" -> True] <> "\n"];
Close[ProcessConnection[proc, "StandardInput"]];
result = ImportString[ReadString[ProcessConnection[proc, "StandardOutput"]], "ExpressionJSON"];
```

The full `FeynGravContractor[]` wrapper — with FeynCalcInternal conversion, UTF-8 stdin, exit-code checks, and stderr offset diagnostics — is documented in [docs/feyncalc-notation.md](docs/feyncalc-notation.md) §Integration with Mathematica and exercised by the bundled benchmark notebook [Examples/contractor_benchmark_examples.nb](Examples/contractor_benchmark_examples.nb).

Build requires `GOEXPERIMENT=jsonv2` (handled by the Makefile).

---

## Package Layout

| Package | Purpose |
|---------|---------|
| `cmd/contractor` | Entry point, thread pool, flags |
| `pkg/pipeline/expr` | Value types: `Expr` interface and concrete Expr types (Atom, Pair, LorentzIndex, Momentum, PlusExpr, TimesExpr, PowerExpr, RationalExpr, ComplexExpr) with their `New*` constructors; `Term`; the int32-bounded arithmetic ring (`Rational`, `Complex`, `OneComplex`); the sum-of-monomials normal form (`Scalar`, `Monomial`, `Opaque`, `OneScalar`); converters between Expr trees and arithmetic — see `docs/scalar-normal-form.md` |
| `pkg/pipeline/walk` | Generic `Walker[Coeff, Leaf]` + `Folder[Coeff, Leaf]` cyclic-pull-generator traversal driver |
| `pkg/pipeline/codec` | JSON I/O: synchronous `Parser` (`ParseJson(decoder, emit)`), `Writer` (stateless; `WriteExpr` for any Expr, `WriteTerm` for the Term cascade streamed direct from `expr.Term`), `Signer`; shared panic-based control flow in `errors.go` |
| `pkg/pipeline/eval` | `Calculator` (`Add`/`Mul` on `Scalar`; group/fold/atomize); unexported `scalarFolder` driving the inner Expr→Scalar Walker held by `TermFolder` (parser-side outer Walker stage that emits `expr.Term`) |
| `pkg/pipeline/contract` | Index contraction per worker |
| `pkg/pipeline/merge` | Mutex-protected fan-in: pool workers call `Add` directly; one shared `eval.Calculator` accumulates per-pair-key Scalars; `Flush` writes output via `codec.Writer` |
| `pkg/literal` | Mathematica name constants |
