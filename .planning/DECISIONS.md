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
owner: codex

date: 2026-02-13
topic: Long-SWB strict analyzer control wiring gate
decision: Keep stable long-SWB auto policy; defer strict voice-ratio wiring until dedicated fixture-backed evidence avoids mode regressions.
evidence: strict wiring attempts regressed `HYBRID-SWB-40ms-*` mode parity; rollback restored passing parity guards.
do_not_repeat_until: new analyzer trace evidence demonstrates non-regressing strict wiring.
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
