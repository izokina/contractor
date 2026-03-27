# FeynGrav Index Contractor

High-performance Go implementation of tensor index contraction for FeynGrav.

The program reads a Mathematica expression serialized as JSON from standard input and writes the result as JSON to standard output.

It splits the expression into terms, performs parallel index contraction on pairs, groups terms by remaining index structure, and collects coefficients (without simplification).

This achieves approximately 100× faster contraction performance than the native implementation.

Additional symbolic operations will be ported to Go in future versions.

For a more detailed documentation, including architecture diagrams, data structures, and the processing pipeline, please refer to [AI-DOCS.md](AI-DOCS.md). This content has been human-reviewed and provides a fuller and more descriptive overview of the system.

Boris Latosh provided the detailed explanation of the contraction procedure and performed extensive testing.
