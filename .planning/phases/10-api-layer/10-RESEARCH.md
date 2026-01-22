# Phase 10: API Layer - Research

**Researched:** 2026-01-22
**Domain:** Production-ready Go API design for Opus codec
**Confidence:** HIGH

## Summary

Phase 10 implements the public API layer for gopus, wrapping the existing internal encoder and decoder implementations with production-ready interfaces. The API must support frame-based encoding/decoding (Encoder.Encode/Decoder.Decode), streaming wrappers (io.Reader/io.Writer), and both int16 and float32 PCM sample formats.

Research analyzed popular Go audio codec APIs including pion/opus, hraban/opus, go-audio/audio, and go-audio/wav. The established pattern uses struct-based types with constructor functions returning pointer receivers, caller-provided output buffers (for allocation control), and clear method signatures. The API should live in the root `gopus` package to provide clean import paths while keeping implementations in `internal/`.

**Primary recommendation:** Implement a clean public API in the root `gopus` package that wraps internal implementations, following the pion/opus and hraban/opus API conventions. Use caller-provided buffers for decode, float32 as the primary format with int16 wrappers, and provide io.Reader/io.Writer streaming wrappers with internal frame buffering.

## Standard Stack

The established libraries/tools for this domain:

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| Go standard library | 1.25+ | All functionality | Zero dependencies requirement |
| internal/encoder | Phase 8 | Unified encoder | Already implemented |
| internal/hybrid | Phase 4 | Unified decoder | Already implemented |
| internal/multistream | Phase 5/9 | Surround encode/decode | Already implemented |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `io` | stdlib | Reader/Writer interfaces | Streaming API |
| `sync` | stdlib | Mutex for thread safety | Concurrent access patterns |
| `errors` | stdlib | Error types | Public error definitions |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Root package API | Separate `opus` subpackage | Would require `gopus/opus` import path |
| Caller-provided buffers | Return new slices | More allocations, GC pressure |
| float32 primary | int16 primary | float32 is internal format, avoids double conversion |

**Installation:**
```bash
# No external dependencies - pure stdlib
go get gopus
```

## Architecture Patterns

### Recommended Project Structure
```
gopus/
├── doc.go           # Package documentation
├── packet.go        # TOC parsing (existing)
├── decoder.go       # NEW: Public Decoder API
├── encoder.go       # NEW: Public Encoder API
├── stream.go        # NEW: io.Reader/Writer wrappers
├── errors.go        # NEW: Public error types
├── convert.go       # NEW: Sample format conversion helpers
├── internal/
│   ├── encoder/     # Unified encoder (Phase 8)
│   ├── hybrid/      # Unified decoder (Phase 4)
│   └── multistream/ # Surround support (Phase 5/9)
```

### Pattern 1: Struct-Based Encoder/Decoder with Options
**What:** Encoder/Decoder types with constructor functions and configuration methods
**When to use:** All public API types
**Source:** pion/opus, hraban/opus

```go
// Source: hraban/opus API pattern
type Encoder struct {
    enc *encoder.Encoder // internal implementation

    sampleRate int
    channels   int
    frameSize  int
}

// NewEncoder creates a new Opus encoder.
// sampleRate must be one of: 8000, 12000, 16000, 24000, 48000
// channels must be 1 (mono) or 2 (stereo)
// application hints the encoder for optimization:
//   - ApplicationVoIP: optimize for speech
//   - ApplicationAudio: optimize for music
//   - ApplicationLowDelay: minimize latency
func NewEncoder(sampleRate, channels int, application Application) (*Encoder, error) {
    if !validSampleRate(sampleRate) {
        return nil, ErrInvalidSampleRate
    }
    if channels < 1 || channels > 2 {
        return nil, ErrInvalidChannels
    }

    enc := &Encoder{
        enc:        encoder.NewEncoder(sampleRate, channels),
        sampleRate: sampleRate,
        channels:   channels,
        frameSize:  960, // Default 20ms at 48kHz
    }
    enc.applyApplication(application)
    return enc, nil
}
```

### Pattern 2: Caller-Provided Output Buffers
**What:** Decode methods accept output buffer, return samples written
**When to use:** All decode operations for allocation control
**Source:** hraban/opus, pion/opus

```go
// Source: hraban/opus Decoder.Decode pattern
// Decode decodes an Opus packet into PCM samples.
// data: encoded Opus packet
// pcm: output buffer for decoded samples (length must accommodate frame)
// Returns number of samples per channel decoded, or error.
//
// Buffer sizing: For 20ms frames at 48kHz stereo, pcm must have
// at least 960*2 = 1920 elements.
func (d *Decoder) Decode(data []byte, pcm []float32) (int, error) {
    // Validate output buffer size
    maxSamples := d.frameSize * d.channels
    if len(pcm) < maxSamples {
        return 0, ErrBufferTooSmall
    }

    // Decode into internal float64 buffer
    samples, err := d.dec.Decode(data, d.frameSize)
    if err != nil {
        return 0, wrapError(err)
    }

    // Convert to float32 output
    for i, v := range samples {
        if i >= len(pcm) {
            break
        }
        pcm[i] = float32(v)
    }

    return d.frameSize, nil
}
```

### Pattern 3: io.Reader/Writer with Frame Buffering
**What:** Streaming wrappers that handle frame boundaries internally
**When to use:** Streaming encode/decode applications
**Source:** go-audio/wav, audio-io patterns

```go
// Source: go-audio/wav streaming pattern adapted for Opus
// Reader wraps a packet source to provide io.Reader interface.
// Internally buffers decoded PCM for byte-oriented reads.
type Reader struct {
    dec    *Decoder
    source PacketSource  // Interface to get next packet

    buf    []byte        // Decoded PCM buffer (as bytes)
    offset int           // Current read position in buf
}

// PacketSource provides Opus packets for streaming decode.
type PacketSource interface {
    // NextPacket returns the next Opus packet, or io.EOF when done.
    NextPacket() ([]byte, error)
}

// Read implements io.Reader. Returns decoded PCM as bytes.
// The output format is determined by the decoder configuration:
// - For int16: 2 bytes per sample, little-endian
// - For float32: 4 bytes per sample, little-endian
func (r *Reader) Read(p []byte) (n int, err error) {
    // If buffer exhausted, decode next frame
    if r.offset >= len(r.buf) {
        packet, err := r.source.NextPacket()
        if err == io.EOF {
            return 0, io.EOF
        }
        if err != nil {
            return 0, err
        }

        // Decode packet into PCM buffer
        if err := r.decodePacket(packet); err != nil {
            return 0, err
        }
        r.offset = 0
    }

    // Copy available bytes to output
    n = copy(p, r.buf[r.offset:])
    r.offset += n
    return n, nil
}
```

### Pattern 4: Configuration via Setter Methods
**What:** Configuration through individual Set* methods that return errors
**When to use:** All encoder/decoder configuration
**Source:** hraban/opus

```go
// Source: hraban/opus configuration pattern
// SetBitrate sets the target bitrate in bits per second.
// Valid range is 6000 to 510000 (6 kbps to 510 kbps).
// Returns ErrInvalidBitrate if out of range.
func (e *Encoder) SetBitrate(bitrate int) error {
    if bitrate < 6000 || bitrate > 510000 {
        return ErrInvalidBitrate
    }
    e.enc.SetBitrate(bitrate)
    return nil
}

// SetComplexity sets the encoder's computational complexity.
// complexity must be 0-10, where 10 is highest quality/slowest.
// Returns ErrInvalidComplexity if out of range.
func (e *Encoder) SetComplexity(complexity int) error {
    if complexity < 0 || complexity > 10 {
        return ErrInvalidComplexity
    }
    e.enc.SetComplexity(complexity)
    return nil
}

// SetFEC enables or disables in-band Forward Error Correction.
func (e *Encoder) SetFEC(enabled bool) {
    e.enc.SetFEC(enabled)
}

// SetDTX enables or disables Discontinuous Transmission.
// When enabled, reduces bitrate during silence.
func (e *Encoder) SetDTX(enabled bool) {
    e.enc.SetDTX(enabled)
}
```

### Anti-Patterns to Avoid
- **Global decoder state:** Each Decoder must be independent for concurrent use
- **Hidden allocations:** Make buffer requirements explicit in docs
- **Implicit frame sizes:** Always validate input length matches expected frame size
- **Leaking internal types:** Never expose internal/encoder or internal/hybrid types

## Don't Hand-Roll

Problems that look simple but have existing solutions:

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| int16 <-> float32 conversion | Simple multiply/divide | Established scaling with clipping | Edge cases at boundaries |
| Frame size validation | Per-call checks | Config lookup table | Opus has complex valid sizes per mode |
| Error wrapping | String concatenation | fmt.Errorf with %w | Enables errors.Is/As |
| Mutex patterns | Custom locking | sync.Mutex with defer | Avoid deadlocks, ensure unlock |
| Buffer pooling | Custom pool | sync.Pool | Well-tested, handles sizing |

**Key insight:** Audio APIs seem simple but have subtle correctness requirements around buffer sizing, sample conversion, and error handling that are easy to get wrong.

## Common Pitfalls

### Pitfall 1: Sample Conversion Scaling Factor
**What goes wrong:** Using 32767 vs 32768 as conversion factor causes asymmetric clipping
**Why it happens:** +1.0 float maps to 32768 which overflows int16 (max 32767)
**How to avoid:**
- For float to int16: multiply by 32767, clamp to [-32768, 32767]
- For int16 to float: divide by 32768 to maintain range
**Warning signs:** Clipping on full-scale positive signals

```go
// CORRECT: float32 to int16 with proper clamping
func float32ToInt16(f float32) int16 {
    scaled := f * 32767.0
    if scaled > 32767 {
        return 32767
    }
    if scaled < -32768 {
        return -32768
    }
    return int16(scaled)
}

// CORRECT: int16 to float32 preserving range
func int16ToFloat32(i int16) float32 {
    return float32(i) / 32768.0
}
```

### Pitfall 2: Insufficient Output Buffer
**What goes wrong:** Caller provides buffer smaller than decoded frame
**Why it happens:** Frame size varies by mode (120 to 2880 samples at 48kHz)
**How to avoid:**
- Document maximum buffer size needed (2880 * channels for 60ms frame)
- Return ErrBufferTooSmall with clear message
- Provide helper function to calculate buffer size
**Warning signs:** Truncated audio, silent gaps

### Pitfall 3: Thread Safety Assumptions
**What goes wrong:** Concurrent Decode/Encode calls corrupt internal state
**Why it happens:** Decoder/Encoder maintain frame-to-frame state
**How to avoid:**
- Document that types are NOT safe for concurrent use
- Each goroutine should have its own Encoder/Decoder instance
- Or provide synchronized wrapper if needed
**Warning signs:** Garbled audio, crashes under concurrent load

### Pitfall 4: Frame Boundary Handling in Streams
**What goes wrong:** io.Reader returns partial frames, causing decode failures
**Why it happens:** TCP/io.Reader can return any byte count
**How to avoid:**
- Buffer internally to accumulate complete frames
- For output, buffer decoded PCM and serve byte-by-byte
- Track frame boundaries separately from byte boundaries
**Warning signs:** Random decode errors in streaming applications

### Pitfall 5: Ignoring Packet Loss in Decode
**What goes wrong:** Passing nil/empty data without PLC causes clicks/gaps
**Why it happens:** Caller doesn't understand Opus PLC expectations
**How to avoid:**
- Document that nil data triggers PLC (packet loss concealment)
- Provide separate DecodePLC method for explicit concealment
- Never silently return zeros for missing packets
**Warning signs:** Harsh clicking during packet loss

## Code Examples

Verified patterns based on existing internal code and established libraries:

### Frame-Based Encoder API
```go
// Based on: internal/encoder/encoder.go, hraban/opus
type Encoder struct {
    enc        *encoder.Encoder
    sampleRate int
    channels   int
}

// Encode encodes PCM samples to an Opus packet.
// pcm: input samples (interleaved if stereo), length must be frameSize*channels
// data: output buffer for encoded packet (recommended: 4000 bytes max)
// Returns number of bytes written to data, or error.
func (e *Encoder) Encode(pcm []float32, data []byte) (int, error) {
    if len(pcm) != e.frameSize*e.channels {
        return 0, fmt.Errorf("%w: got %d samples, need %d",
            ErrInvalidFrameSize, len(pcm), e.frameSize*e.channels)
    }

    // Convert to float64 for internal encoder
    pcm64 := make([]float64, len(pcm))
    for i, v := range pcm {
        pcm64[i] = float64(v)
    }

    // Encode
    packet, err := e.enc.Encode(pcm64, e.frameSize)
    if err != nil {
        return 0, err
    }

    // DTX: nil packet means silence suppressed
    if packet == nil {
        return 0, nil
    }

    if len(packet) > len(data) {
        return 0, ErrBufferTooSmall
    }

    copy(data, packet)
    return len(packet), nil
}
```

### Frame-Based Decoder API
```go
// Based on: internal/hybrid/decoder.go, pion/opus
type Decoder struct {
    dec        *hybrid.Decoder
    sampleRate int
    channels   int
}

// Decode decodes an Opus packet into PCM samples.
// data: Opus packet (or nil for PLC/packet loss concealment)
// pcm: output buffer, must be large enough for decoded frame
// fec: if true and data is the packet AFTER a lost one, use FEC recovery
// Returns samples per channel decoded, or error.
func (d *Decoder) Decode(data []byte, pcm []float32, fec bool) (int, error) {
    // Parse packet to determine frame size
    var frameSize int
    if data != nil && len(data) > 0 {
        toc := ParseTOC(data[0])
        frameSize = toc.FrameSize
    } else {
        // For PLC, use last known frame size
        frameSize = d.lastFrameSize
    }

    needed := frameSize * d.channels
    if len(pcm) < needed {
        return 0, fmt.Errorf("%w: buffer %d, need %d",
            ErrBufferTooSmall, len(pcm), needed)
    }

    // Decode (nil data triggers PLC)
    samples, err := d.dec.DecodeToFloat32(data, frameSize)
    if err != nil {
        return 0, err
    }

    copy(pcm, samples)
    d.lastFrameSize = frameSize

    return frameSize, nil
}
```

### int16 Convenience Wrappers
```go
// EncodeInt16 encodes int16 PCM samples to an Opus packet.
// This is a convenience wrapper that converts int16 to float32.
func (e *Encoder) EncodeInt16(pcm []int16, data []byte) (int, error) {
    pcm32 := make([]float32, len(pcm))
    for i, v := range pcm {
        pcm32[i] = float32(v) / 32768.0
    }
    return e.Encode(pcm32, data)
}

// DecodeInt16 decodes an Opus packet into int16 PCM samples.
// This is a convenience wrapper that converts from float32.
func (d *Decoder) DecodeInt16(data []byte, pcm []int16, fec bool) (int, error) {
    pcm32 := make([]float32, len(pcm))
    n, err := d.Decode(data, pcm32, fec)
    if err != nil {
        return 0, err
    }

    // Convert float32 -> int16 with clamping
    for i := 0; i < n*d.channels; i++ {
        scaled := pcm32[i] * 32767.0
        if scaled > 32767 {
            pcm[i] = 32767
        } else if scaled < -32768 {
            pcm[i] = -32768
        } else {
            pcm[i] = int16(scaled)
        }
    }

    return n, nil
}
```

### io.Reader Streaming Wrapper
```go
// Reader decodes an Opus stream, implementing io.Reader.
// Output format is float32 little-endian (4 bytes per sample).
type Reader struct {
    dec    *Decoder
    source PacketSource

    pcmBuf []float32  // Decoded PCM frame
    byteBuf []byte    // PCM as bytes for Read()
    offset int        // Current read position
}

// NewReader creates a streaming decoder.
func NewReader(sampleRate, channels int, source PacketSource) (*Reader, error) {
    dec, err := NewDecoder(sampleRate, channels)
    if err != nil {
        return nil, err
    }

    // Pre-allocate for max frame size (60ms at 48kHz)
    maxSamples := 2880 * channels
    return &Reader{
        dec:     dec,
        source:  source,
        pcmBuf:  make([]float32, maxSamples),
        byteBuf: make([]byte, maxSamples*4), // 4 bytes per float32
    }, nil
}

func (r *Reader) Read(p []byte) (n int, err error) {
    for n < len(p) {
        // Refill buffer if needed
        if r.offset >= len(r.byteBuf) {
            packet, err := r.source.NextPacket()
            if err == io.EOF {
                if n > 0 {
                    return n, nil
                }
                return 0, io.EOF
            }
            if err != nil {
                return n, err
            }

            samples, err := r.dec.Decode(packet, r.pcmBuf, false)
            if err != nil {
                return n, err
            }

            // Convert to bytes
            r.byteBuf = r.byteBuf[:samples*r.dec.channels*4]
            for i := 0; i < samples*r.dec.channels; i++ {
                bits := math.Float32bits(r.pcmBuf[i])
                r.byteBuf[i*4+0] = byte(bits)
                r.byteBuf[i*4+1] = byte(bits >> 8)
                r.byteBuf[i*4+2] = byte(bits >> 16)
                r.byteBuf[i*4+3] = byte(bits >> 24)
            }
            r.offset = 0
        }

        // Copy to output
        copied := copy(p[n:], r.byteBuf[r.offset:])
        r.offset += copied
        n += copied
    }
    return n, nil
}
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| cgo wrappers (hraban/opus) | Pure Go (pion/opus, gopus) | 2024 | No C dependencies |
| Single-format APIs | Multi-format (int16/float32) | Standard | Broader compatibility |
| Opaque types | Exposed configuration | Standard | More control |
| Error strings | Error types with errors.Is | Go 1.13 | Better error handling |

**Deprecated/outdated:**
- gopkg.in/hraban/opus.v1: Use v2 or pure Go alternatives
- Global decoder functions: Use instance methods

## Open Questions

Things that couldn't be fully resolved:

1. **Multistream API exposure**
   - What we know: Internal multistream encoder/decoder exist
   - What's unclear: Should public API expose multistream for surround?
   - Recommendation: Expose for completeness, document channel mappings

2. **Application hint effect**
   - What we know: hraban/opus has Application enum (VoIP, Audio, LowDelay)
   - What's unclear: How this maps to internal encoder settings
   - Recommendation: Implement as presets (VoIP=SILK prefer, Audio=CELT prefer)

3. **Resampling responsibility**
   - What we know: Opus natively uses 48kHz internally
   - What's unclear: Should API accept other sample rates or require 48kHz?
   - Recommendation: Accept all valid Opus rates (8k/12k/16k/24k/48k), document

## Sources

### Primary (HIGH confidence)
- pion/opus pkg.go.dev documentation - Decoder API structure
- hraban/opus pkg.go.dev documentation - Encoder/Decoder/Stream API
- Internal gopus code - Existing implementation patterns

### Secondary (MEDIUM confidence)
- go-audio/audio - Buffer interface pattern
- go-audio/wav - io.ReadSeeker/WriteSeeker wrapping
- KVR Audio DSP forum - int16/float32 conversion factors

### Tertiary (LOW confidence)
- Various Medium articles - General io.Reader/Writer patterns for audio

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH - Based on existing internal code and established Go patterns
- Architecture: HIGH - Follows pion/opus and hraban/opus conventions
- Pitfalls: HIGH - Documented issues from real implementations

**Research date:** 2026-01-22
**Valid until:** 2026-03-22 (60 days - stable API patterns)
