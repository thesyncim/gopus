# Gopus Production Plan

Last updated: 2026-05-05

## Objective

Ship `gopus` as a dependable production codec library with:
- deterministic behavior against pinned libopus references,
- stable real-time performance (zero hot-path allocations),
- explicit release gates that block regressions.

## Current Baseline

- Decoder feature-complete and stable across SILK/CELT/Hybrid.
- Encoder compliance summary and broad libopus-relative quality validation are green.
- Core parity/fixture coverage is green, and tag-gated DRED and QEXT parity are guarded by seam-specific libopus-backed tests before any broader support claims.
- Core CI is cross-platform, but production-readiness now depends more on fail-closed gates and public API hardening than on codec-gap hunting.

## Production Success Criteria

1. Correctness
- `TestEncoderComplianceSummary` remains green on pinned fixtures.
- Encoder mode and analysis fixture parity remain green against pinned libopus data.
- Exhaustive fixture honesty/provenance checks remain green on pinned libopus.

2. Real-time performance
- Hot-path `Encode`/`Decode` and int16 variants stay at `0 allocs/op`.
- Benchmark guardrails stay within CI thresholds (`make bench-guard`).
- Decoder and encoder throughput stay within pinned libopus-relative CI thresholds (`make bench-libopus-guard`).
- No race detector failures on fast-tier full package sweep.
- Optional deeper parity-tier race sweep remains available.

3. Operational confidence
- One-command production gate exists for pre-release verification.
- CI fails closed on package load/build issues.
- PR CI matches local production parity semantics closely enough that green means the same thing in both places.
- CI covers race, parity, provenance, and fuzz smoke in addition to the existing test suite.
- The docs contract gate pins default public API shape, public API round-trip/PLC/SILK smoke behavior, constructor/API-abuse validation, build-safety behavior, wrapper safety behavior, core hot-path allocation behavior, encoder encode-path, runtime-state, and routing behavior, single-stream and multistream control behavior, streaming reader/writer plus stream data-flow/end-to-end behavior, Ogg reader/writer contract and flow behavior, Ogg page primitives, packetization, metadata, projection demixing, and internal integration behavior, decoder decode-path behavior, decoder packet-state, normal FEC, provided-packet FEC, FEC/LBRR recovery behavior, multistream constructor/round-trip behavior, multistream sample-format/PLC behavior, internal multistream encoder mapping/packet assembly/policy behavior, internal multistream decoder mapping/conversion/full packet decode integration behavior, ambisonics/projection mapping behavior, multistream package round-trip/channel-isolation behavior, and multistream decoder duration behavior, unsupported-control quarantine, packet-extension dormancy/framing, parser-envelope behavior, and root repacketizer libopus fixture/envelope behavior before the heavier parity lanes run.
- CI and `make verify-production` include the default DNN blob gate, the supported DRED and QEXT tag gates, plus the unsupported-controls DRED parity sweep as required gates. The DNN blob gate covers pinned model blob admission, reset retention, dormant optional-runtime, DNN-ready PLC allocation flatness, and multistream allocation contracts. The DRED gates cover standalone wrapper lifecycle/no-allocation, libopus parse/decode/process metadata checks, real-packet standalone process state/feature parity, standalone recovery scheduling parity, DRED payload scanner edge cases, encoder runtime history/rate-conversion/scratch coverage, top-level decoder dormancy/cache lifecycle coverage, top-level decoder recovery/bridge lifecycle coverage, multistream DRED dormancy/cache failure-path coverage, multistream decoder recovery lifecycle coverage, decoder cached recovery bookkeeping parity, explicit 60% loss quality smoke, stereo cached recovery lifecycle/cursor seams, the SILK wideband 20/40/60 ms mono and 20 ms stereo carried-payload proofs, Hybrid fullband 20/40 ms mono/stereo carried-payload/packet-envelope proofs, final/non-final uncoupled mono, final/non-final single-coupled stereo, and final/non-final non-leading second-coupled multistream CELT/Hybrid/SILK DRED consumers, parser availability, internal converter/payload/basic-analysis seams, the real-model PitchDNN and RDOVAE encoder oracles, the libopus DRED latent trace guard, the conceal-analysis oracle, 48 kHz runtime bootstrap checks, the required mono decoder explicit/live numerical matrix, selected 16 kHz Hybrid mono live-sequence seams, CELT/Hybrid stereo cached/live first/second-loss and next-packet handoff matrices, selected 16 kHz CELT/Hybrid stereo explicit first-loss probes, explicit first-loss and recovery lifecycle/cursor seams, 48 kHz / 16 kHz SILK WB explicit mono first-loss seams, and the 48 kHz SILK WB explicit stereo first-loss seam. The QEXT gate builds a separate QEXT-enabled libopus reference tree and runs no-skip packet-extension parity plus separate reference-tool lookup, packet generator/iterator coverage, and repacketizer/self-delimited extension preservation coverage under `gopus_qext` while default builds keep QEXT controls absent and packet-extension plumbing dormant. Broader Hybrid/SILK primary-frame byte exactness, broader SILK stereo packet/mode matrices, broader multistream packet/mode coverage, and support-surface graduation remain outside the production gate.

4. Public contract clarity
- Streaming and container constructors fail fast on misuse instead of panicking later.
- User-facing docs/examples steer callers toward the caller-owned hot path.
- Public error messages and supported-range docs stay accurate across mono/stereo and multistream APIs.

## Execution Phases

### Phase 1: Guardrail Integrity
- Make wrapper test runners fail closed on package load/build errors.
- Align PR Linux parity lanes with strict libopus reference mode.
- Keep fixture honesty, provenance, and fuzz smoke visible in normal PR gating.

### Phase 2: Public API Hardening
- Reject nil streaming/container endpoints and invalid streaming sample formats at construction time.
- Remove avoidable panic paths from public wrapper configuration surfaces.
- Tighten user-facing error text and examples around multistream and caller-owned buffers.

### Phase 3: Release Discipline
- Keep release checklist and status docs in sync with the real baseline.
- Run production gate before every tag/release candidate.
- Publish benchmark, compliance, and provenance evidence per release.

## What This Change Implements

- Existing production-readiness work already delivered:
  - `TestHotPathAllocsEncodeFloat32`
  - `TestHotPathAllocsEncodeInt16`
  - `TestHotPathAllocsDecodeFloat32`
  - `TestHotPathAllocsDecodeInt16`
  - `TestHotPathAllocsDecodePLC`
  - `TestHotPathAllocsDecodeStereo`
- Existing gate surfaces:
  - `make test-race`
  - `make test-quality`
  - `make test-fuzz-smoke`
  - `make test-dred-tag`
  - `make test-qext-parity`
  - `make test-unsupported-controls-parity`
  - `make verify-production`
  - `make verify-production-exhaustive`
  - `make bench-guard`
  - `make bench-libopus-guard`
  - `make release-evidence`
- Added scheduled exhaustive CI with release-evidence artifact upload.
- Added dedicated CI performance gate (`perf-linux`) using deterministic benchmark guardrails (`tools/bench_guardrails.json`) plus pinned-libopus relative decoder and encoder guardrails.
