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

Stable pre-release surface: `Encoder`, `Decoder`, multistream encode/decode,
`container/ogg`, and caller-owned `Encode`/`Decode`.

Reference behavior comes from `tmp_check/opus-1.6.1/`. When behavior is
uncertain, match libopus unless fixture evidence says otherwise.

## Optional Extensions

Default builds support `SetDNNBlob(...)` only. QEXT and DRED require build tags.
QEXT requires `-tags gopus_qext`, and DRED control/standalone surfaces require `-tags gopus_dred`.
OSCE BWE remains quarantine-only and absent outside quarantine builds.

| Extension | Status | Probe |
| --- | --- | --- |
| DNN blob loading | Supported by default | `OptionalExtensionDNNBlob` |
| QEXT | Tagged support | `OptionalExtensionQEXT` |
| DRED | Tagged control/standalone support | `OptionalExtensionDRED` |
| OSCE BWE | Extra-control parity only | `OptionalExtensionOSCEBWE` |

```sh
go test -tags gopus_qext ./...
go test -tags gopus_dred ./...
go test -tags gopus_extra_controls ./...
```

```sh
make test-dnn-blob-parity
make test-qext-parity
make test-dred-tag
make test-extra-controls-parity
```

The `gopus_extra_controls` tag can expose parity helpers, but it does not
make extra features part of the public support claim.

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
CELT/range-coder math oracles, and SILK NLSF/LPC internals.

## Trust And Verification

Required branch checks:

<!-- required-checks:start -->
- `lint-static-analysis`
- `test-linux`
- `perf-linux`
- `test-macos`
- `test-windows`
<!-- required-checks:end -->

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

- [CONTRIBUTING.md](CONTRIBUTING.md)
- [SECURITY.md](SECURITY.md)
- [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)
- [examples/README.md](examples/README.md)
