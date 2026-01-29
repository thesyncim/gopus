# Decoder Compliance Investigation Findings

## Status: 6/12 test vectors passing, need Q >= 0 (SNR >= 48 dB)

## PROVEN NOT THE ISSUE (Do NOT investigate again)

### 1. Float32 vs Float64 Drift
- **Test:** `TestFloat32VsFloat64Deemphasis` shows minimal drift after 60 frames
- **Evidence:** State error is 9.5e-6 before transient, jumps to 0.019 AT transient
- **Conclusion:** Precision difference is NOT the root cause

### 2. De-emphasis Filter Formula
- **libopus:** `tmp = x + VERY_SMALL + m; m = coef * tmp; output = tmp`
- **gopus:** `tmp = x + state; state = coef * tmp; output = tmp`
- **Conclusion:** Identical (VERY_SMALL = 1e-30 is negligible)

### 3. PreemphCoef Value
- **libopus:** 0.85000610f (from static_modes_float.h line 899)
- **gopus:** 0.85000610 (from tables.go line 26)
- **Conclusion:** Identical

### 4. IMDCT Algorithm
- **Test:** Individual IMDCT achieves ~140 dB SNR
- **Conclusion:** IMDCT math is correct

### 5. Window Coefficients
- **Formula:** `sin(0.5*pi*sin(0.5*pi*(i+0.5)/overlap)^2)`
- **Verified:** First value matches libopus (6.7286966e-05)
- **Conclusion:** Correct

### 6. Short Block Coefficient Extraction
- **libopus:** `&freq[b]` with stride B reads freq[b], freq[b+B], freq[b+2B]...
- **gopus:** `coeffs[b + i*shortBlocks]` extracts same pattern
- **Conclusion:** Mathematically equivalent

### 7. Postfilter/Comb Filter
- **Test:** `TestPostfilterStateTransitionVsLibopus` passes
- **Evidence:** SNR is excellent (136+ dB) through postfilter packets 59-60
- **Conclusion:** Postfilter is correct

### 8. SATURATE Macro
- **libopus float mode:** `#define SATURATE(x,a) (x)` - NO-OP
- **Conclusion:** No saturation/clamping in float mode

## PROVEN TO BE THE ISSUE

### Transient Frame Short Block Synthesis
- **Packet 60 (normal):** state_err=9.5e-6, SNR=136 dB
- **Packet 61 (transient):** state_err=0.019, SNR=80 dB
- **Error jump:** 2000x increase at transient frame
- **Pattern:** Error accumulates block-to-block (102 dB → 61 dB across 8 short blocks)

## SUSPECTED ROOT CAUSE

The `synthesizeChannelWithOverlap` function in synthesis.go (lines 144-194) handles
transient mode differently from libopus. The specific issue is likely in how the
overlap buffer is managed between the 8 short IMDCTs.

### Key Difference Identified
- **libopus:** Writes IMDCT output directly to `out_syn[c]+NB*b` (in-place)
- **gopus:** Creates a shared buffer, calls `imdctOverlapWithPrev`, copies back

The `imdctOverlapWithPrev` function:
1. Copies prevOverlap to output buffer
2. Does IMDCT
3. Does TDAC windowing
4. Returns the full buffer

When called in a loop for short blocks, there may be an issue with how the
overlap region from block N feeds into block N+1.

## NEXT STEPS

1. Compare EXACTLY what libopus writes to buffer for each short block
2. Trace gopus block-by-block output for transient frame
3. Find the specific divergence point
