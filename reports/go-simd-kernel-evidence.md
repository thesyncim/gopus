# Go kernel replacement evidence

This report tracks all 53 symbols from the 41 assembly files in the pre-port
`origin/master` tree. Every entry has a Go replacement. `archsimd` means the
Go 1.27 `simd/archsimd` implementation selected by `GOEXPERIMENT=simd`; ordinary
builds use the listed scalar Go path, and `-tags nosimd` forces the scalar
reference path.

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
The ARM64 CWRS row uses five paired 500 ms samples per mode on the same M4
with Go 1.27.0 and GOMAXPROCS=4. N=48, K=5 is table-covered by
`canUseCWRSFast` and reaches the fast decoder path; N=48, K=12 does not.
The ARM64 stereo deemphasis live fused helper uses five 500 ms samples per
version on the same M4 with Go 1.27.0, `GOEXPERIMENT=simd`, GOMAXPROCS=4,
and N=480. The two Go versions use the same preallocated stereo fixture;
the direct former-symbol comparison remains the scalar-core timing below.
The tuned MDCT post-twiddle row uses three interleaved 15-sample runs; its
reported ranges are the three within-run medians because individual samples
include M4 scheduling outliers. Assembly and both SIMD versions use the same
fixture.
Native AMD64 A/B measurements come from CI runs
[35911787670](https://github.com/thesyncim/gopus/actions/runs/35911787670),
[35925161573](https://github.com/thesyncim/gopus/actions/runs/35925161573),
[35930378365](https://github.com/thesyncim/gopus/actions/runs/35930378365),
[35932476353](https://github.com/thesyncim/gopus/actions/runs/35932476353),
[35936281423](https://github.com/thesyncim/gopus/actions/runs/35936281423), and
[35940248675](https://github.com/thesyncim/gopus/actions/runs/35940248675),
[35971995288](https://github.com/thesyncim/gopus/actions/runs/35971995288),
[35976988223](https://github.com/thesyncim/gopus/actions/runs/35976988223),
[35982232337](https://github.com/thesyncim/gopus/actions/runs/35982232337),
[35987671538](https://github.com/thesyncim/gopus/actions/runs/35987671538), and
[35994840088](https://github.com/thesyncim/gopus/actions/runs/35994840088), and
[36001803330](https://github.com/thesyncim/gopus/actions/runs/36001803330), and
[36007082978](https://github.com/thesyncim/gopus/actions/runs/36007082978): the
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
Rosetta results are not treated as native AMD64 evidence. Runner CPUs vary
between runs, so ratios only compare modes within one run. Run 359769 uses
AMD EPYC 9V74; runs 359822, 359876, 359948, and 360070 use AMD EPYC 7763;
run 360018 uses Intel Xeon Platinum 8573C.

## Per-symbol inventory

`old path` names the assembly file in `origin/master`. `0` in the allocation
column is limited to directly measured kernels.

Direct timing covers all 51 comparable routines. The two startup
feature-discovery helpers have no
comparable per-call Go operation and are marked n/a with the reason.

| # | Former assembly symbol | Old arch | Go replacement source | Replacement path | Measured timing (ns/op) | Allocs/op | Status |
|---:|---|---|---|---|---|---|---|
| 1 | `combFilterConstNeon` | arm64 | `internal/celt/comb_const_simd_arm64.go`; `internal/celt/comb_const_default.go` | archsimd / scalar | N=480: old asm 115.4 (114.7–116.9) → Go SIMD 109.0 (107.7–113.0); scalar Go 617.7 (592.7–623.2; earlier Go 1.27.1 run) | 0 | measured on M4 with Go 1.27.0; Go SIMD is 5.5% faster than asm; exact and zero-alloc checks pass |
| 2 | `cwrsiFastCore` | arm64 | `internal/celt/cwrs_fast_default.go` | scalar Go | live table-covered N=48, K=5: old asm 28.94 (28.85–29.34) → scalar Go 44.02 (43.81–44.12) ns/op | 0 | paired M4 Go 1.27.0; scalar Go is 52% slower than asm; exact output and zero-allocation checks pass |
| 3 | `deemphasisStereoPlanarF32Core` | arm64 | `internal/celt/deemphasis_f32_default.go`; `internal/celt/output_helpers.go` | scalar core / fused SIMD decode | direct N=480 stereo old asm → scalar Go → SIMD build scalar core: 1,065 (1,065–1,068) → 1,167 (1,166–1,169) → 1,168 (1,166–1,177); live fused helper before → after bounds proof: 349.0 (347.7–352.1) → 338.0 (334.8–340.7) | 0 | scalar core trails asm 10% in ordinary/nosimd builds; ARM64 SIMD decode selects the fused helper, which improves 3.2% with the bounds proof and preserves exact bits |
| 4 | `expRotation1PassNeon` | arm64 | `internal/celt/exp_rotation_simd_arm64.go`; `internal/celt/exp_rotation_default.go` | archsimd / scalar | old asm → Go → SIMD: len32/stride1 284.3 (283.6–289.3) → 286.9 (285.8–288.9) → 282.6 (280.6–284.3); len64/stride1 583.2 (581.0–584.1) → 579.7 (574.3–584.2) → 578.2 (574.5–592.8); len32/stride2 138.1 (136.6–145.2) → 148.8 (142.6–152.1) → 159.5 (155.8–167.2); len32/stride4 85.03 (84.45–85.53) → 80.54 (80.13–83.10) → 84.20 (83.98–84.38) | 0 | measured; SIMD near assembly except stride2 slower |
| 5 | `haar1Stride1NEON` | arm64 | `internal/celt/haar1_simd_arm64.go`; `internal/celt/haar1_neon_default.go` | archsimd / scalar | N=32, old asm → Go → SIMD: 9.865 (9.791–14.28) → 14.37 (14.34–14.47) → 8.095 (8.057–8.133) | 0 | measured; SIMD 18% faster than asm median; asm range is noisy |
| 6 | `haar1Stride2NEON` | arm64 | `internal/celt/haar1_simd_arm64.go`; `internal/celt/haar1_neon_default.go` | archsimd / scalar | N=32, old asm → Go → SIMD: 18.73 (14.25–20.18) → 25.88 (25.56–26.26) → 10.59 (10.55–10.80) | 0 | measured; SIMD 43% faster than asm median; asm range is noisy |
| 7 | `haar1Stride4NEON` | arm64 | `internal/celt/haar1_simd_arm64.go`; `internal/celt/haar1_neon_default.go` | archsimd / scalar | N=32, old asm → Go → SIMD: 14.47 (14.40–14.63) → 41.43 (41.35–41.53) → 17.70 (17.64–17.72) | 0 | measured; SIMD 22% slower than asm |
| 8 | `imdctPostRotateF32FromKiss` | arm64 | `internal/celt/imdct_post_kiss_simd_arm64.go`; `internal/celt/imdct_post_kiss_default.go` | archsimd / scalar | N=120: old asm → Go → SIMD: 57.00 (56.43–60.80) → 56.72 (56.41–57.17) → 57.09 (56.93–57.23) | 0 | measured; SIMD within 0.2% of asm |
| 9 | `imdctPreRotateFMA32Kiss` | arm64 | `internal/celt/imdct_pre_kiss_simd_arm64.go`; `internal/celt/imdct_pre_kiss_default.go` | archsimd / scalar | N=120: old asm 19.28 (19.19–19.31) → scalar Go 74.08 (73.89–74.25; earlier Go 1.27.1 run) → Go SIMD 20.41 (20.40–20.49) | 0 | measured on M4 with Go 1.27.0; Go SIMD is 5.9% slower than asm and 23% faster than the first SIMD port |
| 10 | `imdctTDACWindowFMA32` | arm64 | `internal/celt/imdct_tdac_simd_arm64.go`; `internal/celt/imdct_tdac_default.go` | archsimd / scalar | overlap=120/count=60: old asm → Go → SIMD: 24.96 (24.95–25.44) → 95.45 (95.02–96.67) → 25.22 (25.00–25.43) | 0 | measured; SIMD within 1.0% of asm |
| 11 | `celtInnerProd8FMA32` | arm64 | `internal/celt/inner_prod_fma_simd_arm64.go`; `internal/celt/inner_prod_fma_simd_amd64.go`; `internal/celt/inner_prod_fma_default.go` | archsimd / scalar | N=16: 5.94–6.00 → 3.49; N=64: 20.94–21.00 → 6.13–6.43; N=176: 56.24–56.30 → 19.96–20.04 | 0 | measured; faster on M4 |
| 12 | `celtInnerProdSSEStyleAsm` | amd64 | `internal/celt/innerprod_sse_simd_amd64.go`; `internal/celt/innerprod_sse_default.go` | archsimd / scalar | N=480, old asm → Go → SIMD: 89.02 (88.97–89.30) → 395.9 (395.0–445.0) → 81.61 (81.38–82.68) | 0 | measured in run 359362; SIMD 8.3% faster than asm on this runner |
| 13 | `kfBfly4M1Core` | arm64 | `internal/celt/kf_bfly_simd_arm64.go`; `internal/celt/kf_bfly4m1_default.go` | archsimd / scalar | N=128 paired M4: old asm 103–105 → scalar Go 161–166 → Go SIMD 157–162 ns/op | 0 | SIMD is about 3% faster than scalar Go but 1.5× slower than asm; exact old-asm/FMA parity and zero-alloc checks pass |
| 14 | `kfBfly5Inner` | amd64 | `internal/celt/kf_bfly_default.go` | scalar Go | m=8, N=4, old asm → Go → SIMD build: 376.0 (375.3–376.9) → 537.3 (536.8–540.1) → 539.5 (537.2–547.1) | 0 | measured in run 359362; Go replacement 43% slower than asm |
| 15 | `kfBfly3Inner` | amd64 | `internal/celt/kf_bfly_default.go` | scalar Go | m=8, N=4, old asm → Go → SIMD build: 222.2 (222.1–222.3) → 208.5 (207.5–212.0) → 207.4 (207.1–208.0) | 0 | measured in run 359362; Go replacement 6.7% faster than asm |
| 16 | `kfBfly4Inner` | amd64 | `internal/celt/kf_bfly_default.go` | scalar Go | m=8, N=4, old asm → Go → SIMD build: 217.0 (216.5–218.1) → 269.9 (269.4–271.9) → 270.9 (269.4–273.8) | 0 | measured in run 359362; Go replacement 25% slower than asm |
| 17 | `kfBfly5Inner` | arm64 | `internal/celt/kf_bfly_simd_arm64.go`; `internal/celt/kf_bfly_default.go` | archsimd / scalar | m=8, N=4 paired M4: old asm 103.9–105.9 → scalar Go 183.0–188.2 → Go SIMD 71.3–73.4 ns/op | 0 | SIMD is about 31% faster than asm and 2.6× faster than scalar Go; exact old-asm/FMA parity and zero-alloc checks pass |
| 18 | `kfBfly3Inner` | arm64 | `internal/celt/kf_bfly_simd_arm64.go`; `internal/celt/kf_bfly_default.go` | archsimd / scalar | m=8, N=4 paired M4: old asm 70.4–72.1 → scalar Go 75.3–75.6 → Go SIMD 34.1–36.0 ns/op | 0 | SIMD is about 50% faster than asm and 2.1× faster than scalar Go; exact old-asm/FMA parity and zero-alloc checks pass |
| 19 | `kfBfly4Inner` | arm64 | `internal/celt/kf_bfly_simd_arm64.go`; `internal/celt/kf_bfly_default.go` | archsimd / scalar | m=8, N=4 paired M4: old asm 70.5–71.1 → scalar Go 89.2–90.7 → Go SIMD 43.9–45.0 ns/op | 0 | SIMD is about 37% faster than asm and 2.0× faster than scalar Go; exact old-asm/FMA parity and zero-alloc checks pass |
| 20 | `l1AbsSumNeon` | arm64 | `internal/celt/l1_abs_sum_simd_arm64.go`; `internal/celt/l1_abs_sum_default.go` | archsimd / scalar | N=480: old asm → Go → SIMD: 123.2 (122.4–123.3) → 493.0 (475.0–529.3) → 50.59 (50.48–50.78) | 0 | measured; SIMD 59% faster than asm, scalar Go 4.0× slower |
| 21 | `mdctFold1StoreNeon` | arm64 | `internal/celt/mdct_fold_simd_arm64.go`; `internal/celt/mdct_fold_default.go` | archsimd / scalar | n4=64, blocks=8: old asm 36.69 (36.57–36.81) → scalar Go 153.6 (153.4–153.7; earlier Go 1.27.1 run) → Go SIMD 37.18 (37.02–37.28) | 0 | measured on M4 with Go 1.27.0; SIMD is 1.3% slower than asm and about 30% faster than the first SIMD port |
| 22 | `mdctFold3StoreNeon` | arm64 | `internal/celt/mdct_fold_simd_arm64.go`; `internal/celt/mdct_fold_default.go` | archsimd / scalar | n4=64, blocks=8: old asm 36.67 (36.65–38.30) → scalar Go 154.9 (154.8–155.5; earlier Go 1.27.1 run) → Go SIMD 37.35 (37.15–38.31) | 0 | measured on M4 with Go 1.27.0; SIMD is 1.9% slower than asm and about 30% faster than the first SIMD port; samples include one outlier per mode |
| 23 | `mdctMidFoldStoreNeon` | arm64 | `internal/celt/mdct_mid_fold_simd_arm64.go`; `internal/celt/mdct_mid_fold_default.go` | archsimd / scalar | n4=64, blocks=8 paired M4 Go 1.27.0: old asm 14.67 (14.58–15.38) → prior SIMD 15.56 (15.54–15.75) → packed SIMD 14.57 (14.50–14.69); scalar Go 109.4 (109.3–109.6) in an earlier fixture | 0 | packed SIMD is 6.4% faster than prior SIMD and at assembly speed; exact old-asm comparison, zero-alloc, and checkptr level 2 pass |
| 24 | `mdctPostTwiddleNeon` | arm64 | `internal/celt/mdct_post_twiddle_simd_arm64.go`; `internal/celt/mdct_post_twiddle_default.go` | archsimd / scalar | n4=64, pairBlocks=8: old asm median 12.71 (run medians 12.69–12.84) → Go SIMD 14.65 (14.64–14.72); prior Go SIMD 15.48 (15.39–15.79); scalar Go 103.4 (103.3–103.9; earlier Go 1.27.1 run) | 0 | measured on M4 with Go 1.27.0; Go SIMD is 15% slower than asm and about 5% faster than the prior SIMD loop; exact and zero-alloc checks pass |
| 25 | `xcorrKernelAVX8` | amd64 | `internal/celt/pitch_xcorr_kernel_simd_amd64.go`; `internal/silk/pitch_xcorr_kernel_simd_amd64.go`; `internal/celt/pitch_xcorr_tiny_simd_amd64.go` | archsimd / scalar | CELT coarse L×P=240×360, old asm→Go SIMD: Xeon 8573C 2,454→9,875; EPYC 7763 3,156→12,077. Half 480×64: Xeon 834.3→3,264; EPYC 1,044→3,243. Tiny 5×244: Xeon 254.3→230.7; EPYC 334.8→239.2. Tiny 10×10: Xeon 26.03→165.9; EPYC 35.01→161.2. SILK 120×300: Xeon 1,198→6,895; EPYC 1,747→7,665. Same-run ranges appear below. | 0 | native one-pass production path is exact and allocation-free; long and fine searches trail assembly on both runners; strict fixture gate awaits both live-opusdec hash sets |
| 26 | `prefilterDualInnerProdAsm` | arm64 | `internal/celt/prefilter_dual_inner_prod_simd_arm64.go`; default and nosimd variants | archsimd / scalar | N=240, old asm → Go → SIMD: 85.35 (85.08–86.27) → 237.0 (236.7–238.7) → 41.24 (41.10–41.56) | 0 | measured; SIMD 52% faster than asm; scalar Go 2.8× slower |
| 27 | `pvqSearchPulseLoopAVX` | amd64 | `internal/celt/pvq_search.go`; `internal/celt/pvq_search_default.go` | scalar Go | Direct pulse-loop helper, run 359362: old asm → Go → SIMD build 536.5 → 954.9 → 2,580. Production full-search, old asm → Go SIMD: EPYC 9V74 945.7→794.7; EPYC 7763 625.3→606.1 | 0 | production path is 16% faster on 9V74 and 3.1% faster on 7763; direct scalar helper is not selected by AMD64 SIMD dispatch |
| 28 | `pvqSearchPulseLoop` | arm64 | `internal/celt/pvq_search.go`; `internal/celt/pvq_search_default.go` | scalar Go | N=48, pulses=16: old asm → Go → SIMD build: 552.6 (516.9–567.2) → 997.9 (964.7–1,006) → 1,013 (994.8–1,024) | 0 | measured; scalar replacement 81% slower than asm |
| 29 | `x86RcpApprox4` | amd64 | `internal/celt/pvq_search_x86_sse2.go` | archsimd | four varying lanes, old asm → Go SIMD: 1.933 (1.914–1.960) → 1.094 (1.092–1.109) | 0 | measured in run 359362; Go SIMD 43% faster than asm; no ordinary-Go direct equivalent |
| 30 | `x86PVQSearchBestIDSSE2` | amd64 | `internal/celt/pvq_search_x86_sse2.go` | archsimd | N=48, varying data, old asm → Go SIMD: 22.81 (22.79–23.93) → 22.39 (22.35–22.80) | 0 | measured in run 359362; Go SIMD 1.8% faster than asm; no ordinary-Go direct equivalent |
| 31 | `scaleFloat32IntoNEON` | arm64 | `internal/celt/scale_into_simd_arm64.go`; `internal/celt/scale_into_default.go` | archsimd / scalar | old asm → Go → SIMD: N=16 3.286 (3.274–3.301) → 7.656 (7.618–7.791) → 2.716 (2.703–2.719); N=64 7.715 (7.689–7.741) → 27.34 (26.86–29.65) → 5.107 (5.038–5.152); N=176 16.31 (16.18–16.41) → 80.68 (80.36–81.15) → 11.52 (11.40–11.53); N=480 33.87 (33.69–34.02) → 200.7 (200.1–201.0) → 26.73 (26.27–26.77) | 0 | measured; SIMD 17–34% faster than asm, scalar Go 2.3–5.0× slower |
| 32 | `stereoMergeRescaleNEON` | arm64 | `internal/celt/stereo_merge_simd_arm64.go`; `internal/celt/stereo_merge_default.go` | archsimd / scalar | old asm → Go → SIMD: N=16 8.392 (8.366–8.427) → 12.79 (12.66–12.92) → 6.203 (6.169–6.215); N=64 17.61 (17.52–17.70) → 45.57 (45.49–46.16) → 12.05 (11.99–12.07); N=176 31.89 (31.78–31.91) → 121.7 (121.6–122.0) → 27.20 (27.08–27.30); N=480 71.40 (71.05–71.49) → 329.1 (328.5–329.5) → 70.09 (69.60–73.70) | 0 | measured; SIMD 2–32% faster than asm; scalar Go 1.5–4.6× slower |
| 33 | `toneLPCCorrAVXFMA` | amd64 | `internal/celt/tone_lpc_corr_default.go`; `internal/celt/amd64_dispatch_helpers.go` | Go lane helper / scalar | N=480, delays=1/2, old asm → Go → SIMD: 508.6 (508.1–510.8) → 389.6 (389.4–390.8) → 929.2 (920.8–940.0) | 0 | measured in run 359362; candidate machine code is identical in default/SIMD modes despite timing spread, so the relative result needs interleaved remeasurement |
| 34 | `toneLPCCorr` | arm64 | `internal/celt/tone_lpc_corr_default.go`; `internal/celt/tone_lpc_corr_simd_arm64.go` | archsimd / scalar | cnt=480, delays=1/2: old asm 163.4 (163.0–163.5) → scalar Go 559.0 (558.3–559.5) → Go SIMD 117.7 (117.5–118.0) | 0 | measured on M4; Go SIMD is 28% faster than asm and 79% faster than scalar Go |
| 35 | `xcorrKernel4Float32Neon4Acc` | arm64 | `internal/celt/xcorr_kernel_f32_4acc_simd_arm64.go`; `internal/celt/xcorr_kernel_f32_default.go` | archsimd / scalar | N=480: old asm → Go → SIMD: 189.2 (186.9–190.2) → 699.3 (697.0–699.9) → 167.8 (167.5–167.9) | 0 | measured; SIMD 11% faster than asm; scalar Go 3.7× slower |
| 36 | `cpuid` | amd64 | Go runtime CPU feature flags used by `simd/archsimd` | startup feature discovery | n/a | n/a | not comparable; the old helper returns raw CPUID registers, while Go dispatch consumes cached feature flags and has no per-call replacement |
| 37 | `xgetbv` | amd64 | Go runtime CPU feature flags used by `simd/archsimd` | startup feature discovery | n/a | n/a | not comparable; the old helper reads OS vector state during initialization, while Go dispatch consumes cached feature flags and has no per-call replacement |
| 38 | `reciprocalEstimate32` | arm64 | `internal/dnnmath/reciprocal_estimate_default.go` | scalar Go | input set of 64 normal float32 values, old asm → Go → SIMD build: 1.405 (1.398–1.428) → 2.021 (1.918–2.285) → 1.932 (1.915–2.308) | 0 | measured; Go emulation is 44% slower than FRECPE asm; SIMD build uses the same scalar routine |
| 39 | `fma32` | arm64 | `internal/lpcnetplc/fma32_arm64.go`; `internal/lpcnetplc/fma32_default.go` | Go float32 expression / scalar | 64 varying input triples, old asm → Go → SIMD build: 1.915 (1.913–1.916) → 0.5506 (0.5505–0.5517) → 0.5491 (0.5489–0.5509) | 0 | measured; Go FMADD expression is 3.5× faster than the out-of-line asm call |
| 40 | `gruFMA32` | arm64 | `internal/osce/lace/gru_fma_arm64.go`; `internal/osce/lace/gru_fma_default.go` | Go float32 expression / scalar | 64 varying input triples, old asm → Go → SIMD build: 1.914 (1.912–1.916) → 0.5497 (0.5494–0.5504) → 0.5502 (0.5488–0.5515) | 0 | measured; Go FMADD expression is 3.5× faster than the out-of-line asm call |
| 41 | `floatToInt16ScaledCore` | arm64 | `internal/silk/convert_simd_arm64.go`; `internal/silk/float_to_int16_default.go` | archsimd / scalar | N=480, scale 1: old asm 23.33 (22.97–23.59) → prior Go SIMD 34.65 (34.32–35.32) → tuned SIMD 27.16 (26.55–27.47). Scale 32768: old asm 23.36 (22.89–23.72) → prior Go SIMD 34.66 (34.24–35.26) → tuned SIMD 34.92 (34.26–35.65). Scalar Go 489.8 (484.9–513.3; earlier Go 1.27.1 fixture). | 0 | paired M4 Go 1.27.0; live pitch path at scale 1 is 22% faster than prior Go and 16% slower than asm; scale 32768 has no measured gain; exact and zero-alloc checks pass |
| 42 | `innerProductFLPAVX2` | amd64 | `internal/silk/inner_product_flp_simd_amd64.go`; `internal/silk/inner_product_flp_amd64.go` | archsimd / scalar | N=480, old asm → scalar Go → Go SIMD: 70.17 (68.83–71.95) → 204.2 (204.0–204.8) → 69.37 (69.34–70.31) | 0 | measured in run 359362; SIMD and asm are within 1.1% on this runner |
| 43 | `innerProductFLPArm64` | arm64 | `internal/silk/inner_product_flp_arm64.go` | scalar Go | N=480: old asm → Go → SIMD build: 126.6 (126.5–126.7) → 205.5 (205.3–212.8) → 201.6 (200.8–211.3) | 0 | measured; scalar replacement 62% slower than asm |
| 44 | `writeInt16AsFloat32Core` | arm64 | `internal/silk/convert_simd_arm64.go`; `internal/silk/int16_float32_default.go` | archsimd / scalar | N=480: old asm 28.81 (28.55–29.86) → prior Go SIMD 33.68 (33.63–34.56) → tuned Go SIMD 24.00 (23.76–24.10); scalar Go 216.4 (216.1–217.3; earlier Go 1.27.1 fixture) | 0 | paired M4 Go 1.27.0; tuned SIMD is 17% faster than asm and 29% faster than prior SIMD; exact float bits and zero allocations |
| 45 | `synthesizeLPCOrder16Core` | arm64 | `internal/silk/lpc_synth_simd_arm64.go`; `internal/silk/lpc_synth_default.go` | archsimd / scalar | subframe=80: paired M4 Go 1.27.0 old asm 250.0 (228.9–250.5) → Go SIMD 341.9 (340.8–343.5); prior scalar core 391.9 (387.7–394.4) in a separate paired run | 0 | SIMD is 13% faster than scalar Go and 37% slower than asm; exact old-asm parity, default/SIMD/nosimd, and checkptr level 2 pass |
| 46 | `celtPitchXcorrFloatImplASM` | arm64 | `internal/silk/pitch_xcorr_impl_simd_arm64.go`; `internal/silk/pitch_xcorr_impl_default.go` | archsimd / scalar | length=240, maxPitch=120: old asm 3,690 (3,665–3,733) → tuned Go SIMD production 2,483 (2,461–2,496); direct SIMD 2,487 (2,473–2,492); prior SIMD 4,892 (4,868–4,910); scalar Go 13,188 (13,043–13,237; earlier fixture) | 0 | paired M4 Go 1.27.0; tuned SIMD is 33% faster than asm and 49% faster than prior SIMD; exact per-lag bits and zero allocations |
| 47 | `xcorrKernelAVX8` | amd64 | `internal/silk/pitch_xcorr_kernel_simd_amd64.go`; `internal/silk/pitch_xcorr_kernel_avx_amd64.go` | archsimd / scalar | SILK pitch search L×P=120×300, old asm→Go SIMD: Xeon 8573C 1,198→6,895; EPYC 7763 1,747→7,665; earlier EPYC split SIMD 11,206 | 0 | native one-pass production path is exact and allocation-free; pitch search trails assembly on both runners; strict fixture gate awaits both live-opusdec hash sets |
| 48 | `firInterpol21846Core` | arm64 | `internal/silk/resample_fir_simd_arm64.go`; `internal/silk/resample_fir_default.go`; `internal/silk/resample_libopus.go` | archsimd / scalar | nOut=240: old asm 107.56 (105.68–148.66) → scalar Go 280.39 (275.17–287.17) → Go SIMD 115.73 (111.11–119.72) | 0 | paired M4 Go 1.27.0; SIMD is 2.4× faster than scalar Go and 7.6% slower than asm; exact and zero-alloc checks pass |
| 49 | `firInterpol32768Core` | arm64 | `internal/silk/resample_fir_simd_arm64.go`; `internal/silk/resample_fir_default.go`; `internal/silk/resample_libopus.go` | archsimd / scalar | nOut=240: old asm 122.9 (122.5–124.7) → scalar Go 293.8 (293.2–296.0) → Go SIMD production 114.2 (113.2–115.6); direct SIMD core 113.5 (111.5–114.2) | 0 | paired M4 Go 1.27.0; production Go SIMD is 7% faster than asm and 2.6× faster than scalar Go; exact and zero-alloc checks pass |
| 50 | `firInterpol43691Core` | arm64 | `internal/silk/resample_fir_simd_arm64.go`; `internal/silk/resample_fir_default.go`; `internal/silk/resample_libopus.go` | archsimd / scalar | nOut=240: old asm 113.5 (113.2–114.5) → scalar Go 307.0 (304.7–308.7) → Go SIMD production 105.9 (105.7–106.5); direct SIMD core 106.2 (105.1–106.2) | 0 | paired M4 Go 1.27.0; production Go SIMD is 6.7% faster than asm and 2.9× faster than scalar Go; exact and zero-alloc checks pass |
| 51 | `up2HQCore` | arm64 | `internal/silk/up2hq_core_default.go`; `internal/silk/resample_libopus.go` | scalar Go | N=240: old asm → Go → SIMD build: 1,120 (1,096–1,176) → 1,133 (1,126–1,137) → 1,166 (1,154–1,171) | 0 | measured; scalar and SIMD Go are within 4% of asm |
| 52 | `convertFloat32ToInt16UnitBlocks` | arm64 | `pcm_convert_simd_arm64.go`; `pcm_convert_arm64_nosimd.go` | archsimd / scalar | n=480, paired M4 Go 1.27.0: old asm 63.13 (62.17–63.76) → prior SIMD 74.63 (73.88–75.71) → tuned SIMD 60.66 (59.99–61.23); scalar Go 630.1 (618.2–700.6) in an earlier fixture | 0 | tuned SIMD is 3.9% faster than asm and 18.7% faster than prior SIMD; libopus, invalid-lane, unaligned-slice, and zero-alloc checks pass |
| 53 | `convertFloat32ToInt16SaturatingBlocks` | arm64 | `pcm_convert_simd_arm64.go`; `pcm_convert_arm64_nosimd.go` | archsimd / scalar | n=480: old asm 52.31 (52.15–52.60); tuned Go SIMD 53.04 (52.23–53.90); original Go SIMD 102.7; scalar Go 646.5 (639.8–658.0) | 0 | measured; tuned SIMD is within 1.4% of asm and 48% faster than the first Go SIMD port |

## Native AMD64 parity and quality comparison

The same run executes the full CBR packet matrix and precision case for each
mode. Mode-matched old assembly and Go SIMD have the same per-row CBR mismatch
counts: 13 rows fail and six pass. Ordinary Go and `nosimd` are compared
with scalar libopus and report no failing rows (six and four residual rows,
respectively). The precision guard is also mode-matched: old assembly and Go
SIMD both score Q=32.16 against SIMD libopus Q=32.16. The scalar Go modes
score Q=31.56–31.58 in the native A/B run; the focused M4 scalar check gives
Q=31.56 for both `nosimd` Go and scalar libopus. Absolute
byte parity remains unresolved in both the assembly baseline and Go SIMD.
Pull requests use the native same-ISA A/B gate; the absolute SIMD parity gate
still runs on pushes to `master`.
The focused hybrid decode differential reports the same two failing frames,
sample index, and 2.0213886e-34 worst difference for old assembly and Go SIMD
on the same native AMD64 runner in run 359303.
The native A/B comparator passes on run 359324: all 19 CBR cases have no
higher mismatch count or worse status than old assembly, the focused decode
diagnostics match, and the SIMD precision fixture passes against SIMD libopus.
Run 359402 captures the full parity suite in both builds with live `opusdec`
installed. Go SIMD resolves 985 old failing leaf cases; the reported differing
decode samples fall from 733,453 to 177,753. Seven shared cross-validation
cases skip and eight fixture-honesty cases fail because the candidate's Ogg
packet hashes are absent from the committed Linux AMD64 `opusdec` fixture.
The A/B gate rejects these results until a fixture decoded by live `opusdec`
is reviewed and committed.
Run 359719 adds the old `purego` scalar comparison. On Linux AMD64, old
`purego` and candidate `nosimd` each report four residual CBR cases with the
same per-case packet counts. Ordinary Go reports six residual cases in this
run, exposing the AMD64 SSE-order float flag outside `GOEXPERIMENT=simd`.
The current default build selects scalar float order. The standalone native
fixture capture records live `opusdec` output before the full A/B gate. Run
359769 regenerates the same 11-entry SIMD fixture as the committed file, but
platform fixture generation writes the ordinary-Go fixture over that file
before the SIMD full-parity sweep. The A/B script preserves and restores the
committed SIMD fixture after platform generation. Runs 359822, 359876, and
359948 pass the full same-ISA A/B gate: all 19 CBR cases, focused decode,
precision, dispatch, and zero-allocation kernel checks pass. The latest
full-parity sweep
executes 25,474 old-assembly and 25,507 Go SIMD leaf tests. Failing leaf tests
fall from 6,830 for old assembly to 5,845 for Go SIMD; differing decode
samples fall from 733,453 to 177,753.

Runs 360018 (Intel Xeon) and 360070 (AMD EPYC) select the one-pass AMD64 xcorr
loop for lengths 120–240. Same-run CBR, focused decode, precision, dispatch,
exact xcorr oracle, and zero-allocation checks pass on both hosts. The full
A/B gate rejects seven skipped fixture fallback cases and eight
fixture-honesty failures on each run because their seven CPU-specific packet
hashes are absent from the single-host 11-entry fixture. The reviewed fixture
contains all 18 exact hashes captured with live `opusdec` on the two hosts;
the full A/B gate must pass again with both sets committed.

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
at Q=32.16. Scalar Go and scalar libopus each score Q=31.56 in the focused M4
check. The reference build must match the instruction set of the Go mode.

Direct AMD64 benchmark samples on EPYC 9V74 in run 359769 are:

| Benchmark case | Old assembly | Ordinary Go | Go SIMD |
|---|---:|---:|---:|
| CELT xcorr, L×P=240×360 | 3,447 (3,444–3,464) ns | 37,474 (37,425–37,929) ns | 4,316 (4,309–4,351) ns |
| CELT xcorr, L×P=480×64 | 1,176 (1,173–1,179) ns | 13,240 (13,232–13,272) ns | 1,436 (1,435–1,438) ns |
| CELT xcorr, L×P=5×244 | 315.4 (315.3–315.6) ns | 818.4 (817.9–818.5) ns | 299.1 (297.8–308.5) ns |
| CELT xcorr, L×P=10×10 | 33.13 (33.09–33.62) ns | 61.12 (61.07–61.22) ns | 48.97 (48.93–49.00) ns |
| SILK pitch xcorr, L×P=120×300 | 1,822 (1,820–1,845) ns | 18,735 (18,731–18,775) ns | 2,844 (2,841–2,882) ns |
| PVQ full search, N=48 | 945.7 (943.3–947.4) ns | 1,321 (1,320–1,322) ns | 794.7 (793.2–800.3) ns |

Go 1.27.1 on EPYC 7763 in passing run 359876 measures:

| Benchmark case | Old assembly | Ordinary Go | Go SIMD |
|---|---:|---:|---:|
| CELT xcorr, L×P=240×360 | 3,104 (3,104–3,106) ns | 33,533 (33,458–33,545) ns | 14,795 (14,782–14,811) ns |
| CELT xcorr, L×P=480×64 | 1,044 (1,044–1,045) ns | 11,884 (11,875–11,893) ns | 3,242 (3,240–3,244) ns |
| CELT xcorr, L×P=5×244 | 334.5 (334.1–338.5) ns | 754.8 (753.4–755.1) ns | 239.0 (238.8–239.2) ns |
| CELT xcorr, L×P=10×10 | 34.93 (34.89–35.12) ns | 60.49 (60.42–60.52) ns | 161.3 (161.1–161.4) ns |
| SILK pitch xcorr, L×P=120×300 | 1,750 (1,748–1,757) ns | 16,708 (16,692–16,718) ns | 11,206 (11,172–11,212) ns |
| PVQ full search, N=48 | 625.3 (624.3–625.7) ns | 1,221 (1,220–1,223) ns | 606.1 (605.5–606.8) ns |

Run 359948 passed the same-ISA A/B gate on EPYC 7763. Its five-sample direct
Go SIMD kernel timings compare three zero-allocation loop layouts on
the same runner. Each number is the median in ns/op; the table is direct
kernel timing, before production dispatch selects the one-pass loop for
N=120–240. Native bit-exact tests and the full pitch-search benchmark run
after that selection.

| Package | Length | Split 4+4 | One pass 8 | Split 6+2 |
|---|---:|---:|---:|---:|
| CELT | 120 | 290.5 | 195.5 | 292.3 |
| CELT | 240 | 328.3 | 265.8 | 334.2 |
| CELT | 480 | 403.4 | 414.4 | 417.2 |
| SILK | 120 | 289.2 | 195.9 | 291.8 |
| SILK | 240 | 327.9 | 267.1 | 334.0 |
| SILK | 480 | 403.2 | 413.7 | 416.8 |

Run 360018 measures the selected one-pass production path on Intel Xeon Platinum 8573C with
Go 1.27.1. Each value is the median and five-sample range in ns/op; all cases
report 0 B/op and 0 allocs/op. The old assembly and Go SIMD modes use the same
runner and native SIMD libopus reference.

| Production case | Old assembly | Go SIMD |
|---|---:|---:|
| CELT coarse, L×P=240×360 | 2,454 (2,452–2,456) | 9,875 (9,872–9,883) |
| CELT half, L×P=480×64 | 834.3 (833.8–835.4) | 3,264 (3,263–3,268) |
| CELT tiny coarse, L×P=5×244 | 254.3 (254.1–255.8) | 230.7 (230.3–231.0) |
| CELT tiny fine, L×P=10×10 | 26.03 (25.87–26.10) | 165.9 (165.9–166.8) |
| SILK pitch, L×P=120×300 | 1,198 (1,196–1,199) | 6,895 (6,894–6,933) |

Run 360070 measures the same selected path on AMD EPYC 7763 with Go 1.27.1.
Values are five-sample medians and ranges in ns/op; all report zero allocations.

| Production case | Old assembly | Go SIMD |
|---|---:|---:|
| CELT coarse, L×P=240×360 | 3,156 (3,107–3,175) | 12,077 (12,066–12,082) |
| CELT half, L×P=480×64 | 1,044 (1,044–1,045) | 3,243 (3,243–3,246) |
| CELT tiny coarse, L×P=5×244 | 334.8 (333.8–337.6) | 239.2 (238.9–246.6) |
| CELT tiny fine, L×P=10×10 | 35.01 (34.91–35.08) | 161.2 (161.1–161.3) |
| SILK pitch, L×P=120×300 | 1,747 (1,745–1,751) | 7,665 (7,659–7,748) |

The native SIMD run includes the five-sample short CELT xcorr specialization
and the unrolled four-correlation long kernel. Relative to same-run assembly,
long CELT xcorr is 22–25% slower on EPYC 9V74, 3.8× slower on EPYC 7763,
and 4.0× slower on Xeon 8573C. SILK xcorr is 56% slower on 9V74, 4.4× slower
on 7763, and 5.8× slower on Xeon. The vector
NaN check cuts the 7763 five-sample coarse search from 3,886 ns in run 359822
to 239 ns in run 359876; the matched old-assembly case is 334.5 ns. The direct
runtime identity test confirms AVX2/FMA dispatch on both runners; the long
long-search regression remains an optimization target.
Rows 14–16 include the radix 3/4/5 inlining change: radix 3 is 6.7% faster
than assembly, while radix 4 and 5 are 25% and 43% slower.

`celtInnerProd8FMA32` also improves over the AMD64 base scalar path, which has
no matching AMD64 assembly symbol: N=16 18.13→5.473 ns, N=64 62.28→10.16 ns,
and N=176 164.3→29.79 ns. These values do not replace its arm64 asm comparison.
All measurements in the production-path table report 0 allocs/op.

## Measurement follow-up

The corrected native `b.Loop` A/B measures reciprocal and best-ID directly.
The AMD64 harness uses varied input fixtures and observed outputs; the PVQ
pulse-loop fixture ring resets every 64 calls inside the timed loop, equally on
baseline and candidate. Butterfly kernels use stable zero-valued runtime input
buffers so repeated calls stay finite and execute the same arithmetic.

Native A/B shows xcorr SIMD improves over scalar Go at large sizes but stays
slower than old assembly on the long searches, especially on EPYC 7763. The
remaining IMDCT pre-rotate, post-twiddle, and PCM conversion gaps remain
optimization items. ARM64 tone LPC correlation and the constant comb filter
beat old assembly on paired M4 benchmarks. The AMD64 production PVQ full-search
benchmark is 16% faster than old assembly on 9V74 and 3.1% faster on 7763, while
the direct pulse-loop helper is not selected by SIMD dispatch. No overall
performance gain is claimed from these kernel samples.
