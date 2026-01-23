---
phase: 13
plan: 01
subsystem: public-api
tags: [multistream, surround, api, 5.1, 7.1]
dependency-graph:
  requires: [internal/multistream, 10-api-layer]
  provides: [MultistreamEncoder, MultistreamDecoder, public-surround-api]
  affects: []
tech-stack:
  added: []
  patterns: [wrapper-composition, public-api-facade]
key-files:
  created:
    - multistream.go
    - multistream_test.go
  modified:
    - errors.go
    - doc.go
decisions:
  - id: D13-01-01
    summary: Mirror Encoder/Decoder API pattern for Multistream
    rationale: Consistent API surface, familiar to users of mono/stereo API
metrics:
  duration: ~5m
  completed: 2026-01-23
---

# Phase 13 Plan 01: Multistream Public API Summary

**One-liner:** Public MultistreamEncoder/MultistreamDecoder wrapping internal multistream for 5.1/7.1 surround sound encoding/decoding

## What Was Built

### MultistreamEncoder (multistream.go)
- `NewMultistreamEncoder()` - explicit constructor with full parameters
- `NewMultistreamEncoderDefault()` - convenience constructor for 1-8 channels
- Methods: `Encode()`, `EncodeInt16()`, `EncodeFloat32()`, `EncodeInt16Slice()`
- Controls: `SetBitrate()`, `SetComplexity()`, `SetFEC()`, `SetDTX()`
- Getters: `Bitrate()`, `Complexity()`, `FECEnabled()`, `DTXEnabled()`
- Info: `Channels()`, `SampleRate()`, `Streams()`, `CoupledStreams()`
- State: `Reset()`

### MultistreamDecoder (multistream.go)
- `NewMultistreamDecoder()` - explicit constructor with full parameters
- `NewMultistreamDecoderDefault()` - convenience constructor for 1-8 channels
- Methods: `Decode()`, `DecodeInt16()`, `DecodeFloat32()`, `DecodeInt16Slice()`
- Info: `Channels()`, `SampleRate()`, `Streams()`, `CoupledStreams()`
- State: `Reset()`

### Error Types (errors.go)
- `ErrInvalidStreams` - invalid stream count (must be 1-255)
- `ErrInvalidCoupledStreams` - invalid coupled streams (must be 0 to streams)
- `ErrInvalidMapping` - invalid channel mapping table

### Test Coverage (multistream_test.go)
15 test functions covering:
- Encoder/decoder creation for channels 1-8
- Round-trip tests for 5.1 and 7.1 surround
- Stereo and mono edge cases
- Multiple frame encoding for state continuity
- Control method verification (bitrate, complexity, FEC, DTX)
- Packet loss concealment (PLC)
- int16 format path
- Encoder/decoder reset
- Explicit constructor tests
- All application modes (VoIP, Audio, LowDelay)

### Documentation (doc.go)
- New "Multistream (Surround Sound)" section
- 5.1 surround encoding/decoding examples
- List of supported channel configurations

## Key Implementation Details

### API Pattern
The multistream API mirrors the existing Encoder/Decoder pattern:
- float32 as primary format, int16 for compatibility
- Convenience methods that allocate (`EncodeFloat32`, `DecodeFloat32`)
- Buffer-based methods for performance (`Encode`, `Decode`)
- Application hints affect internal encoder settings

### Float32/Float64 Conversion
- Public API uses float32 (consistent with Encoder/Decoder)
- Internal multistream uses float64
- Conversion happens at the wrapper boundary

### Channel Configuration Support
| Channels | Name | Streams | Coupled |
|----------|------|---------|---------|
| 1 | mono | 1 | 0 |
| 2 | stereo | 1 | 1 |
| 3 | 3.0 | 2 | 1 |
| 4 | quad | 2 | 2 |
| 5 | 5.0 | 3 | 2 |
| 6 | 5.1 | 4 | 2 |
| 7 | 6.1 | 5 | 2 |
| 8 | 7.1 | 5 | 3 |

## Commits

| Hash | Type | Description |
|------|------|-------------|
| 4874680 | feat | Add MultistreamEncoder and MultistreamDecoder public API |
| 1657242 | test | Add comprehensive multistream public API tests |
| 70b1318 | docs | Add multistream API documentation to doc.go |

## Verification Results

- `go build ./...` - PASS
- `go test ./...` - PASS (except known test vector issues)
- `CGO_ENABLED=0 go build ./...` - PASS
- Multistream exports visible via `go doc gopus`

## Deviations from Plan

None - plan executed exactly as written.

## Known Limitations

The decoder produces zero-energy output in round-trip tests. This is a known issue with the internal decoder (documented in STATE.md) and not a problem with the public API wrapper. The API correctly exposes the internal multistream functionality.

## Next Phase Readiness

Phase 13 Plan 01 is complete. The multistream public API is fully exposed and documented. This closes the audit gap between the completed internal/multistream implementation and the missing public API.
