# CI Guardrails

Last updated: 2026-04-30

## Goal

Block correctness and hot-path performance regressions before merge.

## What CI Enforces

1. Static-analysis gate (`lint-static-analysis`)
- `make test-doc-contract`
- `make lint`
- Runs the pinned `golangci-lint` baseline from `.golangci.yml`.
- Still validates the optional-extension docs contract on markdown-only PRs before skipping `golangci-lint`.
- Intended to catch actionable vet/static-analysis issues without forcing broad codec-style churn.

2. Correctness gate (`test-linux`)
- `test-linux-parity`: `make test-quality`
- `test-linux-race`: `make ensure-libopus && make test-race`
- `test-linux-provenance`: fixture honesty in pinned Docker plus `make test-provenance`
- `test-linux-flake`: critical parity subset under shuffle/repeat with strict libopus reference enforcement and go-test JSON skip enforcement
- `test-linux-fuzz-smoke`: `make test-fuzz-smoke`
- `test-linux-consumer-smoke`: `make test-consumer-smoke`
- `test-linux-dred-tag`: `make test-dred-tag`
- `test-linux-unsupported-controls`: `make test-unsupported-controls-tag`
- `test-linux-unsupported-controls-parity`: `make test-unsupported-controls-parity`
- Internally split into parallel jobs (`test-linux-parity`, `test-linux-race`, `test-linux-provenance`, `test-linux-flake`, `test-linux-fuzz-smoke`, `test-linux-consumer-smoke`, `test-linux-dred-tag`, `test-linux-unsupported-controls`, `test-linux-unsupported-controls-parity`) and aggregated by `test-linux`.
- This keeps parity/race/provenance/fuzz coverage intact while removing serialized Linux checks from a single job.
- The supported DRED tag gate carries standalone DRED wrapper lifecycle/no-allocation coverage, standalone libopus parse/decode/process metadata proofs, real-packet standalone process state/feature parity, standalone recovery scheduling parity, decoder cached recovery bookkeeping parity, and the libopus-backed narrow SILK wideband 20 ms encoder carried-payload/primary-budget proof directly under `gopus_dred`; the unsupported-controls smoke and required DRED parity sweep stay separate so quarantine API exposure remains a small gate while bootstrap, the real-model PitchDNN oracle, and selected bookkeeping seams also block the aggregate correctness check. The wider carried-payload matrix, RDOVAE encoder real-model oracle, conceal-analysis oracle, and decoder audio numerical matrix remain in `make test-unsupported-controls-parity-experimental` until their Linux matrix is green.

3. Performance gate (`perf-linux`)
- `make bench-guard`
- Runs deterministic benchmark guardrails from `tools/bench_guardrails.json`.
- Fails when median benchmark metrics exceed configured limits.

4. Cross-platform sanity
- `test-macos`: `go test ./... -count=1`
- `test-windows`: `go test ./... -count=1`

5. Markdown-only optimization (without blocking merges)
- Keep the CI workflow trigger active for markdown/docs-only pull requests so required checks still report status.
- Use in-workflow docs-only detection to skip heavy lint/test/perf jobs for markdown-only changes.
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
  - `BenchmarkEncoderEncode_CallerBuffer`
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
  - `lint-static-analysis`
  - `test-linux`
  - `perf-linux`
  - `test-macos`
  - `test-windows`
- Require branches to be up to date before merging.

Without these settings, CI cannot fully prevent regressions from being merged.
