# Performance Report — 422 MB ExpressionJSON Workload

*Captured 2026-05-27 against a 422 MB ExpressionJSON workload (stdin) producing 136 MB on stdout.*

Workload: stdin 422 MB → stdout 136 MB.
Hardware: 8 logical CPUs.
Build: `make build`, jsonv2 experimental.
Profiling was driven by `runtime/pprof` only. Alongside the pre-existing `-cpuprofile`/`-memprofile`, two profiling flags ship in `cmd/contractor/main.go` (`-blockprofile`, `-mutexprofile`); three more — `-memstats` (a final memstats line reporting VmHWM from `/proc/self/status`), `-heapsnap`, and `-heapsnap-ms` (periodic heap snapshots) — were temporary instrumentation added only for this capture and removed before commit.

## Headline numbers (baseline, GOGC=100, 8 threads)

| Metric | Value |
|---|---|
| Wall time | **10.55 s** |
| User CPU | 27.6 s (265%) |
| Peak RSS (VmHWM) | **205 MB** |
| HeapInuse at flush | 168 MB |
| HeapAlloc cumulative | **5.87 GB** |
| Alloc objects | **92 M** |
| GC cycles | 269 (≈25 ms apart) |
| GC STW total | 19 ms |

The numbers below come from cpu/mem/block/mutex profiles plus periodic heap snapshots and a gctrace log.

## What is hurting speed

### 1. The pipeline is single-core-bound; the 8-worker pool is dead weight.

Block profile (cumulative blocking time, 87% of all delay):

```
84.70s  runtime.chanrecv2    ← workers blocked on `for term := range cTerms`
10.59s  sync.WaitGroup.Wait
 1.44s  runtime.chansend1
```

Mutex profile (cumulative): **154 ms total**, of which the merger mutex is 139 ms — i.e. the merger mutex is **not** the bottleneck. The workers are sitting idle waiting for the walker to produce.

Empirical confirmation (varying `-threads`):

| threads | wall | user CPU |
|---|---|---|
| 1 | 10.3 s | 28.4 s |
| 2 | 10.5 s | 27.6 s |
| 4 | 10.6 s | 27.8 s |
| 8 | 11.6 s | 29.4 s |
| 16 | 11.5 s | 29.5 s |

Adding workers is at best neutral; at 8+ it actively hurts (more scheduler / GC stealing). The reasons are structural:

- The parser (`pkg/pipeline/codec/parser.go`) is one goroutine reading sequentially from stdin.
- The walker (`pkg/pipeline/walk/walker.go`) is one goroutine fanning expanded `Term`s out on an **unbuffered** `cTerms` channel.
- `Merger.Add` is serial under a mutex — but it's so cheap (139 ms of contention) that it isn't where the time goes either.

So the critical path is **parser → walker → contractor**, and they share at most one core's worth of useful work each. Net: real work is ≤ 1 core; the rest is GC (see below).

### 2. GC drain dominates CPU samples.

CPU profile, by cumulative samples:

```
15.50s 56.92%  runtime.gcBgMarkWorker  ← 57% of all CPU is in GC mark
14.74s 54.13%  runtime.scanSpan
 8.65s 31.77%  runtime.tryDeferToSpanScan
 7.87s 28.90%  runtime.scanObjectsSmall
```

`GODEBUG=gctrace=1` shows a GC every ~40 ms, with assist time dominating wall‑time GC. STW is negligible (19 ms total). The cost is **concurrent mark CPU stolen from worker cores** — which is fatal here because the pipeline is single-core-bound on the user-code side and any cores left for the application are exactly the cores the GC scanners are saturating.

GOGC sensitivity, same input:

| GOGC | wall | user CPU | RSS peak | GCs |
|---|---|---|---|---|
| 100 (default) | 10.6 s | 27.6 s | 205 MB | 269 |
| 200 | 10.3 s | 19.0 s | 308 MB | 116 |
| 400 | **9.6 s** | 14.6 s | 501 MB | 54 |
| 800 | 9.9 s | 13.1 s | 817 MB | 26 |
| off | 11.5 s | 10.7 s | **6.3 GB** | 0 |

Reading: the pipeline's compute floor is ~10 s of user CPU (single-core bound). At GOGC=400 we already saturate that with only modest extra heap — wall time stops improving because cache locality starts losing what GC stops costing. **Without alloc reduction, no amount of parallelism or tuning is going to break below ~9 s on this hardware.** With alloc reduction, both the GC tax disappears *and* the per-term latency drops, which is the actual speed win.

### 3. The CPU spent outside GC is concentrated in three places.

Cumulative CPU, excluding the runtime nodes above:

| Stage | CPU (cum) | % of wall-CPU |
|---|---|---|
| Parser (`parseNamedExpr` → `collectSources` / `parsePair` / …) | 5.5 s | 20% |
| Walker / `walkLeaf` / `mulStreams` closures | 2.8 s | 10% |
| Encoder writes (`jsontext.encoderState.WriteToken`) — both Signer & Writer share | 1.55 s | 5.7% |
| `Writer.writeExpr` / `writeAtom` | 1.39 s | 5.1% |
| `Signer.LorentzIndex` | 1.12 s | 4.1% |
| `Contractor.ContractAndNormalize` (incl. addPair, Mul) | 1.09 s | 4.0% |
| `Signer.Expr` | 0.76 s | 2.8% |

The parser is the single biggest user-CPU consumer, which lines up with the alloc story: 51% of all allocations originate in `parseNamedExpr → collectSources/parsePair → …`.

## What is hurting peak memory

`HeapInuse` grew **monotonically** from 0 to 168 MB across the run (per gctrace and per the 27 periodic heap snapshots). The peak is at the end of the pipeline, just before `Merger.Flush`. Sources of retained live heap at peak:

```
20.5 MB  bytes.(*Buffer).String   ← signature strings via Signer.*
18.0 MB  expr.NewAtom             ← Atoms retained as Opaque.Val and PowerExpr.Child
 9.0 MB  contract.Contractor.addPair  ← Pair structures stored in Merger.terms
 8.0 MB  eval.Calculator.foldBuf      ← Monomial[] / Opaque[] held in Merger.terms
 7.0 MB  jsontext.Token.String        ← parsed strings still referenced
 6.0 MB  parsePartialObject (passthrough []any)
 5.5 MB  bytes.Clone (decoder readValue path)
 4.5 MB  Contractor.ContractAndNormalize (pairs slice on Term)
 3.2 MB  merge.Merger.Add
```

The merger's `terms map[string]termSet` is the dominant retainer. Each retained entry holds:

- the key — the canonical pair-set signature produced by `Signer.Pairs` (one string per unique key);
- `Pair.Lorentz []LorentzIndex` and `Pair.Momentum []Momentum`, each carrying their own canonical signature strings;
- `Coeff expr.Scalar` — a slice of `Monomial`s, each with its own coefficient and an `Opaques []Opaque` slice, each carrying *another* signature string.

So each retained term is paying signature-string cost three times over (pair-set / per-Pair / per-Opaque), and those strings are the largest single class of retained bytes. The RSS overhead on top of `HeapInuse` is the usual GC slack: 205 MB RSS vs 168 MB inuse = ~37 MB slack with GOGC=100. GOGC=400 inflates that to 501 MB; GOGC=200 keeps RSS to 308 MB and is still a wall-time win.

## Allocation hotspots (the real lever)

Cumulative bytes allocated, top callers (`go tool pprof -alloc_space -top mem.prof`):

| Site | Cum. alloc | % of total | Comment |
|---|---|---|---|
| **`jsontext.NewEncoder` (called from Signer.* sites)** | **2.32 GB** | **38.9%** | One encoder allocated *per signature call*; this is the dominant single allocator |
| `strings.(*Builder).WriteString` (only caller: `eval.Calculator.signature`) | 538 MB | 9.0% | Opaque-list signature concatenation; builds a fresh string every fold |
| `walk.Walker.walkLeaf` closures (Scalar/Pair shape) | 363 MB | 6.1% | One closure per leaf per Walk |
| `eval.Calculator.foldBuf` | 347 MB | 5.8% | Per-monomial `[]Opaque` copy + map entry into `groups` |
| `parsePair` (slice/struct backing) | 342 MB | 5.7% | Each Pair: fresh `Lorentz`/`Momentum` slices |
| `bytes.(*Buffer).String` (Signer return path) | 305 MB | 5.1% | New string for each signature; can't avoid the alloc but it lives forever via the merger |
| `contract.Contractor.addPair` | 231 MB | 3.9% | Allocates a fresh `Pair` per contraction round |
| `walk.Walker.walkLeaf` closures (Complex/Opaque shape) | 156 MB | 2.6% | Same as above for the inner scalar Walker |
| `Calculator.materialize` | 146 MB | 2.4% | Builds the output `[]Monomial` slice from the groups map |
| `jsontext.Token.String` (called from parser) | 137 MB | 2.3% | `.String()` on every parsed string token |
| `expr.NewAtom` | 136 MB flat / 197 cum | 3.3% | Atom struct + its signature (json.Marshal for `[]any`, 44 MB) |
| `TermFolder.Emit` | 124 MB | 2.1% | Per-term `Pairs` copy |

By **object count** (90 M total objects in 10 s = ~9 M/s):

```
11.15M  walkLeaf closures (outer walker)   12.1%
 8.75M  jsontext.Token.String              9.5%
 6.91M  jsontext.NewEncoder                7.5%
 6.35M  bytes.Buffer.String                6.9%
 6.29M  parsePair allocations              6.8%
 6.13M  jsontext stateMachine.pushArray    6.7%
 5.85M  strings.Builder.WriteString        6.4%
 5.45M  Calculator.foldBuf                 5.9%
 4.96M  walkLeaf closures (inner walker)   5.4%
 4.46M  NewAtom                            4.8%
```

### Drill-down: where Signer's allocations go

`Signer` accounts for **~2.86 GB cumulative = ~48% of all bytes allocated**:

| Caller | Cum alloc | NewEncoder share |
|---|---|---|
| `parseLorentzIndex` → `Signer.LorentzIndex` | 1.64 GB | 1.46 GB (89%) |
| `scalarFolder.Fold` → `Signer.Expr` | 492 MB | 399 MB (81%) |
| `parseMomentum` → `Signer.Momentum` | 429 MB | 377 MB (88%) |
| `Merger.Add` → `Signer.Pairs` | 300 MB | 87 MB (29%) |

`pkg/pipeline/codec/signer.go:13-62` reuses an internal `bytes.Buffer` but allocates a **fresh `jsontext.Encoder` on every method call**. Each `Signer.LorentzIndex` runs `enc := jsontext.NewEncoder(&s.buf)` plus four `WriteToken` calls; the encoder header / state-machine objects swamp the 23-byte payload it emits.

This is the single largest lever in the codebase. Two routes (in order of leverage):

- Hand-roll the signature emitter. Every Signer output is a flat array of small known shapes — `["LorentzIndex","mu","D"]` and friends — and there is no benefit to round-tripping it through the json encoder. A `bytes.Buffer` you write quoted bytes directly into would eliminate both the encoder allocations *and* the WriteToken overhead (`jsontext.encoderState.WriteToken` shows up at 5.7% of CPU). For `Signer.Expr` (called from the scalar folder) the Atom values are restricted to `string | int32 | int64 | jsontext.Value | []any`, mirrored from `atomSignature` — same logic can be reused.
- Or, if the encoder must stay, hoist `enc := jsontext.NewEncoder(&s.buf)` to construction and use whatever reset hook v2 exposes. The `Signer.buf.Reset()` path already wants to reuse state.

### Drill-down: the parser

`parseLorentzIndex`/`parseMomentum` allocations are dominated by the Signer (already covered). What's left:

- `parser.go:107-134 parsePair` allocates a fresh `Pair` *and* fresh `Lorentz`/`Momentum` slices on every Pair (342 MB flat). The `p.lorentz`/`p.momentum` reuse-buffer is good, but the final `append(make([]LorentzIndex, 0, len(...)), p.lorentz...)` (line 127) is a per-Pair fresh allocation that the merger then retains. Sizes are tiny (≤2 elements), so consider a pair-pool or a typed two-element inline buffer in `Pair` itself.
- `parser.go:208-223 collectSources` is the same shape (sized copy out of a shared scratch). Worth checking whether the merger / walker actually need a fresh slice each time, or whether the scratch's lifetime can be extended.
- `jsontext.Token.String()` (137 MB cum / 8.75 M objects) — every `readToken(...).String()` allocates. For the four "well-known" names ("Plus","Times","Pair","LorentzIndex","Momentum","Power","Rational","Complex","D") the answer is always one of a handful of literals; matching on the token's underlying bytes instead of `.String()` would let the parser dedupe to package-level constants. This is also where `Atom`'s strings come from on the scalar leaf branch.

### Drill-down: the walker

`Walker.walkLeaf` returns a closure per leaf encountered, and the closure captures `prevCoeff`, `prevLen`, `folded` on the heap (363 MB / 11.15 M objects from the outer walker, 156 MB / 5 M from the inner). With 16 M closures over the run, this is roughly 32 bytes × 16 M = ~500 MB of pure scheduling state. Converting `walkLeaf` to a non-closure state machine (struct with an index, called via interface or non-generic function) would save the lion's share of those allocations.

Similarly, `walkPlus`/`walkTimes`/`walkPower` (`walker.go:63-97`) allocate a `[]stream` of length-of-children on each call (cumulative attribution to `mulStreams.func2` is 1.17 GB across the outer walker — those closures are the bulk). Caching the slice header in the Walker struct is straightforward; reusing the closures themselves requires more surgery.

### Drill-down: the calculator

`calculator.go:103-112 signature` calls `c.sigBuf.Reset()` followed by per-op `WriteString`s and `sigBuf.String()`. The `Reset` does NOT keep the underlying byte buffer (`strings.Builder.Reset()` zeroes the buf entirely in Go's stdlib). That's 538 MB of needless reallocation. Switching to `bytes.Buffer` + `unsafe.String` on `Bytes()` (the same trick already used in `expr.atomSignature`) cuts that to zero — and the returned string is consumed immediately as a map key, so the byte slice's lifetime is bounded by the lookup.

Adjacent: `calculator.go:93` `append(make([]Opaque, 0, len(c.opBuf)), c.opBuf...)` allocates a per-fold Opaques slice. With 92 M total objects in flight, this is one of them; cheap individually but worth a look if the slice can be sliced out of a per-Walker arena.

### Drill-down: the writer

`Writer.writeExpr` is a small allocator (95 MB cum) — `writeAtom` calls `json.Marshal` for `[]any` Atom values (47 MB) which is the only thing worth touching here. The encoder itself contributes 5.7% of CPU through `WriteToken`, but that's structural to v2's API and unrelated to the writer's own behaviour.

## The fastest wins, in priority order

1. **Replace `Signer`'s per-call `jsontext.NewEncoder` with direct byte writes.** ~2.3 GB of alloc disappears (≈40% of total), and ~6 M objects disappear (≈7% of count). Touches `pkg/pipeline/codec/signer.go`; shape is straightforward — every output is a flat array of small known elements.
2. **Use `bytes.Buffer` + `unsafe.String` in `Calculator.signature`.** ~540 MB of alloc disappears (≈9%). Touches `pkg/pipeline/eval/calculator.go:103`. Pattern already established in `expr.atomSignature`.
3. **Stop allocating closures in `walk.Walker.walkLeaf` / `walkPlus` / `walkTimes` / `walkPower`.** ~520 MB closure body + ~1.2 GB through `mulStreams.func2` cum traffic disappears. Touches `pkg/pipeline/walk/walker.go:63-121`. Bigger structural change — convert the cyclic-pull-generator to an explicit stack — but worth several hundred ms of wall time.
4. **Avoid `jsontext.Token.String()` for fixed-set names in the parser.** Saves ~140 MB / 9 M objects and shortens the parser hot path. Touches `pkg/pipeline/codec/parser.go:41,59,110`.
5. **Drop the worker pool, fold contraction into the walker goroutine.** Doesn't make anything faster on its own, but removes ~85 s of channel-wait noise from the block profile and frees the scheduler to give the parser/walker more uninterrupted core time. Touches `cmd/contractor/main.go:42-50`.
6. **For real parallelism**, shard the merger by hash of the pair-set signature and run N walker/contractor goroutines off the parser. This is the only realistic path below ~9 s of wall time, since the current pipeline is fundamentally single-threaded along its critical path.

## For peak-RSS specifically

Peak RSS (205 MB) is dominated by retained signature strings, retained `Pair`/`Monomial`/`Opaque` slices in `Merger.terms`, and the GC slack on top.

- The biggest single move is **shortening the retained signature strings.** Today every Pair and every Opaque carries a JSON-shaped signature. If the canonical key becomes a fixed-width hash (or an interned id) instead of a verbose JSON string, both the 20 MB `bytes.Buffer.String` retained slice and the per-LorentzIndex/per-Momentum strings shrink by ~3-10×.
- Tuning `GOGC` is a free dial: GOGC=200 cuts wall time to 10.3 s with peak RSS 308 MB; if RSS is more important than wall, GOGC=50 (the inverse direction) trades it back. Not a substitute for the structural fixes but a known knob.
- Anything that reduces the live `expr.Expr` graph held in `PowerExpr.Child` / `Opaque.Val` cuts retained memory directly — `expr.NewAtom`'s 18 MB live tail is bigger than it has to be (each Atom is ~32 bytes of struct plus its signature string).

## Profiling flags

Two of these ship in `cmd/contractor/main.go` alongside the pre-existing `-cpuprofile`/`-memprofile`. They default off and have no effect on the pipeline unless their flag is set:

- `-blockprofile <file>` — write a block profile via `pprof.Lookup("block")`.
- `-mutexprofile <file>` — write a mutex profile via `pprof.Lookup("mutex")`.

The remaining three were temporary instrumentation added only for this capture and removed before commit — they are not in the shipped binary:

- `-memstats` — print `runtime.MemStats` plus VmHWM from `/proc/self/status` on exit.
- `-heapsnap <prefix>` / `-heapsnap-ms <N>` — write `<prefix>-NNNN.prof` heap snapshots every N ms during the run. Lets you compare live-heap composition over time.
