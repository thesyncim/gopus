---
phase: 09
plan: 02
subsystem: multistream-encoder
tags: [multistream, encoder, surround, encoding, packet-assembly]

dependency-graph:
  requires: ["09-01"]
  provides: ["multistream-encode", "encoder-control-methods"]
  affects: ["10-01"]

tech-stack:
  composition: ["internal/encoder.Encoder"]
  patterns: ["per-stream-encoding", "self-delimiting-framing", "weighted-bitrate"]

key-files:
  modified:
    - internal/multistream/encoder.go
    - internal/multistream/encoder_test.go

decisions:
  - id: "encode-dtx-handling"
    title: "DTX packet handling in Encode"
    choice: "Empty byte slice for DTX-suppressed streams, nil return if all streams silent"
    rationale: "Consistent with RFC 6716 multistream framing"

metrics:
  lines: 1410
  tests: 32
  completed: "2026-01-22"
  duration: "~4 minutes"
---

# Phase 09 Plan 02: Encode Method and Control Methods Summary

Complete Encode() method and encoder control propagation for multistream encoding.

## One-liner

Encode() routes channels to streams, encodes each via Phase 8 encoder, assembles with self-delimiting framing.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Verify self-delimiting length encoding | (verified from 09-01) | internal/multistream/encoder.go |
| 2 | Verify/implement Encode method | 769c25d | internal/multistream/encoder.go |
| 3 | Verify/add control methods | 769c25d | internal/multistream/encoder.go |
| 4 | Add/verify encoding tests | 769c25d | internal/multistream/encoder_test.go |

## Key Implementation Details

### Task 1: Self-delimiting Length Encoding (Verified)

Already implemented in 09-01 and verified:
- `writeSelfDelimitedLength`: 1-byte for <252, 2-byte otherwise
- `selfDelimitedLengthBytes`: calculate prefix size
- `assembleMultistreamPacket`: first N-1 packets get length prefix, last uses remainder
- Round-trip verified with `parseSelfDelimitedLength`

### Task 2: Encode Method

Added `Encode(pcm []float64, frameSize int) ([]byte, error)`:

```go
func (e *Encoder) Encode(pcm []float64, frameSize int) ([]byte, error) {
    // 1. Validate input length
    expectedLen := frameSize * e.inputChannels
    if len(pcm) != expectedLen {
        return nil, fmt.Errorf("%w: got %d samples, expected %d", ErrInvalidInput, ...)
    }

    // 2. Route input channels to stream buffers
    streamBuffers := routeChannelsToStreams(pcm, e.mapping, ...)

    // 3. Encode each stream independently
    streamPackets := make([][]byte, e.streams)
    for i := 0; i < e.streams; i++ {
        packet, err := e.encoders[i].Encode(streamBuffers[i], frameSize)
        // Handle DTX (nil) case
        streamPackets[i] = packet
    }

    // 4. Assemble multistream packet
    return assembleMultistreamPacket(streamPackets), nil
}
```

Also added:
- `ErrInvalidInput` error for input validation
- DTX handling: empty byte slice for suppressed streams, nil return if all silent

### Task 3: Control Methods

All control methods propagate to stream encoders:

| Method | Description |
|--------|-------------|
| `SetComplexity(complexity int)` | Sets complexity 0-10 for all streams |
| `Complexity() int` | Returns complexity from first encoder |
| `SetFEC(enabled bool)` | Enables/disables FEC for all streams |
| `FECEnabled() bool` | Returns FEC state from first encoder |
| `SetPacketLoss(lossPercent int)` | Sets expected loss 0-100 for all streams |
| `PacketLoss() int` | Returns packet loss from first encoder |
| `SetDTX(enabled bool)` | Enables/disables DTX for all streams |
| `DTXEnabled() bool` | Returns DTX state from first encoder |

### Task 4: Encoding Tests

Added 9 new test functions:

| Test | Description | Subtests |
|------|-------------|----------|
| TestEncode_Basic | Stereo encoding produces valid packet | 1 |
| TestEncode_51Surround | 5.1 produces 4-stream packet | 1 |
| TestEncode_71Surround | 7.1 produces 5-stream packet | 1 |
| TestEncode_InputValidation | Wrong length returns ErrInvalidInput | 6 |
| TestSetBitrate_Distribution | Weighted allocation verification | 1 |
| TestEncoderControlMethods | All control methods work | 1 |
| TestEncode_Mono | Mono encoding works | 1 |

Test results show correct encoding:
- Stereo (2ch, 1 stream): 130 bytes
- 5.1 (6ch, 4 streams): 445 bytes (115+116+106+105+framing)
- 7.1 (8ch, 5 streams): 562 bytes (115+116+116+106+105+framing)

## Verification Results

```
go build ./internal/multistream/   # SUCCESS
go test ./internal/multistream/    # PASS (32 test functions, 80+ subtests)
```

All multistream tests pass including:
- Self-delimiting length round-trip with parseSelfDelimitedLength
- Encode produces valid multistream packets
- Control methods propagate to stream encoders

## Deviations from Plan

None - all must_haves were already implemented in 09-01 or added in this plan.

## Files Modified

| File | Lines | Purpose |
|------|-------|---------|
| internal/multistream/encoder.go | 459 | Added Encode, ErrInvalidInput, control methods |
| internal/multistream/encoder_test.go | 951 | Added 9 encoding test functions |

## Success Criteria Met

- [x] Self-delimiting length encoding matches RFC 6716 Section 3.2.1
- [x] Packet assembly follows RFC 6716 Appendix B
- [x] Encode() produces complete multistream packets
- [x] SetBitrate distributes with weighted allocation
- [x] All encoder control methods propagate to stream encoders

## Key Links Verified

| From | To | Via | Pattern |
|------|----|-----|---------|
| internal/multistream/encoder.go (Encode) | internal/encoder/encoder.go (Encode) | per-stream encoding | `e\.encoders\[i\]\.Encode` |
| internal/multistream/encoder.go (assembleMultistreamPacket) | internal/multistream/stream.go (parseSelfDelimitedLength) | inverse encoding | `writeSelfDelimitedLength` |

## Phase 9 Complete

With 09-02 complete, Phase 9 (Multistream Encoder) is finished:
- MultistreamEncoder struct with Phase 8 encoder composition
- Channel routing via inverse of applyChannelMapping
- Self-delimiting framing per RFC 6716 Appendix B
- Complete Encode() method producing valid multistream packets
- All control methods (bitrate, complexity, FEC, DTX) propagate to streams
- 32 test functions, 80+ test cases all passing

Ready for Phase 10: Public API.
