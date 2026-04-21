# Production TODO

Last updated: 2026-04-21

## Recently completed

- [x] Eliminate per-frame FFT scratch allocation in encoder analysis path.
- [x] Add zero-allocation hot-path guard tests for encode/decode float32 and int16 APIs.
- [x] Add production verification make targets (`test-race`, `test-fuzz-smoke`, `verify-production`, `verify-production-exhaustive`).
- [x] Wire Linux CI to run `make verify-production`.
- [x] Add scheduled CI workflow for `make verify-production-exhaustive`.
- [x] Add `RELEASE_CHECKLIST.md` with required release evidence gates.
- [x] Add `make release-evidence` artifact generator (gates + key benchmark bundle).
- [x] Add CI artifact upload for generated release evidence bundles.
- [x] Document production plan and verification workflow.
- [x] Add deterministic benchmark regression guard tooling (`tools/benchguard` + `tools/bench_guardrails.json`).
- [x] Add explicit CI performance gate (`perf-linux`) and wire `make bench-guard` into `verify-production`.
- [x] Document CI guardrail/branch-protection policy (`CI_GUARDRAILS.md`) and agent rules (`AGENTS.md`).
- [x] Close the previously-known large encoder quality regressions and return `make test-quality` to green.
- [x] Expand zero-allocation hard gates to cover PLC and stereo decode hot paths.

## In progress now

- [ ] Tighten multistream-facing error/reporting text so ranges stay accurate for high-channel-count use.

## Remaining medium-term blockers

- [ ] Decide whether debug-only accessors on public wrapper types should be deprecated or removed before the first stable release.
- [ ] Reduce temporary debug/tuning surface area in non-test packages without disturbing parity coverage.

## Optional stretch goals

- [ ] Expand long-running safety soak beyond mono root paths into stereo, streaming, multistream, and container surfaces.
- [ ] Add architecture-specific performance dashboards (arm64 vs amd64).
