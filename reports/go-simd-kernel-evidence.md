# Go kernel replacement evidence

This report tracks all 53 symbols from the 41 assembly files in the pre-port
`origin/master` tree. Every entry has a Go replacement. `archsimd` means the
Go 1.27 `simd/archsimd` implementation selected by `GOEXPERIMENT=simd`; ordinary
builds use the listed scalar Go path, and `-tags nosimd` forces the scalar
reference path.

## Measurement method

M4 Max (`darwin/arm64`) A/B measurements use Go 1.27.1 and three runs on the
same host. Native AMD64 A/B measurements come from [CI run
35911787670](https://github.com/thesyncim/gopus/actions/runs/35911787670): the
pre-port base and candidate ran on the same Ubuntu x86_64 runner with Go
1.27.1, GCC 13.3.0, and pinned libopus 1.6.1. Direct benchmarks use five
samples at GOMAXPROCS=4. Every measured benchmark reports 0 allocs/op. Values
below are medians with min–max sample ranges. The xcorr and SILK production
measurements include the surrounding pitch-search loop. Direct timing compares
the old assembly, ordinary scalar Go, and Go SIMD with `GOEXPERIMENT=simd`;
the `nosimd` mode uses the same scalar kernels and is included in the CBR and
quality comparison. Rows without a comparable measurement remain pending.
Rosetta results are not treated as native AMD64 evidence.

## Per-symbol inventory

`old path` names the assembly file in `origin/master`. A `pending` timing is not
a performance claim. `0` in the allocation column is limited to directly
measured kernels; other rows need a direct allocation measurement.

| # | Former assembly symbol | Old arch | Go replacement source | Replacement path | Measured timing (ns/op) | Allocs/op | Status |
|---:|---|---|---|---|---|---|---|
| 1 | `combFilterConstNeon` | arm64 | `internal/celt/comb_const_simd_arm64.go`; `internal/celt/comb_const_default.go` | archsimd / scalar | pending | pending | SIMD port; benchmark pending |
| 2 | `cwrsiFastCore` | arm64 | `internal/celt/cwrs_fast_default.go` | scalar Go | pending | pending | scalar replacement; benchmark pending |
| 3 | `deemphasisStereoPlanarF32Core` | arm64 | `internal/celt/deemphasis_f32_default.go` | scalar Go | pending | pending | scalar replacement; benchmark pending |
| 4 | `expRotation1PassNeon` | arm64 | `internal/celt/exp_rotation_simd_arm64.go`; `internal/celt/exp_rotation_default.go` | archsimd / scalar | pending | pending | SIMD port; benchmark pending |
| 5 | `haar1Stride1NEON` | arm64 | `internal/celt/haar1_simd_arm64.go`; `internal/celt/haar1_neon_default.go` | archsimd / scalar | pending | pending | SIMD port; benchmark pending |
| 6 | `haar1Stride2NEON` | arm64 | `internal/celt/haar1_simd_arm64.go`; `internal/celt/haar1_neon_default.go` | archsimd / scalar | pending | pending | SIMD port; benchmark pending |
| 7 | `haar1Stride4NEON` | arm64 | `internal/celt/haar1_simd_arm64.go`; `internal/celt/haar1_neon_default.go` | archsimd / scalar | pending | pending | SIMD port; benchmark pending |
| 8 | `imdctPostRotateF32FromKiss` | arm64 | `internal/celt/imdct_post_kiss_simd_arm64.go`; `internal/celt/imdct_post_kiss_default.go` | archsimd / scalar | 56.3 → 57.0 (N=60) | 0 | measured; 1.2% slower |
| 9 | `imdctPreRotateFMA32Kiss` | arm64 | `internal/celt/imdct_pre_kiss_simd_arm64.go`; `internal/celt/imdct_pre_kiss_default.go` | archsimd / scalar | 19.2 → 26.6 (N=60) | 0 | measured; 38.5% slower, needs optimization |
| 10 | `imdctTDACWindowFMA32` | arm64 | `internal/celt/imdct_tdac_simd_arm64.go`; `internal/celt/imdct_tdac_default.go` | archsimd / scalar | 24.9 → 25.2 (shape not retained) | 0 | preliminary measurement; rerun with case details |
| 11 | `celtInnerProd8FMA32` | arm64 | `internal/celt/inner_prod_fma_simd_arm64.go`; `internal/celt/inner_prod_fma_simd_amd64.go`; `internal/celt/inner_prod_fma_default.go` | archsimd / scalar | N=16: 5.94–6.00 → 3.49; N=64: 20.94–21.00 → 6.13–6.43; N=176: 56.24–56.30 → 19.96–20.04 | 0 | measured; faster on M4 |
| 12 | `celtInnerProdSSEStyleAsm` | amd64 | `internal/celt/innerprod_sse_simd_amd64.go`; `internal/celt/innerprod_sse_default.go` | archsimd / scalar | pending | pending | SIMD port; benchmark pending |
| 13 | `kfBfly4M1Core` | arm64 | `internal/celt/kf_bfly4m1_default.go` | scalar Go | pending | pending | scalar replacement; benchmark pending |
| 14 | `kfBfly5Inner` | amd64 | `internal/celt/kf_bfly_default.go` | scalar Go | pending | pending | scalar replacement; benchmark pending |
| 15 | `kfBfly3Inner` | amd64 | `internal/celt/kf_bfly_default.go` | scalar Go | pending | pending | scalar replacement; benchmark pending |
| 16 | `kfBfly4Inner` | amd64 | `internal/celt/kf_bfly_default.go` | scalar Go | pending | pending | scalar replacement; benchmark pending |
| 17 | `kfBfly5Inner` | arm64 | `internal/celt/kf_bfly_default.go` | scalar Go | pending | pending | scalar replacement; benchmark pending |
| 18 | `kfBfly3Inner` | arm64 | `internal/celt/kf_bfly_default.go` | scalar Go | pending | pending | scalar replacement; benchmark pending |
| 19 | `kfBfly4Inner` | arm64 | `internal/celt/kf_bfly_default.go` | scalar Go | pending | pending | scalar replacement; benchmark pending |
| 20 | `l1AbsSumNeon` | arm64 | `internal/celt/l1_abs_sum_simd_arm64.go`; `internal/celt/l1_abs_sum_default.go` | archsimd / scalar | pending | pending | SIMD port; benchmark pending |
| 21 | `mdctFold1StoreNeon` | arm64 | `internal/celt/mdct_fold_simd_arm64.go`; `internal/celt/mdct_fold_default.go` | archsimd / scalar | pending | pending | SIMD port; benchmark pending |
| 22 | `mdctFold3StoreNeon` | arm64 | `internal/celt/mdct_fold_simd_arm64.go`; `internal/celt/mdct_fold_default.go` | archsimd / scalar | pending | pending | SIMD port; benchmark pending |
| 23 | `mdctMidFoldStoreNeon` | arm64 | `internal/celt/mdct_mid_fold_simd_arm64.go`; `internal/celt/mdct_mid_fold_default.go` | archsimd / scalar | pending | pending | SIMD port; benchmark pending |
| 24 | `mdctPostTwiddleNeon` | arm64 | `internal/celt/mdct_post_twiddle_simd_arm64.go`; `internal/celt/mdct_post_twiddle_default.go` | archsimd / scalar | pending | pending | SIMD port; benchmark pending |
| 25 | `xcorrKernelAVX8` | amd64 | `internal/celt/pitch_xcorr_kernel_simd_amd64.go`; `internal/silk/pitch_xcorr_kernel_simd_amd64.go` | archsimd / scalar | CELT production A/B, old asm → scalar Go → Go SIMD: L×P=240×360 3,080→44,374→12,323; 480×64 1,035→15,784→3,340; 5×244 331.1→967.3→3,977; 10×10 38.78→73.94→252.7. SILK pitch search 120×300: 1,746→16,713→7,788. | 0 | measured in run 359117; SIMD beats scalar Go for large inputs but trails old asm; tiny production shapes regress sharply |
| 26 | `prefilterDualInnerProdAsm` | arm64 | `internal/celt/prefilter_dual_inner_prod_simd_arm64.go`; default and nosimd variants | archsimd / scalar | 84.45–85.69 → 41.27–41.50 (N=240) | 0 | measured; faster on M4 |
| 27 | `pvqSearchPulseLoopAVX` | amd64 | `internal/celt/pvq_search.go`; `internal/celt/pvq_search_default.go` | scalar Go | pending | pending | scalar replacement; SIMD port and benchmark pending |
| 28 | `pvqSearchPulseLoop` | arm64 | `internal/celt/pvq_search.go`; `internal/celt/pvq_search_default.go` | scalar Go | pending | pending | scalar replacement; SIMD port and benchmark pending |
| 29 | `x86RcpApprox4` | amd64 | `internal/celt/pvq_search_x86_sse2.go` | scalar Go | pending | pending | scalar replacement; benchmark pending |
| 30 | `x86PVQSearchBestIDSSE2` | amd64 | `internal/celt/pvq_search_x86_sse2.go` | scalar Go | pending | pending | scalar replacement; SIMD port and benchmark pending |
| 31 | `scaleFloat32IntoNEON` | arm64 | `internal/celt/scale_into_simd_arm64.go`; `internal/celt/scale_into_default.go` | archsimd / scalar | N=16: 3.26 → 2.75; N=64: 7.62 → 5.11; N=176: 16.16 → 12.12; N=480: 33.5 → 26.4 | 0 | measured; faster on M4 |
| 32 | `stereoMergeRescaleNEON` | arm64 | `internal/celt/stereo_merge_simd_arm64.go`; `internal/celt/stereo_merge_default.go` | archsimd / scalar | N=16: 8.41 → 6.12; N=64: 17.46 → 11.98; N=176: 31.50 → 28.56; N=480: 70.96 → 73.24 | 0 | mixed; N=480 is 3.2% slower |
| 33 | `toneLPCCorrAVXFMA` | amd64 | `internal/celt/tone_lpc_corr_default.go`; `internal/celt/amd64_dispatch_helpers.go` | Go lane helper / scalar | pending | pending | SIMD port and benchmark pending |
| 34 | `toneLPCCorr` | arm64 | `internal/celt/tone_lpc_corr_default.go` | scalar Go | pending | pending | scalar replacement; SIMD port and benchmark pending |
| 35 | `xcorrKernel4Float32Neon4Acc` | arm64 | `internal/celt/xcorr_kernel_f32_4acc_simd_arm64.go`; `internal/celt/xcorr_kernel_f32_default.go` | archsimd / scalar | 187.8 → 167–208 (N=480; replacement varied across three runs) | 0 | preliminary; rerun direct benchmark and retain samples |
| 36 | `cpuid` | amd64 | `internal/cpufeat/cpufeat.go` | Go feature discovery | pending | pending | helper replacement; benchmark pending |
| 37 | `xgetbv` | amd64 | `internal/cpufeat/cpufeat.go` | Go feature discovery | pending | pending | helper replacement; benchmark pending |
| 38 | `reciprocalEstimate32` | arm64 | `internal/dnnmath/reciprocal_estimate_default.go` | scalar Go | pending | pending | scalar replacement; SIMD port and benchmark pending |
| 39 | `fma32` | arm64 | `internal/lpcnetplc/fma32_arm64.go`; `internal/lpcnetplc/fma32_default.go` | Go float32 expression / scalar | pending | pending | Go replacement; verify arm64 FMADD codegen and benchmark |
| 40 | `gruFMA32` | arm64 | `internal/osce/lace/gru_fma_arm64.go`; `internal/osce/lace/gru_fma_default.go` | Go float32 expression / scalar | pending | pending | Go replacement; verify arm64 FMADD codegen and benchmark |
| 41 | `floatToInt16ScaledCore` | arm64 | `internal/silk/convert_simd_arm64.go`; `internal/silk/float_to_int16_default.go` | archsimd / scalar | pending | pending | SIMD port; benchmark pending |
| 42 | `innerProductFLPAVX2` | amd64 | `internal/silk/inner_product_flp_simd_amd64.go`; `internal/silk/inner_product_flp_amd64.go` | archsimd / scalar | N=480, old asm → scalar Go → Go SIMD: 97.77 (97.56–98.58) → 252.4 (252.2–252.5) → 195.5 (195.3–197.5) | 0 | measured in run 359117; SIMD improves over scalar Go by 22.5% but is 2.0× slower than old asm |
| 43 | `innerProductFLPArm64` | arm64 | `internal/silk/inner_product_flp_arm64.go` | scalar Go | pending | pending | scalar replacement; SIMD port and benchmark pending |
| 44 | `writeInt16AsFloat32Core` | arm64 | `internal/silk/convert_simd_arm64.go`; `internal/silk/int16_float32_default.go` | archsimd / scalar | pending | pending | SIMD port; benchmark pending |
| 45 | `synthesizeLPCOrder16Core` | arm64 | `internal/silk/lpc_synth_default.go` | scalar Go | pending | pending | scalar replacement; SIMD port and benchmark pending |
| 46 | `celtPitchXcorrFloatImplASM` | arm64 | `internal/silk/pitch_xcorr_impl_default.go`; amd64 SIMD implementation | scalar arm64 / archsimd amd64 | pending | pending | arm64 SIMD port and benchmark pending |
| 47 | `xcorrKernelAVX8` | amd64 | `internal/silk/pitch_xcorr_kernel_simd_amd64.go`; `internal/silk/pitch_xcorr_kernel_avx_amd64.go` | archsimd / scalar | SILK pitch search L×P=120×300, old asm → scalar Go → Go SIMD: 1,746 (1,745–1,749) → 16,713 (16,690–16,807) → 7,788 (7,787–7,800) | 0 | measured in run 359117; SIMD is 2.1× faster than scalar Go and 4.5× slower than old asm |
| 48 | `firInterpol21846Core` | arm64 | `internal/silk/resample_fir_default.go`; `internal/silk/resample_libopus.go` | scalar Go | pending | pending | scalar replacement; SIMD port and benchmark pending |
| 49 | `firInterpol32768Core` | arm64 | `internal/silk/resample_fir_default.go`; `internal/silk/resample_libopus.go` | scalar Go | pending | pending | scalar replacement; SIMD port and benchmark pending |
| 50 | `firInterpol43691Core` | arm64 | `internal/silk/resample_fir_default.go`; `internal/silk/resample_libopus.go` | scalar Go | pending | pending | scalar replacement; SIMD port and benchmark pending |
| 51 | `up2HQCore` | arm64 | `internal/silk/up2hq_core_default.go`; `internal/silk/resample_libopus.go` | scalar Go | pending | pending | scalar replacement; SIMD port and benchmark pending |
| 52 | `convertFloat32ToInt16UnitBlocks` | arm64 | `pcm_convert_simd_arm64.go`; `pcm_convert_default.go` | archsimd / scalar | pending | pending | SIMD port; benchmark pending |
| 53 | `convertFloat32ToInt16SaturatingBlocks` | arm64 | `pcm_convert_simd_arm64.go`; `pcm_convert_default.go` | archsimd / scalar | pending | pending | SIMD port; benchmark pending |

## Native AMD64 parity and quality comparison

The same run executes the full CBR packet matrix and precision case for each
mode. Mode-matched old assembly and Go SIMD have the same per-row CBR mismatch
counts: 13 rows fail and six pass. Ordinary Go and `nosimd` are compared
with scalar libopus and report no failing rows (six and four residual rows,
respectively). The precision guard is also mode-matched: old assembly and Go
SIMD both score Q=32.16 against SIMD libopus Q=32.16; scalar Go scores Q=31.58
against scalar libopus Q=32.16, below the unchanged −0.05 floor. The strict
CBR and quality gates remain blocking while the residuals are diagnosed.

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
| CELT xcorr, L×P=240×360 | 3,080 (3,077–3,083) ns | 44,374 (44,350–44,427) ns | 12,323 (12,313–12,330) ns |
| CELT xcorr, L×P=480×64 | 1,035 (1,034–1,039) ns | 15,784 (15,768–15,797) ns | 3,340 (3,339–3,340) ns |
| CELT xcorr, L×P=5×244 | 331.1 (330.6–335.3) ns | 967.3 (965.5–969.5) ns | 3,977 (3,975–3,980) ns |
| CELT xcorr, L×P=10×10 | 38.78 (38.65–38.82) ns | 73.94 (73.84–73.96) ns | 252.7 (252.6–253.1) ns |
| SILK inner product, N=480 | 97.77 (97.56–98.58) ns | 252.4 (252.2–252.5) ns | 195.5 (195.3–197.5) ns |
| SILK pitch xcorr, L×P=120×300 | 1,746 (1,745–1,749) ns | 16,713 (16,690–16,807) ns | 7,788 (7,787–7,800) ns |

The native SIMD run includes the current N<16 scalar dispatch. Tiny end-to-end
shapes remain substantially slower than both old assembly and the ordinary Go
build. Direct `xcorrKernelAVX8` Go SIMD timings are N=5: 124.1 ns in CELT and
106.0 ns in SILK; N=10: 221.8 ns in CELT and 204.2 ns. These are close to the
ordinary Go path (123.0/104.0 ns at N=5; 221.1/202.0 ns at N=10), so the severe
tiny production regression comes from the wrapper/helper path and remains under
investigation.

`celtInnerProd8FMA32` also improves over the AMD64 base scalar path, which has
no matching AMD64 assembly symbol: N=16 20.80→6.561 ns, N=64 70.87→13.53 ns,
and N=176 187.9→37.17 ns. These values do not replace its arm64 asm comparison.
All measurements in the production-path table report 0 allocs/op.

## Measurement follow-up

The remaining unmeasured rows need a reproducible benchmark matrix that builds
the pre-port assembly at the same Go version and host as the replacement. Each
routine needs representative sizes, a zero-allocation measurement, and
generated-code inspection where the replacement uses `archsimd`. Native A/B
shows xcorr SIMD improves over scalar Go at large sizes but stays slower than
old assembly; tiny production shapes are especially slow even after dispatch.
The pre-rotate slowdown and large-size stereo-merge slowdown remain optimization
items. No overall performance gain is claimed from these kernel samples.
