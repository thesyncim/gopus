# Active Investigation

Last updated: 2026-03-01
Status: active

Objective: tighten fixture-backed libopus 1.6.1 parity while preserving zero-allocation encode/decode hot paths.

Older evidence entries were intentionally pruned on 2026-03-01 to keep this file operationally small.

## Current Snapshot

- Decoder parity remains stable across CELT/SILK/Hybrid lanes covered by the current fixture matrix.
- Encoder compliance summary is currently green under parity tier (`23 passed, 0 failed`).
- Recent merged hardening loops:
  - PR #259: tightened final CELT compliance override floor to `0.191 dB`.
  - PR #260: tightened `SILK-WB-60ms-mono-32k|impulse_train_v1` ratchet floors.
  - PR #261: tightened `SILK-WB-60ms-mono-32k|am_multisine_v1` ratchet floors.
- Required CI matrix is green on the latest merged loop (#261): linux parity/provenance/race/flake + macOS + windows + perf.
- Local broad gate note: `make verify-production` still hits the known local `tmp_check` cgo-disabled blocker; non-`tmp_check` packages pass.

## Remaining Gaps (Current)

- No blocking parity/compliance failures in the current guarded fixture set.
- Main remaining work is ratchet hardening and fixture-matrix expansion for edge lanes with small residual headroom (especially cross-OS variance lanes).

## Next 3 Actions

1. Tighten the next weakest non-frozen ratchet lane with repeated arm64/amd64 evidence.
2. Expand fixture-backed stress/provenance coverage for lanes still requiring wider floor slack on one OS.
3. Keep running broad verification (`make verify-production`, `make bench-guard`) before merge-ready changes.

## Explicit Session Skips

- Do not re-debug SILK decoder correctness without new contradictory evidence.
- Do not re-debug resampler parity unless resampler code/fixtures change.
- Do not revisit NSQ constant-DC behavior unless fixtures indicate regression.

## Evidence Log (Newest First)

- 2026-03-01: Tightened the next SILK ratchet slack lanes in `testvectors/testdata/encoder_compliance_variants_ratchet_baseline*.json`: `SILK-WB-40ms-mono-32k|am_multisine_v1` default floor `-0.04 -> -0.03`, amd64 floor `-0.08 -> -0.05`, and `SILK-WB-60ms-mono-32k|impulse_train_v1` amd64 floor `-0.08 -> -0.05` (default remained `-0.04` after repeated arm64 probes stayed at `gap=-0.04 dB`). Focused repeated subtest probes: arm64 `SILK-WB-40ms am` stayed at `gap=-0.00 dB` and amd64 at `gap=0.00 dB`; arm64 `SILK-WB-60ms impulse` stayed at `gap=-0.04 dB` while amd64 stayed at `gap=-0.00 dB` (`TestEncoderVariantProfileParityAgainstLibopusFixture/cases/...`; expected parent-level `ratchet baseline coverage mismatch` on subtest-only invocation). Validation passed with tightened floors: `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderVariantProfileParityAgainstLibopusFixture -count=1`, `GOARCH=amd64 GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderVariantProfileParityAgainstLibopusFixture -count=1`, `GOPUS_TEST_TIER=exhaustive go test ./testvectors -run TestEncoderVariantProfileProvenanceAudit -count=1 -v`, `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v`, and `make bench-guard`. `make verify-production` remained locally blocked only by known `tmp_check` cgo-disabled setup while non-`tmp_check` packages (including `testvectors`) passed.
- 2026-03-01: Merged PR #261 (`test(testvectors): tighten silk wb 60ms am ratchet floors`) with full green CI; tightened `SILK-WB-60ms-mono-32k|am_multisine_v1` floors to default `-0.03`, amd64 `-0.05`.
- 2026-03-01: Merged PR #260 (`test(testvectors): tighten silk wb 60ms impulse ratchet floors`) with full green CI; tightened `SILK-WB-60ms-mono-32k|impulse_train_v1` floors to default `-0.04`, amd64 `-0.08`.
- 2026-03-01: Merged PR #259 (`test(testvectors): tighten final celt compliance override floor`) with full green CI; tightened CELT override `0.20 -> 0.191`.
- 2026-02-28: Compliance summary stabilized at `23 passed, 0 failed` after source-aligned cadence and row-level no-negative override calibration.
- 2026-02-28: Compliance reference-Q path aligned to libopus-only decode order (direct helper first, `opusdec` fallback, no internal fallback for fixture calibration).
- 2026-02-28: Ported hybrid held-frame SILK/Hybrid->CELT transition redundancy cadence and closed `HYBRID-SWB-20ms-mono-48k|am_multisine_v1` negative residual lane.
