# CI Guardrails

Last updated: 2026-05-05

## Goal

Block correctness and hot-path performance regressions before merge.

## What CI Enforces

1. Static-analysis gate (`lint-static-analysis`)
- `make test-doc-contract`
- `make lint`
- Runs the pinned `golangci-lint` baseline from `.golangci.yml`.
- Runs on markdown-only PRs too, so optional-extension docs changes still pass through the same static/docs contract checks, default public API surface checks, default decoder packet-state/FEC checks, default-build optional-control semantics/quarantine checks, default packet-extension dormancy/framing checks, default parser-envelope checks, and root repacketizer libopus fixture/envelope checks.
- Intended to catch actionable vet/static-analysis issues without forcing broad codec-style churn.

2. Correctness gate (`test-linux`)
- `test-linux-parity`: `make test-quality`
- `test-linux-race`: `make ensure-libopus && make test-race`
- `test-linux-provenance`: fixture honesty in pinned Docker plus `make test-provenance`
- `test-linux-flake`: critical parity subset under shuffle/repeat with strict libopus reference enforcement and go-test JSON skip enforcement
- `test-linux-fuzz-smoke`: `make test-fuzz-smoke`
- `test-linux-consumer-smoke`: `make test-consumer-smoke`
- `test-linux-examples-smoke`: `make test-examples-smoke`
- `test-linux-dnn-blob-parity`: `make test-dnn-blob-parity`
- `test-linux-dred-tag`: `make test-dred-tag`
- `test-linux-qext-parity`: `make test-qext-parity`
- `test-linux-unsupported-controls`: `make test-unsupported-controls-tag`
- `test-linux-unsupported-controls-parity`: `make test-unsupported-controls-parity`
- Internally split into parallel jobs (`test-linux-parity`, `test-linux-race`, `test-linux-provenance`, `test-linux-flake`, `test-linux-fuzz-smoke`, `test-linux-consumer-smoke`, `test-linux-examples-smoke`, `test-linux-dnn-blob-parity`, `test-linux-dred-tag`, `test-linux-qext-parity`, `test-linux-unsupported-controls`, `test-linux-unsupported-controls-parity`) and aggregated by `test-linux`.
- This keeps parity/race/provenance/fuzz coverage intact while removing serialized Linux checks from a single job.
- The examples smoke gate compiles and tests maintained default examples, the DRED/QEXT tagged example builds, and the separate WebRTC example modules so user-facing sample code cannot drift from the supported control surface.
- The default DNN blob gate validates top-level and multistream `SetDNNBlob(...)` controls against pinned libopus USE_WEIGHTS_FILE encoder/decoder model blobs, reset retention, fixture shape, dormant optional-runtime, DNN-ready PLC allocation flatness, and multistream allocation contracts, and fails on skipped helper coverage.
- The supported DRED tag gate carries standalone DRED wrapper lifecycle/no-allocation coverage, standalone libopus parse/decode/process metadata proofs, real-packet standalone process state/feature parity, standalone recovery scheduling parity, DRED payload scanner edge-case coverage, internal DRED header/cache/request/window/no-allocation invariants, encoder DRED runtime history/rate-conversion/scratch coverage, single-stream decoder DRED dormancy/cache lifecycle coverage, top-level decoder DRED recovery/bridge lifecycle coverage, multistream DRED dormancy/cache failure-path coverage, multistream decoder DRED recovery lifecycle coverage, decoder cached recovery bookkeeping parity, explicit 60% loss quality smoke, stereo cached recovery lifecycle/cursor seams, the libopus-backed SILK wideband 20/40/60 ms mono and 20 ms stereo encoder carried-payload proofs, Hybrid fullband 20/40 ms mono/stereo carried-payload/packet-envelope proofs, and final/non-final uncoupled mono, final/non-final single-coupled stereo, and final/non-final non-leading second-coupled multistream CELT/Hybrid/SILK DRED consumers directly under `gopus_dred`; the unsupported-controls smoke mirrors payload scanner, encoder runtime history/rate-conversion/scratch coverage, decoder dormancy/cache, recovery/bridge lifecycle, explicit 60% loss quality smoke, multistream DRED dormancy/cache/ready coverage, and multistream decoder DRED recovery lifecycle coverage while the required DRED parity sweep stays separate so quarantine API exposure remains a small gate while bootstrap, the real-model PitchDNN and RDOVAE encoder oracles, the libopus DRED latent trace guard, the conceal-analysis oracle, selected bookkeeping seams, the required mono decoder explicit/live numerical matrix, selected 16 kHz Hybrid mono live-sequence seams, CELT/Hybrid stereo cached/live first/second-loss and next-packet handoff matrices, selected 16 kHz CELT/Hybrid stereo explicit first-loss probes, explicit first-loss and recovery lifecycle/cursor seams, 48 kHz / 16 kHz SILK WB explicit mono first-loss seams, the 48 kHz SILK WB explicit stereo first-loss seam, and stereo cached recovery lifecycle/cursor seams also block the aggregate correctness check. Required DRED and unsupported-controls parity gates run through JSON no-skip enforcement so missing libopus helpers cannot silently pass. Broader Hybrid/SILK primary-frame byte exactness, broader SILK stereo packet/mode matrices, and broader multistream packet/mode coverage remain open, and `make test-unsupported-controls-parity-experimental` is a legacy alias for the required quarantine parity gate.
- The supported QEXT tag gate builds a separate `tmp_check/opus-1.6.1-qext` libopus tree with `--enable-qext`, runs no-skip packet-extension parity plus separate reference-tool lookup, packet generator/iterator coverage, and repacketizer/self-delimited extension preservation coverage under `gopus_qext`, and keeps default-build QEXT controls absent with packet-extension plumbing compile-time dormant unless the tag is set.

3. Performance gate (`perf-linux`)
- `make bench-guard`
- `make bench-libopus-guard`
- Runs deterministic benchmark guardrails from `tools/bench_guardrails.json`.
- Fails when median benchmark metrics exceed configured limits.
- Compares decoder and encoder throughput against pinned libopus 1.6.1 on the same runner and fails when configured `gopus/libopus` ratios are exceeded.

4. Cross-platform sanity
- `test-macos`: `go test ./... -count=1`
- `test-windows`: `go test ./... -count=1`
- `verify-safety` and the macOS arm64 assembly lane run `make test-assembly-safety`, which now validates native ASM, the `-tags=purego` scalar fallback build, focused differential tests, fuzz smoke, and official-vector parity for both native and purego paths.

5. Markdown-only behavior
- Keep the CI workflow trigger active for markdown/docs-only pull requests so required checks still report status.
- Run the same lint, parity, performance, and platform gates for markdown-only changes; optional-extension documentation changes can otherwise drift from the gated release surface.
- Do not use top-level workflow filters that suppress required checks or in-workflow markdown-only exits that convert skipped parity into green aggregate jobs.

## Benchmark Guardrails

Absolute hot-path benchmark checks are orchestrated by `tools/benchguard`:
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

Libopus-relative decode checks are orchestrated by `tools/testvectorbenchcmp` through `make bench-libopus-guard`:
- Baseline: pinned libopus 1.6.1 from `tmp_check/opus-1.6.1/`
- Inputs: official RFC 8251 bitstreams under `testvectors/testdata/opus_testvectors/`; the Make target runs `ensure-testvectors` first so CI fetches, extracts, and parser-validates the cache before benchmarking.
- Cases: aggregate official-vector decode
- Paths: Float32 and Int16
- Default minimum runtime: `200ms`
- Default count: `3`
- Default max `gopus/libopus` ratio: `1.60x`
- Default max `gopus` allocations/op: `0`

The decoder ratio is intentionally wider than local release-report numbers to
absorb GitHub hosted-runner variance while still comparing directly against
pinned libopus on the same runner. Keep allocation enforcement at `0`.

Libopus-relative encoder checks are orchestrated by `tools/encoderbenchcmp` through the same `make bench-libopus-guard` target:
- Baseline: pinned libopus 1.6.1 from `tmp_check/opus-1.6.1/`
- Inputs: generated deterministic float32 signals from `internal/testsignal`
- Cases: aggregate plus per-workload CELT, SILK, and Hybrid encode workloads
- Path: Float32 caller-owned public encode path
- Default minimum runtime: `200ms`
- Default count: `3`
- Default max `gopus/libopus` ratio: `2.50x`
- Allocation enforcement remains in `make bench-guard`; the encoder libopus-relative suite is a throughput comparison.

For exploratory reports, use `make bench-testvectors-compare` or regenerate `docs/testvector-benchmarks.md` with `make bench-testvectors-report`.

## Threshold Change Policy

Threshold changes are allowed only when all are true:
- There is a measured reason (hardware variance, intentional tradeoff, or known unavoidable cost).
- The change includes updated evidence from `make bench-guard` and, for libopus-relative throughput thresholds, `make bench-libopus-guard`.
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
