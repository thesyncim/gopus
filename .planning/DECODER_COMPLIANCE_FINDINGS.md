# Decoder Compliance Investigation Findings

## Status: 7/12 test vectors passing, need Q >= 0 (SNR >= 48 dB)

### Current Results (2026-01-30):
| Vector | Mode | Q Value | Status |
|--------|------|---------|--------|
| tv01 | SILK mono | 115.00 | ✅ PASS |
| tv02 | Hybrid mono | 1.57 | ✅ PASS |
| tv03 | CELT mono | 44.19 | ✅ PASS |
| tv04 | SILK stereo | 28.56 | ✅ PASS |
| tv05 | Hybrid stereo | 3.50 | ✅ PASS |
| tv06 | Hybrid FB stereo | -3.48 | ❌ FAIL (close!) |
| tv07 | CELT multisize | -40.02 | ❌ FAIL |
| tv08 | Mixed stereo | -92.46 | ❌ FAIL |
| tv09 | CELT stereo | -84.64 | ❌ FAIL |
| tv10 | Mixed stereo | 27.56 | ✅ PASS |
| tv11 | SILK transitions | 116.92 | ✅ PASS |
| tv12 | Complex transitions | -32.07 | ❌ FAIL |

## ⚠️ DO NOT INVESTIGATE AGAIN (Quick Reference)

### Already Verified Correct:
- Float32 vs Float64 drift - NOT the issue
- De-emphasis filter formula - identical to libopus
- PreemphCoef value (0.85000610) - identical
- IMDCT algorithm - achieves 140 dB SNR
- Window coefficients formula - matches libopus
- Short block coefficient extraction - mathematically equivalent
- Postfilter/comb filter - correct (136+ dB SNR)
- IMDCT twiddle formula `cos(2*π*(i+0.125)/n)` - correct
- SILK stereo formulas (silkStereoMSToLR) - mathematically identical
- SILK fixed-point macros (silkSMLAWB, silkSMULBB, etc.) - all match
- SILK stereo tables and constants - all match

### Already Tried and FAILED:
- **Hybrid 60-sample delay** → Made testvector05 regress from PASS to Q=-107
- **SILK stereo predPrevQ13 int16** → Values fit in range, no effect
- **Direct twiddle computation** → No improvement, 3x slower
- **Alternative IMDCT formula** → Made things MUCH worse

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

### 9. IMDCT Trig Table Formula
- **Formula:** `cos(2*π*(i+0.125)/n)` - verified against libopus
- **Test:** Changing to `cos(π*(i+0.125)/n)` made results MUCH worse
- **Conclusion:** Current formula is correct

### 10. Short Block Coefficient Indices
- **Test:** `TestLibopusShortBlockCoeffOrder` shows index sets match
- **libopus:** xp1 reads [0,16,32...944], xp2 reads [952,936...8]
- **gopus:** reads [0,8,16...952] - same set of indices
- **Conclusion:** Coefficient extraction is correct

## PROVEN TO BE THE ISSUE

### Transient Frame Short Block Synthesis
- **Packet 60 (normal):** state_err=3.5e-5, SNR=138 dB
- **Packet 61 (transient):** state_err=2.2e-2, SNR=79 dB
- **Packet 62 (after transient):** state_err=2.4e-1, SNR=36 dB
- **Error jump:** 630x increase at transient frame

### Block-by-Block Error Pattern in Frame 61
```
Block 0 [  0-119]: max=7.5e-08, SNR=102.1 dB (excellent)
Block 1 [120-239]: max=4.4e-07, SNR=88.7 dB
Block 2 [240-359]: max=6.4e-07, SNR=87.1 dB
Block 3 [360-479]: max=3.6e-07, SNR=90.3 dB
Block 4 [480-599]: max=1.9e-07, SNR=93.2 dB
Block 5 [600-719]: max=1.8e-06, SNR=75.7 dB ← error starts growing
Block 6 [720-839]: max=2.2e-06, SNR=69.4 dB
Block 7 [840-959]: max=1.9e-06, SNR=61.1 dB ← worst
```

### Impact on Following Frame
- Frame 62 first 120 samples: max=1.4e-4, SNR=23.8 dB (TERRIBLE!)
- The de-emphasis state error (2.2e-2) causes output errors of similar magnitude
- This cascades to subsequent frames until state stabilizes

### Overlap Buffer Issue (Partial)
- New overlap after transient: first 60 samples valid, last 60 are ZEROS
- This is because short IMDCT only writes to [900:1020], not [1020:1080]
- However, zeros should be overwritten by next frame's IMDCT before TDAC
- The main issue is the state error accumulation, not the zeros

## ROOT CAUSE ANALYSIS

The error accumulates through the short-block overlap-add process:
1. Each short block's TDAC reads from previous block's tail
2. Small numerical differences compound through 8 blocks
3. Blocks 5-7 show increasing error (75→69→61 dB)
4. Final de-emphasis state error (2.2e-2) propagates to next frame

## SUSPECTED DIFFERENCES FROM LIBOPUS

1. **Buffer persistence:** libopus uses decode_mem which persists across frames
   - Positions beyond IMDCT write range contain OLD data
   - gopus initializes buffer with zeros

2. **Overlap buffer content:** After transient frame:
   - gopus: [960:1020]=valid, [1020:1080]=zeros
   - libopus: [960:1020]=valid, [1020:1080]=old_frame_data

3. **TDAC numerical precision:** May have slight differences in windowing math

## NEXT STEPS

1. **[DONE]** Traced IMDCT output values - errors grow within short blocks, not at boundaries
2. **[DONE]** Verified overlap buffer persistence - zeros in [60:120] get overwritten by next IMDCT
3. **[DONE]** Compared TDAC windowing - math matches libopus

## ADDITIONAL FINDINGS FROM INVESTIGATION

### Coefficient Extraction
- Verified coefficient indices match libopus exactly
- xp1/xp2 access pattern produces same index sets
- Test: `TestLibopusShortBlockCoeffOrder` - PASS

### De-emphasis Implementation
- gopus matches libopus formula: `tmp = x + state; state = coef * tmp`
- Only difference: gopus omits VERY_SMALL (1e-30) which is negligible
- State error compounds through frame, causing ~2.2e-2 error by frame end

### Error Growth Pattern
- Error grows WITHIN short blocks, not just at boundaries
- Block 0 start: diff ~1e-9
- Block 5 end: diff ~1e-6 (1000x growth)
- Frame 62 start: diff ~1e-4 (100x jump from previous frame)

### Possible Root Causes (Not Yet Fixed)
1. **FFT numerical stability** - gopus uses generic DFT, may have different error characteristics
2. **Twiddle factor precision** - Computing trig values at runtime vs pre-computed tables
3. **Overlap buffer initialization** - Last 60 samples are zeros vs libopus's residual data
4. **State accumulation** - De-emphasis state compounds small synthesis errors

## CURRENT STATUS (Updated Jan 29, 2026)

### Passing Tests (7/12)
- testvector01: CELT stereo (Q=115.00)
- testvector02: SILK mono (Q=1.57)
- testvector03: SILK mono (Q=44.19)
- testvector04: SILK mono (Q=28.56)
- testvector05: Hybrid mono (Q=3.50)
- testvector10: Mixed mode stereo (Q=27.56) - NOW PASSING!
- testvector11: CELT stereo (Q=116.92)

### Failing Tests (5/12)
- **testvector06:** Hybrid mono, Q=-3.48 (only ~1.7 dB short!)
- **testvector07:** CELT mono with 2.5ms frames, Q=-40.02
- **testvector08:** CELT/SILK stereo, Q=-92.46
- **testvector09:** CELT/SILK stereo, Q=-84.64
- **testvector12:** SILK/Hybrid mono, Q=-32.07

### Key Observations
1. All SILK mono tests pass perfectly (02, 03, 04)
2. CELT stereo passes when pure CELT (01, 11) but fails in mixed modes (08, 09)
3. testvector08 contains mode transitions: SILK→CELT at packet 5
4. R channel errors appear specifically at packet 14 (CELT), not in SILK packets
5. Error pattern diff_M = -diff_S proves issue is in SIDE channel stereo prediction
6. testvector10 improved from Q=-25.26 to Q=27.56 with recent fixes

### Fixes Applied This Session
1. ✅ Transition audio uses `d.prevMode` (decoder_opus_frame.go:321)
2. ✅ CELT channel transitions copy energy arrays (internal/celt/decoder.go)
3. ✅ TF analysis enabled for LM=0 (internal/celt/tf.go, encode_frame.go)

## FIXES APPLIED (Jan 2026)

### 1. Transition Audio Mode Fix ✅
- **File:** `decoder_opus_frame.go` line 321
- **Issue:** ModeSILK case was using current `mode`/`bandwidth` instead of `d.prevMode`/`d.lastBandwidth`
- **Fix:** Changed to use `d.prevMode` and `d.lastBandwidth` for transition audio generation
- **Impact:** Improves mode transition handling (testvector12)

### 2. CELT Channel Transition Energy Copy ✅
- **File:** `internal/celt/decoder.go` - `handleChannelTransition()`
- **Issue:** Only copied overlap buffer on mono→stereo, not energy arrays
- **Fix:** Now copies `prevEnergy`, `prevEnergy2`, `prevLogE`, `prevLogE2`, `preemphState` from L to R
- **Also:** Stereo→mono now takes max of L/R for energy arrays (matches libopus)
- **Impact:** Improves testvector10/12 channel transitions

### 3. TF Analysis for LM=0 ✅
- **Files:** `internal/celt/tf.go`, `internal/celt/encode_frame.go`
- **Issue:** Early return for LM=0 in TFAnalysis, spurious `lm > 0` check in encoder
- **Fix:** Removed early return, removed `lm > 0` check from `enableTFAnalysis`
- **Rationale:** libopus runs tf_analysis for ALL LM values, including LM=0
- **Impact:** Should improve testvector07 (2.5ms frames)

## WHAT WAS TRIED AND DIDN'T WORK

### 1. Direct Twiddle Computation
- Changed DFT to compute each twiddle factor directly instead of iterative multiplication
- Result: No improvement, tests ran 3x slower
- Reverted the change

### 2. Alternative IMDCT Twiddle Formula
- Tried changing `2*π*(i+0.125)/n` to `0.5*π*(i+0.125)/n2`
- Result: Made things MUCH worse (Q=-106 on testvector01 which was passing)
- Reverted the change

### 3. Verified Twiddle Values Match Libopus
- Our formula produces values within 1e-9 of libopus pre-computed tables
- Confirmed: `cos(2*π*(i+0.125)/N)` is correct

### 4. Hybrid 60-Sample SILK-CELT Delay ❌
- **File:** `internal/hybrid/decoder.go`
- **Hypothesis:** SILK output needs 60-sample delay to align with CELT look-ahead
- **Implementation:** Used existing `applyDelayMono()`/`applyDelayStereo()` functions
- **Result:** Made things MUCH WORSE!
  - testvector05: Was PASS (Q=3.45), became FAIL (Q=-107.27)
  - testvector06: Was Q=-3.48, became Q=-108.10
  - testvector10: Was Q=-25.26, became Q=-94.94
  - testvector12: Was Q=-33.91, became Q=-92.80
- **Conclusion:** The delay compensation is NOT needed; libopus handles this internally
- **DO NOT try again**

### 5. SILK Stereo predPrevQ13 int16 Type ❌
- **File:** `internal/silk/libopus_types.go`, `internal/silk/libopus_stereo.go`
- **Hypothesis:** libopus uses `opus_int16 pred_prev_Q13[2]`, gopus uses `int32`
- **Implementation:** Changed to `int16` with proper int32↔int16 casting
- **Result:** Did NOT help - predictor values fit in int16 range (-26726 to 26726)
- **Verification:** Traced actual values at packet 14 transition:
  - pred0: 5892 → 0 (delta = -5892)
  - pred1: -2737 → 5450 (delta = 8187)
  - All values fit in int16, so truncation is not the issue
- **Reverted:** The change didn't affect results
- **DO NOT try again**

## REMAINING POTENTIAL FIXES

### High Priority (testvector06 almost passing)
1. Check SILK-CELT summation in hybrid mode for any gain issues
2. Verify CELT start band (17) handling matches libopus exactly
3. Check if SILK resampler delay is properly compensated

### Medium Priority (SILK stereo bugs)
1. testvector08/09 both have identical Q=-84.64 → consistent stereo bug
2. SILK mono works perfectly (testvector02-04) but stereo fails
3. Investigate `silkStereoMSToLR` and stereo prediction handling

### Lower Priority (complex transient issues)
1. testvector07 transient frame handling - error compounds through 8 short blocks
2. DFT precision for non-power-of-2 sizes (60-element FFT)
3. Short block overlap buffer initialization differences

## DETAILED ANALYSIS BY TEST VECTOR

### testvector06 (Hybrid FB stereo, Q=-3.48)
- **Issue**: Quality drops concentrated in packets 1497-1502, not uniformly distributed
- **Pattern**: Quality is good (Q>10) for packets 0-1250, drops at 1250-1700, recovers at 1700+
- **Only 1.7 dB from passing!**

#### Detailed Investigation (Jan 2026)

**Stream Structure:**
- Packets 0-938: MONO (stereo=false), config 14/15 (10ms/20ms Hybrid FB)
- Packet 939: MONO→STEREO transition (stereo=true starts)
- Packet 1252: Frame size transition 20ms→10ms (config 15→14) while stereo
- Packets 1497-1502: Quality drops sharply (Q=-65 at worst)
- Packets 1700+: Quality recovers to Q>0

**Key Findings:**
1. **Packets 1497-1502 have 90% amplitude**: RMS ratio of decoded/reference is 0.90-0.95 (should be 1.0)
2. **L/R errors are highly correlated (0.99)**: Both channels have the SAME error, not opposite sign
3. **Error is a GAIN issue**: The decoded signal is systematically too quiet, not distorted
4. **Fresh decoder is WORSE**: Starting fresh at packet 1490 gives Q=-89 vs Q=-50 continuous
5. **Quality recovers**: The error doesn't accumulate forever, it dissipates after ~200 packets

**What This Means:**
- The error is NOT in stereo prediction (would cause opposite sign L/R errors)
- The error is in shared processing: SILK MID channel or CELT energy/gain path
- The accumulated state from continuous decoding HELPS, so this isn't drift
- Something about stereo 10ms Hybrid FB specifically causes ~10% gain reduction

**Verified CORRECT:**
1. CELT start band (17) handling - correctly skips bands 0-16 for CELT in Hybrid
2. SILK/CELT output summation - `output[i] = silkSample + celtSample` is correct
3. Resampler architecture - per-channel resamplers matching libopus
4. Energy state management - multi-band arrays handled correctly
5. eMeans table values match libopus exactly

**TRIED AND FAILED:**
- **60-sample SILK-CELT delay** - Made things MUCH worse (Q went to -108)
- The delay infrastructure exists but should NOT be used
- libopus handles timing internally via sequential decode order

**Most Likely Root Causes:**
1. **CELT energy inter-frame prediction** for bands 17-21 in stereo 10ms mode
2. **Fine energy decoding** may have subtle precision difference affecting gain
3. **SILK stereo MID channel** gain in hybrid mode might be slightly off

**Test Files Created:**
- `tv06_packet_transition_test.go` - Analyzes quality around packet 1497
- `tv06_stereo_analysis_test.go` - Per-channel L/R error analysis
- `tv06_framesize_analysis_test.go` - Compares 10ms vs 20ms quality
- `tv06_packet_content_test.go` - RMS and amplitude analysis
- `tv06_detailed_1252_test.go` - Detailed cumulative quality tracking

**Fix complexity**: Medium-High - Root cause identified as ~10% gain reduction in stereo 10ms Hybrid FB mode

### testvector07 (CELT with 2.5ms frames, Q=-40)
- **Issue**: Most errors come from 2.5ms CELT frames (fs=120), NOT transient frames
- **Pattern**: 2.5ms frames have SNR 20-50 dB (should be 80+ dB), error ~1-3%

#### Investigation (Jan 2026)

**Verified NOT the problem:**
1. DFT precision: 60-point DFT has 2.2e-13 error (excellent)
2. IMDCT math: energy ratio correct (60x as expected)
3. Window coefficients: match libopus within 3e-8
4. De-emphasis filter: identical formula to libopus
5. Overlap buffer TDAC: windowing formulas match libopus
6. dynalloc.go LM=0 handling: correct (takes max with previous frame energy for first 8 bands)

**FIXED:**
1. **TF analysis early return** - Removed `if lm == 0 { return tfRes, 0 }` from TFAnalysis()
2. **Encoder enableTFAnalysis** - Removed spurious `lm > 0` check
3. libopus runs tf_analysis for ALL LM values including LM=0

**Remaining Suspects:**
1. **Lowband folding** - For LM=0, effectiveLowband ≈ 0 for early bands (M=1 constraint)
2. **Band width effects** - Very small band widths (1-4 bins) for LM=0 may cause pulse allocation differences
3. **Coefficient dequantization** - algUnquant for n=1 or n=2 may behave differently
4. **TF select** - For LM=0, tf_select is always 0 (tfSelectRsv = LM>0 is false)

**Fix complexity**: Medium - need to trace coefficient extraction for 2.5ms frames

### testvector08/09 (SILK stereo, Q=-92.46 / Q=-84.64)
- **Issue**: Both have similar Q values - indicates consistent stereo bug
- **Pattern**: SILK mono passes (testvector02-04), only stereo fails
- **Key finding**: testvector08 contains BOTH SILK and CELT packets:
  - Packets 0-4: SILK stereo (config=1, TOC=0x0C)
  - Packets 5+: CELT stereo (config=17, TOC=0x8C)
  - Error appears at packet 14 which is CELT, not SILK!

#### Detailed Investigation (Jan 2026)

**Error Pattern Analysis:**
- Packets 0-13: Perfect (SNR 130+ dB for both channels)
- Packet 14: R channel SNR drops to 18.4 dB while L stays at 134 dB
- Even with FRESH decoders, packet 14 has R_SNR=15.5 dB → issue is packet-specific
- Error pattern: **diff_M = -diff_S** (same magnitude, opposite signs!)
- This means: go_M = lib_M + ε, go_S = lib_S - ε
- Consequently: diff_L = 0, diff_R = 2ε (L stays correct, R shows 2x error)
- The symmetric error proves issue is in **SIDE channel modification during stereo prediction**

**Verified CORRECT (DO NOT INVESTIGATE AGAIN):**
1. silkStereoMSToLR formulas - mathematically identical to libopus
2. silkSMLAWB, silkSMULWB, silkSMULBB, silkSMLABB macros - all match libopus exactly
3. stereoInterpLenMs constant: 8 (matches libopus)
4. stereoQuantSubSteps constant: 5 (matches libopus)
5. silk_stereo_pred_quant_Q13 table: matches libopus exactly
6. Delta calculation: matches libopus exactly (same rounding errors)
7. denomQ16 calculation: matches libopus for all sample rates
8. Buffer setup (sMid/sSide state): matches libopus exactly
9. Loop indexing (n=0 to frameLength-1): matches libopus exactly
10. predPrevQ13 type (int16 vs int32): NOT the issue - values fit in int16 range

**Error Characteristics:**
- Error grows linearly within the frame (sample 0 to sample 240)
- Average diff_S: -0.000036 (small constant bias)
- Scale ratio (go_S/lib_S by energy): 1.000195 (nearly 1, not a gain issue)
- Predictor values at packet 13→14 transition:
  - pred0: 5892 → 0 (delta = -5892)
  - pred1: -2737 → 5450 (delta = 8187)
  - All values fit in int16 range (-26726 to 26726)

**Remaining Suspects (NOT YET VERIFIED):**
1. **State initialization at decoder creation** - predPrevQ13, sMid, sSide zeroing
2. **State reset on mode transitions** - resetSideChannelState() may not reset predPrevQ13
3. **SILK-CELT interaction in Hybrid** - state bleed between modes
4. **Resampler state** - might affect stereo differently than mono
5. **Frame-to-frame accumulation** - small errors may compound over 14 packets

**Fix Complexity:** Hard - the math is correct, issue is subtle state management

### testvector10 (Mixed mode stereo, Q=-25)
- **Issue**: Combination of CELT stereo and Hybrid stereo modes
- **Likely causes**: Compound issues from CELT transient + stereo handling
- **Fix complexity**: High - depends on fixing testvector07 and 08/09 first

### testvector12 (SILK/Hybrid transitions, Q=-34)
- **Issue**: Mode transition handling between SILK and Hybrid modes
- **Pattern**: Contains both SILK and Hybrid packets with transitions

#### Investigation (Jan 2026)

**Transition Detection - CORRECT:**
- gopus and libopus both detect: CELT↔SILK/Hybrid transitions
- Conditions match: `mode != MODE_CELT_ONLY && prev_mode == MODE_CELT_ONLY` etc.

**FIXED:**
1. **Transition audio mode** - Was using current `mode`, now uses `d.prevMode`
2. **CELT channel transitions** - Now copies energy arrays on mono↔stereo

**Remaining Discrepancies Found:**
1. **CELT reset timing in Hybrid** - libopus resets before CELT decode, gopus resets after SILK decode via `afterSilk` callback
2. **Mono→stereo CELT** - gopus only copies overlap buffer, libopus does full reset including energy history (partially fixed)
3. **Hybrid decoder state** - `silkDelayBuffer` and `prevPacketStereo` persist across mode transitions but shouldn't

**Key Files:**
- `decoder_opus_frame.go` - main transition logic (lines 133-144, 225, 321)
- `internal/celt/decoder.go` - reset implementation
- `internal/hybrid/decoder.go` - Hybrid state management

**Fix complexity**: Medium - most issues identified, need targeted fixes

## VERIFIED CORRECT

1. IMDCT twiddle formula: `cos(2*π*(i+0.125)/N)`
2. De-emphasis filter with VERY_SMALL constant added
3. PreemphCoef value: 0.85000610
4. Window coefficients formula
5. Short block coefficient extraction pattern (b + i*shortBlocks)
6. Postfilter/comb filter implementation
7. TDAC windowing math

## RECOMMENDED FIX ORDER

1. **testvector08/09** (SILK stereo) - Consistent bug, likely single fix
2. **testvector07** (CELT transient) - Well-understood mechanism
3. **testvector12** (Mode transitions) - May resolve with stereo fix
4. **testvector06** (Hybrid) - Most complex, may partially resolve with other fixes
5. **testvector10** (Mixed) - Should resolve after 1-3 are fixed
