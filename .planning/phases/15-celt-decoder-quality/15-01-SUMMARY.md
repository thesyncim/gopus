---
phase: 15-celt-decoder-quality
plan: 01
subsystem: decoder
tags: [celt, energy-prediction, coefficients, libopus-compat]

# Dependency graph
requires:
  - phase: 03-celt-decoder
    provides: Basic CELT energy decoding structure
provides:
  - Corrected BetaCoefInter array with LM-dependent values
  - BetaIntra constant for intra-frame mode (0.15)
  - Inter-band prediction update formula matching libopus
  - Coefficient validation tests
affects: [15-02, 15-03, 15-04, 15-05, decoder-quality]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "LM-dependent beta coefficients for inter-frame mode"
    - "Fixed beta coefficient for intra-frame mode"
    - "Filtered inter-band prediction accumulator"

key-files:
  created:
    - internal/celt/tables_test.go
  modified:
    - internal/celt/tables.go
    - internal/celt/energy.go
    - internal/celt/energy_encode.go
    - internal/celt/modes_test.go

key-decisions:
  - "BetaCoefInter uses libopus values: 0.92, 0.68, 0.37, 0.20 by LM"
  - "BetaIntra fixed at 0.15 (4915/32768) for intra-frame mode"
  - "Inter-band predictor accumulates filtered quantized deltas, not final energies"

patterns-established:
  - "Use BetaIntra for intra-frame mode (alpha=0, beta=0.15)"
  - "Use BetaCoefInter[lm] for inter-frame mode (alpha=AlphaCoef[lm])"
  - "prevBandEnergy = prev + q - beta*q for inter-band prediction"

# Metrics
duration: 5min
completed: 2026-01-23
---

# Phase 15 Plan 01: Fix Coarse Energy Prediction Coefficients Summary

**Corrected BetaCoef from fixed 0.85 to LM-dependent values matching libopus exactly, fixing the highest-priority decoder quality issue**

## Performance

- **Duration:** 5 min 13s
- **Started:** 2026-01-23T10:38:41Z
- **Completed:** 2026-01-23T10:43:54Z
- **Tasks:** 3
- **Files modified:** 4 (+ 1 created)

## Accomplishments
- Replaced incorrect BetaCoef (all 0.85) with correct BetaCoefInter (0.92, 0.68, 0.37, 0.20)
- Added BetaIntra constant (0.15) for intra-frame mode
- Fixed inter-band prediction update formula to match libopus
- Added coefficient validation tests in tables_test.go
- Updated encoder (energy_encode.go) to use corrected coefficients

## Task Commits

Note: Tasks 1 and 2 were committed as part of 15-02 execution (dependency resolution).
Task 3 adds the validation tests.

1. **Task 1: Fix BetaCoef table in tables.go** - `eb7b7ab` (included in 15-02)
2. **Task 2: Update DecodeCoarseEnergy to use corrected coefficients** - `eb7b7ab` (included in 15-02)
3. **Task 3: Add coefficient validation tests** - `dbd9823` (test)

## Files Created/Modified
- `internal/celt/tables.go` - Replaced BetaCoef with BetaCoefInter + BetaIntra
- `internal/celt/energy.go` - Uses BetaIntra/BetaCoefInter, fixed inter-band formula
- `internal/celt/energy_encode.go` - Same fixes for encoder consistency
- `internal/celt/modes_test.go` - Updated to reference BetaCoefInter
- `internal/celt/tables_test.go` - NEW: Coefficient validation tests

## Decisions Made
- BetaCoefInter values from libopus celt/quant_bands.c:
  - LM=0 (2.5ms): 30147/32768 = 0.9200744
  - LM=1 (5ms):   22282/32768 = 0.6800537
  - LM=2 (10ms):  12124/32768 = 0.3700561
  - LM=3 (20ms):  6554/32768  = 0.2000122
- BetaIntra = 4915/32768 = 0.15 for intra-frame mode
- Inter-band predictor uses: prev = prev + q - beta*q (NOT prev = energy)

## Deviations from Plan

### Execution Order Deviation

**Tasks 1 and 2 were completed as part of Plan 15-02 execution.**

The coefficient fixes in tables.go and energy.go were necessary dependencies for 15-02's range decoder integration work. Commit `eb7b7ab` includes both the 15-02 changes and the 15-01 coefficient fixes.

This execution only needed to complete Task 3 (adding validation tests).

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Updated energy_encode.go for compilation**
- **Found during:** Task 1 verification
- **Issue:** Removing BetaCoef broke energy_encode.go which referenced it
- **Fix:** Updated energy_encode.go to use BetaIntra and BetaCoefInter
- **Files modified:** internal/celt/energy_encode.go

**2. [Rule 3 - Blocking] Updated modes_test.go for compilation**
- **Found during:** Task 1 verification
- **Issue:** Test referenced non-existent BetaCoef
- **Fix:** Updated to reference BetaCoefInter
- **Files modified:** internal/celt/modes_test.go

## Issues Encountered

### Pre-existing Flaky Test

TestCoarseEnergyEncoderProducesValidOutput occasionally fails due to:
- Uses unseeded `rand.Float64()` (shared global state)
- Test state isolation issues between subtests
- NOT related to 15-01 coefficient changes

This is a pre-existing issue from Phase 07-02 test implementation.

## Verification Results

All coefficient validation tests pass:
- TestAlphaCoef: PASS (verifies 4 inter-frame coefficients)
- TestBetaCoefInter: PASS (verifies 4 LM-dependent values are distinct)
- TestBetaIntra: PASS (verifies 0.15 intra-mode value)
- TestDecodeCoarseEnergy: PASS (verifies prediction uses corrected coefficients)

Values confirmed:
- BetaCoefInter[0] = 0.9200744629 (not 0.85)
- BetaCoefInter[3] = 0.2000122070 (not 0.85)
- BetaIntra = 0.1499938965 (not 0.85)

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness
- Coefficient values now match libopus celt/quant_bands.c exactly
- Energy prediction uses correct beta based on intra/inter mode and LM
- Ready for Plan 15-03 (if not already done) or subsequent plans
- No blockers

---
*Phase: 15-celt-decoder-quality*
*Plan: 01*
*Completed: 2026-01-23*
