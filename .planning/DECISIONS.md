# Investigation Decisions

Last updated: 2026-03-01

Purpose: record durable keep/skip decisions to avoid re-running solved investigations.

Older decision entries were intentionally pruned on 2026-03-01 to keep this file compact.

## Entry Template

```text
date: YYYY-MM-DD
topic: <short scope name>
decision: <what to keep/stop doing>
evidence: <test name(s), command(s), fixture(s), or CI links>
do_not_repeat_until: <condition that invalidates this decision>
owner: <handle>
```

## Current Decisions

date: 2026-03-01
topic: SILK-WB-20ms am amd64 ratchet floor hardening
decision: Keep `SILK-WB-20ms-mono-32k|am_multisine_v1` amd64 floor at `min_gap_db=-0.05` (tightened from `-0.10`) while keeping the default floor at `-0.03`.
evidence: Repeated subtest probes were stable on arm64 and amd64 at `gap=-0.00 dB` for `TestEncoderVariantProfileParityAgainstLibopusFixture/cases/SILK-WB-20ms-mono-32k-am_multisine_v1`. After tightening, full parity/provenance/compliance checks stayed green on both arches plus `make bench-guard`; `make verify-production` showed only the known local `tmp_check` cgo-disabled blocker.
do_not_repeat_until: fixture corpus, quality scoring semantics, or SILK WB 20ms packetization/control flow changes materially alter this lane distribution.
owner: codex

date: 2026-03-01
topic: SILK WB ratchet hardening (40ms am + 60ms impulse amd64)
decision: Keep tightened floors for `SILK-WB-40ms-mono-32k|am_multisine_v1` at default `min_gap_db=-0.03` and amd64 `min_gap_db=-0.05`, and for `SILK-WB-60ms-mono-32k|impulse_train_v1` amd64 at `min_gap_db=-0.05` while keeping default at `-0.04`.
evidence: Repeated subtest probes were stable: arm64 `SILK-WB-40ms am` at `gap=-0.00 dB`, amd64 `SILK-WB-40ms am` at `gap=0.00 dB`; arm64 `SILK-WB-60ms impulse` at `gap=-0.04 dB` (so default floor kept), amd64 `SILK-WB-60ms impulse` at `gap=-0.00 dB`. After tightening, full `TestEncoderVariantProfileParityAgainstLibopusFixture` (arm64 + amd64), `TestEncoderVariantProfileProvenanceAudit`, `TestEncoderComplianceSummary`, and `make bench-guard` passed; `make verify-production` showed only the known local `tmp_check` cgo-disabled blocker.
do_not_repeat_until: fixture corpus, quality scoring semantics, or SILK WB packetization/control flow changes materially alter these lane distributions.
owner: codex

date: 2026-03-01
topic: Planning doc compaction policy
decision: Keep `.planning/ACTIVE.md`, `.planning/DECISIONS.md`, and `.planning/WORK_CLAIMS.md concise; archive deep history snapshots under `.planning/archive/`.
evidence: On 2026-03-01, live planning files grew to ~345KB total and reduced usability; archived full snapshots and rewrote compact operational summaries.
do_not_repeat_until: planning volume remains low and navigation cost is no longer a concern.
owner: codex

date: 2026-03-01
topic: SILK-WB-60ms am_multisine ratchet floor hardening
decision: Keep `SILK-WB-60ms-mono-32k|am_multisine_v1` floors at default `min_gap_db=-0.03` and amd64 `min_gap_db=-0.05`.
evidence: Focused arm64/amd64 parity probes were stable at `gap=0.00 dB`; full parity/provenance/compliance checks plus CI matrix stayed green (merged PR #261).
do_not_repeat_until: fixture corpus, quality metric semantics, or SILK WB 60ms packetization/control flow changes materially.
owner: codex

date: 2026-03-01
topic: SILK-WB-60ms impulse ratchet floor hardening
decision: Keep `SILK-WB-60ms-mono-32k|impulse_train_v1` floors at default `min_gap_db=-0.04` and amd64 `min_gap_db=-0.08`.
evidence: Repeated focused arm64/amd64 probes were stable; full parity/provenance/compliance checks plus CI matrix stayed green (merged PR #260).
do_not_repeat_until: fixture corpus, quality metric semantics, or SILK WB 60ms packetization/control flow changes materially.
owner: codex

date: 2026-03-01
topic: Final CELT compliance residual override floor
decision: Keep the remaining no-negative override for `CELT-FB-2.5ms-mono-64k` at `0.191 dB`.
evidence: Deterministic residual observed at approximately `-0.190 dB` with stable packet-shape parity; tightened from `0.20` without regression (merged PR #259).
do_not_repeat_until: CELT 2.5ms parity/control-flow changes or compliance quality-measure semantics shift this residual lane.
owner: codex

date: 2026-02-28
topic: Compliance packet-cadence parity
decision: Keep compliance encode cadence aligned to libopus fixture behavior by allowing bounded trailing flush packets.
evidence: Summary improved from failing rows to stable pass status after cadence alignment; follow-on precision/parity guards remained green.
do_not_repeat_until: fixture cadence model (`signal_frames`/`frames`) or compliance harness semantics change.
owner: codex

date: 2026-02-28
topic: Compliance reference-Q decode-path alignment
decision: Keep reference-Q calibration on libopus-only decode path (direct helper first, `opusdec` fallback) for fixture honesty.
evidence: Refreshed reference-Q fixtures and preserved parity/compliance guard behavior after decode-path alignment.
do_not_repeat_until: libopus helper decode protocol or compliance fixture generation semantics change.
owner: codex

date: 2026-02-28
topic: Hybrid held-frame transition redundancy parity
decision: Keep libopus-style to-CELT redundancy on held SILK/Hybrid transition frames (`celt_to_silk=0` path).
evidence: Source-port closed previously negative hybrid residual lane while parity/provenance/compliance suites stayed green.
do_not_repeat_until: transition-policy semantics or redundancy signaling model changes in encoder hybrid flow.
owner: codex

date: 2026-02-13
topic: Verified areas skip policy
decision: Do not re-debug SILK decoder correctness, resampler parity path, or NSQ constant-DC behavior without new contradictory fixture evidence.
evidence: Sustained passing parity checks plus explicit AGENTS verified-area guidance.
do_not_repeat_until: related code paths or fixtures change.
owner: codex
