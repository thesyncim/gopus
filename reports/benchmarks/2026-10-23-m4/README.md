# Core codec measurements — 2026-10-23

These native Apple M4 Max measurements cover codec source `fd287c213` on
`darwin/arm64`, Go 1.27.0, Apple Clang 21.0.0, libopus 1.6.1, `GOMAXPROCS=1`
and PGO disabled. They do not establish current-revision amd64 results.
[Provenance](provenance.json) records reference flags and static-archive hashes.

## Matched libopus comparison

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
