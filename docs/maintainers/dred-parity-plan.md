# DRED Parity

DRED support is tag-gated with `gopus_dred`.

Required gates:
- `make test-dred-tag`
- `make test-unsupported-controls-parity`

Rules:
- Keep default builds dormant.
- Keep quarantine helpers under `gopus_unsupported_controls`.
- Claim only seams covered by libopus-backed parity tests.
- Preserve zero allocations on normal encode/decode hot paths.
