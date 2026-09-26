# Go kernel replacement evidence

This report tracks all 53 symbols from the 41 assembly files in the pre-port
`origin/master` tree. Every entry has a Go replacement. `archsimd` means the
Go 1.27 `simd/archsimd` implementation selected by `GOEXPERIMENT=simd`; ordinary
builds use the listed scalar Go path, and `-tags nosimd` forces the scalar
reference path.

## Correctness status

The live oracle comparisons use libopus 1.6.1 with the same effective
instruction path on the same machine. Go SIMD (`GOEXPERIMENT=simd`) pairs with the SSE/AVX2 RTCD
reference on AMD64 and the NEON reference on ARM64. Ordinary Go and `nosimd`
pair with the scalar reference (`-O3 -fno-tree-vectorize
-fno-tree-slp-vectorize`). Native AMD64 uses GCC 13.3.0; local ARM64 uses Apple
clang 21.0.0. Resolver checks validate the selected reference and its build stamp.
The native AMD64 SIMD helper reports `opus_select_arch=4` (AVX2), while its
scalar counterpart reports zero with no SIMD macros. ARM64 SIMD binds NEON
at compile time; its zero runtime arch value is not a scalar selection.
Wrong-variant, archive/header, and compiler-policy rejection tests pass in all
three local modes. The public and multistream decode helpers use the same
build-aware archive resolver as the strict CBR oracle. Short-frame, CELT header,
band-allocation, variant provenance, and SILK flush comparisons use live C
packets from the selected reference. Every case logs the C build identity and
PCM input hash. Packet bytes, final ranges, and counts are strict in short-frame,
provenance, and flush tests; header/allocation tests require exact stage fields
and counts and also log packet/range differences. Allocation fields have no
percentage allowance. Quality checks retain their normal floors without
cross-feature exceptions. Fixture bytes, hashes, and baselines are unchanged.

Decoder matrix/rate/loss/corpus/transition and long-frame encoder fixture
consumers still have static numerical expectations without matched CPU
provenance. Their conversion to live C is pending. Frozen packet inputs and
fixture-honesty checks retain their independent roles.

Strict matched CBR oracle, 19 configurations and 2,175 packets per lane:

| Lane | C reference | Exact configurations |
|---|---|---:|
| amd64 Go SIMD | SSE/AVX2 RTCD, gcc | 19 / 19 |
| amd64 ordinary Go | scalar, gcc | 19 / 19 |
| amd64 `nosimd` | scalar, gcc | 19 / 19 |
| arm64 Go SIMD | NEON, Apple clang 21 | 19 / 19 |
| arm64 ordinary Go | scalar, Apple clang 21 | 19 / 19 |
| arm64 `nosimd` | scalar, Apple clang 21 | 19 / 19 |

Each lane has zero packet-byte and final-range differences. All three native
AMD64 lanes also pass at `036c4d51`.
Local ARM64 results include the Hybrid transient gate and explicit gain-fade
contraction. This CBR subset does not establish full codec parity.

The live variant audit at `036c4d51` executes all 92 cases: ordinary ARM64
scalar C matches 90/92 exactly; ARM64 NEON matches 85/92 exactly. Scalar failures
are 5 ms stereo CELT chirp and speech; SIMD failures cover six CELT cases and
one Hybrid chirp case. No severe quality gaps occur, but every packet/range
mismatch remains a hard failure. Forced `nosimd` also matches 90/92, with the
same two scalar failures. Its 92-case coverage combines 81 completed cases
before a ten-minute command timeout and an 11-case continuation that passes;
no case is skipped or discarded.
The resolver/rejection matrix passes in all three local modes. An actual SIMD
test invocation with a scalar C override fails before encoding as required;
unchanged fixture coverage, hashes, and stable ordering also pass.

The Hybrid low-complexity gate checks all eight frames of mono 10 ms and stereo
20 ms SWB 48 kbps VBR at complexities 0 and 1, including packet bytes and final
ranges. It passes in all three local modes, as does the actual-C `gain_fade`
float-bit oracle across 13 in-place mono/stereo cases at 48/24 kHz. Warmed
caller-buffer complexity-0 encode checks report zero allocations.

CELT deemphasis runs the exact sequential libopus recurrence, including
`VERY_SMALL` and memory updates on silence. All three local modes pass the six
live-C helper tests, signal/silence state carry and downsample checks, 40 public
silence decode configurations, and both PLC stage cases. Multistream fade-out
requires actual decoded Hybrid history; exact live-C mono/stereo tests cover
fresh SILK, Reset after nonzero Hybrid history, and real Hybrid-to-SILK
transitions. Warmed single-stream public decode
and mono/stereo silence-transition guards report zero allocations. The touched
root, CELT, and multistream packages compile for Linux AMD64 with SIMD;
that cross-compilation provides no native runtime parity evidence.

Transition concealment applies the recursive libopus output gain before
crossfading; the completed outer frame applies gain again. A live-C probe
checks the preceding packet PCM, exact returned length, and every sample of
a standalone 5 ms NULL-packet concealment step at gains 0 and +768. ARM64
SIMD matches the complete standalone stage at both gains. Ordinary and
`nosimd` first differ at sample 50 before gain, identifying a separate PLC
residual. The full CELT-to-Hybrid transition head first differs at sample 2
in all three local modes; its actual recursive context remains under trace.
These probes retain exact assertions. They do not establish whole-transition
parity or close any full-sweep leaf. The early native capture includes both
probes and retains all 3,640 strict multistream/projection cases.

The ARM64 checkpoint at `301be749` includes a strict diagnostic of all 1,440
public encode-then-decode configurations, comparing every output float bit
against the matched C reference. These counts do not include the encoder
refactor at `036c4d51`:

| ARM64 lane | Passing cases | Failing cases | Failing CELT / Hybrid cases |
|---|---:|---:|---:|
| SIMD, exact deemphasis | 1,197 | 243 | 78 / 165 |
| Ordinary scalar | 1,334 | 106 | 17 / 89 |
| `nosimd` scalar | 1,334 | 106 | 17 / 89 |

The same strict SIMD diagnostic with the `e6f2b332` deemphasis implementation
has 496 failures: 253 cases differ between the two failure sets, all passing
with the exact recurrence. The committed public decode harness still has
legacy tolerances; this diagnostic overrides them to expose the residuals.
Multistream decode assertions require exact float bits and integer samples,
including mode transitions. The 3,640-case strict multistream/projection sweep
has no skipped cases:

| ARM64 lane | Passing cases | Failing cases | Surround / discrete / projection / Go-encoded failures |
|---|---:|---:|---:|
| SIMD | 2,333 | 1,307 | 1,080 / 151 / 26 / 50 |
| Ordinary scalar | 2,341 | 1,299 | 1,075 / 149 / 25 / 50 |
| `nosimd` scalar | 2,341 | 1,299 | 1,075 / 149 / 25 / 50 |

The same strict SIMD sweep at `e6f2b332` has 2,994 failures. Exact deemphasis
and the decoded-history guard resolve 1,690 cases and expose three int16
cases. For those three, actual C deemphasis matches Go PCM and final state
for all 54 checked calls given identical incoming data; C clipping changes no
samples, and each float-to-int16 conversion matches its own input. The residual
is upstream float synthesis/history and remains a strict failure. Combined
with the public decode diagnostic, SIMD has 1,550 failures out of 5,080 cases
versus 3,490 at `e6f2b332`; each scalar lane has 1,405 failures out of 5,080.
These are failing configurations, not counts of independent defects.

Native AMD64 early evidence at `036c4d51`, from
[run 36253587780](https://github.com/thesyncim/gopus/actions/runs/36253587780),
uses AMD EPYC 7763, Go 1.27.1, and GCC 13.3. SIMD C reports
AVX2 dispatch (`opus_select_arch=4`); scalar C reports zero and no SIMD
features. The strict 3,640-case multistream/projection sweep has no skips:

| AMD64 lane | Passing cases | Failing cases | Surround / discrete / projection / Go-encoded failures |
|---|---:|---:|---:|
| SIMD | 2,874 | 766 | 665 / 35 / 39 / 27 |
| `nosimd` scalar | 3,628 | 12 | 3 / 3 / 6 / 0 |

All 12 scalar failures first differ on mode-transition frames. Both lanes pass
the six deemphasis helpers, state/downsample coverage, 40 public silence cases,
the mono/stereo history regression, and the warmed single-stream zero-allocation guards. The multistream guard
passes its existing allowance of eight allocations per call; that is not a
zero-allocation result. The SIMD stereo PLC stage test fails at final PCM sample 234 by one float32 ULP;
its captured earlier stages match. Scalar PLC passes. The existing exceptional
SIMD pitch-xcorr oracle also fails. The full native run at this revision is
cancelled during parity testing; the early subset is not a total mismatch
inventory. All three kernel benchmark phases and the old/SIMD/`nosimd`
end-to-end phases complete with exit status zero before cancellation. The
`301be749` full run is also cancelled; neither partial parity artifact
establishes a complete mismatch total.

The completed native `036c4d51` encoder differential family checks all
1,788 configurations × eight frames with zero packet, range, framing, or
cadence differences. The same capture completes 2,268 surround encode
configurations with 287 failing leaves and 432 projection encode
configurations with 159 failing leaves. The projection harness also logs
mode/float residuals under legacy allowances, so passing leaves in that
family are not all exactness proofs. The mono 384 kbps CVBR anchor emits
1,260 Go bytes versus 1,276 C bytes on frame zero; per-stream capacity and
packet budgeting remain under investigation. These completed families are
usable evidence within the interrupted full run, not a complete codec total.

Full parity remains incomplete. The complete native AMD64 SIMD sweep at
`e6f2b332` contains 1,819 unique failing leaf cases across 24 test families:
1,732 multistream/projection, 20 stateful encode, and 67 other checks. The
`b0c9c56a` comparison has 2,057 failures; 238 cases pass at `e6f2b332`.
Those counts describe that revision and include fixture evidence failures;
they are neither current totals nor counts of independent bugs.

Remaining investigations include:
- Encode at `036c4d51`: matched ordinary ARM64 scalar C checks find 38 differing
  frames in 32/1,788 differential configurations and 293 differing frames in
  176/2,844 executed stateful configurations. The stateful harness excludes
  144 additional LBRR-panic specifications. Both tests return PASS under their
  legacy float waivers; these results are not exact parity. All 260 native-rate
  configurations produce matching packets in the sub-48-kHz harness; that
  harness does not check final ranges. The paired scalar variant sweep has
  90/92 exact cases; 5 ms stereo chirp and speech differ. Long-frame, DTX,
  multistream/projection rate allocation and packet budgeting need exact traces.
- Decode: non-silent CELT/Hybrid PCM, SILK stereo multi-frame FEC/LBRR,
  multistream transitions, and mapping/projection residuals need exact traces.
- AMD64 kernels: exceptional pitch-cross-correlation inputs expose NaN-payload
  and signed-zero instruction-order differences. Scalar float32 FMA emulation
  through `float32(math.FMA(...))` also needs exact rounding verification.
- ARM64 SIMD analysis: the live AnalysisInfo/state sweeps have tonality,
  noisiness, slope, and RNN-state differences with clang 21. Ordinary and
  `nosimd` analysis pass locally; native SIMD analysis passes at `e6f2b332`.
- Multistream allocation: a warmed ARM64 SIMD diagnostic of
  `multistream.Decoder.DecodeToFloat32` measures 6 allocations per two-frame
  steady CELT cycle and 8 for steady SILK/Hybrid. Alternating Hybrid/SILK,
  SILK/CELT, and CELT/Hybrid cycles measure 9–16 allocations for mono/stereo.
  The existing multistream allowance is eight per call. These paths do not
  meet a zero-allocation contract; the kernel and single-stream benchmark
  zeroes do not cover them.
- Legacy stateful and decode tolerances can report PASS despite differences.
  Their PASS is not counted as exact parity; owning fixes require strict gates.

## Measurement method

M4 Max (`darwin/arm64`) A/B measurements use the same host and Go version for
each old-assembly/candidate pair. Initial rows use Go 1.27.1; ARM64 tone LPC
and tuned IMDCT pre-rotate, fold, middle-fold, post-twiddle, and comb filter use
Go 1.27.0. The direct benchmark set uses GOMAXPROCS=4 and five samples per
mode; the initial rows use 100 ms samples and the added inventory wrappers use
200 ms samples.
The tuned PCM and ARM64 SILK xcorr rows use five 250 ms samples; ARM64 tone
LPC uses five interleaved 300 ms samples. All use GOMAXPROCS=4 on the same M4.
The tuned SILK float conversion row uses 20 interleaved samples per mode with
one million calls per sample and the same N=480 input fixture. Pitch detection
uses scale 1; scale 32768 is measured separately.
The tuned SILK 21846 FIR row uses 20 interleaved samples per mode with 400,000
calls per sample and the live nOut=240, bufLen=87 fixture.
The tuned SILK 32768 and 43691 FIR rows use five 300 ms samples per mode on
the same M4 with nOut=240; production-dispatch timings include the wrapper.
The tuned SILK int16-to-float32 row uses five 300 ms samples per mode on the
same M4 with N=480 and compares the production-dispatch path.
The tuned ARM64 SILK pitch-xcorr row uses five paired 300 ms samples per mode
on the same M4 with length=240 and maxPitch=120.
The tuned ARM64 SILK LPC synthesis row uses five paired 300 ms samples per mode
on the same M4 with subframe length 80. One assembly sample is a low outlier;
the other four measure 249.2–250.5 ns/op.
The tuned ARM64 unit PCM conversion row uses five interleaved outer runs with
500,000 calls per mode and n=480; the table gives the mean and range of the
five run means on the same M4 with Go 1.27.0.
The tuned ARM64 MDCT middle-fold row uses five paired 200 ms samples per mode
on the same M4 with n4=64 and blocks=8; the old assembly and both Go SIMD
versions use the same direct fixture and Go 1.27.0.
The tuned ARM64 FFT butterfly rows use three paired 200 ms samples per mode
on the same M4 with Go 1.27.0 and `-cpu=1`. The M1 fixture uses N=128; inner
radix-3/4/5 fixtures use m=8, N=4, and fstride=8 with preallocated work copies.
Candidate timings call the production dispatch wrappers.
The refined ARM64 radix-4 M1 path uses seven paired 500 ms samples on the same
M4 and fixture. Its prior and refined Go SIMD medians are 178.0 and 120.9
ns/op; both report zero allocations. The old-assembly timing is from the
earlier paired comparison, so the 103–105 ns range is a reference, not a
same-run ratio for this refinement.
The ARM64 Haar two-group unroll uses five paired 500 ms samples on M4 with
Go 1.27.0 and zero allocations. The live `haar1` wrapper with n0=32 dispatches
to the stride-4 helper with 16 groups; its current and refined Go SIMD medians
are 11.25 and 8.00 ns/op. A direct helper fixture with 32 groups measures
11.69 and 11.14 ns/op medians. These paired Go-only measurements do not use
the earlier old-assembly fixture.
The ARM64 SILK LPC synthesis refinement uses five paired same-fixture runs
with Go 1.27.0, `GOEXPERIMENT=simd`, GOMAXPROCS=1, and subframe length 80.
The current Go SIMD path measures 290.4–292.1 ns/op and the refined path
253.4–255.1 ns/op, both allocation-free. The old-assembly ~250 ns timing
comes from the earlier paired comparison and is a reference here.
The CELT PVQ two-position unroll uses ten paired samples of a normalized
N=48, K=16 low-pulse input with zeroed work buffers, matching the production
path's input shape. The scalar loop measures 705.0 ns/op median
(702.5–728.0), and the unroll measures 522.9 (518.5–527.3), both zero alloc.
The original old-assembly comparison uses a different fixture; no ratio is
inferred across those runs.
The ARM64 SILK float inner-product bounds refinement uses the same M4 and
Go 1.27.0 fixture for five paired samples: 135.6 ns/op median for the prior
Go loop and 107.1 for the refined loop, both zero alloc. Its old-assembly
comparison uses a different fixture.
The ARM64 pointer-endpoint fixes preserve kernel arithmetic and pass the full
CELT SIMD suite with `-gcflags=all=-d=checkptr=2`. Post-fix spot benchmarks
use Go 1.27.0, `GOEXPERIMENT=simd`, `-cpu=1`, three 200 ms samples on M4.
These spot timings are separate from the paired assembly comparisons in the
matrix: L1 N=480 33.54–33.65; prefilter dual N=240 26.96–27.04; scale
N=480 17.30–17.51; stereo merge N=480 50.18–50.64; tone LPC N=480
76.46–76.62; four-output xcorr N=480 133.5–133.7 ns/op.
The ARM64 CWRS row uses five paired 500 ms samples per mode on the same M4
with Go 1.27.0 and GOMAXPROCS=4. N=48, K=5 is table-covered by
`canUseCWRSFast` and reaches the fast decoder path; N=48, K=12 does not.
The ARM64 deemphasis comparison uses five interleaved 300 ms samples per
version on M4 with Go 1.27.0, `GOEXPERIMENT=simd`, and `-cpu=1`. Identical
benchmarks call the live mono and planar-stereo wrappers with N=480, varied
preallocated input, fixed initial memory, and observed output/state. The
reference is the `e6f2b332` implementation through a source overlay; the candidate
uses the exact sequential C recurrence. Public decode binaries use the same
settings and caller-owned output. All samples report zero allocations.
The tuned MDCT post-twiddle row uses three interleaved 15-sample runs; its
reported ranges are the three within-run medians because individual samples
include M4 scheduling outliers. Assembly and both SIMD versions use the same
fixture.
Native AMD64 measurements use real AVX2/FMA hardware. Rosetta is useful for
cross-compilation and scalar checks but does not establish native SIMD behavior.
Kernel benchmark names retain their fixture identities across the baseline and
candidate. The corrected `b.Loop` wrappers observe outputs and vary inputs where
required; zero-valued butterfly buffers keep repeated arithmetic finite.

Run [36056914422](https://github.com/thesyncim/gopus/actions/runs/36056914422)
at `54300227` uses Intel Xeon 6973P-C, Go 1.27.1, GCC 13.3.0, and
`GOAMD64=v1`. The AMD64 inventory uses the five direct samples per mode from run
[36253587780](https://github.com/thesyncim/gopus/actions/runs/36253587780)
at `036c4d51` on AMD EPYC 7763, with the same toolchain and `GOAMD64=v1`.
All direct samples report zero allocations. Both runs use sequential phases;
host load or frequency changes remain a measurement risk. Ratios compare only
modes within one run.

## End-to-end codec throughput

The six public encode/decode benchmarks run on one Apple M4 Max with Go
1.27.0, `-cpu=1`, three interleaved 300 ms samples per mode, and preallocated
caller buffers. Old is the pre-port `ef5a9fe74` default assembly build;
Go SIMD and `nosimd` use `1b18fbf0` with `GOEXPERIMENT=simd`. The table gives
median ns/op; lower is faster. Every sample reports 0 B/op and 0 allocs/op.

| Fixture | Old assembly | Go SIMD | `nosimd` |
|---|---:|---:|---:|
| CELT decode | 6,972 | 6,999 | 10,344 |
| Hybrid decode | 12,694 | 12,278 | 13,623 |
| SILK decode | 10,369 | 9,207 | 10,378 |
| Caller-buffer encode | 38,918 | 34,517 | 52,475 |
| VoIP encode | 46,067 | 38,627 | 61,210 |
| Low-delay encode | 40,303 | 34,744 | 54,005 |

These samples have substantial timing spread (for example, old caller-buffer
encode spans 35,914–43,802 ns/op and Go SIMD spans 33,682–40,909 ns/op).
They establish current allocation counts and measured ranges; small throughput
changes are inconclusive. The earlier quieter `cd62a758` run measures a 2.8%
SIMD improvement by equal-fixture geometric mean. The native AMD64 A/B
workflow records the same three-mode end-to-end benchmarks on one x86 runner.
These tables compare Go implementations with old Go assembly, independently
of the matched libopus scalar/SIMD correctness comparisons.

### ARM64 exact deemphasis: paired decode cost

Five interleaved 300 ms samples on M4, Go 1.27.0, SIMD, `-cpu=1`.
Both revisions use the same benchmark fixture. All samples are 0 B/op and
0 allocs/op. These are Go-to-Go timings, independent of the assembly tables.

| Caller-buffer decode | `e6f2b332` ns/op | Exact recurrence ns/op | Time change |
|---|---:|---:|---:|
| CELT mono | 6,841 | 7,915 | +15.7% |
| Hybrid mono | 11,912 | 13,210 | +10.9% |
| CELT stereo | 9,166 | 10,197 | +11.2% |

The exact path preserves C rounding and executes deemphasis on silence.
The paired live-wrapper measurements are in row 3. No native AMD64 performance
change is inferred from this ARM64 comparison.

### Native AMD64 candidate

Run 36031595048 uses one AMD EPYC 9V74 runner, Go 1.27.1, `-cpu=1`, and
three 300 ms samples per mode. All samples report zero bytes and allocations.
The candidate is `cd62a758`; its unresolved correctness failures are listed
above. These measurements do not establish packet correctness.

| Fixture | Old assembly | Go SIMD candidate | `nosimd` |
|---|---:|---:|---:|
| CELT decode | 20,203 | 20,227 | 22,560 |
| Hybrid decode | 29,753 | 29,829 | 31,696 |
| SILK decode | 23,528 | 23,630 | 23,987 |
| Caller-buffer encode | 92,583 | 123,113 | 118,525 |
| VoIP encode | 98,724 | 129,079 | 124,775 |
| Low-delay encode | 91,661 | 122,326 | 118,035 |

Go SIMD decode is within 0.5% of assembly on these fixtures; encode takes
30.7–33.5% more time. Ratios compare only modes on this same runner.

### Native AMD64: Intel Xeon 6973P-C

Run [36056914422](https://github.com/thesyncim/gopus/actions/runs/36056914422)
at `54300227` uses the same benchmark settings as the EPYC table above.
Every sample reports zero bytes and allocations.

| Fixture | Old assembly | Go SIMD candidate | `nosimd` |
|---|---:|---:|---:|
| CELT decode | 17,207 | 14,095 | 15,591 |
| Hybrid decode | 21,354 | 18,969 | 20,069 |
| SILK decode | 15,332 | 14,729 | 14,929 |
| Caller-buffer encode | 69,964 | 65,658 | 82,568 |
| VoIP encode | 73,721 | 69,483 | 87,243 |
| Low-delay encode | 69,369 | 65,033 | 82,216 |

Go SIMD encode takes 5.7–6.3% less time and decode 3.9–18.1% less time
within this run. CPU and revision differ from the EPYC run; compare modes
within each run rather than inferring changes across these hosts.

### Native AMD64: AMD EPYC 7763

Run [36253587780](https://github.com/thesyncim/gopus/actions/runs/36253587780)
at `036c4d51`, Go 1.27.1, three 300 ms samples per mode, `-cpu=1`.
The assembly baseline is `8ac93c85`. All samples report zero allocations;
timings use sequential phases and the table reports median ns/op. These
benchmark phases complete successfully before the run is cancelled during
its separate parity phase.

| Fixture | Old assembly | Go SIMD | `nosimd` |
|---|---:|---:|---:|
| CELT decode | 20,180 | 16,882 | 22,869 |
| Hybrid decode | 28,614 | 26,095 | 31,675 |
| SILK decode | 22,454 | 21,628 | 22,813 |
| Caller-buffer encode | 91,867 | 88,566 | 118,153 |
| VoIP encode | 98,463 | 94,779 | 125,413 |
| Low-delay encode | 91,530 | 87,898 | 117,833 |

Within this run, Go SIMD takes 3.6–4.0% less time for encode and 3.7–16.3%
less for decode. Other host/revision tables are separate measurements;
no cross-run ratio establishes an improvement. These timings include the
exact-deemphasis path and do not establish complete parity.

### Interleaved AMD64 encode and profiles

Run [36248080529](https://github.com/thesyncim/gopus/actions/runs/36248080529)
at `e6f2b332` compares assembly `8ac93c85` with Go SIMD on AMD EPYC 7763,
Go 1.27.1, using four interleaved 500 ms samples, `-cpu=1`, matching PGO
settings and preallocated caller buffers.

| Caller-buffer encode | Median ns/op | Sample range | Allocs/op |
|---|---:|---:|---:|
| Old assembly | 91,926 | 91,812–92,040 | 0 |
| Go SIMD | 87,662.5 | 87,406–88,504 | 0 |

Go SIMD takes 4.6% less time in this pair. It has no `nosimd` measurement.
Separate 3-second profiles at `b0c9c56a` put pitch search at 10.67% cumulative samples in
Go SIMD versus 5.51% in assembly; the one-pass xcorr helper has 4.67% flat
samples versus 2.20% for assembly xcorr. The SSE-order dual prefilter takes
4.67% cumulative samples in Go SIMD versus 8.59% in assembly. These shares
identify live paths; they do not establish each kernel's contribution to the
end-to-end timing difference.

### Current native AMD64 interleaved encode

[Run 36253587780](https://github.com/thesyncim/gopus/actions/runs/36253587780)
compares assembly `8ac93c85` with `036c4d51` on AMD EPYC 7763,
Go 1.27.1, using four interleaved 500 ms samples, `-cpu=1`, matching PGO
settings, and preallocated caller buffers.

| Caller-buffer encode | Median ns/op | Sample range | Allocs/op |
|---|---:|---:|---:|
| Old assembly | 91,870 | 90,932–92,051 | 0 |
| Go SIMD | 88,977 | 87,905–90,532 | 0 |

Go SIMD takes 3.1% less time in this same-run pair. The `301be749` pair on
Xeon Platinum 8573C measures 94,975.5 → 81,601.5 ns/op (14.1% less time).
Those different hosts cannot establish a revision-to-revision gain or loss.
The completed benchmark phases in the full artifact supply the three-mode
decode table and all 11 measurable AMD64 symbol rows at the same revision.

## Per-symbol inventory

Former symbols identify the pre-port assembly entry points. `0` in the
allocation column is limited to directly measured kernels.

Direct timing covers all 51 comparable routines. The two startup
feature-discovery helpers have no
comparable per-call Go operation and are marked n/a with the reason.

| # | Former assembly symbol | Old arch | Go replacement source | Replacement path | Measured timing (ns/op) | Allocs/op | Status |
|---:|---|---|---|---|---|---|---|
| 1 | `combFilterConstNeon` | arm64 | `internal/celt/comb_const_simd_arm64.go`; `internal/celt/comb_const_default.go` | archsimd / scalar | N=480: old asm 115.4 (114.7–116.9) → Go SIMD 109.0 (107.7–113.0); scalar Go 617.7 (592.7–623.2; earlier Go 1.27.1 run) | 0 | measured on M4 with Go 1.27.0; Go SIMD is 5.5% faster than asm; exact and zero-alloc checks pass |
| 2 | `cwrsiFastCore` | arm64 | `internal/celt/cwrs_fast_default.go` | scalar Go | live table-covered N=48, K=5: old asm 28.94 (28.85–29.34) → scalar Go 44.02 (43.81–44.12) ns/op | 0 | paired M4 Go 1.27.0; scalar Go is 52% slower than asm; exact output and zero-allocation checks pass |
| 3 | `deemphasisStereoPlanarF32Core` | arm64 | `internal/celt/deemphasis_f32_default.go`; `internal/celt/output_helpers.go` | exact scalar recurrence in every build | original direct N=480 old asm → scalar Go: 1,065 (1,065–1,068) → 1,167 (1,166–1,169); current live wrapper pair, e6 SIMD → exact recurrence: stereo 353.5 (349.9–367.6) → 868.9 (860.1–881.0), mono 225.9 (222.2–229.7) → 693.0 (686.4–706.9) | 0 | current live helper matches C bits; exact stereo costs 146% versus e6 reassociation; original asm comparison uses a separate fixture and toolchain measurement |
| 4 | `expRotation1PassNeon` | arm64 | `internal/celt/exp_rotation_simd_arm64.go`; `internal/celt/exp_rotation_default.go` | archsimd / scalar | old asm → Go → SIMD: len32/stride1 284.3 (283.6–289.3) → 286.9 (285.8–288.9) → 282.6 (280.6–284.3); len64/stride1 583.2 (581.0–584.1) → 579.7 (574.3–584.2) → 578.2 (574.5–592.8); len32/stride2 138.1 (136.6–145.2) → 148.8 (142.6–152.1) → 159.5 (155.8–167.2); len32/stride4 85.03 (84.45–85.53) → 80.54 (80.13–83.10) → 84.20 (83.98–84.38) | 0 | measured; SIMD near assembly except stride2 slower |
| 5 | `haar1Stride1NEON` | arm64 | `internal/celt/haar1_simd_arm64.go`; `internal/celt/haar1_neon_default.go` | archsimd / scalar | N=32, old asm → Go → SIMD: 9.865 (9.791–14.28) → 14.37 (14.34–14.47) → 8.095 (8.057–8.133) | 0 | measured; SIMD 18% faster than asm median; asm range is noisy |
| 6 | `haar1Stride2NEON` | arm64 | `internal/celt/haar1_simd_arm64.go`; `internal/celt/haar1_neon_default.go` | archsimd / scalar | N=32, old asm → Go → SIMD: 18.73 (14.25–20.18) → 25.88 (25.56–26.26) → 10.59 (10.55–10.80) | 0 | measured; SIMD 43% faster than asm median; asm range is noisy |
| 7 | `haar1Stride4NEON` | arm64 | `internal/celt/haar1_simd_arm64.go`; `internal/celt/haar1_neon_default.go` | archsimd / scalar | original direct N=32 old asm → scalar Go → Go SIMD: 14.47 (14.40–14.63) → 41.43 (41.35–41.53) → 17.70 (17.64–17.72); refined Go-only paired direct N=32: 11.69 → 11.14 median; live wrapper n0=32 (helper groups=16): 11.25 → 8.00 median | 0 | refined SIMD improves 4.7% on the direct N=32 fixture and 29% on the live wrapper fixture; exact parity, full CELT modes, and focused checkptr pass; asm comparison is from an earlier run |
| 8 | `imdctPostRotateF32FromKiss` | arm64 | `internal/celt/imdct_post_kiss_simd_arm64.go`; `internal/celt/imdct_post_kiss_default.go` | archsimd / scalar | N=120: old asm → Go → SIMD: 57.00 (56.43–60.80) → 56.72 (56.41–57.17) → 57.09 (56.93–57.23) | 0 | measured; SIMD within 0.2% of asm |
| 9 | `imdctPreRotateFMA32Kiss` | arm64 | `internal/celt/imdct_pre_kiss_simd_arm64.go`; `internal/celt/imdct_pre_kiss_default.go` | archsimd / scalar | N=120: old asm 19.28 (19.19–19.31) → scalar Go 74.08 (73.89–74.25; earlier Go 1.27.1 run) → Go SIMD 20.41 (20.40–20.49) | 0 | measured on M4 with Go 1.27.0; Go SIMD is 5.9% slower than asm and 23% faster than the first SIMD port |
| 10 | `imdctTDACWindowFMA32` | arm64 | `internal/celt/imdct_tdac_simd_arm64.go`; `internal/celt/imdct_tdac_default.go` | archsimd / scalar | overlap=120/count=60: old asm → Go → SIMD: 24.96 (24.95–25.44) → 95.45 (95.02–96.67) → 25.22 (25.00–25.43) | 0 | measured; SIMD within 1.0% of asm |
| 11 | `celtInnerProd8FMA32` | arm64 | `internal/celt/inner_prod_fma_simd_arm64.go`; `internal/celt/inner_prod_fma_simd_amd64.go`; `internal/celt/inner_prod_fma_default.go` | archsimd / scalar | N=16: 5.94–6.00 → 3.49; N=64: 20.94–21.00 → 6.13–6.43; N=176: 56.24–56.30 → 19.96–20.04 | 0 | measured; faster on M4 |
| 12 | `celtInnerProdSSEStyleAsm` | amd64 | `internal/celt/innerprod_sse_simd_amd64.go`; `internal/celt/innerprod_sse_default.go` | archsimd / scalar | N=480, EPYC 7763 old asm → scalar Go → Go SIMD: 103.6 (103.5–103.9) → 450 (448.9–450.9) → 110 (109.9–110.3) | 0 | run 36253587780 at 036c4d51; SIMD takes 6.2% more time than asm |
| 13 | `kfBfly4M1Core` | arm64 | `internal/celt/kf_bfly_simd_arm64.go`; `internal/celt/kf_bfly4m1_default.go` | archsimd / scalar | N=128: old asm 103–105, scalar Go 161–166, prior SIMD 157–162 in original paired run; refined SIMD 120.9 median versus prior SIMD 178.0 median in seven paired 500 ms samples | 0 | refined SIMD is 32% faster than prior SIMD in its paired run and about 16% slower than the recorded asm baseline; exact bits, zero alloc, full CELT modes, and focused checkptr pass |
| 14 | `kfBfly5Inner` | amd64 | `internal/celt/kf_bfly_simd_amd64.go`; `internal/celt/kf_bfly_default.go` | archsimd / scalar | m=8, N=4, EPYC 7763 old asm → scalar Go → Go SIMD: 475.1 (475–476.2) → 753.7 (746.7–754.4) → 160.5 (159.7–160.8) | 0 | run 36253587780 at 036c4d51; SIMD takes 66.2% less time than asm; native kernel and zero-allocation checks pass |
| 15 | `kfBfly3Inner` | amd64 | `internal/celt/kf_bfly_simd_amd64.go`; `internal/celt/kf_bfly_default.go` | archsimd / scalar | m=8, N=4, EPYC 7763 old asm → scalar Go → Go SIMD: 254.4 (254–254.8) → 286.5 (286.5–286.7) → 60.69 (60.66–60.76) | 0 | run 36253587780 at 036c4d51; SIMD takes 76.1% less time than asm; native kernel and zero-allocation checks pass |
| 16 | `kfBfly4Inner` | amd64 | `internal/celt/kf_bfly_simd_amd64.go`; `internal/celt/kf_bfly_default.go` | archsimd / scalar | m=8, N=4, EPYC 7763 old asm → scalar Go → Go SIMD: 244.3 (244.2–245) → 366.8 (365.9–368.5) → 82.13 (82.03–82.28) | 0 | run 36253587780 at 036c4d51; SIMD takes 66.4% less time than asm; native kernel and zero-allocation checks pass |
| 17 | `kfBfly5Inner` | arm64 | `internal/celt/kf_bfly_simd_arm64.go`; `internal/celt/kf_bfly_default.go` | archsimd / scalar | m=8, N=4 paired M4: old asm 103.9–105.9 → scalar Go 183.0–188.2 → Go SIMD 71.3–73.4 ns/op | 0 | SIMD is about 31% faster than asm and 2.6× faster than scalar Go; exact old-asm/FMA parity and zero-alloc checks pass |
| 18 | `kfBfly3Inner` | arm64 | `internal/celt/kf_bfly_simd_arm64.go`; `internal/celt/kf_bfly_default.go` | archsimd / scalar | m=8, N=4 paired M4: old asm 70.4–72.1 → scalar Go 75.3–75.6 → Go SIMD 34.1–36.0 ns/op | 0 | SIMD is about 50% faster than asm and 2.1× faster than scalar Go; exact old-asm/FMA parity and zero-alloc checks pass |
| 19 | `kfBfly4Inner` | arm64 | `internal/celt/kf_bfly_simd_arm64.go`; `internal/celt/kf_bfly_default.go` | archsimd / scalar | m=8, N=4 paired M4: old asm 70.5–71.1 → scalar Go 89.2–90.7 → Go SIMD 43.9–45.0 ns/op | 0 | SIMD is about 37% faster than asm and 2.0× faster than scalar Go; exact old-asm/FMA parity and zero-alloc checks pass |
| 20 | `l1AbsSumNeon` | arm64 | `internal/celt/l1_abs_sum_simd_arm64.go`; `internal/celt/l1_abs_sum_default.go` | archsimd / scalar | N=480: old asm → Go → SIMD: 123.2 (122.4–123.3) → 493.0 (475.0–529.3) → 50.59 (50.48–50.78) | 0 | measured; SIMD 59% faster than asm, scalar Go 4.0× slower |
| 21 | `mdctFold1StoreNeon` | arm64 | `internal/celt/mdct_fold_simd_arm64.go`; `internal/celt/mdct_fold_default.go` | archsimd / scalar | n4=64, blocks=8: old asm 36.69 (36.57–36.81) → scalar Go 153.6 (153.4–153.7; earlier Go 1.27.1 run) → Go SIMD 37.18 (37.02–37.28) | 0 | measured on M4 with Go 1.27.0; SIMD is 1.3% slower than asm and about 30% faster than the first SIMD port |
| 22 | `mdctFold3StoreNeon` | arm64 | `internal/celt/mdct_fold_simd_arm64.go`; `internal/celt/mdct_fold_default.go` | archsimd / scalar | n4=64, blocks=8: old asm 36.67 (36.65–38.30) → scalar Go 154.9 (154.8–155.5; earlier Go 1.27.1 run) → Go SIMD 37.35 (37.15–38.31) | 0 | measured on M4 with Go 1.27.0; SIMD is 1.9% slower than asm and about 30% faster than the first SIMD port; samples include one outlier per mode |
| 23 | `mdctMidFoldStoreNeon` | arm64 | `internal/celt/mdct_mid_fold_simd_arm64.go`; `internal/celt/mdct_mid_fold_default.go` | archsimd / scalar | n4=64, blocks=8 paired M4 Go 1.27.0: old asm 14.67 (14.58–15.38) → prior SIMD 15.56 (15.54–15.75) → packed SIMD 14.57 (14.50–14.69); scalar Go 109.4 (109.3–109.6) in an earlier fixture | 0 | packed SIMD is 6.4% faster than prior SIMD and at assembly speed; exact old-asm comparison, zero-alloc, and checkptr level 2 pass |
| 24 | `mdctPostTwiddleNeon` | arm64 | `internal/celt/mdct_post_twiddle_simd_arm64.go`; `internal/celt/mdct_post_twiddle_default.go` | archsimd / scalar | n4=64, pairBlocks=8: old asm median 12.71 (run medians 12.69–12.84) → Go SIMD 14.65 (14.64–14.72); prior Go SIMD 15.48 (15.39–15.79); scalar Go 103.4 (103.3–103.9; earlier Go 1.27.1 run) | 0 | measured on M4 with Go 1.27.0; Go SIMD is 15% slower than asm and about 5% faster than the prior SIMD loop; exact and zero-alloc checks pass |
| 25 | `xcorrKernelAVX8` | amd64 | `internal/celt/pitch_xcorr_kernel_simd_amd64.go`; `internal/silk/pitch_xcorr_kernel_simd_amd64.go`; `internal/celt/pitch_xcorr_tiny_simd_amd64.go` | archsimd / scalar | EPYC 7763 old asm → scalar Go → Go SIMD: CELT coarse 240×360: 3,103 → 33,513 → 8,879; half 480×64: 1,044 → 11,873 → 3,255; tiny 5×244: 338.3 → 753.9 → 682.8; tiny 10×10: 34.85 → 54.89 → 144.1 | 0 | run 36253587780 at 036c4d51; provisional finite timings: CELT coarse 240×360 takes 2.9× asm time, half 480×64 takes 3.1× asm time, tiny 5×244 takes 2.0× asm time, tiny 10×10 takes 4.1× asm time; independent C raw-bit cases fail |
| 26 | `prefilterDualInnerProdAsm` | arm64 | `internal/celt/prefilter_dual_inner_prod_simd_arm64.go`; default and nosimd variants | archsimd / scalar | N=240, old asm → Go → SIMD: 85.35 (85.08–86.27) → 237.0 (236.7–238.7) → 41.24 (41.10–41.56) | 0 | measured; SIMD 52% faster than asm; scalar Go 2.8× slower |
| 27 | `pvqSearchPulseLoopAVX` | amd64 | `internal/celt/pvq_search.go`; `internal/celt/pvq_search_default.go` | scalar Go | EPYC 7763 direct pulse-loop old asm → scalar Go → SIMD build: 614.7 (612.9–615.9) → 961.8 (960.3–963.3) → 911.1 (910–911.9); production full search: 624.7 (624.4–626) → 1,087 (1,086–1,089) → 599.5 (598.5–600.1) | 0 | run 36253587780 at 036c4d51; production full search takes 4.0% less time than asm; direct scalar pulse helper takes 48.2% more time than asm and is not selected by AMD64 SIMD dispatch |
| 28 | `pvqSearchPulseLoop` | arm64 | `internal/celt/pvq_search.go`; `internal/celt/pvq_search_default.go` | scalar Go | original N=48, pulses=16 old asm → Go → SIMD build: 552.6 (516.9–567.2) → 997.9 (964.7–1,006) → 1,013 (994.8–1,024); live-shaped Go-only paired scalar loop 705.0 (702.5–728.0) → two-position unroll 522.9 (518.5–527.3) | 0 | unroll is 25.8% faster than scalar on the production-shaped fixture; exact scan order, libopus parity, zero alloc, full CELT modes, and checkptr pass; asm comparison uses a different fixture |
| 29 | `x86RcpApprox4` | amd64 | `internal/celt/pvq_search_x86_sse2.go` | archsimd | four varying lanes, EPYC 7763 old asm → Go SIMD: 2.808 (2.806–2.813) → 1.561 (1.558–1.566) | 0 | run 36253587780 at 036c4d51; direct SIMD helper takes 44.4% less time than asm; no ordinary-Go direct equivalent |
| 30 | `x86PVQSearchBestIDSSE2` | amd64 | `internal/celt/pvq_search_x86_sse2.go` | archsimd | N=48, varying data, EPYC 7763 old asm → Go SIMD: 26.58 (26.5–26.74) → 29.33 (29.28–29.46) | 0 | run 36253587780 at 036c4d51; direct SIMD helper takes 10.3% more time than asm; no ordinary-Go direct equivalent; production full-search timing is in row 27 |
| 31 | `scaleFloat32IntoNEON` | arm64 | `internal/celt/scale_into_simd_arm64.go`; `internal/celt/scale_into_default.go` | archsimd / scalar | old asm → Go → SIMD: N=16 3.286 (3.274–3.301) → 7.656 (7.618–7.791) → 2.716 (2.703–2.719); N=64 7.715 (7.689–7.741) → 27.34 (26.86–29.65) → 5.107 (5.038–5.152); N=176 16.31 (16.18–16.41) → 80.68 (80.36–81.15) → 11.52 (11.40–11.53); N=480 33.87 (33.69–34.02) → 200.7 (200.1–201.0) → 26.73 (26.27–26.77) | 0 | measured; SIMD 17–34% faster than asm, scalar Go 2.3–5.0× slower |
| 32 | `stereoMergeRescaleNEON` | arm64 | `internal/celt/stereo_merge_simd_arm64.go`; `internal/celt/stereo_merge_default.go` | archsimd / scalar | old asm → Go → SIMD: N=16 8.392 (8.366–8.427) → 12.79 (12.66–12.92) → 6.203 (6.169–6.215); N=64 17.61 (17.52–17.70) → 45.57 (45.49–46.16) → 12.05 (11.99–12.07); N=176 31.89 (31.78–31.91) → 121.7 (121.6–122.0) → 27.20 (27.08–27.30); N=480 71.40 (71.05–71.49) → 329.1 (328.5–329.5) → 70.09 (69.60–73.70) | 0 | measured; SIMD 2–32% faster than asm; scalar Go 1.5–4.6× slower |
| 33 | `toneLPCCorrAVXFMA` | amd64 | `internal/celt/tone_lpc_corr_default.go`; `internal/celt/amd64_dispatch_helpers.go` | Go lane helper / scalar | N=480, EPYC 7763 old asm → scalar Go → Go SIMD: 585.5 (585.2–586.1) → 451.6 (451.1–452.7) → 451.7 (449.8–453.6) | 0 | run 36253587780 at 036c4d51; SIMD takes 22.9% less time than asm; the SIMD build selects the same scalar helper |
| 34 | `toneLPCCorr` | arm64 | `internal/celt/tone_lpc_corr_default.go`; `internal/celt/tone_lpc_corr_simd_arm64.go` | archsimd / scalar | cnt=480, delays=1/2: old asm 163.4 (163.0–163.5) → scalar Go 559.0 (558.3–559.5) → Go SIMD 117.7 (117.5–118.0) | 0 | measured on M4; Go SIMD is 28% faster than asm and 79% faster than scalar Go |
| 35 | `xcorrKernel4Float32Neon4Acc` | arm64 | `internal/celt/xcorr_kernel_f32_4acc_simd_arm64.go`; `internal/celt/xcorr_kernel_f32_default.go` | archsimd / scalar | N=480: old asm → Go → SIMD: 189.2 (186.9–190.2) → 699.3 (697.0–699.9) → 167.8 (167.5–167.9) | 0 | timing only: SIMD 11% faster than asm; scalar Go 3.7× slower; live paired C probe exposes four-phase accumulation-order mismatch |
| 36 | `cpuid` | amd64 | Go runtime CPU feature flags used by `simd/archsimd` | startup feature discovery | n/a | n/a | not comparable; the old helper returns raw CPUID registers, while Go dispatch consumes cached feature flags and has no per-call replacement |
| 37 | `xgetbv` | amd64 | Go runtime CPU feature flags used by `simd/archsimd` | startup feature discovery | n/a | n/a | not comparable; the old helper reads OS vector state during initialization, while Go dispatch consumes cached feature flags and has no per-call replacement |
| 38 | `reciprocalEstimate32` | arm64 | `internal/dnnmath/reciprocal_estimate_default.go` | scalar Go | input set of 64 normal float32 values, old asm → Go → SIMD build: 1.405 (1.398–1.428) → 2.021 (1.918–2.285) → 1.932 (1.915–2.308) | 0 | measured; Go emulation is 44% slower than FRECPE asm; SIMD build uses the same scalar routine |
| 39 | `fma32` | arm64 | `internal/lpcnetplc/fma32_arm64.go`; `internal/lpcnetplc/fma32_default.go` | Go float32 expression / scalar | 64 varying input triples, old asm → Go → SIMD build: 1.915 (1.913–1.916) → 0.5506 (0.5505–0.5517) → 0.5491 (0.5489–0.5509) | 0 | measured; Go FMADD expression is 3.5× faster than the out-of-line asm call |
| 40 | `gruFMA32` | arm64 | `internal/osce/lace/gru_fma_arm64.go`; `internal/osce/lace/gru_fma_default.go` | Go float32 expression / scalar | 64 varying input triples, old asm → Go → SIMD build: 1.914 (1.912–1.916) → 0.5497 (0.5494–0.5504) → 0.5502 (0.5488–0.5515) | 0 | measured; Go FMADD expression is 3.5× faster than the out-of-line asm call |
| 41 | `floatToInt16ScaledCore` | arm64 | `internal/silk/convert_simd_arm64.go`; `internal/silk/float_to_int16_default.go` | archsimd / scalar | N=480, scale 1: old asm 23.33 (22.97–23.59) → prior Go SIMD 34.65 (34.32–35.32) → tuned SIMD 27.16 (26.55–27.47). Scale 32768: old asm 23.36 (22.89–23.72) → prior Go SIMD 34.66 (34.24–35.26) → tuned SIMD 34.92 (34.26–35.65). Scalar Go 489.8 (484.9–513.3; earlier Go 1.27.1 fixture). | 0 | paired M4 Go 1.27.0; live pitch path at scale 1 is 22% faster than prior Go and 16% slower than asm; scale 32768 has no measured gain; exact and zero-alloc checks pass |
| 42 | `innerProductFLPAVX2` | amd64 | `internal/silk/inner_product_flp_simd_amd64.go`; `internal/silk/inner_product_flp_amd64.go` | archsimd / scalar | N=480, EPYC 7763 old asm → scalar Go → Go SIMD: 97.57 (97.44–97.64) → 252.3 (252–253) → 194.4 (194–195.5) | 0 | run 36253587780 at 036c4d51; SIMD takes 99.2% more time than asm; native object retains each benchmark call despite discarded return; observable-output remeasurement remains useful |
| 43 | `innerProductFLPArm64` | arm64 | `internal/silk/inner_product_flp_arm64.go` | scalar Go | original N=480 old asm → Go → SIMD build: 126.6 (126.5–126.7) → 205.5 (205.3–212.8) → 201.6 (200.8–211.3); refined Go-only paired prior loop 135.6 → bounds-hoisted loop 107.1 median | 0 | refined Go is 21% faster on the paired fixture; exact bits, zero alloc, full SILK modes, and checkptr pass; asm comparison uses a different fixture |
| 44 | `writeInt16AsFloat32Core` | arm64 | `internal/silk/convert_simd_arm64.go`; `internal/silk/int16_float32_default.go` | archsimd / scalar | N=480: old asm 28.81 (28.55–29.86) → prior Go SIMD 33.68 (33.63–34.56) → tuned Go SIMD 24.00 (23.76–24.10); scalar Go 216.4 (216.1–217.3; earlier Go 1.27.1 fixture) | 0 | paired M4 Go 1.27.0; tuned SIMD is 17% faster than asm and 29% faster than prior SIMD; exact float bits and zero allocations |
| 45 | `synthesizeLPCOrder16Core` | arm64 | `internal/silk/lpc_synth_simd_arm64.go`; `internal/silk/lpc_synth_default.go` | archsimd / scalar | subframe=80: original paired M4 old asm 250.0 (228.9–250.5) → Go SIMD 341.9 (340.8–343.5); refined Go-only paired current SIMD 290.4–292.1 → refined SIMD 253.4–255.1; scalar Go 391.9 (387.7–394.4) in a separate run | 0 | refined SIMD is about 13% faster than prior SIMD on the paired fixture and close to the recorded asm baseline; exact parity, zero alloc, full SILK modes, and checkptr level 2 pass |
| 46 | `celtPitchXcorrFloatImplASM` | arm64 | `internal/silk/pitch_xcorr_impl_simd_arm64.go`; `internal/silk/pitch_xcorr_impl_default.go` | archsimd / scalar | length=240, maxPitch=120: old asm 3,690 (3,665–3,733) → tuned Go SIMD production 2,483 (2,461–2,496); direct SIMD 2,487 (2,473–2,492); prior SIMD 4,892 (4,868–4,910); scalar Go 13,188 (13,043–13,237; earlier fixture) | 0 | paired M4 Go 1.27.0; tuned SIMD is 33% faster than asm and 49% faster than prior SIMD; exact per-lag bits and zero allocations |
| 47 | `xcorrKernelAVX8` | amd64 | `internal/silk/pitch_xcorr_kernel_simd_amd64.go`; `internal/silk/pitch_xcorr_kernel_avx_amd64.go` | archsimd / scalar | SILK pitch search 120×300, EPYC 7763 old asm → scalar Go → Go SIMD: 1,748 (1,743–1,751) → 16,707 (16,696–16,788) → 6,839 (6,697–6,848) | 0 | run 36253587780 at 036c4d51; SIMD takes 291.2% more time than asm; native finite-input and allocation checks pass; exceptional-input arithmetic remains unresolved |
| 48 | `firInterpol21846Core` | arm64 | `internal/silk/resample_fir_simd_arm64.go`; `internal/silk/resample_fir_default.go`; `internal/silk/resample_libopus.go` | archsimd / scalar | nOut=240: old asm 107.56 (105.68–148.66) → scalar Go 280.39 (275.17–287.17) → Go SIMD 115.73 (111.11–119.72) | 0 | paired M4 Go 1.27.0; SIMD is 2.4× faster than scalar Go and 7.6% slower than asm; exact and zero-alloc checks pass |
| 49 | `firInterpol32768Core` | arm64 | `internal/silk/resample_fir_simd_arm64.go`; `internal/silk/resample_fir_default.go`; `internal/silk/resample_libopus.go` | archsimd / scalar | nOut=240: old asm 122.9 (122.5–124.7) → scalar Go 293.8 (293.2–296.0) → Go SIMD production 114.2 (113.2–115.6); direct SIMD core 113.5 (111.5–114.2) | 0 | paired M4 Go 1.27.0; production Go SIMD is 7% faster than asm and 2.6× faster than scalar Go; exact and zero-alloc checks pass |
| 50 | `firInterpol43691Core` | arm64 | `internal/silk/resample_fir_simd_arm64.go`; `internal/silk/resample_fir_default.go`; `internal/silk/resample_libopus.go` | archsimd / scalar | nOut=240: old asm 113.5 (113.2–114.5) → scalar Go 307.0 (304.7–308.7) → Go SIMD production 105.9 (105.7–106.5); direct SIMD core 106.2 (105.1–106.2) | 0 | paired M4 Go 1.27.0; production Go SIMD is 6.7% faster than asm and 2.9× faster than scalar Go; exact and zero-alloc checks pass |
| 51 | `up2HQCore` | arm64 | `internal/silk/up2hq_core_default.go`; `internal/silk/resample_libopus.go` | scalar Go | N=240: old asm → Go → SIMD build: 1,120 (1,096–1,176) → 1,133 (1,126–1,137) → 1,166 (1,154–1,171) | 0 | measured; scalar and SIMD Go are within 4% of asm |
| 52 | `convertFloat32ToInt16UnitBlocks` | arm64 | `pcm_convert_simd_arm64.go`; `pcm_convert_arm64_nosimd.go` | archsimd / scalar | n=480, paired M4 Go 1.27.0: old asm 63.13 (62.17–63.76) → prior SIMD 74.63 (73.88–75.71) → tuned SIMD 60.66 (59.99–61.23); scalar Go 630.1 (618.2–700.6) in an earlier fixture | 0 | tuned SIMD is 3.9% faster than asm and 18.7% faster than prior SIMD; libopus, invalid-lane, unaligned-slice, and zero-alloc checks pass |
| 53 | `convertFloat32ToInt16SaturatingBlocks` | arm64 | `pcm_convert_simd_arm64.go`; `pcm_convert_arm64_nosimd.go` | archsimd / scalar | n=480: old asm 52.31 (52.15–52.60); tuned Go SIMD 53.04 (52.23–53.90); original Go SIMD 102.7; scalar Go 646.5 (639.8–658.0) | 0 | measured; tuned SIMD is within 1.4% of asm and 48% faster than the first Go SIMD port |

## Native AMD64 parity and quality comparison

Run [36248080529](https://github.com/thesyncim/gopus/actions/runs/36248080529)
at `e6f2b332` passes all 19 strict CBR cases in ordinary, SIMD and `nosimd`:
zero packet differences and zero final-range differences out of 2,175 per
mode. The per-frame encode differential sweep passes. The dedicated Hybrid
SWB stereo decode reproducer also passes. The full SIMD suite has 1,819 failing
leaf cases, including the different stereo PCM mismatch and the stateful,
multistream, projection and exceptional-float cases listed above.

The native A/B job remains red. Its coverage comparison reports 105 baseline
sub-48 kHz names absent because the names carry corrected 2.5/5 ms durations,
and one fixture fallback skipped. These are explicit evidence gaps; a baseline
failure or equal failure count is not a correctness result. Fixture-honesty
and raw-bit xcorr failures remain visible and are not waived.

## Measurement follow-up

The 53-row inventory retains each measured revision and fixture. All 51
comparable routines have direct allocation measurements; startup CPU helpers
are not comparable per-call operations. The paired ARM64
deemphasis/decode measurements cover the current recurrence; other ARM64 rows
retain their recorded revisions and fixtures.

On the current native AMD64 run, long CELT and SILK xcorr, tiny xcorr, SILK
float inner product, and the direct PVQ best-ID helper remain optimization
targets. Xcorr requires exact exceptional-input arithmetic before its timings
can support a complete correctness claim. The public encode/decode figures
above measure the entire path with zero steady-state allocations.
