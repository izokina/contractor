# FeynGrav Index Contractor

High-performance Go implementation of tensor index contraction for FeynGrav.

The program reads a Mathematica expression serialized as JSON from standard input and writes the result as JSON to standard output.

It splits the expression into terms, performs parallel index contraction on pairs, groups terms by remaining index structure, and collects coefficients (without simplification).

This achieves approximately 100× faster contraction performance than the native implementation.

Additional symbolic operations will be ported to Go in future versions.

Boris Latosh provided the detailed explanation of the contraction procedure and performed extensive testing.
