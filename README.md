# FeynGrav Index Contractor

Go implementation of tensor index contraction for FeynGrav, roughly 100× faster than the native Mathematica version. Takes a Mathematica expression serialized as ExpressionJSON on stdin, contracts all repeated Lorentz indices, groups terms by remaining index structure, and writes the result to stdout.

---

## Background

The input language follows FeynCalc's internal representation. Repeated compatible Lorentz indices on `Pair` objects contract via the Einstein convention: `g^{μν} g_{μν} = 4` in 4D spacetime, or the symbol `D` in `D` dimensions (FeynCalc's dimensional-regularization convention, `D = 4 − 2ε`).

The second argument of `LorentzIndex` or `Momentum` is a **dimension label**, not a derivative marker:

- `LorentzIndex["mu"]` — four-dimensional Lorentz index, contracts to **4**
- `LorentzIndex["mu", "D"]` — `D`-dimensional Lorentz index, contracts to **`D`**
- `LorentzIndex["mu", ["Plus", -4, "D"]]` — evanescent `D-4`-dimensional Lorentz index, contracts to **`-4 + D`**
- `Momentum["p"]` / `Momentum["p", "D"]` / `Momentum["p", ["Plus", -4, "D"]]` — four-dimensional / `D`-dimensional / `D-4`-dimensional momentum

One index name in two dimensions contracts in the dimension both share: `D` with 4 gives 4, `D` with `D-4` gives `D-4`, and 4 with `D-4` gives 0.

Depending on its arguments, `Pair[…]` represents a metric tensor (two `LorentzIndex`), a Lorentz vector (`LorentzIndex` × `Momentum`), or a scalar product (two `Momentum`). A scalar-product `Pair[Momentum[p], Momentum[q]]` is already a scalar — it is not an index contraction, and the contractor passes it through, its two momenta in content order. A momentum that is a sum of labels, such as `Momentum[p1 + p2]`, is expanded into one `Pair` per label.

See [docs/feyncalc-notation.md](docs/feyncalc-notation.md) for the full FeynCalc-grounded interpretation and the worked Lorentz / `D`-dimensional contraction examples.

---

## Pipeline

```
stdin JSON → Parser → batches of summands → Walker → batches of terms → Contractor → Merger → stdout JSON
```

`-threads` workers run every stage, each taking the earliest work available: a turn at the `Parser`, which one worker holds at a time; walking a batch of root summands into `expr.Term` values whose coefficients are already in Gaussian-rational normal form; or contracting a batch of terms. Each worker has its own Walker and Contractors, so walking and contraction are lock-free. Workers add their terms to the merger a batch at a time under a single mutex (the only fan-in point in the pipeline); when all workers exit, `main` calls `merger.Flush` to write output. The merger groups contracted terms by remaining pair structure, accumulates coefficients in an `eval.Sum` (exact integer / Rational / Complex arithmetic, zero terms dropped), and writes them in an order that comes from their content alone.

---

## I/O Format

ExpressionJSON (Mathematica's JSON serialization). One expression in, one out.

```
Expr:   ["Plus",  expr, ...]       -- sum; can appear anywhere, not only at root
        ["Times", expr, ...]       -- product; args are themselves full Exprs
        ["Power", expr, n]         -- int32 n ≥ 1 expanded; any other n → opaque
        ["Pair",  arg, arg]        -- tensor pair with exactly 2 index/momentum args
        ["Rational", p, q]         -- folded into the coefficient when p, q are integers; else p · q^-1
        ["Complex", re, im]        -- likewise, for integer or Rational parts; else re + i·im
        ["Head", expr, ...]        -- any other head: one opaque factor, written back as it came in
        scalar                     -- string symbol or number
Arg:    ["LorentzIndex", "name"]
        ["LorentzIndex", "name", "D"]
        ["LorentzIndex", "name", ["Plus", -4, "D"]]
        ["Momentum", source]       -- also with "D" or ["Plus", -4, "D"] after the source
```

JSON objects, `true`, `false`, `null` and the empty array `[]` are rejected at any depth.

See [docs/samples.md](docs/samples.md) for the worked examples and *Known implementation flaws* catalog. Inputs and expected outputs are bundled as a machine-readable fixture in [scripts/sample-fixtures.json](scripts/sample-fixtures.json), verified by [scripts/check-fixtures.py](scripts/check-fixtures.py) (run via `bash scripts/run-tests.sh`). The scalar normal form used by the merger (Gaussian-rational coefficients with opaque atoms) is specified in [docs/scalar-normal-form.md](docs/scalar-normal-form.md); user-visible limitations (parsing scope, malformed index use, memory and time, large exponents, momentum sources, opaque normalisation, non-integer literals) and the output order are catalogued in [docs/known-limitations.md](docs/known-limitations.md).

---

## Install

Each [release](https://github.com/izokina/contractor/releases) carries binaries for Linux, macOS and Windows, on amd64 and arm64, named `contractor-<os>-<arch>` (`.exe` on Windows). They are built without cgo, so they need nothing else installed.

From the source, with Go 1.27 or later:

```bash
make install                      # the binary to /usr/local/bin, the man page to /usr/local/share/man/man1
make install PREFIX="$HOME/.local"
make install DESTDIR=/tmp/stage   # stage the files for a package
make uninstall                    # takes the same PREFIX and DESTDIR
```

`go install github.com/izokina/contractor/cmd/contractor@latest` installs the binary alone. The man page, [docs/contractor.1](docs/contractor.1), describes the flags, the input and output, and the exit status.

---

## Usage

```bash
make build

# Run with all CPU cores (default)
./bin/contractor < input.json > output.json

# Specify thread count
./bin/contractor -threads 4 < input.json > output.json

# Hold at most about 1 GB of merged terms in memory, the rest in a temporary file
./bin/contractor -merge-memory 1000000000 < input.json > output.json
```

`-merge-memory` bounds the merged terms held in memory to about that many bytes, and moves the rest to a temporary file in `TMPDIR`; the output stays the same. The default, 0, keeps them all in memory.

The input is read from stdin only; an argument that is not a flag is a usage error, exit status 2. Exit status 0 means the output is complete. A rejected input exits 1 with a message on stderr and nothing on stdout; a failure while writing the output exits 1 too, and leaves partial JSON on stdout.

From Mathematica, drive the contractor via `StartProcess` (not via the shell-pipe `OpenWrite["!…"]` form) so that `stderr` and exit codes remain accessible:

```mathematica
proc = StartProcess[{"/absolute/path/to/bin/contractor"}];
WriteString[ProcessConnection[proc, "StandardInput"],
            ExportString[FeynCalcInternal[expr], "ExpressionJSON", "Compact" -> True] <> "\n"];
Close[ProcessConnection[proc, "StandardInput"]];
result = ImportString[ReadString[ProcessConnection[proc, "StandardOutput"]], "ExpressionJSON"];
```

The full `FeynGravContractor[]` wrapper — with FeynCalcInternal conversion, UTF-8 stdin, exit-code checks, and stderr offset diagnostics — is documented in [docs/feyncalc-notation.md](docs/feyncalc-notation.md) §Integration with Mathematica and exercised by the bundled benchmark notebook [Examples/contractor_benchmark_examples.nb](Examples/contractor_benchmark_examples.nb).

---

## Changes in 0.3.0

What a user of 0.2.0 sees differently:

- The output order comes from content alone, at every level, so the output is the same bytes for any thread count and summand order; the pair-free monomials come first, each an addend of the root `Plus`, never a `Plus` nested in it.
- An opaque factor repeated in a monomial is written as a power, `["Power", "x", 3]` rather than `["Times", "x", "x", "x"]`; a repeated Pair stays repeated.
- A momentum that is a linear combination of labels with exact numeric coefficients, such as `Momentum[p1 - p2]`, is expanded into one `Pair` per label.
- The evanescent dimension `D-4`, `["Plus", -4, "D"]`, is accepted, and one index name in two dimensions contracts in the dimension both share; 0.2.0 rejected the first and left the second uncontracted.
- A `Rational` or `Complex` that is not a number is read as the product or sum it stands for: `Rational[x, y]` is `x · Power[y, -1]`.
- A product inside an opaque factor or a momentum source keeps its input order; 0.2.0 reordered its factors.
- Input that is not one well-formed ExpressionJSON expression is rejected: data after the expression, JSON objects, `true`, `false`, `null`, empty arrays, input nested too deep, and a `Rational`, `Complex` or `Power` inside an unknown head with other than two arguments. A `Pair` inside a momentum source is read as a Pair of its own, with the usual checks and its index first; 0.2.0 rejected it or mixed its arguments with the outer Pair's.
- Messages on stderr are reworded, ASCII and without a timestamp; the usage text goes to stderr; an argument that is not a flag is a usage error, exit status 2; and `-merge-memory` is new.

---

## Package Layout

| Package | Purpose |
|---------|---------|
| `cmd/contractor` | Entry point, flags |
| `pkg/pipeline/ident` | Identity: a run-scoped table of IDs for the values the parser reads, and the names, momentum sources and opaque factors they stand for; hashes of ID sequences |
| `pkg/pipeline/intern` | The rules that key an expression from its parts' keys |
| `pkg/pipeline/expr` | Value types: the `Expr` tree, `Term`, and the `Pair` and `Slot` values made of IDs; the exact `Rational`/`Complex` arithmetic; the sum-of-monomials `Scalar` normal form (see `docs/scalar-normal-form.md`) |
| `pkg/pipeline/walk` | `Walker`: the terms of a root summand, pulled one at a time |
| `pkg/pipeline/codec` | JSON I/O: synchronous `Parser` (`ParseJson(decoder, emit)`), which keys what it parses and prepares each summand for the walk; `Writer` (encoder + reusable big-int scratch, single-goroutine; `Add` and `Close` stream the whole output); the Mathematica name constants; shared panic-based control flow in `errors.go` |
| `pkg/pipeline/eval` | `Sum`, which merges monomials and terms with exact coefficients; `Calculator` (`Mul` on `Scalar`); `Folder`, the value of a scalar node as a `Scalar` |
| `pkg/pipeline/contract` | Index contraction per worker |
| `pkg/pipeline/merge` | Mutex-protected fan-in: each worker adds through its own `Input`; an `eval.Sum` per subtree merges the terms; past `-merge-memory`, subtrees move to a temporary file; `Flush` writes output in content order via `codec.Writer` |
| `pkg/pipeline/worker` | `Run`: the `-threads` generic workers that parse, walk and contract |
