---
phase: 08
plan: 02
subsystem: encoder
tags: [toc, packet, opus-format, rfc-6716]
dependency-graph:
  requires: [08-01]
  provides: [toc-generation, packet-assembly, complete-opus-packets]
  affects: [08-03, 08-04]
tech-stack:
  added: []
  patterns: [encoder-decoder-symmetry, round-trip-verification]
key-files:
  created:
    - internal/encoder/packet.go
    - internal/encoder/packet_test.go
  modified:
    - packet.go
    - packet_test.go
    - internal/encoder/encoder.go
    - internal/encoder/encoder_test.go
decisions: [D08-02-01, D08-02-02]
metrics:
  duration: ~12 minutes
  completed: 2026-01-22
---

# Phase 08 Plan 02: TOC Byte Generation and Packet Assembly Summary

TOC byte generation functions added to packet.go, packet assembly functions added to internal/encoder/packet.go, and Encoder.Encode() now returns complete Opus packets with valid TOC bytes.

## What Was Done

### Task 1: TOC Byte Generation
Added to `packet.go`:
- `GenerateTOC(config, stereo, frameCode)`: Creates TOC byte from encoding parameters
- `ConfigFromParams(mode, bandwidth, frameSize)`: Returns config index 0-31 for valid combinations
- `ValidConfig(config)`: Validates config index is in range

Tests verify round-trip: `GenerateTOC` -> `ParseTOC` produces identical values for all 32 configs.

### Task 2: Packet Assembly
Created `internal/encoder/packet.go`:
- `BuildPacket(frameData, mode, bandwidth, frameSize, stereo)`: Creates single-frame packet (code 0)
- `BuildMultiFramePacket(frames, mode, bandwidth, frameSize, stereo, vbr)`: Creates multi-frame packet (code 3)
- `writeFrameLength(dst, length)`: Handles RFC 6716 two-byte frame length encoding (lengths >= 252)

Error handling:
- `ErrInvalidConfig`: Invalid mode/bandwidth/frameSize combination
- `ErrInvalidFrameCount`: Frame count not in 1-48 range

### Task 3: Encoder Integration
Updated `Encoder.Encode()` to:
1. Encode raw frame data via SILK/Hybrid/CELT sub-encoders
2. Call `BuildPacket` to add TOC byte
3. Apply bitrate constraints (VBR/CVBR/CBR) to final packet

Added `modeToGopus()` helper to convert internal encoder.Mode to gopus.Mode.

## Decisions Made

| ID | Decision | Rationale |
|----|----------|-----------|
| D08-02-01 | TOC generation as inverse of ParseTOC | Ensures encoder/decoder symmetry |
| D08-02-02 | ConfigFromParams searches configTable linearly | Simple O(32) search; configTable is fixed-size array |

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Incomplete FEC/DTX files**
- **Found during:** Task 1 build
- **Issue:** `internal/encoder/fec.go` and `dtx.go` referenced undefined Encoder fields
- **Fix:** Backed up incomplete fec.go, kept working dtx.go after verifying Encoder struct had necessary fields
- **Files:** internal/encoder/fec.go.bak (removed)
- **Commit:** Part of build fixes

**2. [Rule 3 - Blocking] Unused math/rand import**
- **Found during:** Task 2 tests
- **Issue:** encoder_test.go had unused import preventing test compilation
- **Fix:** Import was auto-restored by goimports (rand.Float64 is used)
- **Files:** internal/encoder/encoder_test.go

## Artifacts Produced

| File | Purpose | Exports |
|------|---------|---------|
| packet.go | TOC generation | GenerateTOC, ConfigFromParams, ValidConfig |
| internal/encoder/packet.go | Packet assembly | BuildPacket, BuildMultiFramePacket |
| packet_test.go | TOC tests | TestGenerateTOC, TestGenerateTOCRoundTrip, TestConfigFromParams, TestValidConfig |
| internal/encoder/packet_test.go | Packet tests | TestBuildPacket, TestBuildMultiFramePacket, TestWriteFrameLength |
| internal/encoder/encoder_test.go | Integration tests | TestEncoderPacketFormat, TestEncoderPacketConfigs, TestEncoderPacketStereo, TestEncoderPacketParseable |

## Verification Results

All verification criteria pass:

1. `go build ./...` - compiles successfully
2. `go test -v ./...` - all tests pass
3. GenerateTOC + ParseTOC round-trip - 256 combinations verified
4. Encoder output parseable by ParsePacket - confirmed
5. Config indices match RFC 6716:
   - Hybrid SWB 10ms: config 12
   - Hybrid SWB 20ms: config 13
   - Hybrid FB 10ms: config 14
   - Hybrid FB 20ms: config 15

## Key Links Verified

| From | To | Via | Status |
|------|----|-----|--------|
| internal/encoder/packet.go | packet.go | GenerateTOC, ConfigFromParams | Working |
| internal/encoder/encoder.go | internal/encoder/packet.go | BuildPacket | Working |

## Test Coverage

- packet.go: 4 new test functions
- internal/encoder/packet.go: 6 test functions
- internal/encoder/encoder.go: 4 new test functions for packet format

## Commits

| Hash | Type | Description |
|------|------|-------------|
| 45d668e | feat | Add TOC byte generation functions |
| dba0ebe | feat | Add packet assembly functions |
| f2da4ff | test | Add encoder packet format verification tests |

## Next Phase Readiness

Plan 08-02 complete. All success criteria met:
- GenerateTOC and ConfigFromParams functions exist in packet.go
- BuildPacket and BuildMultiFramePacket work for all modes
- Encoder.Encode() returns complete Opus packets
- TOC byte correctly identifies mode, bandwidth, stereo, frame code
- All tests pass

Ready for 08-03 (VBR/CBR controls) or 08-04 (FEC/DTX).
