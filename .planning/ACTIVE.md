# Active Investigation

Last updated: 2026-02-13
Status: active

## Objective

Close the remaining strict encoder quality gap (`Q >= 0`) while preserving libopus 1.6.1 parity and zero-allocation hot paths.

## Current Hypothesis

With feature-parity checklist items complete, the best ROI is targeted quality uplift on the current worst compliance profiles, always validated against libopus fixtures.

## Next 3 Actions (Targeted)

1. Reproduce the current worst profile from `TestEncoderComplianceSummary`.
2. Diff the exact encoder control/math path against `tmp_check/opus-1.6.1`.
3. Apply one bounded change, rerun focused tests, then run `make verify-production` and `make bench-guard`.

## Feature Parity Plan (libopus 1.6.1)

- [x] Complete: Wire full multistream surround-analysis and energy-mask production into `surroundTrim` control flow for alloc-trim parity.
- [x] Complete: Implement LFE-aware multistream handling parity (stream detection, mapping policy, allocation effects).
- [x] Complete: Match libopus surround per-stream control policy parity (mode forcing, channel decisions, bandwidth policy).
- [x] Complete: Close remaining public CTL/API parity gaps versus libopus request/set/get surfaces.
- [x] Complete: Add repacketizer API parity coverage with fixture-validated behavior.
- [x] Complete: Tighten ambisonics behavior parity (mapping/control/packet behavior parity tests).

## Explicit Skips For This Session

- Skip re-debugging SILK decoder correctness unless decoder-path files are touched.
- Skip re-debugging resampler parity unless resampler-path files are touched.
- Skip re-investigating NSQ constant-DC amplitude behavior unless evidence conflicts.

## Stop Conditions

- Stop and reassess after 3 failed hypotheses without measurable quality uplift.
- Run broad gate (`make verify-production`) only once a focused change is ready.

## Evidence Log (Newest First)

- 2026-02-13: Closed libopus max-bandwidth CTL validation gap in root wrappers: `Encoder.SetMaxBandwidth` and `MultistreamEncoder.SetMaxBandwidth` now reject invalid bandwidth values with `ErrInvalidBandwidth` instead of silently accepting unknown enums. Added/updated tests in `encoder_test.go`, `multistream_test.go`, and API roundtrip coverage in `api_test.go`; `make verify-production` passed.
- 2026-02-13: Closed a concrete multistream CTL parity gap: `MultistreamEncoder.SetSignal` now mirrors libopus `OPUS_SET_SIGNAL_REQUEST` validation semantics by rejecting invalid signal values with `ErrInvalidSignal` instead of silently accepting arbitrary integers. Added coverage in `TestMultistreamEncoder_Controls` for valid voice/music transitions and invalid-value rejection. Validation: focused root/multistream control tests plus `make verify-production` (includes parity + bench-guard + race) passed.
- 2026-02-13: Compacted planning docs to reduce context load; full history archived in `.planning/archive/ACTIVE_2026-02-13_full.txt`, `.planning/archive/DECISIONS_2026-02-13_full.txt`, and `.planning/archive/WORK_CLAIMS_2026-02-13_full.txt`.
- 2026-02-13: Closed delay-compensation parity gap by gating on low-delay application state instead of forced CELT mode; focused tests + `make verify-production` + `make bench-guard` passed.
- 2026-02-13: Closed multistream application forwarding parity by propagating application policy to all stream encoders without clobbering bitrate/complexity.
- 2026-02-13: Closed lookahead and application-change lock parity in root wrappers to match libopus first-frame and low-delay behavior.
- 2026-02-12 to 2026-02-13: Completed surround trim producer flow, LFE-aware multistream policy, per-stream surround control policy, CTL/API surfaces, repacketizer parity fixture coverage, and ambisonics parity guards.
