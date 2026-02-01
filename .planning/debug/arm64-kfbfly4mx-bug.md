---
status: gathering
trigger: "Fix ARM64 assembly bug in kfBfly4Mx - test failing with mismatch at stage i=2 m=4 n=15 fstride=15 mm=16 diff=3.509578"
created: 2026-02-01T00:00:00Z
updated: 2026-02-01T00:00:00Z
---

## Current Focus

hypothesis: Assembly implementation has a bug compared to Go reference
test: Run test to confirm failure, then compare assembly logic
expecting: Identify the specific operation that diverges
next_action: Run test, add debug prints to find first mismatch point

## Symptoms

expected: kfBfly4Mx assembly matches Go reference implementation
actual: Test fails with diff=3.509578 at stage i=2 m=4 n=15 fstride=15 mm=16
errors: "kfBfly4Mx mismatch: stage i=2 m=4 n=15 fstride=15 mm=16 diff=3.509578"
reproduction: go test ./internal/celt -run TestKfBfly4MxAgainstRef -v
started: Unknown - assembly implementation being tested

## Eliminated

## Evidence

## Resolution

root_cause:
fix:
verification:
files_changed: []
