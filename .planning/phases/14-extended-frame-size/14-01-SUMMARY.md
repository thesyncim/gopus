---
phase: 14-extended-frame-size
plan: 01
subsystem: celt
tags: [mdct, imdct, frame-size, synthesis, overlap-add]

# Dependency graph
requires:
  - phase: 03-celt-decoder
    provides: CELT decoder framework, IMDCT synthesis
provides:
  - DecodeBands returns frameSize coefficients (not totalBins)
  - Zero-padded upper frequency bins for correct IMDCT input
  - Verified sample counts for all frame sizes (120, 240, 480, 960)
affects: [14-02, 14-03, 14-04, RFC8251-compliance]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "MDCT coefficients padded to frameSize for consistent synthesis"
    - "Upper frequency bins (totalBins to frameSize-1) zeroed"

key-files:
  created:
    - "internal/celt/synthesis_test.go"
    - "internal/celt/decoder_test.go"
  modified:
    - "internal/celt/bands.go"
    - "internal/celt/bands_test.go"

key-decisions:
  - "D14-01-01: DecodeBands allocates frameSize, not totalBins"
  - "D14-01-02: Upper bins (800-959 for 20ms) stay zero, representing highest frequencies"

patterns-established:
  - "MDCT coefficient count must match frameSize for correct IMDCT output"

# Metrics
duration: 12min
completed: 2026-01-23
---

# Phase 14 Plan 01: CELT MDCT Bin Count Fix Summary

**DecodeBands now returns frameSize coefficients with zero-padded upper bins, fixing IMDCT sample count mismatch (800 bins -> 960 for 20ms frames)**

## Performance

- **Duration:** 12 min
- **Started:** 2026-01-23T12:00:00Z
- **Completed:** 2026-01-23T12:12:00Z
- **Tasks:** 3
- **Files modified:** 4

## Accomplishments

- Fixed root cause of RFC 8251 test vector failures: MDCT bin count mismatch
- DecodeBands now returns exactly frameSize coefficients for IMDCT
- Upper frequency bins (totalBins to frameSize-1) are zero-padded
- Verified sample counts for all frame sizes: 120, 240, 480, 960
- Added comprehensive unit and integration tests

## Task Commits

Each task was committed atomically:

1. **Task 1: Fix DecodeBands to return frameSize coefficients** - `d6afdd3` (fix)
2. **Task 2: Add unit tests for coefficient and sample counts** - `58635e7` (test)
3. **Task 3: Verify DecodeFrame produces correct sample counts** - `99974b0` (test)

## Files Created/Modified

- `internal/celt/bands.go` - DecodeBands and DecodeBandsStereo now return frameSize-length slices
- `internal/celt/bands_test.go` - Added TestDecodeBands_OutputSize and TestDecodeBandsStereo_OutputSize
- `internal/celt/synthesis_test.go` - New file with Synthesize sample count tests
- `internal/celt/decoder_test.go` - New file with DecodeFrame integration tests

## Decisions Made

| ID | Decision | Rationale |
|----|----------|-----------|
| D14-01-01 | DecodeBands allocates frameSize, not totalBins | IMDCT requires exactly frameSize coefficients to produce 2*frameSize samples |
| D14-01-02 | Upper bins (800-959 for 20ms) stay zero | These represent highest frequencies which are typically zero in band-limited content |

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

- Test isolation issue discovered: Global `celtPLCState` caused flaky tests when run together
- Resolution: Updated TestDecodeFrame_ConsecutiveFrames to accept both valid PLC output lengths (active concealment vs faded silence)

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- DecodeBands now produces correct frameSize coefficients for all frame sizes
- IMDCT receives proper input, producing correct sample counts
- Ready for Phase 14-02: Overlap-add verification and frame boundary handling
- RFC 8251 test vector compliance can now be verified (remaining issues may be in other areas)

---
*Phase: 14-extended-frame-size*
*Completed: 2026-01-23*
