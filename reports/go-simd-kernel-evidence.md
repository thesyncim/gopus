# Go kernel replacement evidence

All 53 symbols from the 41 assembly files at baseline `8ac93c85` have Go
replacements. No tracked `.s` or `.S` files remain. Go 1.27.0 is the minimum.
The inventory below covers every symbol, including two startup CPU helpers
without a comparable per-call replacement.

## Reference contract

- Ordinary builds and `-tags nosimd` use scalar Go. `GOEXPERIMENT=simd`
  selects `simd/archsimd` kernels where implemented, with scalar fallbacks.
- The required oracle contract pairs Go with libopus 1.6.1 on the same CPU,
  using the same scalar/SIMD dispatch, feature flags, input, controls, and scalar
  widths. The codebase audit tracks remaining gaps in legacy test coverage.
  Exact gates compare packets, counts, final ranges, and PCM bits without
  architecture-based numerical allowances.
- AMD64 SIMD references use SSE/AVX2 RTCD; the recorded native helper reports
  `opus_select_arch=4` (AVX2). ARM64 SIMD references bind NEON at compile time;
  their zero runtime arch value does not indicate scalar arithmetic.
- Scalar references disable assembly, intrinsics, and compiler vectorization.
  Native measurements use GCC 13.3.0; local ARM64 uses Apple clang 21.0.0.
  Archive/header/compiler stamps and effective dispatch are checked.
- Quality gates remain separate from exact gates. A quality-only pass does not
  establish byte or sample equality. Frozen fixtures retain their producer
  provenance; live expectations use the selected C build.

## Current correctness status

**Complete codec/extension parity is not yet proven.** The remaining local
float-QEXT witness is a mono multi-frame packet: the second frame differs by
one float32 bit at sample 50. The stereo extension-budget correction passes
its spectrum, synthesis and PCM regression, while the mono witness remains
under investigation. ARM64 SIMD Q30 stereo-angle arithmetic matches the
selected C primitive after the product-rounding correction at `3f1f4ab1`.

A 300-frame SILK WB mono CBR soak exposes an encoder packet mismatch starting
at frame 91 in both scalar and SIMD. Its matching-input/control trace is under
investigation; decoded PCM for the C packets remains exact. A persistent
Hybrid-prime → PLC sequence also differs at sample 1 despite identical prime
PCM and final range. A strict malformed multistream sweep exposes fixed-point
PCM differences where a feature-based bypass omits comparison. These cases are
not covered by the passing short CBR matrix below.

The decoder audit requires exact PCM equality alongside waveform-quality
checks. Each public output format uses its corresponding C API and matching
feature/ISA build. API-rate, int16 PLC and int24 gates pass the tested scalar
and SIMD lanes; fixed output is not inferred by converting C float output.
The no-LBRR FEC correction at `31119cba` preserves packet-driven SILK state
and passes exact loss/recovery and warm zero-allocation regressions.

Native early artifact `10983122541` from
[run 36444790749](https://github.com/thesyncim/gopus/actions/runs/36444790749)
at `d9d2c07a` contains 94 exit records: 90 succeed; three SIMD OSCE phases and
the aggregate status fail. BWE mono/stereo pass. Five LACE/NoLACE sample cases
fail in OSCE, OSCE+QEXT and DRED+OSCE+QEXT; their native SIMD arithmetic is under
investigation. The selected-correlation fix at `31903086` passes local ARM64
exact forward/public-output and zero-allocation gates; native AMD64 validation
is pending. Runs at `a8283fd7` and `949cfc32` are superseded. The current
[run 36456014367](https://github.com/thesyncim/gopus/actions/runs/36456014367)
at `905eec03` includes the correlation correction and the documentation contract
fix: all completed jobs pass, including the build matrix and macOS; the native
SIMD job remains in progress at this checkpoint. It does not include all subsequent local changes.
Early artifacts do not constitute a full CI pass.

The [codebase parity audit](parity-evidence-audit.md) separates confirmed runtime
witnesses from oracle and assertion gaps, with validation status for each.

### Verified local coverage

The following results use live, matching references on ARM64 with Go 1.27.1.
They describe their explicit cases and revisions, not all possible inputs.

| Surface | Coverage | Result |
|---|---|---|
| Fixed multistream decode | 3,024 surround, 400 discrete, 144 projection and 72 Go-encoded cases; integer Hybrid PLC, redundancy, multi-frame and degenerate packets | All 3,640 cases pass at `1e3edd99` in fixed scalar and fixed+QEXT scalar/SIMD |
| Fixed projection encode | Q15 stereo width, integer mode thresholds, five rates, full projection packet sweep | Exact state, packets and ranges in fixed/fixed+QEXT scalar/SIMD; zero warm allocations at `1e862928` |
| Hybrid-to-CELT transitions | Five API rates, mono/stereo, 0/±3 dB | All 30 cases exact in four fixed lanes; zero allocations at `6aefe867` |
| Multistream mode transitions and recovery | Every sample including 5 ms crossfades; 17 PLC/FEC/handover cases | Exact in all eight default/fixed/QEXT scalar/SIMD lanes at `eee85f70` |
| Hybrid public float decode | Mono/stereo, FB/SWB, six bitrates; at least one Hybrid packet required per cell | Exact in all eight default/fixed/QEXT scalar/SIMD lanes at `eee85f70`; warm caller-buffer allocation check passes |
| Malformed single-stream decode | All accepted float32/int16 samples and raw int24 outputs; no magnitude carve-out in exact comparison | Six focused feature/ISA lanes and the full default scalar public suite pass at `c0d68c52` |
| FEC packet oracle identity | Public feature archive and matching private-header configuration | Four exact packet selectors pass all eight default/fixed/QEXT scalar/SIMD lanes at `79685ce8` |
| Loss after CELT redundancy | 24/48 kHz, 10/20 ms, mono/stereo, three output formats, gains, consecutive loss and recovery | Exact in all eight float/fixed/QEXT scalar/SIMD lanes at `f7453894` |
| Low-rate fixed Hybrid | 8/12 kHz received, loss and recovery | Exact fixed/fixed+QEXT scalar/SIMD output with zero warm allocations |
| Strict encoder packets | CELT 19 case/signal pairs; CBR 19 cases / 2,175 packets; FEC 24 configurations × 3 signals | Scalar/SIMD pass without residual waivers at `4cb8015c` |
| CVBR public/CLI encode | Matching float API and CLI int24 input, including zero-padded EOF frame | Packet bytes, sizes and ranges pass scalar/SIMD; fixed+QEXT SIMD also passes at `84d1aa38` |
| CLI decode conformance | Matching opus_decode_float and opus_decode24 APIs across the CLI configuration matrix | Every decoded float bit matches in scalar, SIMD and fixed+QEXT SIMD at `af0c9198` |
| Multi-frame DTX encode | 945 speech/fade/duration configurations, silence and recovery | All packet bytes match selected C in scalar/SIMD at `af0c9198` |
| DRED latent traces | Initial mono/stereo 20/40/60 ms, long-frame cadence and model reload | Exact state and latent bits pass DRED and combined DRED+OSCE+QEXT scalar/SIMD at `036cc257` |
| Fixed-QEXT encoder transitions | 2,988 configurations × 40 frames | Scalar/SIMD packet/range gates pass |
| CELT/QEXT primitives | PVQ grid, QEXT extension-band content, native 96 kHz MDCT | Exact packet/float-bit gates pass scalar/SIMD at `e250d4af` |
| Native 96 kHz QEXT | CELT 2.5/5/10/20 ms; mono/stereo; float32/int16/int24; reset, PLC and Hybrid transitions | Focused paired float/fixed-QEXT gates pass scalar/SIMD |
| DRED+QEXT multistream | 96-frame 5.1, 7.1 and first-order ambisonic sequences; reset and simultaneous extensions | Exact packets/ranges and zero warm allocations in scalar/SIMD |
| DRED+OSCE+QEXT reference | Feature/ISA identity and decoder state layout including QEXT history | Warm-up gates pass both lanes; full public SIMD suite passes at `69a94bce` |
| Malformed multi-frame decode | Valid → malformed → valid → PLC → valid; mono/stereo, three formats, padding and frame overruns | All 24 combinations preserve C status, PCM, ranges and recovery; zero allocations |
| Custom modes | Float/fixed, generated geometries, controls, PLC, recovery and reset | Selected-C matrices pass subject to the explicit C undefined-behavior boundary below |
| Allocation and type discipline | Caller-owned encode/decode buffers, extension scratch, fixed integer composition | Warm allocation gates pass; type guard retains 27 recorded findings with no added debt |

Full package results provide broader regression coverage alongside these exact
gates. The root public suite passes default scalar/SIMD, fixed SIMD, fixed+QEXT
SIMD (388.052 s), custom+QEXT SIMD (260.261 s), and DRED+OSCE+QEXT SIMD
(315.382 s) at their recorded checkpoints. The full fixed+QEXT multistream
package passes scalar (90.343 s) and SIMD (86.055 s) at the `6aefe867`
transition checkpoint. Later `f7453894` transition coverage is focused; a fresh
final revision run is still required. A full suite pass does not convert its
quality-only assertions into exactness proof.

### Correctness cost outside the assembly inventory

The ARM64 transient-analysis recurrence uses the selected C two-state update.
An algebraically reduced recurrence changes float32 rounding. On M4 Max,
Go 1.27.1 SIMD, five 750 ms samples compare the two forms: median 5 ms-frame
analysis is 2,046 → 2,327 ns, and 20 ms analysis is 6,192 → 7,013 ns
(13.7% and 13.3% more time). Both allocate zero bytes. This is a focused
primitive comparison; it does not establish an end-to-end regression. Full CELT,
selected-C CBR and short-frame packet gates pass both scalar/SIMD builds.

### C reference boundary

Libopus 1.6.1 accepts a 96 kHz / 2048-sample custom-QEXT geometry whose
stateful history access is invalid under AddressSanitizer. Go retains bounded
history access and safe concealment. Defined first-frame behavior remains
compared with C; subsequent unsafe C behavior is excluded explicitly, with Go
loss/recovery/reset and zero-allocation checks retained. The upstream source
check dated 2026-09-28 finds the same invalid history expression.
See [the reproducer and upstream evidence](libopus-custom-qext-boundaries.md).
The upstream build also rejects fixed-point combined with DRED/OSCE; those
combinations are outside the supported reference configuration.

## End-to-end codec throughput

### Native AMD64 end-to-end measurements

Early artifact `10983122541` from [run 36444790749](https://github.com/thesyncim/gopus/actions/runs/36444790749)
compares assembly `8ac93c85` with SIMD/nosimd `d9d2c07a` on AMD EPYC 7763,
Go 1.27.1, GCC 13.3.0, GOAMD64=v1, PGO enabled. Four interleaved 500 ms
samples use `-cpu=1`; all 72 samples report 0 B/op and 0 allocs/op.
Values are median ns/op. These base-codec measurements do not establish OSCE parity.

| Workload | Old assembly | Go SIMD | `nosimd` |
|---|---:|---:|---:|
| CELT decode | 20,281.5 | 13,545 | 16,668 |
| Hybrid decode | 28,442.5 | 24,367 | 30,701 |
| SILK decode | 22,479 | 16,460 | 21,222.5 |
| Caller-buffer encode | 92,144.5 | 64,915.5 | 100,802 |
| VoIP encode | 98,179.5 | 70,047 | 106,786 |
| Low-delay encode | 91,436.5 | 64,768.5 | 100,178 |

SIMD takes 14.3–33.2% less time than assembly in these six workloads.
Scalar takes less time for CELT/SILK decode and more for Hybrid decode and encode.
The 11 AMD64 kernel rows retain their separate `6c466ded` measurements from
artifact `10979051418`; ARM64 rows retain their own measured revisions.
Absolute timings across different CPU models are not revision comparisons.

### Matched libopus 1.6.1 comparison

Same runner/revision; C scalar vs Go scalar and C SIMD vs Go SIMD. Three
250 ms minimum runs per case. Values are µs/packet (lower is faster).
All Go rows allocate zero; C allocations are not measured.

| Workload | C scalar | Go scalar | C SIMD | Go SIMD |
|---|---:|---:|---:|---:|
| CELT-FB-20ms-stereo-128k | 179.80 | 191.93 | 134.29 | 130.40 |
| CELT-FB-5ms-mono-64k | 19.92 | 23.62 | 18.71 | 19.98 |
| Hybrid-FB-20ms-mono-64k | 358.50 | 358.10 | 254.68 | 281.42 |
| Hybrid-FB-20ms-stereo-96k | 203.08 | 219.22 | 151.69 | 142.33 |
| SILK-WB-20ms-mono-32k | 687.66 | 615.31 | 449.19 | 387.32 |
| RFC vectors Float32 | 30.78 | 32.98 | 28.97 | 27.88 |
| RFC vectors Int16 | 33.97 | 36.42 | 31.47 | 31.29 |

SIMD Go trails SIMD C by 6.8% for short CELT and 10.5% for Hybrid mono encode;
its other encode rows take 2.9–13.8% less time. SIMD vector decode takes
3.7%/0.6% less time for float32/int16. Scalar Go trails C by 6.7%/18.6% for
20/5 ms CELT, 8.0% for Hybrid stereo, and 7.2% for both decode formats.
Decoder rows aggregate 20,075 identical packets; encoder rows use identical PCM
and controls. BWE mono/stereo exactness passes on this runner; five LACE/NoLACE
sample cases still fail. Those OSCE failures are separate from these base-codec
performance measurements.

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
| 3 | `deemphasisStereoPlanarF32Core` | arm64 | `internal/celt/output_helpers.go` (`deemphasisChannel`) | exact scalar recurrence in every build | b36c1c20 live N=480 diagnostic, scalar / SIMD build: mono 714.4 (657.5–976.2) / 1,552 (1,278–2,100); stereo 1,353 (1,350–1,355) / 1,599 (1,431–2,033). Paired contiguous accumulation overlay on 5e14 source: mono N=480 baseline 1,150 (1,150–1,158) → candidate 882.4 (877.5–891.6) ordinary; baseline 1,150 (1,150–1,165) → candidate 884.9 (879.8–887.0) SIMD build | 0 | M4 / Go 1.27.0, five 300 ms paired samples; unchanged recurrence matches C and zero-allocation gates pass; direct improvement is 23.3%/23.1%; public Hybrid results are in the throughput section; no AMD64 gain is inferred |
| 4 | `expRotation1PassNeon` | arm64 | `internal/celt/exp_rotation_simd_arm64.go`; `internal/celt/exp_rotation_default.go` | archsimd / scalar | old asm → Go → SIMD: len32/stride1 284.3 (283.6–289.3) → 286.9 (285.8–288.9) → 282.6 (280.6–284.3); len64/stride1 583.2 (581.0–584.1) → 579.7 (574.3–584.2) → 578.2 (574.5–592.8); len32/stride2 138.1 (136.6–145.2) → 148.8 (142.6–152.1) → 159.5 (155.8–167.2); len32/stride4 85.03 (84.45–85.53) → 80.54 (80.13–83.10) → 84.20 (83.98–84.38) | 0 | measured; SIMD near assembly except stride2 slower |
| 5 | `haar1Stride1NEON` | arm64 | `internal/celt/haar1_simd_arm64.go`; `internal/celt/haar1_scalar.go` | archsimd / scalar | N=32, old asm → Go → SIMD: 9.865 (9.791–14.28) → 14.37 (14.34–14.47) → 8.095 (8.057–8.133) | 0 | measured; SIMD 18% faster than asm median; asm range is noisy |
| 6 | `haar1Stride2NEON` | arm64 | `internal/celt/haar1_simd_arm64.go`; `internal/celt/haar1_scalar.go` | archsimd / scalar | N=32, old asm → Go → SIMD: 18.73 (14.25–20.18) → 25.88 (25.56–26.26) → 10.59 (10.55–10.80) | 0 | measured; SIMD 43% faster than asm median; asm range is noisy |
| 7 | `haar1Stride4NEON` | arm64 | `internal/celt/haar1_simd_arm64.go`; `internal/celt/haar1_scalar.go` | archsimd / scalar | original direct N=32 old asm → scalar Go → Go SIMD: 14.47 (14.40–14.63) → 41.43 (41.35–41.53) → 17.70 (17.64–17.72); refined Go-only paired direct N=32: 11.69 → 11.14 median; live wrapper n0=32 (helper groups=16): 11.25 → 8.00 median | 0 | refined SIMD improves 4.7% on the direct N=32 fixture and 29% on the live wrapper fixture; exact parity, full CELT modes, and focused checkptr pass; asm comparison is from an earlier run |
| 8 | `imdctPostRotateF32FromKiss` | arm64 | `internal/celt/imdct_post_kiss_simd_arm64.go`; `internal/celt/imdct_post_kiss_default.go` | archsimd / scalar | N=120: old asm → Go → SIMD: 57.00 (56.43–60.80) → 56.72 (56.41–57.17) → 57.09 (56.93–57.23) | 0 | measured; SIMD within 0.2% of asm |
| 9 | `imdctPreRotateFMA32Kiss` | arm64 | `internal/celt/imdct_pre_kiss_simd_arm64.go`; `internal/celt/imdct_pre_kiss_arm64_nosimd.go` | archsimd / scalar | N=120: old asm 19.28 (19.19–19.31) → scalar Go 74.08 (73.89–74.25; earlier Go 1.27.1 run) → Go SIMD 20.41 (20.40–20.49) | 0 | measured on M4 with Go 1.27.0; Go SIMD is 5.9% slower than asm and 23% faster than the first SIMD port |
| 10 | `imdctTDACWindowFMA32` | arm64 | `internal/celt/imdct_tdac_simd_arm64.go`; `internal/celt/imdct_tdac_default.go` | archsimd / scalar | overlap=120/count=60: old asm → Go → SIMD: 24.96 (24.95–25.44) → 95.45 (95.02–96.67) → 25.22 (25.00–25.43) | 0 | measured; SIMD within 1.0% of asm |
| 11 | `celtInnerProd8FMA32` | arm64 | `internal/celt/inner_prod_fma_simd_arm64.go`; `internal/celt/inner_prod_fma_simd_amd64.go`; `internal/celt/inner_prod_fma_default.go` | archsimd / scalar | N=16: 5.94–6.00 → 3.49; N=64: 20.94–21.00 → 6.13–6.43; N=176: 56.24–56.30 → 19.96–20.04 | 0 | measured; faster on M4 |
| 12 | `celtInnerProdSSEStyleAsm` | amd64 | `internal/celt/innerprod_sse_simd_amd64.go`; `internal/celt/innerprod_sse_default.go` | archsimd / scalar | N=480, old asm → scalar Go → Go SIMD: 48.94 (48.9–49.1) → 273.8 (273.7–274) → 65.04 (64.85–65.44) | 0 | run 36435145306 / artifact 10979051418; asm 8ac93c85 vs Go 6c466ded; five 300 ms samples, Intel Xeon 6973P-C, Go 1.27.1, GCC 13.3; zero allocations; SIMD takes 32.9% more time than asm |
| 13 | `kfBfly4M1Core` | arm64 | `internal/celt/kf_bfly_simd_arm64.go`; `internal/celt/kf_bfly4m1_default.go` | archsimd / scalar | N=128: old asm 103–105, scalar Go 161–166, prior SIMD 157–162 in original paired run; refined SIMD 120.9 median versus prior SIMD 178.0 median in seven paired 500 ms samples | 0 | refined SIMD is 32% faster than prior SIMD in its paired run and about 16% slower than the recorded asm baseline; exact bits, zero alloc, full CELT modes, and focused checkptr pass |
| 14 | `kfBfly5Inner` | amd64 | `internal/celt/kf_bfly_simd_amd64.go`; `internal/celt/kf_bfly_default.go` | archsimd / scalar | m=8, N=4, old asm → scalar Go → Go SIMD: 1,020 (988.6–1,039) → 473.1 (472.5–473.7) → 110.8 (106–112.3) | 0 | run 36435145306 / artifact 10979051418; asm 8ac93c85 vs Go 6c466ded; five 300 ms samples, Intel Xeon 6973P-C, Go 1.27.1, GCC 13.3; zero allocations; SIMD takes 89.1% less time than asm |
| 15 | `kfBfly3Inner` | amd64 | `internal/celt/kf_bfly_simd_amd64.go`; `internal/celt/kf_bfly_default.go` | archsimd / scalar | m=8, N=4, old asm → scalar Go → Go SIMD: 227 (226.1–265.2) → 199.5 (198.6–200.3) → 46.15 (45.78–47.06) | 0 | run 36435145306 / artifact 10979051418; asm 8ac93c85 vs Go 6c466ded; five 300 ms samples, Intel Xeon 6973P-C, Go 1.27.1, GCC 13.3; zero allocations; SIMD takes 79.7% less time than asm |
| 16 | `kfBfly4Inner` | amd64 | `internal/celt/kf_bfly_simd_amd64.go`; `internal/celt/kf_bfly_default.go` | archsimd / scalar | m=8, N=4, old asm → scalar Go → Go SIMD: 210.3 (195.2–224.7) → 233.7 (233.2–279.4) → 69 (61.99–70.91) | 0 | run 36435145306 / artifact 10979051418; asm 8ac93c85 vs Go 6c466ded; five 300 ms samples, Intel Xeon 6973P-C, Go 1.27.1, GCC 13.3; zero allocations; SIMD takes 67.2% less time than asm |
| 17 | `kfBfly5Inner` | arm64 | `internal/celt/kf_bfly_simd_arm64.go`; `internal/celt/kf_bfly_default.go` | archsimd / scalar | m=8, N=4 paired M4: old asm 103.9–105.9 → scalar Go 183.0–188.2 → Go SIMD 71.3–73.4 ns/op | 0 | SIMD is about 31% faster than asm and 2.6× faster than scalar Go; exact old-asm/FMA parity and zero-alloc checks pass |
| 18 | `kfBfly3Inner` | arm64 | `internal/celt/kf_bfly_simd_arm64.go`; `internal/celt/kf_bfly_default.go` | archsimd / scalar | m=8, N=4 paired M4: old asm 70.4–72.1 → scalar Go 75.3–75.6 → Go SIMD 34.1–36.0 ns/op | 0 | SIMD is about 50% faster than asm and 2.1× faster than scalar Go; exact old-asm/FMA parity and zero-alloc checks pass |
| 19 | `kfBfly4Inner` | arm64 | `internal/celt/kf_bfly_simd_arm64.go`; `internal/celt/kf_bfly_default.go` | archsimd / scalar | m=8, N=4 paired M4: old asm 70.5–71.1 → scalar Go 89.2–90.7 → Go SIMD 43.9–45.0 ns/op | 0 | SIMD is about 37% faster than asm and 2.0× faster than scalar Go; exact old-asm/FMA parity and zero-alloc checks pass |
| 20 | `l1AbsSumNeon` | arm64 | `internal/celt/l1_abs_sum_simd_arm64.go`; `internal/celt/l1_abs_sum_default.go` | archsimd / scalar | N=480: old asm → Go → SIMD: 123.2 (122.4–123.3) → 493.0 (475.0–529.3) → 50.59 (50.48–50.78) | 0 | measured; SIMD 59% faster than asm, scalar Go 4.0× slower |
| 21 | `mdctFold1StoreNeon` | arm64 | `internal/celt/mdct_fold_simd_arm64.go`; `internal/celt/mdct_fold_default.go` | archsimd / scalar | n4=64, blocks=8: old asm 36.69 (36.57–36.81) → scalar Go 153.6 (153.4–153.7; earlier Go 1.27.1 run) → Go SIMD 37.18 (37.02–37.28) | 0 | measured on M4 with Go 1.27.0; SIMD is 1.3% slower than asm and about 30% faster than the first SIMD port |
| 22 | `mdctFold3StoreNeon` | arm64 | `internal/celt/mdct_fold_simd_arm64.go`; `internal/celt/mdct_fold_default.go` | archsimd / scalar | n4=64, blocks=8: old asm 36.67 (36.65–38.30) → scalar Go 154.9 (154.8–155.5; earlier Go 1.27.1 run) → Go SIMD 37.35 (37.15–38.31) | 0 | measured on M4 with Go 1.27.0; SIMD is 1.9% slower than asm and about 30% faster than the first SIMD port; samples include one outlier per mode |
| 23 | `mdctMidFoldStoreNeon` | arm64 | `internal/celt/mdct_mid_fold_simd_arm64.go`; `internal/celt/mdct_mid_fold_default.go` | archsimd / scalar | n4=64, blocks=8 paired M4 Go 1.27.0: old asm 14.67 (14.58–15.38) → prior SIMD 15.56 (15.54–15.75) → packed SIMD 14.57 (14.50–14.69); scalar Go 109.4 (109.3–109.6) in an earlier fixture | 0 | packed SIMD is 6.4% faster than prior SIMD and at assembly speed; exact old-asm comparison, zero-alloc, and checkptr level 2 pass |
| 24 | `mdctPostTwiddleNeon` | arm64 | `internal/celt/mdct_post_twiddle_simd_arm64.go`; `internal/celt/mdct_post_twiddle_default.go` | archsimd / scalar | n4=64, pairBlocks=8: old asm median 12.71 (run medians 12.69–12.84) → Go SIMD 14.65 (14.64–14.72); prior Go SIMD 15.48 (15.39–15.79); scalar Go 103.4 (103.3–103.9; earlier Go 1.27.1 run) | 0 | measured on M4 with Go 1.27.0; Go SIMD is 15% slower than asm and about 5% faster than the prior SIMD loop; exact and zero-alloc checks pass |
| 25 | `xcorrKernelAVX8` | amd64 | `internal/celt/pitch_xcorr_kernel_simd_amd64.go`; `internal/celt/pitch_xcorr_tiny_simd_amd64.go`; scalar defaults | archsimd / scalar | CELT pitch wrappers, old asm → scalar Go → Go SIMD: coarse 240×360: 2,354 (2,349–2,400) → 2.081e+04 (2.078e+04–2.087e+04) → 2,487 (2,484–2,493); half 480×64: 839 (828.9–952.7) → 7,923 (7,444–8,579) → 828.9 (826–842.7); tiny coarse 5×244: 235.5 (234.1–245) → 406.8 (406.1–407.1) → 88.98 (88.96–90.12); tiny coarse PLC 5×244: 234.5 (234.4–235.3) → 463.4 (456.9–474.4) → 89.53 (88.96–99.63); tiny fine 10×10: 22.79 (22.69–26.51) → 32.96 (32.87–33.01) → 15.91 (15.9–15.95); tiny fine PLC 10×10: 23.86 (22.79–25.93) → 27.72 (27.39–30.74) → 15.97 (15.94–16.61) | 0 | run 36435145306 / artifact 10979051418; asm 8ac93c85 vs Go 6c466ded; five 300 ms samples, Intel Xeon 6973P-C, Go 1.27.1, GCC 13.3; zero allocations; workload-specific results, no blanket xcorr claim |
| 26 | `prefilterDualInnerProdAsm` | arm64 | `internal/celt/prefilter_dual_inner_prod_simd_arm64.go`; default and nosimd variants | archsimd / scalar | N=240, old asm → Go → SIMD: 85.35 (85.08–86.27) → 237.0 (236.7–238.7) → 41.24 (41.10–41.56) | 0 | measured; SIMD 52% faster than asm; scalar Go 2.8× slower |
| 27 | `pvqSearchPulseLoopAVX` | amd64 | `internal/celt/pvq_search.go`; `internal/celt/pvq_search_default.go` | scalar Go | Direct helper old asm → scalar Go → SIMD build: 529.2 (524–575.8) → 729.4 (702–782.2) → 658.5 (657.2–671.9); production full search: 548.5 (547.3–570.6) → 773.9 (773.1–776) → 440.2 (439.2–3,128) | 0 | run 36435145306 / artifact 10979051418; asm 8ac93c85 vs Go 6c466ded; five 300 ms samples, Intel Xeon 6973P-C, Go 1.27.1, GCC 13.3; zero allocations; direct pulse helper is not the finite-input production route; full search is the live comparator |
| 28 | `pvqSearchPulseLoop` | arm64 | `internal/celt/pvq_search.go`; `internal/celt/pvq_search_default.go` | scalar Go | original N=48, pulses=16 old asm → Go → SIMD build: 552.6 (516.9–567.2) → 997.9 (964.7–1,006) → 1,013 (994.8–1,024); live-shaped Go-only paired scalar loop 705.0 (702.5–728.0) → two-position unroll 522.9 (518.5–527.3) | 0 | unroll is 25.8% faster than scalar on the production-shaped fixture; exact scan order, libopus parity, zero alloc, full CELT modes, and checkptr pass; asm comparison uses a different fixture |
| 29 | `x86RcpApprox4` | amd64 | `internal/celt/pvq_search_x86_sse2.go` | archsimd | Four varying lanes, old asm → Go SIMD: 1.464 (1.455–1.471) → 1.039 (1.038–1.06); no scalar direct benchmark | 0 | run 36435145306 / artifact 10979051418; asm 8ac93c85 vs Go 6c466ded; five 300 ms samples, Intel Xeon 6973P-C, Go 1.27.1, GCC 13.3; zero allocations |
| 30 | `x86PVQSearchBestIDSSE2` | amd64 | `internal/celt/pvq_search_x86_sse2.go` | archsimd | N=48 varying data, old asm → Go SIMD exact helper: 19.63 (19.58–22.54) → 215.5 (214.6–248.8); no scalar direct benchmark | 0 | run 36435145306 / artifact 10979051418; asm 8ac93c85 vs Go 6c466ded; five 300 ms samples, Intel Xeon 6973P-C, Go 1.27.1, GCC 13.3; zero allocations; exact helper is not the finite-input production route; row 27 is the live comparator |
| 31 | `scaleFloat32IntoNEON` | arm64 | `internal/celt/scale_into_simd.go`; `internal/celt/scale_into_default.go` | archsimd / scalar | old asm → Go → SIMD: N=16 3.286 (3.274–3.301) → 7.656 (7.618–7.791) → 2.716 (2.703–2.719); N=64 7.715 (7.689–7.741) → 27.34 (26.86–29.65) → 5.107 (5.038–5.152); N=176 16.31 (16.18–16.41) → 80.68 (80.36–81.15) → 11.52 (11.40–11.53); N=480 33.87 (33.69–34.02) → 200.7 (200.1–201.0) → 26.73 (26.27–26.77) | 0 | measured; SIMD 17–34% faster than asm, scalar Go 2.3–5.0× slower |
| 32 | `stereoMergeRescaleNEON` | arm64 | `internal/celt/stereo_merge_simd_arm64.go`; `internal/celt/stereo_merge_default.go` | archsimd / scalar | old asm → Go → SIMD: N=16 8.392 (8.366–8.427) → 12.79 (12.66–12.92) → 6.203 (6.169–6.215); N=64 17.61 (17.52–17.70) → 45.57 (45.49–46.16) → 12.05 (11.99–12.07); N=176 31.89 (31.78–31.91) → 121.7 (121.6–122.0) → 27.20 (27.08–27.30); N=480 71.40 (71.05–71.49) → 329.1 (328.5–329.5) → 70.09 (69.60–73.70) | 0 | measured; SIMD 2–32% faster than asm; scalar Go 1.5–4.6× slower |
| 33 | `toneLPCCorrAVXFMA` | amd64 | `internal/celt/tone_lpc_corr_default.go` | scalar Go in both candidate builds | N=480, old asm → ordinary scalar Go → same scalar helper in SIMD build: 444.4 (444.1–468.6) → 262.3 (261.7–263.4) → 280.1 (263–301.3) | 0 | run 36435145306 / artifact 10979051418; asm 8ac93c85 vs Go 6c466ded; five 300 ms samples, Intel Xeon 6973P-C, Go 1.27.1, GCC 13.3; zero allocations; build-mode timing gap remains unexplained; both Go modes call the scalar helper |
| 34 | `toneLPCCorr` | arm64 | `internal/celt/tone_lpc_corr_scalar_arm64.go`; `internal/celt/tone_lpc_corr_simd_arm64.go` | archsimd / scalar | cnt=480, delays=1/2: recorded old asm 163.4 (163.0–163.5); earlier scalar Go 559.0 (558.3–559.5), earlier SIMD 117.7 (117.5–118.0); current exact-order SIMD 1,109 median (803.8–1,287), five 300 ms samples | 0 | M4/Go 1.27.0 current diagnostic is noisy and slower; earlier SIMD timing uses a reduction that fails selected-C parity, so its speedup is not a current claim; all 26 LPC / 12 tone-detection checks pass in three modes, SIMD warm allocations are zero; controlled performance tuning remains |
| 35 | `xcorrKernel4Float32Neon4Acc` | arm64 | `internal/celt/xcorr_kernel_f32_neon_ordered_simd_arm64.go`; `internal/celt/xcorr_kernel_f32_default.go` | archsimd / scalar | N=480 recorded old asm 189.2 (186.9–190.2), scalar Go 699.3 (697.0–699.9), four-phase SIMD 167.8 (167.5–167.9); current ordered SIMD 402.2 (399.1–419.1), five 300 ms samples | 0 | replacement xcorrKernel4Float32NeonOrdered matches all 21 selected-C raw-bit cases with zero warm allocations; earlier four-phase speedup does not apply to exact arithmetic; current M4/Go 1.27.0 diagnostic is separate from the assembly run, and production batching remains optimization work |
| 36 | `cpuid` | amd64 | Go runtime CPU feature flags used by `simd/archsimd` | startup feature discovery | n/a | n/a | not comparable; the old helper returns raw CPUID registers, while Go dispatch consumes cached feature flags and has no per-call replacement |
| 37 | `xgetbv` | amd64 | Go runtime CPU feature flags used by `simd/archsimd` | startup feature discovery | n/a | n/a | not comparable; the old helper reads OS vector state during initialization, while Go dispatch consumes cached feature flags and has no per-call replacement |
| 38 | `reciprocalEstimate32` | arm64 | `internal/dnnmath/reciprocal_estimate_default.go` | scalar Go | input set of 64 normal float32 values, old asm → Go → SIMD build: 1.405 (1.398–1.428) → 2.021 (1.918–2.285) → 1.932 (1.915–2.308) | 0 | measured; Go emulation is 44% slower than FRECPE asm; SIMD build uses the same scalar routine |
| 39 | `fma32` | arm64 | `internal/lpcnetplc/fma32_arm64.go`; `internal/lpcnetplc/fma32_default.go` | Go float32 expression / scalar | 64 varying input triples, old asm → Go → SIMD build: 1.915 (1.913–1.916) → 0.5506 (0.5505–0.5517) → 0.5491 (0.5489–0.5509) | 0 | measured; Go FMADD expression is 3.5× faster than the out-of-line asm call |
| 40 | `gruFMA32` | arm64 | `internal/osce/lace/gru_fma_arm64.go`; `internal/osce/lace/gru_fma_default.go` | Go float32 expression / scalar | 64 varying input triples, old asm → Go → SIMD build: 1.914 (1.912–1.916) → 0.5497 (0.5494–0.5504) → 0.5502 (0.5488–0.5515) | 0 | measured; Go FMADD expression is 3.5× faster than the out-of-line asm call |
| 41 | `floatToInt16ScaledCore` | arm64 | `internal/silk/convert_simd_arm64.go`; `internal/silk/float_to_int16_default.go` | archsimd / scalar | N=480, scale 1: old asm 23.33 (22.97–23.59) → prior Go SIMD 34.65 (34.32–35.32) → tuned SIMD 27.16 (26.55–27.47). Scale 32768: old asm 23.36 (22.89–23.72) → prior Go SIMD 34.66 (34.24–35.26) → tuned SIMD 34.92 (34.26–35.65). Scalar Go 489.8 (484.9–513.3; earlier Go 1.27.1 fixture). | 0 | paired M4 Go 1.27.0; live pitch path at scale 1 is 22% faster than prior Go and 16% slower than asm; scale 32768 has no measured gain; exact and zero-alloc checks pass |
| 42 | `innerProductFLPAVX2` | amd64 | `internal/silk/inner_product_flp_simd_amd64.go`; `internal/silk/inner_product_flp_amd64.go` | archsimd / scalar | N=480, old asm → scalar Go → Go SIMD: 61.14 (61.06–73.46) → 310 (268.7–347.3) → 66.59 (66.21–66.91) | 0 | run 36435145306 / artifact 10979051418; asm 8ac93c85 vs Go 6c466ded; five 300 ms samples, Intel Xeon 6973P-C, Go 1.27.1, GCC 13.3; zero allocations; SIMD takes 8.9% more time than asm in this fixture |
| 43 | `innerProductFLPArm64` | arm64 | `internal/silk/inner_product_flp_arm64.go` | scalar Go | original N=480 old asm → Go → SIMD build: 126.6 (126.5–126.7) → 205.5 (205.3–212.8) → 201.6 (200.8–211.3); refined Go-only paired prior loop 135.6 → bounds-hoisted loop 107.1 median | 0 | refined Go is 21% faster on the paired fixture; exact bits, zero alloc, full SILK modes, and checkptr pass; asm comparison uses a different fixture |
| 44 | `writeInt16AsFloat32Core` | arm64 | `internal/silk/convert_simd_arm64.go`; `internal/silk/int16_float32_default.go` | archsimd / scalar | N=480: old asm 28.81 (28.55–29.86) → prior Go SIMD 33.68 (33.63–34.56) → tuned Go SIMD 24.00 (23.76–24.10); scalar Go 216.4 (216.1–217.3; earlier Go 1.27.1 fixture) | 0 | paired M4 Go 1.27.0; tuned SIMD is 17% faster than asm and 29% faster than prior SIMD; exact float bits and zero allocations |
| 45 | `synthesizeLPCOrder16Core` | arm64 | `internal/silk/lpc_synth_simd_arm64.go`; `internal/silk/lpc_synth_default.go` | archsimd / scalar | subframe=80: original paired M4 old asm 250.0 (228.9–250.5) → Go SIMD 341.9 (340.8–343.5); refined Go-only paired current SIMD 290.4–292.1 → refined SIMD 253.4–255.1; scalar Go 391.9 (387.7–394.4) in a separate run | 0 | refined SIMD is about 13% faster than prior SIMD on the paired fixture and close to the recorded asm baseline; exact parity, zero alloc, full SILK modes, and checkptr level 2 pass |
| 46 | `celtPitchXcorrFloatImplASM` | arm64 | `internal/silk/pitch_xcorr_impl_simd_arm64.go`; `internal/silk/pitch_xcorr_impl_default.go` | archsimd / scalar | length=240, maxPitch=120: old asm 3,690 (3,665–3,733) → tuned Go SIMD production 2,483 (2,461–2,496); direct SIMD 2,487 (2,473–2,492); prior SIMD 4,892 (4,868–4,910); scalar Go 13,188 (13,043–13,237; earlier fixture) | 0 | paired M4 Go 1.27.0; tuned SIMD is 33% faster than asm and 49% faster than prior SIMD; exact per-lag bits and zero allocations |
| 47 | `xcorrKernelAVX8` | amd64 | `internal/silk/pitch_xcorr_kernel_simd_amd64.go`; `internal/silk/pitch_xcorr_impl_simd_amd64.go` | archsimd / scalar | Live celtPitchXcorrFloat wrapper 120×300, old asm → scalar Go → Go SIMD: 1,125 (1,118–1,397) → 1.031e+04 (1.016e+04–1.045e+04) → 1,556 (1,555–1,565) | 0 | run 36435145306 / artifact 10979051418; asm 8ac93c85 vs Go 6c466ded; five 300 ms samples, Intel Xeon 6973P-C, Go 1.27.1, GCC 13.3; zero allocations; SIMD takes 38.3% more time than asm in this fixture |
| 48 | `firInterpol21846Core` | arm64 | `internal/silk/resample_fir_simd_arm64.go`; `internal/silk/resample_fir_default.go`; `internal/silk/resample_libopus.go` | archsimd / scalar | nOut=240: old asm 107.56 (105.68–148.66) → scalar Go 280.39 (275.17–287.17) → Go SIMD 115.73 (111.11–119.72) | 0 | paired M4 Go 1.27.0; SIMD is 2.4× faster than scalar Go and 7.6% slower than asm; exact and zero-alloc checks pass |
| 49 | `firInterpol32768Core` | arm64 | `internal/silk/resample_fir_simd_arm64.go`; `internal/silk/resample_fir_default.go`; `internal/silk/resample_libopus.go` | archsimd / scalar | nOut=240: old asm 122.9 (122.5–124.7) → scalar Go 293.8 (293.2–296.0) → Go SIMD production 114.2 (113.2–115.6); direct SIMD core 113.5 (111.5–114.2) | 0 | paired M4 Go 1.27.0; production Go SIMD is 7% faster than asm and 2.6× faster than scalar Go; exact and zero-alloc checks pass |
| 50 | `firInterpol43691Core` | arm64 | `internal/silk/resample_fir_simd_arm64.go`; `internal/silk/resample_fir_default.go`; `internal/silk/resample_libopus.go` | archsimd / scalar | nOut=240: old asm 113.5 (113.2–114.5) → scalar Go 307.0 (304.7–308.7) → Go SIMD production 105.9 (105.7–106.5); direct SIMD core 106.2 (105.1–106.2) | 0 | paired M4 Go 1.27.0; production Go SIMD is 6.7% faster than asm and 2.9× faster than scalar Go; exact and zero-alloc checks pass |
| 51 | `up2HQCore` | arm64 | `internal/silk/up2hq_core_default.go`; `internal/silk/resample_libopus.go` | scalar Go | N=240: old asm → Go → SIMD build: 1,120 (1,096–1,176) → 1,133 (1,126–1,137) → 1,166 (1,154–1,171) | 0 | measured; scalar and SIMD Go are within 4% of asm |
| 52 | `convertFloat32ToInt16UnitBlocks` | arm64 | `pcm_convert_simd_arm64.go`; `pcm_convert_arm64_nosimd.go` | archsimd / scalar | N=480, b36c1c20 M4/Go 1.27.0: scalar 424.7 / exact Go SIMD 131.5 (131.3–132.7); recorded old asm 63.13 (62.17–63.76) | 0 | five 300 ms samples per build; exact C tie/tail/invalid-lane checks and zero allocations pass; assembly timing is an earlier run with different rounding semantics and does not establish an exact-output speed ratio |
| 53 | `convertFloat32ToInt16SaturatingBlocks` | arm64 | `pcm_convert_simd_arm64.go`; `pcm_convert_arm64_nosimd.go` | archsimd / scalar | N=480, b36c1c20 M4/Go 1.27.0: scalar 422.4 / exact Go SIMD 106.9 (106.7–107.0); recorded old asm 52.31 (52.15–52.60) | 0 | five 300 ms samples per build; exact C tie/tail/invalid-lane checks and zero allocations pass; assembly timing is an earlier run with different rounding semantics and does not establish an exact-output speed ratio |

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
76.46–76.62 (earlier four-partial reduction; not the exact tone implementation);
four-output xcorr N=480 133.5–133.7 ns/op.
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

The AMD64 symbol rows use the completed direct-kernel phases in artifact
`10979051418` from [run 36435145306](https://github.com/thesyncim/gopus/actions/runs/36435145306)
at `6c466ded`: Intel Xeon 6973P-C, Go 1.27.1, GCC 13.3.0,
`GOAMD64=v1`, runtime AVX2/FMA dispatch, and five 300 ms samples per mode.
All samples report zero allocations. Each baseline/candidate kernel phase
completes successfully. The direct xcorr rows retain their separate shapes;
the pulse and best-ID helpers differ from the live finite-input PVQ search.
Sequential kernel phases retain host-load and frequency risks.

## Validation and performance follow-up

Finish the open QEXT and strict decoder audit findings, validate the final
revision on native AMD64 and ARM64, and refresh the PR tables from completed
artifacts. Preserve all 53 inventory rows and their fixture/compiler provenance.
Measurements from different CPUs or revisions do not establish a source-change
speed ratio. The current Hybrid mono SIMD encode row trails matched SIMD C;
several direct kernels also trail assembly, as recorded in the inventory.

The [compiler audit](go-simd-compiler-audit.md) records dispatch, instruction
lowering, and emulated Penryn/Sandy Bridge compatibility checks. Emulation
provides CPU-compatibility evidence, not native SIMD performance evidence.
