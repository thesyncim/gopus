# Go kernel replacement evidence

This report tracks all 53 symbols from the 41 assembly files in the pre-port
`origin/master` tree. Every entry has a Go replacement. `archsimd` means the
Go 1.27 `simd/archsimd` implementation selected by `GOEXPERIMENT=simd`; ordinary
builds use the listed scalar Go path, and `-tags nosimd` forces the scalar
reference path.

## Measurement method

The available direct A/B measurements used an M4 Max (`darwin/arm64`) and Go
1.27.1, with the pre-port assembly and replacement benchmarked on the same host
and toolchain. The measurements were repeated three times. Reported direct
benchmarks had 0 allocs/op. The table records ranges across the observed runs;
rows without such a measurement remain pending. This is kernel-only evidence;
end-to-end encode/decode timing and native AMD64 timings are pending. Rosetta
results are not treated as native AMD64 performance evidence.

## Per-symbol inventory

`old path` names the assembly file in `origin/master`. A `pending` timing is not
a performance claim. `0` in the allocation column is limited to directly
measured kernels; other rows need a direct allocation measurement.

| # | Former assembly symbol | Old arch | Go replacement source | Replacement path | Old asm → Go (ns/op) | Allocs/op | Status |
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
| 25 | `xcorrKernelAVX8` | amd64 | `internal/celt/pitch_xcorr_kernel_simd_amd64.go`; `internal/silk/pitch_xcorr_kernel_avx_amd64.go` | archsimd / scalar | 187.8 → 167–208 (N=480; variable across runs) | 0 | preliminary; rerun and report median/range |
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
| 42 | `innerProductFLPAVX2` | amd64 | `internal/silk/inner_product_flp_simd_amd64.go`; `internal/silk/inner_product_flp_amd64.go` | archsimd / scalar | pending | pending | SIMD port; benchmark pending |
| 43 | `innerProductFLPArm64` | arm64 | `internal/silk/inner_product_flp_arm64.go` | scalar Go | pending | pending | scalar replacement; SIMD port and benchmark pending |
| 44 | `writeInt16AsFloat32Core` | arm64 | `internal/silk/convert_simd_arm64.go`; `internal/silk/int16_float32_default.go` | archsimd / scalar | pending | pending | SIMD port; benchmark pending |
| 45 | `synthesizeLPCOrder16Core` | arm64 | `internal/silk/lpc_synth_default.go` | scalar Go | pending | pending | scalar replacement; SIMD port and benchmark pending |
| 46 | `celtPitchXcorrFloatImplASM` | arm64 | `internal/silk/pitch_xcorr_impl_default.go`; amd64 SIMD implementation | scalar arm64 / archsimd amd64 | pending | pending | arm64 SIMD port and benchmark pending |
| 47 | `xcorrKernelAVX8` | amd64 | `internal/silk/pitch_xcorr_kernel_simd_amd64.go`; `internal/silk/pitch_xcorr_kernel_avx_amd64.go` | archsimd / scalar | pending | pending | SIMD port; benchmark pending |
| 48 | `firInterpol21846Core` | arm64 | `internal/silk/resample_fir_default.go`; `internal/silk/resample_libopus.go` | scalar Go | pending | pending | scalar replacement; SIMD port and benchmark pending |
| 49 | `firInterpol32768Core` | arm64 | `internal/silk/resample_fir_default.go`; `internal/silk/resample_libopus.go` | scalar Go | pending | pending | scalar replacement; SIMD port and benchmark pending |
| 50 | `firInterpol43691Core` | arm64 | `internal/silk/resample_fir_default.go`; `internal/silk/resample_libopus.go` | scalar Go | pending | pending | scalar replacement; SIMD port and benchmark pending |
| 51 | `up2HQCore` | arm64 | `internal/silk/up2hq_core_default.go`; `internal/silk/resample_libopus.go` | scalar Go | pending | pending | scalar replacement; SIMD port and benchmark pending |
| 52 | `convertFloat32ToInt16UnitBlocks` | arm64 | `pcm_convert_simd_arm64.go`; `pcm_convert_default.go` | archsimd / scalar | pending | pending | SIMD port; benchmark pending |
| 53 | `convertFloat32ToInt16SaturatingBlocks` | arm64 | `pcm_convert_simd_arm64.go`; `pcm_convert_default.go` | archsimd / scalar | pending | pending | SIMD port; benchmark pending |

## Measurement follow-up

The pending rows need a reproducible benchmark matrix that builds the pre-port
assembly at the same Go version and host as the replacement. Native AMD64 must
run on an AMD64 runner. Each routine needs representative sizes, a zero-allocation
measurement, and generated-code inspection where the replacement uses
`archsimd`. The pre-rotate slowdown and large-size stereo-merge slowdown remain
optimization items; no overall performance gain is claimed from these kernel
samples alone.
