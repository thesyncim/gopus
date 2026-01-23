---
phase: 14-extended-frame-size
plan: 04
subsystem: testing
tags: [rfc8251, compliance, testvectors, quality-metrics]

# Dependency graph
requires:
  - phase: 14-02
    provides: CELT 2.5ms and 5ms frame decoding support
  - phase: 14-03
    provides: SILK 40ms and 60ms decode path verification
provides:
  - RFC 8251 compliance test infrastructure
  - Quality metric computation for all 12 test vectors
  - Frame size and mode tracking per test vector
  - Hybrid mode verification confirming RFC 6716 constraints
affects: [future-compliance, decoder-fixes]

# Tech tracking
tech-stack:
  added: []
  patterns: [test-vector-validation, quality-metrics]

key-files:
  created: []
  modified:
    - internal/testvectors/compliance_test.go
    - internal/testvectors/parser.go

key-decisions:
  - "Extended frame sizes only appear in SILK/CELT modes, not Hybrid (verified)"
  - "Track decode errors by type to identify patterns"
  - "Q=-100 indicates fundamental decoder issue needing investigation"

patterns-established:
  - "Frame size tracking: Parse TOC to extract mode and frame size per packet"
  - "Error summary: Aggregate decode errors by type to reduce log noise"

# Metrics
duration: 3min
completed: 2026-01-23
---

# Phase 14 Plan 04: Test Vector Validation Summary

**RFC 8251 compliance test infrastructure with frame size/mode tracking confirms extended sizes only in SILK/CELT modes**

## Performance

- **Duration:** 2m 33s
- **Started:** 2026-01-23T09:26:10Z
- **Completed:** 2026-01-23T09:28:43Z
- **Tasks:** 3
- **Files modified:** 2

## Accomplishments

- Enhanced compliance test with frame size and mode logging per packet
- Improved error handling to track decode errors by type with summary
- Added TestComplianceSummary producing overview table for all 12 vectors
- Verified hybrid mode assumption: extended frame sizes (2.5/5/40/60ms) only appear in SILK or CELT modes

## Task Commits

Each task was committed atomically:

1. **Task 1: Enhance compliance test with frame size and mode logging** - `2003ca4` (feat)
2. **Task 2: Run full compliance test suite** - `6a41d3e` (feat)
3. **Task 3: Document compliance status with mode verification** - `7a861d4` (feat)

## Files Created/Modified

- `internal/testvectors/compliance_test.go` - Enhanced with frame size tracking, error summary, and TestComplianceSummary
- `internal/testvectors/parser.go` - Added getModeFromConfig() for TOC mode detection

## Decisions Made

1. **Hybrid mode verification confirmed** - Extended frame sizes (2.5ms/5ms/40ms/60ms) only appear in SILK-only or CELT-only modes, never in Hybrid mode, matching RFC 6716 specification
2. **Error aggregation by type** - Limit verbose error logging to first 3 occurrences per error type, then summarize counts
3. **Per-packet frame size allocation** - Decode buffer sized per-packet based on each packet's TOC, not just first packet

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

**Decoder returns "hybrid: invalid frame size" for CELT/SILK packets**

The compliance test revealed that CELT-only and SILK-only packets are incorrectly triggering the hybrid frame size validation error. This causes all 12 test vectors to fail with Q=-100. This is a decoder bug outside the scope of this plan.

- testvector01 (CELT-only): 1703 packets fail with hybrid frame size error
- testvector02-04 (SILK-only): 264, 224, 282 packets fail respectively
- The error message "hybrid: invalid frame size" appears for mode=CELT packets

This needs investigation in a future plan to trace why non-hybrid packets trigger hybrid validation.

## Test Vector Summary

| Vector | Packets | Modes | Frame Sizes | Q(.dec) | Status |
|--------|---------|-------|-------------|---------|--------|
| testvector01 | 2147 | CELT | 2.5,5,10,20ms | -100.00 | FAIL |
| testvector02 | 1185 | SILK | 10,20,40,60ms | -100.00 | FAIL |
| testvector03 | 998 | SILK | 10,20,40,60ms | -100.00 | FAIL |
| testvector04 | 1265 | SILK | 10,20,40,60ms | -100.00 | FAIL |
| testvector05 | 2037 | Hybrid | 10,20ms | -100.00 | FAIL |
| testvector06 | 1876 | Hybrid | 10,20ms | -100.00 | FAIL |
| testvector07 | 4186 | CELT | 2.5,5,10,20ms | -100.00 | FAIL |
| testvector08 | 1247 | CELT,SILK | 2.5,5,10,20ms | -100.00 | FAIL |
| testvector09 | 1337 | SILK,CELT | 2.5,5,10,20ms | -100.00 | FAIL |
| testvector10 | 1912 | CELT,Hybrid | 2.5,5,10,20ms | -100.00 | FAIL |
| testvector11 | 553 | CELT | 20ms | -100.00 | FAIL |
| testvector12 | 1332 | SILK,Hybrid | 20ms | -100.00 | FAIL |

**Overall: 0/12 passed**

**Hybrid mode verification: CONFIRMED** - Extended sizes only in SILK/CELT modes as expected per RFC 6716.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

**Phase 14 Extended Frame Size Support is complete in terms of infrastructure:**
- 14-01: DecodeBands returns frameSize coefficients
- 14-02: OverlapAdd produces frameSize samples for all frame sizes
- 14-03: SILK 40ms/60ms decode path verified
- 14-04: Test vector validation infrastructure with hybrid mode verification

**Blocker for passing compliance tests:**
The decoder has a bug where CELT-only and SILK-only packets incorrectly trigger hybrid frame size validation. This needs investigation to understand why the decoder is treating non-hybrid packets as hybrid mode.

---
*Phase: 14-extended-frame-size*
*Completed: 2026-01-23*
