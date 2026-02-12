# Gopus Production Plan

Last updated: 2026-02-12

## Objective

Ship `gopus` as a dependable production codec library with:
- deterministic behavior against pinned libopus references,
- stable real-time performance (zero hot-path allocations),
- explicit release gates that block regressions.

## Current Baseline

- Decoder feature-complete and stable across SILK/CELT/Hybrid.
- Encoder feature-complete with known quality gap in strict `Q >= 0` profiles.
- Broad parity and fixture coverage already exists.
- Core CI is cross-platform and includes fixture provenance checks.

## Production Success Criteria

1. Correctness
- `TestEncoderComplianceSummary` remains green on pinned fixtures.
- `TestSILKParamTraceAgainstLibopus` remains exact parity for canonical WB fixture.
- Exhaustive fixture honesty/provenance checks remain green on pinned libopus.

2. Real-time performance
- Hot-path `Encode`/`Decode` and int16 variants stay at `0 allocs/op`.
- No race detector failures on fast-tier full package sweep.
- Optional deeper parity-tier race sweep remains available.

3. Operational confidence
- One-command production gate exists for pre-release verification.
- CI covers race, parity, and fuzz smoke in addition to existing test suite.

4. Quality closure
- Raise remaining encoder profiles to strict production threshold (`Q >= 0`) without parity regressions.

## Execution Phases

### Phase 1: Hardening Guardrails (Now)
- Add zero-allocation regression guards for hot paths.
- Add explicit production verification make targets.
- Ensure README documents production verification workflow.

### Phase 2: Quality Closure (Next)
- Focus SILK/Hybrid speech-bitrate quality uplift.
- Tune CELT short-frame transients against libopus references.
- Introduce ratcheting quality thresholds per profile until all strict gates pass.

### Phase 3: Release Discipline
- Define release checklist with required gate evidence.
- Run production gate before every tag/release candidate.
- Publish benchmark and compliance deltas per release.

## What This Change Implements

- Removed per-frame FFT scratch allocation in encoder tonality analysis.
- Added hot-path allocation guard tests:
  - `TestHotPathAllocsEncodeFloat32`
  - `TestHotPathAllocsEncodeInt16`
  - `TestHotPathAllocsDecodeFloat32`
  - `TestHotPathAllocsDecodeInt16`
- Added make targets:
  - `make test-race`
  - `make test-race-parity`
  - `make test-fuzz-smoke`
  - `make verify-production`
  - `make verify-production-exhaustive`
