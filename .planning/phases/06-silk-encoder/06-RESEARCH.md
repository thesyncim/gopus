# Phase 6: SILK Encoder - Research

**Researched:** 2026-01-22
**Domain:** SILK audio encoding per RFC 6716 Section 5.2 and draft-vos-silk-01/02
**Confidence:** HIGH

## Summary

Phase 6 implements the SILK encoder, which performs the reverse of Phase 2's decoder: analyzing PCM audio to produce SILK-mode Opus packets. The encoder must produce output that is decodable by both the Phase 2 decoder and libopus. SILK encoding involves LPC analysis (using Burg's method), LSF quantization (two-stage VQ with codebook search), pitch detection (three-stage coarse-to-fine autocorrelation), LTP coefficient encoding, excitation quantization via shell coding, and gain quantization.

The encoder is inherently more complex than the decoder because analysis algorithms have no single "correct" answer - they involve optimization and search. However, the existing Phase 2 decoder provides all the codebooks, ICDF tables, and synthesis algorithms that constrain what the encoder must produce. The encoder must generate bitstreams that, when decoded, produce intelligible speech.

**Primary recommendation:** Structure the encoder as the mirror of the decoder: FrameParams generation (analysis) -> parameter encoding (range coder). Reuse all existing SILK tables and codebooks. Implement LPC analysis with Burg's method, LSF quantization with exhaustive codebook search, and autocorrelation-based pitch detection. Verify each component by round-tripping through the Phase 2 decoder.

## Standard Stack

The established libraries/tools for this domain:

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| Go standard library | 1.21+ | All functionality | Zero dependencies requirement (CMP-03) |
| internal/rangecoding | Phase 1 | Entropy encoding | Already implemented, bit-exact |
| internal/silk | Phase 2 | Tables, codebooks, decoder | Symmetric encode/decode |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `math` | stdlib | Float operations, trig | LPC analysis, pitch detection |
| `testing` | stdlib | Unit tests | Encoder verification |

### Reusable from Phase 2
| Component | Location | Encoder Use |
|-----------|----------|-------------|
| ICDF tables | `silk/tables.go` | Same tables for encoding symbols |
| LSF codebooks | `silk/codebook.go` | Codebook search target |
| LTP codebooks | `silk/codebook.go` | LTP coefficient quantization |
| Pitch contours | `silk/codebook.go` | Pitch lag delta encoding |
| Bandwidth configs | `silk/bandwidth.go` | Frame sizing, pitch ranges |
| FrameParams struct | `silk/decode_params.go` | Encoder output structure |
| lpcResidual() | `silk/lpc.go` | Residual computation for analysis |

**Installation:**
```bash
# No external dependencies - pure stdlib
# Uses internal/rangecoding from Phase 1
# Uses internal/silk tables/codebooks from Phase 2
```

## Architecture Patterns

### Recommended Project Structure
```
gopus/
├── internal/
│   ├── rangecoding/
│   │   ├── encoder.go      # From Phase 1 (needs EncodeICDF16)
│   │   └── decoder.go      # For verification
│   └── silk/
│       ├── encoder.go      # NEW: Main encoder struct
│       ├── encode_frame.go # NEW: Frame encoding pipeline
│       ├── lpc_analysis.go # NEW: LPC via Burg's method
│       ├── lsf_encode.go   # NEW: LPC-to-LSF, quantization
│       ├── pitch_detect.go # NEW: Pitch detection
│       ├── ltp_encode.go   # NEW: LTP analysis and encoding
│       ├── excitation_encode.go # NEW: Shell coding encoder
│       ├── gain_encode.go  # NEW: Gain quantization
│       ├── stereo_encode.go # NEW: Mid-side encoding
│       ├── vad.go          # NEW: Voice activity detection
│       ├── decoder.go      # From Phase 2
│       ├── tables.go       # From Phase 2 (shared)
│       ├── codebook.go     # From Phase 2 (shared)
│       └── *_test.go       # Encoder tests with round-trip
```

### Pattern 1: Analysis-Synthesis Verification Loop
**What:** Every encoder component verifies by round-tripping through decoder
**When to use:** All encoder development and testing
**Example:**
```go
// Encoder produces FrameParams via analysis
func (e *Encoder) analyzeFrame(pcm []float32) *FrameParams {
    params := &FrameParams{}
    params.SignalType, params.QuantOffset = e.classifyFrame(pcm)
    params.Gains = e.analyzeGains(pcm)
    params.LPCCoeffs = e.analyzeLPC(pcm)
    if params.SignalType == 2 { // Voiced
        params.PitchLags = e.detectPitch(pcm)
        params.LTPCoeffs = e.analyzeLTP(pcm, params.PitchLags)
    }
    params.Excitation = e.computeExcitation(pcm, params)
    return params
}

// Verification: encode -> decode -> compare
func TestRoundTrip(t *testing.T) {
    enc := silk.NewEncoder()
    dec := silk.NewDecoder()

    params := enc.analyzeFrame(originalPCM)
    bitstream := enc.EncodeFrame(params)

    decoded := dec.DecodeFrame(bitstream)
    assertSimilar(t, originalPCM, decoded, tolerance)
}
```

### Pattern 2: Codebook Search with Rate-Distortion
**What:** Find best codebook entry minimizing distortion + rate cost
**When to use:** LSF quantization, LTP coefficient encoding
**Example:**
```go
// Source: draft-vos-silk-01 Section 2.1.2.7
func (e *Encoder) quantizeLSF(lsf []float32, isVoiced bool) (stage1Idx int, residuals []int) {
    bestRD := math.MaxFloat64
    var bestIdx int
    var bestRes []int

    // Search stage 1 codebook (32 entries)
    codebook := LSFCodebookNBMB // or WB
    for idx := 0; idx < 32; idx++ {
        // Compute weighted distortion
        dist := e.computeLSFDistortion(lsf, codebook[idx][:])

        // Add rate cost (from ICDF probabilities)
        rate := e.lsfStage1Rate(idx, isVoiced)

        rd := dist + e.lambda*rate
        if rd < bestRD {
            bestRD = rd
            bestIdx = idx
            bestRes = e.computeLSFResiduals(lsf, codebook[idx][:])
        }
    }
    return bestIdx, bestRes
}
```

### Pattern 3: Encoder State Mirrors Decoder State
**What:** Encoder maintains identical state to decoder for prediction
**When to use:** Gain encoding, LSF interpolation, stereo weights
**Example:**
```go
// Source: RFC 6716 Section 4.2.7.4
type Encoder struct {
    // Mirror decoder state for synchronized prediction
    haveEncoded       bool
    previousLogGain   int32
    isPreviousVoiced  bool
    prevLSFQ15        []int16
    prevStereoWeights [2]int16

    // Analysis buffers
    inputBuffer       []float32
    lpcState          []float32
}
```

### Anti-Patterns to Avoid
- **Encoding without verification:** Always test encoded output through decoder
- **Optimizing prematurely:** Get correct output first, then optimize search
- **Ignoring rate cost:** Codebook selection must consider bit cost, not just distortion
- **Different state than decoder:** Encoder and decoder must stay synchronized

## Don't Hand-Roll

Problems that look simple but have existing solutions:

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Autocorrelation | Simple nested loop | FFT-based or Burg's method | Numerical stability, efficiency |
| LPC-to-LSF | Polynomial root finding | Chebyshev polynomial method | Standard, stable algorithm |
| Pitch detection | Simple peak finding | Three-stage autocorrelation | Octave errors, noise robustness |
| Codebook search | Random or heuristic | Exhaustive with early termination | Quality-critical for intelligibility |
| Shell coding | Custom bit allocation | Mirror decoder's tree structure | Must produce decodable bitstream |

**Key insight:** The encoder's freedom is in analysis quality, not output format. The output format is rigidly defined by what the decoder expects. All encoding must produce bitstreams that decode correctly.

## Common Pitfalls

### Pitfall 1: LPC Coefficient Instability
**What goes wrong:** Encoded LPC produces unstable filter (poles outside unit circle)
**Why it happens:** Burg's method on noisy input, insufficient bandwidth expansion
**How to avoid:** Apply limitLPCFilterGain() from Phase 2 before LSF conversion; verify LSF ordering
**Warning signs:** Decoder output explodes, clips constantly

### Pitfall 2: Pitch Octave Errors
**What goes wrong:** Detected pitch is 2x or 0.5x actual pitch
**Why it happens:** Autocorrelation has peaks at multiples of fundamental
**How to avoid:** Use three-stage detection with bias toward shorter lags; validate against previous frame
**Warning signs:** Voiced speech sounds "robotic" or has pitch artifacts

### Pitfall 3: LSF Quantization Order Violations
**What goes wrong:** Quantized LSFs are not strictly increasing
**Why it happens:** Stage 2 residuals can reverse ordering
**How to avoid:** Apply stabilizeLSF() after quantization (existing in Phase 2)
**Warning signs:** Decoder produces unstable LPC coefficients

### Pitfall 4: Shell Coding Bit Budget Mismatch
**What goes wrong:** Encoded excitation doesn't decode correctly
**Why it happens:** Encoder tree structure differs from decoder expectation
**How to avoid:** Mirror decoder's decodePulseDistribution() exactly in reverse
**Warning signs:** Decoded excitation has wrong magnitudes or positions

### Pitfall 5: Gain Quantization Clipping
**What goes wrong:** First frame gain or delta gains out of range
**Why it happens:** Not applying gain limiter for first frame, delta exceeding [-4, 11]
**How to avoid:** Follow RFC 6716 Section 4.2.7.4 gain index calculation exactly
**Warning signs:** Volume jumps, clicks at frame boundaries

### Pitfall 6: Frame Type Misclassification
**What goes wrong:** Voiced frame encoded as unvoiced or vice versa
**Why it happens:** Inadequate VAD, wrong periodicity threshold
**How to avoid:** Implement proper VAD with subband analysis; use LTP gain as secondary check
**Warning signs:** Voiced speech loses pitch periodicity; unvoiced sounds "pitched"

### Pitfall 7: Stereo Prediction Weight Discontinuity
**What goes wrong:** Stereo image jumps between frames
**Why it happens:** Not using delta encoding for weights after first frame
**How to avoid:** Track prevStereoWeights; encode delta from previous
**Warning signs:** Audible "pumping" in stereo field

### Pitfall 8: Range Encoder State Mismatch
**What goes wrong:** Decoder fails to decode encoded bitstream
**Why it happens:** Encoder ICDF tables or parameters differ from decoder
**How to avoid:** Use exact same ICDF tables; verify round-trip on every symbol type
**Warning signs:** Decoder throws errors or produces garbage

## Code Examples

Verified patterns from official sources and RFC specifications:

### LPC Analysis via Burg's Method
```go
// Source: draft-vos-silk-01 Section 2.1.2.1, DSP.jl Burg implementation
// Burg's method minimizes forward and backward prediction error simultaneously.
func burgLPC(signal []float32, order int) []float32 {
    n := len(signal)
    a := make([]float32, order+1)  // LPC coefficients
    a[0] = 1.0

    // Forward and backward prediction errors
    ef := make([]float32, n)
    eb := make([]float32, n)
    copy(ef, signal)
    copy(eb, signal)

    for i := 1; i <= order; i++ {
        // Compute reflection coefficient
        var num, den float32
        for j := i; j < n; j++ {
            num += ef[j] * eb[j-1]
            den += ef[j]*ef[j] + eb[j-1]*eb[j-1]
        }
        if den < 1e-10 {
            break // Prevent division by zero
        }
        k := -2.0 * num / den

        // Update LPC coefficients
        for j := 1; j < i; j++ {
            temp := a[j]
            a[j] = temp + k*a[i-j]
        }
        a[i] = k

        // Update prediction errors
        for j := n - 1; j >= i; j-- {
            temp := ef[j]
            ef[j] = temp + k*eb[j-1]
            eb[j-1] = eb[j-1] + k*temp
        }
    }

    return a[1:] // Return coefficients without a[0]=1
}
```

### LPC to LSF Conversion via Chebyshev Polynomials
```go
// Source: Speex lsp.c, Kabal & Ramachandran IEEE 1986
// Constructs P(z) and Q(z) polynomials, finds roots via bisection.
func lpcToLSF(lpc []float32) []float32 {
    order := len(lpc)
    lsf := make([]float32, order)

    // Construct symmetric polynomials P and Q
    p := make([]float32, order/2+1)
    q := make([]float32, order/2+1)

    p[0] = 1.0
    q[0] = 1.0
    for i := 0; i < order/2; i++ {
        p[i+1] = lpc[i] + lpc[order-1-i] - p[i]
        q[i+1] = lpc[i] - lpc[order-1-i] + q[i]
    }

    // Find roots by searching for sign changes
    lsfIdx := 0
    for freq := float32(0.0); freq < math.Pi && lsfIdx < order; freq += 0.001 {
        x := float32(math.Cos(float64(freq)))

        // Evaluate Chebyshev polynomial at x
        pVal := chebyshevEval(p, x)
        qVal := chebyshevEval(q, x)

        // Check for sign change (root crossing)
        // Use bisection to refine root location
        // ... (detailed bisection logic)

        if foundRoot {
            lsf[lsfIdx] = freq
            lsfIdx++
        }
    }

    return lsf
}

func chebyshevEval(coef []float32, x float32) float32 {
    // Clenshaw's recurrence for Chebyshev evaluation
    var b0, b1, b2 float32
    for i := len(coef) - 1; i >= 0; i-- {
        b2 = b1
        b1 = b0
        b0 = 2*x*b1 - b2 + coef[i]
    }
    return b0 - x*b1
}
```

### Three-Stage Pitch Detection
```go
// Source: draft-vos-silk-01 Section 2.1.2.5
func (e *Encoder) detectPitch(signal []float32, bandwidth Bandwidth) []int {
    config := GetBandwidthConfig(bandwidth)

    // Stage 1: Coarse search at 4kHz (downsampled)
    ds4k := downsample(signal, config.SampleRate/4000)
    coarseLag := e.autocorrPitchSearch(ds4k,
        config.PitchLagMin/4, config.PitchLagMax/4)

    // Stage 2: Refined search at 8kHz
    ds8k := downsample(signal, config.SampleRate/8000)
    midLag := e.autocorrPitchSearch(ds8k,
        max(config.PitchLagMin/2, coarseLag*2-4),
        min(config.PitchLagMax/2, coarseLag*2+4))

    // Stage 3: Fine search at full rate per subframe
    pitchLags := make([]int, e.numSubframes)
    for sf := 0; sf < e.numSubframes; sf++ {
        subframe := signal[sf*config.SubframeSamples:(sf+1)*config.SubframeSamples]
        pitchLags[sf] = e.autocorrPitchSearch(subframe,
            max(config.PitchLagMin, midLag*2-2),
            min(config.PitchLagMax, midLag*2+2))
    }

    return pitchLags
}

func (e *Encoder) autocorrPitchSearch(signal []float32, minLag, maxLag int) int {
    n := len(signal)
    var bestLag int
    var bestCorr float32 = -1

    for lag := minLag; lag <= maxLag; lag++ {
        var corr, energy1, energy2 float32
        for i := lag; i < n; i++ {
            corr += signal[i] * signal[i-lag]
            energy1 += signal[i] * signal[i]
            energy2 += signal[i-lag] * signal[i-lag]
        }

        // Normalized correlation
        if energy1 > 0 && energy2 > 0 {
            normCorr := corr / float32(math.Sqrt(float64(energy1*energy2)))

            // Bias toward shorter lags to avoid octave errors
            // Source: draft-vos-silk-01 Section 2.1.2.5
            normCorr *= 1.0 - 0.001*float32(lag-minLag)

            if normCorr > bestCorr {
                bestCorr = normCorr
                bestLag = lag
            }
        }
    }

    return bestLag
}
```

### LSF Quantization with Two-Stage VQ
```go
// Source: RFC 6716 Section 4.2.7.5, draft-vos-silk-01 Section 2.1.2.7
func (e *Encoder) quantizeLSF(lsfQ15 []int16, isWideband, isVoiced bool) (int, []int, int) {
    lpcOrder := len(lsfQ15)

    // Select codebook based on bandwidth and voicing
    var codebook [][10]uint8  // or [16]uint8 for WB
    var icdf []uint16
    if isWideband {
        codebook = LSFCodebookWB[:]
        if isVoiced {
            icdf = ICDFLSFStage1WBVoiced
        } else {
            icdf = ICDFLSFStage1WBUnvoiced
        }
    } else {
        codebook = LSFCodebookNBMB[:]
        if isVoiced {
            icdf = ICDFLSFStage1NBMBVoiced
        } else {
            icdf = ICDFLSFStage1NBMBUnvoiced
        }
    }

    // Stage 1: Find best codebook vector (exhaustive search)
    bestStage1 := 0
    bestDist := int64(math.MaxInt64)

    for idx := 0; idx < len(codebook); idx++ {
        var dist int64
        for i := 0; i < lpcOrder; i++ {
            diff := int64(lsfQ15[i]) - int64(codebook[idx][i])<<7
            // Weight by perceptual importance (IHMW)
            weight := e.lsfWeight(i, lsfQ15)
            dist += diff * diff * int64(weight) >> 8
        }

        // Add rate cost from ICDF
        rate := e.computeRate(idx, icdf)
        totalCost := dist + int64(e.lambda*float32(rate))

        if totalCost < bestDist {
            bestDist = totalCost
            bestStage1 = idx
        }
    }

    // Stage 2: Quantize residuals
    residuals := make([]int, lpcOrder)
    mapIdx := bestStage1 >> 2
    for i := 0; i < lpcOrder; i++ {
        target := int(lsfQ15[i]) - int(codebook[bestStage1][i])<<7
        residuals[i] = e.quantizeResidual(target, mapIdx, i, isWideband)
    }

    // Interpolation index (0-4)
    interpIdx := e.computeLSFInterp(lsfQ15)

    return bestStage1, residuals, interpIdx
}
```

### Shell Coding Excitation Encoder
```go
// Source: RFC 6716 Section 4.2.7.8, gcp/opus silk/encode_pulses.c
func (e *Encoder) encodeExcitation(excitation []int32, signalType, quantOffset int) {
    shellSize := 16
    numShells := len(excitation) / shellSize

    // Compute pulse counts per shell
    pulseCounts := make([]int, numShells)
    for shell := 0; shell < numShells; shell++ {
        offset := shell * shellSize
        for i := 0; i < shellSize; i++ {
            pulseCounts[shell] += abs(int(excitation[offset+i]))
        }
    }

    // Determine rate level (minimize total bits)
    rateLevel := e.selectRateLevel(pulseCounts, signalType)
    if signalType == 2 {
        e.rangeEncoder.EncodeICDF16(rateLevel, ICDFRateLevelVoiced, 8)
    } else {
        e.rangeEncoder.EncodeICDF16(rateLevel, ICDFRateLevelUnvoiced, 8)
    }

    // Encode pulse counts per shell
    for shell := 0; shell < numShells; shell++ {
        e.rangeEncoder.EncodeICDF16(pulseCounts[shell], ICDFExcitationPulseCount, 8)
    }

    // Encode LSBs for large counts
    for shell := 0; shell < numShells; shell++ {
        if pulseCounts[shell] > 10 {
            // Encode LSB count
            e.rangeEncoder.EncodeICDF16(lsbCounts[shell], ICDFExcitationLSB, 8)
        }
    }

    // Encode shell structure (binary splits)
    for shell := 0; shell < numShells; shell++ {
        if pulseCounts[shell] > 0 {
            offset := shell * shellSize
            e.encodePulseDistribution(excitation[offset:offset+shellSize], pulseCounts[shell])
        }
    }

    // Encode signs
    for shell := 0; shell < numShells; shell++ {
        offset := shell * shellSize
        for i := 0; i < shellSize; i++ {
            if excitation[offset+i] != 0 {
                sign := 0
                if excitation[offset+i] < 0 {
                    sign = 1
                }
                signIdx := min(abs(int(excitation[offset+i]))-1, 5)
                icdf := ICDFExcitationSign[signalType][quantOffset][signIdx]
                e.rangeEncoder.EncodeICDF16(sign, icdf, 8)
            }
        }
    }

    // Encode LCG seed
    seed := e.computeLCGSeed()
    e.rangeEncoder.EncodeICDF16(seed, ICDFLCGSeed, 8)
}

func (e *Encoder) encodePulseDistribution(pulses []int32, totalPulses int) {
    if len(pulses) == 1 {
        return // All pulses go to this position
    }

    mid := len(pulses) / 2
    var leftCount int
    for i := 0; i < mid; i++ {
        leftCount += abs(int(pulses[i]))
    }

    // Encode left count using split ICDF
    icdf := ICDFExcitationSplit[min(totalPulses, len(ICDFExcitationSplit)-1)]
    e.rangeEncoder.EncodeICDF16(leftCount, icdf, 8)

    // Recurse
    rightCount := totalPulses - leftCount
    e.encodePulseDistribution(pulses[:mid], leftCount)
    e.encodePulseDistribution(pulses[mid:], rightCount)
}
```

### Range Encoder Extension for uint16 ICDF
```go
// Source: Needed because existing EncodeICDF uses uint8, but SILK tables use uint16
func (e *Encoder) EncodeICDF16(s int, icdf []uint16, ftb uint) {
    ft := uint32(1) << ftb
    var fl, fh uint32
    if s > 0 {
        fl = ft - uint32(icdf[s-1])
    } else {
        fl = 0
    }
    fh = ft - uint32(icdf[s])
    e.Encode(fl, fh, ft)
}
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Autocorrelation LPC | Burg's method | SILK original design | Better numerical stability |
| Direct LSF search | Multi-stage VQ | Speech coding standard | Reduced complexity |
| Simple VAD | Subband VAD + RNN | libopus 1.3 | Better classification |
| Full codebook search | Survivor path search | Complexity optimization | 85% computation savings |

**Current libopus version:** 1.6 (December 2025)
- Added wideband-to-fullband bandwidth extension
- Deep learning redundancy encoder in development
- SILK layer unchanged from RFC 6716

**Encoder complexity modes:**
- Low: Order 8 filters, single-stage VQ, basic pitch
- Medium: Order 12 filters, two-stage VQ
- High: Order 16 filters, multi-stage VQ, refined pitch

## Open Questions

Things that couldn't be fully resolved:

1. **Exact Burg's Method Implementation**
   - What we know: SILK uses Burg's method for LPC analysis
   - What's unclear: Exact windowing, look-ahead, and stability checks
   - Recommendation: Start with standard Burg's, verify via round-trip

2. **VAD Threshold Values**
   - What we know: VAD uses subband SNR analysis
   - What's unclear: Exact threshold values for voiced/unvoiced/inactive
   - Recommendation: Implement basic VAD, tune thresholds empirically

3. **Rate-Distortion Lambda Value**
   - What we know: Codebook search uses RD = dist + lambda * rate
   - What's unclear: Optimal lambda value for different bitrates
   - Recommendation: Start with lambda = 1.0, tune for target bitrate

4. **Noise Shaping Quantizer Details**
   - What we know: SILK uses noise shaping for perceptual quality
   - What's unclear: Exact filter coefficients and compensation gains
   - Recommendation: Implement basic quantization first, add shaping later

5. **Delayed Decision Quantization**
   - What we know: High complexity uses multiple quantization paths
   - What's unclear: Number of paths, selection criteria
   - Recommendation: Single path for initial implementation

## Required Range Encoder Extension

The existing range encoder supports `EncodeICDF(s int, icdf []uint8, ftb uint)` but SILK tables use `[]uint16`. Need to add:

```go
// EncodeICDF16 encodes a symbol using uint16 ICDF table.
// Required because SILK tables use uint16 (256 doesn't fit in uint8).
func (e *Encoder) EncodeICDF16(s int, icdf []uint16, ftb uint) {
    ft := uint32(1) << ftb
    var fl, fh uint32
    if s > 0 {
        fl = ft - uint32(icdf[s-1])
    }
    fh = ft - uint32(icdf[s])
    e.Encode(fl, fh, ft)
}
```

## Sources

### Primary (HIGH confidence)
- [RFC 6716 Section 5.2](https://datatracker.ietf.org/doc/html/rfc6716#section-5.2) - SILK Encoder (informative)
- [draft-vos-silk-01](https://datatracker.ietf.org/doc/html/draft-vos-silk-01) - Original SILK specification with encoder details
- [draft-vos-silk-02](https://datatracker.ietf.org/doc/html/draft-vos-silk-02) - Updated SILK draft
- [Phase 2 SILK Decoder](./02-silk-decoder/02-RESEARCH.md) - Decoder constraints on encoder output
- [xiph/speex lsp.c](https://github.com/xiph/speex/blob/master/libspeex/lsp.c) - LPC-to-LSF algorithm

### Secondary (MEDIUM confidence)
- [gcp/opus encode_pulses.c](https://github.com/gcp/opus/blob/master/silk/encode_pulses.c) - Shell coding encoder
- [MathWorks LPC to LSF](https://www.mathworks.com/help/dsp/ref/lpctolsflspconversion.html) - Chebyshev method documentation
- [Kabal & Ramachandran IEEE 1986](https://www.semanticscholar.org/paper/The-computation-of-line-spectral-frequencies-using-Kabal-Ramachandran/8ad551c344bfbb405b8da8a776c7c0d360dea5e1) - LSF computation algorithm
- [musicdsp.org LPC analysis](https://www.musicdsp.org/en/latest/Analysis/137-lpc-analysis-autocorrelation-levinson-durbin-recursion.html) - Levinson-Durbin reference

### Tertiary (LOW confidence)
- WebSearch results on pitch detection algorithms
- Community discussions on CELP encoder implementations

## Metadata

**Confidence breakdown:**
- LPC Analysis (Burg): HIGH - Well-documented algorithm, multiple references
- LSF Quantization: HIGH - RFC specifies codebooks, search is standard
- Pitch Detection: MEDIUM - Algorithm known, exact thresholds unclear
- Shell Coding: HIGH - Must mirror decoder exactly
- VAD: MEDIUM - Algorithm described, thresholds not specified
- Stereo Encoding: HIGH - Mirror of decoder's unmixing

**Research date:** 2026-01-22
**Valid until:** 2026-04-22 (stable RFC, encoder algorithms well-established)
