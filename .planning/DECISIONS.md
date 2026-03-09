# Investigation Decisions

Last updated: 2026-03-08

Purpose: record durable keep/skip decisions to avoid re-running solved investigations.

Older decision entries were intentionally pruned on 2026-03-01 to keep this file compact.

## Entry Template

```text
date: YYYY-MM-DD
topic: <short scope name>
decision: <what to keep/stop doing>
evidence: <test name(s), command(s), fixture(s), or CI links>
do_not_repeat_until: <condition that invalidates this decision>
owner: <handle>
```

## Current Decisions

date: 2026-03-09
topic: Quant-band transform stride specializations
decision: Keep the missing exact transform specializations in `celt/bands_quant.go` and `celt/haar1.go`: `haar1` specialized for strides `6/8/12`, and plain (non-Hadamard) `deinterleaveHadamardInto` / `interleaveHadamardInto` specialized for strides `12/16`. These cases are on the current encoder hot path and the specialized loops preserve the same per-element float32 arithmetic as the generic implementations.
evidence: Added `celt/haar1_exact_test.go` plus new coverage in `celt/hadamard_work_test.go` and `celt/haar1_bench_test.go`. Focused validation passed with `go test ./celt -run '^(TestHaar1SpecializedMatchesGeneric|TestHaar1Transform|TestHadamardWorkIntoMatchesLegacy)$' -count=1`, broader `go test ./celt -count=1`, parity `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v` (`23 passed, 0 failed`), and `make bench-guard`. Direct stride benches on Apple M4 Max improved materially: `BenchmarkHadamardWorkRoundTripCurrentStride12 ~62.41-65.02 ns/op` vs legacy `~108.1-110.0 ns/op`, and `BenchmarkHadamardWorkRoundTripCurrentStride16 ~55.34-58.16 ns/op` vs legacy `~94.16-100.3 ns/op`. Same-base root `BenchmarkEncoderEncode_Stereo` against a clean detached `HEAD` worktree improved from `~76069-76560 ns/op` to `~73436-73772 ns/op`.
do_not_repeat_until: the CELT quant-band block-count mix or transform staging changes enough that strides `6/8/12/16` are no longer representative, or same-base encoder benchmarks on target hosts stop favoring these specialized paths.
owner: codex

date: 2026-03-08
topic: Stereo theta-RDO prepared lowband reuse
decision: Keep the encoder-side prepared-lowband path in `celt/bands_quant.go` for stereo theta-RDO. When the encoder tries both `theta_round=-1` and `theta_round=+1`, precompute the x-channel lowband fold source once and reuse it across both trials instead of repeating the lowband copy/haar/deinterleave staging twice.
evidence: Added `celt/quant_band_prepared_lowband_test.go`; `go test ./celt -run '^TestQuantBandStereoPreparedLowbandMatchesStandard$' -count=1` passed with exact range-coder output, exact x/y/lowbandOut equality, and matching remaining-bit state against the standard path. Broader validation stayed green with `go test ./celt -count=1` and `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v` (`23 passed, 0 failed`). Same-host A/B against clean merged baseline `832c55c` favored the keep: root `BenchmarkEncoderEncode_Stereo` current `~80565-81544 ns/op` vs baseline `~82095-82267 ns/op`, and the short fair speech encode harness improved from baseline `avg 1.967958458s` to current `avg 1.96057818s`.
do_not_repeat_until: the theta-RDO trial structure, lowband fold-source semantics, or quant-band TF staging changes enough that the lowband preparation is no longer invariant across the two stereo trials.
owner: codex

date: 2026-03-08
topic: Encoder delay-buffer simplification and expRotation coefficient table
decision: Keep the encoder delay-compensation rewrite in `encoder/encoder.go` that removes the redundant tail snapshot and updates `delayBuffer` as a rolling raw-input history window, and keep the exact precomputed `expRotation()` coefficient table in `celt/exp_rotation_coeffs.go` for the production `(length,k,spread)` ranges. Both changes preserve existing behavior and improve the fair speech encode harness on the current host.
evidence: Added `encoder/delay_compensation_test.go` legacy-state coverage and direct helper benches; `go test ./encoder -run '^(TestApplyDelayCompensationMatchesLegacyState|TestDelayCompensation_StreamDelay(Mono|Stereo)|TestPrepareCELTPCM_DelayCompensationGatedByLowDelay|TestCELTTransitionPrefillSnapshotsLibopusDelayHistoryWindow)$' -count=1` passed. The helper bench improved versus the legacy shape (`Mono480 ~115 ns vs ~162 ns`, `Mono960 ~151-156 ns vs ~244-252 ns`, `Stereo480 ~218-222 ns vs ~322-333 ns`, `Stereo960 ~332-342 ns vs ~537-549 ns`, `0 allocs/op`). Added `celt/exp_rotation_coeffs_test.go`; `go test ./celt -run '^(TestExpRotationCoefficientsMatchDirectComputation|TestRotationUnitLibopus)$' -count=1` and `go test ./celt -count=1` passed. Quality/perf gates stayed green with `go test ./encoder -count=1`, `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v` (`23 passed, 0 failed`), and same-host `go test -run '^$' -bench '^BenchmarkEncoderEncode_Stereo$' -benchmem -benchtime=4s -count=3 .` landing around `~81582-82852 ns/op`. The fair speech encode harness improved from the recent `gopus avg ~1.987658666s` to `avg 1.954469531s` while libopus stayed around `avg 1.608260869s`.
do_not_repeat_until: the CELT transition-prefill/delay-buffer semantics change, or `expRotation()` starts seeing materially different `(length,k,spread)` ranges where the current exact table coverage no longer represents the hot path.
owner: codex

date: 2026-03-08
topic: ARM64 stride-1 expRotation asm on Apple M4 Max
decision: Do not ship the arm64 stride-1 `expRotation1()` asm prototype. Keep the Go stride-1 loop as the active path; the asm version was exact but slower on the current host.
evidence: A temporary exact dispatch guard for the asm version passed, but the existing rotation bench regressed on Apple M4 Max from the prior Go path (`BenchmarkExpRotation1Stride1Len32 ~133.9-136.6 ns/op`, `Len64 ~269.8-274.3 ns/op`) to the asm attempt (`Len32 ~176.3-185.4 ns/op`, `Len64 ~355.3-360.4 ns/op`). The asm files and dispatch scaffolding were reverted immediately rather than kept behind tags.
do_not_repeat_until: a materially different arm64 microarchitecture is the target, or there is a new exact vectorized stride-1 design rather than this scalar asm shape.
owner: codex

date: 2026-03-08
topic: Stereo prefilter helper staging and sum-of-squares unroll rejection
decision: Keep the stereo prefilter input/output layout cleanup in `celt/prefilter.go` by routing frame staging through `DeinterleaveStereoInto()` / `InterleaveStereoInto()`. Do not keep the exact-order `sumOfSquaresF64toF32()` unroll experiment; on the current host the plain scalar loop is faster.
evidence: Added `celt/stereo_layout_test.go`; `go test ./celt -run '^(TestStereoLayoutHelpersRoundTrip|TestRunPrefilterParityAgainstLibopusFixture)$' -count=1` passed and the libopus-backed prefilter fixture stayed exact. `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v` remained green (`23 passed, 0 failed`). Same-host root `BenchmarkEncoderEncode_Stereo` improved from the prior Hadamard safe point (`~84892-85127 ns/op`) to `~84320-84870 ns/op`, and the fair speech encode example improved from `avg 2.016978903s` to `avg 1.98866625s`. The exact-order `sumOfSquaresF64toF32` unroll was benchmarked and reverted because it regressed materially (`~87.46-87.74 ns/op` current vs legacy `~51.82-53.15 ns/op`) while leaving the root bench flat.
do_not_repeat_until: stereo prefilter staging layout changes, `runPrefilter()` stop using planar scratch, or a materially different host/compiler changes the scalar float32 sum-of-squares cost model.
owner: codex

date: 2026-03-08
topic: Hadamard work-buffer staging in quantBand encode/decode
decision: Keep the direct work-buffer staging in `celt/bands_quant.go` for `quantBand()` and `quantBandDecode()`. Deinterleave into dedicated scratch-owned work buffers and only interleave back into the original coefficient slice when resynthesis needs the reconstructed order. Also keep the `stride=3` and `stride=6` specializations in the direct permutation helpers because the generic non-power-of-two path is hot in representative stereo encode profiles.
evidence: Added `celt/hadamard_work_test.go`; exactness passed with `go test ./celt -run '^TestHadamardWorkIntoMatchesLegacy$' -count=1` and package coverage stayed green with `go test ./celt -count=1`. Parity remained green with `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v` (`23 passed, 0 failed`). The direct representative roundtrip benchmark improved from legacy `~311.7-334.2 ns/op` with allocs to `~77.7-79.9 ns/op`, and after specializing `stride=3/6` settled at `~63.3-63.7 ns/op` versus legacy generic `~92.3-92.7 ns/op`, all at `0 allocs/op`. Same-host root `BenchmarkEncoderEncode_Stereo` stayed favorable after the keep (`~84892-85127 ns/op` current versus the earlier pre-loop slice around `~83943-86471 ns/op`), while the fair speech harness stayed quality-stable but still behind libopus (`gopus avg 2.016978903s` vs `libopus avg 1.665199583s`).
do_not_repeat_until: quant-band interleave/deinterleave staging, transient-block count distribution, or scratch ownership changes enough that the current non-power-of-two stride mix is no longer representative.
owner: codex

date: 2026-03-08
topic: Transient stereo fused forward pass and PVQ arm64 extract fallback
decision: Keep the stereo-only fused forward transient-analysis pass in `celt/transient.go`, keep the fused collapse-mask plus residual-normalization helper in `celt/bands_quant.go`, and do not route arm64 `pvqExtractAbsSign` through asm by default. The transient and collapse changes are exact and materially faster on the current host; the arm64 extract asm helper is exact but slower than the generic loop, so default to the generic path while keeping the faster arm64 asm helpers that still win.
evidence: Added `celt/transient_bench_test.go`, `celt/bands_resynth_collapse_test.go`, `celt/pvq_dispatch_test.go`, `celt/arm64_helper_dispatch_test.go`, and `celt/imdct_rotate_dispatch_test.go`. Focused and package validation passed: `go test ./celt -count=1`, `go test ./encoder -count=1`, and `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v` (`23 passed, 0 failed`). Same-host benches favored the kept paths: `BenchmarkTransientAnalysisCurrentStereo ~5294-5424 ns/op` vs legacy `~8051-8184 ns/op`, mono `~3796-4037 ns/op` vs `~4452-4586 ns/op`, and `BenchmarkNormalizeResidualIntoAndCollapseCurrent ~23.37-23.43 ns/op` vs legacy `~31.02-32.04 ns/op`. After demoting the arm64 extract asm helper, `BenchmarkPVQExtractAbsSignCurrent ~13.25-13.44 ns/op` matched or beat the prior asm-backed shape, while the remaining arm64 helpers still showed large wins over reference (`celtPitchXcorr`, `pitchAutocorr5`, IMDCT rotate). Root encoder benches stayed favorable (`BenchmarkEncoderEncode ~47122/47194/49397 ns/op`; `BenchmarkEncoderEncode_Stereo ~83943-86471 ns/op`), though the fair speech harness still trails libopus (`gopus avg 2.019779111s` vs `libopus avg 1.661602875s`), so more encoder-side work is still required.
do_not_repeat_until: transient-analysis math/order changes, PVQ pulse-vector sign staging changes, or a materially different arm64 core/layout suggests the current `pvqExtractAbsSign` cost tradeoff is no longer representative.
owner: codex

date: 2026-03-08
topic: Analysis MLP fixed-row GEMM specialization
decision: Keep `encoder/gemmAccumF32` specialized for the production tonality-network row counts (`2`, `24`, `32`) instead of routing those hot paths through the generic slice-updating loop. Preserve the exact per-output accumulation order by keeping the `j`-outer loop and accumulating each output in a dedicated local before writing back once at the end.
evidence: Added `encoder/analysis_mlp_exact_test.go`; `go test ./encoder -run '^TestGemmAccumF32MatchesGenericReference$' -count=1` passed with exact output equality against a generic reference. Same-host microbench A/B improved `BenchmarkGemmAccumF32Rows24 ~194.1-196.6 ns/op -> ~89.7-90.6 ns/op`, `BenchmarkGemmAccumF32Rows32 ~197.3-198.6 ns/op -> ~95.8-97.6 ns/op`, and `BenchmarkAnalysisGRU ~1145-1183 ns/op -> ~575-586 ns/op`. End-to-end encoder perf stayed favorable: `BenchmarkTonalityAnalysis48kMono ~7114-7162 ns/op -> ~6419-6591 ns/op`, `BenchmarkTonalityAnalysis48kStereo ~7409-7465 ns/op -> ~6731-6857 ns/op`, same-base root `BenchmarkEncoderEncode ~47006-47292 ns/op` at safe-point `3c00f78` vs current `~46732-47052 ns/op`, and the fair speech encode example improved from `avg 2.00178525s` to `avg 1.992770028s`. `go test ./encoder -count=1`, `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v` (`23 passed, 0 failed`), and `make bench-guard` all passed.
do_not_repeat_until: the analysis network topology changes away from row counts `2/24/32`, the accumulation order requirements change, or same-base encoder benchmarks on target hosts stop favoring the specialized path.
owner: codex

date: 2026-03-08
topic: Experimental `sum_sq` and `spread_count` asm kernels
decision: Do not ship the experimental `sum_sq` or `spread_count` asm paths, and do not keep them hidden behind opt-in build tags. `sum_sq` changes float32 accumulation order and fails exactness; `spread_count` needed a correctness fix just to test and still lost to the generic Go loop on the current host. Keep the generic implementations as the only supported path unless a future asm rewrite is both exact and measurably favorable.
evidence: Enabling the hidden kernels for audit exposed two failures. First, arm64 `spread_count` had a compare-direction bug; after fixing that locally, direct microbench still favored generic Go (`BenchmarkSpreadCountThresholdsLegacy ~16.09-17.08 ns/op` vs asm `~24.26-24.42 ns/op`). Second, `sum_sq` failed a direct legacy-reference check because its vector lane accumulation changed the float32 sum order (`TestSumOfSquaresF64toF32MatchesLegacy` failed at `n=3`). After removing both asm/tag paths, `go test ./celt -count=1`, `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v` (`23 passed, 0 failed`), and `make bench-guard` all passed.
do_not_repeat_until: either helper gets a new exact asm implementation and fresh same-base A/B evidence shows a real end-to-end win on target hardware.
owner: codex

date: 2026-03-08
topic: 48 kHz mono analysis down2 loop unroll on Apple M4 Max
decision: Keep the two-output unroll in `encoder/analysis.go` `silkResamplerDown2HP()` and mirror the same loop shape in `silkResamplerDown2HPStereo()`, but only because it preserves the exact scalar arithmetic/state order and stays end-to-end favorable on the current host. Treat this as a loop-structure cleanup, not a math change.
evidence: Added `encoder/analysis_resampler_bench_test.go` plus `TestSilkResamplerDown2HPMatchesLegacy` in `encoder/analysis_test.go`. Focused exactness passed (`go test ./encoder -run '^(TestSilkResamplerDown2HPMatchesLegacy|TestSilkResamplerDown2HPStereoMatchesDownmixThenResample)$' -count=1`). Same-base arm64 A/B against `origin/master` favored the change: `BenchmarkSilkResamplerDown2HPLegacy ~947.7-978.3 ns/op` vs `BenchmarkSilkResamplerDown2HPCurrent ~874.1-895.4 ns/op`, `BenchmarkTonalityAnalysis48kMono ~7250-7284 ns/op` baseline vs `~7158-7187 ns/op` current, and root `BenchmarkEncoderEncode ~47245-47784 ns/op` baseline vs `~46996-47321 ns/op` current.
do_not_repeat_until: the down2 filter coefficients, state-update order, 48 kHz tonality-analysis call pattern, or target host/arch changes enough that the exactness guard or same-base encoder A/B is no longer representative.
owner: codex

date: 2026-03-08
topic: amd64 CELT asm runtime dispatch and arm64 default-on asm
decision: Keep CELT amd64 AVX2/FMA kernels shipped in the default binary behind runtime CPU dispatch, not `amd64.v3` source selection or user-facing build tags, but only for kernels that satisfy the existing exactness guards. Use a small in-repo CPU feature layer (`internal/cpufeat`) to select the AVX2/FMA path at runtime and keep exact generic helpers as the fallback on older x86 and for any non-exact amd64 kernel. Keep arm64 asm default-on with no user build tags; only add finer arm64 runtime splits when there is a proven second kernel worth selecting.
evidence: Replaced `celt/amd64_dispatch_v1.go` / `celt/amd64_dispatch_v3.go` with runtime-selected `celt/amd64_dispatch.go`, added `internal/cpufeat` for amd64 AVX2/FMA probing plus Darwin arm64 feature inventory, and retagged the AVX/FMA amd64 `.s` files from `//go:build amd64.v3` to `//go:build amd64`. Validation passed: `go test ./celt -run 'Test(CeltInnerProd|DualInnerProd|CeltPitchXcorr|PrefilterPitchXcorr)' -count=1`, `GOOS=linux GOARCH=amd64 go test -c ./celt`, and `GOOS=linux GOARCH=amd64 go test -c .`. Same-host A/B against detached `master` `563d1a6` showed no arm64 regression from the refactor (`BenchmarkEncoderEncode ~45942-46123 ns/op` current vs `~46203-46617 ns/op` master; `BenchmarkEncoderEncode_Stereo ~83452-84451 ns/op` current vs `~84235-85047 ns/op` master). CI then exposed a parity failure in `TestAMD64DispatchMatchesGeneric` on Linux amd64 because `expRotation1Stride2AVXFMA` changed the final float64 result slightly, so that helper was returned to the generic path while keeping the rest of the runtime-dispatch refactor intact. A follow-up arm64 `celtInnerProd` asm attempt was rejected and reverted because it lost to the existing Go path on Apple M4 Max.
do_not_repeat_until: the shipped amd64 kernel inventory changes, a new higher-ISA amd64 kernel (for example AVX-512) is ready for selection, or there is a proven second arm64 kernel worth dispatching at runtime on supported hosts.
owner: codex

date: 2026-03-08
topic: 48 kHz stereo tonality-analysis fused downmix+down2 path on Apple M4 Max
decision: Keep the stereo-only fused `encoder/analysis.go` 48 kHz staging path (`silkResamplerDown2HPStereo`) in `tonalityAnalysis`, but do not enable the analogous fused mono path on this host. The stereo helper is worth keeping; the mono fused helper is not.
evidence: Same-base stash A/B on the merged master showed `BenchmarkTonalityAnalysis48kStereo` improving from baseline `~7649-7697 ns/op` to current `~7353-7362 ns/op` (~`4%` faster), while the mono fused variant regressed enough to justify dropping it; after narrowing the change to stereo only, `BenchmarkTonalityAnalysis48kMono` returned to baseline (`~7344-7371 ns/op` baseline vs `~7354-7384 ns/op` current). Root `BenchmarkEncoderEncode_Stereo` stayed slightly favorable on the same base (`~87530-87876 ns/op` baseline vs `~87201-87722 ns/op` current). Safety checks stayed green: `go test ./encoder -run '^(TestSilkResamplerDown2HPStereoMatchesDownmixThenResample|TestAnalysisTraceFixtureParityWithLibopus)$' -count=1 -v`, `go test ./encoder -count=1`, `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v` (`23 passed, 0 failed`), and `make bench-guard`. `make verify-production` again failed only on the known local `tmp_check` cgo-disabled blocker after all other packages passed.
do_not_repeat_until: the 48 kHz analysis staging path, downmix/resample helper shape, or host/arch target changes enough that a new same-base A/B shows the mono fused path becoming favorable or the stereo helper losing its edge.
owner: codex

date: 2026-03-08
topic: removeDoubling float-width yy_lookup scratch
decision: Keep `celt/prefilter.go` `removeDoubling()` using `encoderScratch.prefilterYYLookup` as `[]float32`, matching libopus float-width `yy_lookup` semantics instead of storing those running values in `[]float64` scratch and narrowing them on every read.
evidence: Added `celt/remove_doubling_test.go`; `go test ./celt -run '^TestRemoveDoublingMatchesLegacyYYLookup$' -bench '^(BenchmarkRemoveDoublingCurrent|BenchmarkRemoveDoublingLegacy)$' -count=3 -cpu=1` passed with exact lag/gain agreement against the legacy float64-lookup reference. The microbench improved from legacy `~2496-2601 ns/op` to current `~2265-2300 ns/op`. Same-host A/B versus baseline worktree `2242402` stayed favorable on the encoder root bench (`~47325-47750 ns/op` baseline vs `~47231-47336 ns/op` current) and on the fair speech encode example (`avg 2.00886493s -> 1.991523458s`). `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v` remained green (`23 passed, 0 failed`), and `make bench-guard` passed.
do_not_repeat_until: `removeDoubling()` gain/threshold semantics, prefilter scratch ownership, or libopus pitch-doubling control flow changes in a way that invalidates the float-width lookup equivalence guard.
owner: codex

date: 2026-03-08
topic: CELT long-block IMDCT direct post-rotate target
decision: Keep the `celt/mdct.go` long-block IMDCT path writing `kissFFT32ToInterleaved` / `imdctPostRotateF32` output directly into the overlap/output scratch window instead of staging through a separate float32 buffer and copying into place before TDAC windowing.
evidence: Added `celt/imdct_overlap_f32_test.go`; `go test ./celt -run '^(TestIMDCTOverlapWithPrevScratchF32MatchesLegacyBufferCopy|TestDecodeFrameWithPacketStereoToFloat32MatchesDecodeFrame)$' -count=1` passed with bit-exact output against the legacy buffer-copy shape. Same-host decode A/B against safe-point worktree `3b416d0` remained slightly favorable: `BenchmarkDecoderDecode_CELT ~9168-9312 ns/op` baseline vs `~9074-9280 ns/op` current, and the fair speech decode example (`go run ./examples/bench-decode -sample speech -iters 3 -warmup 1 -mode both -batch 8`) improved from `avg 496.15843ms` to `avg 494.475055ms`. `go test ./celt -count=1` and `make bench-guard` passed.
do_not_repeat_until: the long-block IMDCT output layout, overlap/TDAC staging, or float32 scratch ownership changes in a way that invalidates direct post-rotate writes into the overlap buffer region.
owner: codex

date: 2026-03-08
topic: CELT MDCT direct-stage fast path
decision: Keep the `celt/mdct_encode.go` fast path that folds/window-prepares samples and writes the pre-twiddled values directly into the bit-reversed `kissCpx` FFT scratch on the normal direct MDCT path. Keep the staged `f[]` materialization only as the fallback/debug path.
evidence: Added an exact staged-reference guard in `celt/mdct_stage_test.go`; `go test ./celt -run '^(TestMDCTForwardOverlapDirectStageMatchesLegacyStagedPath|TestMDCT.*|TestLibopus.*MDCT.*|TestMDCTForward.*|TestMDCTShort.*)$' -count=1` passed and the direct-stage output matched the legacy staged path bit-for-bit on sizes `120/240/480/960`. Same-host isolated MDCT A/B versus baseline worktree `e50002a` improved from `frameSize=120 ~612.0-616.4 ns/op` to `~434.5-453.0 ns/op`, `240 ~1083-1089 ns/op` to `~790.2-801.0 ns/op`, `480 ~2021-2055 ns/op` to `~1515-1526 ns/op`, and `960 ~3947-4004 ns/op` to `~3045-3075 ns/op`. End-to-end encoder perf remained favorable (`BenchmarkEncoderEncode ~48822-49219 ns/op` baseline vs `~48129-49205 ns/op` current; fair speech encode example `avg 2.034969125s -> 2.025911069s`). Encoder compliance stayed green (`GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v`, `23 passed, 0 failed`).
do_not_repeat_until: the MDCT folding/pre-twiddle order, direct `kissCpx` FFT state layout, or float-math parity requirements change in a way that invalidates the direct-stage equivalence guard.
owner: codex

date: 2026-03-08
topic: CELT zero-pulse resynthesis fused fill/energy path
decision: Keep `celt/bands_quant.go` `seededZeroPulseResynth()` for the zero-pulse noise/fold resynthesis path used by `quantPartition()` and `quantPartitionDecode()`. Keep the fused generate+energy accumulation shape, but only on the exact seed-present / lowband-length-safe cases; retain the existing fallback path for nil seed or short lowband slices.
evidence: Exact legacy-equivalence guards passed (`go test ./celt -run '^(TestSeededZeroPulseResynthMatchesLegacy|TestSeededZeroPulseResynthFallback)$' -count=1`). Direct helper microbench improved from legacy noise `~76.99-77.83 ns/op` to `~19.55-19.78 ns/op` and fold `~152.5-153.8 ns/op` to `~24.30-24.49 ns/op`. Root decoder A/B versus baseline worktree `2bf74af` on the same host improved `BenchmarkDecoderDecode_CELT` from `~10341-10554 ns/op` to `~9086-9173 ns/op` (~`12%` faster) while encoder remained flat (`~48108-48507 ns/op` baseline vs `~47950-48584 ns/op` current). The fair speech decode example improved from `avg 514.151069ms` to `avg 495.682292ms` at `batch 8`. `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v` stayed green (`23 passed, 0 failed`), and `make bench-guard` passed.
do_not_repeat_until: zero-pulse band fill semantics, seed handling, lowband slicing invariants, or renormalization order/precision requirements change in a way that invalidates this fused helper.
owner: codex

date: 2026-03-07
topic: libopus perf comparison fairness harness
decision: Keep end-to-end perf comparisons pinned to `tmp_check/opus-1.6.1/opus_demo` with batched whole-stream runs; do not compare against ffmpeg or a harness that pays per-iteration libopus process startup overhead.
evidence: `examples/bench-encode` and `examples/bench-decode` now use `internal/benchutil/opus_demo.go` to drive repeated raw float32 input / repeated `.bit` streams through the pinned libopus reference. On the speech fixture, `go run ./examples/bench-encode -sample speech -iters 3 -warmup 1 -mode both -bitrate 64000 -complexity 10 -batch 8` and `go run ./examples/bench-decode -sample speech -iters 3 -warmup 1 -mode both -batch 8` produced stable batched gopus-vs-libopus measurements without the earlier startup bias.
do_not_repeat_until: the reference libopus version, the example harness protocol, or the desired fairness criteria for cross-implementation perf comparisons change materially.
owner: codex

date: 2026-03-07
topic: CWRS encode table-lookup fast path on Apple M4 Max
decision: Keep the `celt/cwrs.go` `icwrsLookupFast()` path that bypasses dynamic `unext()` row stepping when the static PVQ `U(n,k)` table covers all encode-side row lookups, and route both `EncodePulsesScratch` and `encodePulsesFast` through it before allocating the dynamic `u` buffer.
evidence: Focused CWRS correctness stayed green (`GOMAXPROCS=1 go test ./celt -run '^(Test.*CWRS.*|Test.*Pulses.*|TestPVQ_V.*|TestNCWRS.*)$' -count=1`) and full `GOMAXPROCS=1 go test ./celt -count=1` plus encoder analysis slice passed. Direct CWRS encode microbench versus a baseline worktree with the same surviving perf stack but without this change improved by about 2x on representative CELT shapes: `BenchmarkCWRS32Encode` `N8_K4 ~24.3-24.6 ns -> ~12.6-13.0 ns`, `N16_K4 ~49.1-50.0 ns -> ~22.1 ns`, `N32_K3 ~91.7-92.0 ns -> ~40.6-41.6 ns`, `N64_K2 ~166-167 ns -> ~81-82 ns`; `BenchmarkCWRS32RoundTrip/N16_K4 ~80.2-81.0 ns -> ~55.0-56.5 ns`. Root encode bench improved to `BenchmarkEncoderEncode ~43.8-44.1 us/op` and `BenchmarkEncoderEncodeInt16 ~44.1-44.4 us/op`. Speech example encode versus clean `HEAD` baseline improved from `best 255.378875ms / avg 257.276052ms` to current `best 228.690125ms / avg 229.578783ms` and repeat `best 229.583250ms / avg 232.349939ms`, clearing the `10%` target. `make bench-guard` passed. `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v` passed (`23 passed, 0 failed`). `make verify-production` remained blocked only by the known local `tmp_check` cgo-disabled setup.
do_not_repeat_until: the PVQ `U(n,k)` table coverage changes, CWRS encode semantics change, or broader perf/correctness gates on target hosts show this table-driven path regressing against the dynamic `unext()` fallback.
owner: codex

date: 2026-03-06
topic: transientAnalysisScratch fused HP/pair-energy pass on Apple M4 Max
decision: Keep the `celt/transient.go` pairwise transient-analysis rewrite that fuses HP filtering and forward pair-energy accumulation, and keep removal of the unused `transientTmp` scratch from `celt/encoder.go`.
evidence: Fresh `BenchmarkEncoderEncode` CPU profile (`go test . -run '^$' -bench '^(BenchmarkEncoderEncode$|BenchmarkEncoderEncodeInt16$)' -count=1 -benchtime=3s -cpu=1 -cpuprofile`) showed `celt.(*Encoder).transientAnalysisScratch` at `0.52s flat`, `7.10%`. After the rewrite, source profile dropped that routine to `0.39s flat`, `0.55s cum`. Direct hotspot A/B (`go test ./celt -run '^$' -bench '^(BenchmarkTransientAnalysis(Current|Legacy))$' -count=5 -cpu=1`) measured current `~6.21-6.34 us/op` versus legacy `~6.94-7.26 us/op` (~`8-13%` faster). Focused correctness stayed green (`go test ./celt -run '^(TestTransientAnalysisTfEstimate|TestWeakTransientMode|TestTransientAnalysisWithState|TestStereoTransientDetection)$' -count=1`) and `go test ./celt -count=1` passed.
do_not_repeat_until: transient detector math/order changes, or broader perf gates on target hosts show this fused path losing end-to-end despite the isolated hotspot win.
owner: codex

date: 2026-03-05
topic: ARM64 quarter-rate float32 prefilter scratch path on Apple M4 Max
decision: Do not replace the current quarter-rate `pitchSearch()` coarse path with a direct float32 scratch + dedicated float32 xcorr helper on this host. Keep the restored baseline `celt/prefilter_xcorr_arm64.s` path instead.
evidence: Focused correctness was green for the prototype (`go test ./celt -run '^(TestRunPrefilterParityAgainstLibopusFixture|TestPrefilterPitchXcorr|TestPrefilterPitchXcorrEdge)$' -count=1`), but perf regressed materially on Apple M4 Max. `go test ./celt -run '^$' -bench '^(BenchmarkPrefilterPitchXcorr|BenchmarkPrefilterPitchXcorrFloat|BenchmarkPitchSearch(Current|Legacy))$' -count=5 -cpu=1` measured baseline `BenchmarkPrefilterPitchXcorr ~3.92-3.96 us/op`, prototype float32 helper `BenchmarkPrefilterPitchXcorrFloat ~7.55-7.75 us/op`, and candidate `BenchmarkPitchSearchCurrent ~5.28-5.36 us/op` versus restored baseline `~3.72-3.76 us/op` (legacy `~4.07-4.08 us/op`). Same-session top-level encoder probe also improved after revert (`go test . -run '^$' -bench '^(BenchmarkEncoderEncode$|BenchmarkEncoderEncodeInt16$)' -count=1 -cpu=1`: candidate `~58.6/59.2 us/op`, restored `~55.7/56.3 us/op`).
do_not_repeat_until: a materially different float32 xcorr kernel/layout exists (not the current celt-local helper shape), or profiling on a different ARM64 microarchitecture shows the current baseline correlation path is no longer competitive.
owner: codex

date: 2026-03-05
topic: ARM64 prefilterPitchXcorr asm shape on Apple M4 Max
decision: Keep the existing `celt/prefilter_xcorr_arm64.s` 4-stream float32-accumulation kernel. Do not retry the three tested asm reshapes on this host: libopus-style shifted-window `VEXT` windows, dual-accumulator 8-wide splitting, or multi-register `ld1` pair loads.
evidence: `BenchmarkEncoderEncode` CPU profile showed `celt.prefilterPitchXcorr` as the top asm hotspot (`0.37s flat`, `7.23%`). Focused correctness guard stayed green on baseline restoration (`go test ./celt -run '^(TestPrefilterPitchXcorr|TestPrefilterPitchXcorrEdge)$' -count=1`). Focused microbench evidence on Apple M4 Max (`go test ./celt -run '^$' -bench '^BenchmarkPrefilterPitchXcorr$' -count=5 -cpu=1`): baseline `~3.76-3.81 us/op`; shifted-window rewrite `~6.50-6.76 us/op`; dual-accumulator variant `~4.09-4.14 us/op`; load-pair variant `~3.80-3.83 us/op`.
do_not_repeat_until: the quarter-rate prefilter input layout changes (for example, a float32 scratch path), or new evidence on a materially different ARM64 microarchitecture shows the current kernel regressing.
owner: codex

date: 2026-03-04
topic: findBestPitch sparse xcorr conversion skip
decision: Keep `celt/prefilter.go` `findBestPitch` guard that skips float64->float32 conversion when `xcorr[i] <= 0`, plus BCE hints on `y[length+maxPitch-1]` and `xcorr[maxPitch-1]`.
evidence: Quality/parity remained green (`go test ./celt -count=1`; `go test ./encoder -count=1`; `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v`, `23 passed, 0 failed`; full runnable-package sweep excluding local `tmp_check` passed). `make bench-guard` passed. Bench-binary stash A/B (`mode=gopus,iters=20,warmup=3`) improved from baseline `best 260.523958ms / avg 263.289118ms` to candidate `best 258.820209ms / avg 261.284043ms` (~`0.65%` best, `0.76%` avg).
do_not_repeat_until: pitch-search sparse-window behavior or `findBestPitch` scoring semantics change.
owner: codex

date: 2026-03-04
topic: transient harmonic-mean loop float32 normalization
decision: Keep float32 `normE` and float32 table-index math in `celt/transient.go` harmonic-mean loop (`id := int(normE * (energy[i] + epsF32))`) instead of per-iteration float64 conversions.
evidence: Quality/parity remained green (`go test ./celt -count=1`; `go test ./encoder -count=1`; `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v`, `23 passed, 0 failed`; full runnable-package sweep excluding local `tmp_check` passed). `make bench-guard` passed. Bench-binary stash A/B (`mode=gopus,iters=20,warmup=3`) improved from baseline `best 260.885500ms / avg 263.234895ms` to candidate `best 257.531416ms / avg 262.346818ms` (~`1.29%` best, `0.34%` avg).
do_not_repeat_until: transient detector threshold/index semantics or energy-domain precision requirements change.
owner: codex

date: 2026-03-04
topic: Analysis MLP gemmAccumF32 4-way row unroll
decision: Keep 4-way row unroll + scalar tail in `encoder/analysis_mlp.go` `gemmAccumF32`.
evidence: Quality/parity remained green (`go test ./celt -count=1`; `go test ./encoder -count=1`; `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v`, `23 passed, 0 failed`; full runnable-package sweep excluding local `tmp_check` passed). `make bench-guard` passed. Bench-binary stash A/B (`mode=gopus,iters=20,warmup=3`) improved from baseline `best 261.709625ms / avg 263.283785ms` to candidate `best 257.730042ms / avg 260.000585ms` (~`1.52%` best, `1.25%` avg).
do_not_repeat_until: analysis MLP weight layout (`colStride`) or accumulation semantics change.
owner: codex

date: 2026-03-04
topic: ARM64 toneLPCCorr pointer-walk addressing
decision: Keep `celt/tone_lpc_corr_arm64.s` pointer-walk addressing for delayed streams (`x+delay`, `x+delay2`) instead of per-iteration delayed-address recomputation.
evidence: Quality/parity remained green (`go test ./celt -count=1`; `go test ./encoder -run 'Test(Analysis|RunAnalysis|TonalityAnalysis|UpdateOpusVADReusesFreshAnalysis|AnalysisTraceFixtureParityWithLibopus)' -count=1`; `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v`, `23 passed, 0 failed`; full runnable-package sweep excluding local `tmp_check` passed). `make bench-guard` passed. Bench-binary stash A/B (`mode=gopus,iters=20,warmup=3`) improved from baseline `best 277.177375ms / avg 281.044162ms` to candidate `best 273.911625ms / avg 278.773247ms` (~`1.18%` best, `0.81%` avg).
do_not_repeat_until: tone-LPC correlation accumulation ordering, delay semantics, or ARM64 asm constraints change.
owner: codex

date: 2026-03-04
topic: ARM64 pitchAutocorr5 8-wide unroll
decision: Keep ARM64 `pitchAutocorr5` inner-loop unroll in `celt/pitch_autocorr_arm64.s` (8 elements/iteration + 4/2/1 tails) and explicit inner-pointer init before tail paths.
evidence: Quality/parity remained green (`go test ./celt -count=1`; `go test ./encoder -run 'Test(Analysis|RunAnalysis|TonalityAnalysis|UpdateOpusVADReusesFreshAnalysis|AnalysisTraceFixtureParityWithLibopus)' -count=1`; `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v`, `23 passed, 0 failed`; full runnable-package sweep excluding local `tmp_check` passed). `make bench-guard` passed. Bench-binary stash A/B (`mode=gopus,iters=20,warmup=3`) improved from baseline `best 273.530834ms / avg 276.484366ms` to candidate `best 270.693291ms / avg 274.498254ms` (~`1.0%` best, `0.7%` avg).
do_not_repeat_until: pitch autocorrelation float32 accumulation semantics, lag/correction ordering, or ARM64 asm constraints change.
owner: codex

date: 2026-03-04
topic: ARM64 prefilter inner-product 8-wide unroll
decision: Keep ARM64 SIMD loop unroll in `celt/prefilter_innerprod_arm64.s` for `prefilterInnerProd` and `prefilterDualInnerProd` (8 elements/iteration with 4/2/1 tails), preserving float32 accumulation order.
evidence: Quality/parity remained green (`go test ./celt -count=1`; `go test ./encoder -run 'Test(Analysis|RunAnalysis|TonalityAnalysis|UpdateOpusVADReusesFreshAnalysis|AnalysisTraceFixtureParityWithLibopus)' -count=1`; `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v`, `23 passed, 0 failed`; full runnable-package sweep excluding known local `tmp_check` also passed). `make bench-guard` passed. Bench-binary stash A/B showed wins: `mode=gopus,iters=20,warmup=3` improved from baseline `best 266.359167ms / avg 269.458806ms` to candidate `best 263.696541ms / avg 267.300729ms` (~`1.0%` best, `0.8%` avg); `mode=both,iters=8,warmup=2` candidate gopus also improved vs baseline.
do_not_repeat_until: prefilter dot-product float32 semantics, ARM64 asm constraints, or pitch-search/remove-doubling call patterns change.
owner: codex

date: 2026-03-04
topic: MDCT pre/post twiddle loop specialization
decision: Keep `celt/mdct_encode.go` split-loop structure in `mdctForwardOverlapF32Scratch` that hoists `mdctUseFMALikeMixEnabled` and direct-`kissCpx`/fallback selection out of inner pre/post twiddle loops.
evidence: Focused quality/parity stayed green (`go test ./celt -run 'Test(Transient|PrefilterPitchXcorr|RunPrefilterParityAgainstLibopusFixture|Tone|MDCT)' -count=1`; `go test ./encoder -run 'Test(Analysis|RunAnalysis|TonalityAnalysis|UpdateOpusVADReusesFreshAnalysis|AnalysisTraceFixtureParityWithLibopus)' -count=1`; `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v`, `23 passed, 0 failed`). Broad runnable sweep passed (`go list -e -f '{{if not .Error}}{{.ImportPath}}{{end}}' ./... | rg -v '/tmp_check$' | xargs go test -count=1`). `make bench-guard` passed. Bench-binary stash A/B (`go run ./examples/bench-encode -sample speech -iters 20 -warmup 3 -mode gopus -bitrate 64000 -complexity 10`) improved from baseline `best 272.930583ms / avg 275.878118ms` to candidate `best 269.492375ms / avg 273.872166ms` (~`1.26%` best, `0.73%` avg faster); shorter `iters 8 mode both` confirmation also improved.
do_not_repeat_until: MDCT twiddle math, `mdctUseFMALikeMixEnabled` semantics, or Kiss FFT staging path changes invalidate this loop structure and require a new A/B.
owner: codex

date: 2026-03-04
topic: MDCT direct bit-reversed kissCpx staging path
decision: Keep `celt/mdct_encode.go` direct MDCT FFT staging path that writes pre-twiddled values directly into bit-reversed `kissCpx` scratch and runs `st.fftImpl()` in-place for supported FFT sizes, while preserving the existing `kissFFT32To` fallback path for unsupported states.
evidence: Quality/parity remained green (`go test ./celt -run 'Test(Transient|PrefilterPitchXcorr|RunPrefilterParityAgainstLibopusFixture|Tone|MDCT)' -count=1`; `go test ./encoder -run 'Test(Analysis|RunAnalysis|TonalityAnalysis|UpdateOpusVADReusesFreshAnalysis|AnalysisTraceFixtureParityWithLibopus)' -count=1`; `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v`, `23 passed, 0 failed`). Bench evidence from requested examples harness A/B (`go run ./examples/bench-encode -sample speech -iters 8 -warmup 2 -mode gopus -bitrate 64000 -complexity 10`): candidate `best 282.346709ms / avg 284.964474ms` vs baseline `best 285.893458ms / avg 288.955969ms` (about `~1.3-1.4%` faster). `make bench-guard` passed; `make verify-production` showed only known local `tmp_check` cgo-disabled blocker.
do_not_repeat_until: MDCT pre/post-twiddle math order, Kiss FFT state layout/bitrev semantics, or supported CELT frame-size FFT set changes in a way that invalidates this staging path.
owner: codex

date: 2026-03-03
topic: Transient analysis fused pair-energy and forward-mask pass
decision: Keep the `celt/transient.go` `transientAnalysisScratch` fused loop that computes pair energy and forward masking in one traversal, and keep removal of the no-longer-needed `transientX2` scratch slice from `celt/encoder.go`.
evidence: Quality/parity remained green (`go test ./celt -run 'Test(Transient|PrefilterPitchXcorr|RunPrefilterParityAgainstLibopusFixture|Tone)' -count=1`; `go test ./celt -count=1`; `go test ./encoder -run 'Test(Analysis|RunAnalysis|TonalityAnalysis|UpdateOpusVADReusesFreshAnalysis|AnalysisTraceFixtureParityWithLibopus)' -count=1`; `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v`, `23 passed, 0 failed`). Controlled A/B microbench (`GOMAXPROCS=1 go test ./ -bench 'BenchmarkEncoderEncode$|BenchmarkEncoderEncodeInt16$' -benchmem -run '^$' -count=8 -benchtime=2s -cpu=1`) improved current vs baseline from `~52.2-54.0 us/op` to `~51.5-53.5 us/op` (`BenchmarkEncoderEncode`) and from `~53.5-54.0 us/op` to `~51.9-52.8 us/op` (`BenchmarkEncoderEncodeInt16`). `make bench-guard` passed; `make verify-production` showed only the known local `tmp_check` cgo-disabled blocker.
do_not_repeat_until: transient-analysis forward-masking math order, detector threshold semantics, or scratch layout changes in ways that invalidate this A/B result.
owner: codex

date: 2026-03-02
topic: Analysis MLP float32 weight-cache path and transient float32 scratch path
decision: Keep the analysis MLP fast path that preconverts global int8 dense/GRU weights to float32 once at init (`initAnalysisMLPWeightCaches`) and uses `gemmAccumF32` during `ComputeDense`/`ComputeGRU`. Keep transient analysis working buffers in float32 (`transientEnergy`, `transientX`) to avoid float64<->float32 conversion churn in `celt.(*Encoder).transientAnalysisScratch`.
evidence: Quality/parity stayed green (`go test ./encoder -run 'Test(Analysis|RunAnalysis|TonalityAnalysis|UpdateOpusVADReusesFreshAnalysis|AnalysisTraceFixtureParityWithLibopus)' -count=1`; `go test ./celt -run 'Test(Transient|PrefilterPitchXcorr|RunPrefilterParityAgainstLibopusFixture|Tone)' -count=1`; `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v`, `23 passed, 0 failed`). Perf evidence: root encode microbench (`go test . -run '^$' -bench 'BenchmarkEncoderEncode$|BenchmarkEncoderEncodeInt16$' -benchmem -benchtime=2s -count=5`) improved int16 cluster from ~`55k ns/op` to ~`50-51k ns/op` best samples; `make bench-guard` passed with encoder samples around ~`50.9-54.7k ns/op`, `0 allocs/op`. CPU profile comparison (`-cpuprofile` on `BenchmarkEncoderEncode`) showed `transientAnalysisScratch` flat share dropping from ~`7.3%` to ~`5.2%`.
do_not_repeat_until: analysis MLP topology/weights change, transient detector math order changes, or parity fixtures indicate regression tied to these fast paths.
owner: codex

date: 2026-03-02
topic: Tonality analysis redundant-energy scan and sqrt/log reuse
decision: Keep the tonality-analysis hot-path update in `encoder/analysis.go` that (1) computes `log(bandE)` once per band and reuses it for `logE` and `bandLog2`, (2) persists per-frame `sqrt(E)` into `SqrtE` for stationarity accumulation reuse across history frames, and (3) reuses first-pass `bandERaw` sums in bandwidth-mask evaluation instead of rescanning per-band bins.
evidence: A/B microbenchmark (`GOMAXPROCS=1 go test ./encoder -run '^$' -bench 'BenchmarkAnalysisBandEnergy(Legacy|Current)$' -benchmem -benchtime=2s -count=6 -cpu=1`) shows legacy `~528.7-539.5 ns/op` vs current `~400.1-402.5 ns/op` (~24-26% faster, `0 allocs/op`) for the optimized section. Quality/parity remained green (`go test ./encoder -run 'Test(Analysis|RunAnalysis|TonalityAnalysis|UpdateOpusVADReusesFreshAnalysis|AnalysisTraceFixtureParityWithLibopus)' -count=1`; `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v`, `23 passed, 0 failed`). End-to-end perf gate stayed green (`make bench-guard`).
do_not_repeat_until: tonality-analysis band-accumulation math, stationarity definition, or bandwidth-mask sequencing changes in ways that invalidate this section-level A/B benchmark.
owner: codex

date: 2026-03-02
topic: Pitch downsample factor-2 specialization and state-rounding skip in prefilter
decision: Keep `pitchDownsample()` specialized fast path for `factor=2` mono/stereo in `celt/prefilter.go`, and keep conditional skipping of `prefilterMem`/overlap re-rounding in the default float32 prefilter path while retaining explicit rounding in debug/alternate-precision modes (`tmpSkipPrefInputRoundEnabled` or `tmpPrefilterF64Enabled`).
evidence: A/B microbenchmark (`go test ./celt -run '^$' -bench 'BenchmarkPitchDownsample(Current|Legacy)(Mono|Stereo)$' -benchmem -benchtime=2s -count=5`) shows stereo improvement from legacy `~2471-2519 ns/op` to current `~2252-2316 ns/op` (~8-10% faster); mono remains neutral/slightly improved (`~1931-2010 ns/op` legacy vs `~1920-1994 ns/op` current). Parity/compliance stayed green (`go test ./celt -run 'Test(PrefilterPitchXcorr|RunPrefilterParityAgainstLibopusFixture|TransientAnalysis)' -count=1`; `go test ./celt -count=1`; `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v` with `23 passed, 0 failed`), full runnable package sweep passed, and `make bench-guard` passed.
do_not_repeat_until: prefilter pitch-downsample input layout/factor usage changes, float-precision debug flag semantics change, or libopus prefilter parity fixtures show rounding-behavior drift.
owner: codex

date: 2026-03-02
topic: Pitch search fine-stage candidate-window optimization
decision: Keep the `pitchSearch()` fine-stage rewrite in `celt/prefilter.go` that preserves libopus-equivalent candidate coverage (`±2` around both coarse winners) while replacing the full-range `abs()`-gated scan with explicit window loops and a single `clear(xcorr[:halfPitch])`. Keep the dedicated A/B benchmark fixture (`celt/pitch_search_bench_test.go`) to guard this hotspot against regressions.
evidence: Direct A/B benchmark on representative prefilter dimensions (`go test ./celt -run '^$' -bench 'BenchmarkPitchSearch(Current|Legacy)$' -benchmem -benchtime=2s -count=5`) showed current `~3764-3828 ns/op` vs legacy `~4088-4159 ns/op` (~8% faster) at `0 allocs/op`. Quality parity remained green (`go test ./celt -run 'Test(PrefilterPitchXcorr|RunPrefilterParityAgainstLibopusFixture|TransientAnalysis)' -count=1`; `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v` with `23 passed, 0 failed`). Perf guard stayed green (`make bench-guard` passed, encoder rows around `57.2/56.9 us/op`).
do_not_repeat_until: libopus pitch-search candidate semantics, prefilter pitch range geometry, or arm64/x86 correlation kernels change in ways that alter this hotspot's cost model.
owner: codex

date: 2026-03-02
topic: Prefilter mono fast path and selective input rounding
decision: Keep `celt/prefilter.go` mono fast-path gather/scatter (`copy`-based) in `runPrefilter`, and keep selective input rounding of only appended frame samples when prefilter history is already float32-quantized (`!tmpSkipPrefMemRoundEnabled`) while preserving full-buffer rounding fallback when debug flags bypass history rounding.
evidence: Libopus parity fixture remained exact after changes (`go test ./celt -run 'TestRunPrefilterParityAgainstLibopusFixture' -count=1 -v`: `cases=300`, mismatch counters `0`, `maxGainDiff=0.000000`). Additional guards passed: `go test ./celt -run 'Test(TransientAnalysis|PrefilterPitchXcorr)' -count=1`; `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v` with `23 passed, 0 failed`; `make bench-guard` passed. Conservative benchmark evidence on arm64 from guard-style runs shows `BenchmarkEncoderEncode` improving from roughly `~55.2 us/op` to `~54.6 us/op` and `BenchmarkEncoderEncodeInt16` from `~55.8 us/op` to `~54.8 us/op` (with expected run-to-run variance in standalone benchmark probes).
do_not_repeat_until: prefilter input precision policy, `tmpSkipPref*` debug semantics, or libopus prefilter fixture behavior changes.
owner: codex

date: 2026-03-02
topic: Transient analysis direct PCM consumption in hot path
decision: Keep `celt/transient.go` `transientAnalysisScratch` consuming channel samples directly from `pcm` during HP filtering (mono and stereo paths) instead of copying into a per-channel scratch slice first.
evidence: CELT transient/tone tests and parity remained green (`go test ./celt -run 'Test(Transient|Tone|PatchTransientDecision)' -count=1`; `go test ./celt -run 'TestTransientAnalysis' -count=1 -v`; `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v` with `23 passed, 0 failed`). Performance improved on arm64 (`go test . -run '^$' -bench '^(BenchmarkEncoderEncode$|BenchmarkEncoderEncodeInt16$|BenchmarkEncoderEncode_VoIP$|BenchmarkEncoderEncode_LowDelay$)' -benchmem -count=5 -cpu=1`): `BenchmarkEncoderEncode` ~`55.7-55.9 us/op` -> ~`54.6-55.1 us/op`; `BenchmarkEncoderEncodeInt16` ~`56.4-56.6 us/op` -> ~`55.2-55.7 us/op`; `BenchmarkEncoderEncode_VoIP` ~`51.2-51.6 us/op` -> ~`50.2-50.3 us/op`. `make bench-guard` passed; `make verify-production` remained blocked only by known local `tmp_check` cgo-disabled setup.
do_not_repeat_until: transient-analysis input layout/control flow changes or libopus parity fixtures indicate a regression in transient decisions.
owner: codex

date: 2026-03-02
topic: Tonality analysis hot-path scratch hoist and bin-energy reuse
decision: Keep `encoder/analysis.go` tonality hot-path temporaries (`FFT in/out`, tonality/noisiness working arrays) as persistent `TonalityAnalysisState` scratch, and keep reuse of precomputed raw FFT bin energies in the later bandwidth pass instead of recomputing from `out[]`.
evidence: Quality/parity checks remained green (`go test ./encoder -run 'Test(Analysis|RunAnalysis|TonalityAnalysis|UpdateOpusVADReusesFreshAnalysis|AnalysisTraceFixtureParityWithLibopus)' -count=1`; `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v` with `23 passed, 0 failed`). Performance improved on arm64 (`go test . -run '^$' -bench '^(BenchmarkEncoderEncode$|BenchmarkEncoderEncodeInt16$|BenchmarkEncoderEncode_VoIP$|BenchmarkEncoderEncode_LowDelay$)' -benchmem -count=5 -cpu=1`): `BenchmarkEncoderEncode` ~`56.6-57.4 us/op` -> ~`55.7-55.9 us/op`; `BenchmarkEncoderEncodeInt16` ~`56.6-57.8 us/op` -> ~`56.4-56.6 us/op`; `make bench-guard` passed. Profile evidence: tonality-analysis cum share `17.43% -> 14.21%`, `runtime.morestack` `3.49% -> 2.19%`.
do_not_repeat_until: tonality-analysis algorithm/control-flow changes or new libopus parity evidence requires rework of this path.
owner: codex

date: 2026-03-01
topic: Cross-arch ratchet hardening (SILK/Hybrid weak lanes)
decision: Keep tightened floors for `SILK-WB-20ms-mono-32k|am_multisine_v1` and `SILK-WB-40ms-mono-32k|am_multisine_v1` at `min_gap_db=-0.02` on both baselines. Keep tightened weak-lane floors for `HYBRID-SWB-40ms-mono-48k|impulse_train_v1` at default `-0.05` / amd64 `-0.12`, `SILK-WB-40ms-mono-32k|impulse_train_v1` at default `-0.03` / amd64 `-0.11`, and `SILK-NB-40ms-mono-16k|am_multisine_v1` at default `-0.03` / amd64 `-0.08`.
evidence: Focused repeated subtest probes across 3 runs per arch were deterministic for the three weak lanes: arm64 `+0.06/+0.03/-0.00 dB` and amd64 `-0.10/-0.09/-0.06 dB` (`HYBRID-SWB-40ms impulse`, `SILK-WB-40ms impulse`, `SILK-NB-40ms am`). Full variant parity and provenance remained green on arm64 + amd64 after tightening; compliance summary runs completed with no test failures; `make bench-guard` passed; `make verify-production` failed only on expected local `tmp_check` cgo-disabled setup.
do_not_repeat_until: fixture corpus, quality scoring semantics, or SILK/Hybrid packetization/control-flow changes materially alter these lane distributions.
owner: codex

date: 2026-03-01
topic: SILK-WB-60ms amd64 ratchet floor hardening
decision: Keep tightened amd64 floors for `SILK-WB-60ms-mono-32k|am_multisine_v1` and `SILK-WB-60ms-mono-32k|impulse_train_v1` at `min_gap_db=-0.03`; keep default floors unchanged (`am_multisine=-0.03`, `impulse_train=-0.04`) due arm64 impulse stability at `-0.04 dB`.
evidence: Focused repeated subtest probes: arm64 `impulse_train` stayed `gap=-0.04 dB` (5 runs), amd64 `impulse_train` stayed `gap=-0.00 dB` (5 runs), arm64/amd64 `am_multisine` stayed `gap=0.00 dB` (5 runs). After tightening amd64 floors, full parity/provenance/compliance checks passed plus `make bench-guard`; `make verify-production` showed only the known local `tmp_check` cgo-disabled blocker.
do_not_repeat_until: fixture corpus, quality scoring semantics, or SILK WB 60ms packetization/control flow changes materially alter this lane distribution.
owner: codex

date: 2026-03-01
topic: Ambisonics high-order libopus parity coverage
decision: Keep expanded libopus parity matrix coverage for ambisonics families 2 and 3, including TOA (`16ch`, `18ch`) and family-3 4th/5th-order lanes (`25ch`, `27ch`, `36ch`, `38ch`), as the default regression guard.
evidence: `go test ./multistream -run 'TestLibopus_AmbisonicsFamily(2|3)Matrix' -count=1 -v` passed for all newly added lanes with stable internal-vs-libopus energy ratios and decode drift within guard thresholds; full `go test ./multistream -count=1` also passed.
do_not_repeat_until: ambisonics projection/mapping control flow, projection matrix defaults, or libopus-reference decode helper semantics change.
owner: codex

date: 2026-03-01
topic: SILK-WB-20ms am amd64 ratchet floor hardening
decision: Keep `SILK-WB-20ms-mono-32k|am_multisine_v1` amd64 floor at `min_gap_db=-0.05` (tightened from `-0.10`) while keeping the default floor at `-0.03`.
evidence: Repeated subtest probes were stable on arm64 and amd64 at `gap=-0.00 dB` for `TestEncoderVariantProfileParityAgainstLibopusFixture/cases/SILK-WB-20ms-mono-32k-am_multisine_v1`. After tightening, full parity/provenance/compliance checks stayed green on both arches plus `make bench-guard`; `make verify-production` showed only the known local `tmp_check` cgo-disabled blocker.
do_not_repeat_until: fixture corpus, quality scoring semantics, or SILK WB 20ms packetization/control flow changes materially alter this lane distribution.
owner: codex

date: 2026-03-01
topic: SILK WB ratchet hardening (40ms am + 60ms impulse amd64)
decision: Keep tightened floors for `SILK-WB-40ms-mono-32k|am_multisine_v1` at default `min_gap_db=-0.03` and amd64 `min_gap_db=-0.05`, and for `SILK-WB-60ms-mono-32k|impulse_train_v1` amd64 at `min_gap_db=-0.05` while keeping default at `-0.04`.
evidence: Repeated subtest probes were stable: arm64 `SILK-WB-40ms am` at `gap=-0.00 dB`, amd64 `SILK-WB-40ms am` at `gap=0.00 dB`; arm64 `SILK-WB-60ms impulse` at `gap=-0.04 dB` (so default floor kept), amd64 `SILK-WB-60ms impulse` at `gap=-0.00 dB`. After tightening, full `TestEncoderVariantProfileParityAgainstLibopusFixture` (arm64 + amd64), `TestEncoderVariantProfileProvenanceAudit`, `TestEncoderComplianceSummary`, and `make bench-guard` passed; `make verify-production` showed only the known local `tmp_check` cgo-disabled blocker.
do_not_repeat_until: fixture corpus, quality scoring semantics, or SILK WB packetization/control flow changes materially alter these lane distributions.
owner: codex

date: 2026-03-01
topic: Planning doc compaction policy
decision: Keep `.planning/ACTIVE.md`, `.planning/DECISIONS.md`, and `.planning/WORK_CLAIMS.md concise; archive deep history snapshots under `.planning/archive/`.
evidence: On 2026-03-01, live planning files grew to ~345KB total and reduced usability; archived full snapshots and rewrote compact operational summaries.
do_not_repeat_until: planning volume remains low and navigation cost is no longer a concern.
owner: codex

date: 2026-03-01
topic: SILK-WB-60ms am_multisine ratchet floor hardening
decision: Keep `SILK-WB-60ms-mono-32k|am_multisine_v1` floors at default `min_gap_db=-0.03` and amd64 `min_gap_db=-0.05`.
evidence: Focused arm64/amd64 parity probes were stable at `gap=0.00 dB`; full parity/provenance/compliance checks plus CI matrix stayed green (merged PR #261).
do_not_repeat_until: fixture corpus, quality metric semantics, or SILK WB 60ms packetization/control flow changes materially.
owner: codex

date: 2026-03-01
topic: SILK-WB-60ms impulse ratchet floor hardening
decision: Keep `SILK-WB-60ms-mono-32k|impulse_train_v1` floors at default `min_gap_db=-0.04` and amd64 `min_gap_db=-0.08`.
evidence: Repeated focused arm64/amd64 probes were stable; full parity/provenance/compliance checks plus CI matrix stayed green (merged PR #260).
do_not_repeat_until: fixture corpus, quality metric semantics, or SILK WB 60ms packetization/control flow changes materially.
owner: codex

date: 2026-03-01
topic: Final CELT compliance residual override floor
decision: Keep the remaining no-negative override for `CELT-FB-2.5ms-mono-64k` at `0.191 dB`.
evidence: Deterministic residual observed at approximately `-0.190 dB` with stable packet-shape parity; tightened from `0.20` without regression (merged PR #259).
do_not_repeat_until: CELT 2.5ms parity/control-flow changes or compliance quality-measure semantics shift this residual lane.
owner: codex

date: 2026-02-28
topic: Compliance packet-cadence parity
decision: Keep compliance encode cadence aligned to libopus fixture behavior by allowing bounded trailing flush packets.
evidence: Summary improved from failing rows to stable pass status after cadence alignment; follow-on precision/parity guards remained green.
do_not_repeat_until: fixture cadence model (`signal_frames`/`frames`) or compliance harness semantics change.
owner: codex

date: 2026-02-28
topic: Compliance reference-Q decode-path alignment
decision: Keep reference-Q calibration on libopus-only decode path (direct helper first, `opusdec` fallback) for fixture honesty.
evidence: Refreshed reference-Q fixtures and preserved parity/compliance guard behavior after decode-path alignment.
do_not_repeat_until: libopus helper decode protocol or compliance fixture generation semantics change.
owner: codex

date: 2026-02-28
topic: Hybrid held-frame transition redundancy parity
decision: Keep libopus-style to-CELT redundancy on held SILK/Hybrid transition frames (`celt_to_silk=0` path).
evidence: Source-port closed previously negative hybrid residual lane while parity/provenance/compliance suites stayed green.
do_not_repeat_until: transition-policy semantics or redundancy signaling model changes in encoder hybrid flow.
owner: codex

date: 2026-02-13
topic: Verified areas skip policy
decision: Do not re-debug SILK decoder correctness, resampler parity path, or NSQ constant-DC behavior without new contradictory fixture evidence.
evidence: Sustained passing parity checks plus explicit AGENTS verified-area guidance.
do_not_repeat_until: related code paths or fixtures change.
owner: codex
