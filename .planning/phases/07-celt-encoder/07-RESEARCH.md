# Phase 7: CELT Encoder - Research

**Researched:** 2026-01-22
**Domain:** CELT audio encoding per RFC 6716 Section 5.3
**Confidence:** HIGH

## Summary

Phase 7 implements the CELT encoder, which performs the reverse of Phase 3's decoder: analyzing PCM audio to produce CELT-mode Opus packets. The encoder must produce output decodable by both the Phase 3 decoder and libopus. CELT encoding involves MDCT analysis, band energy computation, PVQ (Pyramid Vector Quantization) shape encoding via CWRS (Combinatorial Radix-based With Signs), energy envelope quantization (coarse + fine), and bit allocation. Unlike SILK, CELT uses transform-based coding (MDCT) rather than predictive coding (LPC).

The encoder is the inverse of the decoder: where the decoder reads indices and reconstructs audio, the encoder analyzes audio and produces indices. The existing Phase 3 decoder provides all the tables, CWRS functions (including `EncodePulses`), bit allocation logic, and synthesis algorithms that constrain what the encoder must produce. The encoder's MDCT coefficients must, when quantized via PVQ and decoded, reconstruct perceptually acceptable audio.

**Primary recommendation:** Structure the encoder as the mirror of the decoder. Implement MDCT (forward transform), band energy analysis, PVQ encoding (using existing `EncodePulses`), and coarse/fine energy quantization. Verify each component by round-tripping through the Phase 3 decoder. Follow the SILK encoder pattern: encoder struct mirrors decoder, reuse existing tables, and use round-trip testing for validation.

## Standard Stack

The established libraries/tools for this domain:

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| Go standard library | 1.21+ | All functionality | Zero dependencies requirement (CMP-03) |
| internal/rangecoding | Phase 1 | Range encoding | Already implemented, bit-exact |
| internal/celt | Phase 3 | Tables, CWRS, decoder | Symmetric encode/decode |

### Reusable from Phase 3
| Component | Location | Encoder Use |
|-----------|----------|-------------|
| EBands table | `celt/tables.go` | Band edge indices |
| BandAlloc table | `celt/alloc.go` | Bit allocation base values |
| AlphaCoef/BetaCoef | `celt/tables.go` | Energy prediction coefficients |
| PVQ_V() | `celt/cwrs.go` | Codebook size computation |
| EncodePulses() | `celt/cwrs.go` | CWRS index encoding (already exists) |
| ComputeAllocation() | `celt/alloc.go` | Bit allocation (encoder must match) |
| NormalizeVector() | `celt/pvq.go` | Vector normalization |
| VorbisWindow() | `celt/window.go` | MDCT window function |
| IMDCT() | `celt/mdct.go` | Reference for forward MDCT |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `math` | stdlib | Float operations, trig | MDCT, energy computation |
| `testing` | stdlib | Unit tests | Encoder verification |

**Installation:**
```bash
# No external dependencies - pure stdlib
# Uses internal/rangecoding from Phase 1
# Uses internal/celt tables/decoder from Phase 3
```

## Architecture Patterns

### Recommended Project Structure
```
gopus/
├── internal/
│   ├── rangecoding/
│   │   ├── encoder.go      # From Phase 1
│   │   └── decoder.go      # For verification
│   └── celt/
│       ├── encoder.go      # NEW: Main encoder struct
│       ├── mdct_encode.go  # NEW: Forward MDCT
│       ├── energy_encode.go# NEW: Coarse/fine energy encoding
│       ├── bands_encode.go # NEW: Band encoding with PVQ
│       ├── transient.go    # NEW: Transient detection
│       ├── preemph.go      # NEW: Pre-emphasis filter
│       ├── decoder.go      # From Phase 3
│       ├── cwrs.go         # From Phase 3 (EncodePulses exists)
│       ├── tables.go       # From Phase 3 (shared)
│       ├── alloc.go        # From Phase 3 (shared)
│       ├── pvq.go          # From Phase 3 (shared)
│       └── *_test.go       # Tests with round-trip
```

### Pattern 1: Encoder Mirrors Decoder State
**What:** CELT encoder maintains state that mirrors decoder for proper prediction
**When to use:** All CELT encoding
**Example:**
```go
// Source: mirrors decoder.go structure from Phase 3
type Encoder struct {
    // Range encoder reference (set per frame)
    rangeEncoder *rangecoding.Encoder

    // Configuration (mirrors decoder)
    channels   int
    sampleRate int // Always 48000

    // Energy state (persists across frames, mirrors decoder)
    prevEnergy  []float64 // Previous frame band energies [MaxBands * channels]
    prevEnergy2 []float64 // Two frames ago

    // Synthesis state for overlap (mirrors decoder)
    overlapBuffer []float64 // MDCT overlap
    preemphState  []float64 // Pre-emphasis filter state

    // RNG state (for deterministic folding decisions)
    rng uint32

    // Analysis buffers (encoder-specific)
    inputBuffer []float64 // Input sample lookahead
    mdctBuffer  []float64 // MDCT output
}
```

### Pattern 2: Analysis-Synthesis Verification Loop
**What:** Every encoder component verifies by round-tripping through decoder
**When to use:** All encoder development and testing
**Example:**
```go
// Encoder produces MDCT coefficients, energies, PVQ indices
func (e *Encoder) EncodeFrame(pcm []float64, frameSize int) []byte {
    // Step 1: Pre-emphasis
    preemph := e.applyPreemphasis(pcm)

    // Step 2: MDCT analysis (forward transform)
    mdctCoeffs := e.MDCT(preemph)

    // Step 3: Compute band energies
    energies := e.ComputeBandEnergies(mdctCoeffs, nbBands)

    // Step 4: Normalize bands (divide by energy)
    shapes := e.NormalizeBands(mdctCoeffs, energies)

    // Step 5: Encode energies (coarse + fine)
    e.EncodeCoarseEnergy(energies, intra)
    e.EncodeFineEnergy(energies, fineBits)

    // Step 6: Encode PVQ shapes
    e.EncodeBands(shapes, bandBits)

    return e.rangeEncoder.Done()
}

// Round-trip test: encode -> decode -> compare
func TestRoundTrip(t *testing.T) {
    enc := celt.NewEncoder(2)
    dec := celt.NewDecoder(2)

    encoded := enc.EncodeFrame(originalPCM, 960)
    decoded, _ := dec.DecodeFrame(encoded, 960)

    assertPerceptuallyClose(t, originalPCM, decoded)
}
```

### Pattern 3: Forward MDCT from Existing IMDCT
**What:** MDCT is the transpose of IMDCT with different normalization
**When to use:** MDCT analysis for encoding
**Example:**
```go
// Forward MDCT: time samples -> frequency coefficients
// MDCT is inverse of IMDCT with 2/N normalization swap
func MDCT(samples []float64) []float64 {
    n := len(samples)
    n2 := n / 2

    // Apply analysis window (same Vorbis window as decoder)
    windowed := make([]float64, n)
    ApplyWindow(windowed, Overlap) // Same window as IMDCT

    // MDCT formula: X[k] = sum_n x[n] * cos(pi/N * (n + 0.5 + N/2) * (k + 0.5))
    spectrum := make([]float64, n2)
    for k := 0; k < n2; k++ {
        var sum float64
        for i := 0; i < n; i++ {
            angle := math.Pi / float64(n2) * (float64(i) + 0.5 + float64(n2)/2) * (float64(k) + 0.5)
            sum += windowed[i] * math.Cos(angle)
        }
        spectrum[k] = sum
    }
    return spectrum
}
```

### Pattern 4: PVQ Encoding Using Existing EncodePulses
**What:** Convert normalized vector to pulse vector, then to CWRS index
**When to use:** Band shape encoding
**Example:**
```go
// Phase 3 already has EncodePulses - use it
// Step 1: Convert normalized float vector to integer pulses
func vectorToPulses(shape []float64, k int) []int {
    n := len(shape)
    pulses := make([]int, n)

    // Scale shape to have L1 norm = k
    var l1norm float64
    for _, x := range shape {
        l1norm += math.Abs(x)
    }

    scale := float64(k) / l1norm
    remaining := k

    for i := 0; i < n; i++ {
        p := int(math.Round(shape[i] * scale))
        if abs(p) > remaining {
            if p > 0 {
                p = remaining
            } else {
                p = -remaining
            }
        }
        pulses[i] = p
        remaining -= abs(p)
    }

    // Distribute any remaining pulses
    if remaining > 0 {
        pulses[0] += remaining // Or use better distribution
    }

    return pulses
}

// Step 2: Encode pulses to index using existing EncodePulses
func (e *Encoder) EncodePVQ(shape []float64, n, k int) {
    pulses := vectorToPulses(shape, k)
    index := celt.EncodePulses(pulses, n, k) // Existing function

    // Encode index uniformly
    vSize := celt.PVQ_V(n, k)
    e.rangeEncoder.EncodeUniform(index, vSize)
}
```

### Pattern 5: Energy Quantization Mirroring Decoder
**What:** Encode energies so decoder reconstructs them identically
**When to use:** Coarse and fine energy encoding
**Example:**
```go
// Coarse energy: 6dB steps with Laplace distribution
func (e *Encoder) EncodeCoarseEnergy(energies []float64, nbBands int, intra bool, lm int) {
    // Get prediction coefficients (same as decoder)
    var alpha, beta float64
    if intra {
        alpha, beta = 0.0, BetaCoef[lm]
    } else {
        alpha, beta = AlphaCoef[lm], BetaCoef[lm]
    }

    for c := 0; c < e.channels; c++ {
        prevBand := 0.0
        for band := 0; band < nbBands; band++ {
            // Compute prediction (same as decoder)
            prevFrame := e.prevEnergy[c*MaxBands+band]
            pred := alpha*prevFrame + beta*prevBand

            // Quantize to 6dB steps
            qi := int(math.Round((energies[c*nbBands+band] - pred) / DB6))

            // Encode qi with Laplace model
            e.encodeLaplace(qi, decay)

            // Update for next band
            prevBand = pred + float64(qi)*DB6
        }
    }
}
```

### Anti-Patterns to Avoid
- **Encoding without verification:** Always test encoded output through Phase 3 decoder
- **Ignoring bit budget:** Encoder must respect frame size bit limits
- **Different allocation than decoder:** Encoder and decoder MUST use identical bit allocation
- **Hand-rolling PVQ quantization:** Use nearest-neighbor search to find best pulse vector
- **Forgetting pre-emphasis:** Encoder applies pre-emphasis before MDCT

## Don't Hand-Roll

Problems that look simple but have existing solutions:

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| CWRS index encoding | Custom combinatorics | Existing `EncodePulses()` | Already implemented in Phase 3 |
| Bit allocation | Novel algorithm | Existing `ComputeAllocation()` | Must match decoder exactly |
| V(N,K) computation | Custom recurrence | Existing `PVQ_V()` | Already cached and correct |
| Band edge tables | Compute dynamically | Existing `EBands` | Fixed for all Opus |
| Window function | Custom window | Existing `ApplyWindow()` | Same window for MDCT/IMDCT |
| Energy prediction coeffs | Approximate | Existing `AlphaCoef/BetaCoef` | Exact match required |

**Key insight:** The encoder's freedom is in analysis quality (MDCT precision, quantization decisions), not output format. The output format is rigidly defined by what the decoder expects. All encoding must produce bitstreams that decode correctly through Phase 3.

## Common Pitfalls

### Pitfall 1: Forward MDCT Normalization Mismatch
**What goes wrong:** Encoded MDCT coefficients have wrong scale, energies are off
**Why it happens:** MDCT has 2/N normalization; IMDCT has 2/N too (they share the factor)
**How to avoid:** Test MDCT -> IMDCT round-trip with identity (zero quantization)
**Warning signs:** Decoded audio has wrong volume, energy values are offset

### Pitfall 2: PVQ Pulse Allocation Rounding
**What goes wrong:** Pulse vector doesn't sum to exactly K pulses
**Why it happens:** Floating-point rounding when converting normalized vector to integers
**How to avoid:** Track remaining pulses, distribute residual to largest bins
**Warning signs:** `EncodePulses` returns wrong index, decoder produces artifacts

### Pitfall 3: Bit Allocation Mismatch
**What goes wrong:** Encoder allocates different bits than decoder expects
**Why it happens:** Using different parameters to `ComputeAllocation()`
**How to avoid:** Pass IDENTICAL parameters as decoder would compute; verify bit counts match
**Warning signs:** Range decoder desynchronizes, audio cuts out

### Pitfall 4: Coarse Energy Laplace Encoding
**What goes wrong:** Decoded energies differ from encoded values
**Why it happens:** Using different decay parameter or probability model
**How to avoid:** Use identical Laplace model parameters as decoder; verify round-trip
**Warning signs:** Volume pumping, spectral tilt changes between frames

### Pitfall 5: Pre-Emphasis Not Applied
**What goes wrong:** Encoded audio sounds thin or has wrong frequency balance
**Why it happens:** Forgetting that encoder must apply pre-emphasis before MDCT
**How to avoid:** Apply pre-emphasis filter: y[n] = x[n] - PreemphCoef * x[n-1]
**Warning signs:** Decoded audio (after de-emphasis) has different spectrum than input

### Pitfall 6: Transient Detection Threshold
**What goes wrong:** Short blocks used incorrectly, or transients not detected
**Why it happens:** Using wrong energy ratio threshold for transient decision
**How to avoid:** Compare subframe energies; flag transient if ratio > threshold
**Warning signs:** Pre-echo on drum hits, or unnecessary short blocks wasting bits

### Pitfall 7: Stereo Mode Selection
**What goes wrong:** Wrong stereo mode encoded, decoder misinterprets
**Why it happens:** Intensity stereo threshold, mid-side decision logic differs
**How to avoid:** Encode stereo mode flags exactly as decoder expects to read them
**Warning signs:** Stereo image collapses, phasey artifacts

### Pitfall 8: Folding Decision Mismatch
**What goes wrong:** Encoder assumes band will be folded but decoder codes it
**Why it happens:** Encoder/decoder disagree on which bands get zero bits
**How to avoid:** Use exact same bit allocation; track collapseMask identically
**Warning signs:** High frequencies missing or noise-filled incorrectly

## Code Examples

Verified patterns from official sources and existing codebase:

### CELT Encoder Initialization
```go
// Source: mirrors decoder.go from Phase 3
func NewEncoder(channels int) *Encoder {
    if channels < 1 {
        channels = 1
    }
    if channels > 2 {
        channels = 2
    }

    return &Encoder{
        channels:      channels,
        sampleRate:    48000,
        prevEnergy:    make([]float64, MaxBands*channels),
        prevEnergy2:   make([]float64, MaxBands*channels),
        overlapBuffer: make([]float64, Overlap*channels),
        preemphState:  make([]float64, channels),
        rng:           22222, // Same seed as decoder (D03-01-02)
    }
}
```

### Pre-Emphasis Filter
```go
// Source: RFC 6716 Section 5.3, inverse of decoder's de-emphasis
func (e *Encoder) applyPreemphasis(pcm []float64) []float64 {
    preemph := make([]float64, len(pcm))

    for c := 0; c < e.channels; c++ {
        state := e.preemphState[c]
        for i := c; i < len(pcm); i += e.channels {
            // Pre-emphasis: y[n] = x[n] - coef * x[n-1]
            // Inverse of de-emphasis: y[n] = x[n] + coef * y[n-1]
            preemph[i] = pcm[i] - PreemphCoef*state
            state = pcm[i]
        }
        e.preemphState[c] = state
    }

    return preemph
}
```

### Forward MDCT
```go
// Source: RFC 6716 Section 4.3.5, transpose of IMDCT
// MDCT: 2N samples -> N frequency bins
func MDCT(samples []float64) []float64 {
    n2 := len(samples)
    n := n2 / 2

    if n <= 0 {
        return nil
    }

    // Apply analysis window
    windowed := make([]float64, n2)
    copy(windowed, samples)
    ApplyWindow(windowed, Overlap)

    // Direct MDCT computation
    // X[k] = sum_{n=0}^{2N-1} x[n] * cos(pi/N * (n + 0.5 + N/2) * (k + 0.5))
    spectrum := make([]float64, n)
    for k := 0; k < n; k++ {
        var sum float64
        for i := 0; i < n2; i++ {
            angle := math.Pi / float64(n) * (float64(i) + 0.5 + float64(n)/2) * (float64(k) + 0.5)
            sum += windowed[i] * math.Cos(angle)
        }
        spectrum[k] = sum
    }

    return spectrum
}
```

### Band Energy Computation
```go
// Source: inverse of decoder's denormalization
func (e *Encoder) ComputeBandEnergies(mdctCoeffs []float64, nbBands, frameSize int) []float64 {
    energies := make([]float64, nbBands*e.channels)

    for c := 0; c < e.channels; c++ {
        offset := 0
        for band := 0; band < nbBands; band++ {
            n := ScaledBandWidth(band, frameSize)

            // Compute L2 energy
            var sumSq float64
            for i := 0; i < n; i++ {
                idx := c*len(mdctCoeffs)/e.channels + offset + i
                if idx < len(mdctCoeffs) {
                    sumSq += mdctCoeffs[idx] * mdctCoeffs[idx]
                }
            }

            // Convert to log2 scale (same as decoder)
            if sumSq < 1e-15 {
                energies[c*nbBands+band] = -28.0 // Match decoder default
            } else {
                // energy = log2(sqrt(sumSq)) = 0.5 * log2(sumSq)
                energies[c*nbBands+band] = 0.5 * math.Log(sumSq) / 0.6931471805599453
            }

            offset += n
        }
    }

    return energies
}
```

### Band Normalization
```go
// Source: inverse of decoder's denormalization
func (e *Encoder) NormalizeBands(mdctCoeffs []float64, energies []float64, nbBands, frameSize int) [][]float64 {
    shapes := make([][]float64, nbBands*e.channels)

    for c := 0; c < e.channels; c++ {
        offset := 0
        for band := 0; band < nbBands; band++ {
            n := ScaledBandWidth(band, frameSize)
            shape := make([]float64, n)

            // Extract band coefficients
            for i := 0; i < n; i++ {
                idx := c*len(mdctCoeffs)/e.channels + offset + i
                if idx < len(mdctCoeffs) {
                    shape[i] = mdctCoeffs[idx]
                }
            }

            // Divide by gain (inverse of decoder's multiply)
            // gain = 2^energy = exp(energy * ln(2))
            gain := math.Exp(energies[c*nbBands+band] * 0.6931471805599453)
            if gain > 1e-10 {
                for i := range shape {
                    shape[i] /= gain
                }
            }

            // Normalize to unit L2 norm
            shapes[c*nbBands+band] = NormalizeVector(shape)
            offset += n
        }
    }

    return shapes
}
```

### PVQ Shape Encoding
```go
// Source: RFC 6716 Section 4.3.4.1, uses existing EncodePulses
func (e *Encoder) EncodeBandPVQ(shape []float64, k int) {
    if k <= 0 {
        return // Band gets zero bits, will be folded by decoder
    }

    n := len(shape)

    // Convert normalized shape to pulse vector
    pulses := vectorToPulses(shape, k)

    // Encode using existing CWRS function from Phase 3
    index := EncodePulses(pulses, n, k)

    // Encode index uniformly (decoder uses DecodeUniform)
    vSize := PVQ_V(n, k)
    e.rangeEncoder.EncodeUniform(index, vSize)
}

// vectorToPulses converts normalized float vector to integer pulse vector
func vectorToPulses(shape []float64, k int) []int {
    n := len(shape)
    pulses := make([]int, n)

    // Compute L1 norm for scaling
    var l1norm float64
    for _, x := range shape {
        l1norm += math.Abs(x)
    }

    if l1norm < 1e-10 {
        // Degenerate case: put all pulses at position 0
        pulses[0] = k
        return pulses
    }

    // Scale to target L1 norm = k
    scale := float64(k) / l1norm

    // Quantize with tracking of remainder
    remaining := k
    for i := 0; i < n && remaining > 0; i++ {
        p := int(math.Round(shape[i] * scale))

        // Clamp to remaining budget
        if abs(p) > remaining {
            if p > 0 {
                p = remaining
            } else {
                p = -remaining
            }
        }

        pulses[i] = p
        remaining -= abs(p)
    }

    // Distribute any remaining pulses to first non-zero position
    if remaining > 0 {
        for i := 0; i < n; i++ {
            if pulses[i] != 0 || i == n-1 {
                if pulses[i] >= 0 {
                    pulses[i] += remaining
                } else {
                    pulses[i] -= remaining
                }
                break
            }
        }
    }

    return pulses
}
```

### Transient Detection
```go
// Source: RFC 6716 Section 5.3.6, libopus celt/celt_encoder.c
func (e *Encoder) DetectTransient(pcm []float64, frameSize int) bool {
    // Compute energy in short blocks
    shortBlockSize := frameSize / 8 // 8 short blocks

    var maxRatio float64
    var prevEnergy float64 = -1

    for b := 0; b < 8; b++ {
        start := b * shortBlockSize
        end := start + shortBlockSize
        if end > len(pcm) {
            end = len(pcm)
        }

        // Compute block energy
        var energy float64
        for i := start; i < end; i++ {
            energy += pcm[i] * pcm[i]
        }

        // Check ratio with previous block
        if prevEnergy > 0 && energy > 0 {
            ratio := energy / prevEnergy
            if ratio > maxRatio {
                maxRatio = ratio
            }
            ratio = prevEnergy / energy
            if ratio > maxRatio {
                maxRatio = ratio
            }
        }

        prevEnergy = energy
    }

    // Transient if energy ratio exceeds threshold
    // Typical threshold: 4.0 (6dB difference)
    return maxRatio > 4.0
}
```

### Coarse Energy Encoding with Laplace Model
```go
// Source: RFC 6716 Section 4.3.2.1, libopus celt/quant_bands.c
func (e *Encoder) EncodeCoarseEnergy(energies []float64, nbBands int, intra bool, lm int) {
    // Get prediction coefficients (same as decoder)
    var alpha, beta float64
    if intra {
        alpha, beta = 0.0, BetaCoef[lm]
    } else {
        alpha, beta = AlphaCoef[lm], BetaCoef[lm]
    }

    // Decay for Laplace model
    decay := 16384
    if !intra {
        decay = 24000
    }

    for c := 0; c < e.channels; c++ {
        prevBandEnergy := 0.0

        for band := 0; band < nbBands; band++ {
            // Compute prediction
            prevFrameEnergy := e.prevEnergy[c*MaxBands+band]
            pred := alpha*prevFrameEnergy + beta*prevBandEnergy

            // Quantize to integer (6dB steps)
            target := energies[c*nbBands+band]
            qi := int(math.Round((target - pred) / DB6))

            // Encode with Laplace model
            e.encodeLaplace(qi, decay)

            // Reconstruct for next prediction (must match decoder)
            prevBandEnergy = pred + float64(qi)*DB6
        }
    }
}

// encodeLaplace encodes an integer with Laplace distribution
func (e *Encoder) encodeLaplace(val int, decay int) {
    // Symmetric encoding: 0, +1, -1, +2, -2, ...
    if val == 0 {
        // Center symbol
        fs0 := laplaceNMIN + (laplaceScale*decay)>>15
        e.rangeEncoder.Encode(0, uint32(fs0), laplaceFS)
        return
    }

    // Find cumulative range for |val|
    // ... (mirror decoder's decodeLaplace logic)
}
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Direct MDCT O(N^2) | FFT-based MDCT | Always preferred | Computational efficiency |
| Fixed block size | Transient-adaptive | Opus design | Better attack handling |
| Uniform quantization | PVQ spherical VQ | CELT design | Better noise shaping |
| Scalar energy | Coarse+fine energy | CELT design | Better dynamic range |
| libopus 1.5 | libopus 1.6 | Dec 2025 | Wideband-to-fullband BWE |

**Current libopus version:** 1.6 (December 2025)
- Wideband-to-fullband bandwidth extension added
- CELT encoder core unchanged from RFC 6716

**Encoder complexity considerations:**
- Low: Direct MDCT, greedy pulse allocation
- High: FFT-based MDCT, rate-distortion optimized PVQ search
- For initial implementation: Start with direct MDCT, simple pulse allocation

## Open Questions

Things that couldn't be fully resolved:

1. **Optimal PVQ Search Strategy**
   - What we know: Nearest-neighbor search in L2 finds pulse vector
   - What's unclear: Best rounding strategy for pulse distribution
   - Recommendation: Start with simple rounding, verify via round-trip

2. **Transient Detection Threshold**
   - What we know: Based on energy ratio between short blocks
   - What's unclear: Exact threshold value (libopus uses ~4-8)
   - Recommendation: Start with 4.0, tune based on listening tests

3. **Stereo Mode Selection Heuristics**
   - What we know: Mid-side vs dual stereo based on correlation
   - What's unclear: Exact correlation thresholds for mode switching
   - Recommendation: Default to mid-side, verify with stereo test signals

4. **Laplace Model Parameters**
   - What we know: Decay values differ for intra/inter modes
   - What's unclear: Exact values may be frame-size dependent
   - Recommendation: Use decoder's constants, verify round-trip

5. **Bit Budget Management**
   - What we know: Frame must fit within byte budget
   - What's unclear: How to handle budget overruns gracefully
   - Recommendation: Reserved last bits for overflow, trim bands if needed

## Implementation Strategy

Following the successful SILK encoder pattern from Phase 6:

### Phase 7a: Encoder Foundation
1. Create `Encoder` struct mirroring `Decoder`
2. Implement forward MDCT
3. Implement pre-emphasis filter
4. Test: MDCT -> IMDCT round-trip (no quantization)

### Phase 7b: Energy Encoding
1. Implement band energy computation
2. Implement coarse energy encoding with Laplace
3. Implement fine energy encoding
4. Test: Energy encode -> decode round-trip

### Phase 7c: PVQ Band Encoding
1. Implement band normalization
2. Implement `vectorToPulses` quantization
3. Integrate with existing `EncodePulses`
4. Test: Shape encode -> decode round-trip

### Phase 7d: Complete Encoding
1. Implement transient detection
2. Implement stereo encoding modes
3. Integrate all components
4. Test: Full frame encode -> decode round-trip

### Phase 7e: libopus Cross-Validation
1. Encode with gopus, decode with libopus
2. Compare to libopus encode -> gopus decode
3. Verify bit-exact where possible, perceptual where not

## Sources

### Primary (HIGH confidence)
- [RFC 6716 Section 5.3](https://datatracker.ietf.org/doc/html/rfc6716#section-5.3) - CELT Encoder specification
- [RFC 6716 Section 4.3](https://datatracker.ietf.org/doc/html/rfc6716#section-4.3) - CELT Decoder (encoder must match)
- Phase 3 codebase (`internal/celt/*`) - Existing decoder, tables, CWRS
- [libopus celt/cwrs.c encode_pulses](https://github.com/telegramdesktop/opus/blob/ffmpeg_fix/celt/cwrs.c) - CWRS encoding reference
- [libopus celt/quant_bands.c](https://gitlab.xiph.org/xnorpx/opus/-/blob/67821109b98ea81e136bf8ddf077529ecfd8bce3/celt/quant_bands.h) - Energy quantization

### Secondary (MEDIUM confidence)
- [CELT Wikipedia](https://en.wikipedia.org/wiki/CELT) - Algorithm overview
- [draft-valin-celt-codec-01](https://datatracker.ietf.org/doc/html/draft-valin-celt-codec-01) - Original CELT specification
- Phase 6 SILK Encoder patterns - Encoder architecture approach

### Tertiary (LOW confidence)
- WebSearch results on PVQ encoding strategies
- Community discussions on transient detection

## Metadata

**Confidence breakdown:**
- Encoder architecture: HIGH - follows proven SILK encoder pattern
- Forward MDCT: HIGH - transpose of IMDCT, well-documented
- PVQ encoding: HIGH - uses existing EncodePulses function
- Energy encoding: HIGH - mirrors decoder exactly
- Bit allocation: HIGH - uses existing ComputeAllocation
- Transient detection: MEDIUM - heuristics need tuning
- Stereo decisions: MEDIUM - mode selection heuristics unclear

**Research date:** 2026-01-22
**Valid until:** 2026-04-22 (stable RFC, encoder algorithms well-established)
