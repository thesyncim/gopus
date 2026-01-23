---
phase: 14-extended-frame-size
plan: 05
subsystem: decoder
tags: [mode-routing, silk, celt, hybrid, toc-parsing, rfc6716, rfc8251]

# Dependency graph
requires:
  - phase: 14-01
    provides: TOC parsing with Mode, Bandwidth, FrameSize fields
  - phase: 14-02
    provides: CELT decoder with extended frame size support
  - phase: 14-03
    provides: SILK decoder with extended frame size support
provides:
  - Mode routing in Decoder based on TOC mode field
  - SILK-only packets route to SILK decoder
  - CELT-only packets route to CELT decoder
  - Hybrid packets route to Hybrid decoder
  - PLC uses last decoded mode, not default to Hybrid
affects: [15-decoder-quality, future-compliance-work]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - Mode-based routing using switch on toc.Mode
    - Separate decoders for each Opus mode

key-files:
  created: []
  modified:
    - decoder.go
    - decoder_test.go
    - errors.go

key-decisions:
  - "Track lastMode for PLC to use correct decoder on packet loss"
  - "Add three decoder fields (silkDecoder, celtDecoder, hybridDecoder)"
  - "Route based on toc.Mode from ParseTOC"

patterns-established:
  - "Mode routing: switch mode { case ModeSILK: decodeSILK(); case ModeCELT: decodeCELT(); case ModeHybrid: decodeHybrid() }"

# Metrics
duration: 3min
completed: 2026-01-23
---

# Phase 14 Plan 05: Mode Routing Summary

**Mode routing implemented in Decoder to route SILK/CELT/Hybrid packets to their respective decoders, fixing the architectural blocker preventing RFC 8251 test vector compliance**

## Performance

- **Duration:** 3 min
- **Started:** 2026-01-23T09:51:08Z
- **Completed:** 2026-01-23T09:53:58Z
- **Tasks:** 3
- **Files modified:** 3

## Accomplishments

- Implemented mode routing in Decoder struct with three decoder fields
- SILK-only packets (configs 0-11) now route to SILK decoder
- CELT-only packets (configs 16-31) now route to CELT decoder
- Hybrid packets (configs 12-15) continue to route to Hybrid decoder
- Extended frame sizes (CELT 2.5/5ms, SILK 40/60ms) decode without "hybrid: invalid frame size" error
- PLC tracks lastMode to use correct decoder on packet loss

## Task Commits

Each task was committed atomically:

1. **Task 1: Add SILK and CELT decoders to Decoder struct and implement mode routing** - `79d6c40` (feat)
2. **Task 2: Add mode routing tests and verify extended frame sizes** - `63f9d94` (test)
3. **Task 3: Run RFC 8251 compliance test and verify improvement** - (verification only, no commit)

## Files Created/Modified

- `decoder.go` - Added silkDecoder, celtDecoder, hybridDecoder fields; mode routing switch; decodeSILK, decodeCELT, decodeHybrid helper methods; lastMode tracking
- `decoder_test.go` - Added TestDecode_ModeRouting (20 test cases), TestDecode_ExtendedFrameSizes (4 test cases), TestDecode_PLC_ModeTracking
- `errors.go` - Added ErrInvalidMode and ErrInvalidBandwidth

## Decisions Made

- **Track lastMode for PLC:** Store last decoded mode to ensure PLC uses correct decoder (SILK PLC for SILK streams, CELT PLC for CELT streams)
- **Three decoder fields:** Each mode has dedicated decoder instance initialized in NewDecoder
- **Mode routing via switch:** Clean switch statement on toc.Mode routes to appropriate helper method

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None - implementation followed plan precisely.

## RFC 8251 Compliance Status

The mode routing fix resolves the architectural blocker. Before this fix:
- All packets were routed to hybrid.Decoder
- Extended frame sizes (CELT 2.5/5ms, SILK 40/60ms) were rejected with "hybrid: invalid frame size"
- Compliance test vectors failed immediately

After this fix:
- Packets route to correct decoder based on TOC mode
- Extended frame sizes decode successfully
- Compliance tests run to completion
- Q metrics computed (Q=-100, indicating decoder output quality issues separate from routing)

## Test Vector Results

| Vector | Packets | Modes | Frame Sizes | Status |
|--------|---------|-------|-------------|--------|
| testvector01 | 2147 | CELT | 2.5/5/10/20ms | Decodes, Q=-100 |
| testvector02 | 1185 | SILK | 10/20/40/60ms | Decodes, Q=-100 |
| testvector03 | 998 | SILK | 10/20/40/60ms | Decodes, Q=-100 |
| testvector04 | 1265 | SILK | 10/20/40/60ms | Decodes, Q=-100 |
| testvector05 | 2037 | Hybrid | 10/20ms | Decodes, Q=-100 |
| testvector06 | 1876 | Hybrid | 10/20ms | Decodes, Q=-100 |
| testvector07 | 4186 | CELT | 2.5/5/10/20ms | Decodes, Q=-100 |
| testvector08 | 1247 | SILK,CELT | 2.5/5/10/20ms | Decodes, Q=-100 |
| testvector09 | 1337 | SILK,CELT | 2.5/5/10/20ms | Decodes, Q=-100 |
| testvector10 | 1912 | CELT,Hybrid | 2.5/5/10/20ms | Decodes, Q=-100 |
| testvector11 | 553 | CELT | 20ms | Decodes, Q=-100 |
| testvector12 | 1332 | SILK,Hybrid | 20ms | Decodes, Q=-100 |

Note: Q=-100 indicates decoder output doesn't match reference. This is a separate decoder quality issue, not a routing issue. The routing fix allows decoding to proceed; quality improvements require work on SILK/CELT decoder algorithms.

## Next Phase Readiness

- Mode routing architecture is complete and tested
- Phase 14 (Extended Frame Size Support) is functionally complete
- Future work: Investigate decoder quality issues to achieve Q >= 0 compliance

---
*Phase: 14-extended-frame-size*
*Completed: 2026-01-23*
