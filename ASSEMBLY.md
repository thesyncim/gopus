# Assembly Coverage

Last updated: 2026-03-14

`gopus` ships assembly only for helpers that stay bit-exact against the pure-Go path.

## Selection Model

- `arm64`: assembly is selected automatically by `GOARCH=arm64`.
- `amd64`: two helper groups exist:
  - `imdct_rotate` and `kf_bfly` use baseline amd64 assembly through the normal `*_asm.go` wrappers.
  - the wider CELT math helpers use runtime dispatch in `celt/amd64_dispatch.go`, guarded by `internal/cpufeat/amd64.go`.
- Runtime dispatch enables the AVX2+FMA helpers only when the host OS and CPU report both features. Otherwise the generic Go path is used.
- Non-`amd64`/`arm64` targets use the pure-Go fallbacks.

This means the AVX/FMA paths are not selected purely at compile time. `GOAMD64=v3` is not required for the current production dispatch.

## CELT Kernels

`arm64` kernels:

- `celt/abs_sum_arm64.s`
- `celt/exp_rotation_arm64.s`
- `celt/float32_arm64.s`
- `celt/haar1_arm64.s`
- `celt/imdct_rotate_arm64.s`
- `celt/kf_bfly5n1_arm64.s`
- `celt/kf_bfly_arm64.s`
- `celt/pitch_autocorr_arm64.s`
- `celt/pitch_xcorr_arm64.s`
- `celt/prefilter_innerprod_arm64.s`
- `celt/prefilter_xcorr_arm64.s`
- `celt/pvq_search_arm64.s`
- `celt/stereo_layout_arm64.s`
- `celt/tone_lpc_corr_arm64.s`
- `celt/transient_energy_arm64.s`

`amd64` baseline kernels:

- `celt/imdct_rotate_amd64.s`
- `celt/kf_bfly_amd64.s`

`amd64` runtime-dispatched AVX2/FMA kernels:

- `celt/abs_sum_amd64.s`
- `celt/exp_rotation_amd64.s`
- `celt/float32_amd64.s`
- `celt/pitch_autocorr_amd64.s`
- `celt/pitch_xcorr_amd64.s`
- `celt/prefilter_innerprod_amd64.s`
- `celt/prefilter_xcorr_amd64.s`
- `celt/pvq_search_amd64.s`
- `celt/tone_lpc_corr_amd64.s`
- `celt/transient_energy_amd64.s`

## SILK Kernels

`arm64` kernels:

- `silk/inner_prod_arm64.s`
- `silk/pitch_xcorr_arm64.s`

There is currently no shipped SILK amd64 assembly.

## Pure-Go Fallbacks

Representative fallback files for the assembly-backed helpers:

- `celt/abs_sum_default.go`
- `celt/exp_rotation_default.go`
- `celt/float32_default.go`
- `celt/haar1.go`, `celt/haar1_nonarm64.go`
- `celt/imdct_rotate_default.go`
- `celt/kf_bfly_default.go`
- `celt/kf_bfly5n1.go`, `celt/kf_bfly5n1_nonarm64.go`
- `celt/pitch_autocorr_default.go`
- `celt/pitch_xcorr_default.go`
- `celt/prefilter_innerprod_default.go`
- `celt/prefilter_xcorr_default.go`, `celt/prefilter_xcorr_fast_default.go`
- `celt/pvq_search_default.go`
- `celt/stereo_layout_impl.go`
- `celt/tone_lpc_corr_default.go`
- `celt/transient_energy_default.go`
- `silk/inner_prod_default.go`
- `silk/pitch_xcorr_impl_default.go`

## Validation

```bash
go test ./celt ./silk -count=1
GOARCH=amd64 GOAMD64=v1 go test ./celt -run '^TestAMD64DispatchMatchesGeneric$' -count=1
GOARCH=amd64 GOAMD64=v1 go test ./celt -count=1
make verify-production
make bench-guard
```

When assembly behavior is uncertain, confirm parity against the pinned libopus tree in `tmp_check/opus-1.6.1/` before tuning.
