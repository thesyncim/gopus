# gopus

`gopus` is a pure-Go implementation of Opus targeting RFC 6716/libopus parity.

Primary API:

```go
func (d *Decoder) Decode(data []byte, pcm []float32) (int, error)
func (e *Encoder) Encode(pcm []float32, data []byte) (int, error)
```

The hot encode/decode paths use caller-owned buffers and are guarded for zero allocations.

## Status

Released version: none yet.

`v0.1.0` is not a release until the tag and GitHub Release are both published.

Latest release evidence: none yet.

## Optional Extensions

Default builds support `SetDNNBlob(...)` only. QEXT requires `-tags gopus_qext`; DRED control/standalone surfaces require `-tags gopus_dred`; OSCE BWE remains unsupported outside quarantine builds.

See [Optional Extensions](docs/optional-extensions.md).

## Verification

```sh
go test ./...
make test-doc-contract
make verify-production
```

## Trust And Verification

- [required checks and branch protection](docs/maintainers/CI_GUARDRAILS.md)
- [private reporting and supported versions](SECURITY.md)
- [release checklist](docs/maintainers/RELEASE_CHECKLIST.md)
- [Dependabot, Scorecard, action review, and release provenance plan](docs/maintainers/SUPPLY_CHAIN.md)
- [external consumer smoke test](examples/external-consumer-smoke/smoke_test.go)
