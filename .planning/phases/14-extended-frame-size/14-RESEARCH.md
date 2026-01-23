# Phase 14: Extended Frame Size Support - Research

**Researched:** 2026-01-23
**Domain:** Opus frame size handling per RFC 6716 (CELT 2.5/5ms, SILK 40/60ms)
**Confidence:** HIGH

## Summary

Phase 14 addresses the critical gap preventing RFC 8251 test vector compliance: limited frame size support. The current implementation only supports CELT 10ms/20ms frames and SILK 10ms/20ms frames. RFC 6716 requires support for CELT 2.5ms/5ms frames and SILK 40ms/60ms frames to pass the official test vectors.

Research confirms that:
1. **CELT 2.5ms/5ms frames** use the same MDCT architecture with scaled bin counts (frame_size/120 = 1 for 2.5ms, 2 for 5ms, 4 for 10ms, 8 for 20ms)
2. **SILK 40ms/60ms frames** are decoded as 2 or 3 consecutive 20ms sub-blocks with shared parameter prediction
3. The existing mode configurations in `celt/modes.go` already define 2.5ms (120 samples) and 5ms (240 samples) - these need to be properly exercised
4. The existing SILK frame handling in `silk/frame.go` already defines 40ms/60ms - the decode path needs verification

The known gap mentions "MDCT bin count (800) doesn't match frame size (960)" - this requires investigation as the MDCT bin count should equal frame_size for non-transient frames.

**Primary recommendation:** Verify and fix the existing frame size infrastructure for CELT 2.5/5ms and SILK 40/60ms, focusing on correct MDCT sizing, proper band scaling, and sub-block coordination.

## Standard Stack

### Core
| Component | Version | Purpose | Why Standard |
|-----------|---------|---------|--------------|
| internal/celt | Phase 3 | CELT decoding | Existing implementation with mode configs |
| internal/silk | Phase 2 | SILK decoding | Existing implementation with frame handling |
| internal/hybrid | Phase 4 | Hybrid coordination | Orchestrates SILK+CELT |
| internal/rangecoding | Phase 1 | Entropy decoding | Shared range decoder |

### Supporting
| Component | Purpose | When to Use |
|-----------|---------|-------------|
| internal/testvectors | RFC 8251 compliance | Validation of frame size fixes |
| packet.go | TOC parsing | Frame size extraction from packets |

### Dependencies
- Phase 3 (CELT Decoder): Required for 2.5ms/5ms frame support
- Phase 4 (Hybrid Decoder): Required for coordinating extended frame sizes

## Architecture Patterns

### Pattern 1: CELT Frame Size to MDCT Configuration
**What:** Each CELT frame size maps to specific MDCT parameters
**When to use:** All CELT decoding

```go
// Source: RFC 6716 Section 4.3, libopus celt/modes.c
// Existing modeConfigs in celt/modes.go

// Frame size determines:
// 1. LM (log mode): log2(frameSize/120)
// 2. MDCT size: frameSize samples (for non-transient)
// 3. Short block count: 1,2,4,8 (for transient)
// 4. Effective bands: reduced for shorter frames

// Key relationship:
// - FrameSize 120 (2.5ms): LM=0, EffBands=13, MDCTSize=120
// - FrameSize 240 (5ms):   LM=1, EffBands=17, MDCTSize=240
// - FrameSize 480 (10ms):  LM=2, EffBands=19, MDCTSize=480
// - FrameSize 960 (20ms):  LM=3, EffBands=21, MDCTSize=960

// Band scaling: bands are scaled by frameSize/120
// For 20ms: bands use 8x the bins of 2.5ms
// ScaledBandWidth(band, 960) = BandWidth(band) * 8
```

### Pattern 2: SILK Multi-Block Decoding for 40/60ms
**What:** Long SILK frames decompose into 2-3 consecutive 20ms sub-blocks
**When to use:** SILK 40ms and 60ms frame decoding

```go
// Source: RFC 6716 Section 4.2, libopus silk/dec_API.c
// Existing implementation in silk/frame.go and silk/decode.go

// 40ms frame: 2 x 20ms sub-blocks (8 subframes total)
// 60ms frame: 3 x 20ms sub-blocks (12 subframes total)

// Each sub-block shares prediction state with previous:
// - LPC coefficients interpolate across sub-blocks
// - Gain prediction uses previous sub-block's final gain
// - Pitch lag prediction uses previous lag

func (d *Decoder) DecodeFrame(..., duration FrameDuration, ...) {
    if is40or60ms(duration) {
        subBlocks := getSubBlockCount(duration)  // 2 or 3
        for block := 0; block < subBlocks; block++ {
            d.decode20msBlock(bandwidth, vadFlag, blockOutput)
        }
    } else {
        d.decodeBlock(bandwidth, vadFlag, duration, output)
    }
}
```

### Pattern 3: CELT Transient Short Blocks
**What:** Transient mode uses multiple short MDCTs instead of one long MDCT
**When to use:** Frames with transient flag set

```go
// Source: RFC 6716 Section 4.3.5, libopus celt/celt_decoder.c

// For transient frames:
// - 20ms frame uses 8 x 120-sample short blocks
// - 10ms frame uses 4 x 120-sample short blocks
// - 5ms frame uses 2 x 120-sample short blocks
// - 2.5ms frame uses 1 x 120-sample short block (same as non-transient)

// The shortBlocks count is stored in ModeConfig:
// mode.ShortBlocks = 1,2,4,8 for LM=0,1,2,3

// Coefficients are interleaved for short blocks
// IMDCTShort handles the de-interleaving
```

### Pattern 4: Band Edge Scaling
**What:** Band edges scale proportionally with frame size
**When to use:** All band processing

```go
// Source: RFC 6716 Section 4.3.3, celt/tables.go

// eBands defines band edges at 2.5ms base (120 samples)
var EBands = [22]int{
    0, 1, 2, 3, 4, 5, 6, 7, 8, 10,
    12, 14, 16, 20, 24, 28, 34, 40, 48, 60,
    78, 100,
}

// For larger frame sizes, multiply by scale factor:
// scale = frameSize / 120
// ScaledBandStart(band, frameSize) = EBands[band] * scale

// Example: Band 20 (15.6-20kHz)
// - 2.5ms (120 samples): bins 78-100 (22 bins)
// - 20ms (960 samples): bins 624-800 (176 bins)

// CRITICAL: Total MDCT bins = EBands[maxBand+1] * scale
// For 20ms fullband: 100 * 8 = 800 bins
// But frame size is 960 samples...
```

### Anti-Patterns to Avoid
- **Assuming MDCT bins = frame size:** For shorter frames, fewer bands are coded
- **Ignoring EffBands for short frames:** 2.5ms has only 13 bands, not 21
- **Processing all 21 bands for short frames:** Will read past valid data
- **Not scaling band widths:** Band widths must scale with frame size
- **Missing sub-block state transfer:** SILK sub-blocks share prediction state

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Mode configuration | Custom frame size logic | Existing modeConfigs map | Already correctly defined |
| Band scaling | Manual multiplication | ScaledBandWidth/Start/End | Already implemented in tables.go |
| Sub-block decomposition | Novel approach | Existing getSubBlockCount | Already defined in frame.go |
| MDCT size selection | Hardcoded sizes | mode.MDCTSize | Correctly varies by frame |
| Short block count | Computed dynamically | mode.ShortBlocks | Pre-computed in mode configs |

**Key insight:** The infrastructure for extended frame sizes largely exists. The issue is likely in how the existing code is exercised or in edge cases for the shorter/longer frame paths.

## Common Pitfalls

### Pitfall 1: MDCT Bin Count vs Frame Size Mismatch
**What goes wrong:** Decoder produces wrong sample count (e.g., 1480 vs 960)
**Why it happens:** Confusion between:
- MDCT coefficient count (determined by bands and scaling)
- IMDCT output size (2x coefficient count)
- Expected frame size (determined by TOC)
**How to avoid:**
- MDCT coefficient count = sum of scaled band widths (may be less than frameSize)
- IMDCT produces 2x output, overlap-add reduces to frameSize
- Verify: `totalBins = ScaledBandEnd(nbBands-1, frameSize)`
**Warning signs:** Sample count mismatch, overlap-add errors

### Pitfall 2: Wrong EffBands for Short Frames
**What goes wrong:** Reading beyond allocated bands for 2.5ms/5ms frames
**Why it happens:** Using 21 bands when only 13 (2.5ms) or 17 (5ms) are valid
**How to avoid:** Use `mode.EffBands` from GetModeConfig(frameSize)
**Warning signs:** Range decoder errors, garbage in high bands

### Pitfall 3: Missing Alpha/Beta Coefficient Scaling
**What goes wrong:** Energy prediction uses wrong coefficients for frame size
**Why it happens:** Alpha/Beta coefficients are LM-dependent
**How to avoid:** Index AlphaCoef/BetaCoef arrays by mode.LM
**Warning signs:** Energy envelope discontinuities, pumping artifacts

### Pitfall 4: SILK Sub-Block State Not Transferred
**What goes wrong:** 40ms/60ms frames have discontinuities between sub-blocks
**Why it happens:** Each sub-block treated as independent frame
**How to avoid:** Maintain decoder state across sub-blocks within packet
**Warning signs:** Clicks at 20ms boundaries within long frames

### Pitfall 5: Transient Flag Handling for Short Frames
**What goes wrong:** Incorrect short block count for 2.5ms frames
**Why it happens:** 2.5ms with transient still uses 1 block (same as non-transient)
**How to avoid:** Check `if transient && lm >= 1` before using short blocks
**Warning signs:** Division errors, wrong coefficient layout

### Pitfall 6: Band Width of Zero for Uncoded Bands
**What goes wrong:** Folding tries to use bands that don't exist at short frame sizes
**Why it happens:** Band 18-20 have width 0 at 2.5ms
**How to avoid:** Check `ScaledBandWidth(band, frameSize) > 0` before processing
**Warning signs:** Index out of bounds, nil slice access

## Code Examples

### CELT Frame Size Detection and Configuration
```go
// Source: Verified from celt/modes.go, celt/decoder.go

func (d *Decoder) DecodeFrame(data []byte, frameSize int) ([]float64, error) {
    // Validate frame size (all four CELT sizes are valid)
    if !ValidFrameSize(frameSize) {
        return nil, ErrInvalidFrameSize
    }

    // Get mode configuration - this handles 120, 240, 480, 960
    mode := GetModeConfig(frameSize)
    nbBands := mode.EffBands  // 13, 17, 19, or 21
    lm := mode.LM             // 0, 1, 2, or 3

    // Alpha/beta for energy prediction depend on LM
    alpha := AlphaCoef[lm]
    beta := BetaCoef[lm]

    // ... rest of decoding
}
```

### SILK 40/60ms Decoding
```go
// Source: Verified from silk/frame.go, silk/decode.go

func (d *Decoder) DecodeFrame(
    rd *rangecoding.Decoder,
    bandwidth Bandwidth,
    duration FrameDuration,
    vadFlag bool,
) ([]float32, error) {
    config := GetBandwidthConfig(bandwidth)
    numSubframes := getSubframeCount(duration)  // 8 for 40ms, 12 for 60ms
    totalSamples := numSubframes * config.SubframeSamples
    output := make([]float32, totalSamples)

    // 40/60ms decode as multiple 20ms sub-blocks
    if is40or60ms(duration) {
        subBlocks := getSubBlockCount(duration)  // 2 or 3
        subBlockSamples := 4 * config.SubframeSamples  // 20ms worth

        for block := 0; block < subBlocks; block++ {
            blockOutput := output[block*subBlockSamples : (block+1)*subBlockSamples]
            // decode20msBlock maintains state across calls
            err := d.decode20msBlock(bandwidth, vadFlag, blockOutput)
            if err != nil {
                return nil, err
            }
        }
    } else {
        err := d.decodeBlock(bandwidth, vadFlag, duration, output)
        if err != nil {
            return nil, err
        }
    }

    d.haveDecoded = true
    return output, nil
}
```

### Band Processing for Variable Frame Sizes
```go
// Source: Verified from celt/bands.go, celt/tables.go

func (d *Decoder) DecodeBands(
    energies []float64,
    bandBits []int,
    nbBands int,      // Use mode.EffBands, not MaxBands
    stereo bool,
    frameSize int,
) []float64 {
    // Calculate total bins using effective band count for this frame size
    totalBins := 0
    for band := 0; band < nbBands; band++ {
        width := ScaledBandWidth(band, frameSize)
        if width <= 0 {
            break  // No more valid bands at this frame size
        }
        totalBins += width
    }

    coeffs := make([]float64, totalBins)
    offset := 0

    for band := 0; band < nbBands; band++ {
        n := ScaledBandWidth(band, frameSize)
        if n <= 0 {
            continue  // Skip zero-width bands
        }

        // Decode or fold this band...
        offset += n
    }

    return coeffs
}
```

### IMDCT Output Size Verification
```go
// Source: Derived from celt/mdct.go, celt/synthesis.go

// CRITICAL: IMDCT output is 2x input size
// overlap-add then reduces to frameSize output

func (d *Decoder) Synthesize(coeffs []float64, transient bool, shortBlocks int) []float64 {
    // coeffs length = totalBins (scaled band widths sum)
    // For 20ms fullband: totalBins = 800 (EBands[21] * 8)
    // IMDCT produces 2*800 = 1600 samples
    // Overlap-add with 120-sample overlap produces: 1600 - 120 = 1480

    // This is WRONG if we expect 960 samples!
    // The issue: we're computing MDCT bins based on bands, not frame size

    // CORRECT approach: MDCT size should equal frameSize for non-transient
    // coeffs should have frameSize elements, not totalBins

    var imdctOut []float64
    if transient && shortBlocks > 1 {
        imdctOut = IMDCTShort(coeffs, shortBlocks)
    } else {
        imdctOut = IMDCT(coeffs)  // Produces 2*len(coeffs) samples
    }

    // Apply window and overlap-add
    // Output should be frameSize samples (minus overlap from first frame)
    return d.WindowAndOverlap(imdctOut)
}
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Fixed 20ms frames only | Variable 2.5-60ms | Opus RFC 6716 (2012) | Low latency options |
| Single MDCT size | LM-scaled MDCT | Opus design | Frame size flexibility |
| Fixed band count | EffBands per frame size | Opus design | Efficient short frames |
| 20ms SILK blocks | Multi-block 40/60ms | Opus design | Lower overhead for long frames |

**Current implementation status:**
- CELT modes.go: Already defines 2.5ms and 5ms configurations
- SILK frame.go: Already defines 40ms and 60ms constants and helpers
- Hybrid decoder: Restricts to 10ms/20ms - needs extension
- Band processing: May not correctly handle reduced EffBands

## Open Questions

### 1. MDCT Bin Count Investigation
**What we know:** Known gap mentions "MDCT bin count (800) doesn't match frame size (960)"
**What's unclear:** Root cause of this mismatch
**Investigation needed:**
- Is the MDCT operating on band coefficients (800) or frame samples (960)?
- Does overlap-add correctly reconstruct to 960 samples?
- Is there zero-padding or truncation happening?
**Recommendation:** Trace through synthesis path for a 20ms frame, verify sample counts at each stage

### 2. Test Vector Frame Size Distribution
**What we know:** Test vectors use 2.5ms, 5ms, 40ms, 60ms according to verification report
**What's unclear:** Which specific vectors use which frame sizes
**Investigation needed:** Parse test vectors and log frame sizes
**Recommendation:** Add diagnostic output to compliance test showing frame sizes encountered

### 3. SILK Sub-Block LBRR Handling
**What we know:** LBRR (Low Bit Rate Redundancy) frames appear before regular SILK frames
**What's unclear:** How LBRR interacts with 40/60ms sub-block decoding
**Recommendation:** Initially skip LBRR as pion/opus does; add later if needed for PLC

### 4. Hybrid Mode Extended Frame Sizes
**What we know:** Hybrid mode per RFC only supports 10ms/20ms
**What's unclear:** Why test vectors might contain hybrid + extended sizes
**Recommendation:** Verify that extended frame sizes only appear in SILK-only or CELT-only modes

## MDCT/Synthesis Deep Dive

### Expected Sample Counts
```
Frame Size | MDCT Input | IMDCT Output | After Overlap-Add
-----------|------------|--------------|------------------
120 (2.5ms)|    120     |     240      | 120 (first frame)
240 (5ms)  |    240     |     480      | 240 (steady state)
480 (10ms) |    480     |     960      | 480
960 (20ms) |    960     |    1920      | 960
```

### Known Gap Analysis
The gap states "800 vs 960 mismatch". 800 = EBands[21] * 8 = 100 * 8.

This suggests the MDCT is being fed 800 coefficients (sum of band widths) instead of 960 (frame size). The IMDCT of 800 produces 1600, and after overlap-add minus 120 = 1480.

**Root cause hypothesis:** The `DecodeBands` function returns `totalBins` coefficients based on band widths, but synthesis expects `frameSize` coefficients.

**Fix approach:** Either:
1. Zero-pad band coefficients to frameSize before IMDCT, or
2. Modify IMDCT/synthesis to work with band-based coefficient counts

The correct approach per libopus is that MDCT size equals frameSize, and bands are laid out within that space with the upper bins (800-960 for 20ms) representing the highest frequencies.

## Sources

### Primary (HIGH confidence)
- [RFC 6716 Definition of the Opus Audio Codec](https://datatracker.ietf.org/doc/html/rfc6716) - Official specification
- Existing codebase: celt/modes.go, celt/tables.go, silk/frame.go, silk/decode.go
- [libopus celt/modes.c](https://github.com/xiph/opus/blob/main/celt/modes.c) - Reference MDCT configuration
- [libopus silk/dec_API.c](https://github.com/xiph/opus/blob/main/silk/dec_API.c) - Reference SILK multi-frame handling

### Secondary (MEDIUM confidence)
- [RFC 8251 Test Vectors](https://opus-codec.org/docs/opus_testvectors-rfc8251.tar.gz) - Compliance test data
- Phase 12 verification report - Documents frame size failures
- [Opus Wikipedia](https://en.wikipedia.org/wiki/Opus_(audio_format)) - Frame size overview

### Tertiary (LOW confidence)
- WebSearch results on CELT MDCT configuration
- arXiv paper on Opus CELT (binary PDF, not fully parsed)

## Metadata

**Confidence breakdown:**
- Frame size constants: HIGH - Verified in existing code and RFC
- SILK 40/60ms handling: HIGH - Code path exists, needs verification
- CELT 2.5/5ms handling: HIGH - Mode configs exist, need exercising
- MDCT bin count issue: MEDIUM - Root cause hypothesized but not verified
- Test vector specifics: MEDIUM - Known to use extended sizes, details unclear

**Research date:** 2026-01-23
**Valid until:** 2026-04-23 (stable RFC, unlikely to change)
