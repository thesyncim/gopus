# Phase 5: Multistream Decoder - Research

**Researched:** 2026-01-22
**Domain:** Opus multistream decoding, surround sound, channel mapping
**Confidence:** HIGH

## Summary

The multistream decoder coordinates multiple elementary Opus streams for surround sound configurations. A single multistream packet contains multiple individual Opus packets (one per stream), where each stream is decoded by an independent decoder instance (SILK, CELT, or Hybrid). Streams are categorized as "coupled" (stereo, decoded to 2 channels) or "uncoupled" (mono, decoded to 1 channel). A channel mapping table routes decoded audio to output channels.

The existing gopus implementation already has all the component decoders needed (SILK, CELT, Hybrid). The multistream decoder is primarily a coordinator that:
1. Parses multistream packets to extract individual stream data
2. Manages multiple decoder instances (one per stream)
3. Applies channel mapping to route decoded audio to output channels

**Primary recommendation:** Create a new `internal/multistream` package that wraps existing decoders and implements self-delimiting framing parsing, channel mapping per RFC 7845/Vorbis conventions.

## Standard Stack

The established libraries/tools for this domain:

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| internal/silk | existing | SILK stream decoding | Already implemented in Phase 2 |
| internal/celt | existing | CELT stream decoding | Already implemented in Phase 3 |
| internal/hybrid | existing | Hybrid stream decoding | Already implemented in Phase 4 |
| internal/rangecoding | existing | Entropy decoding | Foundation component |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| internal/plc | existing | Packet loss concealment | Per-stream PLC needed |
| gopus (root) | existing | TOC parsing, packet info | Parse first stream's TOC |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| New multistream package | Extend existing decoders | Clean separation vs. complexity; new package is cleaner |
| Custom channel mapping | Hardcoded Vorbis tables | Flexibility vs. complexity; tables are sufficient for mapping family 0-1 |

**Installation:**
No new external dependencies. Uses existing internal packages.

## Architecture Patterns

### Recommended Project Structure
```
internal/
  multistream/
    decoder.go       # MultistreamDecoder type and creation
    stream.go        # Stream parsing, self-delimiting framing
    channel.go       # Channel mapping and layout handling
    mapping.go       # Predefined Vorbis channel mappings
    multistream.go   # Decode methods (high-level API)
    multistream_test.go
```

### Pattern 1: Coordinator Pattern
**What:** MultistreamDecoder owns and coordinates multiple elementary decoder instances
**When to use:** When one component needs to manage multiple similar sub-components with shared configuration
**Example:**
```go
// Based on existing gopus patterns and RFC 7845 specification
type MultistreamDecoder struct {
    // Configuration from initialization
    sampleRate     int       // 8000, 12000, 16000, 24000, or 48000
    outputChannels int       // Total output channels (max 255)
    streams        int       // Total streams (N)
    coupledStreams int       // Coupled stereo streams (M)
    mapping        []byte    // Channel mapping table

    // Decoder instances - one per stream
    // First M decoders are stereo (coupled), remaining N-M are mono
    decoders []streamDecoder  // Interface wrapping SILK/CELT/Hybrid

    // Per-stream state
    streamStates []streamState
}

// streamDecoder wraps the three decoder types
type streamDecoder interface {
    Decode(data []byte, frameSize int) ([]float64, error)
    Reset()
    Channels() int
}
```

### Pattern 2: Self-Delimiting Frame Parser
**What:** Parse multistream packet by extracting individual stream packets
**When to use:** Reading N-1 self-delimited packets + 1 standard packet
**Example:**
```go
// Based on RFC 6716 Section 3.2.1 and Appendix B
// Self-delimiting: adds packet length after TOC parsing completes
func parseMultistreamPacket(data []byte, numStreams int) ([][]byte, error) {
    packets := make([][]byte, numStreams)
    offset := 0

    // First N-1 packets use self-delimiting framing
    for i := 0; i < numStreams-1; i++ {
        packetLen, consumed, err := parseSelfDelimitedLength(data[offset:])
        if err != nil {
            return nil, err
        }
        offset += consumed
        packets[i] = data[offset : offset+packetLen]
        offset += packetLen
    }

    // Last packet uses remaining data (standard framing)
    packets[numStreams-1] = data[offset:]
    return packets, nil
}

// Parse 1-2 byte length encoding per RFC 6716 Section 3.2.1
func parseFrameLength(data []byte) (length int, bytesConsumed int, err error) {
    if len(data) == 0 {
        return 0, 0, ErrPacketTooShort
    }
    firstByte := int(data[0])
    if firstByte < 252 {
        return firstByte, 1, nil
    }
    if len(data) < 2 {
        return 0, 0, ErrPacketTooShort
    }
    secondByte := int(data[1])
    return secondByte*4 + firstByte, 2, nil
}
```

### Pattern 3: Channel Mapping Application
**What:** Route decoded stream channels to output channels via mapping table
**When to use:** After decoding all streams, before returning output
**Example:**
```go
// Based on RFC 7845 Section 5.1.1 channel mapping
// mapping[j] = i means output channel j gets input from decoded channel i
func applyChannelMapping(
    decodedStreams [][]float64,
    mapping []byte,
    coupledStreams int,
    frameSize int,
    outputChannels int,
) []float64 {
    output := make([]float64, frameSize*outputChannels)

    for outCh := 0; outCh < outputChannels; outCh++ {
        mappingIdx := int(mapping[outCh])

        if mappingIdx == 255 {
            // Silent channel - leave zeros
            continue
        }

        // Determine source stream and channel within stream
        var streamIdx, chanInStream int
        if mappingIdx < 2*coupledStreams {
            // Coupled stream: even = left, odd = right
            streamIdx = mappingIdx / 2
            chanInStream = mappingIdx % 2
        } else {
            // Uncoupled (mono) stream
            streamIdx = coupledStreams + (mappingIdx - 2*coupledStreams)
            chanInStream = 0
        }

        // Copy samples from source to output
        src := decodedStreams[streamIdx]
        srcChannels := streamChannels(streamIdx, coupledStreams)
        for s := 0; s < frameSize; s++ {
            output[s*outputChannels+outCh] = src[s*srcChannels+chanInStream]
        }
    }
    return output
}
```

### Anti-Patterns to Avoid
- **Single monolithic decoder:** Don't merge multistream logic into existing decoders; keep separation clean
- **Copying all decoded data:** Avoid unnecessary copies; decode directly into output buffer regions where possible
- **Assuming stream homogeneity:** Each stream can be different mode (SILK/CELT/Hybrid); dispatcher must check TOC per stream
- **Ignoring timing constraints:** All streams MUST have same frame duration; validate this during parsing

## Don't Hand-Roll

Problems that look simple but have existing solutions:

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Frame length parsing | Custom parser | Existing `parseFrameLength` in packet.go | Already RFC-compliant, tested |
| TOC byte parsing | New implementation | Existing `ParseTOC` in packet.go | Already handles all 32 configs |
| Packet structure parsing | Custom logic | Existing `ParsePacket` in packet.go | Handles all frame codes |
| SILK/CELT/Hybrid dispatch | Mode switch | Look at existing pattern in project | Consistent with Phase 2-4 |
| Channel order tables | Compute dynamically | Hardcoded Vorbis tables | Standard, well-defined |

**Key insight:** The multistream decoder is primarily orchestration code. The hard work (SILK, CELT, Hybrid decoding) is already done in Phases 2-4. The new code should focus on packet demuxing and channel routing.

## Common Pitfalls

### Pitfall 1: Self-Delimiting Framing Confusion
**What goes wrong:** Confusing standard framing (used for last packet) with self-delimiting framing (used for N-1 packets)
**Why it happens:** RFC 6716 Section 3 describes standard framing; Appendix B describes self-delimiting variant
**How to avoid:** Explicitly handle: first N-1 streams use self-delimiting (length prefix added), last stream uses standard (no length prefix, consumes remaining bytes)
**Warning signs:** Last stream decodes incorrectly or with wrong length

### Pitfall 2: Channel Mapping Index Interpretation
**What goes wrong:** Misinterpreting mapping table values, especially boundary between coupled and uncoupled streams
**Why it happens:** The formula `i < 2*coupled_streams` is easy to confuse
**How to avoid:** Clearly document and unit test:
- If `mapping[j] < 2*coupled_streams`: it's from a coupled (stereo) stream
  - Stream index = mapping[j] / 2
  - Channel in stream = mapping[j] % 2 (0=left, 1=right)
- If `mapping[j] >= 2*coupled_streams && mapping[j] != 255`: it's from uncoupled (mono) stream
  - Stream index = coupled_streams + (mapping[j] - 2*coupled_streams)
  - Channel = 0 (mono)
- If `mapping[j] == 255`: silent channel (output zeros)
**Warning signs:** Channels are swapped or contain wrong audio content

### Pitfall 3: Decoder State Independence
**What goes wrong:** Sharing state between stream decoders, causing cross-contamination
**Why it happens:** Trying to optimize memory by reusing decoder instances
**How to avoid:** Create N independent decoder instances (M coupled + (N-M) uncoupled), each with its own state. Each stream decoder must maintain separate state for PLC, LPC history, overlap buffers, etc.
**Warning signs:** Audio artifacts when some streams have packet loss but others don't

### Pitfall 4: Mode Mismatch Per Stream
**What goes wrong:** Assuming all streams use the same Opus mode (SILK/CELT/Hybrid)
**Why it happens:** Convenience assumption that doesn't hold in general case
**How to avoid:** Parse TOC byte of each stream independently; dispatch to correct decoder type per stream
**Warning signs:** Decoding errors on specific streams in mixed-mode multistream

### Pitfall 5: Output Channel Interleaving
**What goes wrong:** Incorrect sample ordering in output buffer
**Why it happens:** Confusion between sample-interleaved and channel-sequential layouts
**How to avoid:** Output should be sample-interleaved: [ch0_s0, ch1_s0, ch2_s0, ..., ch0_s1, ch1_s1, ...]. This matches existing gopus convention for stereo.
**Warning signs:** Audio sounds garbled, rapid channel switching artifacts

## Code Examples

Verified patterns from official sources and existing codebase:

### Vorbis Channel Mapping Tables (Family 1)
```go
// Source: RFC 7845 Section 5.1.1.2, Vorbis I Specification
// These are the standard channel orderings for surround sound

var vorbisChannelMappings = map[int][]string{
    1: {"mono"},
    2: {"left", "right"},
    3: {"left", "center", "right"},
    4: {"front_left", "front_right", "rear_left", "rear_right"},
    5: {"front_left", "center", "front_right", "rear_left", "rear_right"},
    6: {"front_left", "center", "front_right", "rear_left", "rear_right", "LFE"},
    7: {"front_left", "center", "front_right", "side_left", "side_right", "rear_center", "LFE"},
    8: {"front_left", "center", "front_right", "side_left", "side_right", "rear_left", "rear_right", "LFE"},
}

// Default mapping tables for channel mapping family 1
// Returns (streams, coupled_streams, mapping)
func defaultMapping(channels int) (int, int, []byte) {
    switch channels {
    case 1:
        return 1, 0, []byte{0}
    case 2:
        return 1, 1, []byte{0, 1}
    case 3:
        return 2, 1, []byte{0, 2, 1}
    case 4:
        return 2, 2, []byte{0, 1, 2, 3}
    case 5:
        return 3, 2, []byte{0, 4, 1, 2, 3}
    case 6:
        return 4, 2, []byte{0, 4, 1, 2, 3, 5}
    case 7:
        return 5, 2, []byte{0, 4, 1, 2, 3, 5, 6}
    case 8:
        return 5, 3, []byte{0, 6, 1, 2, 3, 4, 5, 7}
    default:
        return 0, 0, nil
    }
}
```

### MultistreamDecoder Creation
```go
// Based on libopus opus_multistream_decoder_create pattern
func NewDecoder(
    sampleRate int,      // 8000, 12000, 16000, 24000, 48000
    channels int,        // Output channel count (1-255)
    streams int,         // Total stream count (N)
    coupledStreams int,  // Coupled (stereo) stream count (M)
    mapping []byte,      // Channel mapping table
) (*Decoder, error) {
    // Validate parameters
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
            return nil, fmt.Errorf("invalid mapping[%d]=%d", i, m)
        }
    }

    // Create decoder instances
    // First M streams are coupled (stereo), remaining are mono
    decoders := make([]streamDecoder, streams)
    for i := 0; i < streams; i++ {
        if i < coupledStreams {
            decoders[i] = newStreamDecoder(sampleRate, 2) // Stereo
        } else {
            decoders[i] = newStreamDecoder(sampleRate, 1) // Mono
        }
    }

    return &Decoder{
        sampleRate:     sampleRate,
        outputChannels: channels,
        streams:        streams,
        coupledStreams: coupledStreams,
        mapping:        mapping,
        decoders:       decoders,
    }, nil
}
```

### Decode Method
```go
// Main decode method following libopus pattern
func (d *Decoder) Decode(data []byte, frameSize int) ([]float64, error) {
    // Handle PLC for nil data
    if data == nil {
        return d.decodePLC(frameSize)
    }

    // Parse multistream packet into individual stream packets
    streamPackets, err := parseMultistreamPacket(data, d.streams)
    if err != nil {
        return nil, err
    }

    // Validate all streams have same duration (from first stream's TOC)
    expectedDuration := getFrameDuration(streamPackets[0])
    for i := 1; i < d.streams; i++ {
        if getFrameDuration(streamPackets[i]) != expectedDuration {
            return nil, ErrDurationMismatch
        }
    }

    // Decode each stream
    decodedStreams := make([][]float64, d.streams)
    for i := 0; i < d.streams; i++ {
        decoded, err := d.decoders[i].Decode(streamPackets[i], frameSize)
        if err != nil {
            return nil, fmt.Errorf("stream %d decode error: %w", i, err)
        }
        decodedStreams[i] = decoded
    }

    // Apply channel mapping
    output := applyChannelMapping(
        decodedStreams,
        d.mapping,
        d.coupledStreams,
        frameSize,
        d.outputChannels,
    )

    return output, nil
}
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Fixed surround layouts | Flexible mapping table | RFC 7845 (2016) | Supports arbitrary channel configurations |
| Single mapping family | Multiple families (0, 1, 255) | RFC 7845 (2016) | Family 255 allows up to 255 channels |

**Deprecated/outdated:**
- None for multistream; the RFC 7845 approach remains current

## Open Questions

Things that couldn't be fully resolved:

1. **Self-delimiting framing exact bytes**
   - What we know: N-1 packets use self-delimiting, last uses standard framing; length encoding uses 1-2 bytes (same formula as frame lengths: <252 = 1 byte, 252-255 = 2 bytes with formula second*4+first)
   - What's unclear: Appendix B of RFC 6716 wasn't fully accessible; exact byte placement after TOC needs verification
   - Recommendation: Implement based on frame length parsing pattern, verify against libopus test vectors

2. **Mixed-mode multistream packets**
   - What we know: Each stream can theoretically be different mode
   - What's unclear: How common are mixed-mode packets in practice? Are there test vectors?
   - Recommendation: Support it by design (parse TOC per stream), but don't prioritize testing exotic combinations

3. **Test vector availability**
   - What we know: Opus test vectors exist for single-stream
   - What's unclear: Are there official multistream test vectors with surround configurations?
   - Recommendation: Generate test vectors using libopus encoder for 5.1 and 7.1 configurations

## Sources

### Primary (HIGH confidence)
- RFC 6716 - Definition of the Opus Audio Codec (https://www.rfc-editor.org/rfc/rfc6716) - Packet framing, frame length encoding
- RFC 7845 - Ogg Encapsulation for the Opus Audio Codec (https://www.rfc-editor.org/rfc/rfc7845.html) - Channel mapping, multistream packet structure
- Opus Multistream API (https://www.opus-codec.org/docs/opus_api-1.1.2/group__opus__multistream.html) - API patterns, parameter constraints
- libopus source (https://github.com/xiph/opus) - Reference implementation patterns

### Secondary (MEDIUM confidence)
- XiphWiki OggOpus (https://wiki.xiph.org/OggOpus) - Usage examples
- libopus opus_multistream_decoder.c - Implementation patterns for parsing and channel mapping

### Tertiary (LOW confidence)
- WebSearch results for self-delimiting framing - Appendix B details inferred from fragments

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH - Uses existing tested decoders
- Architecture: HIGH - Based on RFC specifications and libopus patterns
- Pitfalls: MEDIUM - Based on specification analysis, not production experience
- Self-delimiting framing: MEDIUM - Core concepts clear, exact byte layout needs verification

**Research date:** 2026-01-22
**Valid until:** 2026-02-22 (30 days - stable domain, RFCs don't change)
