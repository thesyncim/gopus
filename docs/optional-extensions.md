# Optional Extensions

Default builds support `SetDNNBlob(...)` only. QEXT and DRED require build tags. OSCE BWE remains quarantine-only.

| Extension | Status | Probe |
| --- | --- | --- |
| DNN blob loading | Supported by default | `OptionalExtensionDNNBlob` |
| QEXT | Tagged support | `OptionalExtensionQEXT` |
| DRED | Tagged control/standalone support | `OptionalExtensionDRED` |
| OSCE BWE | Unsupported and quarantined | `OptionalExtensionOSCEBWE` |

Use:
- QEXT: `go test -tags gopus_qext ./...`
- DRED: `go test -tags gopus_dred ./...`
- Quarantine parity only: `go test -tags gopus_unsupported_controls ./...`

Release gates:
- Default DNN blobs: `make test-dnn-blob-parity`
- QEXT: `make test-qext-parity`
- DRED: `make test-dred-tag`
- Quarantine parity: `make test-unsupported-controls-parity`

The `gopus_unsupported_controls` tag can expose parity helpers, but it does not make unsupported features part of the public support claim.
