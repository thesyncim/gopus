# Active Investigation

Last updated: 2026-02-12
Status: active

## Objective

Close the remaining strict encoder quality gap (`Q >= 0`) without parity regressions or hot-path allocation regressions.

## Current Hypothesis

The highest ROI is targeted SILK/Hybrid quality tuning first, validated against pinned libopus fixtures, before broad CELT retuning.

## Next 3 Actions (Targeted)

1. Pick one failing/weak quality profile and lock a single reproducible fixture case.
2. Run only the narrowest parity/compliance tests needed for that profile before code edits.
3. Capture a short evidence note in this file with command, result, and next decision.

## Explicit Skips For This Session

- Skip re-debugging SILK decoder correctness unless decoder-path files are touched.
- Skip re-debugging resampler parity unless resampler-path files are touched.
- Skip re-investigating NSQ constant-DC amplitude behavior unless evidence conflicts with known expected dithering behavior.

## Stop Conditions

- Stop and reassess after 3 failed hypotheses without measurable quality uplift.
- Escalate to broad gate (`make verify-production`) only when a focused change is ready for merge-level validation.

## Evidence Log (Newest First)

- 2026-02-12: Addressed CELT short-frame under-allocation by adding a non-hybrid `frameSize==120` boost of `+128` bits in `celt/encode_frame.go` (`computeTargetBits`). Quality evidence: `go test ./testvectors -run TestEncoderComplianceCELT -count=1 -v` PASS with `FB-2.5ms-mono` improved from `Q=-43.27` (~27.23 dB) to `Q=-30.98` (~33.13 dB), and target stats increased from avg `226` to `372` bits. Guard evidence: `go test ./celt -run TestCeltTargetBits25ms -count=1 -v` PASS, `go test ./encoder -run TestCELTLongFrameVBRBitrateBudget -count=1 -v` PASS, `go test ./testvectors -run 'TestEncoderComplianceSummary|TestEncoderCompliancePrecisionGuard|TestEncoderVariantProfileParityAgainstLibopusFixture' -count=1 -v` PASS, `make verify-production` PASS, `make bench-guard` PASS. Next decision: keep the `+128` 2.5ms boost as current best validated short-frame setting and continue strict-quality closure on SILK/Hybrid.
- 2026-02-12: Investigated PR #28 CI failures on linux/windows (`TestOpusdecCrossvalFixtureCoverage` missing SHA entries). Root cause: `make fixtures-gen-amd64` regenerated libopus amd64 fixtures but not `celt/testdata/opusdec_crossval_fixture_amd64.json`, while `celt` tests select `_amd64` fixtures on `runtime.GOARCH=="amd64"`. Fix: added `GOPUS_OPUSDEC_CROSSVAL_FIXTURE_OUT` support to `tools/gen_opusdec_crossval_fixture.go`, wired generator into `fixtures-gen-amd64`, then regenerated amd64 fixture with `make fixtures-gen-amd64`. Validation: linux/amd64 container run of `go test ./celt -run 'TestOpusdecCrossvalFixtureCoverage|TestOpusdecCrossvalFixtureHonestyAgainstLiveOpusdec' -count=1 -v` PASS; broad gate `make verify-production` PASS. Next decision: keep arch-specific crossval fixture regeneration coupled to amd64 fixture workflow.
- 2026-02-12: Finalized CELT 20ms non-hybrid boost ceiling at `+1216` bits for high-budget frames (`baseBits >= 1024`) and kept reduced-budget subframes at `+256` in `celt/encode_frame.go`. Quality evidence: `go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v` PASS with `CELT-FB-20ms-mono-64k Q=-8.27` and `CELT-FB-20ms-stereo-128k Q=-9.01` (up from prior baseline `Q=-9.91` / `Q=-11.99`; approx +1.64 dB and +2.98 dB SNR). Guardrails: `TestEncoderCompliancePrecisionGuard`, `TestEncoderVariantProfileParityAgainstLibopusFixture`, `TestCELTLongFrameVBRBitrateBudget` all PASS. Broad gate: `make verify-production` PASS (parity, bench-guard, race). Next decision: keep `+1216` ceiling as current best validated setting.
- 2026-02-12: Ran high-budget CELT boost sweep (`+512,+640,+768,+896,+1024,+1152,+1216,+1280`) against libopus-anchored guards. Result: quality continued improving up to `+1280` but `go run ./tools/gen_opusdec_crossval_fixture.go` failed on `stereo_20ms_silence` with libopus `opusdec` open/decode failure; `+1216` is highest tested value that preserves fixture generation and live-opusdec honesty checks. Next decision: do not exceed `+1216` without first fixing silence-packet interoperability.
- 2026-02-12: Regenerated CELT opusdec crossval fixture after intentional CELT packet-budget change: `go run ./tools/gen_opusdec_crossval_fixture.go` (wrote `celt/testdata/opusdec_crossval_fixture.json`). Verified fixture parity with live decoder: `go test ./celt -run 'TestOpusdecCrossvalFixtureCoverage|TestOpusdecCrossvalFixtureHonestyAgainstLiveOpusdec' -count=1 -v` PASS. Broad gate now clean: `make verify-production` PASS (includes parity tests, bench-guard, and race). Next decision: keep fixture update with codec change to maintain deterministic crossval coverage.
- 2026-02-12: Implemented targeted CELT budget uplift in `celt/encode_frame.go` (`computeTargetBits`: keep +128 for 10ms and add +256 for 20ms when non-hybrid). Validation: `go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v` (PASS) with measurable CELT gains (`CELT-FB-20ms-mono-64k: Q -9.91 -> -9.34`, `CELT-FB-20ms-stereo-128k: Q -11.99 -> -10.84`; +0.28 to +0.55 dB SNR). Guardrails still green: `go test ./testvectors -run TestEncoderCompliancePrecisionGuard -count=1 -v` PASS, `go test ./encoder -run 'TestHybridVBRBitrateBudget|TestCELTLongFrameVBRBitrateBudget' -count=1 -v` PASS, `go test ./testvectors -run TestEncoderVariantProfileParityAgainstLibopusFixture -count=1 -v` PASS. Next decision: keep this CELT uplift and re-run merge-level gates before handoff.
- 2026-02-12: Tested long-frame SWB ModeAuto hint retuning against `TestEncoderVariantProfileParityAgainstLibopusFixture` focused cases (`HYBRID-SWB-40ms-*`, `HYBRID-FB-60ms-*`). Result: unstable mode switching caused worse ratchet metrics in SWB speech/am_multisine; hypothesis rejected and encoder-mode heuristic edits were reverted. Next decision: avoid SWB ModeAuto heuristic churn in this session and prioritize deterministic CELT quality uplift.
- 2026-02-12: Ran narrow speech-mode slices for rapid repro: `go test ./testvectors -run 'TestEncoderComplianceSILK/WB-20ms-mono' -count=1 -v` and `go test ./testvectors -run 'TestEncoderComplianceHybrid/SWB-20ms-mono' -count=1 -v` (both PASS; absolute quality baseline remains ~`Q=-50.7`, near 23.6-23.7 dB SNR). Next decision: inspect encoder-side SILK analysis/gain/noise-shaping parity path for targeted uplift opportunities.
- 2026-02-12: Ran `go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v` (PASS, `19 passed, 0 failed`); parity-gap status remains GOOD/BASE across fixtures, but strict absolute target (`Q >= 0`) still unmet (worst current CELT absolute Q observed: `CELT-FB-20ms-stereo-128k` at `Q=-11.99`). Next decision: use a narrower CELT slice for fast iterations, then implement targeted encoder-side quality tuning.
- 2026-02-12: Initialized active-memory workflow for agent sessions; no codec math changes in this update.
