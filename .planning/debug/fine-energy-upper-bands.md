---
status: investigating
trigger: "Fine energy qi values differ for bands 17-20 causing encoder divergence"
created: 2026-02-01T10:00:00Z
updated: 2026-02-01T10:00:00Z
---

## Current Focus

hypothesis: Overlap buffer state or pre-emphasis state affects MDCT coefficients for upper bands
test: Compare MDCT coefficients and band energies for bands 17-20 between gopus and libopus
expecting: Find the exact point where energies diverge
next_action: Examine how energies are computed and compare with libopus

## Symptoms

expected: Fine energy qi values should match libopus for all bands
actual: Bands 17-20 have different qi values
errors: |
  | Band | libopus qi | gopus qi | fineBits |
  |------|------------|----------|----------|
  | 17   | 0          | 5        | 3        |
  | 18   | 1          | 3        | 3        |
  | 19   | 0          | 1        | 1        |
  | 20   | 1          | 0        | 1        |
reproduction: Run TestTraceRealEncodingDivergence in internal/celt/cgo_test/
started: Identified by Agent 39

## Eliminated

## Evidence

- timestamp: 2026-02-01T10:00:00Z
  checked: Previous debug findings from Agent 39
  found: Coarse energy encoding is CORRECT (first 16 bytes match). Issue is only in fine energy for upper bands.
  implication: The band energies array passed to EncodeFineEnergy differs for bands 17-20

## Resolution

root_cause:
fix:
verification:
files_changed: []
