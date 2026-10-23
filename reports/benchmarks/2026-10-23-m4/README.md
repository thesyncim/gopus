# Core codec measurements — 2026-10-23

These native Apple M4 Max measurements cover PGO-enabled codec source
`42975dbd3` and PGO-off source `fd287c213` on
`darwin/arm64`, Go 1.27.0, Apple Clang 21.0.0, libopus 1.6.1 and `GOMAXPROCS=1`.
PGO-enabled and PGO-off snapshots are explicitly separated. They do not establish current-revision amd64 results.
[Provenance](provenance.json) records reference flags and static-archive hashes.

## Explicit PGO comparison

The Go binaries in this snapshot explicitly use the checked-in root
`default.pgo`; C uses its matched scalar/SIMD `-O3` build without PGO. Profile
SHA-256 is `a85d5274259f90793a655bf823a20062238f897903b7f0634e885bdfbec770b9`; its source
commit is `18eaa63b8` (native amd64 profile). The codec source is `42975dbd3`.
[PGO provenance](pgo-provenance.json) records both identities, so applying a
profile is not confused with regenerating it from current source.

| Workload | C SIMD ns/sample | Go SIMD ns/sample | Go/C time | Result |
|---|---:|---:|---:|---|
| Encode aggregate | 146.32 | 143.66 | 0.982× | Go faster |
| Encode CELT, fullband, 20 ms stereo, 128 kbps | 74.57 | 86.89 | 1.165× | Go slower |
| Encode CELT, fullband, 5 ms mono, 64 kbps | 34.61 | 41.42 | 1.197× | Go slower |
| Encode SILK, wideband, 20 ms mono, 32 kbps | 337.21 | 282.86 | 0.839× | Go faster |
| Encode Hybrid, fullband, 20 ms mono, 64 kbps | 169.95 | 166.92 | 0.982× | Go faster |
| Encode Hybrid, fullband, 20 ms stereo, 96 kbps | 88.10 | 102.49 | 1.163× | Go slower |
| Decode RFC vectors, float32 | 19.00 | 17.63 | 0.928× | Go faster |
| Decode RFC vectors, int16 | 19.20 | 18.09 | 0.942× | Go faster |

Scalar and SIMD values, in ns/sample per channel:

| Workload | C scalar | Go scalar | C SIMD | Go SIMD |
|---|---:|---:|---:|---:|
| Encode aggregate | 167.30 | 154.39 | 146.32 | 143.66 |
| Encode CELT, fullband, 20 ms stereo, 128 kbps | 97.05 | 105.37 | 74.57 | 86.89 |
| Encode CELT, fullband, 5 ms mono, 64 kbps | 39.57 | 52.22 | 34.61 | 41.42 |
| Encode SILK, wideband, 20 ms mono, 32 kbps | 375.95 | 292.99 | 337.21 | 282.86 |
| Encode Hybrid, fullband, 20 ms mono, 64 kbps | 197.06 | 177.08 | 169.95 | 166.92 |
| Encode Hybrid, fullband, 20 ms stereo, 96 kbps | 118.42 | 122.17 | 88.10 | 102.49 |
| Decode RFC vectors, float32 | 19.55 | 21.04 | 19.00 | 17.63 |
| Decode RFC vectors, int16 | 21.32 | 23.36 | 19.20 | 18.09 |

Raw output: [PGO scalar](pgo-scalar.log), [PGO SIMD](pgo-simd.log).
Machine-readable rows: [PGO scalar TSV](pgo-scalar.tsv),
[PGO SIMD TSV](pgo-simd.tsv). All 16 Go rows across the two lanes report zero
warm allocations. The same five-run, 300 ms minimum, single-threaded methodology
below applies; the explicit build argument is `-pgo=default.pgo`.

Lower ns/sample and ns/packet is faster; higher × realtime is faster. Go/C time
below 1.0 means Go is faster. The PGO-off SILK row of 287.108348 ns/sample against
C's 347.584491 is a Go win: 17.4% less time, or 72.56× versus 59.94× realtime.
The PGO-enabled SILK row is 282.860092 against 337.209430, 16.1% less time.
These are separate snapshots, not an interleaved PGO-on/off A/B experiment;
their difference cannot establish the impact of PGO.

Reproduce with the commands below using `PGO_FILE=default.pgo`. For native amd64,
apply the target and reference-selection flags in the
[README commands](../../../README.md#performance). The existing
profile is measured as committed; native regeneration and its own benchmarks
are a separate `PGO refresh` workflow artifact. Architecture, workload mix and
profile age all limit how broadly these results apply.

## PGO-off comparison

Each row is the median by ns/sample of five runs of at least 300 ms. C and Go
receive the same controls, packet streams or PCM inputs. Timings include stream
reset, exclude construction, input generation/loading, C helper startup and file
I/O, and run without concurrent benchmarks or tests. Scalar and SIMD lanes run
sequentially, scalar first; this is a host snapshot, not an interleaved speedup
estimate between lanes. C scalar disables assembly, RTCD and intrinsics; C SIMD
enables RTCD and intrinsics. The reference selection follows the Go build lane.

| Workload | C scalar | Go scalar | C SIMD | Go SIMD |
|---|---:|---:|---:|---:|
| Encode aggregate | 169.80 | 151.35 | 148.79 | 140.48 |
| Encode CELT, fullband, 20 ms stereo, 128 kbps | 97.77 | 102.56 | 74.05 | 83.35 |
| Encode CELT, fullband, 5 ms mono, 64 kbps | 40.02 | 50.36 | 34.97 | 41.65 |
| Encode SILK, wideband, 20 ms mono, 32 kbps | 378.43 | 286.71 | 347.58 | 287.11 |
| Encode Hybrid, fullband, 20 ms mono, 64 kbps | 194.00 | 174.80 | 179.19 | 164.38 |
| Encode Hybrid, fullband, 20 ms stereo, 96 kbps | 111.68 | 124.92 | 93.23 | 102.09 |
| Decode RFC vectors, float32 | 19.38 | 20.22 | 18.65 | 17.34 |
| Decode RFC vectors, int16 | 20.82 | 22.95 | 18.55 | 17.89 |

The encode aggregate gives each of five one-second workloads 48,000 samples per
channel: 400 packets and 240,000 samples per aggregate operation. Stereo workload
sample counts count frames, not interleaved values. The RFC decode aggregate uses
20,075 packets, 48 kHz stereo output and 15,390,480 samples per channel.
[Encoder workload hashes](encoder-workloads.tsv) identify the deterministic input
PCM used by `encoderbenchcmp`.

Raw outputs: [scalar](scalar.log), [SIMD](simd.log). Machine-readable rows:
[scalar TSV](scalar.tsv), [SIMD TSV](simd.tsv). Every Go row passes the exact
zero-allocation guard, which measures warmed reset-plus-stream processing with
`testing.AllocsPerRun`. The TSV `bytes_per_op` field means compressed packet bytes,
not allocated bytes. C allocations are not measured. The runtime-ratio ceiling
of 3.25 is only a regression guard and is not evidence of outperforming libopus.

Run from the repository root with the project environment wrapper where required:

```sh
GOMAXPROCS=1 GOEXPERIMENT=nosimd make bench-libopus-guard \
  PGO_FILE=off BENCH_LIBOPUS_GUARD_TIME=300ms BENCH_LIBOPUS_GUARD_COUNT=5
GOMAXPROCS=1 GOEXPERIMENT=simd make bench-libopus-guard \
  PGO_FILE=off BENCH_LIBOPUS_GUARD_TIME=300ms BENCH_LIBOPUS_GUARD_COUNT=5
```

The SIMD encoder aggregate Go/C ratio is 0.944; RFC float32/int16 decode ratios
are 0.930/0.964. CELT 20 ms, CELT 5 ms and stereo Hybrid encoding remain slower
than C at 1.126/1.191/1.095. These results do not support a universal faster-than-C
claim. Native amd64 timings and exactness require a native runner; older results
are scoped to their revision in the [validation reference](../../validation.md#performance).

## CELT 2.5 ms allocation-loop simplification

The comparison isolates `internal/celt/alloc_decode_std.go`: `before` uses the
file at `49a55632e`; `after` uses the file at `fd287c213`. Other codec sources match.
The retained change selects the invariant allocation row once and records the
last boosted band in the existing boost branch. Arithmetic and entropy-symbol
order remain unchanged.

The [public benchmark samples](short-decode-public.txt) contain six samples per
variant and configuration, at 500 ms each, over eight mono/stereo and
8/16/24/48 kHz configurations. Each operation resets and decodes 400 packets.
The invocation order is before/after/after/before/before/after, with two samples
per invocation. All 96 rows report **0 B/op, 0 allocs/op**. The geometric mean
of the per-case median after/before ratios is 0.991; per-case changes range
from −1.99% to +0.19%. This small difference is noise-sensitive; the change is
retained for simpler work in the loop, without a broad speedup claim.

The [interleaved C comparison](short-decode-direct.txt) runs 15 alternating
rounds, three timed passes and three instances per side, over the same eight
CELT configurations. The retained source's Go/C geometric-mean ratio is 0.937
(range 0.891–0.989). A/A instance spread is 0.5% median, 1.8% maximum.
[Summary data](short-decode-summary.json) includes the individual medians and
samples. These short CELT cases complement the RFC aggregate; they do not
represent all Opus decode workloads.

```sh
GOMAXPROCS=1 GOEXPERIMENT=simd GOPUS_BENCH_TIER=simd go test -pgo=off \
  -tags gopus_libopus_bench -run '^$' -bench '^BenchmarkCELTShortFrameDecode$' \
  -benchtime=500ms -count=6 .
GOMAXPROCS=1 GOEXPERIMENT=simd GOPUS_BENCH_TIER=simd \
  GOPUS_SCOREBOARD_INTERLEAVED=1 GOPUS_SCOREBOARD_MODES=decode \
  GOPUS_SCOREBOARD_FILTER='^CELT/(mono|stereo)/(8|16|24|48)k/2_5ms$' \
  GOPUS_SCOREBOARD_ROUNDS=15 GOPUS_SCOREBOARD_PASSES=3 GOPUS_SCOREBOARD_INSTANCES=3 \
  go test -pgo=off -tags gopus_libopus_bench -run '^TestScoreboardInterleaved$' -v -count=1 .
```

For before/after experiments, build both binaries first, changing only the
allocation source through a Go overlay, then alternate their execution. The
commands above reproduce retained-source workloads, not that A/B ordering.
