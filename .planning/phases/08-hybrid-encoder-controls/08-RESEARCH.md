# Phase 8: Hybrid Encoder & Controls - Research

**Researched:** 2026-01-22
**Domain:** Opus hybrid mode encoding, encoder controls (VBR/CBR, bitrate, FEC, DTX, complexity)
**Confidence:** HIGH

## Summary

Phase 8 completes the Opus encoder by implementing hybrid mode (SILK + CELT) encoding and all encoder control mechanisms. The hybrid encoder combines the existing SILK encoder (Phase 6) for frequencies 0-8kHz with the CELT encoder (Phase 7) for 8-20kHz, using a shared range coder with sequential data output (SILK first, then CELT). Key implementation areas include: (1) hybrid mode coordination with proper delay alignment, (2) TOC byte generation for all modes, (3) VBR/CBR mode switching with packet size control, (4) bitrate allocation between SILK and CELT layers, (5) in-band FEC using SILK's LBRR mechanism, and (6) DTX for silence detection.

The existing codebase provides excellent foundation: hybrid decoder (Phase 4) demonstrates the frequency split and delay compensation logic that the encoder must mirror, SILK encoder has LBRR/FEC tables already defined, and CELT encoder handles bit allocation. The critical insight is that the encoder must produce bytes in the exact inverse order that the decoder consumes them - SILK data first, CELT data second, from a unified range coder.

**Primary recommendation:** Build a unified `Encoder` type in `internal/encoder/` that orchestrates SILK and CELT encoders, handles mode selection, and implements all control parameters (VBR/CBR/bitrate/FEC/DTX/complexity).

## Standard Stack

The established libraries/tools for this domain:

### Core (Already in Project)
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| internal/silk | Phase 6 | SILK encoding (0-8kHz for hybrid) | Complete, round-trip tested |
| internal/celt | Phase 7 | CELT encoding (8-20kHz for hybrid) | Complete, libopus cross-validated |
| internal/rangecoding | Phase 1 | Shared range coder | Encoder/decoder pair verified |
| internal/hybrid | Phase 4 | Reference for hybrid coordination | Decoder logic to mirror |

### Supporting (New for Phase 8)
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| internal/encoder | New | Unified encoder orchestration | Main encoder entry point |
| packet.go (extend) | Existing | TOC byte generation | Packet framing |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Unified encoder type | Separate SILK/CELT/Hybrid encoders | Unified is cleaner for mode switching |
| Manual bitrate allocation | Auto-allocation based on content | Manual gives more control; start simple |

**Installation:**
```bash
# No new external dependencies - pure Go implementation
```

## Architecture Patterns

### Recommended Project Structure
```
internal/
├── encoder/             # NEW: Unified encoder orchestration
│   ├── encoder.go       # Encoder struct with all controls
│   ├── hybrid.go        # Hybrid mode encoding (SILK + CELT)
│   ├── controls.go      # VBR/CBR, bitrate, complexity
│   ├── fec.go           # In-band FEC (LBRR) encoding
│   ├── dtx.go           # Discontinuous transmission
│   └── encoder_test.go  # Integration tests
├── silk/
│   └── (existing)       # SILK encoder from Phase 6
├── celt/
│   └── (existing)       # CELT encoder from Phase 7
├── hybrid/
│   └── decoder.go       # Reference for coordination logic
└── rangecoding/
    └── (existing)       # Shared range coder
```

### Pattern 1: Unified Encoder with Mode Selection
**What:** Single encoder type that can operate in SILK, CELT, or Hybrid mode based on configuration and bitrate
**When to use:** Always - this matches libopus architecture
**Example:**
```go
// Source: RFC 6716 Section 3.2
type Encoder struct {
    // Sub-encoders
    silkEncoder *silk.Encoder
    celtEncoder *celt.Encoder

    // Configuration
    mode         Mode       // ModeSILK, ModeHybrid, ModeCELT
    bandwidth    Bandwidth  // NB, MB, WB, SWB, FB
    sampleRate   int        // 8000-48000
    channels     int        // 1 or 2
    frameSize    int        // In samples at 48kHz

    // Controls
    vbr          bool       // VBR mode (default true)
    bitrate      int        // Target bits per second
    complexity   int        // 0-10
    fecEnabled   bool       // In-band FEC
    dtxEnabled   bool       // Discontinuous transmission
    packetLoss   int        // Expected packet loss %
}
```

### Pattern 2: Shared Range Coder for Hybrid Mode
**What:** SILK and CELT share one range encoder, writing data sequentially
**When to use:** Hybrid mode encoding
**Example:**
```go
// Source: RFC 6716 Section 3.2.1
func (e *Encoder) encodeHybridFrame(pcm []float64, frameSize int) ([]byte, error) {
    // Initialize shared range encoder
    buf := make([]byte, e.maxPacketSize())
    re := &rangecoding.Encoder{}
    re.Init(buf)

    // Step 1: SILK encodes first (low frequencies, 0-8kHz)
    // SILK operates at 16kHz (WB) for hybrid mode
    silkSamples := resample48to16(pcm)
    e.silkEncoder.SetRangeEncoder(re)
    e.silkEncoder.EncodeFrame(silkSamples, vadFlag)

    // Step 2: CELT encodes second (high frequencies, 8-20kHz)
    // CELT gets remaining bits after SILK
    e.celtEncoder.SetRangeEncoder(re)
    celtBits := e.computeCELTBudget(re.Tell())
    e.celtEncoder.EncodeFrameHybrid(pcm, frameSize, celtBits)

    // Finalize
    return re.Done(), nil
}
```

### Pattern 3: TOC Byte Generation
**What:** Generate TOC byte from encoding configuration
**When to use:** Every encoded packet
**Example:**
```go
// Source: RFC 6716 Section 3.1
func (e *Encoder) generateTOC(frameCode int) byte {
    // Config = mode/bandwidth/frameSize combination (0-31)
    config := e.computeConfig()

    // TOC = config << 3 | stereo << 2 | frameCode
    var toc byte
    toc = byte(config << 3)
    if e.channels == 2 {
        toc |= 0x04 // Stereo bit
    }
    toc |= byte(frameCode & 0x03)
    return toc
}

func (e *Encoder) computeConfig() int {
    // Maps to configTable in packet.go
    switch e.mode {
    case ModeHybrid:
        if e.bandwidth == BandwidthSuperwideband {
            if e.frameSize == 480 { return 12 } // 10ms
            return 13 // 20ms
        }
        // Fullband
        if e.frameSize == 480 { return 14 }
        return 15
    // ... SILK configs 0-11, CELT configs 16-31
    }
}
```

### Pattern 4: VBR vs CBR Encoding
**What:** Control packet size variability
**When to use:** Based on application requirements
**Example:**
```go
// Source: RFC 6716 Section 2.1.9
func (e *Encoder) encodeFrame(pcm []float64) ([]byte, error) {
    if e.vbr {
        // VBR: Let each layer use natural bit count
        // SILK is inherently VBR, CELT adapts to fill remaining budget
        return e.encodeVBR(pcm)
    }

    // CBR: Force exact packet size
    // SILK produces variable output, CELT fills remainder
    targetBytes := e.bitrate * e.frameDurationMs() / 8000
    return e.encodeCBR(pcm, targetBytes)
}

func (e *Encoder) encodeCBR(pcm []float64, targetBytes int) ([]byte, error) {
    // SILK encodes first (variable)
    silkBytes := e.encodeSILK(pcm)

    // CELT fills to exact target
    celtBudget := (targetBytes - len(silkBytes)) * 8
    celtBytes := e.encodeCELT(pcm, celtBudget)

    // Pad if needed to hit exact target
    return padToSize(append(silkBytes, celtBytes...), targetBytes)
}
```

### Pattern 5: In-Band FEC (LBRR)
**What:** Encode redundant low-bitrate data for loss recovery
**When to use:** When FEC enabled and packet loss expected
**Example:**
```go
// Source: RFC 6716 Section 4.2.4
func (e *Encoder) encodeFEC(currentFrame, previousFrame []float32) {
    if !e.fecEnabled || e.packetLoss == 0 {
        return
    }

    // Encode previous frame at low bitrate (LBRR)
    // LBRR data appears BEFORE regular SILK frames in bitstream
    lbrrData := e.encodeLBRR(previousFrame)

    // Write LBRR flag
    e.rangeEncoder.EncodeICDF16(1, silk.ICDFLBRRFlag, 8)

    // Write LBRR frame
    e.writeLBRRFrame(lbrrData)
}
```

### Anti-Patterns to Avoid
- **Separate range coders for SILK/CELT:** RFC 6716 requires shared coder
- **CELT before SILK in hybrid:** Order matters - SILK data comes first
- **Ignoring delay compensation:** 2.7ms CELT delay required for alignment
- **Hard CBR with SILK:** Causes quality degradation; SILK is inherently VBR

## Don't Hand-Roll

Problems that look simple but have existing solutions:

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Mode selection | Manual thresholds | libopus decision logic | Tuned over years of testing |
| Bitrate allocation | 50/50 split | Content-adaptive allocation | SILK needs vary by speech type |
| FEC trigger | Simple loss check | Rate-distortion optimization | Complex trade-off |
| DTX detection | Energy threshold | Existing VAD | Already in silk.classifyFrame |
| TOC config mapping | Manual table | configTable from packet.go | Already implemented |

**Key insight:** The project already has significant infrastructure (SILK VAD, CELT allocation, hybrid decoder). Phase 8 orchestrates these rather than rebuilding.

## Common Pitfalls

### Pitfall 1: Wrong SILK/CELT Encoding Order
**What goes wrong:** Decoder expects SILK data first; reversed order causes decode failure
**Why it happens:** Intuition suggests encoding high-quality layer first
**How to avoid:** Follow RFC 6716 Section 3.2.1 exactly - SILK first, CELT second
**Warning signs:** libopus decoder rejects packets, corrupt audio

### Pitfall 2: Missing Delay Compensation in Encoder
**What goes wrong:** SILK and CELT not time-aligned in output
**Why it happens:** Encoders have different lookahead (SILK: 5ms, CELT: 2.5ms)
**How to avoid:** Delay CELT input by 2.7ms (130 samples at 48kHz) relative to SILK
**Warning signs:** Phasing artifacts when decoding hybrid packets

### Pitfall 3: Incorrect Hybrid Band Configuration
**What goes wrong:** Frequency overlap or gap between SILK and CELT
**Why it happens:** Misconfigured band boundaries
**How to avoid:**
- SILK: WB mode (16kHz), covers 0-8kHz
- CELT: Hybrid mode, bands 17-21 only (8-20kHz)
- Follow existing hybrid.HybridCELTStartBand = 17
**Warning signs:** Audible artifacts at 8kHz crossover

### Pitfall 4: CBR Mode Causing Quality Loss
**What goes wrong:** Forcing SILK to exact bitrate degrades quality
**Why it happens:** SILK is inherently VBR; forcing CBR requires "bit shaving"
**How to avoid:**
- Use constrained VBR (CVBR) when possible
- In hard CBR, let SILK be VBR and use CELT to fill remaining bytes
- Accept CELT-only at very low bitrates rather than degraded hybrid
**Warning signs:** Speech artifacts, consonant loss

### Pitfall 5: FEC Bitrate Overhead
**What goes wrong:** FEC uses too many bits, reducing primary stream quality
**Why it happens:** LBRR encoding adds 20-30% overhead
**How to avoid:**
- Only enable FEC when packetLoss > 0%
- Scale FEC bitrate with expected loss
- At high loss (>50%), may be better to increase primary bitrate
**Warning signs:** Lower quality at same bitrate with FEC enabled

### Pitfall 6: DTX Transition Artifacts
**What goes wrong:** Audible clicks when switching to/from DTX silence
**Why it happens:** Abrupt state changes without fade
**How to avoid:**
- Encode "comfort noise" frames, not total silence
- Apply fade in/out over 5-10ms
- Maintain filter state during DTX
**Warning signs:** Click sounds at speech onset/offset

## Code Examples

Verified patterns from official sources:

### Hybrid Frame Encoding (Core Pattern)
```go
// Source: RFC 6716 Section 3.2.1
// This mirrors the decode logic in internal/hybrid/decoder.go

func (e *HybridEncoder) EncodeFrame(pcm []float64, frameSize int) ([]byte, error) {
    // Validate hybrid frame sizes (10ms or 20ms only)
    if frameSize != 480 && frameSize != 960 {
        return nil, ErrInvalidFrameSize
    }

    // Initialize shared range encoder
    maxBytes := e.computeMaxPacketSize()
    buf := make([]byte, maxBytes)
    re := &rangecoding.Encoder{}
    re.Init(buf)

    // Step 1: Downsample to 16kHz for SILK (WB mode)
    // SILK handles 0-8kHz
    silkInput := downsample48to16(pcm)
    silkFrameSize := frameSize / 3  // 160 or 320 samples

    // Step 2: SILK encodes first
    e.silkEncoder.SetRangeEncoder(re)
    silkDuration := silk.Frame10ms
    if frameSize == 960 {
        silkDuration = silk.Frame20ms
    }
    vadFlag := e.silkEncoder.classifyFrame(silkInput)
    e.silkEncoder.EncodeFrame(silkInput, vadFlag)
    silkBitsUsed := re.Tell()

    // Step 3: Apply CELT input delay (2.7ms = 130 samples at 48kHz)
    // This aligns CELT with SILK's longer lookahead
    celtInput := e.applyInputDelay(pcm, 130)

    // Step 4: CELT encodes high frequencies (8-20kHz, bands 17-21)
    e.celtEncoder.SetRangeEncoder(re)
    celtBudget := (maxBytes * 8) - silkBitsUsed
    e.celtEncoder.EncodeFrameHybrid(celtInput, frameSize, celtBudget)

    // Finalize and return
    return re.Done(), nil
}
```

### TOC Byte and Packet Assembly
```go
// Source: RFC 6716 Section 3.1 Table

// Config indices for hybrid mode
const (
    ConfigHybridSWB10ms = 12  // Hybrid SWB 10ms
    ConfigHybridSWB20ms = 13  // Hybrid SWB 20ms
    ConfigHybridFB10ms  = 14  // Hybrid FB 10ms
    ConfigHybridFB20ms  = 15  // Hybrid FB 20ms
)

func (e *Encoder) BuildPacket(frameData []byte) []byte {
    // Generate TOC byte
    config := e.computeConfig()
    stereo := 0
    if e.channels == 2 {
        stereo = 1
    }
    frameCode := 0 // Single frame (code 0)

    toc := byte((config << 3) | (stereo << 2) | frameCode)

    // Assemble packet: TOC + frame data
    packet := make([]byte, 1+len(frameData))
    packet[0] = toc
    copy(packet[1:], frameData)

    return packet
}
```

### VBR/CBR Mode Control
```go
// Source: opus-codec.org encoder CTL documentation

type BitrateMode int

const (
    ModeVBR BitrateMode = iota  // Variable bitrate (default)
    ModeCVBR                     // Constrained VBR
    ModeCBR                      // Constant bitrate
)

func (e *Encoder) SetBitrateMode(mode BitrateMode) {
    e.bitrateMode = mode
}

func (e *Encoder) computePacketSize(pcm []float64) int {
    switch e.bitrateMode {
    case ModeVBR:
        // Natural size based on content
        return e.estimateVBRSize(pcm)
    case ModeCVBR:
        // Target with +-10% tolerance
        target := e.bitrate * e.frameDurationMs() / 8000
        return constrainSize(e.estimateVBRSize(pcm), target, 0.10)
    case ModeCBR:
        // Exact target size
        return e.bitrate * e.frameDurationMs() / 8000
    }
    return 0
}
```

### In-Band FEC (LBRR) Encoding
```go
// Source: RFC 6716 Section 4.2.4

func (e *Encoder) encodeSILKWithFEC(current, previous []float32) {
    // VAD flags are first symbols in bitstream
    // Bits 7-5 of first byte contain VAD flags for up to 3 frames
    vadFlags := e.computeVADFlags(current)

    // LBRR flag follows VAD flags
    lbrrPresent := e.fecEnabled && e.packetLoss > 0 && previous != nil

    // Write VAD flags (up to 3 bits)
    for i := 0; i < e.numFrames && i < 3; i++ {
        e.rangeEncoder.EncodeBit(vadFlags[i], 1)  // 50% probability
    }

    // Write LBRR flag
    if lbrrPresent {
        e.rangeEncoder.EncodeICDF16(1, silk.ICDFLBRRFlag, 8)

        // Encode previous frame at reduced quality (LBRR)
        e.encodeLBRRFrame(previous)
    } else {
        e.rangeEncoder.EncodeICDF16(0, silk.ICDFLBRRFlag, 8)
    }

    // Encode current frame normally
    e.encodeSILKFrame(current)
}
```

### DTX Silence Detection
```go
// Source: getstream.io/resources/projects/webrtc/advanced/dtx/

func (e *Encoder) shouldUseDTX(pcm []float64) bool {
    if !e.dtxEnabled {
        return false
    }

    // Use existing VAD infrastructure from SILK encoder
    // DTX triggers when VAD indicates silence/noise
    signalType, _ := e.silkEncoder.classifyFrame(toFloat32(pcm))

    // Signal type 0 = inactive (silence/noise)
    if signalType == 0 {
        e.dtxFrameCount++

        // After several silent frames, switch to DTX mode
        // Send comfort noise frame every 400ms
        if e.dtxFrameCount > 20 {  // 400ms at 20ms frames
            e.dtxFrameCount = 0
            return true  // Send comfort noise frame
        }
        return false  // Skip this frame (true DTX)
    }

    // Active speech - reset DTX counter
    e.dtxFrameCount = 0
    return false
}
```

### Complexity Setting Application
```go
// Source: opus-codec.org/docs/opus_api-1.5/

func (e *Encoder) SetComplexity(complexity int) {
    if complexity < 0 {
        complexity = 0
    }
    if complexity > 10 {
        complexity = 10
    }
    e.complexity = complexity

    // Complexity affects various encoder decisions
    // Higher complexity = better quality but slower

    // SILK: Affects pitch search resolution, LSF interpolation
    e.silkEncoder.SetComplexity(complexity)

    // CELT: Affects PVQ search iterations, MDCT precision
    e.celtEncoder.SetComplexity(complexity)
}

// Complexity impacts (from libopus):
// 0-1: Minimal search, fastest encoding
// 2-4: Basic searches, good for real-time
// 5-7: More thorough analysis (default: 10)
// 8-10: Exhaustive search, highest quality
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Fixed SILK/CELT split | Adaptive mode switching | libopus 1.0 (2012) | Better quality at all bitrates |
| Simple FEC | Rate-distortion optimized LBRR | libopus 1.4 (2023) | Less overhead, better recovery |
| Basic DTX | ML-enhanced silence detection | libopus 1.5 (2024) | Fewer false triggers |
| Fixed complexity | Per-frame adaptive | libopus 1.1 (2013) | CPU/quality balance |

**Deprecated/outdated:**
- OPUS_SET_INBAND_FEC(1) forcing SILK mode: Use OPUS_SET_INBAND_FEC(2) to allow CELT without FEC
- Deep Redundancy (DRED): ML-based, out of scope per REQUIREMENTS.md

## Open Questions

Things that couldn't be fully resolved:

1. **Optimal SILK/CELT bitrate split for hybrid**
   - What we know: SILK is VBR, CELT fills remainder
   - What's unclear: Exact allocation algorithm for different content types
   - Recommendation: Start with SILK using ~60% of budget, tune empirically

2. **Complexity setting implementation details**
   - What we know: 0-10 scale, affects search algorithms
   - What's unclear: Exact per-level thresholds in libopus
   - Recommendation: Implement coarse levels (low/medium/high), refine later

3. **Frame size matching between modes**
   - What we know: SILK 10/20/40/60ms, CELT 2.5/5/10/20ms, Hybrid 10/20ms only
   - What's unclear: Behavior when user requests incompatible size
   - Recommendation: Return error for invalid combinations

## Sources

### Primary (HIGH confidence)
- RFC 6716: Definition of the Opus Audio Codec - https://www.rfc-editor.org/rfc/rfc6716.html
  - Section 3.2: Hybrid mode structure
  - Section 4.2.4: LBRR (FEC) encoding
  - Section 3.1: TOC byte format
- opus-codec.org encoder CTL documentation - https://opus-codec.org/docs/opus_api-1.5/group__opus__encoderctls.html
  - VBR, bitrate, complexity, FEC, DTX controls
- Existing codebase (internal/hybrid/decoder.go, internal/silk/tables.go)
  - LBRR tables, hybrid coordination logic

### Secondary (MEDIUM confidence)
- libopus 1.4 release notes (April 2023) - https://opus-codec.org/release/stable/2023/04/20/libopus-1_4.html
  - Improved FEC tuning
- getstream.io DTX documentation - https://getstream.io/resources/projects/webrtc/advanced/dtx/
  - DTX behavior and implementation patterns
- Opus Codec FAQ - https://wiki.xiph.org/OpusFAQ
  - Mode selection, bitrate ranges

### Tertiary (LOW confidence)
- Various blog posts on VBR vs CBR implementation
  - General patterns, not Opus-specific

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH - Using existing verified components
- Architecture: HIGH - Clear RFC 6716 specification
- Hybrid encoding: HIGH - Mirrors implemented decoder
- VBR/CBR: MEDIUM - General pattern clear, tuning empirical
- FEC (LBRR): MEDIUM - Tables exist, encoding logic needs verification
- DTX: MEDIUM - Pattern clear, thresholds empirical
- Complexity: LOW - Implementation details not fully documented

**Research date:** 2026-01-22
**Valid until:** 2026-02-22 (30 days - stable RFC, implementation patterns settled)
