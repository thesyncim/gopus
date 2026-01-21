---
phase: 04-hybrid-decoder
plan: 02
subsystem: decoder
tags: [plc, packet-loss, concealment, silk, celt, hybrid, opus]

# Dependency graph
requires:
  - phase: 04-01-hybrid-decoder
    provides: Hybrid decoder foundation for PLC integration
  - phase: 02-silk-decoder
    provides: SILK decoder state for PLC extrapolation
  - phase: 03-celt-decoder
    provides: CELT decoder state and synthesis for PLC
provides:
  - PLC package with state tracking and fade management
  - SILK PLC with LPC extrapolation and pitch prediction
  - CELT PLC with energy decay and noise fill
  - Hybrid PLC coordinating SILK+CELT concealment
  - Interface-based design avoiding circular imports
affects: [05-unified-api, integration-testing, real-time-streaming]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - Interface-based state access to avoid circular imports
    - Exponential fade decay for smooth concealment
    - LPC-filtered comfort noise for unvoiced speech
    - Energy-scaled noise for CELT spectral shaping

key-files:
  created:
    - internal/plc/plc.go
    - internal/plc/silk_plc.go
    - internal/plc/celt_plc.go
    - internal/plc/plc_test.go
  modified:
    - internal/silk/silk.go
    - internal/celt/decoder.go
    - internal/hybrid/hybrid.go
    - internal/hybrid/hybrid_test.go

key-decisions:
  - "D04-02-01: Interface-based PLC design avoids silk/celt circular imports"
  - "D04-02-02: FadePerFrame = 0.5 (~-6dB per frame) for smooth decay"
  - "D04-02-03: MaxConcealedFrames = 5 (~100ms at 20ms frames)"
  - "D04-02-04: Nil data signals PLC instead of error"
  - "D04-02-05: EnergyDecayPerFrame = 0.85 for CELT band energy"

patterns-established:
  - "Interface abstraction for cross-package state access"
  - "Nil input as PLC trigger (not error)"
  - "Exponential fade for packet loss concealment"

# Metrics
duration: 11min
completed: 2026-01-21
---

# Phase 04 Plan 02: PLC (Packet Loss Concealment) Summary

**PLC module with SILK (LPC/pitch extrapolation), CELT (energy decay/noise fill), and Hybrid (coordinated) concealment using exponential fade**

## Performance

- **Duration:** 11 min
- **Started:** 2026-01-21T22:53:32Z
- **Completed:** 2026-01-21T23:04:31Z
- **Tasks:** 3
- **Files modified:** 8 (4 created, 4 modified)

## Accomplishments

- Created PLC package with State tracking, fade factor management, and mode handling
- Implemented SILK PLC with LPC extrapolation (voiced) and comfort noise (unvoiced)
- Implemented CELT PLC with energy decay and noise-filled bands
- Integrated PLC into SILK, CELT, and Hybrid decoders via nil data detection
- Designed interface-based architecture to avoid circular imports
- Added 15 comprehensive tests for PLC functionality

## Task Commits

Each task was committed atomically:

1. **Task 1: PLC package with state tracking** - `cc0479a` (feat)
2. **Task 2: SILK and CELT concealment algorithms** - `b8dc6ae` (feat)
3. **Task 3: Decoder integration and tests** - `7b3be41` (feat)

## Files Created/Modified

### Created
- `internal/plc/plc.go` - State struct, Mode enum, fade tracking, constants
- `internal/plc/silk_plc.go` - ConcealSILK, voiced/unvoiced PLC, pitch estimation
- `internal/plc/celt_plc.go` - ConcealCELT, ConcealCELTHybrid, noise generation
- `internal/plc/plc_test.go` - 15 tests covering state, SILK PLC, CELT PLC

### Modified
- `internal/silk/silk.go` - Added PLC state, decodePLC/decodePLCStereo methods
- `internal/celt/decoder.go` - Added PLC state, decodePLC/decodePLCHybrid methods
- `internal/hybrid/hybrid.go` - Added coordinated PLC for nil data
- `internal/hybrid/hybrid_test.go` - Updated test expectations for PLC behavior

## Decisions Made

1. **D04-02-01: Interface-based PLC design**
   - SILKDecoderState and CELTDecoderState interfaces
   - Avoids circular imports between plc, silk, and celt packages
   - Allows PLC to access decoder state without direct dependency

2. **D04-02-02: FadePerFrame = 0.5 (~-6dB per frame)**
   - Exponential decay: fade = fade * 0.5 each lost frame
   - After 1 loss: 0.5, After 2: 0.25, After 5: ~0.03
   - Smooth fade-out prevents jarring audio artifacts

3. **D04-02-03: MaxConcealedFrames = 5**
   - ~100ms of concealment at 20ms frames
   - After 5 frames, output effectively silent (fade < 0.03)
   - Matches typical real-time streaming requirements

4. **D04-02-04: Nil data signals PLC**
   - `Decode(nil, frameSize)` triggers PLC instead of error
   - Empty slice `[]byte{}` still returns error
   - Clean API for network applications with packet loss

5. **D04-02-05: EnergyDecayPerFrame = 0.85**
   - CELT band energies decay by 15% per frame
   - Maintains spectral shape while fading out
   - Prevents abrupt energy changes

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

**Circular Import Detection:**
- Initial design imported silk/celt from plc package
- Go compiler correctly identified circular dependency
- Resolved by creating interface types in plc package
- Silk and CELT decoders implement interfaces automatically

## Test Results

All tests pass:
- `internal/plc`: 15 tests covering state tracking, fade profile, SILK/CELT PLC
- `internal/silk`: 46 tests (no regressions)
- `internal/celt`: 61 tests (no regressions)
- `internal/hybrid`: 15 tests (1 test updated for PLC behavior)
- Total: 83+ tests passing

## PLC Behavior Summary

### SILK PLC
- **Voiced frames:** Pitch period repetition from output history
- **Unvoiced frames:** LPC-filtered comfort noise
- **Fade:** Applied to excitation amplitude

### CELT PLC
- **Energy:** Previous frame energies decayed by 0.85
- **Bands:** Filled with normalized noise scaled by energy
- **Synthesis:** Normal IMDCT + window + overlap-add

### Hybrid PLC
- **SILK:** 0-8kHz via SILK PLC (16kHz upsampled to 48kHz)
- **CELT:** 8-20kHz via CELT PLC (bands 17-21 only)
- **Combination:** SILK delayed 60 samples, then summed with CELT

## Next Phase Readiness

- Phase 04 (Hybrid Decoder) complete
- Ready for Phase 05 (Unified API)
- PLC provides essential reliability for real-time streaming

---
*Phase: 04-hybrid-decoder*
*Completed: 2026-01-21*
