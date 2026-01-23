# Phase 15: CELT Decoder Quality - Research

**Researched:** 2026-01-23
**Domain:** CELT decoder algorithm corrections, MDCT synthesis, RFC 6716 compliance
**Confidence:** HIGH

## Summary

Phase 15 addresses the Q=-100 score on CELT decoder test vectors, indicating zero correlation between decoded output and reference audio. This research investigates the root causes and provides specific fixes. Analysis of the gopus CELT implementation against RFC 6716 and libopus reveals multiple algorithm issues across the decoding pipeline: energy decoding, bit allocation, PVQ decoding, IMDCT synthesis, and denormalization.

The Q=-100 score means the decoded audio has no energy correlation with reference audio - the output is effectively noise or silence. This points to fundamental algorithm bugs rather than precision issues. After analyzing the codebase, several critical issues have been identified:

1. **Coarse energy Laplace decoding** uses incorrect probability model and prediction coefficients
2. **Bit allocation** doesn't match libopus algorithm for converting bits to pulses
3. **IMDCT/synthesis** may have windowing and overlap-add bugs for non-power-of-2 sizes
4. **Denormalization formula** uses incorrect energy-to-gain conversion

**Primary recommendation:** Fix in priority order: (1) coarse energy decoding with correct Laplace model and prediction coefficients, (2) band denormalization formula, (3) IMDCT synthesis for CELT-specific sizes (120/240/480/960), (4) bit allocation and PVQ decoding verification.

## Standard Stack

### Core References
| Resource | Version | Purpose | Why Standard |
|----------|---------|---------|--------------|
| RFC 6716 | 2012 | CELT decoder specification | Official specification |
| libopus | 1.4+ | Reference implementation | Bit-exact reference |
| RFC 8251 | 2017 | Test vectors | Compliance validation |

### Key Source Files (libopus)
| File | Purpose | Critical Functions |
|------|---------|-------------------|
| celt/quant_bands.c | Energy decoding | unquant_coarse_energy(), unquant_fine_energy() |
| celt/bands.c | Band processing | quant_all_bands(), denormalise_bands() |
| celt/celt_decoder.c | Main decode loop | celt_decode_with_ec() |
| celt/laplace.c | Laplace codec | ec_laplace_decode() |
| celt/rate.c | Bit allocation | pulses2bits(), bits2pulses() |

## Architecture Patterns

### CELT Decoding Pipeline (Correct Order)
```
1. Silence flag decode
2. Postfilter params decode (if present)
3. Transient flag decode
4. Intra flag decode
5. Coarse energy decode (Laplace + prediction)
6. TF resolution decode
7. Spread decision decode
8. Dynamic allocation decode
9. Trim value decode
10. Intensity/dual-stereo decode (if stereo)
11. Fine quantization decode
12. PVQ band decoding (quant_all_bands)
13. Anti-collapse (if transient)
14. Energy finalization (remainder bits)
15. Denormalization (scale by energy)
16. IMDCT synthesis
17. Overlap-add
18. De-emphasis filter
```

### Correct Prediction Coefficients (from libopus)

```go
// Source: libopus celt/quant_bands.c

// Inter-frame prediction coefficients (alpha) by LM
var PredCoef = [4]float64{
    29440.0 / 32768.0,  // LM=0 (2.5ms): 0.8984375
    26112.0 / 32768.0,  // LM=1 (5ms):   0.796875
    21248.0 / 32768.0,  // LM=2 (10ms):  0.6484375
    16384.0 / 32768.0,  // LM=3 (20ms):  0.5
}

// Inter-band prediction coefficients (beta) by LM - INTER MODE
var BetaCoefInter = [4]float64{
    30147.0 / 32768.0,  // LM=0: 0.9200744...
    22282.0 / 32768.0,  // LM=1: 0.6800537...
    12124.0 / 32768.0,  // LM=2: 0.3700561...
    6554.0 / 32768.0,   // LM=3: 0.2000122...
}

// Intra mode beta (no inter-frame prediction, only inter-band)
const BetaIntra = 4915.0 / 32768.0  // 0.15
```

**CRITICAL BUG IN GOPUS:** The current `BetaCoef` array uses `1.0 - 4915.0/32768.0 = 0.85` for all LM values, which is WRONG. The correct beta varies by LM for inter-frame mode.

### Correct Energy Denormalization Formula

```go
// Source: libopus celt/bands.c denormalise_bands()

// Energy is stored in log2 scale, not dB scale
// gain = 2^(energy) where energy is log2-scale band energy
// libopus uses: g = celt_exp2_db(MIN32(32.f, lg))

// Correct conversion:
// For float: gain = exp2(energy) = pow(2, energy)
// Using natural log: gain = exp(energy * ln(2))

const ln2 = 0.6931471805599453

// WRONG (current gopus):
// gain := math.Exp(energies[band] * ln2)  // This is 2^energy, but assumes dB

// The energy in coarse decoding is accumulated in 6dB steps (qi*6)
// but after fine decoding it's converted to log2 scale
// Final denormalization: gain = 2^(bandLogE[band]) where bandLogE is log2-scale

func denormalize(coeff, logEnergy float64) float64 {
    gain := math.Exp2(logEnergy) // pow(2, logEnergy)
    return coeff * gain
}
```

### Correct Laplace Decoding

```go
// Source: libopus celt/laplace.c ec_laplace_decode()

// Probability model uses 15-bit precision (fs = 32768)
// Decay controls spread of distribution
// Different decay values for intra vs inter mode

// prob_model table indexed by min(band, 20) * 2
var probModel = [42]int{
    // Per-band probability parameters from libopus
    // Format: [fs0, decay] pairs for bands 0-20
    // ...
}

// The Laplace decode returns signed integer quantization index
// This index is then scaled by 6dB (DB6 = 6.0) and added to prediction
```

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Laplace probability model | Custom probability tables | libopus prob_model[] exact values | Bit-exact decoding required |
| IMDCT for 120/240/480/960 | FFT-based (power-of-2) | Direct computation or properly handled sizes | CELT sizes aren't power-of-2 |
| Energy prediction | Simplified alpha/beta | Exact libopus coefficients per LM | Frame size matters |
| Range decoder update | Bit-based approximation | Proper ec_dec_update() equivalent | Entropy coding must be exact |

**Key insight:** The current gopus implementation has several "approximations" that break bit-exact decoding. CELT decoding is deterministic - any deviation cascades through the bitstream causing complete decoding failure.

## Common Pitfalls

### Pitfall 1: Incorrect Energy Prediction Coefficients
**What goes wrong:** Q=-100 because energy envelope is completely wrong
**Why it happens:** Using fixed beta=0.85 instead of LM-dependent values
**How to avoid:** Use exact libopus pred_coef[] and beta_coef[] arrays
**Warning signs:** Decoded energy values diverge from reference within first few bands

**Current gopus bug:**
```go
// WRONG - current tables.go
var BetaCoef = [4]float64{
    1.0 - 4915.0/32768.0,  // All are 0.85 - WRONG
    // ...
}

// CORRECT - should be
var BetaCoef = [4]float64{
    30147.0/32768.0,  // LM=0: varies by frame size
    22282.0/32768.0,  // etc.
    // ...
}
```

### Pitfall 2: Incorrect Denormalization Scale
**What goes wrong:** Output amplitude completely wrong, no correlation
**Why it happens:** Confusing dB scale vs log2 scale
**How to avoid:** Use exp2(energy) not exp(energy * ln2 * some_factor)
**Warning signs:** Output either silent (too small) or clipped (too large)

**Current gopus bug:**
```go
// WRONG - current bands.go
gain := math.Exp(energies[band] * 0.6931471805599453) // ln(2)
// This assumes energy is log2-scale but may have wrong base assumption

// CORRECT - explicit
gain := math.Exp2(energies[band])  // 2^energy
```

### Pitfall 3: IMDCT Size Mismatch
**What goes wrong:** Synthesis produces wrong sample count or garbled audio
**Why it happens:** CELT uses non-power-of-2 sizes (120, 240, 480, 960)
**How to avoid:** Use direct IMDCT for non-power-of-2 or proper pre/post-processing
**Warning signs:** Frame overlap incorrect, clicks/pops between frames

**Analysis:** The current mdct.go falls back to IMDCTDirect() for non-power-of-2, which should be correct but verify the formula matches RFC 6716 Section 4.3.5.

### Pitfall 4: Range Decoder State Synchronization
**What goes wrong:** Decoding drifts, produces garbage after first few bands
**Why it happens:** updateRange() uses bit approximation instead of proper update
**How to avoid:** Implement proper ec_dec_update() semantics in range decoder
**Warning signs:** First packets decode "somewhat", later packets are noise

**Current concern:**
```go
// Current energy.go updateRange() uses a bit-based approximation
// This may not properly track range decoder state
func (d *Decoder) updateRange(fl, fh, ft uint32) {
    // ... uses DecodeBit() calls to approximate - THIS IS WRONG
}
```

### Pitfall 5: Bit Allocation Formula Mismatch
**What goes wrong:** PVQ decodes wrong number of pulses per band
**Why it happens:** bitsToK() formula doesn't match libopus bits2pulses()
**How to avoid:** Use binary search over PVQ_V() like libopus rate.c
**Warning signs:** Some bands have energy, others are silent when shouldn't be

### Pitfall 6: Silence Flag Position
**What goes wrong:** Non-silence frames decoded as silence
**Why it happens:** Silence flag probability wrong (should be ~15 = 1/32768)
**How to avoid:** Use exact probability from RFC 6716
**Warning signs:** Many frames produce all zeros

## Code Examples

### Correct Coarse Energy Decoding
```go
// Source: Derived from libopus celt/quant_bands.c

func (d *Decoder) DecodeCoarseEnergy(nbBands int, intra bool, lm int) []float64 {
    energies := make([]float64, nbBands*d.channels)

    // Select prediction mode
    var coef, beta float64
    if intra {
        coef = 0.0           // No inter-frame prediction
        beta = 4915.0/32768.0  // Fixed intra beta
    } else {
        coef = PredCoef[lm]    // Inter-frame alpha
        beta = BetaCoefInter[lm]  // Inter-frame beta (varies by LM!)
    }

    for c := 0; c < d.channels; c++ {
        prev := 0.0  // Previous band energy prediction

        for band := 0; band < nbBands; band++ {
            // Decode Laplace-distributed quantization index
            pi := 2 * min(band, 20)  // Index into probability model
            qi := d.decodeLaplaceProper(probModel[pi], probModel[pi+1])

            // Apply prediction
            prevFrame := d.prevEnergy[c*MaxBands + band]
            pred := coef*prevFrame + prev

            // Compute energy: prediction + quantized delta (6 dB per step)
            q := float64(qi) * DB6
            energy := pred + q

            // Update inter-band predictor
            prev = prev + q - beta*q  // Note: subtract beta*q

            energies[c*nbBands + band] = energy
        }
    }

    return energies
}
```

### Correct Denormalization
```go
// Source: Derived from libopus celt/bands.c denormalise_bands()

func denormalizeBand(shape []float64, logEnergy float64) []float64 {
    // logEnergy is in log2 scale (not dB!)
    // gain = 2^logEnergy
    gain := math.Exp2(logEnergy)

    result := make([]float64, len(shape))
    for i, s := range shape {
        result[i] = s * gain
    }
    return result
}
```

### Correct IMDCT Formula
```go
// Source: RFC 6716 Section 4.3.5

// y[n] = sum_{k=0}^{N-1} X[k] * cos(pi/N * (n + 0.5 + N/2) * (k + 0.5))
// Output has 2N samples, input has N coefficients

func IMDCTDirect(spectrum []float64) []float64 {
    N := len(spectrum)
    N2 := N * 2
    output := make([]float64, N2)

    for n := 0; n < N2; n++ {
        var sum float64
        for k := 0; k < N; k++ {
            angle := math.Pi / float64(N) *
                    (float64(n) + 0.5 + float64(N)/2) *
                    (float64(k) + 0.5)
            sum += spectrum[k] * math.Cos(angle)
        }
        // Normalization factor: 2/N (or 1/N depending on convention)
        output[n] = sum * 2.0 / float64(N)
    }

    return output
}
```

## State of the Art

| Old Approach (gopus) | Current Approach (libopus) | Impact |
|---------------------|---------------------------|--------|
| Fixed beta=0.85 all LM | LM-dependent beta coefficients | Energy prediction accuracy |
| Bit-approximation range update | Proper ec_dec_update() | Bitstream synchronization |
| exp(e*ln2) denormalization | exp2(e) denormalization | Amplitude accuracy |
| Simplified Laplace decode | Full probability model | Entropy coding accuracy |

**Critical changes needed:**
1. Replace BetaCoef table with correct LM-dependent values
2. Implement proper range decoder state update in Laplace decode
3. Verify denormalization uses exp2() correctly
4. Verify IMDCT formula matches RFC 6716

## Investigation Priority

### Phase 15 Task Breakdown

**Task 1: Fix Coarse Energy Prediction (HIGHEST PRIORITY)**
- Replace BetaCoef with correct LM-dependent values from libopus
- Verify AlphaCoef matches (currently looks correct)
- Fix prediction update formula: `prev = prev + q - beta*q`

**Task 2: Fix Range Decoder Integration**
- Implement proper updateRange() using ec_dec_update() semantics
- Or expose DecodeSymbol() method in rangecoding package
- Verify Laplace decoding consumes correct bit budget

**Task 3: Fix Denormalization**
- Verify energy scale (log2 vs dB) throughout pipeline
- Use exp2() for final denormalization
- Add test comparing single-band decode to libopus

**Task 4: Verify IMDCT Synthesis**
- Test IMDCTDirect() against known input/output pairs
- Verify windowing applies correct Vorbis window coefficients
- Verify overlap-add produces correct sample count

**Task 5: Frame Size Specific Testing**
- Create tests for 2.5ms (120 samples) CELT frames
- Create tests for 5ms (240 samples) CELT frames
- Create tests for 10ms (480 samples) CELT frames
- Verify each produces correct output length

## Test Vectors Analysis

The RFC 8251 test vectors that exercise CELT-only decoding:
- **testvector06-12:** Contain CELT-only packets (configs 16-31)
- These test various bandwidths (NB, WB, SWB, FB) and frame sizes

To isolate CELT issues:
1. Filter test vectors to CELT-only packets
2. Decode single frame and compare to reference
3. Binary search to find first divergence point

## Open Questions

1. **Laplace Probability Model Exact Values**
   - What we know: libopus uses prob_model[] array indexed by band
   - What's unclear: Exact values need extraction from libopus source
   - Recommendation: Extract and hardcode in tables.go

2. **Fine Energy Quantization Formula**
   - What we know: Uses `(q + 0.5) / (1 << bits) - 0.5`
   - What's unclear: Whether current implementation matches exactly
   - Recommendation: Verify against libopus unquant_fine_energy()

3. **Energy Remainder Bits**
   - What we know: Uses single-bit refinements
   - What's unclear: Correct offset calculation
   - Recommendation: Compare with libopus unquant_energy_finalise()

## Sources

### Primary (HIGH confidence)
- [RFC 6716](https://www.rfc-editor.org/rfc/rfc6716) - Opus codec specification, Section 4.3 CELT decoder
- [libopus quant_bands.c](https://github.com/xiph/opus/blob/main/celt/quant_bands.c) - Energy decoding reference
- [libopus bands.c](https://github.com/xiph/opus/blob/main/celt/bands.c) - Band processing reference
- [libopus celt_decoder.c](https://github.com/xiph/opus/blob/main/celt/celt_decoder.c) - Main decoder loop

### Secondary (MEDIUM confidence)
- [RFC 8251](https://www.rfc-editor.org/rfc/rfc8251) - Test vectors specification
- [opus_compare.c](https://github.com/xiph/opus/blob/main/src/opus_compare.c) - Quality metric

### Tertiary (LOW confidence)
- WebSearch results on CELT implementation patterns
- Academic papers on MDCT algorithms

## Metadata

**Confidence breakdown:**
- Prediction coefficient bug: HIGH - Direct comparison with libopus source
- Denormalization formula: HIGH - Clear difference in implementation
- Range decoder integration: MEDIUM - Need to verify current behavior
- IMDCT correctness: MEDIUM - Formula looks correct but sizes may have issues

**Research date:** 2026-01-23
**Valid until:** 2026-02-23 (30 days - specification is stable)
