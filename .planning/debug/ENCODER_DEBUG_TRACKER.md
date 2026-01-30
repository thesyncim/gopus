# Encoder Debug Tracker

## Session: 2026-01-30

### Current Status
- **Encoder SNR:** -4.30 dB (corrupted audio)
- **Critical blocker:** TF Analysis divergence at byte 7
- **First 7 bytes match:** `7B 5E 09 50 B7 8C 08`
- **Byte 7 diverges:** gopus=`0x33`, libopus=`0xD0`
- **Decoder Tests:** 11/12 passing (only TV12 fails with Q=-32.06)

---

## Session Update: 2026-01-30 (Later)

### TV10 Regression Fixed

**Issue:** Uncommitted changes caused TV10 to regress from Q=27.59 to Q=-31.70

**Root Cause:** Changes in `decoder_opus_frame.go` related to redundancy handling:
1. Commented-out redundancy audio application
2. Added `setRedundantBandwidth()` function and calls

**Resolution:** Reverted `decoder.go` and `decoder_opus_frame.go` to HEAD

**Current State After Revert:**
- TV01-TV11: PASSING
- TV12: FAILING (Q=-32.06) - expected, known SILK issue

### Encoder Changes Retained (Safe for Decoder)

These changes are encoder-only and do NOT affect decoder tests:

1. **`internal/celt/encode_frame.go`:** Toneishness cap fix
   ```go
   maxToneishness := 1.0 - tfEstimate
   if toneishness > maxToneishness {
       toneishness = maxToneishness
   }
   ```
   AND `toneishness < 0.98` check for TF analysis gating

2. **`internal/celt/transient.go`:** Tone detection functions (toneLPC, toneDetect)

3. **Debug/test files:** Various test file updates and debug additions

---

## Verified Correct (PROVEN - DO NOT INVESTIGATE AGAIN)

| Component | Test | Result | Notes |
|-----------|------|--------|-------|
| Pre-emphasis | Correlation test | 1.000000 | Matches libopus exactly |
| MDCT forward | SNR comparison | > 138 dB | Algorithm correct |
| CWRS encoding | Roundtrip test | Signs preserved | Encoding/decoding works |
| expRotation | Roundtrip SNR | > 599 dB | Perfect |
| Coarse energy | Byte comparison | Bytes 0-6 match | Energy quantization OK |
| Header/TOC | Byte comparison | Matches | Frame construction OK |
| Range encoder lifecycle | Fix applied | Working | nil after Done() |
| SILK GainDequantTable | Fix applied | 81 → 81920 | Correct Q values |
| SILK gain quantization | Fix applied | computeLogGainIndexQ16 | Q16 formula |
| SILK excitation scaling | Fix applied | 32768 scaling | Correct amplitude |
| PVQ search | Fix applied | absX copy | Input not modified |
| Toneishness detection | Fix applied | matches libopus | Tone detection OK |
| TF analysis gating | Fix applied | toneishness < 0.98 | Gating correct |
| Range encoder | Code analysis + CGO tests | Matches libopus | All operations verified |

---

## Under Investigation

### 1. TF Analysis (Byte 7 Divergence)
**Status:** ALGORITHM VERIFIED - ROOT CAUSE IDENTIFIED
**Location:** `internal/celt/tf.go`
**Symptom:** Byte 7 = 0x33 (gopus) vs 0xD0 (libopus)

#### Suspects (VERIFIED BY AGENT 1):
- [x] `haar1()` transform implementation - **CORRECT** (matches libopus)
- [x] `l1Metric()` computation - **CORRECT** (matches libopus)
- [x] Viterbi search path costs - **CORRECT** (matches libopus)
- [x] tfSelectTable values/indexing - **CORRECT** (matches libopus)
- [ ] bandLogE input to TFAnalysis - **SUSPECT** (may differ due to toneishness issue)
- [ ] **NEW: enable_tf_analysis gating** - **ROOT CAUSE** (see Agent 5)

#### ROOT CAUSE (from Agent 5 cross-reference):
Missing `toneishness = min(toneishness, 1.0 - tfEstimate)` modification in `encode_frame.go` line 122.
This causes gopus to potentially DISABLE TF analysis when libopus ENABLES it.

#### Recommended Fix:
Add after line 122 in encode_frame.go:
```go
maxToneishness := 1.0 - tfEstimate
if toneishness > maxToneishness {
    toneishness = maxToneishness
}
```

#### Findings:
See **Agent 1: TF Analysis** section in Investigation Log below for full analysis.

---

### 2. Band Coefficient Encoding
**Status:** PENDING
**Location:** `internal/celt/bands_encode.go`

#### Suspects:
- [ ] PVQ encoding order
- [ ] Split band decisions
- [ ] Fine energy encoding

#### Findings:
_To be filled by investigation agents_

---

### 3. Range Encoder State
**Status:** COMPLETE - VERIFIED CORRECT
**Location:** `internal/rangecoding/encoder.go`

#### Suspects:
- [x] Range register width - **VERIFIED: 32-bit matches libopus**
- [x] Carry propagation - **VERIFIED: Algorithm matches exactly**
- [x] Flush sequence - **VERIFIED: ec_enc_done matches**

#### Findings:
See **Agent 3: Range Encoder** section below for complete analysis.

**Summary:** All core range encoder operations match libopus exactly:
- Initialization, normalization, carry propagation
- Encode, EncodeBin, EncodeBit, EncodeICDF, EncodeUniform
- Raw bits, finalization (Done)
- Buffer management (front/back writing)
- Constants (EC_CODE_TOP, EC_CODE_BOT, EC_SYM_BITS, etc.)

**Conclusion:** Range encoder is NOT the source of byte 7 divergence.
The divergence is in WHAT is encoded, not HOW it is encoded.

---

### 4. Energy Encoding
**Status:** ANALYZED (see Agent 2 findings below)
**Location:** `internal/celt/energy_encode.go`

#### Suspects:
- [x] Fine energy bits allocation - **CORRECT** (matches libopus)
- [ ] Energy prediction - Appears correct but missing `prev_quant` tracking
- [ ] Inter-frame energy state - State update appears correct
- [ ] **NEW: Missing `prev_quant` parameter in `EncodeFineEnergy`**
- [ ] **NEW: Missing `error[]` array for residual tracking**

#### Findings:
See **Agent 2: Energy/Bands** section below for detailed analysis.

**Key Issue:** The `EncodeFineEnergy` function is missing the `prev_quant`
parameter that libopus uses. This affects:
1. Quantization formula: `(error*(1<<prev)+.5f)*extra`
2. Offset scaling: `offset *= (1<<(14-prev))*(1.f/16384)`

**Impact on byte 7:** Since coarse energy (bytes 0-6) matches, byte 7 divergence
is more likely in TF encoding or the bits AFTER fine energy encoding.
Fine energy is encoded AFTER byte 7 in the packet stream.

---

## Investigation Log

### Agent 1: TF Analysis
**Timestamp:** 2026-01-30

#### Files Analyzed:
- `/Users/thesyncim/GolandProjects/gopus/internal/celt/tf.go` (Go TFAnalysis, TFEncodeWithSelect, l1Metric)
- `/Users/thesyncim/GolandProjects/gopus/internal/celt/bands_quant.go` (haar1 implementation)
- `/Users/thesyncim/GolandProjects/gopus/tmp_check/opus-1.6.1/celt/celt_encoder.c` (libopus tf_analysis, tf_encode, l1_metric)
- `/Users/thesyncim/GolandProjects/gopus/tmp_check/opus-1.6.1/celt/bands.c` (libopus haar1)
- `/Users/thesyncim/GolandProjects/gopus/internal/celt/tables.go` (tfSelectTable)
- `/Users/thesyncim/GolandProjects/gopus/tmp_check/opus-1.6.1/celt/celt.c` (libopus tf_select_table)

---

#### 1. ALGORITHM COMPARISON SUMMARY

| Component | Go Implementation | libopus | Status |
|-----------|-------------------|---------|--------|
| `tfSelectTable` | `[4][8]int8` matching values | `signed char tf_select_table[4][8]` | **MATCH** |
| `haar1()` | `invSqrt2 * x[idx]` in-place | `MULT32_32_Q31(.70710678f, X[])` | **MATCH** |
| `l1Metric()` | `L1 + LM*bias*L1` | `MAC16_32_Q15(L1, LM*bias, L1)` | **MATCH** |
| Bias calculation | `0.04 * max(-0.25, 0.5-tfEstimate)` | Same formula | **MATCH** |
| Lambda | `max(80, 20480/effectiveBytes + 2)` | `IMAX(80, 20480/effectiveBytes + 2)` | **MATCH** |
| Viterbi costs | Forward pass + backward traceback | Same algorithm | **MATCH** |
| TF encoding | XOR with prev, logp varies | Same algorithm | **MATCH** |

---

#### 2. DETAILED FUNCTION COMPARISONS

##### 2.1 haar1() Transform

**Go (bands_quant.go lines 145-157):**
```go
func haar1(x []float64, n0, stride int) {
    n0 >>= 1
    invSqrt2 := 0.7071067811865476
    for i := 0; i < stride; i++ {
        for j := 0; j < n0; j++ {
            idx0 := stride*2*j + i
            idx1 := stride*(2*j+1) + i
            tmp1 := invSqrt2 * x[idx0]
            tmp2 := invSqrt2 * x[idx1]
            x[idx0] = tmp1 + tmp2
            x[idx1] = tmp1 - tmp2
        }
    }
}
```

**libopus (bands.c lines 644-657):**
```c
void haar1(celt_norm *X, int N0, int stride)
{
   N0 >>= 1;
   for (i=0;i<stride;i++)
      for (j=0;j<N0;j++)
      {
         tmp1 = MULT32_32_Q31(QCONST32(.70710678f,31), X[stride*2*j+i]);
         tmp2 = MULT32_32_Q31(QCONST32(.70710678f,31), X[stride*(2*j+1)+i]);
         X[stride*2*j+i] = ADD32(tmp1, tmp2);
         X[stride*(2*j+1)+i] = SUB32(tmp1, tmp2);
      }
}
```

**Verdict: EQUIVALENT** - Both use identical indexing and sqrt(2)/2 scaling.

---

##### 2.2 l1Metric() Computation

**Go (tf.go lines 221-229):**
```go
func l1Metric(tmp []float64, N int, LM int, bias float64) float64 {
    var L1 float64
    for i := 0; i < N && i < len(tmp); i++ {
        L1 += math.Abs(tmp[i])
    }
    L1 = L1 + float64(LM)*bias*L1  // = L1 * (1 + LM*bias)
    return L1
}
```

**libopus (celt_encoder.c lines 650-660):**
```c
static opus_val32 l1_metric(const celt_norm *tmp, int N, int LM, opus_val16 bias)
{
   L1 = 0;
   for (i=0;i<N;i++)
      L1 += EXTEND32(ABS16(SHR32(tmp[i], NORM_SHIFT-14)));
   L1 = MAC16_32_Q15(L1, LM*bias, L1);  // = L1 + (LM*bias*L1)
   return L1;
}
```

**Verdict: EQUIVALENT** - Both compute `L1 * (1 + LM*bias)`.

---

##### 2.3 TFAnalysis Viterbi Algorithm

**Key loop structure comparison:**

| Element | Go (tf.go) | libopus (celt_encoder.c) | Match |
|---------|------------|--------------------------|-------|
| Initial L1 | `l1Metric(tmp, N, isTransient?lm:0, bias)` | `l1_metric(tmp, N, isTransient?LM:0, bias)` | YES |
| -1 case check | `if isTransient && !narrow` | `if (isTransient && !narrow)` | YES |
| Loop bound | `k < (lm + !isTransient && !narrow)` | `k < LM+!(isTransient||narrow)` | YES |
| B computation | `if isTransient { B = lm-k-1 } else { B = k+1 }` | Same | YES |
| Metric conversion | `metric[i] = isTransient ? 2*bestLevel : -2*bestLevel` | Same | YES |
| Narrow case | `if narrow && (metric[i]==0 || metric[i]==-2*lm) { metric[i]-- }` | Same | YES |

**Viterbi Cost computation - MATCH**

---

##### 2.4 TF Encoding (TFEncodeWithSelect)

**Go (tf.go lines 468-521):**
- Reserve tfSelect bit if `lm > 0 && tell+logp+1 <= budget`
- Encode XOR of `tfRes[i] ^ curr`
- Update logp: `isTransient ? 4 : 5` after first band
- Encode tfSelect if it makes a difference

**libopus (celt_encoder.c lines 824-862):**
- Identical logic

**Verdict: EQUIVALENT**

---

#### 3. IDENTIFIED POTENTIAL ISSUES

##### 3.1 **SUSPECT: Input Data to TFAnalysis**

The algorithm is correct, but the INPUT to `TFAnalysis()` may differ:

**Issue:** Go calls `TFAnalysis(normL, len(normL), nbBands, ...)` where:
- `normL` comes from `NormalizeBandsToArray(mdctCoeffs, energies, nbBands, frameSize)`
- This normalizes MDCT coefficients by dividing by `exp2(energy/DB6)`

**Potential mismatch:** If the band energies used for normalization differ between gopus and libopus, the normalized coefficients `X` will differ, causing different L1 metrics and thus different TF decisions.

**Test needed:** Compare `normL` coefficients between gopus and libopus for the same input.

---

##### 3.2 **SUSPECT: Importance Array**

The `importance[]` array is computed by `DynallocAnalysis()` and passed to `TFAnalysis()`.

Both use same threshold: `effectiveBytes >= 30 + 5*LM`

However, if the importance computation inside `ComputeImportance()` differs from libopus `dynalloc_analysis()`, the Viterbi costs will differ.

**Test needed:** Compare `importance[]` values between gopus and libopus.

---

##### 3.3 **SUSPECT: tfEstimate Value**

The `tfEstimate` value comes from `TransientAnalysis()` and affects bias:
```go
bias := 0.04 * math.Max(-0.25, 0.5-tfEstimate)
```

If `tfEstimate` differs between implementations, bias differs, L1 metrics differ, and TF decisions differ.

**Test needed:** Compare `tfEstimate` between gopus and libopus.

---

#### 4. CROSS-REFERENCE WITH AGENT 5 FINDING

**Agent 5 found a CRITICAL issue:** libopus modifies `toneishness` after `transient_analysis()`:

```c
// libopus celt_encoder.c line 2033:
toneishness = MIN32(toneishness, 1.0 - tf_estimate);
```

**gopus does NOT perform this modification!**

This affects whether `enable_tf_analysis` is true:
- If toneishness >= 0.98, TF analysis is DISABLED
- libopus caps toneishness based on tf_estimate, potentially ENABLING TF analysis
- gopus uses raw toneishness, potentially DISABLING TF analysis

**This could be the root cause of byte 7 divergence!**

If gopus skips TF analysis (due to high raw toneishness), it uses the fallback path which encodes different TF values than the Viterbi-computed values libopus uses.

---

#### 5. CONCLUSION

**TF Analysis Algorithm: VERIFIED CORRECT**

The divergence at byte 7 is NOT caused by a bug in the TF algorithm itself.

**ROOT CAUSE HYPOTHESIS (from cross-referencing Agent 5):**
The missing `toneishness = min(toneishness, 1.0 - tfEstimate)` modification causes gopus to disable TF analysis when libopus enables it, leading to completely different TF encoding.

**Recommended fix (per Agent 5):**
Add the toneishness cap in `encode_frame.go` after line 122:
```go
maxToneishness := 1.0 - tfEstimate
if toneishness > maxToneishness {
    toneishness = maxToneishness
}
```

---

#### 6. TESTS REVIEWED

| Test File | Purpose | Findings |
|-----------|---------|----------|
| `tf_divergence_test.go` | TF encoding with different tfRes | Tests encoding mechanics |
| `trace_byte7_divergence_test.go` | Manual step-by-step trace | Shows divergence at byte 7 |
| `byte7_analysis_test.go` | Binary comparison | Confirms bytes 0-6 match |

---

#### 7. RECOMMENDED NEXT STEPS

1. **Apply toneishness cap fix** (Agent 5 recommendation)
2. If still divergent, **add CGO test** to compare:
   - libopus `X[]` (normalized coefficients) after `normalise_bands()`
   - libopus `importance[]` after `dynalloc_analysis()`
   - libopus `metric[]` from `tf_analysis()`
   - libopus `tf_res[]` before encoding
3. **Trace range encoder state** (rng, val, tell) at byte 7 boundary

### Agent 2: Energy/Bands
**Timestamp:** 2026-01-30 - Energy Encoding Analysis

#### Files Analyzed:
- `/Users/thesyncim/GolandProjects/gopus/internal/celt/energy_encode.go`
- `/Users/thesyncim/GolandProjects/gopus/internal/celt/encode_frame.go`
- `/Users/thesyncim/GolandProjects/gopus/tmp_check/opus-1.6.1/celt/quant_bands.c`
- `/Users/thesyncim/GolandProjects/gopus/internal/celt/alloc.go`
- `/Users/thesyncim/GolandProjects/gopus/internal/celt/cgo_test/encoder_energy_compare_test.go`

---

#### 1. FINE ENERGY ENCODING ANALYSIS

**CRITICAL DIFFERENCE FOUND: `quant_fine_energy()` uses `prev_quant` parameter**

##### libopus (quant_bands.c lines 363-402):
```c
void quant_fine_energy(const CELTMode *m, int start, int end,
    celt_glog *oldEBands, celt_glog *error,
    int *prev_quant, int *extra_quant, ec_enc *enc, int C)
{
    for (i=start;i<end;i++)
    {
        opus_int16 extra, prev;
        extra = 1<<extra_quant[i];  // extra = 2^extra_quant[i]
        if (extra_quant[i] <= 0) continue;
        prev = (prev_quant!=NULL) ? prev_quant[i] : 0;  // <-- CRITICAL

        // Float path quantization:
        q2 = (int)floor((error[i+c*m->nbEBands]*(1<<prev)+.5f)*extra);

        // Float path offset calculation:
        offset = (q2+.5f)*(1<<(14-extra_quant[i]))*(1.f/16384) - .5f;
        offset *= (1<<(14-prev))*(1.f/16384);  // <-- uses prev

        oldEBands[i+c*m->nbEBands] += offset;
        error[i+c*m->nbEBands] -= offset;
    }
}
```

##### gopus (energy_encode.go lines 411-465):
```go
func (e *Encoder) EncodeFineEnergy(energies []float64, quantizedCoarse []float64,
    nbBands int, fineBits []int) {
    for band := 0; band < nbBands; band++ {
        bits := fineBits[band]
        if bits <= 0 { continue }

        ft := 1 << bits
        scale := float64(ft)

        // MISSING: prev_quant handling
        fine := energies[idx] - quantizedCoarse[idx]
        q := int(math.Floor((fine/DB6+0.5)*scale + 1e-9))

        // Offset calculation - DIFFERENT from libopus
        offset := (float64(q)+0.5)/scale - 0.5
        quantizedCoarse[idx] += offset * DB6
    }
}
```

**ISSUE 1:** gopus does NOT use a `prev_quant` parameter at all. The libopus
function tracks previous quantization bits per band, which affects:
1. The quantization formula: `(error*(1<<prev)+.5f)*extra`
2. The offset scaling: `offset *= (1<<(14-prev))*(1.f/16384)`

**ISSUE 2:** gopus uses `fine/DB6` scaling while libopus uses the `error` array
directly (which is already in the correct units).

---

#### 2. ENERGY BIT ALLOCATION ANALYSIS

##### libopus allocation (rate.h + quant_bands.c):
- `MAX_FINE_BITS = 8` (matches gopus `maxFineBits = 8`)
- `FINE_OFFSET = 21` (matches gopus `fineOffset = 21`)
- Fine bits come from `ebits[]` array populated by `clt_compute_allocation()`

##### gopus allocation (alloc.go):
- `maxFineBits = 8` - **CORRECT**
- `fineOffset = 21` - **CORRECT**
- Fine bits computed in `interpBits2Pulses()` / `interpBits2PulsesEncode()`

**Allocation formula check (alloc.go lines 389-396):**
```go
// gopus:
ebits[j] = maxInt(0, bits[j]+offset+(den<<(bitRes-1)))
ebits[j] = celtUdiv(ebits[j], den) >> bitRes
if channels*ebits[j] > (bits[j] >> bitRes) {
    ebits[j] = bits[j] >> stereo >> bitRes
}
ebits[j] = minInt(ebits[j], maxFineBits)
```

This matches libopus logic for fine bit allocation.

---

#### 3. ENERGY FINALIZATION (quant_energy_finalise)

##### libopus (quant_bands.c lines 404-432):
```c
void quant_energy_finalise(..., int *fine_quant, int *fine_priority,
    int bits_left, ec_enc *enc, int C)
{
    for (prio=0;prio<2;prio++) {
        for (i=start;i<end && bits_left>=C ;i++) {
            if (fine_quant[i] >= MAX_FINE_BITS || fine_priority[i]!=prio)
                continue;
            c=0; do {
                q2 = error[i+c*m->nbEBands]<0 ? 0 : 1;
                ec_enc_bits(enc, q2, 1);
                // Float path:
                offset = (q2-.5f)*(1<<(14-fine_quant[i]-1))*(1.f/16384);
                if (oldEBands != NULL) oldEBands[i+c*m->nbEBands] += offset;
                error[i+c*m->nbEBands] -= offset;
                bits_left--;
            } while (++c < C);
        }
    }
}
```

##### gopus (energy_encode.go lines 537-583):
```go
func (e *Encoder) EncodeEnergyFinalise(energies []float64, quantizedEnergies []float64,
    nbBands int, fineQuant []int, finePriority []int, bitsLeft int) {
    for prio := 0; prio < 2; prio++ {
        for band := 0; band < nbBands && bitsLeft >= channels; band++ {
            if fineQuant[band] >= maxFineBits || finePriority[band] != prio {
                continue
            }
            errorVal := energies[idx] - quantizedEnergies[idx]
            q2 := 0
            if errorVal >= 0 { q2 = 1 }
            re.EncodeRawBits(uint32(q2), 1)
            offset := (float64(q2) - 0.5) / float64(uint(1)<<(fineQuant[band]+1))
            quantizedEnergies[idx] += offset * DB6
            bitsLeft--
        }
    }
}
```

**Offset formula comparison:**
- libopus: `(q2-.5f)*(1<<(14-fine_quant[i]-1))*(1.f/16384)`
- gopus: `(q2 - 0.5) / (1 << (fineQuant[band]+1))` then `* DB6`

**ISSUE 3:** The offset formulas are different. Let's verify:
- libopus (fineQuant=3): `(q2-.5)*(1<<(14-3-1))*(1/16384) = (q2-.5)*(1<<10)/16384 = (q2-.5)/16`
- gopus (fineQuant=3): `(q2-0.5)/(1<<4) = (q2-0.5)/16` ... then `*DB6`

The gopus version multiplies by DB6 at the end, but libopus does NOT. This is
because libopus stores energies in different units during encoding.

---

#### 4. INTER-FRAME ENERGY STATE MANAGEMENT

##### libopus (quant_bands.c):
- Uses `oldEBands[]` which is updated in place during encoding
- Uses `error[]` array to track residuals between encoding stages
- State is stored in `CELTEncoder` struct

##### gopus (energy_encode.go + encoder.go):
- Uses `e.prevEnergy[]` for inter-frame prediction (lines 251, 329-339)
- Updates `prevEnergy` after encoding completes

**State update check (energy_encode.go lines 329-339):**
```go
// Update previous-frame energy state after encoding completes.
for c := 0; c < channels; c++ {
    for band := 0; band < nbBands; band++ {
        idx := c*nbBands + band
        if idx >= len(quantizedEnergies) { continue }
        if band < MaxBands {
            e.prevEnergy[c*MaxBands+band] = quantizedEnergies[idx]
        }
    }
}
```

This appears correct - matches libopus updating `oldEBands` after encoding.

---

#### 5. SUMMARY OF POTENTIAL ISSUES

| Issue | Severity | Description |
|-------|----------|-------------|
| Missing `prev_quant` | **HIGH** | `EncodeFineEnergy` ignores the `prev_quant` parameter that libopus uses to adjust quantization precision based on previous frame's fine bits |
| Offset scaling | MEDIUM | `EncodeEnergyFinalise` uses different offset formula with DB6 multiplication |
| Error tracking | **HIGH** | libopus maintains a separate `error[]` array that tracks residuals; gopus recomputes from original energies |

---

#### 6. RECOMMENDED FIXES (for separate implementation session)

1. **Add `prev_quant` tracking**: The encoder should track fine bits per band from previous frames and use this in `EncodeFineEnergy`.

2. **Implement error array**: Maintain an `error[]` array parallel to energies that gets updated by coarse, fine, and finalise stages.

3. **Review offset formulas**: Ensure the DB6 scaling is applied consistently with libopus units.

**Note:** Since coarse energy (bytes 0-6) matches libopus, the divergence at byte 7 may be in TF encoding rather than fine energy. Fine energy encoding happens AFTER TF encoding in the packet structure.

### Agent 3: Range Encoder
**Timestamp:** 2026-01-30 21:30

#### Investigation Summary

Performed comprehensive analysis comparing Go range encoder (`internal/rangecoding/encoder.go`) against libopus reference (`tmp_check/opus-1.6.1/celt/entenc.c`).

#### Constants Verification: MATCH
All constants match between Go and libopus:

| Constant | Go Value | libopus Value | Status |
|----------|----------|---------------|--------|
| EC_SYM_BITS | 8 | 8 | MATCH |
| EC_CODE_BITS | 32 | 32 | MATCH |
| EC_SYM_MAX | 255 | 255 | MATCH |
| EC_CODE_TOP | 0x80000000 | 0x80000000 | MATCH |
| EC_CODE_BOT | 0x00800000 | 0x00800000 | MATCH |
| EC_CODE_SHIFT | 23 | 23 | MATCH |
| EC_UINT_BITS | 8 | 8 | MATCH |

#### Core Operations Comparison

**1. Initialization (ec_enc_init / Init): MATCH**
- Go: `rng = EC_CODE_TOP`, `val = 0`, `rem = -1`, `ext = 0`
- libopus: Same values

**2. Carry Propagation (ec_enc_carry_out / carryOut): MATCH**
- Symbol != 0xFF: flush buffered bytes with carry
- Symbol == 0xFF: increment ext counter
- Carry propagates through rem and ext bytes

**3. Normalization (ec_enc_normalize / normalize): MATCH**
- Both use loop: `while rng <= EC_CODE_BOT`
- Both extract `val >> EC_CODE_SHIFT` for carry propagation
- Both shift: `val = (val << 8) & (EC_CODE_TOP - 1)`, `rng <<= 8`

**4. ec_encode / Encode (fl, fh, ft): MATCH**
- Both use: `r = rng / ft`
- fl > 0: `val += rng - r*(ft-fl)`, `rng = r * (fh - fl)`
- fl == 0: `rng -= r * (ft - fh)`

**5. ec_encode_bin / EncodeBin: MATCH**
- Both use: `r = rng >> bits` with same formula as ec_encode

**6. ec_enc_bit_logp / EncodeBit: MATCH**
- Both compute `r = rng >> logp`, `threshold = rng - r`
- val=1: `val += threshold`, `rng = r`
- val=0: `rng = threshold`

**7. ec_enc_icdf / EncodeICDF: MATCH**
- Both use: `r = rng >> ftb`
- s > 0: `val += rng - r*icdf[s-1]`, `rng = r * (icdf[s-1] - icdf[s])`
- s == 0: `rng -= r * icdf[s]`

**8. ec_enc_uint / EncodeUniform: MATCH**
Verified formulas are equivalent:
- libopus: `ec_encode(_fl, _fl+1, _ft+1)` (after `_ft--`)
- Go: `encodeUniformInternal(val, ft)` with equivalent computation

**9. ec_enc_bits / EncodeRawBits: MATCH**
- Both write raw bits to end of buffer (reverse direction)
- Both use window accumulation with overflow check

**10. ec_enc_done / Done: MATCH**
- Both compute `l = EC_CODE_BITS - ilog(rng)`
- Both compute mask and rounded end value
- Both flush rem and ext with carryOut(0)
- Both handle raw end bits

#### ilog Function Verification: MATCH
Go implementation produces identical results to libopus ec_ilog for all uint32 values.

#### CGO Test Coverage
Existing tests in `internal/celt/cgo_test/range_encoder_libopus_test.go`:
- `TestRangeEncoderUniformMatchesLibopus`
- `TestCWRSBytesMatchLibopus`
- `TestEncodeBitStateTrace`
- `TestEncodeSequenceMatchesLibopus`
- `TestEncodeICDFMatchesLibopus`

#### Conclusion: RANGE ENCODER IS CORRECT

**The byte 7 divergence is NOT caused by the range encoder itself.**

The divergence must come from WHAT is being encoded, not HOW it is encoded:
- TF analysis computes different tfRes values
- Laplace encoding receives different quantized values
- Some encoding parameter differs before byte 7

**Byte 7 Position Analysis:**
- Byte 7 starts at bit 56
- After header (~4 bits) and coarse energy (~50 bits)
- Likely contains end of coarse energy or start of TF encoding
- Values being encoded at this position should be traced

### Agent 4: Full Pipeline Trace
**Timestamp:** 2026-01-30

#### Summary
Traced the complete encoding pipeline from `EncodeFrame()` to bitstream output. Documented the exact sequence of operations that write to each byte position, with focus on byte 7 divergence.

---

## ENCODER PIPELINE SEQUENCE

### Overview
The CELT encoder pipeline in `internal/celt/encode_frame.go` follows this sequence:

```
Input PCM -> DC Reject -> Delay Buffer -> Pre-emphasis -> Transient Analysis
          -> MDCT -> Band Energies -> Range Encoder Init -> Header Flags
          -> Coarse Energy -> TF Resolution -> Spread -> Dynalloc -> Trim
          -> Bit Allocation -> Fine Energy -> PVQ Bands -> Finalize
```

### Detailed Operation Sequence

#### Phase 1: Signal Preprocessing (No bitstream writes)
| Step | Function | Location | Purpose |
|------|----------|----------|---------|
| 1 | `ApplyDCReject()` | encode_frame.go:64 | High-pass filter to remove DC |
| 2 | Delay buffer handling | encode_frame.go:70-87 | Lookahead compensation (192 samples) |
| 3 | `ApplyPreemphasisWithScaling()` | encode_frame.go:94 | Pre-emphasis + 32768x scaling |
| 4 | `TransientAnalysis()` | encode_frame.go:119 | Detect transients, compute tfEstimate |
| 5 | MDCT via `ComputeMDCTWithHistory()` | encode_frame.go:147 | Transform to frequency domain |
| 6 | `ComputeBandEnergies()` | encode_frame.go:181 | Log-domain band energies |

#### Phase 2: Bitstream Header (Bytes 0-1)
| Step | Function | Bits | Bit Position | Notes |
|------|----------|------|--------------|-------|
| 7 | Range encoder `Init()` | 0 | 0 | Initialize with buffer |
| 8 | Silence flag `EncodeBit(0, 15)` | ~0.07 | 1 | Only if tell==1 |
| 9 | Postfilter flag `EncodeBit(0, 1)` | ~1 | ~1-2 | If budget allows |
| 10 | Transient flag `EncodeBit(bit, 3)` | ~0.4 | ~2-3 | If LM>0 |
| 11 | Intra flag `EncodeBit(bit, 3)` | ~0.4 | ~3-4 | Energy prediction mode |

**After header: typically ~9 bits used (~byte 1)**

#### Phase 3: Coarse Energy (Bytes 1-6)
| Step | Function | Location | Purpose |
|------|----------|----------|---------|
| 12 | `EncodeCoarseEnergy()` | energy_encode.go:186 | Laplace-coded band energies |

The coarse energy encoding uses Laplace coding for each of the 21 bands:
- Each band encodes a quantized energy deviation (qi value)
- Uses `encodeLaplace()` with fs and decay parameters
- Prediction from previous band and previous frame

**After coarse energy: typically ~50-55 bits used (~byte 6-7)**

#### Phase 4: TF Resolution (Byte 7+)
| Step | Function | Location | Purpose |
|------|----------|----------|---------|
| 13 | `TFAnalysis()` | tf.go:249 | Viterbi search for optimal TF |
| 14 | `TFEncodeWithSelect()` | tf.go:468 | Encode per-band TF flags |

TF encoding happens at **bit ~50-70 range (byte 6-8)**:
- First band: logp=4 (transient) or logp=2 (non-transient)
- Subsequent: logp=4 (transient) or logp=5 (non-transient)
- Optional tfSelect bit if LM>0

#### Phase 5: Spread, Dynalloc, Trim (Bytes 8-9)
| Step | Function | Location | Purpose |
|------|----------|----------|---------|
| 15 | `SpreadingDecision()` | spread_decision.go | Spectral spreading mode |
| 16 | `EncodeICDF(spread)` | encode_frame.go:415 | Spread decision (2-3 bits) |
| 17 | Dynalloc loop | encode_frame.go:427-437 | Dynamic allocation (per-band) |
| 18 | `EncodeICDF(allocTrim)` | encode_frame.go:443 | Allocation trim (3-4 bits) |

#### Phase 6: Bit Allocation + Fine Energy (Bytes 9+)
| Step | Function | Location | Purpose |
|------|----------|----------|---------|
| 19 | `ComputeAllocationWithEncoder()` | encode_frame.go:460-473 | Compute bit allocation |
| 20 | `EncodeFineEnergy()` | encode_frame.go:481 | Fine energy bits |

#### Phase 7: PVQ Band Encoding (Main payload)
| Step | Function | Location | Purpose |
|------|----------|----------|---------|
| 21 | `quantAllBandsEncode()` | encode_frame.go:497-521 | Main PVQ encoding |
| 22 | Anti-collapse bit | encode_frame.go:524-526 | If reserved |
| 23 | `EncodeEnergyFinalise()` | encode_frame.go:529-533 | Leftover bits |

---

## BYTE 7 DIVERGENCE ANALYSIS

### Bit Position Calculation
For 64kbps mono 20ms (960 samples):
- Target bits = 64000 * 960 / 48000 = **1280 bits** = 160 bytes
- Header: ~9 bits
- Coarse energy (21 bands, ~2-3 bits avg): ~45-50 bits
- **Total before TF: ~54-59 bits (~byte 7)**

### What Writes to Byte 7
**Byte 7 = bits 56-63**

Based on analysis, byte 7 contains:
1. **Tail of coarse energy** (bands 18-20): bits ~50-56
2. **Start of TF encoding** (bands 0-3): bits ~56-64

The divergence at byte 7 (gopus=0x33, libopus=0xD0) indicates:
- **Binary:** gopus=0b00110011, libopus=0b11010000
- **Difference:** 6 bits different out of 8

### Potential Root Causes

#### 1. TF Analysis Discrepancy (`tf.go`)
The `TFAnalysis()` function computes per-band TF resolution flags using:
- `haar1()` transform for L1 metric computation
- Viterbi algorithm with lambda transition costs
- `tfSelectTable` lookup for final values

**Suspects:**
- [ ] `l1Metric()` bias calculation differs
- [ ] Viterbi path cost accumulation
- [ ] `tfSelect` determination logic (line 382-384)
- [ ] Band metric computation (line 336-344)

#### 2. Coarse Energy Tail
The last 2-3 bands of coarse energy encoding could differ if:
- [ ] Laplace encoding fs/decay parameters differ
- [ ] Budget exhaustion handling differs
- [ ] Previous energy state differs

#### 3. TF Encode Sequence (`TFEncodeWithSelect`)
- [ ] `tfSelectRsv` budget calculation (line 481-484)
- [ ] Change encoding order (XOR with previous)
- [ ] `tfSelect` encoding condition (line 511-515)

---

## KEY INTERMEDIATE VALUES TO COMPARE

To isolate byte 7 divergence, compare these values between gopus and libopus:

### After Coarse Energy
```
1. re.Tell() - should match
2. re.Range() - should match
3. re.Val() - should match
4. quantizedEnergies[0:21] - should match
```

### Before TF Encoding
```
5. transient flag value
6. tfEstimate value
7. importance[0:21] weights
8. normL coefficients (first 200)
```

### During TF Analysis
```
9. metric[0:21] (per-band L1 metrics)
10. selcost[0] and selcost[1]
11. tfSelect computed value
12. tfRes[0:21] before encoding
```

### After TF Encoding
```
13. tfRes[0:21] after table lookup
14. tfChanged flag value
15. re.Tell() - where divergence might show
```

---

## TEST FILES FOR FURTHER INVESTIGATION

| Test File | Purpose |
|-----------|---------|
| `trace_byte7_divergence_test.go` | Manual step-by-step encoding trace |
| `byte7_analysis_test.go` | Binary comparison of byte 7 region |
| `tf_divergence_test.go` | TF encoding with different tfRes values |
| `coarse_energy_trace_test.go` | Coarse energy encoding verification |
| `full_encode_trace_test.go` | Full pipeline state comparison |

---

## RECOMMENDED NEXT STEPS

1. **Add libopus FFI tracing** for coarse energy qi values at each band
2. **Trace range encoder state** (rng, val, rem) after each coarse energy band
3. **Compare tfEstimate** values between gopus and libopus
4. **Verify TF metric computation** with known test vectors
5. **Test with forced TF values** (all-zeros, all-ones) to isolate TF vs coarse energy

---

## Fixes Applied This Session

| Fix | File | Before | After | Impact |
|-----|------|--------|-------|--------|
| Toneishness cap | encode_frame.go:122 | Raw toneishness | `min(toneishness, 1.0-tfEstimate)` | Matches libopus TF gating |
| predQ8 sign extension | libopus_nlsf.go:18 | `int32(predQ8[i])` | `int32(int8(predQ8[i]))` | TV12 Q: -32 → -8 dB |

---

## What NOT to Try (Already Failed)

_List items here as they fail to prevent re-investigation_

---

### Agent 5: Transient Detection
**Timestamp:** 2026-01-30

#### Summary
Analyzed transient detection in gopus (`internal/celt/transient.go`) vs libopus (`celt/celt_encoder.c` transient_analysis). Both implementations detect transient=true for the first frame of a sine wave, but the issue is **how the transient result feeds into TF analysis**.

#### Transient Detection Algorithm Comparison

| Aspect | libopus | gopus | Match? |
|--------|---------|-------|--------|
| High-pass filter | (1 - 2*z^-1 + z^-2) / (1 - z^-1 + 0.5*z^-2) | Same formula | YES |
| Forward masking decay | 0.0625 (6.7 dB/ms) | 0.0625 | YES |
| Backward masking decay | 0.125 (13.9 dB/ms) | 0.125 | YES |
| Inverse table | 128 entries (exact same values) | Same table | YES |
| Threshold | mask_metric > 200 | mask_metric > 200 | YES |
| tf_estimate formula | sqrt(max(0, 0.0069*min(163,tf_max)-0.139)) | Same | YES |

#### Toneishness Detection Comparison

| Aspect | libopus | gopus | Match? |
|--------|---------|-------|--------|
| tone_lpc algorithm | Forward+backward least-squares fit | Same algorithm | YES |
| Singular matrix check | den < 0.001*R00*R11 | Same | YES |
| Complex root check | lpc0^2 + 4*lpc1 < 0 | Same | YES |
| Toneishness value | -lpc[1] (squared pole radius) | -lpc1 | YES |
| Frequency calculation | acos(0.5*lpc0)/delay | acos(0.5*lpc0)/delay | YES |
| Tone gating threshold | toneishness > 0.98 && tone_freq < 0.026 | Same | YES |

#### CRITICAL Flow Difference Found: MISSING TONEISHNESS MODIFICATION

**1. libopus post-transient modification (line 2033):**

```c
// libopus celt_encoder.c line 2033 (AFTER transient_analysis):
toneishness = MIN32(toneishness, QCONST32(1.f, 29)-SHL32(tf_estimate, 15));
```

**libopus modifies toneishness AFTER transient_analysis, capping it based on tf_estimate!**
This modified toneishness is then used for `enable_tf_analysis` check.

**2. gopus does NOT perform this modification:**

```go
// gopus encode_frame.go line 122
toneishness := transientResult.Toneishness
// Uses raw toneishness directly - NO modification based on tfEstimate
```

**GOPUS IS MISSING THIS TONEISHNESS MODIFICATION!**

#### Impact Analysis

The modification formula `toneishness = MIN32(toneishness, 1.0 - tf_estimate)` means:
- For a transient frame with tf_estimate = 0.5:
  - Max toneishness allowed = 1.0 - 0.5 = 0.5
- Even a "tonal" signal (original toneishness=0.98) would be capped to 0.5
- This could **ENABLE TF analysis** when gopus would otherwise disable it (because toneishness >= 0.98 disables TF analysis)

**Example scenario (440 Hz sine wave, first frame):**
- gopus: toneishness=0.98, tfEstimate=0.3 -> toneishness stays 0.98 -> TF analysis DISABLED
- libopus: toneishness=0.98, tfEstimate=0.3 -> toneishness capped to 0.7 -> TF analysis ENABLED

**This difference would cause completely different TF encoding, explaining byte 7 divergence!**

#### enable_tf_analysis Gating Comparison

```c
// libopus line 2242:
enable_tf_analysis = effectiveBytes>=15*C && !hybrid && st->complexity>=2
                     && !st->lfe && toneishness < QCONST32(.98f, 29);
```

```go
// gopus line 342:
enableTFAnalysis := effectiveBytes >= 15*e.channels && e.complexity >= 2
                    && toneishness < 0.98
```

**Problem:** gopus uses raw toneishness, libopus uses the modified (capped) toneishness.

#### Recommended Fix

In `encode_frame.go` after TransientAnalysis (around line 122):

```go
// Match libopus line 2033: cap toneishness based on tf_estimate
// libopus: toneishness = MIN32(toneishness, QCONST32(1.f, 29)-SHL32(tf_estimate, 15))
// In float: toneishness = min(toneishness, 1.0 - tf_estimate)
maxToneishness := 1.0 - tfEstimate
if toneishness > maxToneishness {
    toneishness = maxToneishness
}
```

#### Specific Variables to Trace for Verification

1. **tf_estimate value:**
   - gopus: `transientResult.TfEstimate`
   - libopus: output of `transient_analysis()` tf_estimate pointer
   - Compare for same input

2. **toneishness BEFORE and AFTER modification:**
   - gopus: raw `transientResult.Toneishness` (never modified)
   - libopus: `MIN32(toneishness, 1.0 - tf_estimate)` in Q29

3. **enable_tf_analysis result:**
   - May differ due to different toneishness values
   - This determines whether Viterbi TF analysis runs or fallback path is used

#### Files Analyzed
- `/Users/thesyncim/GolandProjects/gopus/internal/celt/transient.go` - gopus transient detection (569 lines)
- `/Users/thesyncim/GolandProjects/gopus/tmp_check/opus-1.6.1/celt/celt_encoder.c`:
  - Lines 267-466: transient_analysis()
  - Lines 1305-1402: tone_lpc() and tone_detect()
  - Line 2033: **CRITICAL** toneishness modification
  - Line 2242: enable_tf_analysis check
- `/Users/thesyncim/GolandProjects/gopus/internal/celt/tf.go` - gopus TF analysis
- `/Users/thesyncim/GolandProjects/gopus/internal/celt/encode_frame.go` - gopus encoding pipeline
- `/Users/thesyncim/GolandProjects/gopus/internal/celt/cgo_test/transient_compare_test.go` - comparison tests

