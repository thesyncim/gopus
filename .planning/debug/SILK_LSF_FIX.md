# SILK LSF/NLSF Quantization: gopus vs libopus Analysis

## Executive Summary

The SILK encoder in gopus uses fundamentally different approaches for LPC to LSF/NLSF conversion and VQ quantization compared to libopus. This document details the architectural differences and their implications.

## Key Differences

### 1. LPC to LSF/NLSF Conversion

#### gopus Implementation (`lsf_encode.go`)

```go
// Uses floating-point Chebyshev polynomial root finding
func lpcToLSFEncode(lpcQ12 []int16) []int16 {
    // Convert Q12 to float for computation
    lpc := make([]float64, order)
    for i := 0; i < order; i++ {
        lpc[i] = float64(lpcQ12[i]) / 4096.0
    }

    // Build P(z) and Q(z) symmetric/antisymmetric polynomials
    // Cumulative sum/difference construction

    // Root finding via bisection on cos(omega) in [0, pi]
    // Resolution: 1024 points with bisection refinement
}
```

**Characteristics:**
- Floating-point computation
- Generic Chebyshev polynomial evaluation
- Bisection root finding with 1e-8 tolerance
- Output: Q15 LSF values mapped from [0, pi]

#### libopus Implementation (`A2NLSF.c`)

```c
// Uses fixed-point with piecewise-linear cosine approximation
void silk_A2NLSF(opus_int16 *NLSF, opus_int32 *a_Q16, const opus_int d) {
    // Table-based cosine: silk_LSFCosTab_FIX_Q12 (129 entries)
    // Fixed-point polynomial evaluation in Q16

    // Binary division for root refinement (3 steps)
    // Linear interpolation for sub-sample precision

    // Bandwidth expansion fallback if roots not found
}
```

**Key libopus features:**
1. **Piecewise-linear cosine**: Uses precomputed Q12 table (129 values)
2. **Fixed-point arithmetic**: All computations in Q16/Q12
3. **Bandwidth expansion**: If roots not found, applies `silk_bwexpander_32` and retries
4. **NLSF format**: Output in Q15 representing index*256 + fractional

**Critical difference**: libopus NLSF is NOT a direct angular representation like LSF. It's an index-based representation that piecewise-linearly approximates cos(omega):
```
NLSF = k * 256 + ffrac  where silk_LSFCosTab_FIX_Q12[k] approximates cos(omega)
```

### 2. NLSF Codebook Structure

#### gopus (`codebook.go`)

```go
// Stage 1 codebook: 32 vectors, Q8 values
var LSFCodebookWB = [32][16]uint8{...}

// Stage 2 residual: 8 maps x 9 vectors x 16 coefficients
// Simplified uniform residuals
var LSFStage2ResWB = [8][9][16]int8{
    {
        {-3, -3, -3, ...},  // Same value across all coefficients
        {-2, -2, -2, ...},
        ...
    },
}
```

**Issue**: Stage 2 codebooks are placeholder/simplified uniform values, not the actual SILK VQ codebooks.

#### libopus (`structs.h`, `tables_NLSF_CB_*.c`)

```c
typedef struct {
    const opus_int16     nVectors;        // e.g., 32
    const opus_int16     order;           // 10 or 16
    const opus_int16     quantStepSize_Q16;     // 11796 (WB), 9830 (NB)
    const opus_int16     invQuantStepSize_Q6;   // 356 (WB), 427 (NB)
    const opus_uint8     *CB1_NLSF_Q8;    // Stage 1 codebook
    const opus_int16     *CB1_Wght_Q9;    // Per-vector weights for distortion
    const opus_uint8     *CB1_iCDF;       // Probability tables
    const opus_uint8     *pred_Q8;        // Prediction coefficients
    const opus_uint8     *ec_sel;         // Entropy coding selector
    const opus_uint8     *ec_iCDF;        // Stage 2 iCDF
    const opus_uint8     *ec_Rates_Q5;    // Rate tables
    const opus_int16     *deltaMin_Q15;   // Minimum spacing constraints
} silk_NLSF_CB_struct;
```

**Missing in gopus:**
1. `CB1_Wght_Q9` - Per-codeword weighting for distortion calculation
2. `pred_Q8` - Predictive coding coefficients
3. `ec_sel`/`ec_iCDF`/`ec_Rates_Q5` - Entropy coding tables
4. `quantStepSize_Q16`/`invQuantStepSize_Q6` - Quantization parameters

### 3. VQ Encoding Process

#### gopus (`lsf_quantize.go`)

```go
func (e *Encoder) searchStage1Codebook(...) (int, int64) {
    // Simple weighted MSE search
    for idx := 1; idx < numCodewords; idx++ {
        for i := 0; i < lpcOrder; i++ {
            diff := target - cbVal
            weight := e.computeLSFWeight(i, lpcOrder)  // Simplified perceptual weight
            dist += (diff * diff * weight) >> 8
        }
        rate := e.computeSymbolRate(idx, icdf)
        totalCost := dist + lambda*rate
    }
}

func (e *Encoder) computeStage2Residuals(...) []int {
    // Direct MSE search over 9 uniform residual vectors
    for resIdx := 0; resIdx < 9; resIdx++ {
        resVal := LSFStage2ResWB[mapIdx][resIdx][i] << 7
        dist := abs(target - resVal)
    }
}
```

#### libopus (`NLSF_encode.c`)

```c
opus_int32 silk_NLSF_encode(...) {
    // 1. NLSF stabilization first
    silk_NLSF_stabilize(pNLSF_Q15, psNLSF_CB->deltaMin_Q15, order);

    // 2. Stage 1: VQ with predictive weighting
    silk_NLSF_VQ(err_Q24, pNLSF_Q15, CB1_NLSF_Q8, CB1_Wght_Q9, nVectors, order);

    // 3. Multi-survivor search (nSurvivors typically 16-32)
    silk_insertion_sort_increasing(err_Q24, tempIndices1, nVectors, nSurvivors);

    // 4. For each survivor: Trellis-based stage 2 quantization
    for(s = 0; s < nSurvivors; s++) {
        // Compute weighted residual
        res_Q10[i] = (pNLSF_Q15[i] - CB1_NLSF_Q8[i]<<7) * W_tmp_Q9 >> 14;
        W_adj_Q5[i] = pW_Q2[i] / (W_tmp_Q9 * W_tmp_Q9);

        // Delayed-decision trellis quantization
        RD_Q25[s] = silk_NLSF_del_dec_quant(indices, res_Q10, W_adj_Q5,
                                            pred_Q8, ec_ix, ec_Rates_Q5, ...);
    }

    // 5. Select best survivor, decode to get quantized NLSF
    silk_NLSF_decode(pNLSF_Q15, NLSFIndices, psNLSF_CB);
}
```

**Key libopus features missing in gopus:**
1. **NLSF stabilization before VQ** - Not after
2. **Predictive VQ** - Uses prediction coefficients `pred_Q8`
3. **Weighted distortion** - Per-codeword weights `CB1_Wght_Q9`
4. **Multi-survivor search** - Keeps top N candidates
5. **Trellis quantization** - `silk_NLSF_del_dec_quant` uses delayed-decision
6. **Rate-distortion optimization** - Uses actual entropy rates

### 4. Stage 2 Quantization: Delayed-Decision Trellis

libopus uses a sophisticated delayed-decision trellis quantizer (`NLSF_del_dec_quant.c`):

```c
// Key parameters:
#define NLSF_QUANT_DEL_DEC_STATES 4  // Number of parallel states
#define NLSF_QUANT_MAX_AMPLITUDE 4   // Max residual amplitude
#define NLSF_QUANT_LEVEL_ADJ 0.1     // Quantization adjustment factor

// The algorithm:
// 1. Process coefficients in reverse order (order-1 to 0)
// 2. For each coefficient, track multiple quantization paths
// 3. Use prediction from previous coefficient's quantization
// 4. Apply rate cost from ec_Rates_Q5 tables
// 5. Prune paths based on RD cost
// 6. Select best path at the end
```

gopus uses a simple independent-coefficient search, missing:
- Predictive dependency between coefficients
- Multiple path tracking
- Trellis pruning

### 5. Quantization Level Adjustment

libopus applies `NLSF_QUANT_LEVEL_ADJ` (0.1) to dequantized values:
```c
if(out_Q10 > 0) {
    out_Q10 = out_Q10 - SILK_FIX_CONST(NLSF_QUANT_LEVEL_ADJ, 10);
} else if(out_Q10 < 0) {
    out_Q10 = out_Q10 + SILK_FIX_CONST(NLSF_QUANT_LEVEL_ADJ, 10);
}
```

This is not implemented in gopus.

## Impact Assessment

### On Encoder Output

| Aspect | Impact | Severity |
|--------|--------|----------|
| NLSF values | Different quantization levels | High |
| Bitstream | Different coded indices | High |
| Interoperability | Decoders will produce different output | Medium |
| Perceptual quality | May be slightly degraded | Low-Medium |

### On Decoder (Unchanged)

The decoder implementation in gopus correctly uses:
- `silkNLSF2A` for NLSF to LPC conversion
- `stabilizeLSF` for NLSF stabilization
- Correct interpolation handling

**The decoder should NOT be modified** - it already matches libopus behavior.

## Files Involved

### gopus Encoder (to potentially fix)
- `/Users/thesyncim/GolandProjects/gopus/internal/silk/lsf_encode.go` - LPC to LSF
- `/Users/thesyncim/GolandProjects/gopus/internal/silk/lsf_quantize.go` - VQ encoder
- `/Users/thesyncim/GolandProjects/gopus/internal/silk/codebook.go` - Codebook data

### gopus Decoder (working correctly)
- `/Users/thesyncim/GolandProjects/gopus/internal/silk/lsf.go` - LSF decoding/conversion
- `/Users/thesyncim/GolandProjects/gopus/internal/silk/libopus_nlsf.go` - NLSF2A

### libopus Reference
- `A2NLSF.c` - LPC to NLSF conversion
- `NLSF_encode.c` - NLSF VQ encoding
- `NLSF_VQ.c` - Stage 1 VQ distortion
- `NLSF_del_dec_quant.c` - Stage 2 trellis quantization
- `NLSF_decode.c` - NLSF decoding (for encoder feedback)
- `NLSF_stabilize.c` - NLSF stabilization
- `table_LSF_cos.c` - Cosine lookup table

## Recommended Actions

### Phase 1: Documentation and Testing (Current)
1. [x] Document the differences (this file)
2. [ ] Create CGO comparison test for A2NLSF
3. [ ] Create CGO comparison test for NLSF_encode

### Phase 2: Core Conversion (If fixing)
1. Implement fixed-point A2NLSF with cosine table
2. Add bandwidth expansion fallback
3. Verify NLSF format matches libopus

### Phase 3: VQ Encoding (If fixing)
1. Import actual CB1_Wght_Q9 and prediction tables
2. Implement multi-survivor search
3. Implement delayed-decision trellis quantizer
4. Add rate tables and RD optimization

### Phase 4: Integration
1. Update encoder pipeline to use fixed-point
2. Test against libopus bitstreams
3. Verify decoder compatibility

## Notes

1. **Decoder is fine** - Focus encoder fixes only
2. **Complexity trade-off** - libopus encoding is significantly more complex
3. **Bit-exact not required** - Perceptually acceptable is sufficient for most uses
4. **Test vectors** - Use existing testvector files for validation

## CGO Test Location

Test files are located in:
- `/Users/thesyncim/GolandProjects/gopus/internal/celt/cgo_test/silk_lsf_encode_compare_test.go` - LSF/NLSF encoding comparison tests

## CGO Test Results

The following tests were created and pass:

### TestSilkA2NLSFComparison
Compares LPC to NLSF conversion. libopus correctly converts LPC coefficients (Q16) to NLSF values (Q15). Roundtrip error (NLSF->LPC->NLSF) is very small (1-2 Q12 units).

### TestSilkNLSFCosineTable
Verifies the libopus cosine lookup table:
- Table size: 128 (129 entries for [0, pi])
- silk_LSFCosTab_FIX_Q12[0] = 8192 (2*cos(0))
- silk_LSFCosTab_FIX_Q12[64] = 0 (2*cos(pi/2))
- silk_LSFCosTab_FIX_Q12[128] = -8192 (2*cos(pi))

### TestSilkNLSFCodebookParams
Reveals key codebook parameters not present in gopus:

| Parameter | NB/MB | WB |
|-----------|-------|-----|
| nVectors | 32 | 32 |
| order | 10 | 16 |
| quantStepSize_Q16 | 11796 | 9830 |
| invQuantStepSize_Q6 | 356 | 427 |

### TestSilkNLSFStabilize
Tests minimum spacing constraints:
- NB/MB deltaMin: [250, 3, 6, 3, 3, 3, 4, 3, 3, 3, 461]
- WB deltaMin: [100, 3, 40, 3, 3, 3, 5, 14, 14, 10, 11, 3, 8, 9, 7, 3, 347]

### TestSilkNLSFEncodeCompare
Demonstrates full VQ encoding with:
- Stage 1 index (0-31)
- Stage 2 residual indices (typically -5 to +5)
- Quantized NLSF values differ from input by significant amounts due to codebook quantization
- Decode(Encode(x)) is consistent

### TestSilkCodebookStage1Values
Reveals per-codeword weights (CB1_Wght_Q9) not present in gopus:
- Weights vary significantly across coefficients (e.g., 2194-3216 for NB/MB)
- This affects rate-distortion optimization
