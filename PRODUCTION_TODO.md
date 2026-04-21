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
- [x] Document CI guardrail/branch-protection policy (`CI_GUARDRAILS.md`) and the concise project brief (`AGENTS.md`).
- [x] Close the previously-known large encoder quality regressions and return `make test-quality` to green.
- [x] Expand zero-allocation hard gates to cover PLC and stereo decode hot paths.
- [x] Make CELT opusdec crossval fixture coverage fail closed instead of silently rewriting tracked fixtures during normal test runs.
- [x] Remove deprecated debug/state wrappers from the pre-release public surface.
- [x] Simplify repo workflow/docs by removing the old experiment scaffolding.
- [x] Reduce CELT tuning/test surface area by making tonality and spread helpers package-private and deleting unused table accessor exports.
- [x] Remove unused encoder/CELT diagnostic surface by deleting VAD tracing APIs and hiding the dead coarse-decision hook.
- [x] Collapse dead coarse-energy hook branches, drop the unused raw band-energy accessor, and simplify VAD helper returns.

## In progress now

- [x] Tighten multistream-facing error/reporting text so ranges stay accurate for high-channel-count use.

## Remaining medium-term blockers

- [ ] Reduce temporary debug/tuning surface area in non-test packages without disturbing parity coverage.
- [ ] Trim dead or redundant tests now that the main parity/quality gates are trustworthy.

## Optional stretch goals

- [ ] Expand long-running safety soak beyond mono root paths into stereo, streaming, multistream, and container surfaces.
- [ ] Add architecture-specific performance dashboards (arm64 vs amd64).
