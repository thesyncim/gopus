## Status

- Current state: waiting for the first evaluated attempt
- Latest attempt: none yet
- Why it ended that way: no judged result yet
- Branch state: claim only
- Next action: run init, preflight, and the first judged attempt

## Claim

- Lane:
- Editable surface:
- Owner:
- Hypothesis:
- Blocked by:
- Queue dependency:

## Overlap Check

- [ ] I checked the active draft PR queue for the same lane and editable surface.
- [ ] No other editable PR currently owns this pair, or this PR is read-only scout work.
- [ ] This draft PR was opened before the first editable code change on this branch.
- [ ] This slice stays within one editable surface plus, if needed, one narrow supporting helper.

## Recent Attempts

| Outcome | Commit | Tried | Why |
| --- | --- | --- | --- |
| pending | - | none yet | waiting for the first judged attempt |

## Evidence

- Primary judge or target:
- Best current result:
- Latest result:
- Risk and rollback notes:

## Merge Readiness

- [ ] Rebasing onto the current queue head is done.
- [ ] The lane-specific evidence was rerun after the last rebase.
- [ ] `make bench-guard` passed if this change touches a hot path.
- [ ] This PR is ready to merge on its own, not as part of a batch.
