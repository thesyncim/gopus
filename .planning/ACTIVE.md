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

- 2026-02-12: Initialized active-memory workflow for agent sessions; no codec math changes in this update.
