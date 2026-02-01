---
status: resolved
trigger: "Fix ARM64 assembly bug in kfBfly4Mx - test failing with mismatch at stage i=2 m=4 n=15 fstride=15 mm=16 diff=3.509578"
created: 2026-02-01T00:00:00Z
updated: 2026-02-01T00:00:00Z
---

## Current Focus

hypothesis: CONFIRMED - CMP operand order is reversed in Go ARM64 assembly
test: Inner loop CMP R7, R2 computes R2-R7, not R7-R2
expecting: Swapping operands will fix the loop condition
next_action: Fix CMP R7, R2 to CMP R2, R7 (line 181)

## Symptoms

expected: kfBfly4Mx assembly matches Go reference implementation
actual: Test fails with diff=3.509578 at stage i=2 m=4 n=15 fstride=15 mm=16
errors: "kfBfly4Mx mismatch: stage i=2 m=4 n=15 fstride=15 mm=16 diff=3.509578"
reproduction: go test ./internal/celt -run TestKfBfly4MxAgainstRef -v
started: Unknown - assembly implementation being tested

## Eliminated

## Evidence

- timestamp: 2026-02-01T00:30:00Z
  checked: Inner loop execution with m=1 vs m=2
  found: m=1 passes (single iteration), m=2 fails (only first iteration runs)
  implication: Inner loop condition terminates prematurely

- timestamp: 2026-02-01T00:35:00Z
  checked: Go ARM64 assembly operand order for CMP
  found: Go Plan9 ARM64 reverses operands - CMP R7, R2 means R2-R7 in GNU syntax
  implication: CMP R7, R2 computes R2-R7, BLT branches when m<j instead of j<m

## Resolution

root_cause: CMP operand order is reversed in Go ARM64 Plan9 assembly. CMP R7, R2 computes R2-R7, not R7-R2. The inner loop used "CMP R7, R2; BLT" which checked if m < j instead of j < m, causing the loop to exit after the first iteration.

fix: Changed "CMP R7, R2" to "CMP R2, R7" for both inner and outer loops (lines 181, 185) so that BLT correctly branches when j < m and i < n respectively.

verification: All TestKfBfly4Mx* tests pass. kfBfly4MxAvailable() now returns true.

files_changed:
  - /Users/thesyncim/GolandProjects/gopus/internal/celt/kf_bfly4_arm64.s (fixed CMP operand order)
  - /Users/thesyncim/GolandProjects/gopus/internal/celt/kf_bfly_m1_arm64.go (enabled kfBfly4Mx)
