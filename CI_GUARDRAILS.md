# CI Guardrails

Last updated: 2026-02-12

## Goal

Block correctness and hot-path performance regressions before merge.

## What CI Enforces

1. Correctness gate (`test-linux`)
- `test-linux-parity`: `make ensure-libopus && GOPUS_TEST_TIER=parity go test ./... -count=1`
- `test-linux-race`: `make ensure-libopus && make test-race`
- `make docker-test-exhaustive` via `test-linux-provenance`
- `test-linux-flake`: critical parity subset under shuffle/repeat with go-test JSON skip enforcement
- Internally split into parallel jobs (`test-linux-parity`, `test-linux-race`, `test-linux-provenance`, `test-linux-flake`) and aggregated by `test-linux`.
- This keeps parity/race/provenance coverage intact while removing serialized Linux checks from a single job.

2. Performance gate (`perf-linux`)
- `make bench-guard`
- Runs deterministic benchmark guardrails from `tools/bench_guardrails.json`.
- Fails when median benchmark metrics exceed configured limits.

3. Cross-platform sanity
- `test-macos`: `go test ./... -count=1`
- `test-windows`: `go test ./... -count=1`

4. Markdown-only optimization (without blocking merges)
- Keep the CI workflow trigger active for markdown/docs-only pull requests so required checks still report status.
- Use in-workflow docs-only detection to skip heavy test/perf jobs for markdown-only changes.
- Do not use top-level workflow filters that suppress all required checks and leave PRs in "Expected" state.

## Benchmark Guardrails

Benchmark command is orchestrated by `tools/benchguard`:
- Package: `.`
- CPU: `1` (`GOMAXPROCS=1`)
- Count: `5`
- Benchtime: `200ms`
- Benchmarks:
  - `BenchmarkDecoderDecode_CELT`
  - `BenchmarkDecoderDecodeInt16`
  - `BenchmarkEncoderEncode`
  - `BenchmarkEncoderEncodeInt16`

Guardrail thresholds live in `tools/bench_guardrails.json`.

## Threshold Change Policy

Threshold changes are allowed only when all are true:
- There is a measured reason (hardware variance, intentional tradeoff, or known unavoidable cost).
- The change includes updated evidence from `make bench-guard`.
- A reviewer explicitly signs off on the threshold adjustment.

Never raise thresholds just to hide regressions.

## Branch Protection Setup (Repository Settings)

Enable branch protection for `master` with:
- Require pull request before merging.
- Require status checks to pass before merging.
- Required checks:
  - `test-linux`
  - `perf-linux`
  - `test-macos`
  - `test-windows`
- Require branches to be up to date before merging.

Without these settings, CI cannot fully prevent regressions from being merged.
