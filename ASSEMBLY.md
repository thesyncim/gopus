# Assembly Coverage

Last updated: 2026-03-08

`gopus` ships production assembly only when it is exact, default-on, and worth keeping.
No user-passed build tag is required to enable assembly.

## Selection Model

- `arm64`: shipped assembly is baseline-safe and selected automatically by `GOARCH=arm64`.
- `amd64`: baseline-safe assembly stays on normal `amd64` wrappers; AVX/FMA-class CELT kernels use Go's standard `amd64.v3` source selection.
- `amd64 && !amd64.v3`: exact generic Go helpers are used for the CELT surfaces that require v3 instructions.
- Other architectures use pure Go fallbacks.

This matches the Go stdlib model more closely than runtime CPU probing. There is no production asm-enablement tag such as `gopus_*_asm`.

## CELT Assembly

Always-on `arm64` kernels:

- `celt/abs_sum_arm64.s`
- `celt/exp_rotation_arm64.s`
- `celt/float32_arm64.s`
- `celt/imdct_rotate_arm64.s`
- `celt/kf_bfly_arm64.s`
- `celt/pitch_autocorr_arm64.s`
- `celt/pitch_xcorr_arm64.s`
- `celt/prefilter_innerprod_arm64.s`
- `celt/prefilter_xcorr_arm64.s`
- `celt/pvq_search_arm64.s`
- `celt/tone_lpc_corr_arm64.s`
- `celt/transient_energy_arm64.s`

Always-on baseline-safe `amd64` kernels:

- `celt/imdct_rotate_amd64.s`
- `celt/kf_bfly_amd64.s`

`amd64.v3` CELT kernels:

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

The `amd64.v3` selection is wired through:

- `celt/amd64_dispatch_v3.go`
- `celt/amd64_dispatch_v1.go`
- `celt/amd64_dispatch_helpers.go`

## SILK Assembly

Always-on `arm64` kernels:

- `silk/inner_prod_arm64.s`
- `silk/pitch_xcorr_arm64.s`

There is currently no shipped SILK amd64 assembly. `amd64` and non-`arm64` builds use the exact Go fallbacks for those surfaces.

## Validation Guidance

1. Native sanity:
```bash
go test ./celt ./silk -count=1
```
2. Old-hardware amd64 fallback:
```bash
GOARCH=amd64 GOAMD64=v1 go test ./celt -run '^TestAMD64DispatchMatchesGeneric$' -count=1
GOARCH=amd64 GOAMD64=v1 go test ./celt -count=1
```
3. `amd64.v3` compile-time selection:
```bash
GOARCH=amd64 GOAMD64=v3 go test ./celt -run '^$' -c -o /tmp/celt_amd64v3.test
```
4. Broad guardrails before merge:
```bash
make verify-production
make bench-guard
```

When assembly behavior is uncertain, confirm algorithmic parity against the pinned libopus reference in `tmp_check/opus-1.6.1/` before tuning.
