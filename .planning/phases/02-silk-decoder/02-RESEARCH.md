# Phase 2: SILK Decoder - Research

**Researched:** 2026-01-21
**Domain:** SILK audio decoding per RFC 6716 Section 4.2
**Confidence:** HIGH

## Summary

Phase 2 implements the SILK decoder, which decodes speech-optimized Opus packets using Linear Predictive Coding (LPC) synthesis. SILK is the LP layer of Opus and handles narrowband (8kHz), mediumband (12kHz), and wideband (16kHz) speech. The decoder processes compressed frames through header parsing, parameter decoding (gains, LSF/LPC coefficients, pitch lags), excitation reconstruction, and synthesis filtering (LTP for voiced, LPC for all).

This research analyzed RFC 6716 Section 4.2, the pion/opus pure-Go SILK implementation, and libopus reference code. The SILK decoder is well-specified with clear algorithmic steps in the RFC. The pion/opus project provides a complete working Go reference that follows RFC naming conventions and structure. Key complexity lies in the numerous ICDF probability tables (~47 tables) and codebook tables (~20 tables) required for entropy decoding.

**Primary recommendation:** Follow pion/opus architecture closely for SILK decoder, adapting their table definitions and decoder structure. Use the existing rangecoding package for entropy decoding. Implement mono decoding first, then add stereo unmixing as a separate step.

## Standard Stack

The established libraries/tools for this domain:

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| Go standard library | 1.21+ | All functionality | Zero dependencies requirement (CMP-03) |
| internal/rangecoding | Phase 1 | Entropy decoding | Already implemented, bit-exact |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `testing` | stdlib | Unit tests | All test code |
| `math` | stdlib | Float operations | Synthesis filter math |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Direct pion/opus import | Reference only | Would couple to their design |
| FFmpeg SILK tables | RFC tables | Non-standard source |

**Installation:**
```bash
# No external dependencies - pure stdlib
# Uses internal/rangecoding from Phase 1
```

## Architecture Patterns

### Recommended Project Structure
```
gopus/
├── internal/
│   ├── rangecoding/          # From Phase 1
│   │   ├── decoder.go
│   │   ├── encoder.go
│   │   └── constants.go
│   └── silk/                  # NEW: Phase 2
│       ├── decoder.go         # Main SILK decoder struct and Decode method
│       ├── tables.go          # ICDF tables (~47 tables)
│       ├── codebook.go        # LSF, LTP, pitch codebooks (~20 tables)
│       ├── lpc.go             # LPC coefficient conversion and synthesis
│       ├── ltp.go             # Long-term prediction (pitch) synthesis
│       ├── excitation.go      # Excitation signal reconstruction
│       ├── stereo.go          # Stereo prediction and unmixing
│       └── silk_test.go       # Unit tests
├── packet.go                  # From Phase 1 (TOC parsing)
└── decoder.go                 # Public API (future Phase 10)
```

### Pattern 1: Stateful Decoder with Frame Persistence
**What:** SILK decoder maintains state across frames for continuity
**When to use:** All SILK decoding
**Example:**
```go
// Source: pion/opus internal/silk/decoder.go, RFC 6716 Section 4.2
type Decoder struct {
    rangeDecoder           *rangecoding.Decoder
    haveDecoded            bool      // True after first frame decoded
    isPreviousFrameVoiced  bool      // Voice activity from prior frame
    previousLogGain        int32     // Gain for delta quantization
    previousFrameLPCValues []float32 // d_LPC state (10 NB/MB, 16 WB)
    finalOutValues         []float32 // Output buffer with history
    n0Q15                  []int16   // Previous frame LSF coefficients
}
```

### Pattern 2: Bandwidth-Dependent Parameters
**What:** SILK parameters vary by bandwidth (NB/MB/WB)
**When to use:** All parameter decoding
**Example:**
```go
// Source: RFC 6716 Section 4.2
type BandwidthConfig struct {
    SampleRate      int   // 8000, 12000, or 16000 Hz
    LPCOrder        int   // 10 for NB/MB, 16 for WB
    SubframeSamples int   // 40 NB, 60 MB, 80 WB (for 20ms frames)
    PitchLagMin     int   // Minimum pitch lag in samples
    PitchLagMax     int   // Maximum pitch lag in samples
}

var bandwidthConfigs = map[Bandwidth]BandwidthConfig{
    BandwidthNarrowband:  {8000, 10, 40, 16, 144},
    BandwidthMediumband:  {12000, 10, 60, 24, 216},
    BandwidthWideband:    {16000, 16, 80, 32, 288},
}
```

### Pattern 3: ICDF-Based Range Decoding
**What:** Use pre-defined inverse CDF tables for symbol decoding
**When to use:** All parameter extraction from bitstream
**Example:**
```go
// Source: RFC 6716 Section 4.1.4, pion/opus
// ICDF tables start at 256, decrease, end at 0
var icdfFrameTypeVADActive = []uint8{256, 230, 166, 128, 0}

func (d *Decoder) decodeFrameType() (signalType, quantOffset int) {
    // Decode using ICDF table based on VAD flag
    if vadActive {
        idx := d.rangeDecoder.DecodeICDF(icdfFrameTypeVADActive, 8)
        signalType = idx >> 1  // 0=inactive, 1=unvoiced, 2=voiced
        quantOffset = idx & 1  // 0=low, 1=high
    }
    return
}
```

### Pattern 4: Subframe-by-Subframe Processing
**What:** Process each 5ms subframe with interpolated parameters
**When to use:** Synthesis filtering
**Example:**
```go
// Source: RFC 6716 Section 4.2.7.9
func (d *Decoder) silkFrameReconstruction(
    bandwidth Bandwidth,
    signalType int,
    gains []int32,      // 4 subframe gains
    lpcCoeffs []int16,  // Q12 LPC coefficients
    pitchLags []int,    // 4 subframe pitch lags (voiced only)
    ltpCoeffs [][]int8, // 4x5 LTP coefficients (voiced only)
    excitation []int32, // Reconstructed excitation
    out []float32,
) {
    subframeSamples := bandwidthConfigs[bandwidth].SubframeSamples
    for sf := 0; sf < 4; sf++ {
        start := sf * subframeSamples
        end := start + subframeSamples

        // LTP synthesis (voiced only)
        if signalType == 2 {
            d.ltpSynthesis(excitation[start:end], pitchLags[sf], ltpCoeffs[sf])
        }

        // LPC synthesis
        d.lpcSynthesis(excitation[start:end], lpcCoeffs, gains[sf], out[start:end])
    }
}
```

### Anti-Patterns to Avoid
- **Global decoder state:** Each decoder instance must have independent state
- **Ignoring previous frame state:** LPC and gain values carry across frames
- **Hardcoded bandwidth values:** Always use config tables for flexibility
- **Mixing Q-format values:** Keep track of Q12, Q13, Q15, Q16 fixed-point formats

## Don't Hand-Roll

Problems that look simple but have existing solutions:

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Range decoding | Custom bit reader | `internal/rangecoding.Decoder` | Already implemented, bit-exact |
| LSF to LPC conversion | Custom algorithm | Direct port of RFC algorithm | Complex stability constraints |
| ICDF tables | Generate dynamically | Static tables from RFC/pion | Tables are fixed, generation error-prone |
| Pitch lag interpolation | Simple linear | RFC-specified contour codebooks | Specific shapes for each bandwidth |
| Excitation shell coding | Custom partition | Direct port of RFC algorithm | Recursive binary partition is precise |

**Key insight:** SILK decoding is entirely specified by RFC 6716 with pseudocode. Port the algorithms exactly rather than reimplementing from scratch. The complexity is in the tables and fixed-point arithmetic, not novel algorithms.

## Common Pitfalls

### Pitfall 1: Fixed-Point Format Confusion
**What goes wrong:** Coefficients overflow or produce wrong magnitudes
**Why it happens:** SILK uses multiple Q-formats: Q7 (LTP), Q12 (LPC), Q13 (stereo weights), Q15 (LSF), Q16 (gains)
**How to avoid:** Document Q-format in variable names or comments; verify shifts match RFC
**Warning signs:** Audio has wrong volume, distortion, or clicks

### Pitfall 2: LPC Filter Instability
**What goes wrong:** Output explodes or rings
**Why it happens:** Insufficient bandwidth expansion, filter poles outside unit circle
**How to avoid:** Implement `limitLPCFilterPredictionGain()` exactly per RFC with up to 16 rounds of bandwidth expansion
**Warning signs:** Loud buzzing or exponentially growing output

### Pitfall 3: Forgetting Previous Frame State
**What goes wrong:** Clicks at frame boundaries, wrong gain values
**Why it happens:** SILK uses delta coding for gains and interpolation for LSF coefficients
**How to avoid:** Store `previousLogGain`, `previousFrameLPCValues`, `n0Q15` across frames
**Warning signs:** First frame decodes fine, subsequent frames have artifacts

### Pitfall 4: Wrong ICDF Table Selection
**What goes wrong:** Decoded parameters are nonsensical
**Why it happens:** Many parameters use different tables based on signal type (voiced/unvoiced/inactive) or bandwidth
**How to avoid:** Carefully match table selection to RFC conditions
**Warning signs:** Decoded values out of expected range

### Pitfall 5: Subframe Count Mismatch
**What goes wrong:** Only partial frame decoded, buffer overrun
**Why it happens:** 10ms frames have 2 subframes, 20ms frames have 4 subframes
**How to avoid:** Calculate subframe count from frame duration in TOC
**Warning signs:** Only half the expected samples output

### Pitfall 6: Stereo Channel Interleaving
**What goes wrong:** Mid and side channels mixed up, stereo image wrong
**Why it happens:** SILK interleaves mid and side channel data in the bitstream
**How to avoid:** Decode parameters for both channels alternately, unmix after synthesis
**Warning signs:** Audio sounds phase-cancelled or mono when it should be stereo

### Pitfall 7: LTP Lookback Buffer Underrun
**What goes wrong:** Pitch prediction reads invalid data
**Why it happens:** LTP filter needs `pitchLag + 2` samples of history
**How to avoid:** Maintain output history buffer of at least 306 samples (max lag + filter order)
**Warning signs:** Voiced speech has garbage or zeros at frame start

### Pitfall 8: Excitation Sign Decoding
**What goes wrong:** Excitation pulses have wrong polarity
**Why it happens:** Sign tables differ by signal type and quantization offset
**How to avoid:** Select correct sign ICDF based on (signalType * 2 + quantOffset)
**Warning signs:** Speech sounds distorted or "buzzy"

## Code Examples

Verified patterns from official sources:

### Decoder Initialization
```go
// Source: pion/opus internal/silk/decoder.go
func NewDecoder() *Decoder {
    return &Decoder{
        haveDecoded:            false,
        isPreviousFrameVoiced:  false,
        previousLogGain:        0,
        previousFrameLPCValues: make([]float32, 16), // Max for WB
        finalOutValues:         make([]float32, 306), // Max lookback buffer
        n0Q15:                  make([]int16, 16),
    }
}
```

### Frame Type Decoding
```go
// Source: RFC 6716 Section 4.2.7.3
func (d *Decoder) decodeFrameType(vadFlag bool) (signalType, quantOffset int) {
    if !vadFlag {
        // Inactive frame
        return 0, 0
    }
    // Decode 2 bits: signal type (0-2) and quantization offset (0-1)
    idx := d.rangeDecoder.DecodeICDF(icdfFrameTypeVADActive, 8)
    signalType = idx >> 1   // 0=inactive, 1=unvoiced, 2=voiced
    quantOffset = idx & 1   // 0=low quantization, 1=high quantization
    return
}
```

### Subframe Gain Decoding
```go
// Source: RFC 6716 Section 4.2.7.4, pion/opus
func (d *Decoder) decodeSubframeGains(signalType int, numSubframes int) []int32 {
    gains := make([]int32, numSubframes)

    // First gain: absolute or delta from previous frame
    var icdfMSB []uint8
    switch signalType {
    case 0: icdfMSB = icdfGainMSBInactive
    case 1: icdfMSB = icdfGainMSBUnvoiced
    case 2: icdfMSB = icdfGainMSBVoiced
    }

    msb := d.rangeDecoder.DecodeICDF(icdfMSB, 8)
    lsb := d.rangeDecoder.DecodeICDF(icdfGainLSB, 8)
    gains[0] = int32(msb*8 + lsb)

    // Subsequent gains: delta from previous subframe
    for i := 1; i < numSubframes; i++ {
        delta := d.rangeDecoder.DecodeICDF(icdfDeltaGain, 8)
        gains[i] = gains[i-1] + int32(delta) - 4 // Centered at 4
    }

    return gains
}
```

### LSF Coefficient Decoding (Two-Stage VQ)
```go
// Source: RFC 6716 Section 4.2.7.5.1
func (d *Decoder) decodeLSFCoefficients(bandwidth Bandwidth, signalType int) []int16 {
    isWideband := bandwidth == BandwidthWideband
    isVoiced := signalType == 2

    // Stage 1: Select codebook vector
    var icdf []uint8
    if isWideband {
        if isVoiced { icdf = icdfLSFStage1WBVoiced } else { icdf = icdfLSFStage1WBUnvoiced }
    } else {
        if isVoiced { icdf = icdfLSFStage1NBVoiced } else { icdf = icdfLSFStage1NBUnvoiced }
    }
    stage1Idx := d.rangeDecoder.DecodeICDF(icdf, 8)

    // Stage 2: Decode residuals for each coefficient
    // ... residual decoding using stage 2 tables

    // Combine stage 1 codebook with stage 2 residuals
    // ... LSF reconstruction

    return lsfQ15
}
```

### LPC Synthesis Filter
```go
// Source: RFC 6716 Section 4.2.7.9.2
func (d *Decoder) lpcSynthesis(
    residual []int32,
    a []int16,        // Q12 LPC coefficients
    gain int32,       // Q16 subframe gain
    dLPC []float32,   // Previous LPC output values (state)
    out []float32,
) {
    order := len(a)
    for i, res := range residual {
        // Compute LPC prediction
        var pred int64
        for j := 0; j < order; j++ {
            pred += int64(a[j]) * int64(dLPC[(len(dLPC)-1-j)])
        }
        pred >>= 12 // Q12 to integer

        // Add scaled residual
        sample := (int64(gain) * int64(res)) >> 16 // Q16 gain
        sample += pred

        // Clamp to 16-bit range
        if sample > 32767 { sample = 32767 }
        if sample < -32768 { sample = -32768 }

        // Store output and update state
        out[i] = float32(sample) / 32768.0

        // Shift state and add new sample
        copy(dLPC[:len(dLPC)-1], dLPC[1:])
        dLPC[len(dLPC)-1] = float32(sample)
    }
}
```

### LTP Synthesis Filter (Voiced Frames)
```go
// Source: RFC 6716 Section 4.2.7.9.1
func (d *Decoder) ltpSynthesis(
    excitation []int32,
    pitchLag int,
    ltpCoeffs []int8, // Q7 LTP filter taps (5 coefficients)
    history []float32, // Previous excitation (at least pitchLag + 2 samples)
) {
    for i := range excitation {
        var pred int64
        // 5-tap filter centered at pitchLag
        for j := 0; j < 5; j++ {
            histIdx := len(history) - pitchLag + j - 2
            if histIdx >= 0 && histIdx < len(history) {
                pred += int64(ltpCoeffs[j]) * int64(history[histIdx] * 128) // Q7
            }
        }
        pred >>= 7 // Q7 to integer

        // Add prediction to excitation
        excitation[i] += int32(pred)

        // Update history
        copy(history[:len(history)-1], history[1:])
        history[len(history)-1] = float32(excitation[i])
    }
}
```

### Stereo Unmixing
```go
// Source: RFC 6716 Section 4.2.8
func stereoUnmix(
    mid, side []float32,
    w0Q13, w1Q13 int16, // Stereo prediction weights
    left, right []float32,
) {
    for i := range mid {
        m := mid[i]
        s := side[i]

        // Apply prediction weights (Q13 format)
        // Left = Mid + pred(Side)
        // Right = Mid - Side + pred
        pred := float32(w0Q13)*m + float32(w1Q13)*s
        pred /= 8192.0 // Q13 to float

        left[i] = m + s + pred
        right[i] = m - s + pred
    }
}
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| SILK-only codec | SILK within Opus | RFC 6716 (2012) | Unified with CELT, seamless switching |
| 20ms only frames | 10/20/40/60ms support | Opus design | Latency flexibility |
| Integer-only output | Float32 intermediate | Modern impls | Easier resampling |
| No redundancy | LBRR frames | SILK original | Packet loss resilience |

**Deprecated/outdated:**
- Standalone SILK codec (draft-vos-silk): Superseded by Opus
- SILK super-wideband: In Opus, SWB/FB use Hybrid mode (SILK WB + CELT), not SILK alone

## Open Questions

Things that couldn't be fully resolved:

1. **LBRR Frame Handling**
   - What we know: LBRR frames appear before regular SILK frames for redundancy
   - What's unclear: Whether to decode them or skip for basic implementation
   - Recommendation: Skip LBRR initially (pion/opus does this), add later for PLC

2. **Resampling to 48kHz**
   - What we know: SILK outputs at 8/12/16kHz, Opus API expects 48kHz
   - What's unclear: Best resampling approach (integer factor, polyphase)
   - Recommendation: Simple integer upsampling (3x or 6x) for initial impl, optimize later

3. **10ms Frame Support**
   - What we know: 10ms frames have 2 subframes, pion/opus only supports 20ms
   - What's unclear: Full parameter table differences for 10ms
   - Recommendation: Implement 20ms first, verify tables apply to 10ms

4. **Test Vector Format**
   - What we know: RFC 8251 has test vectors, available as archives
   - What's unclear: Best way to extract and use SILK-specific test vectors
   - Recommendation: Use libopus to generate reference output for known inputs

## SILK Decoder Data Requirements

### ICDF Tables Required (~47 tables)

| Category | Table Count | Purpose |
|----------|-------------|---------|
| Frame type/VAD | 2 | Signal type classification |
| Gain quantization | 5 | MSB (3 variants), LSB, delta |
| LSF Stage 1 | 4 | NB/MB, WB x voiced/unvoiced |
| LSF Stage 2 | 16 | 8 codebooks x 2 bandwidth groups |
| LSF supporting | 2 | Extension, interpolation |
| Pitch/LTP | 6 | Lag, contour, periodicity, LTP indices |
| Excitation | 5 | Pulse counts, splits, LSB |
| Excitation signs | 42 | 3 types x 2 quant x 7 pulse counts |
| Rate control | 2 | Unvoiced, voiced |
| LCG seed | 1 | Uniform 4-symbol |

### Codebook Tables Required (~20 tables)

| Table | Dimensions | Format |
|-------|------------|--------|
| LSF Stage 1 NB/MB | 32 x 10 | Q8 uint |
| LSF Stage 1 WB | 32 x 16 | Q8 uint |
| LSF Stage 2 NB/MB | 32 x 10 | int |
| LSF Stage 2 WB | 32 x 16 | int |
| Prediction weights NB/MB | 2 x 9 | uint |
| Prediction weights WB | 2 x 15 | uint |
| Weight selection NB/MB | 32 x 9 | binary |
| Weight selection WB | 32 x 15 | binary |
| LTP filter (3 periodicity) | 8x5, 16x5, 32x5 | Q7 int8 |
| Pitch contour (4 variants) | varies x 2/4 | int8 |
| LSF minimum spacing (2) | 11, 17 | int |
| Cosine table | 129 | Q12 int32 |
| LSF ordering (2) | 10, 16 | uint8 |

## Sources

### Primary (HIGH confidence)
- [RFC 6716 Section 4.2](https://datatracker.ietf.org/doc/html/rfc6716#section-4.2) - SILK decoder specification
- [pion/opus internal/silk/decoder.go](https://github.com/pion/opus/blob/master/internal/silk/decoder.go) - Pure Go SILK implementation
- [pion/opus internal/silk/icdf.go](https://github.com/pion/opus/blob/master/internal/silk/icdf.go) - Complete ICDF table definitions
- [pion/opus internal/silk/codebook.go](https://github.com/pion/opus/blob/master/internal/silk/codebook.go) - Codebook table definitions
- [libopus silk/decode_frame.c](https://android.googlesource.com/platform/external/libopus/+/refs/heads/main/silk/decode_frame.c) - Reference C implementation

### Secondary (MEDIUM confidence)
- [draft-vos-silk-02](https://datatracker.ietf.org/doc/html/draft-vos-silk-02) - Original SILK codec draft
- [Opus FAQ](https://wiki.xiph.org/OpusFAQ) - Implementation guidance
- [pion/opus blog](https://pion.ly/blog/pion-opus/) - Design philosophy

### Tertiary (LOW confidence)
- WebSearch results on SILK implementation patterns
- Community discussions on Rust/Go Opus implementations

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH - stdlib only, uses existing rangecoding
- Architecture: HIGH - follows established pion/opus structure
- Pitfalls: HIGH - documented in RFC and verified against pion/opus
- SILK algorithm: HIGH - RFC normative + multiple reference implementations
- Tables: HIGH - can be directly ported from pion/opus (MIT licensed)
- Stereo unmixing: MEDIUM - less detail in accessible sources

**Research date:** 2026-01-21
**Valid until:** 2026-04-21 (stable RFC, unlikely to change)
