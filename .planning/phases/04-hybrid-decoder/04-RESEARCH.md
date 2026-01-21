# Phase 04: Hybrid Decoder - Research

**Researched:** 2026-01-21
**Domain:** Opus Hybrid Mode Decoding + Packet Loss Concealment
**Confidence:** HIGH

## Summary

Phase 04 implements the Hybrid decoder (combined SILK + CELT) and basic Packet Loss Concealment (PLC). Hybrid mode is used for super-wideband (SWB) and fullband (FB) speech at medium bitrates, combining SILK for 0-8kHz and CELT for 8-20kHz. The existing SILK decoder (Phase 2) and CELT decoder (Phase 3) are already complete and provide the foundation for this phase.

The Hybrid decoder's primary complexity lies in:
1. Correctly splitting the range-coded bitstream between SILK and CELT
2. Upsampling SILK's 16kHz WB output to 48kHz
3. Compensating for timing differences between layers (CELT has 2.7ms additional delay)
4. Summing the two layer outputs for final audio

PLC is decoder-specific logic that generates plausible audio when packets are lost. The RFC mandates basic PLC support; advanced ML-based PLC (Deep PLC, DRED) is out of scope per project requirements.

**Primary recommendation:** Implement Hybrid decoder as a coordinator that reuses existing SILK/CELT decoders, with explicit delay compensation and output summing. PLC should be implemented per-layer (SILK PLC, CELT PLC) with the Hybrid layer coordinating both.

## Standard Stack

The established libraries/tools for this domain:

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| internal/silk | Phase 2 | WB SILK decoding (16kHz) | Already complete, provides WB mode |
| internal/celt | Phase 3 | Fullband CELT decoding | Already complete, provides DecodeFrameWithDecoder() |
| internal/rangecoding | Phase 1 | Shared entropy decoder | Single decoder shared between layers |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| internal/silk/resample.go | Existing | 16kHz -> 48kHz upsampling | Hybrid output combining |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Linear interpolation upsample | Polyphase resampling | Higher quality but more complex; linear sufficient for v1 |
| Per-layer PLC | Combined PLC | Per-layer is simpler, matches libopus architecture |

## Architecture Patterns

### Recommended Project Structure
```
internal/
  hybrid/
    decoder.go         # Hybrid mode coordinator (NEW)
    plc.go             # Packet loss concealment (NEW)
  silk/
    plc.go             # SILK-specific PLC (NEW)
  celt/
    plc.go             # CELT-specific PLC (NEW)
```

### Pattern 1: Layered Decoder Coordination
**What:** Hybrid decoder as thin coordination layer over existing SILK/CELT decoders
**When to use:** Always for hybrid mode
**Example:**
```go
// Source: RFC 6716 Section 3.2, libopus src/opus_decoder.c
type HybridDecoder struct {
    silk *silk.Decoder
    celt *celt.Decoder

    // State for combining outputs
    delayBuffer []float64  // SILK delay compensation buffer (60 samples at 48kHz)
    prevMode    Mode       // Previous frame's mode (for PLC)
}

func (d *HybridDecoder) DecodeFrame(data []byte, frameSize int) ([]float64, error) {
    // 1. Initialize shared range decoder
    rd := &rangecoding.Decoder{}
    rd.Init(data)

    // 2. Decode SILK (WB mode = 16kHz internal)
    silkOut, err := d.silk.DecodeFrameWithRangeDecoder(rd, silk.BandwidthWideband, duration, vadFlag)

    // 3. Upsample SILK 16kHz -> 48kHz
    silk48k := upsampleTo48k(silkOut, 16000)

    // 4. Apply delay compensation (60 samples at 48kHz = 1.25ms)
    silk48kDelayed := d.applyDelayCompensation(silk48k)

    // 5. Decode CELT (bands above 8kHz only)
    celtOut, err := d.celt.DecodeFrameWithDecoder(rd, frameSize)

    // 6. Sum outputs
    output := make([]float64, len(celtOut))
    for i := range output {
        output[i] = silk48kDelayed[i] + celtOut[i]
    }

    return output, nil
}
```

### Pattern 2: CELT Band Limiting for Hybrid
**What:** In hybrid mode, CELT decodes only bands above 8kHz (bands 17-21)
**When to use:** All hybrid frames
**Example:**
```go
// Source: RFC 6716, libopus CELT_SET_END_BAND(17)
// In hybrid mode, bands 0-16 (0-8kHz) are zeroed/skipped
// CELT only contributes frequency content above 8kHz

const HybridCELTStartBand = 17  // Band 17 starts at ~8kHz

func (d *CELTDecoder) DecodeHybridFrame(rd *rangecoding.Decoder, frameSize int) ([]float64, error) {
    // Energy decoding: zero energy for bands 0-16
    energies := make([]float64, MaxBands)
    for band := HybridCELTStartBand; band < nbBands; band++ {
        energies[band] = d.decodeCoarseBandEnergy(band, intra, lm)
    }

    // Band decoding: skip bands 0-16 entirely
    // Frequency content below 8kHz comes from SILK layer
    ...
}
```

### Pattern 3: Per-Layer PLC
**What:** Each layer (SILK, CELT) maintains its own concealment state
**When to use:** All packet loss scenarios
**Example:**
```go
// Source: RFC 6716 Section 4.4, libopus silk_PLC() and celt_decode_lost()
// PLC is invoked when data is NULL

func (d *HybridDecoder) DecodePLC(frameSize int) []float64 {
    // Generate concealment for each layer
    silkPLC := d.silk.GeneratePLC(frameSize / 3)  // WB samples
    celtPLC := d.celt.GeneratePLC(frameSize)       // 48kHz samples

    // Upsample and combine
    silk48k := upsampleTo48k(silkPLC, 16000)
    silk48kDelayed := d.applyDelayCompensation(silk48k)

    output := make([]float64, len(celtPLC))
    for i := range output {
        output[i] = silk48kDelayed[i] + celtPLC[i]
    }

    return output
}
```

### Anti-Patterns to Avoid
- **Tight coupling SILK/CELT in hybrid:** Keep them separate decoders coordinated by hybrid layer
- **Ignoring delay compensation:** Outputs will be phase-misaligned causing audible artifacts
- **Complex PLC algorithms:** Basic PLC (signal decay, parameter extrapolation) is sufficient for v1
- **Decoding full CELT bands in hybrid:** Bands 0-16 must be zeroed/skipped to avoid doubling low frequencies

## Don't Hand-Roll

Problems that look simple but have existing solutions:

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| SILK upsampling | Custom polyphase resampler | Existing linear interpolation | Decision D02-05-01 already made; polyphase deferred |
| Range decoder | Second decoder instance | Single shared decoder | SILK and CELT share bitstream, single decoder advances position |
| Delay buffer | Complex ring buffer | Simple slice with shift | 60 samples is small; optimization unnecessary |
| Energy interpolation for PLC | Complex models | Simple decay with noise | RFC only requires "reasonable" audio for PLC |

**Key insight:** Hybrid mode is primarily coordination and combination of existing decoders. The complexity is in the timing and band management, not in new algorithms.

## Common Pitfalls

### Pitfall 1: Incorrect CELT Band Range for Hybrid
**What goes wrong:** Decoding all 21 CELT bands in hybrid mode, causing low-frequency doubling with SILK
**Why it happens:** Forgetting that CELT must discard bands below 8kHz in hybrid
**How to avoid:** Set CELT start band to 17 (approximately 8kHz) for hybrid frames
**Warning signs:** Audio sounds "phasey" or has doubled bass

### Pitfall 2: Missing Delay Compensation
**What goes wrong:** SILK and CELT outputs are summed without timing alignment
**Why it happens:** RFC mentions 2.7ms encoder delay but decoder compensation is less documented
**How to avoid:** Delay SILK output by 60 samples at 48kHz (1.25ms) relative to CELT
**Warning signs:** Transients sound "smeared" or doubled
**Note:** The exact delay value is `SilkCELTDelay = 60` samples already defined in celt/tables.go

### Pitfall 3: Shared Range Decoder State Corruption
**What goes wrong:** SILK decoding corrupts state before CELT can read
**Why it happens:** Both layers read from same bitstream sequentially
**How to avoid:** SILK decodes first (per spec), then CELT decodes remainder; never reset decoder between
**Warning signs:** CELT produces garbage or decoder reports errors

### Pitfall 4: PLC Not Maintaining Per-Layer State
**What goes wrong:** PLC produces silence instead of plausible audio
**Why it happens:** Not tracking previous frame parameters for extrapolation
**How to avoid:** Each decoder maintains state for concealment (SILK: LPC/pitch, CELT: energy/shape)
**Warning signs:** Packet loss produces silent gaps instead of smooth transitions

### Pitfall 5: Wrong Frame Size Assumptions
**What goes wrong:** Trying to decode 40ms/60ms frames in hybrid mode
**Why it happens:** Forgetting hybrid only supports 10ms and 20ms
**How to avoid:** Validate frame duration early; hybrid configs are only 12-15 (10ms or 20ms)
**Warning signs:** Decoder crashes or produces truncated output

### Pitfall 6: Forgetting SILK Uses WB Mode in Hybrid
**What goes wrong:** Using wrong bandwidth for SILK in hybrid (NB or MB)
**Why it happens:** Opus bandwidth field indicates SWB/FB but SILK internally uses WB
**How to avoid:** Always use `silk.BandwidthWideband` (16kHz) for SILK in hybrid mode
**Warning signs:** Wrong LPC order (10 vs 16), wrong sample count

## Code Examples

Verified patterns from official sources:

### Hybrid Frame Detection
```go
// Source: RFC 6716 Section 3.1, packet.go (existing)
// TOC config 12-15 = hybrid mode

func IsHybridMode(tocConfig uint8) bool {
    return tocConfig >= 12 && tocConfig <= 15
}

// Config 12-13: Hybrid SWB (super-wideband) 10ms/20ms
// Config 14-15: Hybrid FB (fullband) 10ms/20ms
```

### Hybrid Frame Duration
```go
// Source: RFC 6716, existing packet.go configTable
// Hybrid only supports 10ms (480 samples at 48kHz) or 20ms (960 samples)

func HybridFrameSize(tocConfig uint8) int {
    switch tocConfig {
    case 12, 14:
        return 480  // 10ms
    case 13, 15:
        return 960  // 20ms
    default:
        return 0    // Not hybrid
    }
}
```

### SILK WB Sample Count for Hybrid
```go
// Source: SILK bandwidth configs (internal/silk/bandwidth.go)
// SILK WB = 16kHz, so sample counts are 48kHz / 3

func SILKSamplesForHybrid(frameSize48kHz int) int {
    return frameSize48kHz / 3  // 16kHz = 48kHz / 3
    // 10ms: 480/3 = 160 samples
    // 20ms: 960/3 = 320 samples
}
```

### Basic SILK PLC
```go
// Source: RFC 6716 Section 4.2.7.9, libopus silk/PLC.c
// SILK PLC extrapolates LPC parameters and generates decaying excitation

func (d *SILKDecoder) GeneratePLC(samples int) []float32 {
    output := make([]float32, samples)

    // Use previous LPC coefficients
    lpc := d.PrevLPCValues()

    // Generate decaying noise excitation
    gain := d.PreviousLogGain()
    decayFactor := float32(0.98)  // Per-sample decay

    for i := 0; i < samples; i++ {
        // Random excitation (LCG noise)
        excitation := d.NextRNG() / float32(1<<31) * 2 - 1
        excitation *= float32(gain) * decayFactor
        decayFactor *= 0.98

        // LPC synthesis
        sample := excitation
        for j := 0; j < len(lpc); j++ {
            if i-j-1 >= 0 {
                sample -= lpc[j] * output[i-j-1]
            }
        }
        output[i] = sample
    }

    return output
}
```

### Basic CELT PLC
```go
// Source: RFC 6716 Section 4.3.6, libopus celt/celt_decoder.c celt_decode_lost()
// CELT PLC generates shaped noise at previous frame's energy levels

func (d *CELTDecoder) GeneratePLC(frameSize int) []float64 {
    // Use previous frame's energy envelope
    energies := d.PrevEnergy()

    // Generate noise in frequency domain
    coeffs := make([]float64, frameSize)
    for band := 0; band < d.nbBands; band++ {
        bandStart := ScaledBandStart(band, frameSize)
        bandEnd := ScaledBandEnd(band, frameSize)

        // Noise with energy from previous frame, decayed
        energy := energies[band] * 0.9  // 10% energy reduction

        for bin := bandStart; bin < bandEnd; bin++ {
            coeffs[bin] = d.generateNoise() * math.Exp(energy * 0.6931)
        }
    }

    // IMDCT synthesis
    return d.Synthesize(coeffs, false, 1)
}
```

### Delay Compensation
```go
// Source: libopus, celt/tables.go SilkCELTDelay = 60
// SILK must be delayed 60 samples (1.25ms) relative to CELT at 48kHz

func (d *HybridDecoder) applyDelayCompensation(silk48k []float64) []float64 {
    // Prepend delay buffer, append current to buffer
    output := make([]float64, len(silk48k))

    // First samples come from delay buffer
    copy(output[:SilkCELTDelay], d.delayBuffer)

    // Rest comes from current frame
    copy(output[SilkCELTDelay:], silk48k[:len(silk48k)-SilkCELTDelay])

    // Update delay buffer with tail of current frame
    copy(d.delayBuffer, silk48k[len(silk48k)-SilkCELTDelay:])

    return output
}
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Simple noise fill for PLC | Deep PLC with neural networks | Opus 1.5 (2024) | Much better quality but out of scope |
| Fixed crossover at 8kHz | Same | RFC 6716 (2012) | Fundamental to hybrid design |
| LBRR for redundancy | DRED (deep redundancy) | Opus 1.5 (2024) | Better burst loss recovery, out of scope |

**Deprecated/outdated:**
- LBRR (Low Bitrate Redundancy): Still supported but DRED is preferred in modern libopus
- Note: For this pure-Go implementation, basic PLC is sufficient; ML-based PLC explicitly out of scope

## Open Questions

Things that couldn't be fully resolved:

1. **Exact CELT band zeroing method**
   - What we know: CELT bands below 8kHz are "discarded" in hybrid
   - What's unclear: Whether they're decoded then zeroed, or skipped in decoding entirely
   - Recommendation: Start with zeroing decoded bands; optimize to skip if needed

2. **VAD flag propagation in hybrid**
   - What we know: SILK uses VAD flag for frame type
   - What's unclear: How VAD is encoded/decoded in hybrid mode specifically
   - Recommendation: Read VAD from SILK layer header as normal

3. **Stereo handling in hybrid**
   - What we know: Both layers support stereo
   - What's unclear: Whether stereo prediction/unmixing happens per-layer or combined
   - Recommendation: Apply stereo processing per-layer, sum after

## Sources

### Primary (HIGH confidence)
- [RFC 6716](https://www.rfc-editor.org/rfc/rfc6716) - Official Opus specification, Sections 3.2 (Hybrid), 4.4 (PLC)
- Existing codebase: internal/silk/, internal/celt/, packet.go - Complete Phase 2/3 implementations
- [libopus source](https://github.com/xiph/opus) - src/opus_decoder.c hybrid decoding path

### Secondary (MEDIUM confidence)
- [Opus Codec Wikipedia](https://en.wikipedia.org/wiki/Opus_(audio_format)) - Overview of hybrid mode crossover
- [AES135 Paper](https://jmvalin.ca/papers/aes135_opus_celt.pdf) - CELT architecture in hybrid

### Tertiary (LOW confidence)
- [pion/opus](https://github.com/pion/opus) - Pure Go reference (SILK only, no hybrid yet)

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH - Using existing Phase 2/3 components
- Architecture: HIGH - RFC 6716 clearly defines hybrid operation
- Pitfalls: HIGH - Well-documented in RFC and existing implementations
- PLC: MEDIUM - Basic approach is clear, advanced techniques out of scope

**Research date:** 2026-01-21
**Valid until:** 2026-03-21 (stable domain, 60 days)
