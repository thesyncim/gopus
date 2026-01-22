---
phase: 05-multistream-decoder
plan: 02
subsystem: decoder
tags: [opus, multistream, surround, channel-mapping, plc, decode]

# Dependency graph
requires:
  - phase: 05-01
    provides: MultistreamDecoder struct, channel mapping, packet parser
  - phase: 04-hybrid-decoder
    provides: hybrid.Decoder for stream decoding
provides:
  - Decode method with channel mapping application
  - DecodeToInt16 and DecodeToFloat32 convenience wrappers
  - PLC support for multistream packets
  - Comprehensive test suite (18 test functions)
affects: [unified-api, ogg-container, streaming-decoder]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - Sample-interleaved output format [ch0_s0, ch1_s0, ..., chN_s0, ch0_s1, ...]
    - Channel mapping application via applyChannelMapping
    - Per-stream PLC with coordinated fade

key-files:
  created:
    - internal/multistream/multistream.go
    - internal/multistream/multistream_test.go
  modified: []

key-decisions:
  - "D05-02-01: Sample-interleaved output format for multistream decode"
  - "D05-02-02: Per-stream PLC with global fade factor coordination"

patterns-established:
  - "Channel routing: applyChannelMapping routes decoded streams to output"
  - "Multistream PLC: Each stream generates PLC independently, then mapped"

# Metrics
duration: 4min
completed: 2026-01-22
---

# Phase 5 Plan 02: Multistream Decode Summary

**Multistream Decode methods with channel mapping application, PLC support, and comprehensive test suite (18 tests, 697 lines)**

## Performance

- **Duration:** 3 min 33 sec
- **Started:** 2026-01-22T09:45:29Z
- **Completed:** 2026-01-22T09:49:02Z
- **Tasks:** 3
- **Files created:** 2

## Accomplishments

- Decode method that parses multistream packets, decodes each stream, and applies channel mapping
- applyChannelMapping function for routing streams to output channels (supports silent channels)
- PLC support coordinating per-stream concealment with global fade factor
- DecodeToInt16 and DecodeToFloat32 convenience wrappers
- Comprehensive test suite with 18 test functions covering all APIs

## Task Commits

Each task was committed atomically:

1. **Task 1: Decode method with channel mapping** - `780fd68` (feat)
2. **Task 2: Comprehensive test suite** - `0162e50` (test)

Task 3 was verification only (no changes needed).

## Files Created

- `internal/multistream/multistream.go` - Decode, decodePLC, DecodeToInt16, DecodeToFloat32, applyChannelMapping
- `internal/multistream/multistream_test.go` - 18 test functions, 697 lines, 65+ test cases

## Decisions Made

| ID | Decision | Rationale |
|----|----------|-----------|
| D05-02-01 | Sample-interleaved output format | Standard format: [ch0_s0, ch1_s0, ..., chN_s0, ch0_s1, ...] matches audio APIs |
| D05-02-02 | Per-stream PLC with global fade | Each stream handles PLC independently; fade factor shared for consistent decay |

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None.

## Test Coverage

- **TestNewDecoder_ValidConfigs** - mono through 7.1 surround configurations
- **TestNewDecoder_InvalidConfigs** - parameter validation edge cases
- **TestDefaultMapping** - Vorbis mapping tables for 1-8 channels
- **TestResolveMapping** - coupled/uncoupled channel resolution
- **TestStreamChannels** - stereo vs mono channel count
- **TestParseSelfDelimitedLength** - length prefix parsing
- **TestParseMultistreamPacket** - packet extraction
- **TestApplyChannelMapping** - channel routing verification
- **TestGetFrameDuration** - TOC-based duration extraction
- **TestValidateStreamDurations** - duration consistency checks
- **TestDecodePLC** - packet loss concealment path
- **TestDecodeToInt16/Float32** - conversion wrappers
- **TestFloat64ToInt16/Float32** - sample conversion helpers
- **TestDecoderReset** - state reset
- **TestMultistreamPLCState** - PLC state accessor

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- Multistream decoder complete with full Decode API
- Ready for integration with unified decoder API (Phase 06)
- All tests pass (81 test runs including subtests)
- No vet warnings, no race conditions

---
*Phase: 05-multistream-decoder*
*Plan: 02*
*Completed: 2026-01-22*
