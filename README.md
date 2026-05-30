# gopus

Pure-Go Opus targeting RFC 6716 and parity with pinned libopus 1.6.1.

Primary caller-buffer API:

```go
func (d *Decoder) Decode(data []byte, pcm []float32) (int, error)
func (e *Encoder) Encode(pcm []float32, data []byte) (int, error)
```

Encode/decode hot paths are guarded for zero allocations.

## Current State

Released version: none yet.

`v0.1.0` is not a release until the tag and GitHub Release are both published.

Latest release evidence: none yet.

Stable pre-release surface: `Encoder`, `Decoder`, multistream encode/decode
(including projection/ambisonics via `NewProjectionEncoder`/`NewProjectionDecoder`),
`container/ogg`, `container/red` (RFC 2198 RED parse/build/recover), and
caller-owned `Encode`/`Decode`. The `float32`, `int16`, and `int24`
(`EncodeInt24`/`DecodeInt24`) PCM forms are all available on the single-stream
and multistream encode/decode paths.

Scope: the default build is core encode/decode/multistream/Ogg/RED, matching a
default libopus `./configure`. Optional features are exposed exactly the way
libopus exposes them — behind a compile flag in libopus, behind the matching Go
build tag here — and the default build links ZERO of their code (enforced by
`TestDefaultBuildIsZeroCostForGatedFeatures`). The tag <-> libopus-flag mapping:
`gopus_dred` = `--enable-dred`/`ENABLE_DRED`, `gopus_extra_controls` =
`--enable-osce`/`ENABLE_OSCE` plus the deep-PLC family (`ENABLE_DEEP_PLC`:
PitchDNN/FARGAN), `gopus_qext` = `--enable-qext`/`ENABLE_QEXT`, `gopus_custom` =
`--enable-custom-modes`/`CUSTOM_MODES`, `gopus_fixedpoint` =
`--enable-fixed-point`/`FIXED_POINT`. DRED, OSCE (BWE/LACE/NoLACE), QEXT
extension framing, and Opus Custom standard modes are parity-complete and
SUPPORTED under their build tag — none are experimental.

Two efforts are still in progress and honestly marked as such: native 96 kHz
(Opus HD) bitstream parity (the `-tags gopus_qext` 96 kHz path is a resampling
wrapper over the 48 kHz CELT core, not native-bitstream byte parity with
libopus's 96 kHz CELT mode), and the full fixed-point pipeline. Default-build
API sample rates are 8/12/16/24/48 kHz; 96 kHz is accepted only under
`-tags gopus_qext`.

Reference behavior comes from `tmp_check/opus-1.6.1/`. When behavior is
uncertain, match libopus unless fixture evidence says otherwise.

## Optional Extensions

Default builds expose no optional extensions; `SetDNNBlob(...)` is a no-op returning `ErrOptionalExtensionUnavailable`.
This matches a default libopus build, where `LPCNET_SOURCES` (the DNN / PitchDNN
/ FARGAN / RDOVAE neural code) is empty and none of it is compiled. gopus mirrors
that exactly: the neural packages (`internal/dred`, `internal/dred/rdovae`,
`internal/lpcnetplc`, `internal/osce`) are absent from the default import graph.
DNN blob loading (USE_WEIGHTS_FILE model loading) requires `-tags gopus_dred` or
`-tags gopus_extra_controls`; QEXT requires `-tags gopus_qext`; DRED
control/standalone surfaces require `-tags gopus_dred`; OSCE BWE/LACE/NoLACE
require `-tags gopus_extra_controls`. Under their build tag these are
parity-complete and supported, exactly as libopus exposes them behind the
corresponding compile flag.

| Extension | Status | Probe |
| --- | --- | --- |
| DNN blob loading | Supported under `gopus_dred` / `gopus_extra_controls` | `OptionalExtensionDNNBlob` |
| QEXT | Supported under `gopus_qext` | `OptionalExtensionQEXT` |
| DRED | Supported under `gopus_dred` (control + standalone) | `OptionalExtensionDRED` |
| OSCE BWE | Supported under `gopus_extra_controls` | `OptionalExtensionOSCEBWE` |

```sh
go test -tags gopus_qext ./...
go test -tags gopus_dred ./...
go test -tags gopus_extra_controls ./...
```

```sh
make test-dnn-blob-parity
make test-core-oracles-parity
make test-qext-parity
make test-dred-tag
make test-extra-controls-parity
make test-custom-parity
make test-corpus-quality
```

The `gopus_extra_controls` tag enables the OSCE and deep-PLC family exactly as
libopus's `--enable-osce` does. These features are supported under the tag and
link zero code into the default build.

## Verification

Run focused tests while iterating. Before merge-ready codec changes, run:

```sh
go test ./...
make test-doc-contract
make lint
make test-consumer-smoke
make test-examples-smoke
make verify-production
```

```sh
make verify-production-exhaustive
make release-evidence
```

`make release-evidence` must produce a PASS summary before a tag is published.
Current focused gates cover tagged DRED/QEXT seams, RED recovery ordering,
CELT/range-coder math oracles, and SILK NLSF/LPC/gain/stereo/LTP internals.
Core libopus oracles run in normal `go test`; strict gates only make setup failures fatal.

## Trust And Verification

Required branch checks:

<!-- required-checks:start -->
- `lint-static-analysis`
- `test-linux`
- `perf-linux`
- `test-macos`
- `test-windows`
<!-- required-checks:end -->

These aggregate gates make the libopus C-oracle parity suites mandatory across
platforms: the core float numeric oracle (`make test-core-oracles-parity`) runs
on Linux, macOS, and Windows; the tagged DRED and `--enable-fixed-point` oracle
gates (`make test-dred-tag`, `make test-fixedpoint-parity`) run on Linux and
macOS; the QEXT (`make test-qext-parity`), Opus Custom `--enable-custom-modes`
(`make test-custom-parity`), extended corpus signal-quality
(`make test-corpus-quality`), and extra-controls oracle gates run on Linux. Each
lane builds the pinned libopus C reference first (`make ensure-libopus*`) under
`GOWORK=off GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1`. Windows keeps the
core float oracle plus the `gopus_libopus_oracle` decoder/encoder fixture parity
smoke; the broad tagged bash focus-gate sweeps are not run under MSYS2/mingw.

Release checklist:

- select a `vMAJOR.MINOR.PATCH` tag
- confirm README and package docs agree
- run the verification commands
- attach release evidence summary and archive
- publish the tag and GitHub Release together

Supply-chain controls:

- Dependabot is enabled for GitHub Actions and Go modules.
- OpenSSF Scorecard runs on `master`, weekly, and by manual dispatch.
- Workflow permissions are least-privilege.
- Release evidence records commit SHA, Go version, platform, pinned libopus
  version and SHA256, command logs, benchmark guardrails, fuzz/safety summary,
  parity summary, and module inventory.
- Future binary releases need signed checksums, provenance, and an SPDX or CycloneDX SBOM.

Security reports: [SECURITY.md](SECURITY.md).
Consumer smoke test: [examples/external-consumer-smoke/smoke_test.go](examples/external-consumer-smoke/smoke_test.go).

## Public Docs

- [PARITY_MATRIX.md](PARITY_MATRIX.md) — libopus 1.6.1 coverage and remaining gaps
- [docs/parity-testing.md](docs/parity-testing.md) — how parity is tested (bit-exact kernels + self-selecting quality tiers)
- [CONTRIBUTING.md](CONTRIBUTING.md)
- [SECURITY.md](SECURITY.md)
- [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)
- [examples/README.md](examples/README.md)
