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
- A metric trace in 4 dimensions gives `4`; a metric trace in `D` dimensions gives `D`.
- A product of two vectors with the same Lorentz index contracts to the corresponding scalar product.
- A `Pair[Momentum[p], Momentum[q]]` already represents a scalar product; it is not an index contraction.
- A single `Pair[LorentzIndex[mu], Momentum[p]]` represents a vector with a free Lorentz index and is contracted only when another compatible Lorentz index is present.

---

## Dimension-Aware Lorentz Contractions

The input follows the internal tensor representation of [FeynCalc](https://github.com/FeynCalc/feyncalc), a Mathematica package for symbolic Feynman-diagram calculations. This section summarises only what the contractor needs; FeynCalc's [reference guide](https://feyncalc.github.io/reference) is authoritative.

FeynCalc distinguishes Lorentz objects living in four dimensions from those living in a symbolic number of dimensions `D`. This matters in dimensional regularization, where loop integrals and tensor expressions are evaluated in $D = 4 - 2 \, \epsilon$. The second argument of `LorentzIndex` or `Momentum` is this dimension label; it is not a marker for derivative indices.

`Pair` represents a metric tensor, a Lorentz vector, or a scalar product, depending on its arguments:

| Object | 4 dimensions | `D` dimensions |
|---|---|---|
| Lorentz index $\mu$ | `LorentzIndex[mu]` | `LorentzIndex[mu, D]` |
| Momentum $p$ | `Momentum[p]` | `Momentum[p, D]` |
| Metric tensor $g^{\mu\nu}$ | `Pair[LorentzIndex[mu], LorentzIndex[nu]]` | `Pair[LorentzIndex[mu, D], LorentzIndex[nu, D]]` |
| Lorentz vector $p^\mu$ | `Pair[LorentzIndex[mu], Momentum[p]]` | `Pair[LorentzIndex[mu, D], Momentum[p, D]]` |
| Scalar product $p \cdot q$ | `Pair[Momentum[p], Momentum[q]]` | `Pair[Momentum[p, D], Momentum[q, D]]` |

Contraction preserves the dimension labels:

| Input | Result |
|---|---|
| `Pair[LorentzIndex[mu], LorentzIndex[mu]]` | `4` |
| `Pair[LorentzIndex[mu, D], LorentzIndex[mu, D]]` | `D` |
| `Pair[LorentzIndex[mu], Momentum[p]] Pair[LorentzIndex[mu], Momentum[q]]` | `Pair[Momentum[p], Momentum[q]]` |
| `Pair[LorentzIndex[mu, D], Momentum[p, D]] Pair[LorentzIndex[mu, D], Momentum[q, D]]` | `Pair[Momentum[p, D], Momentum[q, D]]` |

A free index is not contracted: `Pair[LorentzIndex[mu], LorentzIndex[nu]]` stays a metric tensor unless one of its indices is repeated elsewhere in the term.

Both arguments of a `Pair` must carry the same dimension label; a `Pair` that mixes them indicates an error in the source expression and is rejected. Across `Pair`s, an index is identified by its name and dimension together, so `LorentzIndex[mu]` and `LorentzIndex[mu, D]` are unrelated and never contract with each other.

This should not be interpreted as a full implementation of dimensional regularization. The contractor does not evaluate loop integrals, expand expressions around $D = 4 - 2 \, \epsilon$, or manipulate ultraviolet or infrared poles. Its role is narrower: it performs dimension-aware Lorentz contractions in a way compatible with FeynCalc's internal representation.

Implementation details — the streaming pipeline, the per-worker contractor, the merger, the coefficient ring — are documented in [architecture.md](architecture.md). The scalar normal form used by the merger is specified in [scalar-normal-form.md](scalar-normal-form.md). User-visible limitations of the contractor are catalogued in [known-limitations.md](known-limitations.md).

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
- Lorentz index: `["LorentzIndex", "mu"]` for a four-dimensional index, or `["LorentzIndex", "mu", "D"]` for a `D`-dimensional index.
- Momentum: `["Momentum", "p"]` for a four-dimensional momentum, or `["Momentum", "p", "D"]` for a `D`-dimensional momentum.

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
