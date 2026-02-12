# Release Checklist

Use this checklist before cutting a release tag.

## Release Metadata

- [ ] Version tag selected (for example, `v0.x.y`).
- [ ] Changelog/release notes drafted.
- [ ] Breaking changes called out explicitly.

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
- [ ] Race test output from `make test-race`.

## Sanity Checks

- [ ] `go test ./... -count=1` passes locally or in CI for the release commit.
- [ ] Linux/macOS/Windows CI green for the release commit.
- [ ] No unreviewed fixture or parity baseline deltas.

## Post-Release

- [ ] Tag pushed and release published.
- [ ] Release notes link verification evidence.
- [ ] Next iteration TODOs captured in `PRODUCTION_TODO.md`.
