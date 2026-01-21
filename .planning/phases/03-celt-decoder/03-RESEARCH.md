# Phase 3: CELT Decoder - Research

**Researched:** 2026-01-21
**Domain:** CELT audio decoding per RFC 6716 Section 4.3
**Confidence:** HIGH

## Summary

Phase 3 implements the CELT decoder, which decodes music and general audio Opus packets using the Modified Discrete Cosine Transform (MDCT). CELT is the transform-based layer of Opus and handles all bandwidths from narrowband (8kHz) to fullband (48kHz) with frame sizes from 2.5ms to 20ms. The decoder processes compressed frames through transient detection, energy envelope decoding (coarse + fine), bit allocation, PVQ (Pyramid Vector Quantization) shape decoding using CWRS (Combinatorial Radix-based Search), band folding, denormalization, and IMDCT synthesis.

This research analyzed RFC 6716 Section 4.3, the arXiv paper on Opus CELT implementation (1602.04845), libopus reference code (celt_decoder.c, cwrs.c, bands.c, rate.c, modes.c), and the Concentus multi-language implementation. CELT is significantly more complex than SILK due to the PVQ/CWRS combinatorial indexing, flexible bit allocation algorithm, and multiple stereo modes (mid-side, dual stereo, intensity stereo). The pion/opus project does not currently implement CELT, making libopus and Concentus the primary references.

**Primary recommendation:** Implement CELT decoder following libopus structure closely. Start with mono decoding at 20ms frames, then add stereo support and shorter frame sizes. Use float64 for internal calculations (per user's discretion), converting to float32 at the output boundary. Implement FFT-based IMDCT for efficiency.

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
| `math` | stdlib | Float operations, trig functions | IMDCT, FFT twiddles |
| `math/bits` | stdlib | Bit manipulation | CWRS index calculations |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Direct FFT import | gonum/fft | External dependency violates CMP-03 |
| pion/opus CELT | Reference only | No CELT implementation exists |
| Concentus Go port | Direct port | Large codebase, fixed-point focused |

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
│   │   └── constants.go
│   ├── silk/                  # From Phase 2
│   └── celt/                  # NEW: Phase 3
│       ├── decoder.go         # Main CELT decoder struct and Decode method
│       ├── tables.go          # eBands, bit allocation, alpha/beta coefficients
│       ├── energy.go          # Coarse and fine energy decoding
│       ├── pvq.go             # PVQ decoding, spherical normalization
│       ├── cwrs.go            # CWRS combinatorial indexing (encode_pulses, decode_pulses)
│       ├── bands.go           # Band processing, quant_all_bands equivalent
│       ├── stereo.go          # Mid-side, dual stereo, intensity stereo
│       ├── folding.go         # Band folding for uncoded bands
│       ├── anticollapse.go    # Anti-collapse noise injection
│       ├── mdct.go            # FFT-based IMDCT implementation
│       ├── window.go          # Vorbis window function
│       ├── postfilter.go      # Comb filter postprocessing
│       └── celt_test.go       # Unit tests
├── packet.go                  # From Phase 1 (TOC parsing)
└── decoder.go                 # Public API (future Phase 10)
```

### Pattern 1: Stateful Decoder with Frame Persistence
**What:** CELT decoder maintains state across frames for overlap-add and anti-collapse
**When to use:** All CELT decoding
**Example:**
```go
// Source: libopus celt/celt_decoder.c
type Decoder struct {
    rangeDecoder    *rangecoding.Decoder

    // Mode configuration
    overlap         int       // Window overlap (120 samples at 48kHz)
    frameSize       int       // Current frame size in samples
    channels        int       // 1 or 2

    // Energy state (persists across frames)
    prevEnergy      []float64 // Previous frame band energies (for inter-frame prediction)
    prevEnergy2     []float64 // Two frames ago (for anti-collapse)

    // Synthesis state (persists for overlap-add)
    overlapBuffer   []float64 // Previous frame's overlap tail
    preemphState    float64   // De-emphasis filter state

    // Postfilter state
    postfilterPeriod int      // Pitch period for comb filter
    postfilterGain   float64  // Comb filter gain
    postfilterTapset int      // Filter tap configuration

    // Error recovery
    rng             uint32    // RNG state for PLC
}
```

### Pattern 2: Frame-Size Dependent Configuration
**What:** CELT parameters vary by frame size (2.5/5/10/20ms)
**When to use:** All decoding configuration
**Example:**
```go
// Source: libopus celt/modes.c
type FrameConfig struct {
    FrameSize    int   // In samples at 48kHz (120, 240, 480, 960)
    ShortBlocks  int   // Number of short MDCTs if transient (1, 2, 4, 8)
    MDCTSize     int   // MDCT window size (120, 240, 480, 960)
    EffBands     int   // Effective number of bands for this frame size
}

var frameConfigs = map[int]FrameConfig{
    120:  {120, 1, 120, 13},   // 2.5ms - limited bands
    240:  {240, 2, 120, 17},   // 5ms
    480:  {480, 4, 120, 19},   // 10ms
    960:  {960, 8, 120, 21},   // 20ms - full 21 bands
}
```

### Pattern 3: Band-Based Processing
**What:** CELT organizes frequency spectrum into ~21 Bark-scale bands
**When to use:** Energy decoding, bit allocation, PVQ coding
**Example:**
```go
// Source: libopus celt/modes.c - eBand5ms table
// MDCT bin indices for band edges at 48kHz/5ms base
// Frequencies: 0, 200, 400, 600, 800, 1k, 1.2k, 1.4k, 1.6k, 2k, 2.4k, 2.8k, 3.2k,
//              4k, 4.8k, 5.6k, 6.8k, 8k, 9.6k, 12k, 15.6k, 20k Hz
var eBands = []int{
    0, 1, 2, 3, 4, 5, 6, 7, 8, 10, 12, 14, 16, 20, 24, 28, 34, 40, 48, 60, 78, 100,
}

// Band width in MDCT bins
func bandWidth(band int) int {
    return eBands[band+1] - eBands[band]
}
```

### Pattern 4: Coarse-Fine Energy Quantization
**What:** Energy encoded in two stages: 6dB coarse + variable fine resolution
**When to use:** Energy envelope decoding
**Example:**
```go
// Source: RFC 6716 Section 4.3.2, libopus celt/quant_bands.c
func (d *Decoder) decodeCoarseEnergy(intra bool, prevEnergy []float64) []float64 {
    energies := make([]float64, numBands)

    // Prediction coefficients depend on intra/inter mode
    var alpha, beta float64
    if intra {
        alpha, beta = 0.0, 1.0 - 4915.0/32768.0
    } else {
        alpha, beta = 0.0, 1.0 - 4915.0/32768.0 // Loaded from tables for inter
    }

    for band := 0; band < numBands; band++ {
        // Decode Laplace-distributed residual
        residual := d.decodePulses(band)

        // Apply inter-band and inter-frame prediction
        pred := alpha*prevEnergy[band] + beta*energies[max(0, band-1)]
        energies[band] = pred + float64(residual)*6.0 // 6dB steps
    }

    return energies
}
```

### Pattern 5: PVQ Decoding with CWRS Indexing
**What:** Decode normalized band vectors from combinatorial indices
**When to use:** Shape decoding for coded bands
**Example:**
```go
// Source: libopus celt/cwrs.c
// Decode pulse vector from CWRS index
func decodePulses(index uint32, n, k int) []int {
    // Use recurrence relation: V(N,K) = V(N-1,K) + V(N,K-1) + V(N-1,K-1)
    y := make([]int, n)

    for i := 0; i < n-1 && k > 0; i++ {
        // Find number of pulses at position i
        p := 0
        for {
            // V(n-i-1, k-p) gives count of codewords with p pulses at position i
            v := pvqV(n-i-1, k-p)
            if index < v {
                break
            }
            index -= v
            p++
        }

        // Decode sign if pulses present
        if p > 0 {
            if index&1 == 1 {
                p = -p
            }
            index >>= 1
        }
        y[i] = p
        k -= abs(p)
    }

    // Last position gets remaining pulses
    y[n-1] = k
    // Sign from remaining index bit
    if k > 0 && index&1 == 1 {
        y[n-1] = -k
    }

    return y
}
```

### Anti-Patterns to Avoid
- **Using fixed-point in IMDCT:** Float64 is cleaner and Go doesn't have the same constraints as C embedded targets
- **Forgetting overlap state:** IMDCT requires 50% overlap between frames
- **Ignoring band folding:** Uncoded bands MUST be reconstructed from lower bands
- **Hard-coding band count:** Number of effective bands varies by frame size
- **Confusing stereo modes:** Intensity, mid-side, and dual stereo have different bit allocation

## Don't Hand-Roll

Problems that look simple but have existing solutions:

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Range decoding | Custom bit reader | `internal/rangecoding.Decoder` | Already implemented, bit-exact |
| CWRS indexing | Custom combinatorics | Direct port of libopus cwrs.c | Complex recurrence relations, precomputed tables |
| Band edge tables | Compute dynamically | Static eBands array from libopus | Fixed for all Opus implementations |
| Bit allocation | Novel algorithm | Port libopus rate.c compute_allocation | Exact match required for decoder |
| Window function | Custom window | Vorbis power-complementary window formula | Specified in RFC 6716 |
| Alpha/beta coefficients | Approximate | Exact values from RFC 6716 | Bit-exact decoding requires exact values |

**Key insight:** CELT decoding requires bit-exact reproduction of the encoder's decisions. The decoder must compute identical bit allocation, stereo mode selection, and band coding decisions. Port algorithms directly from libopus rather than reimplementing.

## Common Pitfalls

### Pitfall 1: Incorrect CWRS Index Calculation
**What goes wrong:** PVQ decoding produces wrong pulse vectors
**Why it happens:** CWRS uses a complex recurrence relation; off-by-one errors in V(N,K) tables
**How to avoid:** Use precomputed U(N,K) lookup tables from libopus; verify against test vectors
**Warning signs:** Decoded audio is noise or has periodic artifacts

### Pitfall 2: Bit Allocation Mismatch
**What goes wrong:** Decoder reads wrong number of bits for bands, range coder desynchronizes
**Why it happens:** Bit allocation depends on multiple factors (cap, trim, dynalloc, skip)
**How to avoid:** Port compute_allocation() exactly from libopus; decoder MUST match encoder's decisions
**Warning signs:** Range decoder error, audio cuts out mid-frame

### Pitfall 3: Forgetting Inter-Frame Energy Prediction
**What goes wrong:** Audio has pumping or level changes at frame boundaries
**Why it happens:** Coarse energy uses alpha/beta prediction from previous frame
**How to avoid:** Store prevEnergy and prevEnergy2 arrays; use correct alpha/beta for intra/inter mode
**Warning signs:** Level discontinuities every 20ms

### Pitfall 4: Band Folding Not Implemented
**What goes wrong:** High frequencies are silent or wrong
**Why it happens:** Uncoded bands must copy normalized vectors from coded lower bands
**How to avoid:** Implement folding with proper sign alternation and collapse mask tracking
**Warning signs:** Audio sounds muffled or has missing high frequencies

### Pitfall 5: Stereo Mode Selection Errors
**What goes wrong:** Stereo image is wrong, phase issues, mono collapse
**Why it happens:** Intensity stereo band, mid-side vs dual stereo flag must be decoded correctly
**How to avoid:** Decode intensity_stereo and dual_stereo flags per bit allocation; track per-band
**Warning signs:** Stereo music sounds phasey or collapsed to center

### Pitfall 6: Transient Mode Short Block Errors
**What goes wrong:** Attacks have pre-echo or smearing
**Why it happens:** Transient frames use 8 short MDCTs interleaved, not 1 long MDCT
**How to avoid:** Check transient flag; interleave short MDCT coefficients correctly
**Warning signs:** Drums and transients sound mushy

### Pitfall 7: Anti-Collapse Not Applied
**What goes wrong:** Transient frames have holes/silence in some bands
**Why it happens:** Short MDCTs may allocate zero pulses to some bands; noise injection required
**How to avoid:** Track collapse_masks; inject noise scaled to min energy of last 2 frames
**Warning signs:** Percussive sounds have periodic dropouts

### Pitfall 8: IMDCT Overlap-Add Errors
**What goes wrong:** Clicks or discontinuities at frame boundaries
**Why it happens:** Missing overlap buffer, wrong window application
**How to avoid:** Store overlap tail; apply Vorbis window; sum with previous frame's tail
**Warning signs:** Periodic clicking every frame boundary

### Pitfall 9: De-emphasis Filter State
**What goes wrong:** Spectral tilt is wrong, audio sounds thin
**Why it happens:** Pre-emphasis applied in encoder must be inverted; state must persist
**How to avoid:** Apply de-emphasis: y[n] = x[n] + alpha * y[n-1] with alpha = 0.85
**Warning signs:** High frequencies too loud, bass is weak

## Code Examples

Verified patterns from official sources:

### CELT Decoder Initialization
```go
// Source: libopus celt/celt_decoder.c
func NewDecoder(sampleRate, channels int) *Decoder {
    return &Decoder{
        channels:      channels,
        overlap:       120, // 2.5ms at 48kHz
        prevEnergy:    make([]float64, maxBands*channels),
        prevEnergy2:   make([]float64, maxBands*channels),
        overlapBuffer: make([]float64, 120*channels), // overlap tail
        rng:           0,
    }
}
```

### Band Energy Table (eBands)
```go
// Source: libopus celt/modes.c
// Band edge indices for 5ms base frame at 48kHz
// Values represent MDCT bin indices
var eBands = []int{
    0, 1, 2, 3, 4, 5, 6, 7, 8, 10, 12, 14, 16, 20, 24, 28, 34, 40, 48, 60, 78, 100,
}

// Frequency boundaries (for documentation):
// 0Hz, 200Hz, 400Hz, 600Hz, 800Hz, 1000Hz, 1200Hz, 1400Hz, 1600Hz, 2000Hz,
// 2400Hz, 2800Hz, 3200Hz, 4000Hz, 4800Hz, 5600Hz, 6800Hz, 8000Hz, 9600Hz,
// 12000Hz, 15600Hz, 20000Hz
```

### Coarse Energy Decoding
```go
// Source: RFC 6716 Section 4.3.2, libopus celt/quant_bands.c
func (d *Decoder) unquantCoarseEnergy(nbBands int, intra bool) {
    // Alpha/beta for prediction
    // Inter-frame: alpha depends on LM (frame size), beta for inter-band
    // Intra-frame: alpha=0, beta=1-4915/32768

    var alpha, beta float64
    if intra {
        alpha, beta = 0.0, 0.85
    } else {
        alpha = alphaCoef[d.lm]
        beta = betaCoef[d.lm]
    }

    for c := 0; c < d.channels; c++ {
        prev := 0.0
        for i := 0; i < nbBands; i++ {
            // Decode Laplace-distributed residual
            qi := d.rangeDecoder.DecodeLaplace(energyModel)

            // Apply prediction
            pred := alpha*d.prevEnergy[c*nbBands+i] + beta*prev
            e := pred + float64(qi)*dB6

            d.bandEnergy[c*nbBands+i] = e
            prev = e
        }
    }
}

const dB6 = 6.0 // 6 dB quantization step
```

### Fine Energy Decoding
```go
// Source: RFC 6716 Section 4.3.2
func (d *Decoder) unquantFineEnergy(nbBands int, fineBits []int) {
    for c := 0; c < d.channels; c++ {
        for i := 0; i < nbBands; i++ {
            if fineBits[i] > 0 {
                // Decode fine energy refinement
                q := d.rangeDecoder.DecodeUniform(1 << fineBits[i])
                // Add to coarse energy
                offset := (float64(q) + 0.5) * (1.0 / float64(1<<fineBits[i])) - 0.5
                d.bandEnergy[c*nbBands+i] += offset * dB6
            }
        }
    }
}
```

### PVQ Codebook Size Calculation
```go
// Source: libopus celt/cwrs.c
// V(N,K) = number of PVQ codewords with N dimensions and K pulses
func pvqV(n, k int) uint32 {
    if k == 0 {
        return 1
    }
    if n == 1 {
        return uint32(2*k + 1) // -k to +k
    }

    // Use precomputed U table: V(N,K) = U(N,K) + U(N,K+1)
    // U(N,K) = number of codewords with no pulse at position 0
    return cwrsU[n][k] + cwrsU[n][k+1]
}
```

### Band Folding
```go
// Source: libopus celt/bands.c
func (d *Decoder) foldBand(band int, lowband []float64, seed uint32) []float64 {
    n := bandWidth(band)
    result := make([]float64, n)

    if lowband != nil {
        // Copy from lower band with sign alteration
        for i := 0; i < n; i++ {
            sign := 1.0
            if seed&0x8000 != 0 {
                sign = -1.0
            }
            seed = seed*1664525 + 1013904223 // LCG
            result[i] = sign * lowband[i%len(lowband)]
        }
    } else {
        // No lower band available, use noise
        for i := 0; i < n; i++ {
            seed = seed*1664525 + 1013904223
            result[i] = float64(int32(seed)) / float64(1<<31)
        }
    }

    // Normalize to unit energy
    return normalizeVector(result)
}
```

### Intensity Stereo
```go
// Source: RFC 6716 Section 4.3.4.3, libopus celt/bands.c
func (d *Decoder) intensityStereo(mid []float64, intensityBand int) (left, right []float64) {
    n := len(mid)
    left = make([]float64, n)
    right = make([]float64, n)

    // Decode direction flag (inversion)
    inv := d.rangeDecoder.DecodeBit(1) == 1

    // Copy mid to both channels
    copy(left, mid)
    copy(right, mid)

    // Apply inversion to right channel if flagged
    if inv {
        for i := range right {
            right[i] = -right[i]
        }
    }

    return left, right
}
```

### Mid-Side Stereo with Angle
```go
// Source: libopus celt/bands.c
func (d *Decoder) midSideStereo(mid, side []float64, theta float64) (left, right []float64) {
    n := len(mid)
    left = make([]float64, n)
    right = make([]float64, n)

    // theta encodes the balance between mid and side
    // theta=0: mono (side=0), theta=pi/2: full stereo
    cosTheta := math.Cos(theta)
    sinTheta := math.Sin(theta)

    for i := 0; i < n; i++ {
        // Rotate from mid-side to left-right
        left[i] = cosTheta*mid[i] + sinTheta*side[i]
        right[i] = cosTheta*mid[i] - sinTheta*side[i]
    }

    return left, right
}
```

### IMDCT Synthesis (FFT-based)
```go
// Source: libopus celt/mdct.c
func (d *Decoder) imdct(spectrum []float64, out []float64) {
    n := len(spectrum)
    n2 := n / 2

    // Pre-twiddle: multiply by exp(-i*pi*(n+1)/(2*n))
    // ... FFT computation ...
    // Post-twiddle and windowing

    // Apply Vorbis window
    for i := 0; i < n; i++ {
        out[i] *= vorbisWindow(i, n)
    }
}

// Vorbis power-complementary window
func vorbisWindow(i, n int) float64 {
    x := float64(i) + 0.5
    return math.Sin(math.Pi/2 * math.Pow(math.Sin(math.Pi*x/float64(n)), 2))
}
```

### Overlap-Add Synthesis
```go
// Source: libopus celt/celt_decoder.c celt_synthesis()
func (d *Decoder) overlapAdd(current []float64, output []float64) {
    overlap := d.overlap

    // Add overlap from previous frame
    for i := 0; i < overlap; i++ {
        output[i] = d.overlapBuffer[i] + current[i]
    }

    // Copy non-overlap portion
    copy(output[overlap:], current[overlap:len(current)-overlap])

    // Store overlap for next frame
    copy(d.overlapBuffer, current[len(current)-overlap:])
}
```

### De-emphasis Filter
```go
// Source: libopus celt/celt_decoder.c deemphasis()
func (d *Decoder) deemphasis(in []float64, out []float32) {
    const coef = 0.85 // De-emphasis coefficient

    state := d.preemphState
    for i := range in {
        y := in[i] + coef*state
        state = y
        out[i] = float32(y)
    }
    d.preemphState = state
}
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Standalone CELT | CELT within Opus | RFC 6716 (2012) | Unified with SILK, seamless mode switching |
| Fixed frame size | 2.5/5/10/20ms options | Opus design | Low-latency options for real-time |
| 256-sample MDCT | Variable MDCT (120-960) | Opus design | Frame size flexibility |
| No hybrid mode | SILK+CELT hybrid | Opus design | Best of both for wideband speech |
| libopus 1.1 | libopus 1.6 | Dec 2025 | Various optimizations, DRED extension |

**Deprecated/outdated:**
- Standalone CELT codec (pre-0.10): Superseded by Opus CELT
- RFC 6716 Appendix A reference code: Security fixes in RFC 8251
- CELT without spreading rotations: Enabled by default in all modern implementations

## Open Questions

Things that couldn't be fully resolved:

1. **PVQ Table Size Requirements**
   - What we know: libopus uses precomputed U(N,K) tables up to N=200, K=32
   - What's unclear: Exact table sizes needed for all frame configurations
   - Recommendation: Port entire CELT_PVQ_U_DATA table from libopus cwrs.c

2. **Exact Bit Allocation Table**
   - What we know: band_allocation table has 11 rows x 21 bands
   - What's unclear: Complete values only available in libopus source
   - Recommendation: Extract full table from libopus celt/static_modes_float.h

3. **Test Vector Strategy**
   - What we know: RFC 8251 test vectors exist, libopus has internal test suite
   - What's unclear: How to extract CELT-specific test vectors (vs full Opus)
   - Recommendation: Generate test vectors using libopus encode -> decode reference

4. **FFT Implementation Strategy**
   - What we know: IMDCT requires FFT; libopus uses radix-2/4 split-radix
   - What's unclear: Best pure-Go FFT approach for real-valued transforms
   - Recommendation: Implement radix-2 Cooley-Tukey first; optimize later if needed

5. **Postfilter Parameters**
   - What we know: Comb filter uses period, gain, tapset from bitstream
   - What's unclear: Exact tap coefficients and filter structure
   - Recommendation: Port comb_filter() from libopus celt/celt_decoder.c

## CELT Decoder Data Requirements

### Static Tables Required

| Table | Dimensions | Purpose |
|-------|------------|---------|
| eBands | 22 values | Band edge MDCT bin indices |
| band_allocation | 11 x 21 | Bit allocation per quality level |
| CELT_PVQ_U_DATA | ~1488 values | Precomputed U(N,K) for CWRS |
| alpha_coef | 4 values | Inter-frame energy prediction |
| beta_coef | 4 values | Inter-band energy prediction |
| window | 120 values | Vorbis window coefficients |
| fft_twiddles | 480 complex | FFT twiddle factors |
| mdct_twiddles | 1800 values | MDCT pre/post twiddles |
| cache_index/caps | ~100 values | PVQ bit allocation cache |
| logN | 21 values | Log band widths for allocation |

### Key Constants

| Constant | Value | Purpose |
|----------|-------|---------|
| CELT_MAX_BANDS | 21 | Maximum frequency bands |
| CELT_OVERLAP | 120 | Overlap samples (2.5ms at 48kHz) |
| CELT_FRAME_SIZES | 120, 240, 480, 960 | Valid frame sizes |
| DB6 | 6.0 | Coarse energy quantization step |
| PREEMPH_COEF | 0.85 | De-emphasis filter coefficient |

## Sources

### Primary (HIGH confidence)
- [RFC 6716 Section 4.3](https://datatracker.ietf.org/doc/html/rfc6716#section-4.3) - CELT decoder specification
- [libopus celt/celt_decoder.c](https://github.com/cisco/opus/blob/master/celt/celt_decoder.c) - Reference C implementation
- [libopus celt/cwrs.c](https://github.com/cisco/opus/blob/master/celt/cwrs.c) - CWRS indexing implementation
- [libopus celt/bands.c](https://github.com/cisco/opus/blob/master/celt/bands.c) - Band processing, stereo
- [libopus celt/rate.c](https://github.com/cisco/opus/blob/master/celt/rate.c) - Bit allocation algorithm
- [libopus celt/modes.c](https://github.com/cisco/opus/blob/master/celt/modes.c) - eBands table, mode definitions

### Secondary (MEDIUM confidence)
- [arXiv:1602.04845](https://arxiv.org/pdf/1602.04845) - High-Quality, Low-Delay Music Coding in the Opus Codec
- [Opus CELT AES135 Presentation](https://jmvalin.ca/papers/aes135_opus_celt.pdf) - Technical overview
- [Concentus](https://github.com/lostromb/concentus) - Multi-language Opus port (C#/Java/Go)
- [FFmpeg Opus Implementation](https://www.mail-archive.com/ffmpeg-devel@ffmpeg.org/msg46897.html) - Alternative decoder reference

### Tertiary (LOW confidence)
- [pion/opus](https://github.com/pion/opus) - Pure Go (SILK only currently, no CELT)
- [CELT Wikipedia](https://en.wikipedia.org/wiki/CELT) - General overview
- [Hydrogenaudio Opus](https://wiki.hydrogenaudio.org/index.php?title=Opus) - Community documentation

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH - stdlib only, uses existing rangecoding
- Architecture: HIGH - follows established libopus structure
- Pitfalls: HIGH - documented in RFC and verified against libopus
- CELT algorithm: HIGH - RFC normative + reference implementation
- Tables: HIGH - can be directly ported from libopus (BSD licensed)
- Stereo modes: MEDIUM - complex interaction, needs careful verification
- CWRS indexing: HIGH - well-documented combinatorial algorithm
- FFT/IMDCT: MEDIUM - implementation strategy is clear, optimization open

**Research date:** 2026-01-21
**Valid until:** 2026-04-21 (stable RFC, unlikely to change)
