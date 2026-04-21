# Release Checklist

Use this checklist before cutting and publishing a release tag.

## Release Metadata

- [ ] Version tag selected (for example, `v0.x.y`).
- [ ] Release notes drafted in `docs/releases/` (for example, `docs/releases/v0.1.0.md`).
- [ ] Breaking changes called out explicitly.
- [ ] Stable core called out explicitly:
  - [ ] `Encoder`
  - [ ] `Decoder`
  - [ ] multistream encode/decode
  - [ ] `container/ogg`
  - [ ] caller-owned `Encode` / `Decode` hot path
- [ ] Experimental, unsupported, or intentionally omitted features called out explicitly.

## Required Verification Gates

Run from repository root and keep command output for release evidence.

- [ ] `make verify-production`
- [ ] `make verify-production-exhaustive`
- [ ] `make release-evidence` (captures a timestamped evidence bundle in `reports/release/` by default)

## Required Evidence to Attach

- [ ] `TestEncoderComplianceSummary` output (pass/fail summary).
- [ ] `TestSILKParamTraceAgainstLibopus` output.
- [ ] Hot-path allocation guard output:
  - [ ] `TestHotPathAllocsEncodeFloat32`
  - [ ] `TestHotPathAllocsEncodeInt16`
  - [ ] `TestHotPathAllocsDecodeFloat32`
  - [ ] `TestHotPathAllocsDecodeInt16`
  - [ ] `TestHotPathAllocsDecodePLC`
  - [ ] `TestHotPathAllocsDecodeStereo`
- [ ] Race test output from `make test-race`.

## Sanity Checks

- [ ] `go test ./... -count=1` passes locally or in CI for the release commit.
- [ ] Linux/macOS/Windows CI green for the release commit.
- [ ] No unreviewed fixture or parity baseline deltas.
- [ ] README and package docs describe the same supported default-build surface.
- [ ] `make lint` passes on the release commit.
- [ ] External-consumer smoke path passes on the release commit.

## Post-Release

- [ ] Tag pushed and release published.
- [ ] Release notes link verification evidence.
- [ ] GitHub release attaches or links the evidence bundle from `reports/release/`.
- [ ] GitHub `Release` workflow completed successfully for the tag.
- [ ] Next iteration TODOs captured in `PRODUCTION_TODO.md`.
