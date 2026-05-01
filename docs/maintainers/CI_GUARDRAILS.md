# CI Guardrails

Last updated: 2026-05-01

## Goal

Block correctness and hot-path performance regressions before merge.

## What CI Enforces

1. Static-analysis gate (`lint-static-analysis`)
- `make test-doc-contract`
- `make lint`
- Runs the pinned `golangci-lint` baseline from `.golangci.yml`.
- Runs on markdown-only PRs too, so optional-extension docs changes still pass through the same static/docs contract checks.
- Intended to catch actionable vet/static-analysis issues without forcing broad codec-style churn.

2. Correctness gate (`test-linux`)
- `test-linux-parity`: `make test-quality`
- `test-linux-race`: `make ensure-libopus && make test-race`
- `test-linux-provenance`: fixture honesty in pinned Docker plus `make test-provenance`
- `test-linux-flake`: critical parity subset under shuffle/repeat with strict libopus reference enforcement and go-test JSON skip enforcement
- `test-linux-fuzz-smoke`: `make test-fuzz-smoke`
- `test-linux-consumer-smoke`: `make test-consumer-smoke`
- `test-linux-dnn-blob-parity`: `make test-dnn-blob-parity`
- `test-linux-dred-tag`: `make test-dred-tag`
- `test-linux-qext-parity`: `make test-qext-parity`
- `test-linux-unsupported-controls`: `make test-unsupported-controls-tag`
- `test-linux-unsupported-controls-parity`: `make test-unsupported-controls-parity`
- Internally split into parallel jobs (`test-linux-parity`, `test-linux-race`, `test-linux-provenance`, `test-linux-flake`, `test-linux-fuzz-smoke`, `test-linux-consumer-smoke`, `test-linux-dnn-blob-parity`, `test-linux-dred-tag`, `test-linux-qext-parity`, `test-linux-unsupported-controls`, `test-linux-unsupported-controls-parity`) and aggregated by `test-linux`.
- This keeps parity/race/provenance/fuzz coverage intact while removing serialized Linux checks from a single job.
- The default DNN blob gate validates top-level and multistream `SetDNNBlob(...)` controls against pinned libopus USE_WEIGHTS_FILE encoder/decoder model blobs and fails on skipped helper coverage.
- The supported DRED tag gate carries standalone DRED wrapper lifecycle/no-allocation coverage, standalone libopus parse/decode/process metadata proofs, real-packet standalone process state/feature parity, standalone recovery scheduling parity, decoder cached recovery bookkeeping parity, the libopus-backed SILK wideband 20/40/60 ms mono and 20 ms stereo encoder carried-payload/primary-frame proofs plus 20 ms primary-budget proof, and the Hybrid fullband 20 ms payload-only proof directly under `gopus_dred`; the unsupported-controls smoke and required DRED parity sweep stay separate so quarantine API exposure remains a small gate while bootstrap, the real-model PitchDNN and RDOVAE encoder oracles, the conceal-analysis oracle, selected bookkeeping seams, and the current mono decoder explicit/live numerical matrix also block the aggregate correctness check. Required DRED and unsupported-controls parity gates run through JSON no-skip enforcement so missing libopus helpers cannot silently pass. Hybrid primary-frame byte exactness and broader stereo/multistream decoder coverage remain open; `make test-unsupported-controls-parity-experimental` is limited to Hybrid packet-shape smoke outside supported-tag claims and also fails on skipped libopus-helper coverage.
- The supported QEXT tag gate builds a separate `tmp_check/opus-1.6.1-qext` libopus tree with `--enable-qext`, runs no-skip packet-extension parity under `gopus_qext`, and keeps default-build QEXT controls absent with packet-extension plumbing compile-time dormant unless the tag is set.

3. Performance gate (`perf-linux`)
- `make bench-guard`
- Runs deterministic benchmark guardrails from `tools/bench_guardrails.json`.
- Fails when median benchmark metrics exceed configured limits.

4. Cross-platform sanity
- `test-macos`: `go test ./... -count=1`
- `test-windows`: `go test ./... -count=1`
- `verify-safety` and the macOS arm64 assembly lane run `make test-assembly-safety`, which now validates native ASM, the `-tags=purego` scalar fallback build, focused differential tests, fuzz smoke, and official-vector parity for both native and purego paths.

5. Markdown-only behavior
- Keep the CI workflow trigger active for markdown/docs-only pull requests so required checks still report status.
- Run the same lint, parity, performance, and platform gates for markdown-only changes; optional-extension documentation changes can otherwise drift from the gated release surface.
- Do not use top-level workflow filters that suppress required checks or in-workflow markdown-only exits that convert skipped parity into green aggregate jobs.

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
- Require branches to be up to date before merging.
- Require code owner review for trust-sensitive files covered by `.github/CODEOWNERS`.
- Required checks:
<!-- required-checks:start -->
  - `lint-static-analysis`
  - `test-linux`
  - `perf-linux`
  - `test-macos`
  - `test-windows`
<!-- required-checks:end -->

Trust-sensitive files that must require owner review:
- `.github/workflows/*`
- `SECURITY.md`
- `README.md`
- `docs/optional-extensions.md`
- `docs/maintainers/**`
- `docs/releases/**`
- `tools/ensure_libopus.sh`
- `Makefile`

Without these settings, CI cannot fully prevent regressions from being merged.
