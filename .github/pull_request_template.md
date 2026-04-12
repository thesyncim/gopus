## Claim

Lane:
Editable surface:
Owner:
Hypothesis:
Blocked by:
Queue dependency:

## Overlap Check

- [ ] I checked the active draft PR queue for the same lane and editable surface.
- [ ] No other editable PR currently owns this pair, or this PR is read-only scout work.
- [ ] This slice stays within one editable surface plus, if needed, one narrow supporting helper.

## Evidence

Primary judge or target:
Commands run:
Latest result row or qualitative evidence:
Risk and rollback notes:

## Merge Readiness

- [ ] Rebasing onto the current queue head is done.
- [ ] The lane-specific evidence was rerun after the last rebase.
- [ ] `make bench-guard` passed if this change touches a hot path.
- [ ] This PR is ready to merge on its own, not as part of a batch.
