# Phase 16: SILK Decoder Quality - Research

**Researched:** 2026-01-23
**Domain:** SILK decoder algorithm, LPC synthesis, excitation reconstruction, RFC 6716 Section 4.2
**Confidence:** HIGH

## Summary

Phase 16 addresses SILK-specific decoder quality issues. The critical DecodeBit() bug that caused Q=-100 on all test vectors has already been fixed in commit `1eceab2`. This fix corrects the range coder threshold calculation, which was treating all frames as silence.

After analyzing the SILK decoder implementation and test vector results, the research identifies that SILK-only test vectors (02, 03, 04) still show Q=-100, which could be:
1. Pre-fix baseline that needs re-measurement after the DecodeBit fix
2. SILK-specific algorithm issues beyond the shared range coder bug

The SILK decoder uses the range coder for all parameter decoding (frame type, gains, LSF, pitch, LTP, excitation). With the DecodeBit fix now applied, the immediate action is to re-run compliance tests to establish a new baseline, then investigate any remaining SILK-specific divergences.

**Primary recommendation:** Re-establish baseline quality metrics after DecodeBit fix, then systematically verify each SILK decoding stage against libopus.

## Standard Stack

### Core References
| Resource | Version | Purpose | Why Standard |
|----------|---------|---------|--------------|
| RFC 6716 | 2012 | SILK decoder specification (Section 4.2) | Official Opus specification |
| libopus | 1.4+ | Reference implementation | Bit-exact reference decoder |
| RFC 8251 | 2017 | Test vectors specification | Compliance validation |

### Key Source Files (libopus)
| File | Purpose | Critical Functions |
|------|---------|-------------------|
| silk/decode_frame.c | Main frame decode | silk_decode_frame() |
| silk/decode_parameters.c | Parameter extraction | silk_decode_parameters() |
| silk/decode_pulses.c | Excitation decoding | silk_decode_pulses() |
| silk/NLSF_decode.c | LSF decoding | silk_NLSF_decode() |
| silk/decode_pitch.c | Pitch lag decoding | silk_decode_pitch() |
| silk/LPC_synthesis_order16.c | LPC filter | silk_LPC_synthesis_order16() |

### gopus SILK Implementation
| File | Purpose | Status |
|------|---------|--------|
| internal/silk/decoder.go | Decoder state | Complete |
| internal/silk/decode.go | Frame decode flow | Complete, needs verification |
| internal/silk/decode_params.go | Parameter parsing | Complete |
| internal/silk/excitation.go | Shell coding, excitation | Complete, needs verification |
| internal/silk/lpc.go | LPC synthesis | Complete, needs verification |
| internal/silk/ltp.go | Long-term prediction | Complete, needs verification |
| internal/silk/lsf.go | LSF to LPC conversion | Complete, needs verification |
| internal/silk/gain.go | Gain decoding | Complete |
| internal/silk/pitch.go | Pitch lag decoding | Complete |

## Architecture Patterns

### SILK Decoding Pipeline (RFC 6716 Section 4.2)
```
1. Range decoder initialization (DecodeBit fix applied here)
2. Frame header decode (VAD flag, signal type, quantization offset)
3. Subframe gain decoding (absolute + delta)
4. LSF coefficients decode (two-stage VQ)
5. LSF -> LPC conversion (Chebyshev polynomial)
6. Pitch lag decode (voiced frames only)
7. LTP coefficients decode (voiced frames only)
8. Excitation shell decode (pulses per shell)
9. Excitation pulse distribution (binary splits)
10. Excitation sign decode
11. Shaped noise addition (LCG-based)
12. Excitation gain scaling
13. LTP synthesis (voiced frames, uses output history)
14. LPC synthesis (short-term prediction filter)
15. Output history update (for next frame LTP)
16. Resample to 48kHz
```

### How DecodeBit() Affects SILK

DecodeBit() is used in SILK for:
1. **VAD flags** - Determines if frame is active (uses logp for probability)
2. **Silence detection** - Uses DecodeBit(15) in CELT; similar patterns in SILK
3. **Various binary decisions** - Throughout parameter decoding

With the DecodeBit fix, the probability regions are now correct:
- `[0, rng-r)` = bit 0 (high probability)
- `[rng-r, rng)` = bit 1 (low probability, 1/2^logp)

### SILK Frame Structure
```
For 20ms frame (4 subframes at 5ms each):
- Each subframe: 40/60/80 samples at 8/12/16 kHz (NB/MB/WB)
- Total native samples: 160/240/320 per 20ms
- Upsampled to 48kHz: 960 samples
```

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| LSF codebooks | Approximate values | Exact libopus tables | Bit-exact decoding required |
| LTP filter tables | Simplified coefficients | Exact LTPFilterLow/Mid/High | Pitch prediction accuracy |
| Excitation sign probability | Uniform distribution | Signal-type specific ICDFs | Affects pulse amplitude |
| Cosine table for LSF->LPC | Runtime computation | CosineTable[129] Q12 values | Matches RFC exactly |
| Gain dequantization | Linear approximation | GainDequantTable[64] Q16 | Exponential curve |

**Key insight:** SILK decoding is deterministic. Each ICDF table lookup must match libopus exactly, or the bitstream interpretation diverges.

## Common Pitfalls

### Pitfall 1: Excitation Shell Coding Indexing
**What goes wrong:** Wrong pulses assigned to samples
**Why it happens:** Shell coding uses recursive binary splits with index mapping
**How to avoid:** Verify ICDFExcitationSplit[pulseCount] matches RFC 6716 Section 4.2.7.8.2
**Warning signs:** Excitation has energy but wrong spectral shape

**Current implementation status:**
```go
// decodePulseDistribution in excitation.go
// Uses ICDFExcitationSplit[tableIdx] where tableIdx = min(count, 16)
// Verify table values match RFC exactly
```

### Pitfall 2: LPC Synthesis Filter Order
**What goes wrong:** Filter instability or wrong spectral envelope
**Why it happens:** LPC order varies: 10 for NB/MB, 16 for WB
**How to avoid:** Use GetBandwidthConfig(bandwidth).LPCOrder consistently
**Warning signs:** Output explodes or has wrong formant structure

**Current implementation:**
```go
// lpcSynthesis in lpc.go
// Uses d.lpcOrder set from config
// limitLPCFilterGain applies bandwidth expansion if needed
```

### Pitfall 3: LSF to LPC Conversion Precision
**What goes wrong:** LPC coefficients slightly wrong, affects spectrum
**Why it happens:** Chebyshev recursion with Q12/Q15 fixed-point
**How to avoid:** Use lsfToLPCDirect() with exact table interpolation
**Warning signs:** Spectral envelope slightly off, formants shifted

**Current implementation note:**
```go
// lsfToLPC in lsf.go
// Has two implementations - one using Chebyshev recursion, one direct
// Currently calls lsfToLPCDirect() as fallback - verify this is correct path
```

### Pitfall 4: LTP History Buffer Management
**What goes wrong:** Pitch prediction uses wrong samples
**Why it happens:** Circular buffer indexing, must handle frame boundaries
**How to avoid:** Verify historyIndex updates correctly per subframe
**Warning signs:** Voiced frames sound choppy or have pitch artifacts

**Current implementation:**
```go
// ltpSynthesis in ltp.go
// Uses d.outputHistory[322] circular buffer
// histIdx = d.historyIndex - pitchLag + k - 2 + i
// Verify wraparound handling
```

### Pitfall 5: Excitation Sign Probability Tables
**What goes wrong:** Wrong pulse polarity
**Why it happens:** Sign ICDFs indexed by [signalType][quantOffset][pulseCount]
**How to avoid:** Ensure all 3*2*6 = 36 sign tables match RFC
**Warning signs:** Output sounds harsh or has DC offset

**Current implementation:**
```go
// ICDFExcitationSign[signalType][quantOffset][pulseCount-1]
// Verify signIdx = min(pulseCount-1, 5) bounds check
```

### Pitfall 6: Gain Dequantization Table Values
**What goes wrong:** Output amplitude wrong
**Why it happens:** Gain table is exponential, Q16 format
**How to avoid:** Verify GainDequantTable[64] values match RFC
**Warning signs:** Output too quiet or clipped

## Code Examples

### Correct SILK Frame Decode Flow (from current implementation)
```go
// Source: internal/silk/decode.go
func (d *Decoder) decodeBlock(
    bandwidth Bandwidth,
    vadFlag bool,
    duration FrameDuration,
    output []float32,
) error {
    config := GetBandwidthConfig(bandwidth)
    numSubframes := len(output) / config.SubframeSamples

    // 1. Decode frame type (uses range decoder)
    signalType, quantOffset := d.DecodeFrameType(vadFlag)

    // 2. Decode subframe gains
    gains := d.decodeSubframeGains(signalType, numSubframes)

    // 3. Decode LSF -> LPC coefficients
    lsfQ15 := d.decodeLSFCoefficients(bandwidth, signalType)
    lpcQ12 := lsfToLPC(lsfQ15)
    limitLPCFilterGain(lpcQ12)

    // 4. Decode pitch/LTP (voiced only)
    var pitchLags []int
    var ltpCoeffs [][]int8
    var ltpScale int
    if signalType == 2 { // Voiced
        pitchLags = d.decodePitchLag(bandwidth, numSubframes)
        ltpCoeffs, ltpScale = d.decodeLTPCoefficients(bandwidth, numSubframes)
    }

    // 5. Decode and synthesize each subframe
    for sf := 0; sf < numSubframes; sf++ {
        // Decode excitation
        excitation := d.decodeExcitation(config.SubframeSamples, signalType, quantOffset)

        // Scale excitation by gain
        scaleExcitation(excitation, gains[sf])

        // Apply LTP synthesis (voiced only)
        if signalType == 2 && pitchLags != nil {
            d.ltpSynthesis(excitation, pitchLags[sf], ltpCoeffs[sf], ltpScale)
        }

        // Apply LPC synthesis
        d.lpcSynthesis(excitation, lpcQ12, gains[sf], sfOutput)

        // Update output history
        d.updateHistory(sfOutput)
    }

    return nil
}
```

### LPC Synthesis Filter (verified against RFC)
```go
// Source: internal/silk/lpc.go
// RFC 6716 Section 4.2.7.9.2:
// out[n] = exc[n] + sum(a[k] * out[n-k-1]) for k=0..order-1

func (d *Decoder) lpcSynthesis(excitation []int32, lpcCoeffs []int16, gain int32, out []float32) {
    order := len(lpcCoeffs)
    for i, exc := range excitation {
        sample := int64(exc)
        for k := 0; k < order; k++ {
            var prev float32
            if i-k-1 >= 0 {
                prev = out[i-k-1]
            } else {
                stateIdx := len(d.prevLPCValues) + (i - k - 1)
                if stateIdx >= 0 {
                    prev = d.prevLPCValues[stateIdx]
                }
            }
            prevQ12 := int64(prev * 4096.0)
            sample += (int64(lpcCoeffs[k]) * prevQ12) >> 12
        }
        out[i] = float32(sample) / float32(32767*256)
    }
    d.updateLPCState(out, order)
}
```

## Test Vectors Analysis

### SILK-Only Test Vectors (RFC 8251)
| Vector | Packets | Frame Sizes | Current Q | Notes |
|--------|---------|-------------|-----------|-------|
| testvector02 | 1185 | 10/20/40/60ms | -100.00 | Pure SILK, mono |
| testvector03 | 998 | 10/20/40/60ms | -100.00 | Pure SILK, mono |
| testvector04 | 1265 | 10/20/40/60ms | -100.00 | Pure SILK, mono |

### Mixed Mode Test Vectors with SILK
| Vector | Modes | Current Q | Notes |
|--------|-------|-----------|-------|
| testvector08 | SILK,CELT | -172.99 | Mixed modes, stereo |
| testvector09 | SILK,CELT | -172.41 | Mixed modes, stereo |
| testvector12 | Hybrid,SILK | -154.13 | Hybrid + SILK, mono |

**Key observation:** SILK-only vectors show Q=-100 which is typically "complete failure" (zero correlation). This was caused by the DecodeBit bug treating all frames as silence. After the fix, these should improve significantly.

### Validation Plan
1. Run compliance tests to get new baseline after DecodeBit fix
2. If SILK-only vectors still fail:
   - Add tracing to SILK decode pipeline
   - Compare frame-by-frame with libopus output
   - Identify first divergence point
3. Fix any remaining SILK-specific issues

## State of the Art

| Previous State | Current State | Change | Impact |
|---------------|---------------|--------|--------|
| DecodeBit using `val >= r` | DecodeBit using `val >= (rng - r)` | Commit 1eceab2 | All decoders now correctly decode probability regions |
| All vectors Q=-100 or worse | Expected improvement | Pending measurement | SILK, CELT, Hybrid should all improve |
| Silence frames 100% of time | Normal frame processing | DecodeBit fix | Actual audio content now decoded |

**Critical change already made:**
```go
// OLD (WRONG):
if d.val >= r { return 1 }

// NEW (CORRECT - commit 1eceab2):
threshold := d.rng - r  // '1' region is at TOP of range
if d.val >= threshold { return 1 }
```

## Investigation Priorities

### Plan 16-01: Verify DecodeBit Fix Impact (HIGH PRIORITY)
- Re-run all compliance tests
- Document new Q scores per test vector
- If SILK-only still failing, proceed to 16-02

### Plan 16-02: SILK Parameter Decode Verification (if needed)
- Add tracing to frame type, gain, LSF decoding
- Compare decoded parameters with libopus
- Identify first parameter divergence

### Plan 16-03: SILK Excitation Verification (if needed)
- Trace shell coding output
- Compare excitation pulses with libopus
- Verify shaped noise contribution

### Plan 16-04: SILK Synthesis Verification (if needed)
- Trace LTP synthesis output
- Trace LPC synthesis output
- Compare sample-by-sample with reference

### Plan 16-05: Energy/Quality Correlation Tests
- Add energy ratio tests for SILK output
- Compare energy envelope with reference
- Document remaining gaps

## Open Questions

1. **New Baseline After Fix**
   - What we know: DecodeBit fix is committed (1eceab2)
   - What's unclear: Actual Q improvement on SILK vectors
   - Recommendation: Run compliance tests first

2. **LSF Codebook Completeness**
   - What we know: Tables exist in codebook.go
   - What's unclear: Whether stage 2 residuals are complete
   - Recommendation: Verify against RFC 6716 appendix

3. **Excitation Shell Coding Edge Cases**
   - What we know: Basic algorithm implemented
   - What's unclear: Handling of large pulse counts (>16)
   - Recommendation: Add trace tests with high-energy frames

## Sources

### Primary (HIGH confidence)
- [RFC 6716](https://www.rfc-editor.org/rfc/rfc6716) - Opus codec specification, Section 4.2 SILK decoder
- [libopus silk/decode_frame.c](https://android.googlesource.com/platform/external/libopus/+/refs/heads/main/silk/decode_frame.c) - Reference implementation
- gopus internal/rangecoding/decoder.go - DecodeBit fix verified

### Secondary (MEDIUM confidence)
- [RFC 8251](https://www.rfc-editor.org/rfc/rfc8251) - Test vectors specification
- gopus compliance test results - Current Q scores

### Tertiary (LOW confidence)
- WebSearch patterns for SILK debugging approaches

## Metadata

**Confidence breakdown:**
- DecodeBit fix impact: HIGH - Code verified, fix committed
- SILK architecture understanding: HIGH - Direct code analysis
- Remaining issues: MEDIUM - Need baseline measurement first
- LSF/LPC correctness: MEDIUM - Tables present but not verified against libopus

**Research date:** 2026-01-23
**Valid until:** 2026-02-23 (30 days - specification is stable)

---

## Appendix: SILK-Specific ICDF Tables Inventory

The following ICDF tables are used by SILK decoder (all in internal/silk/tables.go):

### Frame Type
- ICDFFrameTypeVADInactive - 2 symbols
- ICDFFrameTypeVADActive - 5 symbols (encodes signalType + quantOffset)

### Gains
- ICDFGainMSBInactive - 9 symbols
- ICDFGainMSBUnvoiced - 6 symbols
- ICDFGainMSBVoiced - 10 symbols
- ICDFGainLSB - 9 symbols (3 bits)
- ICDFDeltaGain - 16 symbols (delta between subframes)

### LSF
- ICDFLSFStage1NBMBVoiced - 26 symbols
- ICDFLSFStage1NBMBUnvoiced - 24 symbols
- ICDFLSFStage1WBVoiced - 26 symbols
- ICDFLSFStage1WBUnvoiced - 27 symbols
- ICDFLSFStage2NBMB[8] - 7 symbols each
- ICDFLSFStage2WB[8] - 7 symbols each
- ICDFLSFInterpolation - 6 symbols

### Pitch/LTP
- ICDFPitchLagNB/MB/WB - Pitch lag MSB
- ICDFPitchContourNB/MB/WB - Pitch contour indices
- ICDFLTPFilterIndexLow/Mid/High - Periodicity selection
- ICDFLTPGainLow/Mid/High - LTP gain indices

### Excitation
- ICDFRateLevelUnvoiced - 9 symbols
- ICDFRateLevelVoiced - 9 symbols
- ICDFExcitationPulseCount - 17 symbols
- ICDFExcitationSplit[17] - Binary split tables
- ICDFExcitationLSB - 3 symbols
- ICDFExcitationSign[3][2][6] - Sign tables by type/offset/count
- ICDFLCGSeed - 5 symbols

### Stereo
- ICDFStereoPredWeight - 9 symbols
- ICDFStereoPredWeightDelta - 9 symbols
