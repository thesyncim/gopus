# Assembly Coverage

Last updated: 2026-02-12

`gopus` includes architecture-specific assembly in select CELT and SILK hot paths.
This file tracks what is implemented and how fallback behavior works.

## Build-Tag Model

- Assembly wrappers are enabled with: `//go:build arm64 || amd64`
- Pure Go fallbacks are enabled with: `//go:build !arm64 && !amd64`
- Selection is compile-time by `GOARCH` (no runtime CPU dispatch)

## CELT Assembly

1. Haar butterfly
- Wrapper: `celt/haar1_asm.go`
- Native sources:
  - `celt/haar1_amd64.s`
  - `celt/haar1_arm64.s`
- Fallback: `celt/haar1_default.go`
- Entry point: `haar1Stride1Asm`

2. Correlation/inner-product kernels
- Wrapper: `celt/xcorr_asm.go`
- Native sources:
  - `celt/xcorr_amd64.s`
  - `celt/xcorr_arm64.s`
- Fallback: `celt/xcorr_default.go`
- Entry points:
  - `celtInnerProd`
  - `dualInnerProd`
  - `celtPitchXcorr`

## SILK Assembly

1. Float32 inner product / energy
- Wrapper: `silk/inner_prod_asm.go`
- Native sources:
  - `silk/inner_prod_arm64.s`
- Fallback: `silk/inner_prod_default.go`
- Entry points:
  - `innerProductF32`
  - `innerProductFLP`
  - `energyF32`

2. Pitch cross-correlation
- Wrapper: `silk/pitch_xcorr_asm.go` (and `pitch_xcorr.go`)
- Native sources:
  - `silk/pitch_xcorr_arm64.s`
- Fallback: `silk/pitch_xcorr_impl_default.go`
- Entry points:
  - `celtPitchXcorrFloatImpl`

3. NSQ short-term prediction
- Wrapper: `silk/nsq_pred_asm.go`
- Native sources:
  - `silk/nsq_pred_arm64.s`
- Fallback: `silk/nsq_pred_default.go`
- Entry points:
  - `shortTermPrediction16`
  - `shortTermPrediction10`

4. NSQ warped AR feedback
- Wrapper: `silk/nsq_warp_asm.go`
- Native sources:
  - `silk/nsq_warp_arm64.s`
- Fallback: `silk/nsq_warp_default.go`
- Entry points:
  - `warpedARFeedback24`
  - `warpedARFeedback16`

## Validation Guidance

1. Native architecture sanity:
```bash
go test ./celt ./silk -run '^$' -count=1
```
2. Full guardrails before merge:
```bash
make verify-production
make bench-guard
```

When assembly behavior is uncertain, confirm algorithmic parity against the pinned libopus reference in `tmp_check/opus-1.6.1/` before tuning.
