# CELT Pre-emphasis Investigation

## Status: Pre-emphasis is CORRECT - Issue is Elsewhere

Started: 2026-01-30

## Summary

Investigation of the CELT encoder's signal inversion (correlation = -0.54) found that:

1. **Pre-emphasis is working correctly** - gopus matches libopus with correlation 1.000000
2. **MDCT forward transform is correct** - SNR > 200 dB in roundtrip tests
3. **expRotation is correct** - roundtrip SNR > 599 dB
4. **PVQ search matches libopus** - 440/440 exact matches

The signal inversion issue must be elsewhere in the encoding pipeline.

## Pre-emphasis Verification

### Test Results

```
TestPreemphasisComparison:
  Max difference: 0.000370
  Avg difference: 0.000002
  Correlation: 1.000000  (PERFECT)

TestPreemphasisSignInversion:
  libopus: 15 positive, 5 negative
  gopus:   15 positive, 5 negative  (MATCH)
```

### Formula Comparison

**libopus (celt_encoder.c lines 571-578):**
```c
for (i=0;i<N;i++) {
    x = RES2SIG(pcm[i]);
    inp[i] = x - m;              // output = input - mem
    m = MULT16_32_Q15(coef0, x); // mem = coef * input
}
```

**gopus (preemph.go ApplyPreemphasisWithScaling):**
```go
for i := range pcm {
    scaled := pcm[i] * CELTSigScale
    output[i] = scaled - PreemphCoef*state  // output = scaled - coef*state
    state = scaled                           // state = scaled
}
```

Both produce: `y[n] = x[n] - coef * x[n-1]`

The formulas are **algebraically equivalent** - just different state representation:
- libopus stores: `mem = coef * x[n]`
- gopus stores: `state = x[n]`

## Current Investigation Status

### What's Working (Verified)
1. Pre-emphasis filter formula and signs
2. Signal scaling (x32768)
3. MDCT forward transform
4. expRotation for PVQ
5. PVQ search algorithm
6. CWRS encoding/decoding
7. First 7 bytes of encoded packet match libopus

### What Diverges
- Byte 8 onwards in the encoded packet
- Final decoded output has negative correlation (-0.54)

### Possible Remaining Causes

1. **TF (Time-Frequency) Resolution Encoding**
   - TF decisions affect how bands are organized for PVQ
   - Different TF may cause completely different band quantization

2. **Spread Decision**
   - Controls expRotation parameters
   - If spread differs, rotation parameters differ

3. **Fine Energy Encoding**
   - Fine energy bits encoded after coarse energy
   - May affect how decoder interprets energies

4. **Band Quantization Order**
   - Order in which bands are encoded/decoded
   - Stereo parameters may affect mono encoding

5. **Anti-collapse Processing**
   - May flip signs under certain conditions

## Files Examined

- `/Users/thesyncim/GolandProjects/gopus/internal/celt/preemph.go` - Pre-emphasis implementation
- `/Users/thesyncim/GolandProjects/gopus/internal/celt/mdct_encode.go` - Forward MDCT
- `/Users/thesyncim/GolandProjects/gopus/internal/celt/bands_quant.go` - expRotation, PVQ quantization
- `/Users/thesyncim/GolandProjects/gopus/internal/celt/encode_frame.go` - Main encoding pipeline
- `/Users/thesyncim/GolandProjects/gopus/tmp_check/opus-1.6.1/celt/celt_encoder.c` - libopus reference
- `/Users/thesyncim/GolandProjects/gopus/tmp_check/opus-1.6.1/celt/vq.c` - libopus exp_rotation, alg_quant

## Test Files Created

- `/Users/thesyncim/GolandProjects/gopus/internal/celt/cgo_test/preemphasis_compare_test.go`

## Next Steps

1. **Compare TF decisions** between gopus and libopus
2. **Compare spread decision** between gopus and libopus
3. **Trace full algQuant** to find where quantized values first diverge
4. **Compare fine energy encoding**
5. **Compare intensity/dual stereo parameters** (for mono, should be disabled)

## Additional Finding: Transient Detection Issue

### Problem
For a first-frame 440 Hz sine wave:
- gopus: `Transient: true, tfEstimate: 0.9928, shortBlocks: 8`
- Expected for steady-state sine: `Transient: false, shortBlocks: 1`

### Root Cause
1. **First frame has no history** - The preemphBuffer is all zeros for the first frame
2. **Artificial edge at frame start** - Signal jumps from 0 to full amplitude
3. **Transient detector correctly identifies this** as a transient

### Missing libopus Feature
libopus has a "toneishness" check that suppresses transient detection for pure tones:
```c
/* Prevent the transient detector from confusing the partial cycle of a
   very low frequency tone with a transient. */
if (toneishness > QCONST32(.98f, 29) && tone_freq < QCONST16(0.026f, 13))
{
   is_transient = 0;
   mask_metric = 0;
}
```

gopus does NOT implement this toneishness check.

### Impact
- Different TF resolution decisions between gopus and libopus
- Different short block mode (8 vs 1)
- Different MDCT coefficient layout
- All downstream encoding differs

## Conclusion

Pre-emphasis is NOT the cause of signal inversion. The issues are:

1. **Transient detection** - gopus incorrectly triggers transient mode for first frame
2. **Missing toneishness check** - libopus suppresses transients for pure tones
3. **TF resolution encoding** - Differs due to transient detection difference
4. **Short block mode** - Uses 8 blocks instead of 1 for steady-state signals

The fact that the first 7-8 bytes of the packet match suggests coarse energy encoding is correct, and the divergence starts at TF/spread/fine energy encoding.

### Recommended Fixes (Priority Order)
1. **Implement toneishness check** in TransientAnalysis
2. **Verify short block decision** matches libopus
3. **Test with second+ frames** (where preemphBuffer has history)
4. **Compare TF decisions** byte-by-byte with libopus
