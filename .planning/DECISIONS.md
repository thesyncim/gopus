# Investigation Decisions

Last updated: 2026-02-12

Purpose: prevent repeated validation by recording what was tested, what was ruled out, and when re-validation is allowed.

## Entry Template

Use this exact shape:

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
