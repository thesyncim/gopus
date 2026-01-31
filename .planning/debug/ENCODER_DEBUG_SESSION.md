# Encoder Debug Session - 2026-01-30

## Session Status: COMPLETE - All agents finished, TF Analysis fix needed

## Summary

**Decoder: 11/12 passing** (testvector12 fails - SILK state accumulation at BW transitions)
**Encoder: SNR = -4.30 dB** (TF analysis divergence at byte 7)

## Verified Fixes ✅

### 1. SILK Range Encoder Lifecycle (FIXED)
- **Bug**: `e.rangeEncoder` not cleared after standalone encoding
- **Fix**: Added `e.rangeEncoder = nil` after `Done()` in `encode_frame.go`
- **Result**: TestEncodeStreaming now passes (all 5 frames produce 106 bytes)
- **See**: SILK_RANGE_FIX.md

### 2. SILK GainDequantTable (FIXED)
- **Bug**: Table values were ~1000x too small (81 instead of 81920)
- **Fix**: Regenerated table with correct libopus formula in `codebook.go`
- **Result**: All gain quantization tests now pass
- **See**: SILK_GAIN_FIX.md

### 3. SILK Gain Quantization (FIXED)
- **Bug**: Quantization formula didn't match libopus
- **Fix**: Added `computeLogGainIndexQ16()` with proper Q16 formula in `gain_encode.go`
- **Result**: Gain indices computed correctly
- **See**: SILK_GAIN_FIX.md

### 4. SILK Excitation Scaling (FIXED)
- **Bug**: `computeExcitation()` used normalized floats [-1,1] without scaling
- **Fix**: Added 32768 scaling to convert to Q0 int16 range in `excitation_encode.go`
- **Result**: Proper excitation magnitudes now produced
- **See**: SILK_EXCITATION_FIX.md

### 5. CELT PVQ Search Input (FIXED)
- **Bug**: `opPVQSearch` was modifying input slice in-place (absolute values)
- **Fix**: Created local absX copy for search operations in `pvq_search.go`
- **Result**: Input preservation maintained
- **See**: CELT_INVERSION_FIX.md

### 6. Toneishness Detection (IMPLEMENTED)
- **Feature**: Added tone detection matching libopus
- **Files**: `transient.go` - Added `toneLPC()`, `toneDetect()`, `ToneFreq`, `Toneishness`
- **Result**: Pure low-frequency tones (< ~198 Hz) now suppress transient detection
- **See**: CELT_PREEMPH_FIX.md

### 7. TF Analysis Gating (IMPLEMENTED)
- **Feature**: Added toneishness check to disable TF analysis for pure tones
- **File**: `encode_frame.go` - Added `toneishness < 0.98` to `enableTFAnalysis`
- **Result**: Matches libopus behavior for pure tones

## Verified Correct (No Changes Needed) ✅

| Component | Verification | Result |
|-----------|-------------|--------|
| Pre-emphasis | Correlation with libopus | 1.000000 |
| MDCT forward | SNR with libopus | > 138 dB |
| CWRS encoding | Roundtrip test | Signs preserved |
| expRotation | Roundtrip SNR | > 599 dB |
| Coarse energy | Byte comparison | First 7 bytes match |

## Documented Issues 📝

### SILK LSF/NLSF Quantization (Complex - Not Blocking)
- gopus uses floating-point Chebyshev root finding
- libopus uses fixed-point with piecewise-linear cosine tables
- Stage 2 codebooks in gopus are placeholder/simplified
- Missing: multi-survivor search, trellis quantization, RD optimization
- **See**: SILK_LSF_FIX.md
- **Impact**: Medium - affects encoder quality but decoder works correctly

### SILK Gain Input Scaling (Not Blocking)
- `computeSubframeGains()` computes RMS energy (~0.1-0.5)
- libopus computes LPC prediction error energy (millions)
- **Impact**: Gain indices may be wrong range

## Critical Bug - BLOCKING 🔍

### TF Analysis Divergence (BLOCKING)
- **Symptom**: Byte 7 diverges: gopus=`0x33`, libopus=`0xD0`
- **Result**: SNR = -4.30 dB, correlation = -0.54 (inverted signal)
- **Location**: `internal/celt/tf.go` - TFAnalysis function

**What matches:**
- First 7 bytes identical: `7B 5E 09 50 B7 8C 08`
- Header flags, coarse energy all correct
- Both detect transient=true for first frame
- Both have toneishness < 0.98 (TF analysis enabled)

**What diverges:**
- TF resolution encoding starting at bit 56 (byte 7)
- Haar transform output or L1 metric computation
- Viterbi search for optimal tfRes values

**Root Cause Candidates:**
1. Haar transform `haar1()` produces different results
2. L1 metric `l1Metric()` computation differs
3. Viterbi search in TFAnalysis uses different path costs
4. tfSelectTable values or indexing

## Decoder Issue (Separate)

### testvector12 Failure
- **Symptom**: Q = -32.06 after ~800 packets
- **Root Cause**: SILK decoder state accumulation at bandwidth transitions
- **Location**: State desync at NB→MB→WB transitions
- **See**: tv12-hybrid-mode-divergence.md

## All Parallel Agents - COMPLETE

| Agent | Focus Area | Status | Findings File |
|-------|-----------|--------|---------------|
| Agent 1 | SILK Gain Computation | ✅ FIXED | SILK_GAIN_FIX.md |
| Agent 2 | SILK Range Encoder | ✅ FIXED | SILK_RANGE_FIX.md |
| Agent 3 | CELT Signal Inversion | ✅ FIXED | CELT_INVERSION_FIX.md |
| Agent 4 | CELT Energy Quantization | ✅ COMPLETE | CELT_ENERGY_FIX.md |
| Agent 5 | SILK Excitation Scaling | ✅ FIXED | SILK_EXCITATION_FIX.md |
| Agent 6 | SILK LSF/NLSF Encoding | ✅ DOCUMENTED | SILK_LSF_FIX.md |
| Agent 7 | CELT Pre-emphasis | ✅ VERIFIED | CELT_PREEMPH_FIX.md |

## Files Modified This Session

### SILK Encoder
1. `internal/silk/encode_frame.go` - Range encoder lifecycle fix
2. `internal/silk/codebook.go` - Fixed GainDequantTable values
3. `internal/silk/gain_encode.go` - Q16 quantization formula
4. `internal/silk/excitation_encode.go` - Q0 scaling fix

### CELT Encoder
5. `internal/celt/pvq_search.go` - absX copy (no input modification)
6. `internal/celt/transient.go` - Added tone detection
7. `internal/celt/encode_frame.go` - toneishness gating for TF

### Tests Created
- `internal/celt/cgo_test/preemphasis_compare_test.go`
- `internal/celt/cgo_test/pvq_sign_test.go`
- `internal/celt/cgo_test/decoder_compare_test.go`
- `internal/celt/cgo_test/packet_format_test.go`
- `internal/celt/cgo_test/byte_divergence_test.go`
- `internal/celt/cgo_test/cwrs_roundtrip_test.go`
- `internal/celt/cgo_test/tf_divergence_test.go`
- `internal/celt/cgo_test/transient_compare_test.go`
- `internal/celt/cgo_test/silk_excitation_compare_test.go`
- `internal/celt/cgo_test/silk_lsf_encode_compare_test.go`

## Next Steps (Priority Order)

### 1. Fix TF Analysis (CRITICAL - Encoder)
Debug `TFAnalysis()` in `internal/celt/tf.go`:
- Compare Haar transform output per-band
- Compare L1 metric values
- Trace Viterbi search paths
- Verify tfSelectTable indexing

### 2. Fix testvector12 (Decoder)
Debug SILK state at bandwidth transitions:
- Investigate packet 826+ divergence
- Check LPC synthesis with accumulated state
- Verify resampler state at BW changes

## Reference Code
- libopus C reference: `tmp_check/opus-1.6.1/`
- TF analysis: `tmp_check/opus-1.6.1/celt/celt_encoder.c` (tf_analysis)
- SILK decoder: `tmp_check/opus-1.6.1/silk/`

## Progress Log
- 2026-01-30 18:42: Session started
- 18:44: SILK Range Encoder bug fixed
- 18:49: SILK Excitation scaling fixed
- 18:52: SILK LSF/NLSF differences documented
- 18:55: SILK GainDequantTable fixed
- 19:00: CELT inversion investigation documented
- 19:05: ROOT CAUSE - Transient detection identified
- 19:30: Toneishness detection implemented
- 19:45: TF gating added
- 20:00: All agents completed
- **Current**: TF Analysis at byte 7 identified as critical blocker
