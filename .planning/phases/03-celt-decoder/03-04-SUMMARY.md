---
phase: 03-celt-decoder
plan: 04
subsystem: codec
tags: [pvq, cwrs, band-folding, mdct, celt, audio-decoding]

# Dependency graph
requires:
  - phase: 03-01
    provides: CELT decoder struct, tables, modes
  - phase: 03-02
    provides: CWRS DecodePulses and PVQ_V functions
provides:
  - PVQ vector decoding with CWRS index conversion
  - Band folding for uncoded bands with sign variation
  - DecodeBands orchestration for full spectral reconstruction
  - Collapse mask tracking for anti-collapse
  - DecodeUniform/DecodeRawBits for range decoder
affects: [03-05-imdct, celt-synthesis, hybrid-mode]

# Tech tracking
tech-stack:
  added: []
  patterns: [band-by-band-processing, normalized-vectors, energy-denormalization]

key-files:
  created:
    - internal/celt/pvq.go
    - internal/celt/bands.go
    - internal/celt/folding.go
    - internal/celt/bands_test.go
  modified:
    - internal/rangecoding/decoder.go
    - internal/celt/decoder.go

key-decisions:
  - "D03-04-01: DecodeUniform added to range decoder for PVQ index decoding"
  - "D03-04-02: bitsToK uses binary search with V(n,k) approximation"
  - "D03-04-03: FoldBand uses LCG with constants 1664525/1013904223"
  - "D03-04-04: Stereo uses mid-side with 8-step theta quantization"

patterns-established:
  - "Band processing: iterate low-to-high, PVQ if k>0, fold if k==0"
  - "Collapse tracking: uint32 bitmask, bit per band"
  - "Energy denormalization: gain = exp(energy * ln(2))"

# Metrics
duration: 12min
completed: 2026-01-21
---

# Phase 3 Plan 4: PVQ Band Decoding Summary

**PVQ band decoding with CWRS index conversion, band folding for uncoded bands, and DecodeBands orchestration for spectral reconstruction**

## Performance

- **Duration:** 12 min
- **Started:** 2026-01-21T00:00:00Z
- **Completed:** 2026-01-21T00:12:00Z
- **Tasks:** 4
- **Files modified:** 6

## Accomplishments
- PVQ decoding converts CWRS indices to unit-normalized vectors
- Band folding reconstructs uncoded bands from lower coded bands with pseudo-random sign variation
- DecodeBands orchestrates full spectral coefficient reconstruction
- Collapse mask tracks which bands received pulses for anti-collapse processing
- Range decoder extended with DecodeUniform and DecodeRawBits methods

## Task Commits

Each task was committed atomically:

1. **Task 1: Implement PVQ vector decoding and normalization** - `30d66a6` (feat)
2. **Task 2: Implement band folding** - `ff3ae7e` (feat)
3. **Task 3: Implement band processing orchestration** - `5614f23` (feat)
4. **Task 4: Add band processing tests** - `4054fcf` (test)

## Files Created/Modified
- `internal/celt/pvq.go` - PVQ decoding: DecodePVQ, NormalizeVector, DecodeTheta, stereo helpers
- `internal/celt/bands.go` - Band orchestration: DecodeBands, DecodeBandsStereo, bitsToK, denormalization
- `internal/celt/folding.go` - Band folding: FoldBand, FindFoldSource, collapse mask tracking
- `internal/celt/bands_test.go` - Tests: normalization, folding, bits-to-K, collapse mask, benchmarks
- `internal/rangecoding/decoder.go` - Added DecodeUniform and DecodeRawBits methods
- `internal/celt/decoder.go` - Added collapseMask field

## Decisions Made

| ID | Decision | Rationale |
|----|----------|-----------|
| D03-04-01 | Added DecodeUniform to range decoder | Required for PVQ index decoding (uniform distribution over V(n,k) values) |
| D03-04-02 | bitsToK uses binary search with V(n,k) | Accurate conversion from bit allocation to pulse count |
| D03-04-03 | FoldBand uses LCG constants 1664525/1013904223 | Matches libopus for deterministic sign variation |
| D03-04-04 | Stereo uses 8-step theta quantization | Balance between precision and bit cost |

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Added DecodeUniform to range decoder**
- **Found during:** Task 1 (PVQ vector decoding)
- **Issue:** Plan specified DecodePVQ uses d.rangeDecoder.DecodeUniform() but method didn't exist
- **Fix:** Implemented DecodeUniform with multi-byte support and DecodeRawBits for fine bits
- **Files modified:** internal/rangecoding/decoder.go
- **Verification:** go build passes, method used by DecodePVQ
- **Committed in:** 30d66a6 (Task 1 commit)

---

**Total deviations:** 1 auto-fixed (1 blocking)
**Impact on plan:** Auto-fix was essential for completing PVQ decoding. No scope creep.

## Issues Encountered
- None - plan executed smoothly after blocking issue resolved

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness
- PVQ band decoding complete, ready for IMDCT synthesis
- Band vectors are denormalized MDCT coefficients
- Stereo support includes mid-side and intensity modes
- Collapse mask available for anti-collapse in transient frames

---
*Phase: 03-celt-decoder*
*Completed: 2026-01-21*
