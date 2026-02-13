# Active Investigation

Last updated: 2026-02-13
Status: active

## Objective

Close the remaining strict encoder quality gap (`Q >= 0`) while preserving libopus 1.6.1 parity and zero-allocation hot paths.

## Current Hypothesis

With feature-parity checklist items complete, remaining gaps should be closed by direct libopus 1.6.1 source ports (math/control flow/state cadence), not heuristic retuning, and then validated against libopus fixtures.

## Next 3 Actions (Targeted)

1. Reproduce the current worst profile from `TestEncoderComplianceSummary`.
2. Identify the exact corresponding libopus path in `tmp_check/opus-1.6.1` and port it directly (no heuristic substitutes).
3. Rerun focused parity fixtures, then run `make verify-production` and `make bench-guard`.

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

- 2026-02-13: Ported libopus `tonality_analysis_reset()` behavior in `encoder/analysis.go`: `Reset()` now clears full reset-scoped analyzer state (angles/history/energy trackers/info queues/RNN state/downmix state) while preserving reusable configuration and scratch allocations. Added `TestTonalityAnalysisResetClearsState` in `encoder/analysis_test.go`. Validation: focused encoder analysis tests, `TestEncoderVariantProfileParityAgainstLibopusFixture`, `TestEncoderComplianceSummary`, `make verify-production`, and `make bench-guard` passed.
- 2026-02-13: Ported libopus analyzer NaN guard behavior in `encoder/analysis.go`: after FFT, if output is NaN (`out[0].r`), the current analysis slot is marked invalid and returned immediately without feature/MLP updates or counter increments, matching `analysis.c` (`info->valid = 0; return`). Added focused coverage in `encoder/analysis_test.go` (`TestRunAnalysisNaNInputMarksInfoInvalid`). Validation: focused encoder analysis tests, `TestEncoderVariantProfileParityAgainstLibopusFixture`, `TestEncoderComplianceSummary`, `make verify-production`, and `make bench-guard` passed.
- 2026-02-13: Ported libopus digital-silence handling in `tonality_analysis()`: `encoder/analysis.go` now checks the full 30 ms analysis buffer for digital silence, advances write slot, copies prior analysis info, and returns before FFT/feature/MLP updates (matching `analysis.c` control flow). Added focused coverage in `encoder/analysis_test.go` (`TestRunAnalysisSilenceCopiesPreviousInfo`, `TestRunAnalysisInitialSilenceKeepsInvalidInfo`). Validation: focused encoder analysis tests, `TestEncoderVariantProfileParityAgainstLibopusFixture`, `TestEncoderComplianceSummary`, `make verify-production`, and `make bench-guard` passed.
- 2026-02-13: Closed a concrete analyzer parity gap for 16 kHz input: `encoder/analysis.go` now ports libopus `downmix_and_resample()` behavior for `Fs==16000` (3x repeat then `silk_resampler_down2_hp` into 24 kHz analysis domain) in both initial-fill and residual paths, instead of dropping analysis updates on unsupported rate. Added focused coverage in `encoder/analysis_test.go` (`TestRunAnalysis16kProducesValidInfo`, `TestRunAnalysis16kLongFrameUses20msChunks`). Validation: focused analysis tests, `TestEncoderVariantProfileParityAgainstLibopusFixture`, `TestEncoderComplianceSummary`, `make verify-production`, and `make bench-guard` passed.
- 2026-02-13: Wired CELT prefilter `max_pitch_ratio` to libopus source of truth when analysis is valid: top-level encoder now forwards analyzer `MaxPitchRatio` into CELT analysis state, and `celt/encode_frame.go` prefers that value for `runPrefilter()` (fallback remains CELT-local estimator for standalone/invalid-analysis paths). Added focused coverage in `celt/analysis_maxpitch_test.go` (`TestSetAnalysisInfoClampsMaxPitchRatio`, `TestEncodeFrameUsesAnalysisMaxPitchRatioWhenValid`) and kept fixture parity coverage (`TestRunPrefilterParityAgainstLibopusFixture`). Validation: focused CELT tests + `TestEncoderVariantProfileParityAgainstLibopusFixture` + `TestEncoderComplianceSummary` passed.
- 2026-02-13: Ported a narrow libopus 1.6.1 analyzer math slice in `encoder/analysis.go`: added libopus-style high-band `max_pitch_ratio` tracking, `meanE`-based bandwidth masking/selection, and loudness tracker (`ETracker`/`LowECount`) updates while preserving the existing feature-vector wiring to avoid mode-ratchet regressions. Added focused coverage in `encoder/analysis_test.go` (`TestRunAnalysisMaxPitchRatioTracksHighBandEnergy`, `TestRunAnalysisLowEnergyCounterIncreasesAfterLoudnessDrop`). Validation: focused encoder analysis tests, `TestEncoderVariantProfileParityAgainstLibopusFixture`, `TestEncoderComplianceSummary`, `make verify-production`, and `make bench-guard` all passed.
- 2026-02-13: Fixed long-frame analysis residual buffering bug in `encoder/analysis.go`: when downsampled carry-over exceeded the 480-sample residual window, `MemFill` could grow beyond `AnalysisBufSize` and HP-energy carry state no longer matched retained samples. Clamped retained residual to window capacity and scaled `HPEnerAccum` to only the retained fraction. Validation: `TestEncoderVariantProfileParityAgainstLibopusFixture`, `TestEncoderComplianceSummary`, `TestEncoderCompliancePrecisionGuard`, `make verify-production`, and `make bench-guard` all passed.
- 2026-02-13: Tightened constrained-VBR policy toward libopus parity: gated custom CELT short/medium frame uplifts to unconstrained VBR only, added an explicit constrained-VBR target-bit cap (+15%) in CELT target computation, and initialized multistream stream encoders with VBR constraint enabled by default. Added regression coverage for single-stream CELT CVBR envelope (`TestBitrateModeCVBR_CELTStereoEnvelope`) and 5.1 multistream packet envelope (`TestMultistreamEncoder_CVBRPacketEnvelope`). Outcome: `TestLibopus_BitrateQuality` moved from severe overshoot/decode failures to near-target bitrates with full decode; full `make verify-production` passed.
- 2026-02-13: Fixed a concrete CVBR framing defect: `constrainSize` no longer pads undersized packets, which previously rewrote SILK packets into code-3 framing and broke TOC/libopus fixture parity. Added/updated control-transition coverage (`SetVBR`/`SetVBRConstraint`) while preserving current default VBR baseline; validated with focused encoder/multistream control tests, `TestSILKParamTraceAgainstLibopus`, `TestEncoderCompliancePrecisionGuard`, and full `make verify-production`.
- 2026-02-13: Closed libopus max-bandwidth CTL validation gap in root wrappers: `Encoder.SetMaxBandwidth` and `MultistreamEncoder.SetMaxBandwidth` now reject invalid bandwidth values with `ErrInvalidBandwidth` instead of silently accepting unknown enums. Added/updated tests in `encoder_test.go`, `multistream_test.go`, and API roundtrip coverage in `api_test.go`; `make verify-production` passed.
- 2026-02-13: Closed a concrete multistream CTL parity gap: `MultistreamEncoder.SetSignal` now mirrors libopus `OPUS_SET_SIGNAL_REQUEST` validation semantics by rejecting invalid signal values with `ErrInvalidSignal` instead of silently accepting arbitrary integers. Added coverage in `TestMultistreamEncoder_Controls` for valid voice/music transitions and invalid-value rejection. Validation: focused root/multistream control tests plus `make verify-production` (includes parity + bench-guard + race) passed.
- 2026-02-13: Compacted planning docs to reduce context load; full history archived in `.planning/archive/ACTIVE_2026-02-13_full.txt`, `.planning/archive/DECISIONS_2026-02-13_full.txt`, and `.planning/archive/WORK_CLAIMS_2026-02-13_full.txt`.
- 2026-02-13: Closed delay-compensation parity gap by gating on low-delay application state instead of forced CELT mode; focused tests + `make verify-production` + `make bench-guard` passed.
- 2026-02-13: Closed multistream application forwarding parity by propagating application policy to all stream encoders without clobbering bitrate/complexity.
- 2026-02-13: Closed lookahead and application-change lock parity in root wrappers to match libopus first-frame and low-delay behavior.
- 2026-02-12 to 2026-02-13: Completed surround trim producer flow, LFE-aware multistream policy, per-stream surround control policy, CTL/API surfaces, repacketizer parity fixture coverage, and ambisonics parity guards.
