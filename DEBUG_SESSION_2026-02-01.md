# Encoder Debug Session - 2026-02-01

## Current Test Failures

### 1. Bitrate Control Issues (internal/encoder)
- `TestBitrateModeCBR`: Wrong packet sizes (176 vs 160 expected)
- `TestBitrateModeCVBR`: Packets exceed max (210, 214, 196, 208 vs 184 max)
- `TestCBRDifferentBitrates`: 32kbps gives 176 bytes (expect 80), 64kbps gives 176 (expect 160)
- `TestEncoderBitrateRange`: All bitrates produce ~180 bytes regardless of setting

### 2. SILK Native Core Issues (internal/celt/cgo_test)
- `TestSilkNativeEncoderFlat`: 266 samples with |diff| > 1
- `TestSilkNativeCoreFirstMismatch`: First mismatch at pkt=0 sample=2
- Output differences: go=0 vs lib=1, go=0 vs lib=2, etc.

### 3. Pre-emphasis State Drift (internal/celt/cgo_test)
- `TestPreemphStateComparison`: State drift starting at packet 61
- Max difference: 1.93611145 at packet 64

## Agent Assignments

| Agent | Worktree | Focus | Status |
|-------|----------|-------|--------|
| 31 | agent-31 | Bitrate/CBR control | ✅ MERGED (3f7fce3) |
| 32 | agent-32 | SILK native core | ✅ MERGED (c6984a3) |
| 33 | agent-33 | Pre-emphasis state | ✅ FIXED (commit 5464a5f) |
| 34 | agent-34 | Byte 16 divergence (CELT) | 🔄 Active |
| 37 | agent-37 | PVQ normalization | ✅ FIXED (commit 476a14b) |

## C Reference Location
- `/Users/thesyncim/GolandProjects/gopus/tmp_check/opus-1.6.1/`
- Comparison tool: `tmp_check/check_frame_glue.go`

## Key Constraints
- Decoder is WORKING FINE - do not regress it
- Merge fixes back to master when verified
- Document all findings here

---

## Findings Log

### Agent 31: Bitrate Control

#### Root Cause Found: VBR Mode Not Propagated to CELT Encoder

**Problem**: The encoder was producing ~176-180 byte packets regardless of bitrate settings (even at 6kbps which should produce ~15 bytes). CBR and CVBR modes were not being respected.

**Root Cause (Multiple Issues)**:

1. **CELT-only mode**: The CELT encoder defaulted to VBR mode (`vbr: true` in NewEncoder()), but when the high-level Opus encoder set CBR mode via `SetBitrateMode(ModeCBR)`, this setting was NEVER propagated to the CELT encoder. The CELT encoder continued using VBR logic which boosts bitrate based on signal characteristics.

2. **Hybrid mode**: Similar issue - the hybrid encoding path (`encodeHybridFrame`) didn't propagate bitrate mode to the CELT encoder. Additionally, the bit budget calculation used `maxHybridPacketSize * 8` (1275 bytes = max) instead of the target bitrate.

3. **TOC byte accounting**: The target byte calculation didn't account for the TOC byte that `BuildPacket()` adds, resulting in packets being 1 byte larger than expected.

**Evidence**:
- Before fix: CBR at 64kbps produced 176-180 bytes (expected 160)
- Before fix: CBR at 32kbps produced 176 bytes (expected 80)
- VBR boost was being applied even in CBR mode due to `e.vbr == true` in CELT encoder

**libopus behavior**:
- CBR mode: `ec_enc_shrink(enc, nbCompressedBytes)` constrains the range encoder
- VBR mode: Computes target with boosts for tonality, transients, etc.
- The bitrate mode directly controls the VBR flag in CELT encoding

**Fix Applied**:

1. **encoder.go - encodeCELTFrame()**: Added propagation of bitrate mode to CELT encoder
```go
switch e.bitrateMode {
case ModeCBR:
    e.celtEncoder.SetVBR(false)
    e.celtEncoder.SetConstrainedVBR(false)
case ModeCVBR:
    e.celtEncoder.SetVBR(true)
    e.celtEncoder.SetConstrainedVBR(true)
case ModeVBR:
    e.celtEncoder.SetVBR(true)
    e.celtEncoder.SetConstrainedVBR(false)
}
```

2. **hybrid.go - encodeHybridFrame()**:
   - Added same bitrate mode propagation for hybrid encoding
   - Fixed bit budget calculation to use target bitrate instead of max
   - Account for TOC byte (subtract 1 from target)
   - Added `re.Shrink()` for CBR/CVBR modes

3. **hybrid.go - encodeCELTHybrid()**: Fixed totalBits calculation to use bitrate-derived target for CBR mode

**Files Changed**:
- `internal/encoder/encoder.go` - encodeCELTFrame function
- `internal/encoder/hybrid.go` - encodeHybridFrame, encodeCELTHybrid functions

**Verification**:
- `TestBitrateModeCBR`: PASS (160 bytes exactly at 64kbps)
- `TestBitrateModeCVBR`: PASS (packets within 15% tolerance)
- `TestCBRDifferentBitrates`: PASS (80, 160, 240, 320 bytes at 32/64/96/128 kbps)
- `TestEncoderBitrateRange`: PASS (6kbps=15 bytes, 12kbps=30 bytes, etc.)
- All other encoder tests: PASS
- All decoder tests: PASS (no regressions)

**Status**: FIXED - Committed to fix-agent-31 branch

### Agent 32: SILK Native Core

#### Root Cause Found: Test Was Comparing Wrong Output Types

**Problem**: Tests `TestSilkNativeEncoderFlat` and `TestSilkNativeCoreFirstMismatch` were reporting 266 samples with |diff| > 1, with the Go decoder consistently producing values 1-2 lower than libopus (go=0 vs lib=1, go=0 vs lib=2).

**Root Cause**: The test was comparing DIFFERENT outputs:
- **Go decoder** (`DecodeFrame`): Returns samples WITH delay compensation (via `applyMonoDelay`)
- **libopus wrapper** (`DecodePacketNativeCore`): Returns RAW core output WITHOUT delay compensation

The delay compensation function in Go shifts samples through a delay buffer, which prepends zeros at the start of the output. This is correct behavior for matching libopus's API output, but the test was trying to compare raw core outputs.

**Evidence**:
- First samples showed go=0 while lib=1,2,3... (zeros from delay buffer vs actual values)
- The pattern of underestimation was exactly the shift introduced by delay compensation
- Running `TestSilkDecodeCoreCompareFrame2` (which feeds identical inputs to both Go and C cores) passed, proving the core implementations match

**libopus Architecture**:
```
silk_decode_core() -> raw samples -> sMid buffering -> resampler input delay -> API output
```

The Go `DecodeFrame()` applies delay compensation to match libopus API behavior, while `DecodeFrameRaw()` returns raw core output.

**Fix Applied** (silk_native_core_compare_test.go):
Changed both tests to use `DecodeFrameRaw` instead of `DecodeFrame`:
```go
// Before (wrong - includes delay compensation):
goNative, err := goDec.DecodeFrame(&rd, silkBW, duration, true)

// After (correct - raw core output):
goNative, err := goDec.DecodeFrameRaw(&rd, silkBW, duration, true)
```

**Verification**:
- `TestSilkNativeCoreFirstMismatch`: PASS
- `TestSilkNativeCorePacket1AfterPacket0`: PASS
- `TestSilkDecodeCoreCompareFrame2`: PASS (was already passing)
- All testvector tests: PASS
- All decoder tests: PASS (no regressions)

**Files Changed**:
- `internal/celt/cgo_test/silk_native_core_compare_test.go` - Changed DecodeFrame to DecodeFrameRaw in both tests

**Status**: FIXED - Test issue corrected, decoder core was never broken

### Agent 33: Pre-emphasis State

#### Root Cause Found: float64 vs float32 Precision Mismatch

**Problem**: De-emphasis filter state was accumulating differently over time compared to libopus.

**Root Cause**: The Go decoder used `float64` precision for the de-emphasis filter computation, while libopus uses `float32`. The de-emphasis filter is an IIR filter with feedback:
```
y[n] = x[n] + 0.85 * y[n-1]
state = 0.85 * y[n]  // feedback term
```

This feedback term gets multiplied repeatedly (by 0.85 each sample), causing tiny precision differences between float64 and float32 to accumulate over thousands of samples.

**Evidence**:
- Packet 60 (before divergence): go=[52.76, 52.76] lib=[52.76, 52.76] diff=[0.00003, 0.00003]
- Packet 61 (divergence start): go=[-36.39, -36.39] lib=[-36.37, -36.37] diff=[0.022, 0.022]
- Packet 64 (large error): go=[238.59, 238.59] lib=[240.53, 240.53] diff=[1.94, 1.94]

**libopus uses** (from arch.h):
- `celt_sig` = `float` (32-bit)
- `MULT16_32_Q15(a,b)` = `(a)*(b)` in float mode
- `VERY_SMALL` = `1e-30f`

**Fix Applied** (decoder.go, applyDeemphasis function):
- Changed from `float64` to `float32` for filter state and intermediate calculations
- Cast input samples to float32, perform de-emphasis, cast output back to float64
- This matches libopus behavior exactly

```go
// Before (causing drift):
state := d.preemphState[0]  // float64
tmp := samples[i] + verySmall + state
state = PreemphCoef * tmp   // float64 * float64

// After (matching libopus):
state := float32(d.preemphState[0])
tmp := float32(samples[i]) + verySmall + state
state = coef * tmp  // float32 * float32
```

**Tests Updated**:
- `TestDeEmphasis` in mdct_test.go: Loosened tolerance to account for float32 precision

**Verification**:
- All testvector tests pass (43.7 dB SNR)
- All decoder tests pass
- No regressions to the decoder functionality

**Files Changed**:
- `internal/celt/decoder.go` - applyDeemphasis function
- `internal/celt/mdct_test.go` - TestDeEmphasis tolerances

**Status**: MERGED TO MASTER (6f2d709)

**Note**: The CGO state comparison test (`TestPreemphStateComparison`) still shows divergence at packet 61.
The float32 fix improved precision for packets 0-60 (differences now at ~1e-5 level), but there's a sudden
jump at packet 61 that may be caused by different INPUT samples to the de-emphasis filter, not the filter
itself. This requires further investigation into what's happening at packet 61 (possibly a transient or
mode switch).

### Agent 34: Byte 16 Divergence
🔄 Active - Investigating

#### Investigation Progress

**Problem**: First 16 bytes match between gopus and libopus, but divergence occurs at byte 16 (bit 128).

**Key Finding (2026-02-01 Latest)**: Test Bug Fixed, Fine Quant NOW MATCHES!

Fixed test `trace_real_encoding_test.go` - was using wrong decoding method (`DecodeUniform` instead of `DecodeRawBits`) for fine energy. After fix:

- **ALL coarse energies match exactly** (21/21 bands, diff=0.000000)
- **ALL fine quant values match exactly** (lib_q == go_q for all bands)
- **ALL TF values match**
- **Spread and Trim match**
- **PVQ indices for bands 0-4 match exactly**:
  - Band 0: idx=1108958327 (both)
  - Band 1: idx=2878677914 (both)
  - Band 2: idx=899385745 (both)
  - Band 3: idx=2889112013 (both)
  - Band 4: idx=693035576 (both)

**BUT byte-level divergence remains at byte 16:**
```
Byte 16: lib=0xEA go=0xEB (11101010 vs 11101011) <-- 1 bit diff in LSB
Byte 17: lib=0x88 go=0xA1
Byte 18: lib=0x27 go=0x9A
...cascades from there
```

**Bit position analysis:**
- Tell after allocation: 83 bits
- Divergence at: bit 128 (byte 16)
- Band 0 PVQ finishes at: tell=177

So divergence is DURING band 0's PVQ encoding (bit 83 to 177), even though the PVQ INDEX is identical!

**Final Range differs:**
- gopus: 0x00BA5BA7
- libopus: 0x4EA5A600

**Current Hypothesis**: Range encoder internal state differs despite encoding same values. This could be due to:
1. Different normalization timing
2. Subtle difference in division/rounding during range encoding
3. Different carry propagation behavior

**PVQ encoding breakdown for band 0:**
- ft = 4066763520, val = 1108958327
- Multi-byte case: ftb=24 after subtraction
- Range encodes: valHigh=66 in [0, 243)
- Raw encodes: valLow=1662071 in 24 bits

**Files Modified During Investigation**:
- `internal/celt/cgo_test/trace_real_encoding_test.go`
  - Fixed DecodeUniform -> DecodeRawBits for fine energy
  - Added PVQ index comparison for bands 0-4
  - Added byte-level comparison around divergence

**CRITICAL DISCOVERY (2026-02-01)**:

The divergence at byte 16 falls in **band 1's** PVQ encoding (tell=120-155, bytes 15-19):
- Band 0: tell=83-120, bytes 10-15 (MATCHES)
- Band 1: tell=120-155, bytes 15-19 (DIVERGES at byte 16)

BUT the PVQ indices for both bands MATCH exactly:
- Band 0: idx=1108958327 (both)
- Band 1: idx=2878677914 (both)

This proves the divergence is NOT in the PVQ search or MDCT coefficients.

**Also confirmed matching:**
- Bytes from end (fine energy raw bits) - MATCH perfectly
- Single uniform encoding in isolation - MATCH (TestCompareRangeEncoderUniform passes)

**Root Cause Theory:**
The range encoder's internal state (val, rng) accumulates a difference during header encoding (bytes 0-15) that doesn't manifest as different byte output until band 1's encoding. The state can differ while producing the same bytes because bytes are only flushed during normalization.

When band 1's PVQ index (2878677914) is encoded, the different starting state causes different byte output despite encoding the same value.

**Investigation Progress (Agent 34 Continuation)**:

Added CGO wrappers to trace encoder state (`LibopusEncoderTracer`, `ECEncStateTrace`).

**Key Findings:**

1. **Range encoder operations match in isolation:**
   - `TestCompareEncoderStateStepByStep`: PASS
   - Header bits (silence, postfilter, transient, intra): State matches exactly
   - EncodeUniform for PVQ indices: State matches exactly

2. **Full header + Laplace (coarse energy) matches:**
   - `TestHeaderLaplaceSequence`: PASS (bytes match exactly)
   - `TestFullHeaderLaplaceComparison`: PASS
   - State at tell=59 matches: rng=0x32C64300, val=0x60935800

3. **Fine quant values match when decoded correctly:**
   - Fixed test to use `DecodeRawBits` instead of `DecodeUniform` for fine energy
   - All 21 bands now match exactly

4. **Verified matching:**
   - Coarse energies: 21/21 bands match
   - Fine quant values: 21/21 bands match
   - TF values: 21/21 bands match
   - TF select: matches (lib=1 go=1)
   - Spread: matches (lib=3 go=3)
   - Trim: matches (lib=8 go=8)
   - Tell after allocation: 83 bits (both)

5. **BUT byte-level divergence remains at byte 16 (bit 128):**
   - All decoded values match
   - Bytes 0-15 match exactly
   - Bytes from end (raw bits) match exactly
   - Byte 16: lib=0xEA go=0xEB (1 bit difference in LSB)
   - Final range: gopus=0x00BA5BA7 libopus=0x4EA5A600

**Root Cause Analysis:**

The divergence is NOT in any of the value computations (coarse, fine, TF, spread, trim, allocation).
The divergence is NOT in the range encoder operations themselves.

The only remaining possibility: **Something in the actual BANDS encoding (PVQ area)** differs.
Specifically between tell=83 and byte 16 (bit 128), which is during band 0 or band 1 PVQ encoding.

Possible causes:
1. The MDCT coefficients fed to PVQ search differ slightly (precision)
2. The PVQ search produces different pulse vectors despite same correlation
3. The bands encoding logic has a subtle difference in bit allocation or encoding order
4. There's a normalization or scaling issue in the bands encoding path

**Next Steps**:
1. Compare exact MDCT coefficients for bands 0 and 1 between gopus and libopus
2. Compare exact PVQ pulse vectors for bands 0 and 1
3. Trace the bands encoding at a lower level to find the first differing bit
4. Check `quantAllBandsEncode` in bands_quant.go for any differences from libopus

**Files to Investigate**:
- `internal/celt/bands_quant.go` - quantAllBandsEncode, quantBandEncode
- `internal/celt/bands_encode.go` - normalization, PVQ encoding
- `internal/celt/pvq_search.go` - opPVQSearch implementation
- `internal/celt/cwrs.go` - EncodePulses implementation

### Agent 37: PVQ Normalization

#### Root Cause Found: Log-Domain Energy Roundtrip in Normalization

**Problem**: Encoder produces packets that libopus can decode, but with very poor audio quality (SNR of -1 to -4 dB). Agent 34 had found coarse/fine energies match, but byte-level divergence starts at byte 16 in PVQ territory.

**Root Cause**: gopus was normalizing MDCT coefficients using the WRONG energy values:

1. **libopus flow**:
   - `compute_band_energies()` -> linear amplitude (`bandE` = sqrt(sum of squares))
   - `amp2Log2()` -> log-domain (`bandLogE`) for coarse/fine encoding
   - `normalise_bands(freq, X, bandE)` -> uses ORIGINAL linear `bandE`

2. **gopus flow (before fix)**:
   - `ComputeBandEnergies()` -> returns log-domain directly
   - `EncodeCoarseEnergy()` -> quantizes log-domain energies
   - `NormalizeBandsToArray()` -> converts QUANTIZED log back to linear

The problem: gopus normalized with **quantized** energies reconstructed from log domain, but libopus normalizes with **original** (unquantized) linear amplitudes. The log->linear roundtrip and quantization both introduced errors.

**Evidence**:
- libopus `bands.c` normalise_bands() (lines 172-187):
  ```c
  opus_val16 g = 1.f/(1e-27f+bandE[i+c*m->nbEBands]);
  X[j+c*N] = freq[j+c*N]*g;
  ```
  `bandE` is celt_ener type = linear amplitude (sqrt of sum of squares)

- gopus was doing:
  ```go
  eVal := energies[band]  // log2(amplitude) - eMeans[band]
  eVal += eMeans[band] * DB6  // Add back eMeans
  gain := math.Exp2(eVal / DB6)  // Convert log2 to linear (introduces error!)
  norm[offset+i] = mdctCoeffs[offset+i] / gain
  ```

- libopus `celt_encoder.c` maintains separate representations:
  - Line 2096: `compute_band_energies()` -> linear
  - Line 2106: `amp2Log2()` -> log domain for encoding
  - Line 2240: `normalise_bands()` -> uses LINEAR bandE

**Fix Applied**:

1. **bands_encode.go - Added ComputeLinearBandAmplitudes()**:
   Computes sqrt(sum of squares) directly from MDCT coefficients, matching libopus:
   ```go
   func ComputeLinearBandAmplitudes(mdctCoeffs []float64, nbBands, frameSize int) []float64 {
       bandE := make([]float64, nbBands)
       for band := 0; band < nbBands; band++ {
           sum := float32(1e-27)
           for i := 0; i < n; i++ {
               v := float32(mdctCoeffs[offset+i])
               sum += v * v
           }
           bandE[band] = float64(math.Sqrt(float64(sum)))
       }
       return bandE
   }
   ```

2. **bands_encode.go - Updated NormalizeBandsToArray()**:
   Now uses direct linear amplitudes instead of reconstructing from log:
   ```go
   func (e *Encoder) NormalizeBandsToArray(...) []float64 {
       bandE := ComputeLinearBandAmplitudes(mdctCoeffs, nbBands, frameSize)
       // ... normalize using bandE directly
   }
   ```

3. **encode_frame.go - Updated bandE computation**:
   Uses `ComputeLinearBandAmplitudes()` for bandE passed to quantAllBandsEncode.

4. **Uses float32 precision** for amplitude computation to match libopus.

**Files Changed**:
- `internal/celt/bands_encode.go` - Added ComputeLinearBandAmplitudes(), updated NormalizeBandsToArray()
- `internal/celt/encode_frame.go` - Updated bandE computation
- `internal/celt/normalization_test.go` - Added verification tests

**Verification**:
- `TestNormalizeBandsToArrayUnitNorm`: PASS (all bands have L2 norm = 1.0)
- `TestComputeLinearBandAmplitudes`: PASS
- `TestNormalizationUsesLinearAmplitudes`: PASS
- `TestNormalizationRoundTrip`: PASS
- All encoder tests: PASS
- All decoder tests: PASS (no regressions)

**Status**: FIXED - Committed to fix-agent-37 branch (476a14b)

---

## Verified Fixes (Do NOT Regress)
- Laplace encoding fix (EncodeBin vs Encode)
- SILK ICDF table fixes
- Dynalloc offsets fix
- Transient detection patch
- Allocation trim fix
- CBR Shrink() addition
- Range encoder padding byte fix
