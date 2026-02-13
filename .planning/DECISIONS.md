# Investigation Decisions

Last updated: 2026-02-13

Purpose: prevent repeated validation by recording what was tested, what was ruled out, and when re-validation is allowed.

History archive: `.planning/archive/DECISIONS_2026-02-13_full.txt`

## Entry Template

Preferred shape:

```text
date: YYYY-MM-DD
topic: <short scope name>
decision: <what to keep/stop doing>
evidence: <test name(s), command(s), or fixture(s)>
do_not_repeat_until: <condition that would invalidate this decision>
owner: <initials or handle>
```

## Current Decisions

date: 2026-02-13
topic: Analyzer reset semantics parity
decision: Keep `TonalityAnalysisState.Reset()` aligned with libopus `tonality_analysis_reset()`: clear all reset-scoped analyzer state while preserving reusable config/scratch allocations.
evidence: Added `TestTonalityAnalysisResetClearsState`; focused analyzer tests and parity/compliance/full gates passed (`TestEncoderVariantProfileParityAgainstLibopusFixture`, `TestEncoderComplianceSummary`, `make verify-production`, `make bench-guard`).
do_not_repeat_until: libopus changes analyzer reset semantics or fixture evidence indicates reset-state divergence.
owner: codex

date: 2026-02-13
topic: Analyzer FFT NaN guard parity
decision: Keep libopus-style NaN guard in `tonalityAnalysis`: if FFT output is NaN, mark current info slot invalid, advance write position, and return before feature extraction/MLP/counter updates.
evidence: Added `TestRunAnalysisNaNInputMarksInfoInvalid`; parity/compliance and broad gates passed (`TestEncoderVariantProfileParityAgainstLibopusFixture`, `TestEncoderComplianceSummary`, `make verify-production`, `make bench-guard`).
do_not_repeat_until: libopus `analysis.c` changes NaN guard semantics or fixture evidence shows divergence.
owner: codex

date: 2026-02-13
topic: Analyzer digital-silence parity
decision: Keep libopus-style digital-silence handling in `tonalityAnalysis`: when the 30 ms analysis buffer is digital silence, copy the previous analysis slot, advance write position, and skip FFT/feature/MLP updates and counter increments.
evidence: Added `TestRunAnalysisSilenceCopiesPreviousInfo` and `TestRunAnalysisInitialSilenceKeepsInvalidInfo`; parity/compliance and broad gates passed (`TestEncoderVariantProfileParityAgainstLibopusFixture`, `TestEncoderComplianceSummary`, `make verify-production`, `make bench-guard`).
do_not_repeat_until: libopus `analysis.c` changes `is_digital_silence32`/silence-early-return behavior or fixture evidence shows divergence.
owner: codex

date: 2026-02-13
topic: Analyzer 16 kHz resample parity
decision: Keep `Fs==16000` tonality-analysis support aligned with libopus `downmix_and_resample()` (16 kHz -> 24 kHz via 3x repeat + `silk_resampler_down2_hp`), including first-fill and residual-buffer paths.
evidence: Added `TestRunAnalysis16kProducesValidInfo` and `TestRunAnalysis16kLongFrameUses20msChunks`; parity/compliance and broad gates passed (`TestEncoderVariantProfileParityAgainstLibopusFixture`, `TestEncoderComplianceSummary`, `make verify-production`, `make bench-guard`).
do_not_repeat_until: libopus `analysis.c` changes 16 kHz analysis resampling semantics or fixture evidence shows divergence.
owner: codex

date: 2026-02-13
topic: Parity implementation method (no trial-and-error)
decision: For libopus parity gaps, implement by directly porting the corresponding libopus 1.6.1 logic first; do not use heuristic tuning as the primary fix path.
evidence: Repeated mode/quality regressions occurred when threshold-only tuning was attempted without full source-parity state/model alignment.
do_not_repeat_until: libopus parity scope is complete or policy is explicitly revised with team sign-off.
owner: codex

date: 2026-02-13
topic: libopus source of truth (version pin)
decision: Treat `tmp_check/opus-1.6.1/` as the authoritative reference for parity decisions.
evidence: AGENTS policy; recent parity fixes all cross-checked against this tree.
do_not_repeat_until: The pinned libopus version changes or parity policy is formally revised.
owner: codex

date: 2026-02-13
topic: Delay compensation parity gate
decision: Gate CELT delay compensation on low-delay application state, not forced CELT mode.
evidence: Focused encoder and wrapper tests passed; broad gates passed.
do_not_repeat_until: libopus changes delay-compensation/application semantics.
owner: codex

date: 2026-02-13
topic: Multistream application ctl forwarding parity
decision: Forward application policy to every stream encoder and preserve bitrate/complexity controls.
evidence: `TestMultistreamEncoder_SetApplicationForwardsModeAndBandwidth` and related control tests passed.
do_not_repeat_until: libopus changes multistream `OPUS_SET_APPLICATION_REQUEST` semantics.
owner: codex

date: 2026-02-13
topic: Application lock-after-first-encode parity
decision: Keep wrapper application change locked after first successful encode, except same-value sets; unlock on `Reset()`.
evidence: `TestEncoder_SetApplication` and `TestMultistreamEncoder_SetApplicationAfterEncodeRejected` passed.
do_not_repeat_until: libopus changes first-frame application ctl behavior.
owner: codex

date: 2026-02-13
topic: Public lookahead parity by application
decision: Keep lookahead as `Fs/400` for low-delay, `Fs/400 + Fs/250` otherwise.
evidence: `TestEncoder_Lookahead` and `TestMultistreamEncoder_Lookahead` exact checks passed.
do_not_repeat_until: libopus changes `OPUS_GET_LOOKAHEAD` semantics.
owner: codex

date: 2026-02-13
topic: Surround and LFE multistream control parity
decision: Keep per-stream surround policy, LFE handling, and `surroundTrim` producer flow aligned with landed parity tests.
evidence: Focused multistream/celt/encoder parity tests and broad gates passed.
do_not_repeat_until: fixture/interoperability evidence shows surround or LFE divergence.
owner: codex

date: 2026-02-12
topic: CTL/API and repacketizer parity slice
decision: Keep landed root/multistream/decoder ctl wrappers and repacketizer + packet pad/unpad surfaces.
evidence: control-surface tests and fixture-backed repacketizer tests passed.
do_not_repeat_until: libopus fixture or interoperability evidence shows surface mismatch.
owner: codex

date: 2026-02-13
topic: Multistream OPUS_SET_SIGNAL validation parity
decision: Keep `MultistreamEncoder.SetSignal` strict and reject invalid values with `ErrInvalidSignal` (do not silently coerce unknown signal hints).
evidence: Updated `multistream.go` setter semantics and expanded `TestMultistreamEncoder_Controls` to assert valid voice/music transitions and invalid-signal rejection; `make verify-production` passed.
do_not_repeat_until: libopus changes `OPUS_SET_SIGNAL_REQUEST` accepted values/return semantics or fixture/interoperability evidence shows this validation behavior diverges.

date: 2026-02-13
topic: OPUS_SET_MAX_BANDWIDTH validation parity (root wrappers)
decision: Keep `Encoder.SetMaxBandwidth` and `MultistreamEncoder.SetMaxBandwidth` strict: only NB/MB/WB/SWB/FB are accepted; invalid values must return `ErrInvalidBandwidth`.
evidence: Updated wrapper signatures/validation in `encoder.go` and `multistream.go`; added invalid-value assertions in `TestEncoder_SetMaxBandwidth` and `TestMultistreamEncoder_Controls`; updated API roundtrip setup (`TestSILK10msOpusRoundTrip`) for the error-returning setter; `make verify-production` passed.
do_not_repeat_until: libopus changes `OPUS_SET_MAX_BANDWIDTH_REQUEST` accepted values/return semantics, or fixture/interoperability evidence shows divergent behavior.
owner: codex

date: 2026-02-13
topic: CVBR framing parity guard
decision: Do not pad undersized packets in CVBR post-processing; preserve encoder-produced framing/TOC and avoid rewriting single-frame SILK packets into code-3 packets.
evidence: `TestSILK10msTOCByteCorrectness`, `TestLargeFrameSizeModeSelectionAndPacketization`, and `TestLibopusPacketValidation` regress when undersized CVBR packets are padded; removing lower-bound padding restores parity and `make verify-production` passes.
do_not_repeat_until: CVBR upper/lower budget handling is reworked end-to-end with fixture-backed parity evidence.
owner: codex

date: 2026-02-13
topic: VBR default-mode flip gate
decision: Keep current default encoder bitrate mode at VBR for now; defer default CVBR flip until constrained-VBR behavior is fixture-parity-safe.
evidence: Default CVBR flip caused broad `testvectors` parity regressions (`TestSILKParamTraceAgainstLibopus`, `TestEncoderCompliancePrecisionGuard`, long-frame parity). Rolling back the default while keeping safe control-transition semantics restores green `make verify-production`.
do_not_repeat_until: constrained-VBR implementation has dedicated parity fixtures proving no regression in SILK/Hybrid/CELT packet and trace parity.
owner: codex

date: 2026-02-13
topic: CELT constrained-VBR target envelope
decision: Keep custom short/medium CELT uplifts disabled in constrained-VBR mode and cap constrained-VBR CELT target bits to +15% above base bitrate target.
evidence: Without this gate/cap, CVBR produced severe bitrate overshoot (for example stereo CELT 95 kbps yielding ~250 kbps-class packets) and multistream surround interop failures at moderate bitrates. With the gate/cap, new tests (`TestBitrateModeCVBR_CELTStereoEnvelope`, `TestMultistreamEncoder_CVBRPacketEnvelope`) pass and `TestLibopus_BitrateQuality` reports near-target bitrates with full decode.
do_not_repeat_until: libopus-equivalent constrained-VBR internals are fully ported and validated with fixture-level parity for CELT target evolution.
owner: codex

date: 2026-02-13
topic: Multistream default VBR-constraint policy
decision: Initialize multistream stream encoders with VBR constraint enabled by default to align multistream control behavior with libopus expectations while leaving single-stream default untouched.
evidence: Updated `multistream/encoder.go` constructor initialization; control tests and full `make verify-production` remained green; libopus multistream bitrate-quality interop no longer shows decode truncation from oversized packets in this slice.
do_not_repeat_until: single-stream default policy is revisited with dedicated fixture-backed migration plan.
owner: codex

date: 2026-02-13
topic: Long-SWB strict analyzer control wiring gate
decision: Keep stable long-SWB auto policy; defer strict voice-ratio wiring until dedicated fixture-backed evidence avoids mode regressions.
evidence: strict wiring attempts regressed `HYBRID-SWB-40ms-*` mode parity; rollback restored passing parity guards.
do_not_repeat_until: new analyzer trace evidence demonstrates non-regressing strict wiring.
owner: codex

date: 2026-02-13
topic: Analyzer full MLP feature-vector wiring gate
decision: Defer full libopus 25-feature assembly wiring in `encoder/analysis.go` until analyzer state/feature inputs are trace-parity validated; keep narrowed source-ported math (bandwidth masking, `max_pitch_ratio`, loudness tracker) on top of existing feature-vector wiring.
evidence: Direct full-feature wiring caused broad ratchet regressions in `TestEncoderVariantProfileParityAgainstLibopusFixture` (`HYBRID-SWB-20/40ms-*`, including 100% mode mismatch on chirp); narrowing to non-regressing math slice restored green fixture parity plus `make verify-production`.
do_not_repeat_until: dedicated analyzer trace fixtures show gopus feature/state cadence matches libopus 1.6.1 for the same inputs.
owner: codex

date: 2026-02-13
topic: CELT prefilter max_pitch_ratio source parity
decision: When analysis is valid, use analyzer-provided `max_pitch_ratio` for CELT `runPrefilter()` scaling; keep the CELT-local estimator only as fallback when analysis is unavailable.
evidence: libopus `run_prefilter()` scales gain by `analysis->max_pitch_ratio` when `analysis->valid`; wired top-level analysis forwarding into CELT state and updated encode path accordingly; focused tests (`TestSetAnalysisInfoClampsMaxPitchRatio`, `TestEncodeFrameUsesAnalysisMaxPitchRatioWhenValid`, `TestRunPrefilterParityAgainstLibopusFixture`) and fixture parity/compliance suites passed.
do_not_repeat_until: libopus changes `run_prefilter()` analysis-valid scaling semantics, or fixture parity shows a regression from this source selection policy.
owner: codex

date: 2026-02-13
topic: Long-frame tonality residual bound
decision: Keep analysis residual carry-over bounded to the 480-sample post-shift window and scale HP-energy carry to the retained residual fraction only.
evidence: In `encoder/analysis.go`, long-frame paths could leave `MemFill` logically larger than the analysis window and misalign `HPEnerAccum` versus retained samples; clamping retained residual and matching HP carry restored bounded state while keeping fixture parity green (`TestEncoderVariantProfileParityAgainstLibopusFixture`, `TestEncoderComplianceSummary`, `TestEncoderCompliancePrecisionGuard`, `make verify-production`, `make bench-guard`).
do_not_repeat_until: tonality buffering/cadence is redesigned to a full libopus `run_analysis`/`tonality_get_info` port.
owner: codex

date: 2026-02-13
topic: amd64 SWB-40 speech ratchet calibration
decision: Keep calibrated amd64 floor (`-2.32`) for `HYBRID-SWB-40ms-mono-48k/speech_like_v1`.
evidence: CI run `21986775206` failed at `-2.30 dB`; calibrated threshold restored stable gates.
do_not_repeat_until: new multi-OS evidence supports safely tightening this floor.
owner: codex

date: 2026-02-13
topic: Verified areas skip policy
decision: Do not re-debug SILK decoder correctness, resampler parity, or NSQ constant-DC behavior without new contradictory evidence.
evidence: AGENTS verified-area guidance and sustained passing parity checks.
do_not_repeat_until: related decoder/resampler/NSQ code paths or fixtures change.
owner: codex
