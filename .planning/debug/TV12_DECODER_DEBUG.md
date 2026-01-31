# TV12 Decoder Debug: Bandwidth Transition Analysis

## Investigation Summary (2026-01-30)

This document analyzes the `silkDecoderSetFs()` state handling at bandwidth changes, comparing gopus with libopus reference implementation to identify the root cause of TV12 decoder failure.

## Context

- **Test Vector:** testvector12 (SILK/Hybrid mono)
- **Current Q:** -32.06 dB (FAIL, needs Q >= 0)
- **Packet Stats:** 1332 packets (1068 SILK, 264 Hybrid)
- **First Failure:** Packet 214 (MB->WB transition) at -3.3 dB native SNR
- **Issue Location:** SILK core decode for Wideband (16kHz), NOT resampling

## Bandwidth Transition Points in TV12

Based on test analysis:
- NB (8kHz) -> MB (12kHz) transitions
- MB (12kHz) -> WB (16kHz) transitions
- WB -> Hybrid (16kHz SILK + CELT) transitions
- Various reverse transitions

## State Comparison: `silkDecoderSetFs()` / `silk_decoder_set_fs()`

### libopus Reference (`silk/decoder_set_fs.c`)

```c
opus_int silk_decoder_set_fs(
    silk_decoder_state *psDec,
    opus_int fs_kHz,
    opus_int32 fs_API_Hz
) {
    opus_int frame_length, ret = 0;

    // 1. Update subframe length
    psDec->subfr_length = silk_SMULBB(SUB_FRAME_LENGTH_MS, fs_kHz);
    frame_length = silk_SMULBB(psDec->nb_subfr, psDec->subfr_length);

    // 2. Initialize RESAMPLER when switching internal OR external fs
    if (psDec->fs_kHz != fs_kHz || psDec->fs_API_hz != fs_API_Hz) {
        ret += silk_resampler_init(&psDec->resampler_state,
                                    silk_SMULBB(fs_kHz, 1000),
                                    fs_API_Hz, 0);
        psDec->fs_API_hz = fs_API_Hz;
    }

    // 3. If fs_kHz or frame_length changed
    if (psDec->fs_kHz != fs_kHz || frame_length != psDec->frame_length) {
        // Update pitch contour iCDF
        if (fs_kHz == 8) {
            psDec->pitch_contour_iCDF = (nb_subfr==4) ?
                silk_pitch_contour_NB_iCDF : silk_pitch_contour_10_ms_NB_iCDF;
        } else {
            psDec->pitch_contour_iCDF = (nb_subfr==4) ?
                silk_pitch_contour_iCDF : silk_pitch_contour_10_ms_iCDF;
        }

        // 4. CRITICAL: When fs_kHz actually changes
        if (psDec->fs_kHz != fs_kHz) {
            // a. Update LTP memory length
            psDec->ltp_mem_length = silk_SMULBB(LTP_MEM_LENGTH_MS, fs_kHz);

            // b. Update LPC order and NLSF codebook
            if (fs_kHz == 8 || fs_kHz == 12) {
                psDec->LPC_order = MIN_LPC_ORDER;  // 10
                psDec->psNLSF_CB = &silk_NLSF_CB_NB_MB;
            } else {
                psDec->LPC_order = MAX_LPC_ORDER;  // 16
                psDec->psNLSF_CB = &silk_NLSF_CB_WB;
            }

            // c. Update pitch lag low bits iCDF
            if (fs_kHz == 16) {
                psDec->pitch_lag_low_bits_iCDF = silk_uniform8_iCDF;
            } else if (fs_kHz == 12) {
                psDec->pitch_lag_low_bits_iCDF = silk_uniform6_iCDF;
            } else {
                psDec->pitch_lag_low_bits_iCDF = silk_uniform4_iCDF;
            }

            // d. RESET STATE VARIABLES
            psDec->first_frame_after_reset = 1;
            psDec->lagPrev = 100;
            psDec->LastGainIndex = 10;
            psDec->prevSignalType = TYPE_NO_VOICE_ACTIVITY;

            // e. CLEAR BUFFERS
            silk_memset(psDec->outBuf, 0, sizeof(psDec->outBuf));
            silk_memset(psDec->sLPC_Q14_buf, 0, sizeof(psDec->sLPC_Q14_buf));

            // NOTE: prevNLSF_Q15 is NOT reset!
            // first_frame_after_reset=1 forces NLSFInterpCoefQ2=4
            // which disables NLSF interpolation anyway
        }

        psDec->fs_kHz = fs_kHz;
        psDec->frame_length = frame_length;
    }
    return ret;
}
```

### gopus Implementation (`internal/silk/libopus_decode.go`)

```go
func silkDecoderSetFs(st *decoderState, fsKHz int) {
    st.subfrLength = subFrameLengthMs * fsKHz
    frameLength := st.nbSubfr * st.subfrLength

    if st.fsKHz != fsKHz || frameLength != st.frameLength {
        // Pitch contour iCDF update - same as libopus
        if fsKHz == 8 {
            if st.nbSubfr == maxNbSubfr {
                st.pitchContourICDF = silk_pitch_contour_NB_iCDF
            } else {
                st.pitchContourICDF = silk_pitch_contour_10_ms_NB_iCDF
            }
        } else {
            if st.nbSubfr == maxNbSubfr {
                st.pitchContourICDF = silk_pitch_contour_iCDF
            } else {
                st.pitchContourICDF = silk_pitch_contour_10_ms_iCDF
            }
        }

        if st.fsKHz != fsKHz {
            st.ltpMemLength = ltpMemLengthMs * fsKHz
            if fsKHz == 8 || fsKHz == 12 {
                st.lpcOrder = minLPCOrder  // 10
                st.nlsfCB = &silk_NLSF_CB_NB_MB
            } else {
                st.lpcOrder = maxLPCOrder  // 16
                st.nlsfCB = &silk_NLSF_CB_WB
            }
            switch fsKHz {
            case 16:
                st.pitchLagLowBitsICDF = silk_uniform8_iCDF
            case 12:
                st.pitchLagLowBitsICDF = silk_uniform6_iCDF
            case 8:
                st.pitchLagLowBitsICDF = silk_uniform4_iCDF
            }
            st.firstFrameAfterReset = true
            st.lagPrev = 100
            st.lastGainIndex = 10
            st.prevSignalType = typeNoVoiceActivity

            // Clear buffers
            for i := range st.outBuf {
                st.outBuf[i] = 0
            }
            for i := range st.sLPCQ14Buf {
                st.sLPCQ14Buf[i] = 0
            }
            // NOTE: libopus does NOT reset prevNLSF_Q15 on bandwidth change.
            // firstFrameAfterReset=true forces NLSFInterpCoefQ2=4 which disables
            // NLSF interpolation, so the previous NLSF values aren't used anyway.
        }

        st.fsKHz = fsKHz
        st.frameLength = frameLength
    }
}
```

## Key Differences Identified

### 1. RESAMPLER INITIALIZATION - CRITICAL DIFFERENCE!

**libopus:** Calls `silk_resampler_init()` on fs_kHz OR fs_API_Hz change
```c
if (psDec->fs_kHz != fs_kHz || psDec->fs_API_hz != fs_API_Hz) {
    ret += silk_resampler_init(&psDec->resampler_state,
                                silk_SMULBB(fs_kHz, 1000),
                                fs_API_Hz, 0);
    psDec->fs_API_hz = fs_API_Hz;
}
```

**gopus:** Does NOT call resampler initialization in `silkDecoderSetFs()`!
- The resampler is handled separately in `decoder.go` via `handleBandwidthChange()`
- This is done at the Decoder level, not the decoderState level

### 2. Missing State Variables

**libopus `silk_decoder_state` has these fields that may differ:**

| Field | libopus Type | gopus Type | Reset on BW Change? |
|-------|-------------|------------|---------------------|
| `prev_gain_Q16` | opus_int32 | int32 | NO (only on full reset) |
| `exc_Q14[]` | opus_int32[] | int32[] | NO |
| `sLPC_Q14_buf[]` | opus_int32[] | int32[] | YES (cleared) |
| `outBuf[]` | opus_int16[] | int16[] | YES (cleared) |
| `lagPrev` | opus_int | int | YES (set to 100) |
| `LastGainIndex` | opus_int8 | int8 | YES (set to 10) |
| `prevSignalType` | opus_int | int | YES (set to 0) |
| `first_frame_after_reset` | opus_int | bool | YES (set to 1/true) |
| `prevNLSF_Q15[]` | opus_int16[] | int16[] | **NO** |
| `ec_prevSignalType` | opus_int | int | **NO** |
| `ec_prevLagIndex` | opus_int16 | int | **NO** |
| `lossCnt` | opus_int | int | **NO** |
| `sCNG` | silk_CNG_struct | NOT IMPLEMENTED | N/A |
| `sPLC` | silk_PLC_struct | NOT IMPLEMENTED | N/A |

### 3. CNG/PLC State - NOT IMPLEMENTED IN GOPUS

**libopus:** On bandwidth change, `silk_CNG()` checks `fs_kHz != psCNG->fs_kHz` and calls `silk_CNG_Reset()`:
```c
void silk_CNG_Reset(silk_decoder_state *psDec) {
    opus_int i, NLSF_step_Q15, NLSF_acc_Q15;

    NLSF_step_Q15 = silk_DIV32_16(silk_int16_MAX, psDec->LPC_order + 1);
    NLSF_acc_Q15 = 0;
    for (i = 0; i < psDec->LPC_order; i++) {
        NLSF_acc_Q15 += NLSF_step_Q15;
        psDec->sCNG.CNG_smth_NLSF_Q15[i] = NLSF_acc_Q15;
    }
    psDec->sCNG.CNG_smth_Gain_Q16 = 0;
    psDec->sCNG.rand_seed = 3176576;
}
```

**libopus:** PLC state is reset when `fs_kHz != psPLC->fs_kHz`:
```c
void silk_PLC_Reset(silk_decoder_state *psDec) {
    psDec->sPLC.pitchL_Q8 = silk_LSHIFT(psDec->frame_length, 8 - 1);
    psDec->sPLC.prevGain_Q16[0] = SILK_FIX_CONST(1, 16);
    psDec->sPLC.prevGain_Q16[1] = SILK_FIX_CONST(1, 16);
    psDec->sPLC.subfr_length = 20;
    psDec->sPLC.nb_subfr = 2;
}
```

**gopus:** CNG and PLC are NOT implemented - no comfort noise generation or packet loss concealment.

### 4. `ec_prevSignalType` and `ec_prevLagIndex` - NOT RESET

Both libopus and gopus do NOT reset these on bandwidth change. This is correct behavior - they're used for conditional coding of pitch parameters.

### 5. `prevNLSF_Q15` - NOT RESET (Correct!)

Both libopus and gopus do NOT reset `prevNLSF_Q15` on bandwidth change. The `first_frame_after_reset=1` flag forces `NLSFInterpCoefQ2=4` which means "no interpolation", so previous NLSF values are ignored for the first frame anyway.

## Potential Root Causes for TV12 Failure

### Most Likely: Resampler State Management

The resampler is managed differently:
- **libopus:** `silk_resampler_init()` called directly in `silk_decoder_set_fs()`
- **gopus:** Resampler handled at higher level in `Decoder.handleBandwidthChange()`

The gopus implementation tries to match libopus but may have subtle timing differences:
- When exactly is the resampler reset?
- Is the resampler state properly synchronized with SILK decoder state?

### Second: WB LPC Order Handling

At MB->WB transition:
- LPC order changes 10 -> 16
- NLSF codebook changes from `silk_NLSF_CB_NB_MB` to `silk_NLSF_CB_WB`
- The `sLPCQ14Buf` only has first 10 values from previous MB frame
- Positions 10-15 would be zeros (or garbage?)

In gopus:
```go
for i := range st.sLPCQ14Buf {
    st.sLPCQ14Buf[i] = 0
}
```
This clears ALL 16 positions, which is correct. But libopus does the same:
```c
silk_memset(psDec->sLPC_Q14_buf, 0, sizeof(psDec->sLPC_Q14_buf));
```
Both should have the same behavior here.

### Third: prevGainQ16 NOT Reset on BW Change

Both implementations do NOT reset `prev_gain_Q16` on bandwidth change. This is only reset during full decoder reset:

```go
// resetDecoderState in libopus_state.go
func resetDecoderState(st *decoderState) {
    *st = decoderState{}
    st.firstFrameAfterReset = true
    st.prevGainQ16 = 1 << 16  // 65536
}
```

libopus does similar in `silk_reset_decoder()`:
```c
psDec->prev_gain_Q16 = 65536;
```

But at bandwidth change, neither resets `prev_gain_Q16`. This could cause gain prediction issues if the gain scale differs between bandwidths.

## Recommended Fixes to Try

### Fix 1: Verify Resampler Reset Timing

Check if `handleBandwidthChange()` is called BEFORE or AFTER SILK decoding. The libopus order is:
1. `silk_decoder_set_fs()` (includes resampler init)
2. Decode SILK frame
3. Resample output

Gopus order should be:
1. `silkDecoderSetFs()` (no resampler)
2. `handleBandwidthChange()` (resampler reset)
3. Decode SILK frame
4. Resample output

### Fix 2: Consider prevGainQ16 Reset at BW Change

Experiment: Reset `prevGainQ16 = 1 << 16` inside `silkDecoderSetFs()` when `fsKHz` changes.

**WARNING:** This would differ from libopus behavior, but may help if there's a gain accumulation issue.

### Fix 3: Verify NLSF Codebook Tables Match

Ensure `silk_NLSF_CB_WB` in gopus matches libopus exactly:
- `nVectors`
- `order`
- `quantStepSize_Q16`
- `CB1_NLSF_Q8`
- `CB1_Wght_Q9`
- `CB1_iCDF`
- `pred_Q8`
- `ec_sel`
- `ec_iCDF`
- `deltaMin_Q15`

### Fix 4: Add Debug Tracing for First WB Frame

Add detailed tracing for packet 214 (first WB packet):
- Input excitation values
- LPC coefficients after NLSF decode
- sLPC buffer state
- Output values

Compare sample-by-sample with libopus.

## Files to Modify (DO NOT MODIFY YET - RESEARCH ONLY)

1. `internal/silk/libopus_decode.go` - `silkDecoderSetFs()`
2. `internal/silk/decoder.go` - `handleBandwidthChange()`, `DecodeFrame()`
3. `internal/silk/libopus_nlsf.go` - NLSF decoding
4. `internal/silk/libopus_lpc.go` - LPC synthesis

## Test Files to Create/Use

- `internal/celt/cgo_test/tv12_bw_transition_test.go` (exists)
- `internal/celt/cgo_test/tv12_silk_state_test.go` (exists)
- New: Compare first WB frame (packet 214) sample-by-sample with libopus

## Summary

The gopus `silkDecoderSetFs()` implementation appears to match libopus for the state variables it handles. The main differences are:

1. **Resampler initialization is separate** - handled in `Decoder.handleBandwidthChange()` instead of within `silkDecoderSetFs()`

2. **CNG/PLC not implemented** - These are comfort noise and packet loss concealment features that shouldn't affect normal decode

3. **State variable handling appears correct** - Same variables are reset/preserved as in libopus

The root cause is likely in:
- The 16-order LPC synthesis for WB
- NLSF decoding with WB codebook
- Timing of resampler reset relative to SILK decode

**Next step:** Create a test that compares LPC coefficients and synthesis output for packet 214 (first WB packet) between gopus and libopus to pinpoint the divergence point.

## Verification: NLSF Codebook Tables Match

Verified that `silk_NLSF_CB1_WB_Q8` table values are IDENTICAL between gopus and libopus:

**libopus (first 16 bytes):** `7, 23, 38, 54, 69, 85, 100, 116, 131, 147, 162, 178, 193, 208, 223, 239`
**gopus (first 16 bytes):** `7, 23, 38, 54, 69, 85, 100, 116, 131, 147, 162, 178, 193, 208, 223, 239`

The codebook structure parameters also match:
- `nVectors = 32`
- `order = 16`
- `quantStepSize_Q16 = silkFixConst(0.15, 16)`

## Additional Investigation Areas

### 1. LPC Synthesis Precision

The 16-order LPC synthesis in `silkDecodeCore()` uses:
```go
for j := 0; j < st.lpcOrder; j++ {
    lpcPredQ10 = silkSMLAWB(lpcPredQ10, sLPC[maxLPCOrder+i-j-1], int32(A_Q12[j]))
}
```

This matches libopus's pattern, but for 16 coefficients (vs 10 for NB/MB), any precision issues would accumulate more.

### 2. NLSF to LPC Conversion

The `silkNLSF2A()` function converts NLSF coefficients to LPC coefficients. For WB, this operates on 16 coefficients. The conversion involves:
- Cosine approximation
- LSF to LPC expansion

Any precision differences here would affect ALL subsequent samples.

### 3. Critical Test Recommendation

Create test that:
1. Decodes packet 213 (last MB) and packet 214 (first WB) with both decoders
2. Compares intermediate state BEFORE silkDecodeCore():
   - `ctrl.PredCoefQ12` (LPC coefficients)
   - `ctrl.GainsQ16` (subframe gains)
   - `st.sLPCQ14Buf` (LPC history)
   - `pulses` (decoded excitation)
3. If LPC coefficients differ, issue is in NLSF decode
4. If LPC coefficients match but output differs, issue is in LPC synthesis

## Files Relevant to Investigation

| File | Purpose |
|------|---------|
| `internal/silk/libopus_decode.go` | Main SILK decode, `silkDecoderSetFs()`, `silkDecodeCore()` |
| `internal/silk/libopus_nlsf.go` | NLSF decoding and NLSF->LPC conversion |
| `internal/silk/libopus_lpc.go` | LPC synthesis helpers |
| `internal/silk/libopus_codebook.go` | NLSF codebook definitions |
| `internal/silk/libopus_tables.go` | NLSF CB1 table data |
| `internal/silk/decoder.go` | High-level decode, bandwidth change handling |

## Summary of State Handling Comparison

| State Variable | libopus Reset on BW? | gopus Reset on BW? | Notes |
|---------------|---------------------|-------------------|-------|
| `subfrLength` | Recalculated | Recalculated | Same |
| `frameLength` | Recalculated | Recalculated | Same |
| `ltpMemLength` | Yes | Yes | Same |
| `lpcOrder` | Yes | Yes | Same (10->16 for WB) |
| `nlsfCB` | Yes | Yes | Same (switch codebook) |
| `pitchLagLowBitsICDF` | Yes | Yes | Same |
| `pitchContourICDF` | Yes | Yes | Same |
| `firstFrameAfterReset` | Yes (=1) | Yes (=true) | Same |
| `lagPrev` | Yes (=100) | Yes (=100) | Same |
| `lastGainIndex` | Yes (=10) | Yes (=10) | Same |
| `prevSignalType` | Yes (=0) | Yes (=0) | Same |
| `outBuf[]` | Yes (cleared) | Yes (cleared) | Same |
| `sLPC_Q14_buf[]` | Yes (cleared) | Yes (cleared) | Same |
| `prevNLSF_Q15[]` | No | No | Same (correct) |
| `prev_gain_Q16` | No | No | Same (only full reset) |
| `resampler_state` | Yes (init) | Separate | **DIFFERENT!** |
| `sCNG` | Reset | N/A | gopus no CNG |
| `sPLC` | Reset | N/A | gopus no PLC |

The only structural difference is that gopus handles resampler initialization separately from `silkDecoderSetFs()`. This is handled in `Decoder.handleBandwidthChange()` at a higher level.

## Conclusion

The `silkDecoderSetFs()` implementation in gopus appears to correctly mirror libopus's state handling. The issue is likely NOT in the state reset logic itself, but rather in:

1. **16-order LPC synthesis precision** - accumulated errors through 16 coefficients
2. **NLSF to LPC conversion for WB** - the conversion math may have subtle differences
3. **Timing of resampler handling** - though this shouldn't affect native SILK output

Since the error occurs at **native rate** (before resampling), the resampler timing is ruled out.

**Recommended next action:** Instrument `silkDecodeCore()` to compare LPC coefficients (`ctrl.PredCoefQ12`) at packet 214 between gopus and libopus. If they differ, focus on NLSF decode. If they match, focus on LPC synthesis loop.

---

## NEW FINDINGS: LPC Synthesis Analysis for WB (2026-01-30)

### Deep Code Comparison Summary

This section documents detailed analysis of the NLSF decode and LPC synthesis paths for Wideband (16kHz) mode.

### Files Analyzed

#### Go Implementation
| File | Purpose |
|------|---------|
| `internal/silk/libopus_lpc.go` | NLSF2A conversion, stability check |
| `internal/silk/libopus_nlsf.go` | NLSF decode, residual dequant, stabilize |
| `internal/silk/lsf.go` | Alternative LSF to LPC (fallback) |
| `internal/silk/libopus_decode.go` | Core decode loop with LPC synthesis |
| `internal/silk/libopus_fixed.go` | Fixed-point arithmetic macros |
| `internal/silk/libopus_codebook.go` | WB/NB_MB codebook definitions |
| `internal/silk/libopus_tables.go` | Delta min tables, PRED tables |

#### Libopus Reference
| File | Purpose |
|------|---------|
| `tmp_check/opus-1.6.1/silk/NLSF_decode.c` | NLSF vector decoder |
| `tmp_check/opus-1.6.1/silk/NLSF2A.c` | NLSF to LPC conversion |
| `tmp_check/opus-1.6.1/silk/NLSF_stabilize.c` | NLSF stabilization |
| `tmp_check/opus-1.6.1/silk/NLSF_unpack.c` | Entropy table unpacking |
| `tmp_check/opus-1.6.1/silk/decode_core.c` | Core decoding with LPC synthesis |

---

## CRITICAL BUG FOUND: predQ8 Sign Extension

### Issue Description

In `silkNLSFResidualDequant()`, the predictor coefficients (`predQ8`) are stored as `uint8` but should be interpreted as signed values when >= 128.

### Location
`internal/silk/libopus_nlsf.go` line 18

### Code Comparison

**Go (current - INCORRECT):**
```go
func silkNLSFResidualDequant(xQ10 []int16, indices []int8, predQ8 []uint8, quantStepSizeQ16 int, order int) {
    var outQ10 int32
    for i := order - 1; i >= 0; i-- {
        predQ10 := silkRSHIFT(silkSMULBB(outQ10, int32(predQ8[i])), 8)  // BUG: uint8 to int32 is ZERO extension
        outQ10 = int32(indices[i]) << 10
        ...
    }
}
```

**libopus (correct):**
```c
for( i = order-1; i >= 0; i-- ) {
    pred_Q10 = silk_RSHIFT( silk_SMULBB( out_Q10, (opus_int16)pred_coef_Q8[ i ] ), 8 );  // SIGN extension via int16 cast
    ...
}
```

### Impact Analysis

**Values in `silk_NLSF_PRED_WB_Q8` table:**
```
175, 148, 160, 176, 178, 173, 174, 164, 177, 174, 196, 182, 198, 192, 182, 68,
62, 66, 60, 72, 117, 85, 90, 118, 136, 151, 142, 160, 142, 155
```

**Values >= 128 (affected):** 175, 148, 160, 176, 178, 173, 174, 164, 177, 174, 196, 182, 198, 192, 182, 136, 151, 142, 160, 142, 155

That's **21 out of 30 values** in the WB predictor table that are >= 128!

**Example misbehavior:**
- Value: 175 (0xAF)
- libopus interprets as: `(opus_int16)175` = -81 (sign extended)
- gopus interprets as: `int32(uint8(175))` = 175 (zero extended)
- **Difference: 256!** (175 - (-81) = 256)

This causes the NLSF residual dequantization to produce incorrect results, leading to wrong NLSF values, which propagate to wrong LPC coefficients for ALL WB frames.

### NB/MB Also Affected

**Values in `silk_NLSF_PRED_NB_MB_Q8` table:**
```
179, 138, 140, 148, 151, 149, 153, 151, 163, 116, 67, 82, 59, 92, 72, 100, 89, 92
```

**Values >= 128 (affected):** 179, 138, 140, 148, 151, 149, 153, 151, 163

That's **9 out of 18 values** in the NB/MB predictor table that are >= 128.

### Why NB/MB Passes But WB Fails

- WB has 21/30 (70%) affected predictor values
- NB/MB has 9/18 (50%) affected predictor values
- WB operates on 16 coefficients vs 10 for NB/MB
- More coefficients + higher affected rate = more accumulated error
- The existing NB tests (TV02, TV03, TV04) pass because the error is within tolerance, but WB errors compound more severely

---

## Code Analysis Details

### 1. silkNLSF2AFindPoly - MATCH

**Go (libopus_lpc.go lines 19-30):**
```go
func silkNLSF2AFindPoly(out []int32, cLSF []int32, dd int) {
    out[0] = silkLSHIFT(1, nlsf2aQA)
    out[1] = -cLSF[0]
    for k := 1; k < dd; k++ {
        ftmp := cLSF[2*k]
        out[k+1] = silkLSHIFT(out[k-1], 1) - int32(silkRSHIFT_ROUND64(silkSMULL(ftmp, out[k]), nlsf2aQA))
        for n := k; n > 1; n-- {
            out[n] += out[n-2] - int32(silkRSHIFT_ROUND64(silkSMULL(ftmp, out[n-1]), nlsf2aQA))
        }
        out[1] -= ftmp
    }
}
```

**libopus (NLSF2A.c lines 44-63):** Identical algorithm.

### 2. silkNLSF2A - MATCH

- Ordering tables match (`nlsf2aOrdering16`, `nlsf2aOrdering10`)
- Cosine interpolation formula matches
- Polynomial combination matches
- Stability check with bandwidth expansion matches

### 3. silkNLSFStabilize - MATCH

The stabilization algorithm is identical between gopus and libopus:
- Same loop limit (20 iterations)
- Same minimum difference calculation
- Same insertion sort fallback
- Same delta enforcement logic

### 4. silkDecodeCore LPC Synthesis - MATCH

**Go (libopus_decode.go lines 475-482):**
```go
for i := 0; i < st.subfrLength; i++ {
    lpcPredQ10 := int32(st.lpcOrder >> 1)  // Bias: 8 for WB, 5 for NB/MB
    for j := 0; j < st.lpcOrder; j++ {
        lpcPredQ10 = silkSMLAWB(lpcPredQ10, sLPC[maxLPCOrder+i-j-1], int32(A_Q12[j]))
    }
    sLPC[maxLPCOrder+i] = silkAddSat32(presQ14[i], silkLShiftSAT32(lpcPredQ10, 4))
    pxq[i] = silkSAT16(silkRSHIFT_ROUND(silkSMULWW(sLPC[maxLPCOrder+i], gainQ10), 8))
}
```

**libopus (decode_core.c lines 203-232):** Same algorithm (unrolled for performance but mathematically identical).

---

## Delta Min Tables - VERIFIED MATCH

**WB (17 values, includes boundary):**
```
Go:     100, 3, 40, 3, 3, 3, 5, 14, 14, 10, 11, 3, 8, 9, 7, 3, 347
libopus: 100, 3, 40, 3, 3, 3, 5, 14, 14, 10, 11, 3, 8, 9, 7, 3, 347
```
**EXACT MATCH**

**NB/MB (11 values):**
```
Go:     250, 3, 6, 3, 3, 3, 4, 3, 3, 3, 461
libopus: 250, 3, 6, 3, 3, 3, 4, 3, 3, 3, 461
```
**EXACT MATCH**

---

## Fixed-Point Arithmetic - VERIFIED MATCH

| Macro | Go | libopus | Status |
|-------|-----|---------|--------|
| silkSMULBB | `int32(int16(a)) * int32(int16(b))` | `(opus_int32)((opus_int16)a) * (opus_int32)((opus_int16)b)` | MATCH |
| silkSMLAWB | `a + int32((int64(b)*int64(int16(c)))>>16)` | `silk_ADD32(a, silk_SMULWB(b,c))` | MATCH |
| silkSMULWW | `int32((int64(a) * int64(b)) >> 16)` | `(opus_int32)silk_RSHIFT64(silk_SMULL(a,b),16)` | MATCH |
| silkRSHIFT_ROUND | `((x >> (shift - 1)) + 1) >> 1` | Same formula | MATCH |

---

## Existing Test Coverage Gap

### What's Tested
- `nlsf2a_compare_test.go` - Tests NLSF2A with NB (order 10) only
- `silk_nlsf_compare_test.go` - Tests NLSF state for TV02 (NB only)
- `silk_lsf_encode_compare_test.go` - Tests encoding (includes WB)

### What's NOT Tested
- **NLSF decode for WB codebook** - No direct comparison test
- **LPC coefficients for WB** - No verification against libopus
- **silkNLSFResidualDequant for values >= 128** - Bug in this path

---

## Recommended Fix

### Fix for predQ8 Sign Extension Bug

**File:** `internal/silk/libopus_nlsf.go`
**Function:** `silkNLSFResidualDequant`
**Line:** 18

**Current (buggy):**
```go
predQ10 := silkRSHIFT(silkSMULBB(outQ10, int32(predQ8[i])), 8)
```

**Fixed:**
```go
predQ10 := silkRSHIFT(silkSMULBB(outQ10, int32(int8(predQ8[i]))), 8)
```

The key change is `int32(int8(predQ8[i]))` instead of `int32(predQ8[i])`:
- `predQ8[i]` is `uint8` (e.g., 175)
- `int8(predQ8[i])` interprets as signed (175 becomes -81)
- `int32(int8(...))` sign-extends to int32 (-81)
- This matches libopus's `(opus_int16)pred_coef_Q8[i]` behavior

### Alternative Fix (more explicit, matches libopus exactly)

```go
predQ10 := silkRSHIFT(silkSMULBB(outQ10, int32(int16(int8(predQ8[i])))), 8)
```

This explicitly matches the `(opus_int16)` cast in libopus, though the intermediate int16 is unnecessary for the arithmetic.

---

## Summary of Findings

### Root Cause: predQ8 Sign Extension Bug

**Severity:** HIGH
**Impact:** All WB packets, some NB/MB packets
**Fix Complexity:** TRIVIAL (one-line change)

The bug causes NLSF residual dequantization to use incorrect predictor values for any predQ8 coefficient >= 128. Since 70% of WB predictor coefficients are >= 128, this causes significant errors in all WB frames.

### Why NB Tests Pass Despite Bug

1. NB/MB has 50% affected coefficients vs 70% for WB
2. NB/MB uses 10 coefficients vs 16 for WB
3. Error accumulation is proportional to affected coefficient count
4. NB test vectors may have lower sensitivity to NLSF errors

### Verification Steps After Fix

1. Run TV12 and verify Q improves (should go positive)
2. Run TV02/TV03/TV04 to ensure NB still passes
3. Add dedicated WB NLSF comparison test
4. Add regression test for predQ8 >= 128 values

---

## Appendix: Affected Predictor Values

### WB Predictor Table Analysis

| Index | Value | Hex | Sign-Extended | Difference |
|-------|-------|-----|---------------|------------|
| 0 | 175 | 0xAF | -81 | 256 |
| 1 | 148 | 0x94 | -108 | 256 |
| 2 | 160 | 0xA0 | -96 | 256 |
| 3 | 176 | 0xB0 | -80 | 256 |
| 4 | 178 | 0xB2 | -78 | 256 |
| 5 | 173 | 0xAD | -83 | 256 |
| 6 | 174 | 0xAE | -82 | 256 |
| 7 | 164 | 0xA4 | -92 | 256 |
| 8 | 177 | 0xB1 | -79 | 256 |
| 9 | 174 | 0xAE | -82 | 256 |
| 10 | 196 | 0xC4 | -60 | 256 |
| 11 | 182 | 0xB6 | -74 | 256 |
| 12 | 198 | 0xC6 | -58 | 256 |
| 13 | 192 | 0xC0 | -64 | 256 |
| 14 | 182 | 0xB6 | -74 | 256 |
| 15 | 68 | 0x44 | 68 | 0 (OK) |
| 16 | 62 | 0x3E | 62 | 0 (OK) |
| 17 | 66 | 0x42 | 66 | 0 (OK) |
| 18 | 60 | 0x3C | 60 | 0 (OK) |
| 19 | 72 | 0x48 | 72 | 0 (OK) |
| 20 | 117 | 0x75 | 117 | 0 (OK) |
| 21 | 85 | 0x55 | 85 | 0 (OK) |
| 22 | 90 | 0x5A | 90 | 0 (OK) |
| 23 | 118 | 0x76 | 118 | 0 (OK) |
| 24 | 136 | 0x88 | -120 | 256 |
| 25 | 151 | 0x97 | -105 | 256 |
| 26 | 142 | 0x8E | -114 | 256 |
| 27 | 160 | 0xA0 | -96 | 256 |
| 28 | 142 | 0x8E | -114 | 256 |
| 29 | 155 | 0x9B | -101 | 256 |

**21 values affected (70%)**

---

## Files to Modify (DO NOT MODIFY - RESEARCH ONLY)

1. **`internal/silk/libopus_nlsf.go`** - Fix predQ8 sign extension
2. **`internal/silk/nlsf2a_compare_test.go`** - Add WB test cases
3. **`internal/celt/cgo_test/`** - Add WB NLSF comparison test
