# Go kernel replacement evidence

This report tracks all 53 symbols from the 41 assembly files in the pre-port
`origin/master` tree. Every entry has a Go replacement. `archsimd` means the
Go 1.27 `simd/archsimd` implementation selected by `GOEXPERIMENT=simd`; ordinary
builds use the listed scalar Go path, and `-tags nosimd` forces the scalar
reference path.

## Measurement method

M4 Max (`darwin/arm64`) A/B measurements use Go 1.27.1 and three runs on the
same host. Native AMD64 A/B measurements come from [CI run
35884494207](https://github.com/thesyncim/gopus/actions/runs/35884494207): both
the base snapshot and candidate ran on the same Ubuntu x86_64 runner with Go
1.27.1, `GOEXPERIMENT=simd`, GCC 13.3.0, and the pinned libopus 1.6.1 source.
The direct benchmarks use five samples at GOMAXPROCS=4. Every measured direct
benchmark reports 0 allocs/op. Values below are medians with min–max sample
ranges. The AMD64 xcorr and SILK measurements exercise the production path that
calls the named kernel; they include the surrounding pitch-search loop. Rows
without a comparable direct measurement remain pending. Rosetta results are
not treated as native AMD64 evidence.

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
| 25 | `xcorrKernelAVX8` | amd64 | `internal/celt/pitch_xcorr_kernel_simd_amd64.go`; `internal/silk/pitch_xcorr_kernel_avx_amd64.go` | archsimd / scalar | CELT Linux AMD64 production path: L×P=240×360 3,083→16,107 ns; 480×64 1,036→4,350 ns; 5×244 332→7,541 ns; 10×10 38.84→1,966 ns (median; sample ranges below) | 0 | measured; Go SIMD path is slower on all four shapes |
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
| 42 | `innerProductFLPAVX2` | amd64 | `internal/silk/inner_product_flp_simd_amd64.go`; `internal/silk/inner_product_flp_amd64.go` | archsimd / scalar | N=480, Linux AMD64: 97.69→195.1 ns/op (median; 97.60–100.3→194.8–197.0) | 0 | measured; Go SIMD path is 2.0× slower |
| 43 | `innerProductFLPArm64` | arm64 | `internal/silk/inner_product_flp_arm64.go` | scalar Go | pending | pending | scalar replacement; SIMD port and benchmark pending |
| 44 | `writeInt16AsFloat32Core` | arm64 | `internal/silk/convert_simd_arm64.go`; `internal/silk/int16_float32_default.go` | archsimd / scalar | pending | pending | SIMD port; benchmark pending |
| 45 | `synthesizeLPCOrder16Core` | arm64 | `internal/silk/lpc_synth_default.go` | scalar Go | pending | pending | scalar replacement; SIMD port and benchmark pending |
| 46 | `celtPitchXcorrFloatImplASM` | arm64 | `internal/silk/pitch_xcorr_impl_default.go`; amd64 SIMD implementation | scalar arm64 / archsimd amd64 | pending | pending | arm64 SIMD port and benchmark pending |
| 47 | `xcorrKernelAVX8` | amd64 | `internal/silk/pitch_xcorr_kernel_simd_amd64.go`; `internal/silk/pitch_xcorr_kernel_avx_amd64.go` | archsimd / scalar | L×P=120×300, Linux AMD64 production path: 1,748→9,372 ns/op (median; 1,744–1,753→9,338–9,464) | 0 | measured; Go SIMD path is 5.4× slower |
| 48 | `firInterpol21846Core` | arm64 | `internal/silk/resample_fir_default.go`; `internal/silk/resample_libopus.go` | scalar Go | pending | pending | scalar replacement; SIMD port and benchmark pending |
| 49 | `firInterpol32768Core` | arm64 | `internal/silk/resample_fir_default.go`; `internal/silk/resample_libopus.go` | scalar Go | pending | pending | scalar replacement; SIMD port and benchmark pending |
| 50 | `firInterpol43691Core` | arm64 | `internal/silk/resample_fir_default.go`; `internal/silk/resample_libopus.go` | scalar Go | pending | pending | scalar replacement; SIMD port and benchmark pending |
| 51 | `up2HQCore` | arm64 | `internal/silk/up2hq_core_default.go`; `internal/silk/resample_libopus.go` | scalar Go | pending | pending | scalar replacement; SIMD port and benchmark pending |
| 52 | `convertFloat32ToInt16UnitBlocks` | arm64 | `pcm_convert_simd_arm64.go`; `pcm_convert_default.go` | archsimd / scalar | pending | pending | SIMD port; benchmark pending |
| 53 | `convertFloat32ToInt16SaturatingBlocks` | arm64 | `pcm_convert_simd_arm64.go`; `pcm_convert_default.go` | archsimd / scalar | pending | pending | SIMD port; benchmark pending |

## Native AMD64 parity and quality comparison

The same run executes the full CBR packet matrix and the precision case on the
pre-port base and candidate. The base reports 13 failing CBR rows; the
candidate reports the same 13 and increases the difference count in 11 CELT or
Hybrid rows. The SILK NB 20 ms row remains 21/50 on both snapshots, and CELT
2.5 ms mono remains 37/400 on both. CELT 2.5 ms stereo changes from 26/400 to
28/400; the other ten CELT/Hybrid rows increase as shown here:

| CBR case | Base differing packets | Candidate differing packets |
|---|---:|---:|
| CELT-FB-5ms-mono-64k | 96/200 | 113/200 |
| CELT-FB-5ms-stereo-128k | 2/200 | 138/200 |
| CELT-FB-10ms-mono-64k | 7/100 | 95/100 |
| CELT-FB-20ms-mono-64k | 17/50 | 50/50 |
| CELT-FB-20ms-stereo-128k | 29/50 | 50/50 |
| Hybrid-SWB-10ms-mono-48k | 4/100 | 81/100 |
| Hybrid-SWB-20ms-mono-48k | 10/50 | 38/50 |
| Hybrid-FB-10ms-mono-64k | 16/100 | 93/100 |
| Hybrid-FB-20ms-mono-64k | 9/50 | 48/50 |
| Hybrid-FB-20ms-stereo-96k | 8/50 | 50/50 |

For `Hybrid-FB-20ms-stereo-96k`, the base precision guard passes at Q=32.16
against libopus Q=32.16. The candidate records Q=31.58 (gap −0.58), below the
unchanged −0.05 floor. The Linux run is diagnostic evidence for regressions;
the existing parity and quality gates remain blocking.

Direct AMD64 benchmark samples for the relevant production paths are:

| Benchmark case | Base median (range) | Candidate median (range) | Candidate/base |
|---|---:|---:|---:|
| CELT xcorr, L×P=240×360 | 3,083 (3,079–3,107) ns | 16,107 (16,075–16,159) ns | 5.2× slower |
| CELT xcorr, L×P=480×64 | 1,036 (1,033–1,036) ns | 4,350 (4,342–4,358) ns | 4.2× slower |
| CELT xcorr, L×P=5×244 | 332.2 (330.2–335.1) ns | 7,541 (7,525–7,560) ns | 22.7× slower |
| CELT xcorr, L×P=10×10 | 38.84 (38.78–39.42) ns | 1,966 (1,952–1,967) ns | 50.6× slower |
| SILK inner product, N=480 | 97.69 (97.60–100.3) ns | 195.1 (194.8–197.0) ns | 2.0× slower |
| SILK pitch xcorr, L×P=120×300 | 1,748 (1,744–1,753) ns | 9,372 (9,338–9,464) ns | 5.4× slower |

`celtInnerProd8FMA32` also improves over the AMD64 base scalar path, which has
no matching AMD64 assembly symbol: N=16 20.80→6.561 ns, N=64 70.87→13.53 ns,
and N=176 187.9→37.17 ns. These values do not replace its arm64 asm comparison.
The existing `BenchmarkXcorrKernelFloat` scalar helper is unchanged at 223.9
ns/op; it does not isolate `xcorrKernelAVX8`.

## Measurement follow-up

The pending rows need a reproducible benchmark matrix that builds the pre-port
assembly at the same Go version and host as the replacement. Each routine needs
representative sizes, a zero-allocation measurement, and generated-code
inspection where the replacement uses `archsimd`. The native AMD64 xcorr and
SILK inner-product regressions require optimization before completion. The
pre-rotate slowdown and large-size stereo-merge slowdown remain optimization
items; no overall performance gain is claimed from these kernel samples.
