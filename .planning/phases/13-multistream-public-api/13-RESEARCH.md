# Phase 13: Multistream Public API - Research

**Researched:** 2026-01-23
**Domain:** Go public API design, wrapper pattern, multistream Opus
**Confidence:** HIGH

## Summary

This phase exposes the existing `internal/multistream` encoder and decoder through the public `gopus` package. The internal implementation is already complete, well-tested, and follows the same patterns as the mono/stereo encoder/decoder. The gap is purely one of public API exposure.

The existing public API (`gopus.Encoder`, `gopus.Decoder`) provides the pattern to follow: thin wrappers that validate parameters, manage state, and delegate to internal implementations. The internal multistream package already provides `NewEncoder`, `NewEncoderDefault`, `NewDecoder`, `NewDecoderDefault`, and all necessary methods.

**Primary recommendation:** Create `gopus.MultistreamEncoder` and `gopus.MultistreamDecoder` as thin wrappers around `internal/multistream.Encoder` and `internal/multistream.Decoder`, mirroring the existing `gopus.Encoder`/`gopus.Decoder` pattern exactly.

## Standard Stack

The established libraries/tools for this domain:

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| internal/multistream | current | Multistream encode/decode | Already implemented and tested |
| internal/encoder | current | Single-stream encoding | Used by multistream encoder |
| internal/hybrid | current | Single-stream decoding | Used by multistream decoder |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| internal/types | current | Shared types (Bandwidth) | Application hints if needed |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Thin wrapper | Direct exposure | Thin wrapper preferred for API consistency with Encoder/Decoder |

**Installation:**
No new dependencies required.

## Architecture Patterns

### Recommended Project Structure
```
gopus/
├── decoder.go           # Existing mono/stereo decoder
├── encoder.go           # Existing mono/stereo encoder
├── multistream.go       # NEW: MultistreamEncoder/MultistreamDecoder
├── multistream_test.go  # NEW: Tests for multistream public API
├── errors.go            # Add new multistream errors
└── doc.go               # Update package documentation
```

### Pattern 1: Thin Wrapper with Validation
**What:** Public type wraps internal type, validates on construction, delegates all methods
**When to use:** Exposing internal implementations to public API
**Example:**
```go
// Source: gopus/encoder.go (existing pattern)
type Encoder struct {
    enc         *encoder.Encoder
    sampleRate  int
    channels    int
    frameSize   int
    application Application
}

func NewEncoder(sampleRate, channels int, application Application) (*Encoder, error) {
    if !validSampleRate(sampleRate) {
        return nil, ErrInvalidSampleRate
    }
    if channels < 1 || channels > 2 {
        return nil, ErrInvalidChannels
    }
    // ... delegate to internal
}
```

### Pattern 2: Default Configuration Constructor
**What:** Convenience constructor that uses sensible defaults (Vorbis-style mapping)
**When to use:** Most users want standard 5.1/7.1 configurations
**Example:**
```go
// Source: internal/multistream/decoder.go (existing)
func NewDecoderDefault(sampleRate, channels int) (*Decoder, error) {
    streams, coupledStreams, mapping, err := DefaultMapping(channels)
    if err != nil {
        return nil, err
    }
    return NewDecoder(sampleRate, channels, streams, coupledStreams, mapping)
}
```

### Pattern 3: Convenience Slice-Returning Methods
**What:** Methods that allocate and return slices for ease of use
**When to use:** Alongside buffer-based methods for caller convenience
**Example:**
```go
// Source: gopus/decoder.go (existing pattern)
func (d *Decoder) DecodeFloat32(data []byte) ([]float32, error) {
    // Allocate buffer
    pcm := make([]float32, frameSize*d.channels)
    n, err := d.Decode(data, pcm)
    if err != nil {
        return nil, err
    }
    return pcm[:n*d.channels], nil
}
```

### Anti-Patterns to Avoid
- **Exposing internal types directly:** Use wrapper types to maintain API stability
- **Different method signatures:** MultistreamEncoder should mirror Encoder's API style
- **Inconsistent error types:** Use existing gopus error patterns (ErrInvalidChannels, etc.)

## Don't Hand-Roll

Problems that look simple but have existing solutions:

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Channel mapping | Custom mapping logic | internal/multistream.DefaultMapping | RFC 7845 compliant Vorbis mappings |
| Stream routing | Manual channel routing | internal/multistream routing | Already handles coupled/uncoupled |
| Packet assembly | Custom framing | internal/multistream.assembleMultistreamPacket | Self-delimiting framing per RFC 6716 |
| Stream parsing | Custom parser | internal/multistream.parseMultistreamPacket | Handles length prefixes correctly |

**Key insight:** All multistream complexity is already handled in internal/multistream. The public API phase is purely about exposure, not reimplementation.

## Common Pitfalls

### Pitfall 1: Sample Format Conversion Inconsistency
**What goes wrong:** Internal uses float64, public API should offer float32/int16
**Why it happens:** Internal multistream uses float64 for precision
**How to avoid:** Add conversion methods like existing Encoder/Decoder (DecodeToInt16, DecodeToFloat32)
**Warning signs:** Tests only check float64 path

### Pitfall 2: Error Type Proliferation
**What goes wrong:** Creating new error types instead of reusing gopus errors
**Why it happens:** Internal package has its own errors (ErrInvalidChannels, etc.)
**How to avoid:** Map internal errors to gopus public errors OR expose internal errors
**Warning signs:** User sees errors from "multistream:" instead of "gopus:"

### Pitfall 3: Missing Application Hint for Encoder
**What goes wrong:** Multistream encoder doesn't have Application parameter
**Why it happens:** Internal multistream.Encoder doesn't expose Application
**How to avoid:** Accept Application in NewMultistreamEncoder, propagate to stream encoders
**Warning signs:** No way to set VoIP vs Audio mode on multistream

### Pitfall 4: Forgetting Reset Method
**What goes wrong:** Users can't reset decoder state for new streams
**Why it happens:** Reset() is easy to forget when wrapping
**How to avoid:** Expose Reset() on both MultistreamEncoder and MultistreamDecoder
**Warning signs:** Documentation mentions Reset but it's not available

### Pitfall 5: Channel Count Validation
**What goes wrong:** Accepting >8 channels when DefaultMapping only supports 1-8
**Why it happens:** Full constructor allows 1-255 channels, default only 1-8
**How to avoid:** Document clearly, use NewMultistreamEncoderDefault for standard configs
**Warning signs:** Panic or confusing errors on 9+ channels with Default constructor

## Code Examples

Verified patterns from existing codebase:

### MultistreamEncoder Construction (proposed pattern)
```go
// Pattern from: gopus/encoder.go (adapted for multistream)
type MultistreamEncoder struct {
    enc        *multistream.Encoder
    sampleRate int
    channels   int
    frameSize  int
}

// NewMultistreamEncoder creates encoder with explicit configuration.
func NewMultistreamEncoder(sampleRate, channels, streams, coupledStreams int,
    mapping []byte, application Application) (*MultistreamEncoder, error) {

    if !validSampleRate(sampleRate) {
        return nil, ErrInvalidSampleRate
    }
    // Internal multistream.NewEncoder handles channel/stream validation
    enc, err := multistream.NewEncoder(sampleRate, channels, streams, coupledStreams, mapping)
    if err != nil {
        return nil, err // Consider wrapping with gopus prefix
    }

    // Note: May need to propagate Application to underlying encoders
    return &MultistreamEncoder{
        enc:        enc,
        sampleRate: sampleRate,
        channels:   channels,
        frameSize:  960, // Default 20ms
    }, nil
}

// NewMultistreamEncoderDefault creates encoder with Vorbis-style defaults.
func NewMultistreamEncoderDefault(sampleRate, channels int,
    application Application) (*MultistreamEncoder, error) {

    if !validSampleRate(sampleRate) {
        return nil, ErrInvalidSampleRate
    }
    enc, err := multistream.NewEncoderDefault(sampleRate, channels)
    if err != nil {
        return nil, err
    }
    return &MultistreamEncoder{
        enc:        enc,
        sampleRate: sampleRate,
        channels:   channels,
        frameSize:  960,
    }, nil
}
```

### MultistreamDecoder Construction (proposed pattern)
```go
// Pattern from: gopus/decoder.go (adapted for multistream)
type MultistreamDecoder struct {
    dec           *multistream.Decoder
    sampleRate    int
    channels      int
    lastFrameSize int
}

// NewMultistreamDecoder creates decoder with explicit configuration.
func NewMultistreamDecoder(sampleRate, channels, streams, coupledStreams int,
    mapping []byte) (*MultistreamDecoder, error) {

    if !validSampleRate(sampleRate) {
        return nil, ErrInvalidSampleRate
    }
    dec, err := multistream.NewDecoder(sampleRate, channels, streams, coupledStreams, mapping)
    if err != nil {
        return nil, err
    }
    return &MultistreamDecoder{
        dec:           dec,
        sampleRate:    sampleRate,
        channels:      channels,
        lastFrameSize: 960,
    }, nil
}

// NewMultistreamDecoderDefault creates decoder with Vorbis-style defaults.
func NewMultistreamDecoderDefault(sampleRate, channels int) (*MultistreamDecoder, error) {
    if !validSampleRate(sampleRate) {
        return nil, ErrInvalidSampleRate
    }
    dec, err := multistream.NewDecoderDefault(sampleRate, channels)
    if err != nil {
        return nil, err
    }
    return &MultistreamDecoder{
        dec:           dec,
        sampleRate:    sampleRate,
        channels:      channels,
        lastFrameSize: 960,
    }, nil
}
```

### Encode Method (proposed pattern)
```go
// Pattern from: gopus/encoder.go (adapted for multistream)
func (e *MultistreamEncoder) Encode(pcm []float32, data []byte) (int, error) {
    expected := e.frameSize * e.channels
    if len(pcm) != expected {
        return 0, ErrInvalidFrameSize
    }

    // Convert float32 to float64 (internal format)
    pcm64 := make([]float64, len(pcm))
    for i, v := range pcm {
        pcm64[i] = float64(v)
    }

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

### Decode Method (proposed pattern)
```go
// Pattern from: gopus/decoder.go (adapted for multistream)
func (d *MultistreamDecoder) Decode(data []byte, pcm []float32) (int, error) {
    var frameSize int
    if data != nil && len(data) > 0 {
        // Parse TOC from first stream to get frame size
        // Note: multistream packets have length prefixes, need proper parsing
        toc := ParseTOC(data[0]) // Simplified - actual needs stream parsing
        frameSize = toc.FrameSize
    } else {
        frameSize = d.lastFrameSize
    }

    needed := frameSize * d.channels
    if len(pcm) < needed {
        return 0, ErrBufferTooSmall
    }

    // Internal Decode returns float64
    samples, err := d.dec.Decode(data, frameSize)
    if err != nil {
        return 0, err
    }

    // Convert float64 to float32
    for i, s := range samples {
        if i < len(pcm) {
            pcm[i] = float32(s)
        }
    }

    d.lastFrameSize = frameSize
    return frameSize, nil
}
```

### Round-trip Test (proposed pattern)
```go
// Pattern from: gopus/api_test.go (adapted for multistream)
func TestMultistreamRoundTrip_51(t *testing.T) {
    channels := 6 // 5.1 surround

    enc, err := NewMultistreamEncoderDefault(48000, channels, ApplicationAudio)
    if err != nil {
        t.Fatalf("NewMultistreamEncoderDefault error: %v", err)
    }

    dec, err := NewMultistreamDecoderDefault(48000, channels)
    if err != nil {
        t.Fatalf("NewMultistreamDecoderDefault error: %v", err)
    }

    frameSize := 960
    pcmIn := make([]float32, frameSize*channels)
    // Fill with unique values per channel...

    packet, err := enc.EncodeFloat32(pcmIn)
    if err != nil {
        t.Fatalf("Encode error: %v", err)
    }

    pcmOut, err := dec.DecodeFloat32(packet)
    if err != nil {
        t.Fatalf("Decode error: %v", err)
    }

    // Verify output length and energy
    if len(pcmOut) != frameSize*channels {
        t.Errorf("Output length = %d, want %d", len(pcmOut), frameSize*channels)
    }
}
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Separate stereo/mono APIs | Unified Encoder/Decoder | Phase 10 | Simpler API surface |
| Channels via constructor only | Channels + channel mapping | RFC 7845 | Surround support |

**Deprecated/outdated:**
- None - this is new API exposure

## Open Questions

Things that couldn't be fully resolved:

1. **Application hint propagation**
   - What we know: Internal multistream.Encoder doesn't expose Application
   - What's unclear: Whether Application should affect multistream encoding
   - Recommendation: Accept Application parameter but may need internal changes to propagate

2. **Error wrapping strategy**
   - What we know: Internal has ErrInvalidChannels, gopus has ErrInvalidChannels
   - What's unclear: Are these the same errors or duplicates?
   - Recommendation: Check if internal errors satisfy errors.Is with gopus errors, or re-export

3. **TOC parsing for multistream packets**
   - What we know: Multistream packets have length prefixes before first N-1 streams
   - What's unclear: How to get frame size from multistream packet cleanly
   - Recommendation: Use internal parsing or extract first stream TOC after length parsing

## Sources

### Primary (HIGH confidence)
- gopus/encoder.go - Existing public encoder pattern
- gopus/decoder.go - Existing public decoder pattern
- internal/multistream/encoder.go - Complete internal encoder
- internal/multistream/decoder.go - Complete internal decoder
- internal/multistream/encoder_test.go - Test patterns
- internal/multistream/multistream_test.go - Decoder test patterns

### Secondary (MEDIUM confidence)
- gopus/api_test.go - Round-trip test patterns
- .planning/ROADMAP.md - Phase requirements

### Tertiary (LOW confidence)
- None - all findings from codebase analysis

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH - internal implementation complete and tested
- Architecture: HIGH - pattern clearly established by existing Encoder/Decoder
- Pitfalls: HIGH - identified from existing code patterns

**Research date:** 2026-01-23
**Valid until:** 2026-02-23 (30 days - stable codebase)
