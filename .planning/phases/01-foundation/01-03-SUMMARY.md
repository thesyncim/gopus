---
phase: 01-foundation
plan: 03
subsystem: packet-parsing
tags: [opus, toc, packet, rfc6716]

dependency-graph:
  requires: []
  provides: [toc-parsing, packet-frame-extraction]
  affects: [02-silk-decoder, 03-celt-decoder]

tech-stack:
  added: []
  patterns: [config-table-lookup, two-byte-length-encoding]

key-files:
  created:
    - packet.go
    - packet_test.go
    - doc.go
  modified: []

decisions:
  - id: dec-01-03-01
    choice: "Config table as fixed array indexed by config number"
    reason: "Direct O(1) lookup, matches RFC 6716 Section 3.1 table structure"
  - id: dec-01-03-02
    choice: "ParseFrameLength as internal helper function"
    reason: "Two-byte encoding logic reused in Code 2 and Code 3 parsing"

metrics:
  duration: 3m38s
  completed: 2026-01-21
---

# Phase 01 Plan 03: TOC Byte and Packet Frame Parsing Summary

**One-liner:** TOC byte parsing with all 32 configs and packet frame extraction for codes 0-3 per RFC 6716 Section 3.

## What Was Built

### TOC Byte Parsing (packet.go)

Created complete TOC byte parsing implementation:

- **Mode type**: SILK (0-11), Hybrid (12-15), CELT (16-31)
- **Bandwidth type**: Narrowband, Mediumband, Wideband, Superwideband, Fullband
- **TOC struct**: Config, Mode, Bandwidth, FrameSize, Stereo, FrameCode
- **configTable**: All 32 configurations with correct mode/bandwidth/frame-size mappings
- **ParseTOC function**: Extracts all fields from single TOC byte

### Packet Frame Parsing (packet.go)

Implemented ParsePacket function handling all frame codes:

- **Code 0**: Single frame (remainder after TOC)
- **Code 1**: Two equal-sized frames
- **Code 2**: Two frames with first frame length encoded
- **Code 3**: Arbitrary frames (1-48) with VBR/CBR and optional padding
- **Two-byte length encoding**: For frame lengths >= 252 bytes
- **Padding continuation**: For padding > 254 bytes

### Package Documentation (doc.go)

Created package-level documentation explaining:

- Opus codec overview and capabilities
- Three operating modes (SILK, Hybrid, CELT)
- Packet structure with TOC byte layout
- Usage guidance for ParseTOC and ParsePacket

### Test Coverage (packet_test.go)

Comprehensive tests covering:

- All 32 TOC configurations
- All frame codes (0-3)
- Two-byte encoding edge cases (251, 252, 255, 256, 1020, 1275)
- VBR and CBR mode 3 packets
- Padding with continuation bytes
- Maximum frame count (M=48)
- Error conditions (truncated, invalid M, malformed)

## Technical Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Config storage | Fixed [32]configEntry array | O(1) lookup by config index, matches RFC table |
| Mode derivation | From config in table | Avoids repeated range checks |
| Frame length parsing | Internal helper function | Reused by Code 2 and Code 3 paths |
| Error types | Package-level vars | Allows error comparison with == |

## Verification Results

All verifications passed:

1. `go build ./...` - Compiles successfully
2. `go test -v ./...` - All tests pass (10 test functions, 70+ subtests)
3. `go list -f '{{.CgoFiles}}' ./...` - Returns empty (no cgo)
4. `go doc gopus` - Documentation renders correctly
5. Config table verified against RFC 6716 Section 3.1

## Commits

| Hash | Type | Description |
|------|------|-------------|
| 8d32e69 | feat | Implement TOC byte parsing and types |
| 1c5376a | feat | Implement packet frame parsing |
| d0c4319 | test | Add packet parsing tests and package documentation |

## Files Created

| File | Lines | Purpose |
|------|-------|---------|
| packet.go | 268 | TOC and packet parsing implementation |
| packet_test.go | 420 | Comprehensive test suite |
| doc.go | 28 | Package documentation |

## Deviations from Plan

None - plan executed exactly as written.

## Next Phase Readiness

Phase 1 Foundation is complete:

- Plan 01-01: Range coder constants and decoder (complete)
- Plan 01-02: Range encoder (ready for execution)
- Plan 01-03: TOC and packet parsing (complete)

The packet parsing module provides:
- TOC extraction for all decode operations
- Frame boundary detection for multi-frame packets
- Foundation for SILK and CELT decoder implementations

No blockers identified for subsequent phases.
