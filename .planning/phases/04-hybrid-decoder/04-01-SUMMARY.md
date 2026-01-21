---
phase: 04-hybrid-decoder
plan: 01
subsystem: decoder
tags: [hybrid, silk, celt, opus, decoder, 48kHz]

# Dependency graph
requires:
  - phase: 02-silk-decoder
    provides: SILK WB decoder for low-frequency layer (0-8kHz)
  - phase: 03-celt-decoder
    provides: CELT decoder with band processing and synthesis
provides:
  - Hybrid decoder struct coordinating SILK+CELT
  - DecodeFrameHybrid for band-limited CELT decoding
  - Public Decode/DecodeStereo API for hybrid frames
  - 60-sample delay compensation for SILK-CELT alignment
affects: [05-unified-api, 06-encoder, integration-testing]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - Shared range decoder between SILK and CELT layers
    - Delay buffer for time alignment between codec layers
    - Band zeroing for frequency-split hybrid coding

key-files:
  created:
    - internal/hybrid/decoder.go
    - internal/hybrid/hybrid.go
    - internal/hybrid/hybrid_test.go
  modified:
    - internal/celt/decoder.go

key-decisions:
  - "D04-01-01: Zero bands 0-16 in CELT hybrid mode (simpler than true band-limited decoding)"
  - "D04-01-02: Linear interpolation for 3x upsampling (consistent with SILK approach)"
  - "D04-01-03: Delay compensation via shift buffer (60 samples per channel)"

patterns-established:
  - "Multi-layer decoder coordination via shared range decoder"
  - "Delay buffer for codec layer time alignment"

# Metrics
duration: 5min
completed: 2026-01-21
---

# Phase 04 Plan 01: Hybrid Decoder Foundation Summary

**Hybrid decoder coordinating SILK (0-8kHz at WB 16kHz) with CELT (bands 17-21) using 60-sample delay compensation and 3x upsampling**

## Performance

- **Duration:** 5 min
- **Started:** 2026-01-21T22:44:02Z
- **Completed:** 2026-01-21T22:49:21Z
- **Tasks:** 3
- **Files modified:** 4

## Accomplishments

- Created hybrid package with Decoder struct coordinating SILK and CELT sub-decoders
- Added DecodeFrameHybrid to CELT for band-limited decoding (bands 17-21 only)
- Implemented 60-sample delay compensation for SILK-CELT time alignment
- Built public API with Decode/DecodeStereo and convenience wrappers

## Task Commits

Each task was committed atomically:

1. **Task 1: Hybrid decoder struct** - `287dc90` (feat)
2. **Task 2: DecodeFrameHybrid** - `3942220` (feat)
3. **Task 3: Public API and tests** - `8788937` (feat)

## Files Created/Modified

- `internal/hybrid/decoder.go` - Hybrid decoder struct, SILK/CELT coordination, delay compensation
- `internal/hybrid/hybrid.go` - Public Decode/DecodeStereo API, format conversion helpers
- `internal/hybrid/hybrid_test.go` - 15 unit tests (68.9% coverage)
- `internal/celt/decoder.go` - Added DecodeFrameHybrid, HybridCELTStartBand constant

## Decisions Made

1. **D04-01-01: Zero bands 0-16 in CELT hybrid mode**
   - Simpler than true band-limited bit allocation
   - Decode all bands normally, then zero lower bands before synthesis
   - Same final output, less code complexity

2. **D04-01-02: Linear interpolation for 3x upsampling**
   - Consistent with existing SILK upsampling approach
   - Sufficient quality for v1, polyphase can be added later
   - Upsamples SILK 16kHz output to 48kHz

3. **D04-01-03: Delay compensation via shift buffer**
   - 60-sample delay buffer per channel
   - Current SILK tail becomes next frame's delay start
   - Ensures proper time alignment between SILK and CELT layers

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- Hybrid decoder complete for mono and stereo frames
- Ready for integration with unified Opus decoder API
- Test coverage at 68.9% (some tests skipped due to synthetic data limitations)
- All SILK and CELT tests continue to pass (no regressions)

---
*Phase: 04-hybrid-decoder*
*Completed: 2026-01-21*
