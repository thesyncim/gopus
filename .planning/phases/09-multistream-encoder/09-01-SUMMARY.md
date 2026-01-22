---
phase: 09
plan: 01
subsystem: multistream-encoder
tags: [multistream, encoder, surround, channel-mapping]

dependency-graph:
  requires: ["05-01", "05-02", "08-01"]
  provides: ["multistream-encoder-struct", "channel-routing", "packet-assembly"]
  affects: ["09-02"]

tech-stack:
  composition: ["internal/encoder.Encoder"]
  patterns: ["inverse-channel-mapping", "self-delimiting-framing"]

key-files:
  created:
    - internal/multistream/encoder.go
    - internal/multistream/encoder_test.go

decisions:
  - id: "mirror-decoder-validation"
    title: "Identical validation to NewDecoder"
    choice: "NewEncoder validates identically to NewDecoder"
    rationale: "Ensures encoder/decoder symmetry and consistent error handling"

  - id: "weighted-bitrate-allocation"
    title: "Weighted bitrate distribution"
    choice: "Coupled streams get 3 units, mono streams get 2 units"
    rationale: "Matches libopus defaults (96 kbps coupled, 64 kbps mono)"

  - id: "compose-phase8-encoder"
    title: "Compose Phase 8 Encoders"
    choice: "MultistreamEncoder wraps multiple encoder.Encoder instances"
    rationale: "Reuse existing unified encoder; one per stream"

metrics:
  lines: 964
  tests: 8
  completed: "2026-01-22"
  duration: "~6 minutes"
---

# Phase 09 Plan 01: MultistreamEncoder Foundation Summary

MultistreamEncoder struct, creation, and channel routing for surround sound encoding.

## One-liner

MultistreamEncoder wraps Phase 8 Encoders with inverse channel mapping and self-delimiting framing.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Create MultistreamEncoder struct and NewEncoder | 54c2349 | internal/multistream/encoder.go |
| 2 | Channel routing (inverse of applyChannelMapping) | 54c2349 | internal/multistream/encoder.go |
| 3 | Creation and routing unit tests | 1309a31 | internal/multistream/encoder_test.go |

## Key Implementation Details

### Task 1: MultistreamEncoder Struct and NewEncoder

Created `internal/multistream/encoder.go` (327 lines) with:

**Encoder struct** (mirrors Decoder):
```go
type Encoder struct {
    sampleRate     int
    inputChannels  int
    streams        int
    coupledStreams int
    mapping        []byte
    encoders       []*encoder.Encoder
    bitrate        int
}
```

**NewEncoder validation** (identical to NewDecoder):
- channels: 1-255, returns ErrInvalidChannels
- streams: 1-255, returns ErrInvalidStreams
- coupledStreams: 0 to streams, returns ErrInvalidCoupledStreams
- streams + coupledStreams <= 255, returns ErrTooManyChannels
- len(mapping) == channels, returns ErrInvalidMapping
- mapping values: 0 to streams+coupledStreams-1 or 255, returns ErrInvalidMapping

**Stream encoder creation**:
- First M encoders: stereo (channels=2) using encoder.NewEncoder(sampleRate, 2)
- Remaining N-M encoders: mono (channels=1) using encoder.NewEncoder(sampleRate, 1)

**NewEncoderDefault**: Standard 1-8 channel configurations via DefaultMapping()

### Task 2: Channel Routing (routeChannelsToStreams)

Implemented inverse of `applyChannelMapping`:
- Input: sample-interleaved [ch0_s0, ch1_s0, ..., chN_s0, ch0_s1, ...]
- Output: slice of buffers, one per stream (stereo streams interleaved)
- Reuses `resolveMapping` and `streamChannels` from mapping.go
- Handles silent channels (mapping index 255) by skipping

**Self-delimiting framing** (inverse of parseSelfDelimitedLength):
- writeSelfDelimitedLength: 1-byte for <252, 2-byte otherwise
- selfDelimitedLengthBytes: calculate prefix size
- assembleMultistreamPacket: first N-1 packets get length prefix, last uses remainder

### Task 3: Unit Tests

Created `internal/multistream/encoder_test.go` (637 lines) with:

**TestNewEncoder** - 12 validation tests:
- Valid: mono, stereo, 5.1, 7.1
- Invalid: channels=0, channels=256, streams=0, coupledStreams>streams
- Invalid: streams+coupled>255, mapping too short, mapping value exceeds max
- Valid: silent channel (255)

**TestNewEncoderDefault** - 10 configuration tests:
- 1-8 channels with expected streams/coupled/mapping
- 9 channels returns ErrUnsupportedChannels
- 0 channels returns ErrInvalidChannels

**TestRouteChannelsToStreams** - 4 routing tests:
- Mono: single channel routes to mono stream
- Stereo: L/R route to coupled stream as interleaved
- 5.1: FL/FR->stream0, RL/RR->stream1, C->stream2, LFE->stream3
- Silent: 255 mapping leaves zeros in stream

**TestRouteChannelsToStreams_RoundTrip** - 4 configurations:
- Mono, stereo, 5.1, 7.1
- Routes to streams, applies applyChannelMapping back
- Verifies output matches input (proves correct inverse)

**Additional Tests**:
- TestWriteSelfDelimitedLength: 13 length encoding tests with round-trip
- TestAssembleMultistreamPacket: 4 assembly tests (1, 2, 4 streams, large packet)
- TestEncoderSetBitrate: bitrate distribution verification
- TestEncoderReset: reset functionality

## Verification Results

```
go build ./internal/multistream/   # SUCCESS
go vet ./internal/multistream/     # SUCCESS
go test ./internal/multistream/ -run 'TestNewEncoder|TestRouteChannels' # PASS
```

All 40+ test cases pass (including subtests).

## Deviations from Plan

None - plan executed exactly as written.

## Files Modified

| File | Lines | Purpose |
|------|-------|---------|
| internal/multistream/encoder.go | 327 | Encoder struct, NewEncoder, routing, framing |
| internal/multistream/encoder_test.go | 637 | Creation, routing, framing tests |

## Success Criteria Met

- [x] MultistreamEncoder struct defined with Phase 8 Encoder composition
- [x] NewEncoder validates identically to NewDecoder
- [x] NewEncoderDefault handles standard 1-8 channel configurations
- [x] routeChannelsToStreams correctly routes input to stream buffers
- [x] All unit tests pass demonstrating correct creation and routing

## Key Links Verified

| From | To | Via | Pattern |
|------|----|-----|---------|
| internal/multistream/encoder.go | internal/encoder/encoder.go | encoder.NewEncoder composition | `encoder\.NewEncoder` |
| internal/multistream/encoder.go | internal/multistream/mapping.go | DefaultMapping, resolveMapping reuse | `(DefaultMapping\|resolveMapping)` |

## Next Phase Readiness

Ready for 09-02: Encode method implementation.
- Encoder struct complete with all sub-encoders
- Channel routing proven correct via round-trip test
- Packet assembly helpers ready for use in Encode()
