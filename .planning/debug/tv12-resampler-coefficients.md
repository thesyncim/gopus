# TV12 Resampler Coefficient Analysis

## Summary

**COEFFICIENTS ARE IDENTICAL** - No sign errors in the filter coefficients themselves.

## FIR Interpolation Coefficients (12-phase, 4 taps each)

### libopus (silk/resampler_rom.c:83-96)
```c
silk_DWORD_ALIGN const opus_int16 silk_resampler_frac_FIR_12[ 12 ][ RESAMPLER_ORDER_FIR_12 / 2 ] = {
    {  189,  -600,   617, 30567 },
    {  117,  -159, -1070, 29704 },
    {   52,   221, -2392, 28276 },
    {   -4,   529, -3350, 26341 },
    {  -48,   758, -3956, 23973 },
    {  -80,   905, -4235, 21254 },
    {  -99,   972, -4222, 18278 },
    { -107,   967, -3957, 15143 },
    { -103,   896, -3487, 11950 },
    {  -91,   773, -2865,  8798 },
    {  -71,   611, -2143,  5784 },
    {  -46,   425, -1375,  2996 },
};
```

### gopus (internal/silk/resample_libopus.go:74-87)
```go
var silkResamplerFracFIR12 = [12][4]int16{
    {189, -600, 617, 30567},
    {117, -159, -1070, 29704},
    {52, 221, -2392, 28276},
    {-4, 529, -3350, 26341},
    {-48, 758, -3956, 23973},
    {-80, 905, -4235, 21254},
    {-99, 972, -4222, 18278},
    {-107, 967, -3957, 15143},
    {-103, 896, -3487, 11950},
    {-91, 773, -2865, 8798},
    {-71, 611, -2143, 5784},
    {-46, 425, -1375, 2996},
}
```

**VERDICT: IDENTICAL**

---

## 2x Upsampler Allpass Coefficients

### libopus (silk/resampler_rom.h:45-46)
```c
static const opus_int16 silk_resampler_up2_hq_0[ 3 ] = { 1746, 14986, 39083 - 65536 };
static const opus_int16 silk_resampler_up2_hq_1[ 3 ] = { 6854, 25769, 55542 - 65536 };
```

Which evaluates to:
- `silk_resampler_up2_hq_0 = { 1746, 14986, -26453 }`
- `silk_resampler_up2_hq_1 = { 6854, 25769, -9994 }`

### gopus (internal/silk/resample_libopus.go:64-69)
```go
var (
    silkResamplerUp2HQ0 = [3]int16{1746, 14986, 39083 - 65536}
    silkResamplerUp2HQ1 = [3]int16{6854, 25769, 55542 - 65536}
)
```

Which evaluates to:
- `silkResamplerUp2HQ0 = { 1746, 14986, -26453 }`
- `silkResamplerUp2HQ1 = { 6854, 25769, -9994 }`

**VERDICT: IDENTICAL**

---

## Fixed-Point Arithmetic Implementation Comparison

### silk_SMULWB (libopus macros.h:43)
```c
#define silk_SMULWB(a32, b32) ((opus_int32)(((a32) * (opus_int64)((opus_int16)(b32))) >> 16))
```

### gopus (libopus_fixed.go:107-109)
```go
func silkSMULWB(a, b int32) int32 {
    return int32((int64(a) * int64(int16(b))) >> 16)
}
```

**VERDICT: IDENTICAL**

---

### silk_SMLAWB (libopus macros.h:50)
```c
#define silk_SMLAWB(a32, b32, c32) ((opus_int32)((a32) + (((b32) * (opus_int64)((opus_int16)(c32))) >> 16)))
```

### gopus (libopus_fixed.go:111-113)
```go
func silkSMLAWB(a, b, c int32) int32 {
    return a + int32((int64(b)*int64(int16(c)))>>16)
}
```

**VERDICT: IDENTICAL**

---

### silk_SMULBB (libopus macros.h:70)
```c
#define silk_SMULBB(a32, b32) ((opus_int32)((opus_int16)(a32)) * (opus_int32)((opus_int16)(b32)))
```

### gopus (libopus_fixed.go:115-117)
```go
func silkSMULBB(a, b int32) int32 {
    return int32(int16(a)) * int32(int16(b))
}
```

**VERDICT: IDENTICAL**

---

### silk_SMLABB (libopus macros.h:73)
```c
#define silk_SMLABB(a32, b32, c32) ((a32) + ((opus_int32)((opus_int16)(b32))) * (opus_int32)((opus_int16)(c32)))
```

### gopus (libopus_fixed.go:119-121)
```go
func silkSMLABB(a, b, c int32) int32 {
    return a + int32(int16(b))*int32(int16(c))
}
```

**VERDICT: IDENTICAL**

---

## up2HQ Algorithm Comparison

### libopus (resampler_private_up2_HQ.c:56-101)
```c
for( k = 0; k < len; k++ ) {
    in32 = silk_LSHIFT( (opus_int32)in[ k ], 10 );

    // First all-pass section for even output sample
    Y       = silk_SUB32( in32, S[ 0 ] );
    X       = silk_SMULWB( Y, silk_resampler_up2_hq_0[ 0 ] );
    out32_1 = silk_ADD32( S[ 0 ], X );
    S[ 0 ]  = silk_ADD32( in32, X );

    // Second all-pass section for even output sample
    Y       = silk_SUB32( out32_1, S[ 1 ] );
    X       = silk_SMULWB( Y, silk_resampler_up2_hq_0[ 1 ] );
    out32_2 = silk_ADD32( S[ 1 ], X );
    S[ 1 ]  = silk_ADD32( out32_1, X );

    // Third all-pass section for even output sample
    Y       = silk_SUB32( out32_2, S[ 2 ] );
    X       = silk_SMLAWB( Y, Y, silk_resampler_up2_hq_0[ 2 ] );  // <-- SMLAWB
    out32_1 = silk_ADD32( S[ 2 ], X );
    S[ 2 ]  = silk_ADD32( out32_2, X );

    out[ 2 * k ] = (opus_int16)silk_SAT16( silk_RSHIFT_ROUND( out32_1, 10 ) );

    // ... similar for odd samples with silk_resampler_up2_hq_1
}
```

### gopus (resample_libopus.go:268-315)
```go
for k := 0; k < len(in); k++ {
    in32 := int32(in[k]) << 10

    // First all-pass section for even output sample
    Y := in32 - r.sIIR[0]
    X := smulwb(Y, int32(silkResamplerUp2HQ0[0]))
    out32_1 := r.sIIR[0] + X
    r.sIIR[0] = in32 + X

    // Second all-pass section for even output sample
    Y = out32_1 - r.sIIR[1]
    X = smulwb(Y, int32(silkResamplerUp2HQ0[1]))
    out32_2 := r.sIIR[1] + X
    r.sIIR[1] = out32_1 + X

    // Third all-pass section for even output sample
    Y = out32_2 - r.sIIR[2]
    X = smlawb(Y, Y, int32(silkResamplerUp2HQ0[2]))  // <-- smlawb
    out32_1 = r.sIIR[2] + X
    r.sIIR[2] = out32_2 + X

    out[2*k] = sat16(rshiftRound(out32_1, 10))

    // ... similar for odd samples with silkResamplerUp2HQ1
}
```

**VERDICT: ALGORITHM APPEARS IDENTICAL**

---

## FIR Interpolation Algorithm Comparison

### libopus (resampler_private_IIR_FIR.c:48-61)
```c
for( index_Q16 = 0; index_Q16 < max_index_Q16; index_Q16 += index_increment_Q16 ) {
    table_index = silk_SMULWB( index_Q16 & 0xFFFF, 12 );
    buf_ptr = &buf[ index_Q16 >> 16 ];

    res_Q15 = silk_SMULBB(          buf_ptr[ 0 ], silk_resampler_frac_FIR_12[      table_index ][ 0 ] );
    res_Q15 = silk_SMLABB( res_Q15, buf_ptr[ 1 ], silk_resampler_frac_FIR_12[      table_index ][ 1 ] );
    res_Q15 = silk_SMLABB( res_Q15, buf_ptr[ 2 ], silk_resampler_frac_FIR_12[      table_index ][ 2 ] );
    res_Q15 = silk_SMLABB( res_Q15, buf_ptr[ 3 ], silk_resampler_frac_FIR_12[      table_index ][ 3 ] );
    res_Q15 = silk_SMLABB( res_Q15, buf_ptr[ 4 ], silk_resampler_frac_FIR_12[ 11 - table_index ][ 3 ] );
    res_Q15 = silk_SMLABB( res_Q15, buf_ptr[ 5 ], silk_resampler_frac_FIR_12[ 11 - table_index ][ 2 ] );
    res_Q15 = silk_SMLABB( res_Q15, buf_ptr[ 6 ], silk_resampler_frac_FIR_12[ 11 - table_index ][ 1 ] );
    res_Q15 = silk_SMLABB( res_Q15, buf_ptr[ 7 ], silk_resampler_frac_FIR_12[ 11 - table_index ][ 0 ] );
    *out++ = (opus_int16)silk_SAT16( silk_RSHIFT_ROUND( res_Q15, 15 ) );
}
```

### gopus (resample_libopus.go:322-344)
```go
for indexQ16 := int32(0); indexQ16 < maxIndexQ16; indexQ16 += indexIncrQ16 {
    tableIndex := smulwb(indexQ16&0xFFFF, 12)
    bufIdx := int(indexQ16 >> 16)

    resQ15 := smulbb(int32(buf[bufIdx+0]), int32(silkResamplerFracFIR12[tableIndex][0]))
    resQ15 = smlabb(resQ15, int32(buf[bufIdx+1]), int32(silkResamplerFracFIR12[tableIndex][1]))
    resQ15 = smlabb(resQ15, int32(buf[bufIdx+2]), int32(silkResamplerFracFIR12[tableIndex][2]))
    resQ15 = smlabb(resQ15, int32(buf[bufIdx+3]), int32(silkResamplerFracFIR12[tableIndex][3]))
    resQ15 = smlabb(resQ15, int32(buf[bufIdx+4]), int32(silkResamplerFracFIR12[11-tableIndex][3]))
    resQ15 = smlabb(resQ15, int32(buf[bufIdx+5]), int32(silkResamplerFracFIR12[11-tableIndex][2]))
    resQ15 = smlabb(resQ15, int32(buf[bufIdx+6]), int32(silkResamplerFracFIR12[11-tableIndex][1]))
    resQ15 = smlabb(resQ15, int32(buf[bufIdx+7]), int32(silkResamplerFracFIR12[11-tableIndex][0]))

    if outIdx < len(out) {
        out[outIdx] = sat16(rshiftRound(resQ15, 15))
        outIdx++
    }
}
```

**VERDICT: ALGORITHM APPEARS IDENTICAL**

---

## Key Differences Found: NONE IN COEFFICIENTS OR BASIC ALGORITHM

---

## INVESTIGATION NEEDED: Process() Wrapper Logic

The resampler coefficients and fixed-point math are identical. The sign inversion must come from:

1. **Delay buffer management** in `Process()` function
2. **Buffer indexing/alignment** issues
3. **State continuity** across frame boundaries
4. **Float32 to int16 conversion** in `Process()` wrapper

### Suspicious area in resample_libopus.go:Process()

```go
// Convert float32 to int16 for processing
in := make([]int16, len(samples))
for i, s := range samples {
    scaled := s * 32768.0
    if scaled > 32767 {
        in[i] = 32767
    } else if scaled < -32768 {
        in[i] = -32768
    } else {
        in[i] = int16(scaled)
    }
}
```

**POTENTIAL ISSUE**: The `int16(scaled)` conversion for values between -32768 and 32767 might have
rounding behavior differences. When `scaled` is close to 0 or has fractional parts, the truncation
toward zero could cause subtle differences that accumulate.

However, this alone wouldn't cause **sign inversion**.

---

## CRITICAL FINDING: Sign Inversions at NATIVE Rate

**The sign inversions occur in the SILK core output BEFORE resampling!**

### Native Rate Test Results (Packet 826, 8kHz)

```
=== Packet 826 at NATIVE rate (8kHz) ===
Go samples: 160, Lib samples: 160
SNR: -0.2 dB, MaxDiff: 0.012238 at sample 10
Sign inversions: 18 out of 160 samples

First sign inversions:
  [   5] go=+0.000153 lib=+0.000072  (same sign)
  [   6] go=+0.000458 lib=-0.001225  <-- FIRST SIGN INV
  [   7] go=+0.000458 lib=-0.002306  <-- SIGN INV
  [   8] go=+0.000305 lib=-0.003408  <-- SIGN INV
  [   9] go=+0.000183 lib=-0.006691  <-- SIGN INV
  [  10] go=+0.000061 lib=-0.012177  <-- MAX DIFF
```

### Key Observations

1. **Sign inversions start at sample 6** - gopus outputs POSITIVE where libopus outputs NEGATIVE
2. **The libopus signal has a negative pulse** (peak at sample 10: -0.012177)
3. **gopus completely misses this pulse** - values stay near zero/positive
4. **After sample 15, both decoders converge** back to similar values

### Root Cause: NOT the Resampler

The resampler coefficients and implementation are **verified identical** to libopus.

The sign inversions originate in the **SILK core decoder** before resampling.

### Next Investigation Steps

1. **Check SILK LPC synthesis** - potential sign error in filter application
2. **Check LTP (Long-Term Prediction)** - pitch prediction sign
3. **Check excitation signal generation** - PLC/excitation scaling
4. **Check state accumulation** - sLPC buffer, sLTP buffer signs
5. **Compare decoded parameters** - NLSFs, gains, pitch lags, LTP coefficients

The pattern (error appears suddenly at sample 6, peaks at sample 10, then diminishes)
suggests a **short impulse in the excitation** that gopus either:
- Generates with wrong sign, OR
- Misses entirely due to state corruption

### Files to Investigate

- `internal/silk/libopus_decode.go` - Main SILK decoding logic
- `internal/silk/libopus_lpc.go` - LPC synthesis filter
- `internal/silk/ltp.go` - Long-Term Prediction
- `internal/silk/excitation.go` - Excitation signal generation
