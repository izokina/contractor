# FeynCalc Notation and Mathematica Integration

> Physics-authoritative reference for the contractor's input language and for calling the contractor from Mathematica. Implementation mechanics live in [architecture.md](architecture.md); this document is about *what the input means* and *how to drive it from Mathematica*.

---

## What is Index Contraction?

In conventional tensor notation, contraction corresponds to summing over a repeated index, usually one upper and one lower. In FeynCalc's internal representation, the corresponding contraction is encoded through repeated compatible Lorentz indices appearing in `Pair` objects.

For example, `g^{μν} g_{μν}` in 4D spacetime contracts to the scalar value 4.

In FeynGrav calculations, indices represent:
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

This project is designed to process expressions that follow the internal tensor representation used by FeynCalc. FeynCalc is a Wolfram Mathematica package for symbolic evaluation of Feynman diagrams and algebraic calculations in quantum field theory and elementary particle physics. Its source code is available in the FeynCalc GitHub repository, and its online reference guide gives the authoritative definitions of the objects discussed below:

* FeynCalc GitHub repository: `https://github.com/FeynCalc/feyncalc`
* FeynCalc online reference guide: `https://feyncalc.github.io/reference`

The discussion below only summarizes the parts of FeynCalc that are relevant for the contractor. For a complete description of FeynCalc syntax, internal representations, and available algebraic operations, the user should consult the official reference guide.

FeynCalc distinguishes Lorentz objects living in four dimensions from Lorentz objects living in an arbitrary symbolic number of dimensions. The arbitrary dimension is usually denoted by `D`. This distinction is essential in calculations that use dimensional regularization, where loop integrals and tensor expressions are often evaluated in $D = 4 - 2 \, \epsilon$ rather than strictly in four dimensions.

In FeynCalc, the second argument of `LorentzIndex` or `Momentum` is a dimension label. It is not a marker for derivative indices.

For example,
```mathematica
LorentzIndex[mu]
```
denotes a four-dimensional Lorentz index, while
```mathematica
LorentzIndex[mu, D]
```
denotes a Lorentz index in `D` dimensions.

Similarly,
```mathematica
Momentum[p]
```
denotes a four-dimensional momentum, while
```mathematica
Momentum[p, D]
```
denotes a momentum in `D` dimensions.

FeynCalc represents metric tensors, Lorentz vectors, and scalar products internally using `Pair`. The meaning of `Pair[x, y]` depends on whether its arguments are Lorentz indices or momenta.

A four-dimensional metric tensor is represented as
```mathematica
Pair[LorentzIndex[mu], LorentzIndex[nu]]
```
whereas a `D`-dimensional metric tensor is represented as
```mathematica
Pair[LorentzIndex[mu, D], LorentzIndex[nu, D]]
```
A four-dimensional Lorentz vector $p^\mu$ is represented as
```mathematica
Pair[LorentzIndex[mu], Momentum[p]]
```
whereas a `D`-dimensional Lorentz vector $p^\mu$ is represented as
```mathematica
Pair[LorentzIndex[mu, D], Momentum[p, D]]
```

Scalar products are represented by pairing two momenta. For example,
```mathematica
Pair[Momentum[p], Momentum[q]]
```
represents the four-dimensional scalar product of `p` and `q`, while
```mathematica
Pair[Momentum[p, D], Momentum[q, D]]
```
represents the corresponding `D`-dimensional scalar product.

The contractor must therefore preserve the dimension labels attached to Lorentz indices and momenta. In particular,
```mathematica
Pair[LorentzIndex[mu], LorentzIndex[mu]]
```
contracts to `4` while
```mathematica
Pair[LorentzIndex[mu, D], LorentzIndex[mu, D]]
```
contracts to `D`.

Likewise, repeated Lorentz indices in vector expressions are contracted according to their dimension. For example,
```mathematica
Pair[LorentzIndex[mu], Momentum[p]] Pair[LorentzIndex[mu], Momentum[q]]
```
contracts to the four-dimensional scalar product
```mathematica
Pair[Momentum[p], Momentum[q]]
```
whereas
```mathematica
Pair[LorentzIndex[mu, D], Momentum[p, D]] Pair[LorentzIndex[mu, D], Momentum[q, D]]
```
contracts to the `D`-dimensional scalar product
```mathematica
Pair[Momentum[p, D], Momentum[q, D]]
```

A free Lorentz index should not be contracted. For instance,
```mathematica
Pair[LorentzIndex[mu], LorentzIndex[nu]]
```
represents a metric tensor with two free Lorentz indices. It does not contract to `4` unless the two indices are identified, or unless it is multiplied by another tensor expression that supplies the corresponding repeated indices.

This should not be interpreted as a full implementation of dimensional regularization. The contractor does not evaluate loop integrals, expand expressions around $D = 4 - 2 \, \epsilon$, or manipulate ultraviolet or infrared poles. Its role is narrower: it performs dimension-aware Lorentz contractions in a way compatible with FeynCalc's internal representation.

Implementation details — the streaming pipeline, the per-worker contractor, the merger, the coefficient ring — are documented in [architecture.md](architecture.md). The scalar normal form used by the merger is specified in [scalar-normal-form.md](scalar-normal-form.md). User-visible limitations of the contractor are catalogued in [known-limitations.md](known-limitations.md).

---

## Input/Output Format

The system uses Mathematica's `ExpressionJSON` format to exchange expressions with external programs. In this format, a Mathematica expression is represented as a JSON array whose first element is the head of the expression and whose remaining elements are its arguments.

For example, the FeynCalc expression
```mathematica
Pair[LorentzIndex[mu], LorentzIndex[mu]]
```
is represented schematically as
```json
["Pair", ["LorentzIndex", "mu"], ["LorentzIndex", "mu"]]
```
This expression is a four-dimensional metric trace and therefore contracts to `4`.

A vector-vector contraction provides a less trivial example. The expression
```mathematica
Pair[LorentzIndex[mu], Momentum[p]] Pair[LorentzIndex[mu], Momentum[q]]
```
is represented schematically as
```json
[
  "Times",
  ["Pair", ["LorentzIndex", "mu"], ["Momentum", "p"]],
  ["Pair", ["LorentzIndex", "mu"], ["Momentum", "q"]]
]
```
After contraction, the repeated Lorentz index is removed and the result is the scalar product
```json
["Pair", ["Momentum", "p"], ["Momentum", "q"]]
```

By contrast, the expression
```mathematica
Pair[LorentzIndex[mu], LorentzIndex[nu]]
```
is represented as
```json
["Pair", ["LorentzIndex", "mu"], ["LorentzIndex", "nu"]]
```
and should not contract to `4`, because it is a metric tensor with two free Lorentz indices.

Structure:

- Root: `["Plus", term1, term2, ...]` for sums, or a single term/object when the expression contains only one term.
- Term: `["Times", scalar1, ..., pair1, pair2, ...]` for products.
- Pair: `["Pair", object1, object2]`.
- Lorentz index: `["LorentzIndex", "mu"]` for a four-dimensional index, or `["LorentzIndex", "mu", "D"]` for a `D`-dimensional index.
- Momentum: `["Momentum", "p"]` for a four-dimensional momentum, or `["Momentum", "p", "D"]` for a `D`-dimensional momentum.


---

## Integration with Mathematica

The contractor is intended to be called from Mathematica by sending a FeynCalc expression to the external executable in Mathematica's `ExpressionJSON` format and then importing the returned JSON expression back into Mathematica.

The recommended integration uses `StartProcess`, explicit standard streams, and `ExpressionJSON`. This avoids shell-dependent behavior and gives access to `stderr` and the process exit code, which is essential for diagnosing malformed input or contractor-side errors.

The following helper drives the contractor from Mathematica. It can also be used as a standalone Mathematica wrapper, because it does not rely on any special FeynGrav functionality apart from the path variable `packageDirectory` and the availability of FeynCalc's `FeynCalcInternal` conversion.

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

The wrapper performs the following steps:

1. Converts the input expression to FeynCalc's internal representation with `FeynCalcInternal`.
2. Exports the internal expression to compact `ExpressionJSON`.
3. Checks that Mathematica can import the generated JSON before sending it to the contractor.
4. Starts the contractor executable using `StartProcess` without invoking a shell.
5. Writes the JSON request to the contractor's standard input using UTF-8 encoding.
6. Closes standard input so that the contractor receives EOF and starts processing the complete request.
7. Reads the complete standard output and standard error streams.
8. Checks the process exit code and prints diagnostics if the contractor fails.
9. Imports the contractor output back from `ExpressionJSON` into a Wolfram expression.

To use this wrapper outside FeynGrav, replace the definition of `contractorPath` with the absolute path to the compiled contractor executable. For example:

```mathematica
contractorPath = "/absolute/path/to/contractor";
```

The rest of the function can be used unchanged, provided that FeynCalc is loaded and the contractor executable accepts and returns expressions in the expected `ExpressionJSON` format.

---
Worked benchmark examples (which exercise the wrapper against well-known FeynGrav results and demonstrate the speedup) are bundled in [Examples/contractor_benchmark_examples.nb](../Examples/contractor_benchmark_examples.nb).
