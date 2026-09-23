# Go kernel replacement evidence

This report tracks all 53 symbols from the 41 assembly files in the pre-port
`origin/master` tree. Every entry has a Go replacement. `archsimd` means the
Go 1.27 `simd/archsimd` implementation selected by `GOEXPERIMENT=simd`; ordinary
builds use the listed scalar Go path, and `-tags nosimd` forces the scalar
reference path.

## Measurement method

M4 Max (`darwin/arm64`) A/B measurements use Go 1.27.1 on the same host. The
direct benchmark set uses GOMAXPROCS=4 and five samples per mode; the initial
rows use 100 ms samples and the added inventory wrappers use 200 ms samples.
Native AMD64 A/B measurements come from [CI runs
35911787670](https://github.com/thesyncim/gopus/actions/runs/35911787670) and
[35925161573](https://github.com/thesyncim/gopus/actions/runs/35925161573), and
[35930378365](https://github.com/thesyncim/gopus/actions/runs/35930378365): the
pre-port base and candidate ran on the same Ubuntu x86_64 runner with Go
1.27.1, GCC 13.3.0, and pinned libopus 1.6.1. Direct benchmarks use five
samples at GOMAXPROCS=4. Every measured benchmark reports 0 allocs/op. Values
below are medians with min–max sample ranges. The xcorr and SILK production
measurements include the surrounding pitch-search loop. Direct timing compares
the old assembly, ordinary scalar Go, and Go SIMD with `GOEXPERIMENT=simd`;
the `nosimd` mode uses the same scalar kernels and is included in the CBR and
quality comparison. Run 359303 measures the eight AMD64 inventory wrappers
with the corrected `b.Loop` harness; the reciprocal and best-ID cases vary
inputs and observe outputs on each iteration.
Rosetta results are not treated as native AMD64 evidence.

## Per-symbol inventory

`old path` names the assembly file in `origin/master`. `0` in the allocation
column is limited to directly measured kernels.

Direct timing covers all 51 comparable routines. The two startup
feature-discovery helpers have no
comparable per-call Go operation and are marked n/a with the reason.

| # | Former assembly symbol | Old arch | Go replacement source | Replacement path | Measured timing (ns/op) | Allocs/op | Status |
|---:|---|---|---|---|---|---|---|
| 1 | `combFilterConstNeon` | arm64 | `internal/celt/comb_const_simd_arm64.go`; `internal/celt/comb_const_default.go` | archsimd / scalar | N=480: old asm → Go → SIMD: 96.89 (95.42–138.80) → 617.7 (592.7–623.2) → 139.9 (133.8–154.1) | 0 | measured; SIMD is 44% slower than asm, scalar Go 6.4× slower |
| 2 | `cwrsiFastCore` | arm64 | `internal/celt/cwrs_fast_default.go` | scalar Go | N=48, K=12: old asm → Go → SIMD build: 48.25 (48.08–49.62) → 69.16 (66.31–79.03) → 68.73 (67.55–79.83) | 0 | measured; scalar replacement 43% slower than asm |
| 3 | `deemphasisStereoPlanarF32Core` | arm64 | `internal/celt/deemphasis_f32_default.go` | scalar Go | N=480 stereo: old asm → Go → SIMD build: 1,065 (1,065–1,068) → 1,167 (1,166–1,169) → 1,168 (1,166–1,177) | 0 | measured; scalar replacement 10% slower than asm |
| 4 | `expRotation1PassNeon` | arm64 | `internal/celt/exp_rotation_simd_arm64.go`; `internal/celt/exp_rotation_default.go` | archsimd / scalar | old asm → Go → SIMD: len32/stride1 284.3 (283.6–289.3) → 286.9 (285.8–288.9) → 282.6 (280.6–284.3); len64/stride1 583.2 (581.0–584.1) → 579.7 (574.3–584.2) → 578.2 (574.5–592.8); len32/stride2 138.1 (136.6–145.2) → 148.8 (142.6–152.1) → 159.5 (155.8–167.2); len32/stride4 85.03 (84.45–85.53) → 80.54 (80.13–83.10) → 84.20 (83.98–84.38) | 0 | measured; SIMD near assembly except stride2 slower |
| 5 | `haar1Stride1NEON` | arm64 | `internal/celt/haar1_simd_arm64.go`; `internal/celt/haar1_neon_default.go` | archsimd / scalar | N=32, old asm → Go → SIMD: 9.865 (9.791–14.28) → 14.37 (14.34–14.47) → 8.095 (8.057–8.133) | 0 | measured; SIMD 18% faster than asm median; asm range is noisy |
| 6 | `haar1Stride2NEON` | arm64 | `internal/celt/haar1_simd_arm64.go`; `internal/celt/haar1_neon_default.go` | archsimd / scalar | N=32, old asm → Go → SIMD: 18.73 (14.25–20.18) → 25.88 (25.56–26.26) → 10.59 (10.55–10.80) | 0 | measured; SIMD 43% faster than asm median; asm range is noisy |
| 7 | `haar1Stride4NEON` | arm64 | `internal/celt/haar1_simd_arm64.go`; `internal/celt/haar1_neon_default.go` | archsimd / scalar | N=32, old asm → Go → SIMD: 14.47 (14.40–14.63) → 41.43 (41.35–41.53) → 17.70 (17.64–17.72) | 0 | measured; SIMD 22% slower than asm |
| 8 | `imdctPostRotateF32FromKiss` | arm64 | `internal/celt/imdct_post_kiss_simd_arm64.go`; `internal/celt/imdct_post_kiss_default.go` | archsimd / scalar | N=120: old asm → Go → SIMD: 57.00 (56.43–60.80) → 56.72 (56.41–57.17) → 57.09 (56.93–57.23) | 0 | measured; SIMD within 0.2% of asm |
| 9 | `imdctPreRotateFMA32Kiss` | arm64 | `internal/celt/imdct_pre_kiss_simd_arm64.go`; `internal/celt/imdct_pre_kiss_default.go` | archsimd / scalar | N=120: old asm → Go → SIMD: 19.47 (19.40–19.64) → 74.08 (73.89–74.25) → 26.52 (26.42–27.16) | 0 | measured; SIMD 36% slower than asm, scalar Go 3.8× slower |
| 10 | `imdctTDACWindowFMA32` | arm64 | `internal/celt/imdct_tdac_simd_arm64.go`; `internal/celt/imdct_tdac_default.go` | archsimd / scalar | overlap=120/count=60: old asm → Go → SIMD: 24.96 (24.95–25.44) → 95.45 (95.02–96.67) → 25.22 (25.00–25.43) | 0 | measured; SIMD within 1.0% of asm |
| 11 | `celtInnerProd8FMA32` | arm64 | `internal/celt/inner_prod_fma_simd_arm64.go`; `internal/celt/inner_prod_fma_simd_amd64.go`; `internal/celt/inner_prod_fma_default.go` | archsimd / scalar | N=16: 5.94–6.00 → 3.49; N=64: 20.94–21.00 → 6.13–6.43; N=176: 56.24–56.30 → 19.96–20.04 | 0 | measured; faster on M4 |
| 12 | `celtInnerProdSSEStyleAsm` | amd64 | `internal/celt/innerprod_sse_simd_amd64.go`; `internal/celt/innerprod_sse_default.go` | archsimd / scalar | N=480, old asm → Go → SIMD: 109.8 (109.7–109.9) → 450.8 (449.8–454.3) → 122.6 (121.3–123.0) | 0 | measured in run 359303; scalar Go 4.1× slower than asm, SIMD 12% slower |
| 13 | `kfBfly4M1Core` | arm64 | `internal/celt/kf_bfly4m1_default.go` | scalar Go | N=128: old asm → Go → SIMD build: 147.2 (142.0–168.0) → 250.9 (240.6–269.4) → 248.2 (243.5–255.5) | 0 | measured; scalar replacement 69% slower than asm |
| 14 | `kfBfly5Inner` | amd64 | `internal/celt/kf_bfly_default.go` | scalar Go | m=8, N=4, old asm → Go → SIMD build: 476.0 (475.1–477.0) → 990.2 (985.0–1,007) → 988.4 (986.3–1,004) | 0 | measured in run 359303; Go replacement 2.1× slower than asm |
| 15 | `kfBfly3Inner` | amd64 | `internal/celt/kf_bfly_default.go` | scalar Go | m=8, N=4, old asm → Go → SIMD build: 253.6 (253.6–258.1) → 425.6 (421.4–436.8) → 425.2 (419.6–428.8) | 0 | measured in run 359303; Go replacement 1.7× slower than asm |
| 16 | `kfBfly4Inner` | amd64 | `internal/celt/kf_bfly_default.go` | scalar Go | m=8, N=4, old asm → Go → SIMD build: 239.8 (238.4–240.8) → 532.5 (530.5–534.4) → 526.6 (523.2–534.3) | 0 | measured in run 359303; Go replacement 2.2× slower than asm |
| 17 | `kfBfly5Inner` | arm64 | `internal/celt/kf_bfly_default.go` | scalar Go | m=8, N=4: old asm → Go → SIMD build: 163.3 (159.5–175.0) → 273.3 (257.0–279.2) → 270.3 (265.3–274.0) | 0 | measured; scalar replacement 66% slower than asm |
| 18 | `kfBfly3Inner` | arm64 | `internal/celt/kf_bfly_default.go` | scalar Go | m=8, N=4: old asm → Go → SIMD build: 108.2 (104.7–119.5) → 129.2 (116.2–133.8) → 136.1 (123.9–137.7) | 0 | measured; scalar replacement 26% slower than asm |
| 19 | `kfBfly4Inner` | arm64 | `internal/celt/kf_bfly_default.go` | scalar Go | m=8, N=4: old asm → Go → SIMD build: 121.8 (114.7–124.7) → 144.8 (137.7–154.0) → 152.5 (143.9–177.6) | 0 | measured; scalar replacement 25% slower than asm |
| 20 | `l1AbsSumNeon` | arm64 | `internal/celt/l1_abs_sum_simd_arm64.go`; `internal/celt/l1_abs_sum_default.go` | archsimd / scalar | N=480: old asm → Go → SIMD: 123.2 (122.4–123.3) → 493.0 (475.0–529.3) → 50.59 (50.48–50.78) | 0 | measured; SIMD 59% faster than asm, scalar Go 4.0× slower |
| 21 | `mdctFold1StoreNeon` | arm64 | `internal/celt/mdct_fold_simd_arm64.go`; `internal/celt/mdct_fold_default.go` | archsimd / scalar | n4=64, blocks=8: old asm → Go → SIMD: 35.84 (35.81–35.95) → 153.6 (153.4–153.7) → 52.74 (52.68–52.84) | 0 | measured; SIMD is 47% slower than asm, scalar Go 4.3× slower |
| 22 | `mdctFold3StoreNeon` | arm64 | `internal/celt/mdct_fold_simd_arm64.go`; `internal/celt/mdct_fold_default.go` | archsimd / scalar | n4=64, blocks=8: old asm → Go → SIMD: 35.78 (35.75–35.80) → 154.9 (154.8–155.5) → 52.06 (52.02–52.22) | 0 | measured; SIMD is 46% slower than asm, scalar Go 4.3× slower |
| 23 | `mdctMidFoldStoreNeon` | arm64 | `internal/celt/mdct_mid_fold_simd_arm64.go`; `internal/celt/mdct_mid_fold_default.go` | archsimd / scalar | n4=64, blocks=8: old asm → Go → SIMD: 19.47 (19.45–19.47) → 109.4 (109.3–109.6) → 36.15 (35.97–37.20) | 0 | measured; SIMD is 86% slower than asm, scalar Go 5.6× slower |
| 24 | `mdctPostTwiddleNeon` | arm64 | `internal/celt/mdct_post_twiddle_simd_arm64.go`; `internal/celt/mdct_post_twiddle_default.go` | archsimd / scalar | n4=64, pairBlocks=8: old asm → Go → SIMD: 18.65 (18.60–18.71) → 103.4 (103.3–103.9) → 23.31 (23.30–23.36) | 0 | measured; SIMD is 25% slower than asm, scalar Go 5.5× slower |
| 25 | `xcorrKernelAVX8` | amd64 | `internal/celt/pitch_xcorr_kernel_simd_amd64.go`; `internal/silk/pitch_xcorr_kernel_simd_amd64.go` | archsimd / scalar | CELT production A/B, old asm → scalar Go → Go SIMD: L×P=240×360 3,076→44,484→12,065; 480×64 1,034→15,788→3,332; 5×244 335.9→967.5→469.0; 10×10 37.61→76.22→156.5. SILK pitch search 120×300: 1,747→16,716→7,770. | 0 | measured in run 359303; Go SIMD trails old asm, including tiny production shapes |
| 26 | `prefilterDualInnerProdAsm` | arm64 | `internal/celt/prefilter_dual_inner_prod_simd_arm64.go`; default and nosimd variants | archsimd / scalar | N=240, old asm → Go → SIMD: 85.35 (85.08–86.27) → 237.0 (236.7–238.7) → 41.24 (41.10–41.56) | 0 | measured; SIMD 52% faster than asm; scalar Go 2.8× slower |
| 27 | `pvqSearchPulseLoopAVX` | amd64 | `internal/celt/pvq_search.go`; `internal/celt/pvq_search_default.go` | scalar Go | N=48, pulses=16, old asm → Go → SIMD: 646.3 (643.5–658.8) → 1,120 (1,109–1,447) → 1,218 (1,205–1,240) | 0 | measured in run 359303; Go SIMD build 1.9× slower than asm |
| 28 | `pvqSearchPulseLoop` | arm64 | `internal/celt/pvq_search.go`; `internal/celt/pvq_search_default.go` | scalar Go | N=48, pulses=16: old asm → Go → SIMD build: 552.6 (516.9–567.2) → 997.9 (964.7–1,006) → 1,013 (994.8–1,024) | 0 | measured; scalar replacement 81% slower than asm |
| 29 | `x86RcpApprox4` | amd64 | `internal/celt/pvq_search_x86_sse2.go` | archsimd | four varying lanes, old asm → Go SIMD: 2.495 (2.494–2.507) → 1.560 (1.559–1.564) | 0 | measured in run 359303; Go SIMD 37% faster than asm; no ordinary-Go direct equivalent |
| 30 | `x86PVQSearchBestIDSSE2` | amd64 | `internal/celt/pvq_search_x86_sse2.go` | archsimd | N=48, varying data, old asm → Go SIMD: 26.78 (26.76–26.79) → 29.21 (29.13–29.47) | 0 | measured in run 359303; Go SIMD 9% slower than asm; no ordinary-Go direct equivalent |
| 31 | `scaleFloat32IntoNEON` | arm64 | `internal/celt/scale_into_simd_arm64.go`; `internal/celt/scale_into_default.go` | archsimd / scalar | old asm → Go → SIMD: N=16 3.286 (3.274–3.301) → 7.656 (7.618–7.791) → 2.716 (2.703–2.719); N=64 7.715 (7.689–7.741) → 27.34 (26.86–29.65) → 5.107 (5.038–5.152); N=176 16.31 (16.18–16.41) → 80.68 (80.36–81.15) → 11.52 (11.40–11.53); N=480 33.87 (33.69–34.02) → 200.7 (200.1–201.0) → 26.73 (26.27–26.77) | 0 | measured; SIMD 17–34% faster than asm, scalar Go 2.3–5.0× slower |
| 32 | `stereoMergeRescaleNEON` | arm64 | `internal/celt/stereo_merge_simd_arm64.go`; `internal/celt/stereo_merge_default.go` | archsimd / scalar | old asm → Go → SIMD: N=16 8.392 (8.366–8.427) → 12.79 (12.66–12.92) → 6.203 (6.169–6.215); N=64 17.61 (17.52–17.70) → 45.57 (45.49–46.16) → 12.05 (11.99–12.07); N=176 31.89 (31.78–31.91) → 121.7 (121.6–122.0) → 27.20 (27.08–27.30); N=480 71.40 (71.05–71.49) → 329.1 (328.5–329.5) → 70.09 (69.60–73.70) | 0 | measured; SIMD 2–32% faster than asm; scalar Go 1.5–4.6× slower |
| 33 | `toneLPCCorrAVXFMA` | amd64 | `internal/celt/tone_lpc_corr_default.go`; `internal/celt/amd64_dispatch_helpers.go` | Go lane helper / scalar | N=480, delays=1/2, old asm → Go → SIMD: 586.1 (586.1–588.7) → 452.0 (451.4–452.3) → 453.1 (451.9–458.0) | 0 | measured in run 359303; Go replacement 23% faster than asm |
| 34 | `toneLPCCorr` | arm64 | `internal/celt/tone_lpc_corr_default.go` | scalar Go | cnt=480, delays=1/2: old asm → Go → SIMD build: 162.3 (162.2–162.7) → 559.0 (558.3–559.5) → 560.0 (559.3–562.7) | 0 | measured; scalar replacement 3.5× slower than asm |
| 35 | `xcorrKernel4Float32Neon4Acc` | arm64 | `internal/celt/xcorr_kernel_f32_4acc_simd_arm64.go`; `internal/celt/xcorr_kernel_f32_default.go` | archsimd / scalar | N=480: old asm → Go → SIMD: 189.2 (186.9–190.2) → 699.3 (697.0–699.9) → 167.8 (167.5–167.9) | 0 | measured; SIMD 11% faster than asm; scalar Go 3.7× slower |
| 36 | `cpuid` | amd64 | Go runtime CPU feature flags used by `simd/archsimd` | startup feature discovery | n/a | n/a | not comparable; the old helper returns raw CPUID registers, while Go dispatch consumes cached feature flags and has no per-call replacement |
| 37 | `xgetbv` | amd64 | Go runtime CPU feature flags used by `simd/archsimd` | startup feature discovery | n/a | n/a | not comparable; the old helper reads OS vector state during initialization, while Go dispatch consumes cached feature flags and has no per-call replacement |
| 38 | `reciprocalEstimate32` | arm64 | `internal/dnnmath/reciprocal_estimate_default.go` | scalar Go | input set of 64 normal float32 values, old asm → Go → SIMD build: 1.405 (1.398–1.428) → 2.021 (1.918–2.285) → 1.932 (1.915–2.308) | 0 | measured; Go emulation is 44% slower than FRECPE asm; SIMD build uses the same scalar routine |
| 39 | `fma32` | arm64 | `internal/lpcnetplc/fma32_arm64.go`; `internal/lpcnetplc/fma32_default.go` | Go float32 expression / scalar | 64 varying input triples, old asm → Go → SIMD build: 1.915 (1.913–1.916) → 0.5506 (0.5505–0.5517) → 0.5491 (0.5489–0.5509) | 0 | measured; Go FMADD expression is 3.5× faster than the out-of-line asm call |
| 40 | `gruFMA32` | arm64 | `internal/osce/lace/gru_fma_arm64.go`; `internal/osce/lace/gru_fma_default.go` | Go float32 expression / scalar | 64 varying input triples, old asm → Go → SIMD build: 1.914 (1.912–1.916) → 0.5497 (0.5494–0.5504) → 0.5502 (0.5488–0.5515) | 0 | measured; Go FMADD expression is 3.5× faster than the out-of-line asm call |
| 41 | `floatToInt16ScaledCore` | arm64 | `internal/silk/convert_simd_arm64.go`; `internal/silk/float_to_int16_default.go` | archsimd / scalar | N=480: old asm → Go → SIMD: 34.96 (34.95–50.73) → 489.8 (484.9–513.3) → 52.03 (51.95–52.22) | 0 | measured; SIMD is 49% slower than asm, scalar Go 14× slower |
| 42 | `innerProductFLPAVX2` | amd64 | `internal/silk/inner_product_flp_simd_amd64.go`; `internal/silk/inner_product_flp_amd64.go` | archsimd / scalar | N=480, old asm → scalar Go → Go SIMD: 97.77 (97.56–98.58) → 252.4 (252.2–252.5) → 195.5 (195.3–197.5) | 0 | measured in run 359117; SIMD improves over scalar Go by 22.5% but is 2.0× slower than old asm |
| 43 | `innerProductFLPArm64` | arm64 | `internal/silk/inner_product_flp_arm64.go` | scalar Go | N=480: old asm → Go → SIMD build: 126.6 (126.5–126.7) → 205.5 (205.3–212.8) → 201.6 (200.8–211.3) | 0 | measured; scalar replacement 62% slower than asm |
| 44 | `writeInt16AsFloat32Core` | arm64 | `internal/silk/convert_simd_arm64.go`; `internal/silk/int16_float32_default.go` | archsimd / scalar | N=480: old asm → Go → SIMD: 38.87 (38.66–39.76) → 216.4 (216.1–217.3) → 49.43 (49.40–49.80) | 0 | measured; SIMD is 27% slower than asm, scalar Go 5.6× slower |
| 45 | `synthesizeLPCOrder16Core` | arm64 | `internal/silk/lpc_synth_default.go` | scalar Go | subframe=80: old asm → Go → SIMD build: 299.4 (298.4–318.2) → 505.1 (504.4–508.9) → 503.7 (503.2–509.7) | 0 | measured; scalar replacement 68% slower than asm |
| 46 | `celtPitchXcorrFloatImplASM` | arm64 | `internal/silk/pitch_xcorr_impl_default.go`; amd64 SIMD implementation | scalar arm64 / archsimd amd64 | length=240, maxPitch=120: old asm → Go → SIMD build: 5,543 (5,514–6,136) → 13,292 (13,279–13,581) → 13,276 (13,146–13,408) | 0 | measured; arm64 scalar replacement 2.4× slower than asm |
| 47 | `xcorrKernelAVX8` | amd64 | `internal/silk/pitch_xcorr_kernel_simd_amd64.go`; `internal/silk/pitch_xcorr_kernel_avx_amd64.go` | archsimd / scalar | SILK pitch search L×P=120×300, old asm → scalar Go → Go SIMD: 1,747 (1,746–1,766) → 16,716 (16,690–16,819) → 7,770 (7,755–7,775) | 0 | measured in run 359303; SIMD is 2.2× faster than scalar Go and 4.4× slower than old asm |
| 48 | `firInterpol21846Core` | arm64 | `internal/silk/resample_fir_default.go`; `internal/silk/resample_libopus.go` | scalar Go | nOut=240: old asm → Go → SIMD build: 158.2 (158.0–158.5) → 415.8 (414.6–419.1) → 415.9 (412.2–416.3) | 0 | measured; scalar replacement 2.6× slower than asm |
| 49 | `firInterpol32768Core` | arm64 | `internal/silk/resample_fir_default.go`; `internal/silk/resample_libopus.go` | scalar Go | nOut=240: old asm → Go → SIMD build: 182.7 (182.5–275.0) → 434.8 (430.0–438.5) → 429.5 (427.7–431.5) | 0 | measured; scalar replacement 2.4× slower than asm |
| 50 | `firInterpol43691Core` | arm64 | `internal/silk/resample_fir_default.go`; `internal/silk/resample_libopus.go` | scalar Go | nOut=240: old asm → Go → SIMD build: 161.5 (161.2–162.1) → 457.3 (457.1–461.9) → 454.9 (451.7–455.8) | 0 | measured; scalar replacement 2.8× slower than asm |
| 51 | `up2HQCore` | arm64 | `internal/silk/up2hq_core_default.go`; `internal/silk/resample_libopus.go` | scalar Go | N=240: old asm → Go → SIMD build: 1,120 (1,096–1,176) → 1,133 (1,126–1,137) → 1,166 (1,154–1,171) | 0 | measured; scalar and SIMD Go are within 4% of asm |
| 52 | `convertFloat32ToInt16UnitBlocks` | arm64 | `pcm_convert_simd_arm64.go`; `pcm_convert_arm64_nosimd.go` | archsimd / scalar | n=480, old wrapper/asm → Go → SIMD: 92.95 (92.87–93.35) → 630.1 (618.2–700.6) → 154.9 (154.7–155.1) | 0 | measured; SIMD is 67% slower than asm, scalar Go 6.8× slower |
| 53 | `convertFloat32ToInt16SaturatingBlocks` | arm64 | `pcm_convert_simd_arm64.go`; `pcm_convert_arm64_nosimd.go` | archsimd / scalar | n=480, old wrapper/asm → Go → SIMD: 45.43 (45.39–45.66) → 646.5 (639.8–658.0) → 102.8 (102.5–103.0) | 0 | measured; SIMD is 2.3× slower than asm, scalar Go 14.2× slower |

## Native AMD64 parity and quality comparison

The same run executes the full CBR packet matrix and precision case for each
mode. Mode-matched old assembly and Go SIMD have the same per-row CBR mismatch
counts: 13 rows fail and six pass. Ordinary Go and `nosimd` are compared
with scalar libopus and report no failing rows (six and four residual rows,
respectively). The precision guard is also mode-matched: old assembly and Go
SIMD both score Q=32.16 against SIMD libopus Q=32.16; scalar Go scores Q=31.58
against scalar libopus Q=32.16, below the unchanged −0.05 floor. The strict
CBR and quality gates remain blocking while the residuals are diagnosed.
The focused hybrid decode differential reports the same two failing frames,
sample index, and 2.0213886e-34 worst difference for old assembly and Go SIMD
on the same native AMD64 runner in run 359303.

| CBR case | Old asm and Go SIMD differing packets |
|---|---:|
| SILK-NB-20ms-mono-16k | 21/50 |
| CELT-FB-2p5ms-mono-64k | 37/400 |
| CELT-FB-2p5ms-stereo-128k | 26/400 |
| CELT-FB-5ms-mono-64k | 96/200 |
| CELT-FB-5ms-stereo-128k | 2/200 |
| CELT-FB-10ms-mono-64k | 7/100 |
| CELT-FB-20ms-mono-64k | 17/50 |
| CELT-FB-20ms-stereo-128k | 29/50 |
| Hybrid-SWB-10ms-mono-48k | 4/100 |
| Hybrid-SWB-20ms-mono-48k | 10/50 |
| Hybrid-FB-10ms-mono-64k | 16/100 |
| Hybrid-FB-20ms-mono-64k | 9/50 |
| Hybrid-FB-20ms-stereo-96k | 8/50 |

For `Hybrid-FB-20ms-stereo-96k`, old assembly and Go SIMD match SIMD libopus
at Q=32.16. Scalar Go records Q=31.58 (gap −0.58), below the unchanged −0.05
floor.

Direct AMD64 benchmark samples for the relevant production paths are:

| Benchmark case | Old assembly | Ordinary Go | Go SIMD |
|---|---:|---:|---:|
| CELT xcorr, L×P=240×360 | 3,076 (3,070–3,129) ns | 44,484 (44,350–44,526) ns | 12,065 (12,058–12,072) ns |
| CELT xcorr, L×P=480×64 | 1,034 (1,034–1,035) ns | 15,788 (15,768–15,801) ns | 3,332 (3,329–3,341) ns |
| CELT xcorr, L×P=5×244 | 335.9 (333.0–337.7) ns | 967.5 (965.2–980.5) ns | 469.0 (468.7–470.8) ns |
| CELT xcorr, L×P=10×10 | 37.61 (37.25–37.71) ns | 76.22 (76.11–76.48) ns | 156.5 (156.4–156.7) ns |
| SILK inner product, N=480 | 97.77 (97.56–98.58) ns | 252.4 (252.2–252.5) ns | 195.5 (195.3–197.5) ns |
| SILK pitch xcorr, L×P=120×300 | 1,747 (1,746–1,766) ns | 16,716 (16,690–16,819) ns | 7,770 (7,755–7,775) ns |

The native SIMD run includes the short CELT xcorr vector path. Its 5×244
production case improves from 3,977 ns in run 359117 to 469 ns in run 359303,
but remains 40% slower than old assembly. The 10×10 case remains 4.2× slower.
The long CELT and SILK cases remain 3.2–4.5× slower than old assembly.

`celtInnerProd8FMA32` also improves over the AMD64 base scalar path, which has
no matching AMD64 assembly symbol: N=16 20.80→6.561 ns, N=64 70.87→13.53 ns,
and N=176 187.9→37.17 ns. These values do not replace its arm64 asm comparison.
All measurements in the production-path table report 0 allocs/op.

## Measurement follow-up

The corrected native `b.Loop` A/B measures reciprocal and best-ID directly.
The AMD64 harness uses varied input fixtures and observed outputs; the PVQ
pulse-loop fixture ring resets every 64 calls inside the timed loop, equally on
baseline and candidate. Butterfly kernels use stable zero-valued runtime input
buffers so repeated calls stay finite and execute the same arithmetic.

Native A/B shows xcorr SIMD improves over scalar Go at large sizes but stays
slower than old assembly. The pre-rotate slowdown and large-size stereo-merge slowdown remain
optimization items. No overall performance gain is claimed from these kernel
samples.
