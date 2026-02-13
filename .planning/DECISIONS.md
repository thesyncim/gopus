# Investigation Decisions

Last updated: 2026-02-13

Purpose: prevent repeated validation by recording what was tested, what was ruled out, and when re-validation is allowed.

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
topic: CELT 5ms budget uplift (strict-quality continuation, round 10)
decision: Keep non-hybrid/non-LFE CELT `frameSize==240` target-bit uplift at `+128` in `celt/encode_frame.go` (`computeTargetBits`) while keeping other short/medium-frame uplifts unchanged (`2.5ms=+320`, `10ms mono=+256`, `10ms stereo=+832`).
evidence: Focused `go test ./testvectors -run TestEncoderComplianceCELT -count=1 -v` improved `FB-5ms-mono` from `Q=-14.05` to `Q=-12.50` with average target bits `548 -> 634`, while adjacent CELT profiles were unchanged in the same run (`FB-2.5ms-mono=-10.21`, `FB-10ms-mono=-11.14`, `FB-10ms-stereo=-16.19`, `FB-20ms-stereo=-13.60`). Regression guards stayed clean: `TestEncoderComplianceSummary`, `TestEncoderCompliancePrecisionGuard`, `TestEncoderVariantProfileParityAgainstLibopusFixture`, `TestOpusdecCrossvalFixtureCoverage`, `TestOpusdecCrossvalFixtureHonestyAgainstLiveOpusdec`, `make verify-production`, and `make bench-guard` all PASS.
do_not_repeat_until: CELT 5ms bitrate/interoperability regressions appear, or fixture-backed parity evidence indicates `+128` is over-aggressive.
owner: codex

date: 2026-02-13
topic: CELT short-frame budget pivot (strict-quality continuation, round 9)
decision: Keep non-hybrid/non-LFE CELT `frameSize==120` uplift at `+320` in `celt/encode_frame.go` (`computeTargetBits`) and hold 10ms stereo uplift at `+832` (do not raise to `+896`).
evidence: Rejected candidate `frameSize==480` stereo `+896` after focused regression in `go test ./testvectors -run 'TestEncoderComplianceCELT/FB-10ms-stereo' -count=1 -v` (`Q=-16.19 -> -16.47`). Accepted `frameSize==120` uplift `+256 -> +320` after focused improvement in `go test ./testvectors -run 'TestEncoderComplianceCELT/FB-2.5ms-mono' -count=1 -v` (target bits avg `500 -> 564`, `Q=-19.59` / `38.60 dB` -> `Q=-10.21` / `43.10 dB`). Regression guards stayed clean: `TestEncoderComplianceCELT`, `TestEncoderComplianceSummary`, `TestEncoderCompliancePrecisionGuard`, `TestEncoderVariantProfileParityAgainstLibopusFixture`, `TestOpusdecCrossvalFixtureCoverage`, `TestOpusdecCrossvalFixtureHonestyAgainstLiveOpusdec`, `make verify-production`, and `make bench-guard` all PASS.
do_not_repeat_until: CELT 2.5ms bitrate/interoperability regressions appear, or fixture-backed parity evidence indicates `+320` is over-aggressive.
owner: codex

date: 2026-02-13
topic: CELT 10ms stereo budget uplift (strict-quality continuation, round 8)
decision: Keep non-hybrid/non-LFE CELT `frameSize==480` stereo target-bit uplift at `+832` in `celt/encode_frame.go` (`computeTargetBits`), with mono held at `+256` and all other frame-size/LFE paths unchanged.
evidence: Focused slice `go test ./testvectors -run 'TestEncoderComplianceCELT/FB-10ms-stereo' -count=1 -v` improved from `Q=-17.04` to `Q=-16.19` with average target bits `1838 -> 1902`. Regressions remained clean: `TestEncoderComplianceCELT`, `TestEncoderComplianceSummary`, `TestEncoderCompliancePrecisionGuard`, `TestEncoderVariantProfileParityAgainstLibopusFixture`, `TestOpusdecCrossvalFixtureCoverage`, `TestOpusdecCrossvalFixtureHonestyAgainstLiveOpusdec`, `make verify-production`, and `make bench-guard` all PASS.
do_not_repeat_until: CELT 10ms stereo bitrate/interoperability regressions appear, or fixture-backed parity evidence indicates this uplift is over-aggressive.
owner: codex

date: 2026-02-13
topic: CELT 10ms stereo budget uplift (strict-quality continuation, round 7)
decision: Keep non-hybrid/non-LFE CELT `frameSize==480` stereo target-bit uplift at `+768` in `celt/encode_frame.go` (`computeTargetBits`), with mono held at `+256` and all other frame-size/LFE paths unchanged.
evidence: Focused slice `go test ./testvectors -run 'TestEncoderComplianceCELT/FB-10ms-stereo' -count=1 -v` improved from `Q=-17.49` to `Q=-17.04` with average target bits `1774 -> 1838`. Regressions remained clean: `TestEncoderComplianceCELT`, `TestEncoderComplianceSummary`, `TestEncoderCompliancePrecisionGuard`, `TestEncoderVariantProfileParityAgainstLibopusFixture`, `TestOpusdecCrossvalFixtureCoverage`, `TestOpusdecCrossvalFixtureHonestyAgainstLiveOpusdec`, `make verify-production`, and `make bench-guard` all PASS.
do_not_repeat_until: CELT 10ms stereo bitrate/interoperability regressions appear, or fixture-backed parity evidence indicates this uplift is over-aggressive.
owner: codex

date: 2026-02-13
topic: CELT 10ms stereo budget uplift (strict-quality continuation, round 6)
decision: Keep non-hybrid/non-LFE CELT `frameSize==480` stereo target-bit uplift at `+704` in `celt/encode_frame.go` (`computeTargetBits`), with mono held at `+256` and all other frame-size/LFE paths unchanged.
evidence: Focused slice `go test ./testvectors -run 'TestEncoderComplianceCELT/FB-10ms-stereo' -count=1 -v` improved from `Q=-18.40` to `Q=-17.49` with average target bits `1710 -> 1774`. Regressions remained clean: `TestEncoderComplianceCELT`, `TestEncoderComplianceSummary`, `TestEncoderCompliancePrecisionGuard`, `TestEncoderVariantProfileParityAgainstLibopusFixture`, `TestOpusdecCrossvalFixtureCoverage`, `TestOpusdecCrossvalFixtureHonestyAgainstLiveOpusdec`, `make verify-production`, and `make bench-guard` all PASS.
do_not_repeat_until: CELT 10ms stereo bitrate/interoperability regressions appear, or fixture-backed parity evidence indicates this uplift is over-aggressive.
owner: codex

date: 2026-02-13
topic: CELT 10ms stereo budget uplift (strict-quality continuation, round 5)
decision: Keep non-hybrid/non-LFE CELT `frameSize==480` stereo target-bit uplift at `+640` in `celt/encode_frame.go` (`computeTargetBits`), with mono held at `+256` and all other frame-size/LFE paths unchanged.
evidence: Focused slice `go test ./testvectors -run 'TestEncoderComplianceCELT/FB-10ms-stereo' -count=1 -v` improved from `Q=-18.75` to `Q=-18.40` with average target bits `1646 -> 1710`. Regressions remained clean: `TestEncoderComplianceCELT`, `TestEncoderComplianceSummary`, `TestEncoderCompliancePrecisionGuard`, `TestEncoderVariantProfileParityAgainstLibopusFixture`, `TestOpusdecCrossvalFixtureCoverage`, `TestOpusdecCrossvalFixtureHonestyAgainstLiveOpusdec`, `make verify-production`, and `make bench-guard` all PASS.
do_not_repeat_until: CELT 10ms stereo bitrate/interoperability regressions appear, or fixture-backed parity evidence indicates this uplift is over-aggressive.
owner: codex

date: 2026-02-13
topic: CELT 10ms stereo budget uplift (strict-quality continuation, round 4)
decision: Keep non-hybrid/non-LFE CELT `frameSize==480` stereo target-bit uplift at `+576` in `celt/encode_frame.go` (`computeTargetBits`), with mono held at `+256` and all other frame-size/LFE paths unchanged.
evidence: Focused slice `go test ./testvectors -run 'TestEncoderComplianceCELT/FB-10ms-stereo' -count=1 -v` improved from `Q=-19.66` to `Q=-18.75` with average target bits `1582 -> 1646`. Regressions remained clean: `TestEncoderComplianceCELT`, `TestEncoderComplianceSummary`, `TestEncoderCompliancePrecisionGuard`, `TestEncoderVariantProfileParityAgainstLibopusFixture`, `TestOpusdecCrossvalFixtureCoverage`, `TestOpusdecCrossvalFixtureHonestyAgainstLiveOpusdec`, `make verify-production`, and `make bench-guard` all PASS.
do_not_repeat_until: CELT 10ms stereo bitrate/interoperability regressions appear, or fixture-backed parity evidence indicates this uplift is over-aggressive.
owner: codex

date: 2026-02-13
topic: CELT 10ms stereo budget uplift (strict-quality continuation, round 3)
decision: Keep non-hybrid/non-LFE CELT `frameSize==480` stereo target-bit uplift at `+512` in `celt/encode_frame.go` (`computeTargetBits`), with mono held at `+256` and all other frame-size/LFE paths unchanged.
evidence: Focused slice `go test ./testvectors -run 'TestEncoderComplianceCELT/FB-10ms-stereo' -count=1 -v` improved from `Q=-20.54` to `Q=-19.66` with average target bits `1518 -> 1582`. Regressions remained clean: `TestEncoderComplianceCELT`, `TestEncoderComplianceSummary`, `TestEncoderCompliancePrecisionGuard`, `TestEncoderVariantProfileParityAgainstLibopusFixture`, `TestOpusdecCrossvalFixtureCoverage`, `TestOpusdecCrossvalFixtureHonestyAgainstLiveOpusdec`, `make verify-production`, and `make bench-guard` all PASS.
do_not_repeat_until: CELT 10ms stereo bitrate/interoperability regressions appear, or fixture-backed parity evidence indicates this uplift is over-aggressive.
owner: codex

date: 2026-02-13
topic: CELT 10ms stereo budget uplift (strict-quality continuation, round 2)
decision: Keep non-hybrid/non-LFE CELT `frameSize==480` stereo target-bit uplift at `+448` in `celt/encode_frame.go` (`computeTargetBits`), with mono held at `+256` and all other frame-size/LFE paths unchanged.
evidence: Focused slice `go test ./testvectors -run 'TestEncoderComplianceCELT/FB-10ms-stereo' -count=1 -v` improved from `Q=-22.48` to `Q=-20.54` with average target bits `1453 -> 1518`. Regressions remained clean: `TestEncoderComplianceCELT`, `TestEncoderComplianceSummary`, `TestEncoderCompliancePrecisionGuard`, `TestEncoderVariantProfileParityAgainstLibopusFixture`, `TestOpusdecCrossvalFixtureCoverage`, `TestOpusdecCrossvalFixtureHonestyAgainstLiveOpusdec`, `make verify-production`, and `make bench-guard` all PASS.
do_not_repeat_until: CELT 10ms stereo bitrate/interoperability regressions appear, or fixture-backed parity evidence indicates this uplift is over-aggressive.
owner: codex

date: 2026-02-13
topic: CELT 2.5ms mono budget uplift (strict-quality continuation)
decision: Keep non-hybrid/non-LFE CELT `frameSize==120` target-bit uplift at `+256` in `celt/encode_frame.go` (`computeTargetBits`), while keeping the existing 5/10/20ms and LFE handling unchanged.
evidence: Focused slice `go test ./testvectors -run 'TestEncoderComplianceCELT/FB-2.5ms-mono' -count=1 -v` improved from `Q=-25.58` to `Q=-19.59` with average target bits `436 -> 500`. Regressions remained clean: `TestEncoderComplianceCELT`, `TestEncoderComplianceSummary`, `TestEncoderCompliancePrecisionGuard`, `TestEncoderVariantProfileParityAgainstLibopusFixture`, `TestOpusdecCrossvalFixtureCoverage`, `TestOpusdecCrossvalFixtureHonestyAgainstLiveOpusdec`, `make verify-production`, and `make bench-guard` all PASS.
do_not_repeat_until: CELT 2.5ms bitrate/interoperability regressions appear, or fixture-backed parity evidence indicates this uplift is over-aggressive.
owner: codex

date: 2026-02-13
topic: CELT 10ms stereo budget uplift (strict-quality continuation)
decision: Keep non-hybrid/non-LFE CELT `frameSize==480` stereo target-bit uplift at `+384` in `celt/encode_frame.go` (`computeTargetBits`), while keeping mono at `+256` and leaving all other frame-size uplifts unchanged.
evidence: Focused slice `go test ./testvectors -run 'TestEncoderComplianceCELT/FB-10ms-stereo' -count=1 -v` improved from `Q=-25.18` to `Q=-22.48` with average target bits `1325 -> 1453`. Regressions remained clean: `TestEncoderComplianceCELT`, `TestEncoderComplianceSummary`, `TestEncoderCompliancePrecisionGuard`, `TestEncoderVariantProfileParityAgainstLibopusFixture`, `TestOpusdecCrossvalFixtureCoverage`, `TestOpusdecCrossvalFixtureHonestyAgainstLiveOpusdec`, `make verify-production`, and `make bench-guard` all PASS.
do_not_repeat_until: CELT 10ms stereo bitrate/interoperability regressions appear, or fixture-backed parity evidence shows this uplift is over-aggressive.
owner: codex

date: 2026-02-13
topic: CELT 10ms mono budget uplift (post-parity quality slice)
decision: Keep non-hybrid/non-LFE CELT `frameSize==480` mono target-bit uplift at `+256` in `celt/encode_frame.go` (`computeTargetBits`) while leaving stereo and other frame-size uplifts unchanged.
evidence: Focused slice `go test ./testvectors -run 'TestEncoderComplianceCELT/FB-10ms-mono' -count=1 -v` improved from `Q=-11.84` to `Q=-11.14` with average target bits `1229 -> 1358`. Regressions remained clean: `TestEncoderComplianceCELT`, `TestEncoderComplianceSummary`, `TestEncoderCompliancePrecisionGuard`, `TestEncoderVariantProfileParityAgainstLibopusFixture`, `TestOpusdecCrossvalFixtureCoverage`, `TestOpusdecCrossvalFixtureHonestyAgainstLiveOpusdec`, `make verify-production`, and `make bench-guard` all PASS.
do_not_repeat_until: CELT 10ms mono bitrate/interoperability regression appears, or fixture-backed parity evidence indicates this uplift is over-aggressive.
owner: codex

date: 2026-02-13
topic: Surround per-stream control policy parity
decision: Keep multistream per-frame control aligned with libopus: for surround mappings, always apply surround bandwidth policy to each stream but force mode/channel only for coupled streams (`ModeCELT` + `ForceChannels(2)`); do not reset mono/LFE force-channels each frame. For ambisonics mappings, force CELT mode only and preserve caller-configured force-channels.
evidence: Updated `multistream/encoder.go` `applyPerStreamPolicy` to remove mono/ambisonics `ForceChannels(-1)` resets and remove per-frame LFE `ModeCELT`/`ForceChannels(1)`/NB override. Added/updated tests in `multistream/encoder_test.go`: `TestEncode_SurroundPerStreamPolicy`, `TestEncode_SurroundPolicyPreservesMonoForceChannels`, `TestEncode_AmbisonicsForcesCELTMode` (force-channel preservation assertions). Validation: focused multistream policy slice PASS, `go test . -run TestMultistreamEncoder_Controls -count=1` PASS, `make verify-production` PASS, `make bench-guard` PASS.
do_not_repeat_until: libopus fixture/interoperability evidence indicates per-stream mode/channel/bandwidth control divergence in surround or ambisonics mappings.
owner: codex

date: 2026-02-13
topic: LFE-aware multistream parity control propagation
decision: Keep explicit per-stream LFE flag propagation from multistream mapping detection into encoder/CELT state, with LFE enforcing CELT-only narrowband effective behavior. Keep CELT-side LFE gates for TF analysis/alloc-trim and coarse-energy constraints to match libopus LFE handling intent.
evidence: Added `SetLFE`/`LFE` in `encoder/encoder.go` and `celt/encoder.go`, wired `multistream/encoder.go` to mark only the detected LFE stream (`SetLFE(i==lfeStream)`), and applied LFE guards in `celt/encode_frame.go` / `celt/energy_encode.go`. Added tests `TestNewEncoderDefault_SetsLFEFlags`, `TestEncode_SurroundPerStreamPolicy` LFE-flag assertions, `TestLFEEffectiveBandwidthClamp`, `TestLFEModeForcesCELTPath`, `TestEncoderSetLFE`, and `TestComputeTargetBitsLFEAvoidsNonLFEBudgets`. Validation: focused multistream/encoder/celt LFE tests PASS, `go test . -run TestMultistreamEncoder_Controls -count=1` PASS, `make verify-production` PASS, `make bench-guard` PASS.
do_not_repeat_until: libopus fixture/interoperability evidence shows divergence for LFE stream behavior, or remaining surround per-stream policy parity work requires adjusting LFE control semantics.
owner: codex

date: 2026-02-12
topic: Multistream surround-analysis producer parity into surroundTrim flow
decision: Keep the libopus-style surround-analysis producer path in `multistream/encoder.go`: per-channel preemphasis + overlap memory, sample-rate resampling-factor handling, short-overlap MDCT energy analysis, band spreading, and channel-position masking to produce surround band-SMR that feeds per-stream `surroundTrim`.
evidence: Replaced heuristic `channelMaskShape` producer with MDCT/band-energy-based analysis and persisted per-channel analysis memories; updated tests with `TestEncode_SurroundBandSMRProduced` and `TestEncode_SurroundTrimProducedAt24k`, while existing surround policy/trim tests remained green. Validation: focused multistream slice PASS, `make verify-production` PASS, `make bench-guard` PASS.
do_not_repeat_until: libopus-parity evidence indicates analysis/mask math divergence, or remaining LFE/per-stream policy work requires altering this producer flow.
owner: codex

date: 2026-02-12
topic: CTL/API parity closure slice (multistream + decoder gain/pitch)
decision: Keep the added multistream public control wrappers and internal stream propagation for application, bitrate-mode/VBR/CVBR, bandwidth, force-channels, prediction disable, and phase inversion disable. Keep decoder output-gain/pitch CTLs (`SetGain`, `Gain`, `Pitch`) with Q8 dB gain range validation and decode-time gain application across regular decode, PLC, and FEC paths.
evidence: Updated `multistream.go`, `multistream/encoder.go`, `decoder.go`, and `errors.go`; expanded `TestMultistreamEncoder_Controls`; added `TestDecoder_SetGainBounds`, `TestDecoder_GainAppliedToDecodeOutput`, and `TestDecoder_PitchGetter`. Validation: focused CTL tests PASS, focused multistream surround-policy tests PASS, `make verify-production` PASS, `make bench-guard` PASS.
do_not_repeat_until: libopus fixture/interoperability evidence shows semantic mismatch in these surfaces, or remaining surround producer/LFE policy work requires refining these control semantics.
owner: codex

date: 2026-02-12
topic: Ambisonics family-3 parity bounds + behavior guards
decision: Keep family-3 ambisonics validation restricted to libopus projection-supported orders 1..5 (order+1 in [2,6]) and reject order 0 / >5 channel sets for mapping/init. Keep encode-time per-stream ambisonics policy guarded by tests: CELT-only mode, auto force-channels, zero surround trim, and valid multistream packet framing.
evidence: Updated `ValidateAmbisonicsFamily3` in `multistream/ambisonics.go` with order bounds; expanded `multistream/ambisonics_test.go` with `TestAmbisonicsMappingFamily3_UnsupportedOrders`, `TestValidateAmbisonicsFamily3_UnsupportedOrders`, extended valid family-3 mapping/stream-count cases through 5th order (+non-diegetic), and encode coverage (`TestEncoderAmbisonics_Encode`) that now asserts per-stream control policy + parsed packet stream counts. Validation: focused ambisonics slice PASS and `go test ./multistream -count=1` PASS.
do_not_repeat_until: libopus projection family-3 support range changes (new mapping-matrix orders) or packet/control parity evidence indicates a divergence in family-3 behavior.
owner: codex

date: 2026-02-12
topic: Public CTL/API wrapper parity + repacketizer fixture surface
decision: Keep new root-level control wrappers (`encoder.go`, `multistream.go`, `decoder.go`) and packet/repacketizer API surface (`packet.go`) aligned to libopus request/set/get semantics for the implemented subset: bitrate-mode controls, packet-loss controls, explicit bandwidth setters/getters, application setter/getter, decoder bandwidth/duration/DTX getters, repacketizer cat/out/out_range, and packet pad/unpad helpers.
evidence: Added tests `TestEncoder_SetBitrateMode`, `TestEncoder_SetVBRAndConstraint`, `TestEncoder_SetPacketLoss`, `TestEncoder_SetBandwidth`, `TestEncoder_SetApplication`, `TestMultistreamEncoder_Controls` (packet-loss + final-range alias assertion), `TestDecoder_BandwidthAndLastPacketDuration`, `TestDecoder_InDTX`, and fixture-backed `TestRepacketizerParityWithLibopusFixture` using `testdata/repacketizer_libopus_fixture.json` generated from libopus 1.6.1 behavior. Validation: focused tests PASS, `make verify-production` PASS, `make bench-guard` PASS.
do_not_repeat_until: libopus 1.6.1 fixture evidence or interoperability tests show divergence in these added wrapper/repacketizer surfaces, or remaining CTL/API parity work requires signature/semantics refinement.
owner: codex

date: 2026-02-12
topic: Multistream surround/LFE per-stream control parity slice
decision: Keep libopus-style multistream per-frame stream policy in `multistream/encoder.go`: surround-aware per-stream rate allocation (including LFE stream weighting), surround bandwidth forcing thresholds, coupled-stream CELT+stereo forcing, ambisonics CELT forcing, and surround-mask-derived `surroundTrim` propagation via `encoder.SetCELTSurroundTrim`.
evidence: Added `SetCELTSurroundTrim`/`CELTSurroundTrim` in `encoder/encoder.go`; implemented surround/ambisonics mapping inference, LFE stream detection, per-frame allocation/control, and surround-mask trim production in `multistream/encoder.go`; added focused tests `TestAllocateRates_SurroundLFEAware`, `TestEncode_SurroundPerStreamPolicy`, `TestEncode_SurroundTrimProduced`, `TestEncode_AmbisonicsForcesCELTMode` in `multistream/encoder_test.go`. Validation: focused multistream tests PASS, `TestEncoderComplianceSummary` PASS, `make verify-production` PASS, `make bench-guard` PASS.
do_not_repeat_until: libopus fixture-driven parity evidence indicates surround mask/trim producer math should be adjusted, or remaining multistream CTL/repacketizer/ambisonics parity work changes these control surfaces.
owner: codex

date: 2026-02-12
topic: CELT surround-trim plumbing
decision: Keep `surroundTrim` as explicit CELT encoder state used by alloc-trim analysis (`celt/encode_frame.go`) instead of a hardcoded zero, with default reset-to-zero semantics and focused tests. Do not infer non-zero surround trim heuristically until a libopus-parity surround-mask producer is wired.
evidence: Added `SetSurroundTrim`/`SurroundTrim` in `celt/encoder.go`; replaced hardcoded call-site value in `celt/encode_frame.go`; added `TestEncoderSetSurroundTrim`, `TestEncoderResetClearsSurroundTrim`, and `TestAllocTrimSurroundTrimAdjustment`. Validation: focused CELT tests PASS; `TestEncoderComplianceSummary`, `TestEncoderCompliancePrecisionGuard`, `TestEncoderVariantProfileParityAgainstLibopusFixture` PASS; `make verify-production` and `make bench-guard` PASS.
do_not_repeat_until: Surround-mask production/control flow is implemented (or libopus parity evidence requires changing the trim source/units semantics).
owner: codex

date: 2026-02-12
topic: CELT 10ms stereo short-frame budget uplift
decision: Keep a stereo-only 10ms CELT budget uplift in `computeTargetBits` (`frameSize==480`, `channels==2`: additional `+128` bits on top of the existing 10ms boost) to reduce the largest remaining CELT short-frame stereo quality gap without perturbing mono/long-frame behavior.
evidence: Focused compliance slice improved `FB-10ms-stereo` from `Q=-26.80` to `Q=-22.48` (`go test ./testvectors -run 'TestEncoderComplianceCELT/FB-10ms-stereo' -count=1 -v`); broad guards remained green: `TestEncoderComplianceCELT`, `TestEncoderComplianceSummary`, `TestEncoderCompliancePrecisionGuard`, `TestEncoderVariantProfileParityAgainstLibopusFixture`, `TestCELTLongFrameVBRBitrateBudget`, `make verify-production`, and `make bench-guard`.
do_not_repeat_until: CELT 10ms stereo bitrate/interoperability regressions appear, or libopus-referenced parity evidence indicates this uplift is overly aggressive.
owner: codex

date: 2026-02-12
topic: Encoder precision guard ratchet (general, round 2)
decision: Tighten `encoderLibopusGapFloorDB` across stable profiles after short-frame quality uplift (initially 14/19 floors increased), then apply Windows-calibrated adjustments for three newly-sensitive cases while still remaining tighter than the previous baseline: `SILK-WB-10ms-mono-32k=-0.05`, `SILK-WB-60ms-mono-32k=-0.25`, `Hybrid-SWB-10ms-mono-48k=-0.15`. Keep previously held Windows-sensitive floors unchanged: `SILK-WB-40ms-mono-32k=-0.35`, `Hybrid-FB-20ms-mono-64k=-0.55`, `Hybrid-FB-60ms-mono-64k=-0.55`, `Hybrid-FB-20ms-stereo-96k=-0.25`.
evidence: Local ratchet validation `go test ./testvectors -run 'TestEncoderCompliancePrecisionGuard|TestEncoderComplianceSummary|TestEncoderVariantProfileParityAgainstLibopusFixture' -count=1 -v` PASS; PR #31 `test-windows` failure identified these three cases; post-adjustment reruns of the same local tests plus `make verify-production` and `make bench-guard` all PASS.
do_not_repeat_until: Any calibrated floor regresses in CI or fresh multi-OS evidence shows safe headroom to tighten the seven Windows-calibrated/held speech floors further.
owner: codex

date: 2026-02-12
topic: CI Linux gate parallelization (no coverage reduction)
decision: Keep Linux correctness checks split into parallel jobs (`test-linux-parity`, `test-linux-race`, `test-linux-provenance`) and aggregate with `test-linux`; do not re-consolidate parity + race into a single serialized job.
evidence: Recent successful CI telemetry (`gh run view 21964360695`, `gh run view 21964143086`, `gh run view 21963310494`) showed serialized `test-linux-verify` as critical path (~5m51s to ~6m28s), while `perf-linux` benchmark guardrails were already independent. Workflow update preserves full parity/race/provenance checks and removes serialized Linux gating.
do_not_repeat_until: Linux required checks materially change (new required job surface), or post-change CI timing evidence shows no wall-clock benefit over three consecutive successful runs.
owner: codex

date: 2026-02-12
topic: Assembly documentation source of truth
decision: Keep `ASSEMBLY.md` as the canonical inventory for architecture-specific assembly kernels and fallback mappings, and keep `README.md` linked to it instead of duplicating maintenance details elsewhere.
evidence: Added `ASSEMBLY.md`; updated `README.md`, `examples/README.md`, `CODEX.md`, and `CLAUDE.md`; validation gates `make verify-production` and `make bench-guard` passed.
do_not_repeat_until: Any assembly surface changes (`*.s`, `*_asm.go`, build tags, or fallback wiring) or docs structure changes require re-baselining this inventory.
owner: codex

date: 2026-02-12
topic: Encoder precision guard ratchet (general)
decision: Raise `encoderLibopusGapFloorDB` broadly (+0.30 dB across summary profiles), with explicit cross-platform exceptions for four Windows-sensitive speech profiles: `SILK-WB-40ms-mono-32k` (`-0.35`), `Hybrid-FB-20ms-mono-64k` (`-0.55`), `Hybrid-FB-60ms-mono-64k` (`-0.55`), `Hybrid-FB-20ms-stereo-96k` (`-0.25`).
evidence: Initial broad ratchet failed only on Windows CI (`TestEncoderCompliancePrecisionGuard`) for those four cases; after targeted floor adjustment, local precision/parity gates and broad local production gates passed (`make verify-production`, `make bench-guard`).
do_not_repeat_until: New multi-OS evidence indicates these four floors can be tightened further, or any of them regress below current adjusted limits.
owner: codex

date: 2026-02-12
topic: CELT 5ms short-frame bit budget uplift
decision: Keep non-hybrid CELT `frameSize==240` target-bit uplift at `+64` in `celt/encode_frame.go` (`computeTargetBits`).
evidence: `TestEncoderComplianceCELT` improved `FB-5ms-mono` from `Q=-18.10` to `Q=-14.05` (~+1.94 dB SNR); parity/guardrails remained green (`TestEncoderComplianceSummary`, `TestEncoderCompliancePrecisionGuard`, `TestEncoderVariantProfileParityAgainstLibopusFixture`, `TestCELTLongFrameVBRBitrateBudget`, `make verify-production`, `make bench-guard`).
do_not_repeat_until: Short-frame interoperability/bitrate regressions appear or libopus-referenced parity evidence shows this uplift is too aggressive.
owner: codex

date: 2026-02-12
topic: libopus source-of-truth policy (version pin)
decision: When codec behavior is uncertain or gopus/libopus differ, resolve against `tmp_check/opus-1.6.1/` C source first and align gopus to that version before heuristic tuning.
evidence: Explicitly reinforced in agent guidance (`AGENTS.md`, `CODEX.md`, `CLAUDE.md`) during CELT quality tuning session.
do_not_repeat_until: The pinned libopus version changes or project parity policy is formally revised.
owner: codex

date: 2026-02-12
topic: CELT 2.5ms short-frame bit budget boost
decision: Keep non-hybrid CELT `frameSize==120` target-bit uplift at `+192` in `celt/encode_frame.go` (`computeTargetBits`).
evidence: First uplift moved `FB-2.5ms-mono` from `Q=-43.27` to `Q=-30.98`; second uplift to `+192` improved further to `Q=-25.58` (~+8.5 dB total vs original baseline). Guardrails remained green: `TestCeltTargetBits25ms`, `TestCELTLongFrameVBRBitrateBudget`, `TestEncoderComplianceSummary`, `TestEncoderCompliancePrecisionGuard`, `TestEncoderVariantProfileParityAgainstLibopusFixture`, `make verify-production`, and `make bench-guard`.
do_not_repeat_until: Short-frame parity/bitrate/interoperability guards regress or libopus-referenced fixture evidence indicates over-allocation side effects.
owner: codex

date: 2026-02-12
topic: AMD64 opusdec crossval fixture provenance
decision: Always regenerate `celt/testdata/opusdec_crossval_fixture_amd64.json` as part of `make fixtures-gen-amd64` using `GOPUS_OPUSDEC_CROSSVAL_FIXTURE_OUT`; do not ship CELT packet/fixture changes with only the non-amd64 crossval fixture refreshed.
evidence: PR #28 CI failures in linux/windows (`TestOpusdecCrossvalFixtureCoverage`, fixture honesty checks) were caused by missing `_amd64` SHA mappings. After wiring `tools/gen_opusdec_crossval_fixture.go` into `fixtures-gen-amd64` and regenerating, linux/amd64 `go test ./celt -run 'TestOpusdecCrossvalFixtureCoverage|TestOpusdecCrossvalFixtureHonestyAgainstLiveOpusdec' -count=1 -v` and `make verify-production` both passed.
do_not_repeat_until: Crossval tests stop selecting architecture-specific fixture files or generator/output conventions are changed.
owner: codex

date: 2026-02-12
topic: CELT 20ms high-budget boost ceiling
decision: Keep non-hybrid 20ms high-budget boost capped at `+1216` bits (`baseBits >= 1024`) and do not raise to `+1280`+ yet.
evidence: Sweep validated by `TestEncoderCompliancePrecisionGuard`, `TestEncoderVariantProfileParityAgainstLibopusFixture`, `TestCELTLongFrameVBRBitrateBudget`; libopus interoperability check failed at `+1280` (`go run ./tools/gen_opusdec_crossval_fixture.go` failed on `stereo_20ms_silence`), while `+1216` passes fixture generation + `TestOpusdecCrossvalFixtureHonestyAgainstLiveOpusdec` and `make verify-production`.
do_not_repeat_until: Silence-packet interoperability root cause is identified and fixed with explicit libopus `opusdec` validation on stereo silence and broad gate rerun.
owner: codex

date: 2026-02-12
topic: SILK decoder correctness path
decision: Treat SILK decoder correctness as validated; focus quality work on encoder path first.
evidence: TestSILKParamTraceAgainstLibopus PASS with exact canonical WB trace parity.
do_not_repeat_until: Files under `silk/libopus_decoder*.go`, `decoder*.go`, or decoder-side parity fixtures change.
owner: team

date: 2026-02-12
topic: Resampler parity path
decision: Do not re-debug SILK/hybrid downsampling path during encoder quality tuning.
evidence: Project baseline and prior parity checks recorded in AGENTS snapshot.
do_not_repeat_until: Resampler implementation or fixture provenance changes.
owner: team

date: 2026-02-12
topic: NSQ constant-DC amplitude behavior
decision: Treat ~0.576 RMS constant-DC behavior as expected dithering behavior, not a defect.
evidence: Explicitly listed under "Verified Areas (Do Not Re-Debug First)" in AGENTS context.
do_not_repeat_until: New targeted parity evidence shows mismatch against libopus for non-synthetic speech signals.
owner: team

date: 2026-02-12
topic: SWB long-frame ModeAuto heuristic retuning
decision: Do not retune long-frame SWB ModeAuto signal-hint heuristics in this pass; prior tweaks increased mode flapping and degraded ratchet parity on fixture variants.
evidence: Focused runs of `TestEncoderVariantProfileParityAgainstLibopusFixture` SWB-40ms/FB-60ms subsets showed worse gap/mismatch after heuristic edits; reverted.
do_not_repeat_until: New per-frame analyzer trace evidence (music/vad/edge metrics) is captured for the affected fixtures and a bounded hysteresis plan is defined.
owner: codex
