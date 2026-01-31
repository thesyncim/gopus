# TV12 Resampler Logic Analysis

## Executive Summary

**IMPORTANT CORRECTION**: The hypothesis that sign inversions are caused by the resampler is **INCORRECT**.

The actual bug is in the **SILK core decoder**, not the resampler. The evidence shows:
1. Native SILK output (before resampling) already has sign inversions
2. SNR divergence starts at bandwidth transitions (NB->MB->WB)
3. The resampler correctly passes through whatever the SILK decoder produces

---

## Evidence Disproving Resampler Bug

### Test: Fresh vs Stateful Resampler
```
Fresh vs State resampler comparison:
  SNR: 999.0 dB
  MaxDiff: 0.000000 at sample 0
```
The gopus resampler produces identical output whether fresh or with accumulated state.

### Test: Native SILK Output at Packet 826
```
Stateful native SNR: -0.2 dB
First 10 samples:
   [ 0] go=+0.000366 lib=-0.019253 diff=+0.019620
   [ 1] go=+0.000336 lib=-0.015500 diff=+0.015836
   [ 2] go=+0.000183 lib=-0.014521 diff=+0.014704
```
The SILK core output (BEFORE any resampling) is already massively different:
- gopus: small positive values
- libopus: larger negative values

This proves the resampler is NOT the source of sign inversions.

---

## Actual Bug Location: SILK Core Decoder

### Native Rate SNR Analysis

Examining native SILK output (no resampling) across bandwidth transitions:

| Packet | Bandwidth | Native SNR | Notes |
|--------|-----------|-----------|-------|
| 0 | NB (8kHz) | 999.0 dB | Perfect at start |
| 136 | NB (8kHz) | 6.0 dB | Starting to diverge |
| 137 | MB (12kHz) | 4.8 dB | **BW CHANGE** - significant error |
| 213 | MB (12kHz) | 9.9 dB | Partial recovery |
| 214 | WB (16kHz) | -3.3 dB | **BW CHANGE** - severe divergence |
| 215-300 | WB (16kHz) | -2 to -4 dB | **ALL WB packets fail** |
| 826 | NB (8kHz) | -0.2 dB | Error has accumulated |

### Key Observations

1. **Bandwidth transitions cause state desync**: Every bandwidth change correlates with SNR drops
2. **WB packets are worst**: All wideband packets have negative SNR after the first transition
3. **Error accumulates**: By packet 826, even NB packets have negative SNR

---

## Gopus Resampler Implementation Analysis

Despite the resampler not being the root cause, here is the analysis of the implementation for completeness:

### Files Analyzed
- `internal/silk/resample_libopus.go` - LibopusResampler
- `tmp_check/opus-1.6.1/silk/resampler*.c` - C reference

### LibopusResampler Structure
```go
type LibopusResampler struct {
    sIIR [6]int32     // IIR state for 2x upsampler (3rd order allpass x2)
    sFIR [8]int16     // FIR delay buffer (8-tap symmetric)
    invRatioQ16 int32 // Input/output ratio in Q16
    batchSize int32   // Samples per batch
    inputDelay int32  // Delay compensation
    fsInKHz int32     // Input rate kHz
    fsOutKHz int32    // Output rate kHz
    delayBuf []int16  // Delay buffer (size = fsInKHz)
}
```

### 8kHz -> 48kHz Resampling Path

1. **Input**: 160 samples at 8kHz (20ms frame)
2. **2x Upsample via IIR Allpass**: up2HQ() produces 320 samples at 16kHz
3. **FIR Interpolation**: firInterpol() produces 960 samples at 48kHz
4. **Output**: 960 samples at 48kHz

### Coefficient Comparison

**2x Upsampler Allpass (libopus):**
```c
silk_resampler_up2_hq_0[ 3 ] = { 1746, 14986, 39083 - 65536 }  // = {1746, 14986, -26453}
silk_resampler_up2_hq_1[ 3 ] = { 6854, 25769, 55542 - 65536 }  // = {6854, 25769, -9994}
```

**gopus:**
```go
silkResamplerUp2HQ0 = [3]int16{1746, 14986, 39083 - 65536}  // = {1746, 14986, -26453}
silkResamplerUp2HQ1 = [3]int16{6854, 25769, 55542 - 65536}  // = {6854, 25769, -9994}
```
**Match: YES**

**FIR Interpolation Coefficients:**
Both use identical 12-phase, 4-tap symmetric coefficients stored in `silkResamplerFracFIR12`.
**Match: YES**

### Fixed-Point Arithmetic

| Function | libopus | gopus | Match |
|----------|---------|-------|-------|
| silk_SMULWB | (a * int16(b)) >> 16 | (a * int64(int16(b))) >> 16 | YES |
| silk_SMLAWB | a + (b * int16(c)) >> 16 | a + (b * int64(int16(c))) >> 16 | YES |
| silk_SMULBB | int16(a) * int16(b) | int32(int16(a)) * int32(int16(b)) | YES |
| silk_SMLABB | a + int16(b) * int16(c) | a + int32(int16(b)) * int32(int16(c)) | YES |
| silk_RSHIFT_ROUND | ((x >> (shift-1)) + 1) >> 1 | ((x >> (shift-1)) + 1) >> 1 | YES |

---

## Recommendation: Investigate SILK Core Instead

The debugging effort should shift to investigating:

1. **Bandwidth transition handling** in SILK decoder
2. **LPC state management** when switching NB <-> MB <-> WB
3. **Excitation history buffer** state preservation across bandwidth changes
4. **LSF interpolation** coefficients at bandwidth boundaries

### Specific Areas to Check

```go
// internal/silk/decoder.go
type Decoder struct {
    // These need to be checked for proper reset/transition behavior
    lpcOrder      int
    prevLPCValues []float32
    prevLSFQ15    []int16
    outputHistory []float32
    // ...
}
```

The libopus resampler handles bandwidth changes by:
1. Using delay matrix (`delay_matrix_dec`) for proper delay compensation
2. Switching between `USE_silk_resampler_private_up2_HQ_wrapper` and `USE_silk_resampler_private_IIR_FIR`

But the **actual SILK core output is wrong before resampling even happens**.

---

## Conclusion

The resampler implementation in gopus appears correct. The sign inversions and divergence seen in TV12 compliance testing originate from the **SILK decoder core**, specifically in how state is managed during bandwidth transitions.

The earlier debug note claiming "Native SILK output matches at 8kHz" was testing with a **fresh decoder** that hadn't accumulated state from previous packets. With proper stateful decoding, the native output already shows massive divergence before any resampling occurs.
