# Phase 9: Multistream Encoder - Research

**Researched:** 2026-01-22
**Domain:** Opus multistream encoding, surround sound, channel mapping, bitrate allocation
**Confidence:** HIGH

## Summary

The multistream encoder coordinates multiple elementary Opus encoders (Phase 8) to produce multistream packets for surround sound configurations. Each output packet contains multiple individual Opus packets (one per stream) using self-delimiting framing. Streams are categorized as "coupled" (stereo, encoded with 2-channel joint stereo) or "uncoupled" (mono, encoded independently). The encoder must:

1. Accept multi-channel audio input in Vorbis channel order
2. Route channels to appropriate streams per mapping table (inverse of decoder)
3. Encode each stream independently using Phase 8 unified Encoder
4. Combine stream packets using self-delimiting framing
5. Produce packets decodable by Phase 5 multistream decoder and libopus

The existing multistream decoder (Phase 5) provides the exact blueprint for the encoder - the encoder is essentially the inverse operation: routing input channels to streams, encoding each, and assembling with self-delimiting framing.

**Primary recommendation:** Create `internal/multistream/encoder.go` that wraps multiple Phase 8 Encoders, implements inverse channel mapping, and assembles packets with self-delimiting length encoding. Mirror the decoder's architecture closely.

## Standard Stack

The established libraries/tools for this domain:

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| internal/encoder | Phase 8 | Elementary stream encoding | Unified SILK/Hybrid/CELT encoder |
| internal/multistream | Phase 5 | Mapping tables, framing patterns | Already implemented for decoder |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| internal/rangecoding | existing | Range encoder | For optional FEC per stream |
| gopus (root) | existing | TOC generation | For building stream packets |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| New multistream/encoder.go | Extend decoder.go | Cleaner separation encoder vs decoder |
| Wrap Phase 8 Encoder directly | Create new sub-encoder interface | Direct wrapping simpler, Encoder already handles mono/stereo |
| Complex bitrate allocation | Simple proportional | Simple is sufficient for v1; libopus-style masking deferred |

**Installation:**
No new external dependencies. Uses existing internal packages.

## Architecture Patterns

### Recommended Project Structure
```
internal/
  multistream/
    encoder.go          # NEW: MultistreamEncoder type and creation
    encoder_test.go     # NEW: Encoder tests
    decoder.go          # existing: MultistreamDecoder
    mapping.go          # existing: DefaultMapping, resolveMapping (REUSE)
    stream.go           # existing: parseSelfDelimitedLength (REUSE for encode)
    multistream.go      # existing: Decode methods
    multistream_test.go # existing tests
```

### Pattern 1: Inverse Coordinator Pattern
**What:** MultistreamEncoder owns and coordinates multiple elementary encoder instances, mirroring decoder
**When to use:** When encoder structure should mirror decoder for consistency
**Example:**
```go
// Based on existing decoder pattern from internal/multistream/decoder.go
type Encoder struct {
    // Configuration from initialization (mirrors decoder exactly)
    sampleRate     int       // 8000, 12000, 16000, 24000, or 48000
    inputChannels  int       // Total input channels (max 255)
    streams        int       // Total streams (N)
    coupledStreams int       // Coupled stereo streams (M)
    mapping        []byte    // Channel mapping table

    // Encoder instances - one per stream
    // First M encoders are stereo (coupled), remaining N-M are mono
    encoders []*encoder.Encoder

    // Configuration applied to all streams
    bitrate     int         // Total bitrate, distributed across streams
    application int         // VOIP, AUDIO, or RESTRICTED_LOWDELAY
}
```

### Pattern 2: Self-Delimiting Frame Assembly
**What:** Assemble multistream packet by encoding length prefixes for N-1 streams
**When to use:** Building multistream packets (inverse of parser)
**Example:**
```go
// Inverse of parseMultistreamPacket in stream.go
func assembleMultistreamPacket(streamPackets [][]byte) []byte {
    // Calculate total size
    totalSize := 0
    for i, packet := range streamPackets {
        if i < len(streamPackets)-1 {
            // First N-1 packets need length prefix
            totalSize += selfDelimitedLengthBytes(len(packet))
        }
        totalSize += len(packet)
    }

    output := make([]byte, totalSize)
    offset := 0

    // Write first N-1 packets with self-delimiting framing
    for i := 0; i < len(streamPackets)-1; i++ {
        n := writeSelfDelimitedLength(output[offset:], len(streamPackets[i]))
        offset += n
        copy(output[offset:], streamPackets[i])
        offset += len(streamPackets[i])
    }

    // Last packet uses remaining data (standard framing, no length prefix)
    copy(output[offset:], streamPackets[len(streamPackets)-1])

    return output
}

// Write 1-2 byte length encoding per RFC 6716 Section 3.2.1
func writeSelfDelimitedLength(dst []byte, length int) int {
    if length < 252 {
        dst[0] = byte(length)
        return 1
    }
    // Two-byte encoding: length = 4*secondByte + firstByte
    dst[0] = byte(252 + (length % 4))
    dst[1] = byte((length - 252) / 4)
    return 2
}
```

### Pattern 3: Inverse Channel Routing
**What:** Route input channels to stream encoders (inverse of applyChannelMapping)
**When to use:** Before encoding, to prepare stream input buffers
**Example:**
```go
// Inverse of applyChannelMapping in multistream.go
// Routes interleaved input to individual stream buffers
func routeChannelsToStreams(
    input []float64,
    mapping []byte,
    coupledStreams int,
    frameSize int,
    inputChannels int,
    numStreams int,
) [][]float64 {
    // Create buffers for each stream
    streamBuffers := make([][]float64, numStreams)
    for i := 0; i < numStreams; i++ {
        channels := streamChannels(i, coupledStreams) // 2 for coupled, 1 for uncoupled
        streamBuffers[i] = make([]float64, frameSize*channels)
    }

    // Route input channels to appropriate streams
    for inCh := 0; inCh < inputChannels; inCh++ {
        mappingIdx := mapping[inCh]
        if mappingIdx == 255 {
            continue // Silent channel, skip
        }

        streamIdx, chanInStream := resolveMapping(mappingIdx, coupledStreams)
        srcChannels := streamChannels(streamIdx, coupledStreams)

        // Copy samples from input channel to stream buffer
        for s := 0; s < frameSize; s++ {
            streamBuffers[streamIdx][s*srcChannels+chanInStream] = input[s*inputChannels+inCh]
        }
    }

    return streamBuffers
}
```

### Anti-Patterns to Avoid
- **Duplicating mapping logic:** Reuse existing resolveMapping, streamChannels from mapping.go
- **Custom length encoding:** Reuse pattern from stream.go parseSelfDelimitedLength (inverse)
- **Mode selection per stream:** For v1, use same mode for all streams (consistent with decoder expectation)
- **Ignoring decoder compatibility:** Always test with Phase 5 decoder first

## Don't Hand-Roll

Problems that look simple but have existing solutions:

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Default channel mapping | New function | `DefaultMapping` in mapping.go | Already tested, RFC-compliant |
| Mapping index resolution | New logic | `resolveMapping` in mapping.go | Already handles coupled/uncoupled correctly |
| Stream channel count | Compute fresh | `streamChannels` helper | Already exists in mapping.go |
| TOC byte generation | Custom | `GenerateTOC` in packet.go | Phase 8 already uses this |
| Packet assembly | Custom | `BuildPacket` in encoder/packet.go | Phase 8 already uses this |
| Length encoding format | New implementation | Same formula as parseSelfDelimitedLength | Consistency with decoder |

**Key insight:** The encoder is the inverse of the decoder. Most algorithms already exist - we just need to run them in reverse order.

## Common Pitfalls

### Pitfall 1: Self-Delimiting Framing Order
**What goes wrong:** Putting length prefix on wrong packets (last instead of first N-1)
**Why it happens:** Confusion about which packets get the prefix
**How to avoid:** First N-1 packets get length prefix, last packet uses remaining bytes (same as decoder expects)
**Warning signs:** Decoder fails to parse first stream correctly

### Pitfall 2: Channel Routing Inversion
**What goes wrong:** Using decoder's channel routing logic directly instead of inverting it
**Why it happens:** Copy-paste from decoder without understanding direction
**How to avoid:** Decoder: decodedStreams[streamIdx] -> output[outCh]. Encoder: input[inCh] -> streamBuffers[streamIdx]
**Warning signs:** Channels swapped in decoded output

### Pitfall 3: Inconsistent Frame Durations
**What goes wrong:** Different streams encoded with different frame sizes
**Why it happens:** Not synchronizing configuration across encoders
**How to avoid:** All stream encoders MUST use same frameSize (decoder validates this: validateStreamDurations)
**Warning signs:** ErrDurationMismatch from decoder

### Pitfall 4: Bitrate Distribution
**What goes wrong:** All streams get same total bitrate instead of per-stream allocation
**Why it happens:** Misunderstanding of bitrate parameter scope
**How to avoid:** Total bitrate should be distributed: ~96 kbps per coupled pair, ~64 kbps per mono stream (libopus default)
**Warning signs:** Poor quality on some channels, excellent on others

### Pitfall 5: Encoder State Independence
**What goes wrong:** Encoders share state or get reset at wrong times
**Why it happens:** Trying to optimize memory
**How to avoid:** Each stream encoder is independent with its own state. Reset all encoders together when Reset() called.
**Warning signs:** Audio artifacts, especially when one stream has very different content

### Pitfall 6: Sample Interleaving Direction
**What goes wrong:** Processing input as channel-sequential instead of sample-interleaved
**Why it happens:** Confusion about data layout
**How to avoid:** Input is sample-interleaved [ch0_s0, ch1_s0, ..., chN_s0, ch0_s1, ...] matching decoder output
**Warning signs:** Garbled audio, channels mixed together

## Code Examples

Verified patterns from existing codebase and official sources:

### MultistreamEncoder Creation
```go
// Based on decoder pattern in internal/multistream/decoder.go
// API mirrors decoder: NewEncoder(sampleRate, channels, streams, coupledStreams, mapping)
func NewEncoder(
    sampleRate int,      // 8000, 12000, 16000, 24000, 48000
    channels int,        // Input channel count (1-255)
    streams int,         // Total stream count (N)
    coupledStreams int,  // Coupled (stereo) stream count (M)
    mapping []byte,      // Channel mapping table
) (*Encoder, error) {
    // Validation exactly mirrors decoder
    if channels < 1 || channels > 255 {
        return nil, ErrInvalidChannels
    }
    if streams < 1 || streams > 255 {
        return nil, ErrInvalidStreams
    }
    if coupledStreams < 0 || coupledStreams > streams {
        return nil, ErrInvalidCoupledStreams
    }
    if streams+coupledStreams > 255 {
        return nil, ErrTooManyChannels
    }
    if len(mapping) != channels {
        return nil, ErrInvalidMapping
    }

    // Validate mapping values
    for i, m := range mapping {
        if m != 255 && int(m) >= streams+coupledStreams {
            return nil, fmt.Errorf("%w: mapping[%d]=%d exceeds maximum %d",
                ErrInvalidMapping, i, m, streams+coupledStreams-1)
        }
    }

    // Create encoder instances
    // First M encoders are stereo (coupled), remaining N-M are mono
    encoders := make([]*encoder.Encoder, streams)
    for i := 0; i < streams; i++ {
        var chans int
        if i < coupledStreams {
            chans = 2 // Coupled = stereo
        } else {
            chans = 1 // Uncoupled = mono
        }
        encoders[i] = encoder.NewEncoder(sampleRate, chans)
    }

    return &Encoder{
        sampleRate:     sampleRate,
        inputChannels:  channels,
        streams:        streams,
        coupledStreams: coupledStreams,
        mapping:        mapping,
        encoders:       encoders,
    }, nil
}
```

### Encode Method
```go
// Main encode method - inverse of decoder's Decode method
func (e *Encoder) Encode(pcm []float64, frameSize int) ([]byte, error) {
    expectedLen := frameSize * e.inputChannels
    if len(pcm) != expectedLen {
        return nil, ErrInvalidInput
    }

    // Route input channels to stream buffers (inverse of channel mapping)
    streamBuffers := routeChannelsToStreams(
        pcm, e.mapping, e.coupledStreams,
        frameSize, e.inputChannels, e.streams,
    )

    // Encode each stream
    streamPackets := make([][]byte, e.streams)
    for i := 0; i < e.streams; i++ {
        packet, err := e.encoders[i].Encode(streamBuffers[i], frameSize)
        if err != nil {
            return nil, fmt.Errorf("stream %d encode error: %w", i, err)
        }
        if packet == nil {
            // DTX returned nil - generate minimal packet
            // This shouldn't happen in multistream but handle gracefully
            packet = []byte{0} // Minimal valid packet
        }
        streamPackets[i] = packet
    }

    // Assemble multistream packet with self-delimiting framing
    return assembleMultistreamPacket(streamPackets), nil
}
```

### Bitrate Distribution
```go
// Based on libopus defaults: 64 kbps per mono, 96 kbps per coupled pair
// Source: https://wiki.xiph.org/Opus_Recommended_Settings
func (e *Encoder) SetBitrate(totalBitrate int) {
    e.bitrate = totalBitrate

    // Calculate per-stream allocation
    // Coupled streams get more bits (stereo benefit)
    totalUnits := e.coupledStreams*3 + (e.streams - e.coupledStreams)*2
    // 3 units for stereo (96 kbps default), 2 units for mono (64 kbps default)

    unitBitrate := totalBitrate / totalUnits

    for i := 0; i < e.streams; i++ {
        if i < e.coupledStreams {
            e.encoders[i].SetBitrate(unitBitrate * 3) // ~1.5x for stereo
        } else {
            e.encoders[i].SetBitrate(unitBitrate * 2)
        }
    }
}
```

### Self-Delimiting Length Encoding
```go
// Write self-delimiting length (inverse of parseSelfDelimitedLength in stream.go)
// Per RFC 6716 Section 3.2.1:
// - If length < 252: single byte
// - If length >= 252: two bytes where length = 4*secondByte + firstByte
func writeSelfDelimitedLength(dst []byte, length int) int {
    if length < 252 {
        dst[0] = byte(length)
        return 1
    }
    // length = 4*secondByte + firstByte
    // firstByte in range [252, 255], so use 252 + (length % 4)
    // secondByte = (length - firstByte) / 4
    dst[0] = byte(252 + (length % 4))
    dst[1] = byte((length - 252) / 4)
    return 2
}

func selfDelimitedLengthBytes(length int) int {
    if length < 252 {
        return 1
    }
    return 2
}
```

### Ogg Opus Container for Multistream (Testing)
```go
// Extended OpusHead for multistream (mapping family 1)
// Source: RFC 7845 Section 5.1.1
func makeOpusHeadMultistream(channels, sampleRate, streams, coupledStreams int, mapping []byte) []byte {
    // Family 0: 19 bytes (mono/stereo only)
    // Family 1: 19 + 2 + channels bytes (surround with mapping table)
    head := make([]byte, 21+len(mapping))
    copy(head[0:8], "OpusHead")
    head[8] = 1 // Version
    head[9] = byte(channels)
    binary.LittleEndian.PutUint16(head[10:12], 312) // Pre-skip
    binary.LittleEndian.PutUint32(head[12:16], uint32(sampleRate))
    binary.LittleEndian.PutUint16(head[16:18], 0) // Output gain
    head[18] = 1 // Channel mapping family 1 (Vorbis)
    head[19] = byte(streams)
    head[20] = byte(coupledStreams)
    copy(head[21:], mapping)
    return head
}
```

## Bitrate Allocation Strategy

Based on [Opus Recommended Settings](https://wiki.xiph.org/Opus_Recommended_Settings) and libopus defaults:

### Default Per-Stream Allocation
| Stream Type | Default Bitrate | Notes |
|-------------|-----------------|-------|
| Coupled (stereo) | 96 kbps | Joint stereo benefits |
| Uncoupled (mono) | 64 kbps | Solo channels |
| LFE (mono) | 32-48 kbps | Low frequency only |

### Recommended Total Bitrates
| Configuration | Minimum | Recommended | High Quality |
|---------------|---------|-------------|--------------|
| 5.1 surround | 128 kbps | 256 kbps | 384 kbps |
| 7.1 surround | 192 kbps | 320 kbps | 450 kbps |

### Simple Allocation Formula (v1)
```
totalUnits = coupledStreams * 3 + monoStreams * 2
bitratePerUnit = totalBitrate / totalUnits
coupledBitrate = bitratePerUnit * 3  (e.g., 96 kbps)
monoBitrate = bitratePerUnit * 2     (e.g., 64 kbps)
```

For advanced use: LFE can get reduced bitrate (low frequency only) - deferred to v2.

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Equal bitrate per stream | Weighted allocation | libopus 1.1 (2014) | Better quality distribution |
| No surround masking | Surround masking analysis | libopus 1.1 | Perceptual optimization |

**v1 Approach:** Simple weighted allocation (above). Surround masking is complex and deferred.

**libopus 1.6 (2025):** Latest release with improved bandwidth extension and DRED, but multistream API unchanged.

## Open Questions

Things that couldn't be fully resolved:

1. **LFE Bitrate Reduction**
   - What we know: LFE channel is low-frequency only (20-120 Hz)
   - What's unclear: Exact bitrate reduction factor (some sources suggest 50%)
   - Recommendation: v1 treats LFE as regular mono stream; optimization deferred

2. **Surround Masking**
   - What we know: libopus 1.1+ has perceptual masking across channels
   - What's unclear: Algorithm complexity, whether needed for basic functionality
   - Recommendation: Skip for v1; simple allocation is sufficient

3. **Mixed-Mode Multistream**
   - What we know: Each stream could theoretically use different mode
   - What's unclear: Is this needed in practice?
   - Recommendation: All streams use same mode for v1 (consistent config)

## Sources

### Primary (HIGH confidence)
- [RFC 6716](https://www.rfc-editor.org/rfc/rfc6716.html) - Opus packet format, self-delimiting framing
- [Opus Multistream API](https://www.opus-codec.org/docs/opus_api-1.1.2/group__opus__multistream.html) - API patterns, parameter constraints
- [RFC 7845](https://www.rfc-editor.org/rfc/rfc7845.html) - Channel mapping, OpusHead format
- Existing codebase: `internal/multistream/` - Decoder patterns to mirror

### Secondary (MEDIUM confidence)
- [Opus Recommended Settings](https://wiki.xiph.org/Opus_Recommended_Settings) - Bitrate recommendations
- [libopus 1.6 Release](https://opus-codec.org/release/stable/2025/12/15/libopus-1_6.html) - Current version info
- Phase 5 RESEARCH.md - Decoder research, highly relevant

### Tertiary (LOW confidence)
- WebSearch results for bitrate allocation - Implementation patterns

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH - Uses existing tested encoders and decoder patterns
- Architecture: HIGH - Inverse of well-understood decoder
- Self-delimiting framing: HIGH - Formula in decoder code, just write instead of read
- Bitrate allocation: MEDIUM - Simple approach documented, advanced deferred
- Pitfalls: MEDIUM - Based on decoder experience and specification analysis

**Research date:** 2026-01-22
**Valid until:** 2026-02-22 (30 days - stable domain, RFCs don't change)
