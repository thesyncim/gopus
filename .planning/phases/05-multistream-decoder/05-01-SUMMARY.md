---
phase: 05-multistream-decoder
plan: 01
subsystem: decoder
tags: [opus, multistream, surround, channel-mapping, vorbis, rfc7845]

# Dependency graph
requires:
  - phase: 04-hybrid-decoder
    provides: hybrid.Decoder for stream decoding
provides:
  - MultistreamDecoder struct with parameter validation
  - Vorbis channel mapping tables for 1-8 channels (5.1/7.1 surround)
  - Self-delimiting packet parser per RFC 6716 Appendix B
  - DefaultMapping function for mapping family 1
affects: [05-02, unified-api, ogg-container]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - Coordinator pattern for multi-decoder management
    - streamDecoder interface for decoder abstraction
    - Self-delimiting framing for multistream packets

key-files:
  created:
    - internal/multistream/decoder.go
    - internal/multistream/mapping.go
    - internal/multistream/stream.go
  modified: []

key-decisions:
  - "D05-01-01: Use hybrid.Decoder for all streams (handles mode detection via TOC)"
  - "D05-01-02: streamDecoder interface wraps concrete decoders for uniform handling"
  - "D05-01-03: Validate mapping values against streams+coupledStreams bound"

patterns-established:
  - "Coordinator pattern: MultistreamDecoder owns N stream decoders"
  - "Channel mapping: resolveMapping interprets coupled/uncoupled distinction"

# Metrics
duration: 2min
completed: 2026-01-22
---

# Phase 5 Plan 01: Multistream Foundation Summary

**Multistream decoder foundation with Vorbis channel mapping tables (1-8ch), self-delimiting packet parser, and MultistreamDecoder struct for surround sound**

## Performance

- **Duration:** 2 min 20 sec
- **Started:** 2026-01-22T09:39:38Z
- **Completed:** 2026-01-22T09:41:58Z
- **Tasks:** 3
- **Files created:** 3

## Accomplishments

- Vorbis channel mapping tables supporting 1-8 channels (mono through 7.1 surround)
- Self-delimiting packet parser that extracts N stream packets from multistream data
- MultistreamDecoder struct with comprehensive parameter validation
- streamDecoder interface enabling uniform handling of hybrid.Decoder

## Task Commits

Each task was committed atomically:

1. **Task 1: Channel mapping tables and helpers** - `c3790a3` (feat)
2. **Task 2: Self-delimiting packet parser** - `99ee75a` (feat)
3. **Task 3: MultistreamDecoder struct and constructor** - `0fc827d` (feat)

## Files Created

- `internal/multistream/mapping.go` - Vorbis channel mapping tables, DefaultMapping, resolveMapping
- `internal/multistream/stream.go` - Self-delimiting packet parser, getFrameDuration
- `internal/multistream/decoder.go` - Decoder struct, NewDecoder constructor, streamDecoder interface

## Decisions Made

| ID | Decision | Rationale |
|----|----------|-----------|
| D05-01-01 | Use hybrid.Decoder for all streams | Handles SILK/CELT/Hybrid mode detection internally via TOC parsing |
| D05-01-02 | streamDecoder interface wraps concrete decoders | Enables uniform decoder management without exposing hybrid package details |
| D05-01-03 | Validate mapping values against streams+coupledStreams | Prevents invalid channel routing at decoder creation time |

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- Decoder struct created with all validation
- Ready for Plan 02 to add Decode() method and channel mapping application
- Self-delimiting parser ready to extract individual stream packets
- All foundation components compile and pass vet

---
*Phase: 05-multistream-decoder*
*Plan: 01*
*Completed: 2026-01-22*
