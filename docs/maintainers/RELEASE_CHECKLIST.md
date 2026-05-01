# Release Checklist

Use this checklist before cutting and publishing a release tag. A release does
not exist until both the version tag and matching GitHub Release are published.

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

Run from repository root. Publishing is blocked unless `make release-evidence`
produces a passing summary and the GitHub Release attaches that summary plus the
evidence archive.

- [ ] `go test ./...`
- [ ] `make test-doc-contract`
- [ ] `make lint`
- [ ] `make test-consumer-smoke`
- [ ] `make verify-production`
- [ ] `make verify-production-exhaustive`
- [ ] `make release-evidence` (captures a timestamped evidence bundle in `reports/release/` by default)

## Required Evidence to Attach

- [ ] Release evidence summary Markdown (`release-evidence-<timestamp>.md`).
- [ ] Release evidence archive (`release-evidence-<tag>.tar.gz`) containing command logs and inventories.
- [ ] Commit SHA.
- [ ] Go version.
- [ ] OS/platform.
- [ ] libopus reference version and SHA256.
- [ ] Commands run with pass/fail summaries.
- [ ] Benchmark guardrail result.
- [ ] Fuzz/safety summary.
- [ ] Parity summary.
- [ ] Consumer-smoke result.
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

- [ ] Tag pushed.
- [ ] GitHub Release published for the same tag.
- [ ] Release notes link verification evidence.
- [ ] GitHub Release attaches the evidence summary and archive generated from `reports/release/`.
- [ ] GitHub `Release` workflow completed successfully for the tag.
- [ ] Next iteration TODOs captured in `docs/maintainers/PRODUCTION_TODO.md`.
