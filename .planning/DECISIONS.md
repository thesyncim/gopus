# Investigation Decisions

Last updated: 2026-02-16

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

date: 2026-02-16
topic: Multistream packet pad/unpad self-delimited parity
decision: Keep `MultistreamPacketPad` and `MultistreamPacketUnpad` aligned with libopus multistream packet semantics by parsing/re-emitting self-delimited subpackets for streams `0..N-2` and standard framing for the last stream; do not use legacy raw per-stream length-prefix parsing in these APIs.
evidence: Updated `packet.go` multistream pad/unpad paths with self-delimited packet parse/rebuild helpers (`parseSelfDelimitedPacket`, `decodeSelfDelimitedPacket`, `makeSelfDelimitedPacket`), added regression tests in `packet_multistream_padding_test.go` for 2-stream/3-stream round-trips and malformed self-delimited rejection, and validated with focused root + multistream/libopus slices and full `make verify-production`.
do_not_repeat_until: libopus changes multistream packet pad/unpad or self-delimited parsing semantics (`repacketizer.c` / `opus_multistream_*`), or fixture/interoperability evidence shows this behavior drifts.
owner: codex

date: 2026-02-16
topic: Multistream RFC 6716 self-delimited framing parity
decision: Keep multistream packet assembly/parsing on exact RFC 6716 Appendix B semantics: streams `0..N-2` must be emitted as self-delimited Opus packets (no external per-stream length prefix), last stream remains standard framing. Keep decoder-side parsing aligned by consuming self-delimited packets and normalizing to standard elementary packets before stream decode.
evidence: Added framing parser/builder in `multistream/framing.go`; updated assembly in `multistream/encoder.go`; updated packet splitting in `multistream/stream.go`; updated multistream framing tests in `multistream/encoder_test.go` and `multistream/multistream_test.go`; tightened libopus harness in `multistream/libopus_test.go` to fail on textual `opusdec` decode errors and fixed WAV `data` chunk boundary scan. Validation passed with `go test ./multistream -run 'TestLibopus_(Stereo|51Surround|71Surround|BitrateQuality|ContainerFormat|Info)' -count=1 -v`, `go test ./multistream -count=1`, `go test . -run 'TestMultistream' -count=1 -v`, and full `make verify-production`.
do_not_repeat_until: libopus changes multistream self-delimited packet semantics (`opus_multistream_encoder.c`, `opus_multistream_decoder.c`, `repacketizer.c`) or fixture/interoperability evidence shows drift.
owner: codex

date: 2026-02-14
topic: SILK/Hybrid->CELT transition-delay parity (`to_celt`)
decision: Keep libopus `to_celt` transition-delay behavior in `encoder/encoder.go`: when switching from non-CELT to CELT at frame sizes `>=10 ms`, encode one packet in the previous non-CELT mode, but advance next-frame previous-mode state to CELT so subsequent mode decisions transition on the same cadence as libopus.
evidence: Added `prevMode` state and `applyCELTTransitionDelay()` in `encoder/encoder.go`; added focused tests `TestApplyCELTTransitionDelayPolicy` and `TestForcedHybridToCELTTransitionHoldsOneFrame` in `encoder/mode_transition_policy_test.go`; validated with `go test ./encoder -run 'TestApplyCELTTransitionDelayPolicy|TestForcedHybridToCELTTransitionHoldsOneFrame|TestModeTraceFixtureParityWithLibopus' -count=1 -v` and `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderVariantProfileParityAgainstLibopusFixture -count=1 -v`, where prior one-frame drifts in `HYBRID-SWB-20ms-mono-48k/am_multisine_v1` and `HYBRID-SWB-40ms-mono-48k/speech_like_v1` dropped to `mismatch=0.00%`.
do_not_repeat_until: libopus mode-transition/redundancy semantics around `to_celt` change in `opus_encoder.c`, or fixture/interoperability evidence shows this one-frame hold cadence diverges.
owner: codex

date: 2026-02-14
topic: Variants restricted-celt application parity
decision: Keep CELT rows in `TestEncoderVariantProfileParityAgainstLibopusFixture` configured as restricted-celt semantics (`SetMode(ModeCELT)` + `SetLowDelay(true)`), while keeping HYBRID rows mapped to `ModeAuto` (`opus_demo -e audio` parity). Do not compare CELT fixture rows with default audio-delay compensation enabled.
evidence: Reproduced prior CELT chirp/impulse prefilter trace drift and verified that low-delay parity collapses symbol mismatch to 0 in focused trace tests; updated `testvectors/encoder_compliance_variants_fixture_test.go` to set low-delay for CELT rows; refreshed `testvectors/testdata/encoder_compliance_variants_ratchet_baseline.json`; parity slice, `make verify-production`, and `make bench-guard` passed.
do_not_repeat_until: fixture generation mode changes away from `opus_demo -e restricted-celt` for CELT rows, or libopus changes restricted-celt delay-compensation semantics.
owner: codex

date: 2026-02-14
topic: decode_fec frame-size transition granularity
decision: Keep provided-packet FEC recovery in `DecodeWithFEC` keyed to the provided packet TOC frame size (with fallback only when TOC frame size is unavailable), not `lastFrameSize`, so frame-size downshifts do not return oversized PLC-only output.
evidence: Updated `decoder.go` provided-packet FEC path and added `TestDecodeWithFEC_FrameSizeTransitionUsesProvidedPacketGranularity` in `decoder_test.go`; validated with focused root FEC tests plus decoder loss parity and stress suites (`GOPUS_TEST_TIER=parity go test ./testvectors -run TestDecoderLossParityLibopusFixture -count=1 -v`, `GOPUS_TEST_TIER=exhaustive go test ./testvectors -run TestDecoderLossStressPatternsAgainstOpusDemo -count=1 -v`).
do_not_repeat_until: libopus decode_fec frame-size semantics for provided packets change or fixture/interoperability evidence shows this packet-granularity policy regresses.
owner: codex

date: 2026-02-14
topic: Decoder loss stress-pattern parity guard
decision: Keep additional deterministic loss-mask coverage in `TestDecoderLossStressPatternsAgainstOpusDemo` (`burst3_mid`, `periodic5`, `edge_then_mid`, `doublet_stride7`) with live `opus_demo` reference decode and dedicated stress thresholds by codec family.
evidence: Added stress-pattern generator and exhaustive-tier parity test in `testvectors/decoder_loss_parity_test.go`; validated with `GOPUS_TEST_TIER=exhaustive go test ./testvectors -run 'TestDecoderLossStressPatternsAgainstOpusDemo|TestDecoderLossFixtureHonestyWithOpusDemo' -count=1 -v` and `GOPUS_TEST_TIER=parity go test ./testvectors -run TestDecoderLossParityLibopusFixture -count=1 -v`.
do_not_repeat_until: loss fixture corpus/pattern policy changes, libopus `opus_demo` loss decode semantics change, or stress-pattern parity regressions are observed.
owner: codex

date: 2026-02-14
topic: decode_fec single-frame output sizing parity
decision: Keep `decodeFECFrame` output sizing/limits based on a single recovered frame (`frameSize`) instead of packet frame-count (`frameSize * frameCount`) so multi-frame packet metadata does not force spurious PLC fallback from buffer checks.
evidence: Updated `decoder.go` `decodeFECFrame` required-sample and packet-size checks; added `TestDecodeFECFrame_BufferSizingUsesSingleFrame` in `decoder_test.go`; validated with focused root FEC tests (`TestDecodeFECFrame_BufferSizingUsesSingleFrame|TestDecodeWithFEC_UsesProvidedPacketAndPreservesNormalDecode|TestDecodeWithFEC_ProvidedCELTPacketFallsBackToPLC|TestDecodeWithFEC_NoFECRequested`) plus parity guard `GOPUS_TEST_TIER=parity go test ./testvectors -run TestDecoderLossParityLibopusFixture -count=1 -v`.
do_not_repeat_until: libopus changes `opus_decode(..., decode_fec=1)` recovered-frame sizing semantics or fixture/interoperability evidence shows `decodeFECFrame` output sizing drift.
owner: codex

date: 2026-02-14
topic: Decoder loss/FEC fixture workflow + decode_fec semantics parity
decision: Keep `DecodeWithFEC` honoring provided packet data when `fec=true` (libopus-style decode_fec path from packet N+1 with PLC fallback), and keep the dedicated libopus loss fixture workflow (`tools/gen_libopus_decoder_loss_fixture.go`, `testvectors/testdata/libopus_decoder_loss_fixture*.json`) with parity ratchet guards plus fixture honesty checks.
evidence: Updated `decoder.go` FEC path, added focused API tests (`TestDecodeWithFEC_UsesProvidedPacketAndPreservesNormalDecode`, `TestDecodeWithFEC_ProvidedCELTPacketFallsBackToPLC`), added loss fixture loader/parity/honesty tests (`testvectors/libopus_decoder_loss_fixture_test.go`, `testvectors/decoder_loss_parity_test.go`), wired governance + Makefile fixture targets; focused parity/exhaustive tests and full `make verify-production` passed.
do_not_repeat_until: libopus decode_fec/loss recovery semantics in `opus_demo.c`/decoder API change, fixture generator inputs/patterns change, or loss parity ratchet/honesty tests report regression.
owner: codex

date: 2026-02-14
topic: Frame-level mode-trace parity guard and short-frame auto-mode control
decision: Keep the libopus 1.6.1 frame-level mode-trace fixture workflow (`tmp_check/gen_libopus_mode_trace_fixture.go` + `encoder/testdata/libopus_mode_trace_fixture.json`) and the short-frame auto-mode port in `encoder/encoder.go` (libopus threshold/hysteresis with analysis-driven `voice_est`, previous-mode state, VoIP threshold bias, and FEC/DTX SILK forcing conditions).
evidence: Added `encoder/mode_trace_fixture_test.go` parity/metadata guards over 32 fixture cases; mode drift collapsed from large WB/SWB mismatches to <=2% max per case; focused mode/FEC tests, parity/compliance slice, `make verify-production`, and `make bench-guard` passed.
do_not_repeat_until: libopus mode-selection semantics change in `opus_encoder.c` (thresholds/hysteresis/voice_est/FEC forcing/application bias) or the mode-trace fixture reports >2% drift on any covered case.
owner: codex

date: 2026-02-14
topic: CELT constrained-VBR reservoir parity
decision: Keep CELT constrained-VBR budgeting on direct libopus state cadence (`vbr_reservoir`, `vbr_offset`, `vbr_drift`, `vbr_count`) and remove custom guardrails (`+15%` hard cap and frame-size bitrate uplifts) from `computeTargetBits`. For multistream CVBR only, keep bounded `vbr_bound` scaling to respect the Opus 1275-byte aggregate packet cap while preserving single-stream libopus behavior at scale `1.0`.
evidence: Updated `celt/encode_frame.go`/`celt/encoder.go`, added CELT bound-scale propagation in `encoder/encoder.go` and `multistream/encoder.go`, and updated CVBR envelope coverage in `encoder/encoder_test.go`; regenerated `celt/testdata/opusdec_crossval_fixture.json`; focused CVBR/crossval tests, parity/compliance slice, `make verify-production`, and `make bench-guard` all passed.
do_not_repeat_until: libopus changes constrained-VBR reservoir/offset cadence in `celt_encoder.c`, or fixture/interoperability evidence shows renewed constrained-VBR target divergence.
owner: codex

date: 2026-02-14
topic: ModeAuto analyzer-invalid fallback parity
decision: Keep `autoSignalFromPCM()` fallback aligned to libopus by returning `SignalAuto` when analysis is unavailable/invalid (outside SWB 10/20 ms threshold lanes), and do not reintroduce PCM classifier/energy-ratio voice/music forcing in this path.
evidence: Updated `encoder/encoder.go` fallback path and added `TestAutoSignalFromPCMAnalyzerInvalidFallsBackToAuto` plus `TestAutoSignalFromPCMAnalyzerUnavailableFallsBackToAuto` in `encoder/auto_mode_policy_test.go`; focused auto-mode tests, parity/compliance slice, `make verify-production`, and `make bench-guard` passed.
do_not_repeat_until: libopus changes auto-mode fallback semantics around `voice_ratio`/analysis validity in `opus_encoder.c`, or fixture/interoperability evidence shows renewed mode divergence when analysis is invalid.
owner: codex

date: 2026-02-14
topic: Analyzer trace fixture full profile matrix
decision: Keep analyzer trace fixtures aligned to the complete active encoder parity profile set (19 lanes), not a SWB-only subset. Maintain generator coverage in `tmp_check/gen_libopus_analysis_trace_fixture.go` for CELT/HYBRID/SILK mono+stereo profiles and long-frame lanes, and enforce with `TestAnalysisTraceFixtureProfileCoverage`.
evidence: Regenerated `encoder/testdata/libopus_analysis_trace_fixture.json` to 76 cases (19 profiles x 4 variants), and verified no profile coverage gaps against the parity fixture matrix; `TestAnalysisTraceFixtureParityWithLibopus` reported 0 bad frames for all cases; parity slice + `make verify-production` + `make bench-guard` passed.
do_not_repeat_until: Parity profile matrix changes (new case lanes added/removed) or libopus analyzer interface/semantics change and require fixture shape updates.
owner: codex

date: 2026-02-14
topic: Analyzer trace fixture coverage matrix (stereo + 60ms)
decision: Keep the expanded libopus analyzer trace fixture matrix in `tmp_check/gen_libopus_analysis_trace_fixture.go` and `encoder/testdata/libopus_analysis_trace_fixture.json`, including stereo FB profiles and 60 ms mono FB coverage, so analyzer/control parity remains source-backed beyond SWB mono.
evidence: Generator now emits 36 cases across SWB mono, FB mono/stereo, and 60 ms lanes; `TestAnalysisTraceFixtureParityWithLibopus` reported `badFrames=0` on all cases; parity/compliance slice and full gates (`make verify-production`, `make bench-guard`) passed after regeneration.
do_not_repeat_until: Active parity profile matrix changes (new mode/bandwidth/frame-size/channel lanes) or libopus `run_analysis` semantics change and require updating trace coverage.
owner: codex

date: 2026-02-14
topic: Multi-frame SILK per-frame VAD state cadence parity
decision: Keep per-20ms VAD state snapshots (speech activity, input tilt, quality bands) applied before each SILK subframe encode in 40/60ms packets; do not reuse the last-frame VAD state across the whole packet.
evidence: Ported packet control flow in `encoder/encoder.go`, `silk/encode_frame.go`, and `silk/silk_encode.go` to apply frame-local VAD state before each `EncodeFrame` call; added `TestEncodePacketWithFECWithVADStatesUsesPerFrameState`; parity/provenance suites passed and long SILK impulse-heavy negatives dropped from provenance worst-list.
do_not_repeat_until: libopus changes `silk_encode_do_VAD_Fxx`/`enc_API.c` per-frame VAD cadence semantics, or fixture/interoperability evidence shows this per-frame state application diverges.
owner: codex

date: 2026-02-14
topic: Ratchet baseline refresh for SILK long-packet packet-length profile after source parity port
decision: Keep updated ratchet limits for affected SILK NB/WB long-packet variants (`SILK-NB-40ms-*`, `SILK-WB-40ms-*`, `SILK-WB-60ms-*`, `SILK-WB-20ms-stereo/chirp`) to reflect source-backed per-frame VAD cadence, while preserving mode-mismatch/histogram guards.
evidence: Updated `testvectors/testdata/encoder_compliance_variants_ratchet_baseline.json`; `GOPUS_TEST_TIER=parity go test ./testvectors -run 'TestEncoderVariantProfileParityAgainstLibopusFixture|TestEncoderComplianceSummary|TestEncoderCompliancePrecisionGuard' -count=1` and provenance audit passed.
do_not_repeat_until: the SILK packet VAD control path changes again, or fixture-level evidence warrants re-tightening these specific packet-length thresholds.
owner: codex

date: 2026-02-13
topic: SILK maxBits payload budget parity
decision: Keep SILK max-bit budgeting aligned to libopus by reserving the Opus TOC byte from SILK payload budget (`(maxPacketBytes-1)*8`) and apply this in SILK encode paths instead of pre-setting from whole-packet bitrate bits in `Encode()`.
evidence: Added `silkPayloadMaxBits()` and wired it in `encoder/encoder.go` SILK mono/stereo max-bits setup; added `TestSILKMaxBitsReservesTOCByte`; focused encoder controls/SILK tests, variant/compliance parity suite, and `make verify-production` all passed. Provenance evidence improved `SILK-MB-20ms-mono-24k/am_multisine_v1` gap from ~`-0.68dB` to ~`-0.09dB`.
do_not_repeat_until: libopus changes SILK payload bit-budget semantics in `opus_encoder.c`/SILK control flow, or fixture/interoperability evidence shows this TOC-reserved budgeting diverges.
owner: codex

date: 2026-02-13
topic: SWB 10 ms auto-mode control parity
decision: Keep SWB 10 ms auto-mode signal/mode hinting on the same libopus threshold policy used for SWB auto decisions (equivalent-rate threshold with analysis-derived voice estimate, prev-mode `music_prob_min/max`, and `-4000/+4000` hysteresis); do not reintroduce the custom transient-score gate.
evidence: Updated `encoder/encoder.go` (`autoSignalFromPCM`, new `selectSWBAutoSignal`); removed `swb10TransientScore`; added `TestSelectSWBAutoSignal10msHysteresis` and `TestAutoSignalFromPCMSWB10UsesThresholdPolicy`; parity slice now shows `HYBRID-SWB-10ms-mono-48k/chirp_sweep_v1` mismatch `0.00%` with corrected gap and full variant/compliance parity tests pass.
do_not_repeat_until: libopus changes mode-threshold/voice-estimation semantics in `opus_encoder.c`, or fixture/interoperability evidence shows SWB 10 ms divergence under this policy.
owner: codex

date: 2026-02-13
topic: Multistream surround energy-mask control parity
decision: Keep per-stream surround energy-mask wiring active: multistream surround analysis produces per-stream masks (coupled=42, mono=21, LFE cleared), forwards via encoder/celt mask controls, and CELT uses libopus-style mask->surround_dynalloc/surround_trim derivation in dynalloc/alloc-trim control flow.
evidence: Updated `multistream/encoder.go`, `encoder/encoder.go`, `celt/encoder.go`, `celt/encode_frame.go`, `celt/dynalloc.go`; added `TestEncode_SurroundEnergyMaskPerStream`, `TestEncoderSetEnergyMask`, and `TestComputeSurroundDynallocFromMask`; focused package tests and parity fixture slice passed.
do_not_repeat_until: libopus surround masking semantics change in `opus_multistream_encoder.c`/`celt_encoder.c`, or fixture/interoperability evidence indicates divergence.
owner: codex

date: 2026-02-13
topic: Analyzer trace fixture + full 25-feature wiring parity
decision: Keep full libopus 25-feature analyzer assembly enabled (`midE`, `spec_variability`, `cmean/mem/std` cadence, feature slot mapping) and guard it with fixture-backed `AnalysisInfo` parity tests generated from libopus 1.6.1 `run_analysis`/`tonality_get_info`.
evidence: Added `tmp_check/gen_libopus_analysis_trace_fixture.go` (build-ignore), `encoder/testdata/libopus_analysis_trace_fixture.json`, and `encoder/analysis_trace_fixture_test.go`; `TestAnalysisTraceFixtureParityWithLibopus` now reports 0 bad frames across all SWB 10/20/40 ms fixture cases. Focused encoder tests, variant/compliance slices, and `make verify-production` passed.
do_not_repeat_until: libopus changes analyzer feature extraction/MLP input semantics in `analysis.c`, or fixture evidence shows renewed analyzer divergence.
owner: codex

date: 2026-02-13
topic: SWB auto-mode threshold control parity (20/40 ms)
decision: Use libopus mode-threshold policy directly for SWB auto control: previous-mode `music_prob` min/max selection, voice-ratio conversion (`*327>>8`), audio clamp to 115, and `-4000/+4000` hysteresis; remove custom tonality/ratio hold heuristics from SWB 20 ms control path.
evidence: Updated `encoder/encoder.go` (`selectLongSWBAutoMode`, SWB20 path in `autoSignalFromPCM`), restored fixture parity in `TestEncoderVariantProfileParityAgainstLibopusFixture` while retaining analyzer parity and full production gates.
do_not_repeat_until: libopus changes SWB auto mode-threshold logic in `opus_encoder.c`, or fixture/interoperability evidence indicates this control policy diverges.
owner: codex

date: 2026-02-13
topic: Analyzer phase-angle math parity (`fast_atan2f` + `float2int`)
decision: Keep analyzer phase-angle extraction and phase-delta wrapping aligned with libopus `analysis.c` (`fast_atan2f` approximation and `float2int` ties-to-even wrapping), replacing generic `atan2`/`Round` behavior.
evidence: Updated `encoder/analysis.go` math path; added `TestAnalysisFloat2IntRoundToEven` and `TestAnalysisFastAtan2fParityShape`; focused encoder tests, fixture parity/compliance slices, `make verify-production`, and `make bench-guard` all passed.
do_not_repeat_until: libopus changes analyzer phase math in `analysis.c`/`mathops.h`, or fixture evidence shows divergence from this path.
owner: codex

date: 2026-02-13
topic: Analyzer LSB-depth noise-floor parity
decision: Keep analyzer noise-floor computation tied to configured `LSBDepth` (libopus-style scaling by `max(0, lsb_depth-8)`) and propagate encoder `SetLSBDepth()` into analyzer state; preserve analyzer LSB depth across reset.
evidence: Added `TestTonalityAnalysisResetPreservesLSBDepth`, `TestRunAnalysisNoiseFloorRespectsLSBDepth`, and `TestEncoderSetLSBDepthPropagatesToAnalyzer`; parity/compliance/full gates passed (`TestEncoderVariantProfileParityAgainstLibopusFixture`, `TestEncoderComplianceSummary`, `make verify-production`, `make bench-guard`).
do_not_repeat_until: libopus changes analyzer noise-floor/lsb-depth semantics or fixture evidence shows divergence.
owner: codex

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
