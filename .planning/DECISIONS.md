# Investigation Decisions

Last updated: 2026-02-12

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

date: 2026-02-12
topic: libopus source-of-truth policy (version pin)
decision: When codec behavior is uncertain or gopus/libopus differ, resolve against `tmp_check/opus-1.6.1/` C source first and align gopus to that version before heuristic tuning.
evidence: Explicitly reinforced in agent guidance (`AGENTS.md`, `CODEX.md`, `CLAUDE.md`) during CELT quality tuning session.
do_not_repeat_until: The pinned libopus version changes or project parity policy is formally revised.
owner: codex

date: 2026-02-12
topic: CELT 2.5ms short-frame bit budget boost
decision: Keep non-hybrid CELT `frameSize==120` target-bit uplift at `+128` in `celt/encode_frame.go` (`computeTargetBits`).
evidence: `TestEncoderComplianceCELT` improved `FB-2.5ms-mono` from `Q=-43.27` to `Q=-30.98` (~+5.9 dB SNR); guardrails remained green: `TestCeltTargetBits25ms`, `TestCELTLongFrameVBRBitrateBudget`, `TestEncoderComplianceSummary`, `TestEncoderCompliancePrecisionGuard`, `TestEncoderVariantProfileParityAgainstLibopusFixture`, `make verify-production`, and `make bench-guard`.
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
