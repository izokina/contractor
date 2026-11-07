# FeynCalc Notation and Mathematica Integration

> Physics-authoritative reference for the contractor's input language and for calling the contractor from Mathematica. Implementation mechanics live in [architecture.md](architecture.md); this document is about *what the input means* and *how to drive it from Mathematica*.

---

## What is Index Contraction?

In conventional tensor notation, contraction corresponds to summing over a repeated index, usually one upper and one lower. In FeynCalc's internal representation, the corresponding contraction is encoded through repeated compatible Lorentz indices appearing in `Pair` objects.

For example, `g^{μν} g_{μν}` in 4D spacetime contracts to the scalar value 4.

The input carries two kinds of labels:
- **Lorentz indices** (`mu`, `nu`, `rho`, ...): indices labelling components of Lorentz tensors and vectors
- **Momentum labels** (`p`, `q`, `k`, ...): labels for momentum objects, optionally carrying a dimension label

Contraction rules:
- A repeated Lorentz index is contracted according to the usual Einstein convention.
- A metric trace in 4 dimensions gives `4`; a metric trace in `D` dimensions gives `D`, and one in `D-4` dimensions gives `-4 + D`.
- A product of two vectors with the same Lorentz index contracts to the corresponding scalar product.
- A `Pair[Momentum[p], Momentum[q]]` already represents a scalar product; it is not an index contraction.
- A single `Pair[LorentzIndex[mu], Momentum[p]]` represents a vector with a free Lorentz index and is contracted only when another compatible Lorentz index is present.
- A `Pair` is linear in each momentum argument: a momentum that is a linear combination of momentum labels, such as `Momentum[p1 + p2]`, is expanded as the input is read (see [Momentum Sums](#momentum-sums)).

---

## Dimension-Aware Lorentz Contractions

The input follows the internal tensor representation of [FeynCalc](https://github.com/FeynCalc/feyncalc), a Mathematica package for symbolic Feynman-diagram calculations. This section summarises only what the contractor needs; FeynCalc's [reference guide](https://feyncalc.github.io/reference) is authoritative.

FeynCalc distinguishes Lorentz objects living in four dimensions from those living in a symbolic number of dimensions `D`. This matters in dimensional regularization, where loop integrals and tensor expressions are evaluated in $D = 4 - 2 \, \epsilon$. The second argument of `LorentzIndex` or `Momentum` is this dimension label; it is not a marker for derivative indices. A third label, the *evanescent* dimension `D-4`, marks objects that live in the part of `D`-dimensional space outside the four dimensions; FeynCalc's `MTE`, `FVE` and `SPE` carry it, written `["Plus", -4, "D"]` in ExpressionJSON. The contractor reads that `Plus` as a label only where a dimension label stands; anywhere else it is an ordinary sum.

`Pair` represents a metric tensor, a Lorentz vector, or a scalar product, depending on its arguments:

| Object | 4 dimensions | `D` dimensions | `D-4` dimensions |
|---|---|---|---|
| Lorentz index $\mu$ | `LorentzIndex[mu]` | `LorentzIndex[mu, D]` | `LorentzIndex[mu, D-4]` |
| Momentum $p$ | `Momentum[p]` | `Momentum[p, D]` | `Momentum[p, D-4]` |
| Metric tensor $g^{\mu\nu}$ | `Pair[LorentzIndex[mu], LorentzIndex[nu]]` | `Pair[LorentzIndex[mu, D], LorentzIndex[nu, D]]` | `Pair[LorentzIndex[mu, D-4], LorentzIndex[nu, D-4]]` |
| Lorentz vector $p^\mu$ | `Pair[LorentzIndex[mu], Momentum[p]]` | `Pair[LorentzIndex[mu, D], Momentum[p, D]]` | `Pair[LorentzIndex[mu, D-4], Momentum[p, D-4]]` |
| Scalar product $p \cdot q$ | `Pair[Momentum[p], Momentum[q]]` | `Pair[Momentum[p, D], Momentum[q, D]]` | `Pair[Momentum[p, D-4], Momentum[q, D-4]]` |

Contraction preserves the dimension labels:

| Input | Result |
|---|---|
| `Pair[LorentzIndex[mu], LorentzIndex[mu]]` | `4` |
| `Pair[LorentzIndex[mu, D], LorentzIndex[mu, D]]` | `D` |
| `Pair[LorentzIndex[mu, D-4], LorentzIndex[mu, D-4]]` | `-4 + D` |
| `Pair[LorentzIndex[mu], Momentum[p]] Pair[LorentzIndex[mu], Momentum[q]]` | `Pair[Momentum[p], Momentum[q]]` |
| `Pair[LorentzIndex[mu, D], Momentum[p, D]] Pair[LorentzIndex[mu, D], Momentum[q, D]]` | `Pair[Momentum[p, D], Momentum[q, D]]` |

A free index is not contracted: `Pair[LorentzIndex[mu], LorentzIndex[nu]]` stays a metric tensor unless one of its indices is repeated elsewhere in the term.

Both arguments of a `Pair` must carry the same dimension label; a `Pair` that mixes them indicates an error in the source expression and is rejected.

This should not be interpreted as a full implementation of dimensional regularization. The contractor does not evaluate loop integrals, expand expressions around $D = 4 - 2 \, \epsilon$, or manipulate ultraviolet or infrared poles. Its role is narrower: it performs dimension-aware Lorentz contractions in a way compatible with FeynCalc's internal representation.

Implementation details — the streaming pipeline, the per-worker contractor, the merger, the coefficient ring — are documented in [architecture.md](architecture.md). The scalar normal form used by the merger is specified in [scalar-normal-form.md](scalar-normal-form.md). User-visible limitations of the contractor are catalogued in [known-limitations.md](known-limitations.md).

### One index name in two dimensions

The contractor identifies an index by its name alone, so `LorentzIndex[mu]` in one `Pair` and `LorentzIndex[mu, D]` or `LorentzIndex[mu, D-4]` in another contract. Its rule for two different dimensions: `D`-dimensional space is the 4-dimensional part plus the `D-4`-dimensional part, and a contraction keeps what the two sides share, their *meet*:

| Dimensions | Meet |
|---|---|
| equal | the same dimension |
| `D` and 4 | 4 |
| `D` and `D-4` | `D-4` |
| 4 and `D-4` | none: the term is 0 |

The `Pair` a contraction makes carries the meet in both slots, so a momentum can lose its label, and a metric traced across two dimensions gives the trace of their meet:

| Input | Result |
|---|---|
| `Pair[LorentzIndex[mu], Momentum[p]] Pair[LorentzIndex[mu, D], Momentum[q, D]]` | `Pair[Momentum[p], Momentum[q]]` |
| `Pair[LorentzIndex[mu, D-4], LorentzIndex[nu, D-4]] Pair[LorentzIndex[mu, D], Momentum[p, D]]` | `Pair[LorentzIndex[nu, D-4], Momentum[p, D-4]]` |
| `Pair[LorentzIndex[mu], LorentzIndex[nu]] Pair[LorentzIndex[mu, D], LorentzIndex[nu, D]]` | `4` |
| `Pair[LorentzIndex[mu, D], LorentzIndex[nu, D]] Pair[LorentzIndex[mu, D-4], LorentzIndex[nu, D-4]]` | `-4 + D` |
| `Pair[LorentzIndex[mu], Momentum[p]] Pair[LorentzIndex[mu, D-4], Momentum[q, D-4]]` | `0` |

A term that contains one such vanishing contraction is dropped as a whole. [samples.md](samples.md#dimensions) §Dimensions lists the cases.

---

## Momentum Sums

A `Momentum` may hold a sum of momenta, such as `Momentum[p1 + p2, D]`, or its negative, `Momentum[-p1 - p2, D]`, written `["Plus", ["Times", -1, "p1"], ["Times", -1, "p2"]]`. A `Pair` is linear in each argument, and the contractor reads a `Pair` that holds such a sum as the sum of the Pairs of its labels, each with its coefficient, before any index is contracted.

| Input | Result |
|---|---|
| `Pair[LorentzIndex[mu, D], Momentum[p1 + p2, D]]` | `Pair[LorentzIndex[mu, D], Momentum[p1, D]] + Pair[LorentzIndex[mu, D], Momentum[p2, D]]` |
| `Pair[LorentzIndex[mu, D], Momentum[-p1 - p2, D]] Pair[LorentzIndex[mu, D], Momentum[p3, D]]` | `-Pair[Momentum[p1, D], Momentum[p3, D]] - Pair[Momentum[p2, D], Momentum[p3, D]]` |
| `Pair[Momentum[p1 + p2, D], Momentum[p1 - p2, D]]` | `Pair[Momentum[p1, D], Momentum[p1, D]] - Pair[Momentum[p2, D], Momentum[p2, D]]` |

So `Pair[Momentum[p1 + p2], q]` and `Pair[Momentum[p1], q] + Pair[Momentum[p2], q]` give the same result, and a scalar product of two sums of k momenta each becomes k² scalar products, which then merge like any terms (`p1·p2` and `p2·p1` are one scalar product). Only exact numbers are taken out of a momentum as coefficients: a symbolic factor stays inside it, `Momentum[x p]`, and so does a polarization vector, `Momentum[Polarization[p, I]]`. The momenta inside an unknown head, such as the `PropagatorDenominator` of a `FeynAmpDenominator`, are left as written, and so is a `Pair` inside a factor the contractor keeps whole, such as `Power[Pair[Momentum[p1 + p2], q], -1]`. [known-limitations.md](known-limitations.md) §Momentum sources lists the details.

---

## Input/Output Format

Expressions are exchanged in Mathematica's `ExpressionJSON` format: an expression is a JSON array whose first element is its head and whose remaining elements are its arguments. For example, the vector-vector contraction above,
```mathematica
Pair[LorentzIndex[mu], Momentum[p]] Pair[LorentzIndex[mu], Momentum[q]]
```
is sent as
```json
["Times",
  ["Pair", ["LorentzIndex", "mu"], ["Momentum", "p"]],
  ["Pair", ["LorentzIndex", "mu"], ["Momentum", "q"]]]
```
and comes back as
```json
["Pair", ["Momentum", "p"], ["Momentum", "q"]]
```

Structure:

- Root: `["Plus", term1, term2, ...]` for sums, or a single term/object when the expression contains only one term.
- Term: `["Times", scalar1, ..., pair1, pair2, ...]` for products.
- Pair: `["Pair", object1, object2]`.
- Lorentz index: `["LorentzIndex", "mu"]` for a four-dimensional index, `["LorentzIndex", "mu", "D"]` for a `D`-dimensional index, or `["LorentzIndex", "mu", ["Plus", -4, "D"]]` for a `D-4`-dimensional one.
- Momentum: `["Momentum", "p"]` for a four-dimensional momentum, `["Momentum", "p", "D"]` for a `D`-dimensional momentum, or `["Momentum", "p", ["Plus", -4, "D"]]` for a `D-4`-dimensional one.

---

## Integration with Mathematica

The contractor is called from Mathematica by exporting a FeynCalc expression to `ExpressionJSON`, piping it to the executable, and importing the reply. The recommended integration uses `StartProcess` with explicit standard streams rather than a shell pipe: this avoids shell-dependent behaviour and gives access to `stderr` and the exit code, which are essential for diagnosing malformed input or contractor-side errors.

The following helper, used by FeynGrav, drives the contractor:

```mathematica
FeynGravContractor[theInput_] := Module[
	{
	(* Process object returned by StartProcess *)
	proc,
	
	(* Streams connected to the external process *)
	stdin, stdout, stderr,
	
	(* JSON request sent to contractor *)
	jsonIn,
	
	(* JSON reply and diagnostics received from contractor *)
	jsonOut, errOut,
	
	(* Association with process metadata, e.g. exit code *)
	info,
	
	(* Full path to the contractor executable *)
	contractorPath,
	
	(* Parsed byte offset from contractor error message, if available *)
	offset
	},
	
	(* Build the path to the contractor executable.
	FileNameJoin is safer than manual string concatenation. *)
	contractorPath = FileNameJoin[ {ParentDirectory[packageDirectory], "contractor", "bin", "contractor"} ];
	
	(* Convert the Wolfram expression to ExpressionJSON.
	Compact -> True avoids unnecessary whitespace and makes the payload
	smaller and easier for the Go side to process. *)
	jsonIn = ExportString[ FeynCalcInternal[theInput], "ExpressionJSON", "Compact" -> True];
	
	(* Sanity check on the Mathematica side:
	verify that the generated JSON can be imported back before sending it
	to the external program. If this fails, the problem is in the export
	stage rather than in the contractor executable. *)
	Quiet @ Check[
		ImportString[jsonIn, "ExpressionJSON"],
		Print["Mathematica produced invalid ExpressionJSON before sending it."];
		Return[$Failed]
	];
	
	
	(* Start the contractor as an external process.
	The list form {contractorPath} avoids shell interpretation. *)
	proc = StartProcess[{contractorPath}];
	
	(* Obtain the three standard streams of the subprocess. *)
	stdin  = ProcessConnection[proc, "StandardInput"];
	stdout = ProcessConnection[proc, "StandardOutput"];
	stderr = ProcessConnection[proc, "StandardError"];
	
	(* Explicitly use UTF-8 for text written to the process.
	This is important because JSON is expected to be UTF-8 on the Go side. *)
	SetOptions[stdin, CharacterEncoding -> "UTF-8"];
	
	(* Send the JSON request to the contractor.
	We append a newline as an additional framing convenience for the Go side.
	After writing, we MUST close stdin so that the contractor sees EOF and
	knows that the full request has been sent. *)
	WriteString[stdin, jsonIn <> "\n"];
	Close[stdin];
	
	(* Read the complete reply from stdout and any diagnostics from stderr.
	ReadString blocks until the stream is closed or the process exits. *)
	jsonOut = ReadString[stdout];
	errOut  = ReadString[stderr];
	
	(* Collect process metadata, especially the exit code. *)
	info = ProcessInformation[proc];
	
	(* If the contractor returned a nonzero exit code, print diagnostics and
	return $Failed instead of attempting to parse invalid output. *)
	If[Lookup[info, ExitCode, 0] =!= 0,
		Print["contractor stderr: ", errOut];
		Print["process info: ", info];
		
		(* Try to extract the byte offset mentioned in the contractor error
		message. This is useful when debugging malformed JSON payloads. *)
		offset = Quiet @ Check[
			ToExpression @ First @ StringCases[
				errOut,
				RegularExpression["offset\\s+(\\d+)"] -> "$1"
			],
			Missing["NotFound"]
		];
		
		(* If an offset was found, print a short fragment of the input JSON
		around the failing position to simplify debugging. *)
		If[IntegerQ[offset],
			Print["Input near failing offset: "];
			Print @ StringTake[
				jsonIn,
				{
					Max[1, offset - 80],
					Min[StringLength[jsonIn], offset + 80]
				}
			];
		];
		
		
		Return[$Failed];
	];
	
	(* Parse the contractor output back into a Wolfram expression.
	This is the normal successful return value of the function. *)
	ImportString[jsonOut, "ExpressionJSON"]
];
```

Outside FeynGrav, set `contractorPath` to the absolute path of the compiled executable (e.g. `contractorPath = "/absolute/path/to/contractor";`); the rest works unchanged as long as FeynCalc is loaded.

Worked benchmark examples, which exercise the wrapper against well-known FeynGrav results and demonstrate the speedup, are bundled in [Examples/contractor_benchmark_examples.nb](../Examples/contractor_benchmark_examples.nb).
