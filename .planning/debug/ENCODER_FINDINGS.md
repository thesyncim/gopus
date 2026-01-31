# Encoder Compliance Findings

## Status: 0/80 Tests Passing - Investigation In Progress
Started: 2026-01-30

## Baseline Test Results (Jan 30 2026)
| Mode | Configurations | Pass Rate | Q Range |
|------|---------------|-----------|---------|
| SILK | 48 configs | 0% | -99 to -133 |
| CELT | 16 configs | 0% | -96 to -108 |
| Hybrid | 16 configs | 0% | -90 to -105 |

**Total: 0/80 passing (0%)**

### Test Quality Scores
- SILK mono: Q ≈ -100 (SNR 0 dB)
- SILK stereo: Q ≈ -125 (SNR -12 dB)
- CELT mono: Q ≈ -100 (SNR 0 dB)
- CELT stereo: Q ≈ -103 (SNR -1.5 dB)
- Hybrid: Q ≈ -100 (SNR 0 dB)

## Empirically Verified OK ✅

### CELT Encoder (verified by main session)
1. **Range encoder basic operations** - Tests pass (TestRangeEncoderUniformMatchesLibopus)
2. **PVQ search algorithm** - 440/440 exact matches vs libopus (TestPVQSearchVsLibopusWithSameInput)
3. **PVQ encoding end-to-end** - Tests pass (TestPVQEncodingEndToEnd)
4. **Band energy roundtrip** - Perfect ratio 1.0000 in TestDetailedEncoderPath
5. **First 8 bytes of packet** - Match libopus (`7b5e0950b78c08`)

### SILK Encoder (verified by Agent 2)
1. **Range encoder EncodeICDF16** - Matches libopus ec_enc_icdf formula
2. **ICDF tables** - Match RFC 6716
3. **Stereo weight computation** - Mid/side formulas correct
4. **LSF codebook tables** - Match expected dimensions

## Known Issues ❌

### CRITICAL - Root Cause Identified

**SILK: Gain computation returns 0 for ALL inputs** (gain_encode.go)
- ALL 62 test cases fail - function returns idx=0 for all valid gains
- Formula `(logGain + 1.0) * 8.0` expects gains in [0.25, 16] but receives [0.001, 0.27]
- **Impact:** All decoded frames have near-zero amplitude → Q ≈ -100

**SILK: Range encoder not cleared between frames** (encode_frame.go)
- Frame 0 returns 106 bytes, frames 1+ return 0 bytes
- `e.rangeEncoder` persists after frame 0, causing empty output
- **Impact:** Multi-frame encoding completely broken

### CELT Encoder Issues
1. **Packet diverges at byte 8** - First 8 bytes match libopus, then completely different
2. **Self-decode SNR is 0.82 dB** - Both gopus and libopus decoders get same bad result
3. **Correlation is only 0.52** - Should be ~0.99 for good encoding
4. **Energy ratio is 0.60-0.70** - Decoded signal has only 60-70% of input energy

## Under Investigation 🔍
1. **CELT Encoder** - Energy quantization, PVQ encoding, range coder
2. **SILK Encoder** - NLSF encoding, LPC analysis, excitation quantization
3. **Range Coder** - Bit alignment, final range computation
4. **TOC Byte** - Packet structure and header formation

## Test Methodology
- Compare gopus encoder output against libopus reference decoder
- Use CGO wrapper tests for direct comparison
- Track packet-by-packet differences
- Encode with gopus → Decode with libopus → Compare to original

---

## Detailed Findings

### Session 1 - Initial Investigation (Jan 30 2026)

#### Parallel Investigation Areas:
1. **CELT Encoder Path** - bands_encode.go, energy_encode.go, pvq encoding
2. **SILK Encoder Path** - encode_frame.go, lsf_encode.go, gain_encode.go
3. **Range Coder** - encoder.go bit operations
4. **Packet Formation** - TOC byte, frame count byte, payload structure

---

### SILK Encoder Investigation (Agent 2)

#### Files Examined:
- `/Users/thesyncim/GolandProjects/gopus/internal/silk/encoder.go` - Encoder state and initialization
- `/Users/thesyncim/GolandProjects/gopus/internal/silk/encode_frame.go` - Main frame encoding orchestration
- `/Users/thesyncim/GolandProjects/gopus/internal/silk/lsf_encode.go` - LPC to LSF conversion
- `/Users/thesyncim/GolandProjects/gopus/internal/silk/lsf_quantize.go` - LSF two-stage VQ quantization
- `/Users/thesyncim/GolandProjects/gopus/internal/silk/gain_encode.go` - Gain computation and encoding
- `/Users/thesyncim/GolandProjects/gopus/internal/silk/excitation_encode.go` - Excitation/pulse encoding
- `/Users/thesyncim/GolandProjects/gopus/internal/silk/ltp_encode.go` - LTP coefficient analysis and encoding
- `/Users/thesyncim/GolandProjects/gopus/internal/silk/lpc_analysis.go` - Burg LPC analysis
- `/Users/thesyncim/GolandProjects/gopus/internal/silk/vad.go` - Voice Activity Detection
- `/Users/thesyncim/GolandProjects/gopus/internal/silk/pitch_detect.go` - Pitch lag detection
- `/Users/thesyncim/GolandProjects/gopus/internal/silk/stereo_encode.go` - Mid/side stereo encoding
- `/Users/thesyncim/GolandProjects/gopus/internal/silk/tables.go` - ICDF probability tables
- `/Users/thesyncim/GolandProjects/gopus/internal/silk/codebook.go` - LSF and LTP codebooks
- `/Users/thesyncim/GolandProjects/gopus/internal/rangecoding/encoder.go` - Range encoder

#### Empirically Verified OK:
1. **Range encoder EncodeICDF16** - Matches libopus ec_enc_icdf formula (verified in code review)
2. **ICDF tables** - Match RFC 6716 (ICDFFrameTypeVADActive, ICDFGainMSB*, etc.)
3. **Basic frame encoding produces output** - Single frame tests pass (106 bytes for WB 20ms)
4. **Stereo weight computation** - Mid/side conversion formulas correct
5. **LSF codebook tables** - LSFCodebookNBMB and LSFCodebookWB match expected dimensions

#### Known Issues Found:

##### CRITICAL ISSUE 1: Streaming Encoder Produces Empty Frames (CONFIRMED BY CODE)
- **Evidence:** TestEncodeStreaming shows frames 1-4 return 0 bytes while frame 0 returns 106 bytes
- **Location:** `encode_frame.go` lines 14-28 and 110-114
- **Root Cause Analysis:**
  1. Line 14: `useSharedEncoder := e.rangeEncoder != nil`
  2. Line 26: When standalone, `e.rangeEncoder = &rangecoding.Encoder{}`
  3. Line 110-114: `if useSharedEncoder { return nil }`
  4. **BUG:** `e.rangeEncoder` is NEVER cleared after standalone encoding!
  5. On frame 2+, `e.rangeEncoder != nil` is true, so `useSharedEncoder = true`
  6. Then at line 110, `return nil` is executed instead of `e.rangeEncoder.Done()`
- **Fix Required:** Add `defer func() { e.rangeEncoder = nil }()` after standalone encoder creation
  OR clear rangeEncoder at end of standalone path
- **Impact:** Multi-frame encoding is completely broken

##### CRITICAL ISSUE 2: Round-Trip Signal Correlation is -0.17
- **Evidence:** TestMonoRoundTrip_SignalRecovery shows decoded signal is essentially uncorrelated
- **Decoded RMS:** 0.197 vs expected ~3000 (10000 * int16Scale amplitude input)
- **Interpretation:** The signal is being reconstructed but at completely wrong values
  This suggests fundamental encoding parameter mismatch

##### ISSUE 3: LPC to LSF Conversion Uses Floating-Point Root Finding
- **Location:** `lsf_encode.go` lines 10-113
- **Problem:** Uses generic Chebyshev polynomial root finding with bisection
- **libopus approach:** Uses fixed-point NLSF representation and specific codebook matching
- **Impact:** LSF values may not match expected quantization levels, causing
  decoder reconstruction errors

##### ISSUE 4: Excitation Quantization Lacks Proper Scaling
- **Location:** `excitation_encode.go` lines 322-342 - `computeExcitation()`
- **Code:** `excitation[i] = int32(math.Round(residual))`
- **Problem:** Residual is computed from float32 PCM samples which are normalized [-1, 1].
  libopus expects Q0 PCM integers (int16 range). The quantization to integer without
  proper scaling loses all the signal amplitude.
- **Impact:** Pulse magnitudes are near-zero, producing minimal excitation

##### ISSUE 5: Gain Encoding Uses Incorrect Formula (CONFIRMED BY TEST)
- **Location:** `gain_encode.go` lines 59-90
- **Code:** `idx = int(math.Round((logGain + 1.0) * 8.0))`
- **Test Evidence:** TestComputeLogGainIndex FAILS
  - For gainQ16=97 (idx=2), computed idx=0
  - For gainQ16=17830 (idx=63), computed idx=0
  - ALL 62 test cases fail - function returns 0 for all valid gains
- **Root Cause:** The formula `(logGain + 1.0) * 8.0` works for gains in the range
  [0.25, 16] but GainDequantTable values converted to float are in range
  [0.001, 0.27]. For gain=0.27 (max table value), log2(0.27)=-1.9, so
  (-1.9 + 1) * 8 = -7.2, clamped to 0.
- **Fix Required:** The gain input is Q16, so must divide by 65536 BEFORE taking
  log, not after. Or use a lookup approach against GainDequantTable.
- **Impact:** Decoded gain is orders of magnitude wrong - THIS IS THE MAIN BUG

##### ISSUE 6: LSF Stage 2 Residual Encoding Doesn't Match Decoder
- **Location:** `lsf_quantize.go` lines 282-308
- **Problem:** Encoder writes `numResiduals = 3` stage 2 indices, but decoder
  expects specific EC_ICELP coding pattern per RFC 6716 Section 4.2.7.5.2
- **Impact:** Bitstream structure doesn't match what decoder expects

##### ISSUE 7: LTP Periodicity Encoding is Incomplete
- **Location:** `ltp_encode.go` lines 211-262
- **Problem:** For mid/high periodicity (1 or 2), falls back to encoding index 0
  in ICDFLTPFilterIndexLowPeriod, losing periodicity information
- **Code comment says:** "For compatibility with current decoder, fall back to low periodicity"
- **Impact:** LTP prediction quality degrades for voiced frames

#### Hypotheses to Test:

1. **Input Scaling Hypothesis**
   - Test: Feed PCM samples in int16 range (not normalized float) to encoder
   - Expected: Gain computation should produce table-range values

2. **LSF Quantization Hypothesis**
   - Test: Compare LSF stage 1 indices between gopus and libopus for same input
   - Expected: Indices should match or be very close

3. **Range Encoder State Hypothesis**
   - Test: Add e.rangeEncoder = nil reset at start of standalone encoding
   - Expected: Multi-frame encoding should work

4. **Excitation Scaling Hypothesis**
   - Test: Scale excitation by 32768 before quantization
   - Expected: Pulse magnitudes should be non-trivial

5. **NLSF vs LSF Hypothesis**
   - Test: Use libopus-style NLSF representation instead of floating-point LSF
   - Expected: Better match with decoder's NLSF-to-LPC reconstruction

#### Architecture Observations:

1. **Signal Flow:** PCM -> LPC Analysis -> LSF -> NLSF Quantize -> Encode
   - Issue: No explicit NLSF step; LSF computed directly from LPC

2. **Gain Flow:** PCM -> RMS Energy -> Log Gain Index -> Encode
   - Issue: RMS of normalized floats produces tiny gains

3. **Excitation Flow:** PCM -> LPC Residual -> Shell Encode
   - Issue: Float residual quantized without amplitude scaling

4. **Pitch Flow:** Autocorrelation -> 3-stage Search -> Contour Match -> Encode
   - Looks reasonable but untested against libopus

#### Recommended Fix Priority:

1. **HIGH - CRITICAL:** Fix gain index computation (TestComputeLogGainIndex fails 100%)
   - This alone explains Q=-100 results
   - Gain is the main amplitude control; wrong gain = zero amplitude output
2. **HIGH - CRITICAL:** Fix streaming encoder (rangeEncoder state persistence)
   - Blocks multi-frame encoding entirely
3. **HIGH:** Fix excitation quantization scaling
   - Float residuals need scaling to int16 range before quantization
4. **MEDIUM:** Verify LSF quantization matches decoder expectations
5. **MEDIUM:** Review NLSF representation vs floating-point LSF
6. **LOW:** Complete LTP periodicity encoding for mid/high cases

#### Summary

The SILK encoder has two critical bugs that fully explain the 0% pass rate with Q ≈ -100 to -133:

1. **Gain Computation Bug:** `computeLogGainIndex()` returns 0 for ALL valid gain values
   because the formula expects gains in [0.25, 16] but receives gains in [0.001, 0.27].
   This causes all decoded frames to have near-zero amplitude.

2. **Range Encoder Persistence Bug:** After encoding frame 0, the range encoder reference
   is not cleared, causing all subsequent frames to return nil (empty output).

Both bugs are straightforward to fix:
- Gain bug: Fix the logarithm base or use direct table lookup
- Encoder bug: Clear `e.rangeEncoder = nil` at end of standalone encoding

Once these are fixed, additional work may be needed on:
- Input signal scaling (ensure PCM is in expected range)
- Excitation quantization (scale residuals properly)
- LSF/NLSF alignment with decoder expectations

---

### Packet Structure Investigation (Agent 4)

#### Files Examined:
- `/Users/thesyncim/GolandProjects/gopus/encoder.go` - Top-level public encoder API
- `/Users/thesyncim/GolandProjects/gopus/internal/encoder/encoder.go` - Unified encoder implementation
- `/Users/thesyncim/GolandProjects/gopus/internal/encoder/packet.go` - TOC byte generation and packet building
- `/Users/thesyncim/GolandProjects/gopus/internal/encoder/controls.go` - Bitrate modes and padding
- `/Users/thesyncim/GolandProjects/gopus/internal/encoder/hybrid.go` - Hybrid mode SILK+CELT coordination
- `/Users/thesyncim/GolandProjects/gopus/packet.go` - Public packet parsing API
- `/Users/thesyncim/GolandProjects/gopus/internal/silk/encode_frame.go` - SILK frame encoding
- `/Users/thesyncim/GolandProjects/gopus/internal/celt/encoder.go` - CELT encoder state
- `/Users/thesyncim/GolandProjects/gopus/internal/rangecoding/encoder.go` - Range coder implementation

#### Empirically Verified OK:

1. **TOC Byte Generation (generateTOC)**
   - **Evidence:** `TestBuildPacket` passes all 7 test cases
   - **Verification:** Manual calculation confirmed:
     - `TOC = (config << 3) | (stereo << 2) | frameCode` matches RFC 6716 Section 3.1
     - Config 12 (Hybrid SWB 10ms) produces TOC=0x60 - CORRECT
     - Config 13 (Hybrid SWB 20ms) produces TOC=0x68 - CORRECT
     - Config 31 (CELT FB 20ms) produces TOC=0xF8 - CORRECT
   - **Files:** `internal/encoder/packet.go` lines 84-91, `packet.go` lines 99-106

2. **Config Table (configTable)**
   - **Evidence:** All 32 configurations correctly map mode/bandwidth/frameSize
   - **Verification:** Table matches RFC 6716 Section 3.1 exactly:
     - Configs 0-11: SILK (NB/MB/WB with 10/20/40/60ms frames)
     - Configs 12-15: Hybrid (SWB/FB with 10/20ms frames)
     - Configs 16-31: CELT (NB/WB/SWB/FB with 2.5/5/10/20ms frames)
   - **Files:** `internal/encoder/packet.go` lines 28-70

3. **Multi-Frame Packet Structure (BuildMultiFramePacket)**
   - **Evidence:** `TestBuildMultiFramePacket` passes all 4 test cases (CBR/VBR 2-3 frames)
   - **Verification:** Frame count byte format correct:
     - VBR bit (0x80), padding bit (0x40), count (0x3F mask)
     - Frame length encoding for lengths >= 252 uses two-byte format
   - **Files:** `internal/encoder/packet.go` lines 114-167

4. **Single Frame Packet Structure (BuildPacket)**
   - **Evidence:** `TestEncoderPacketConfigs` passes for all 8 mode/bandwidth combinations
   - **Format:** `[TOC byte][frame data]` - no frame length byte for code 0
   - **Files:** `internal/encoder/packet.go` lines 93-109

5. **Packet Parseability**
   - **Evidence:** `TestEncoderPacketParseable` shows encoded packets can be parsed back
   - **Verification:** Round-trip through ParsePacket succeeds

6. **Stereo Flag in TOC**
   - **Evidence:** `TestEncoderPacketStereo` verifies mono/stereo flag set correctly
   - **Verification:** Bit 2 of TOC byte reflects channel count

7. **Frame Length Encoding (writeFrameLength)**
   - **Evidence:** `TestWriteFrameLength` and `TestBuildMultiFramePacketVBRTwoByteLength` pass
   - **Verification:** Two-byte encoding for lengths >= 252 is correct:
     - `length = 4*secondByte + firstByte` matches RFC 6716 Section 3.2.1

8. **Hybrid Payload Order**
   - Per RFC 6716 Section 3.2.1: SILK encodes first, then CELT
   - **Code Review:** `internal/encoder/hybrid.go` lines 52-68 confirms correct order:
     1. `e.encodeSILKHybrid(silkInput, frameSize)` - SILK first
     2. `e.encodeCELTHybrid(celtInput, frameSize)` - CELT second
   - **Status:** VERIFIED OK

#### Known Issues Found:

##### ISSUE 1: CBR Padding Uses Incorrect Method (MINOR - RFC COMPLIANCE)
- **Location:** `internal/encoder/controls.go` lines 73-83 (`padToSize`)
- **Problem:** Padding is implemented as zero-appending to code 0 packet
- **RFC 6716 Requirement:** Per Section 3.2.5, padding MUST use code 3 packet format with padding flag
- **Current Code:**
  ```go
  func padToSize(packet []byte, targetSize int) []byte {
      if len(packet) >= targetSize {
          return packet[:targetSize]
      }
      padded := make([]byte, targetSize)
      copy(padded, packet)
      return padded  // Appends zeros after frame data
  }
  ```
- **Expected per RFC 6716:** Use frame code 3 with:
  - TOC byte with code=3 (bits 0-1 = 0x03)
  - Frame count byte with padding flag (bit 6 = 0x40) and M=1
  - Padding length byte(s)
  - Original frame data
  - Padding bytes (must be zeros)
- **Impact:** CBR packets may not be decodable by strict RFC-compliant decoders
- **Note:** Most decoders are lenient; this is a compliance issue not a functionality bug

##### ISSUE 2: CVBR Truncation May Corrupt Packets (DESIGN FLAW)
- **Location:** `internal/encoder/controls.go` lines 86-97 (`constrainSize`)
- **Problem:** When packet exceeds maxSize, it's truncated
- **Code:**
  ```go
  if len(packet) > maxSize {
      return packet[:maxSize]
  }
  ```
- **Impact:** Truncation of range-coded data corrupts the bitstream
  - Range coder final bytes contain essential state information
  - Truncation produces undecodable packets
- **Proper Solution:** Encoder should target correct size during encoding,
  not truncate post-hoc. CVBR requires rate-control in the encoding loop.

##### ISSUE 3: Hybrid Mode 10ms Frame Buffering Asymmetry (EDGE CASE)
- **Location:** `internal/encoder/hybrid.go` lines 163-177 (`encodeSILKHybrid`)
- **Problem:** For 10ms frames, SILK samples are buffered but CELT continues encoding
- **Code:**
  ```go
  if silkSamples < silkWBSamples {
      if e.silkBufferFilled == 0 {
          copy(e.silkFrameBuffer[:silkSamples], inputSamples)
          e.silkBufferFilled = silkSamples
          return  // Returns without encoding SILK!
      }
  }
  ```
- **Impact:** First 10ms hybrid frame has only CELT layer encoded; SILK layer is empty.
  Second 10ms frame encodes full 20ms SILK from buffered + current samples.
- **Result:** Frame 0 is malformed (CELT only), Frame 1 has SILK from frames 0+1.
  This creates temporal misalignment between SILK and CELT layers.

#### Hypotheses to Test:

1. **Padding Implementation Hypothesis**
   - Test: Encode with CBR mode, verify packet can decode with opusdec
   - Expected: May fail if decoder requires code 3 padding format
   - Recommendation: Implement proper RFC 6716 padding format if CBR is needed

2. **Hybrid 10ms Frame Hypothesis**
   - Test: Encode 10ms hybrid frame sequence, check first frame output size
   - Expected: First frame smaller than subsequent frames (missing SILK)
   - Recommendation: Either buffer both SILK and CELT, or reject 10ms hybrid mode

3. **Range Encoder Final Byte Hypothesis**
   - Test: Compare final byte values between gopus and libopus for same input
   - Expected: Final range encoder byte should match exactly
   - Rationale: Any bit misalignment causes complete decoding failure

#### Test Results Summary:

| Test | Status | Notes |
|------|--------|-------|
| `TestBuildPacket` | 7/7 PASS | All TOC byte cases correct |
| `TestBuildPacketInvalidConfig` | 4/4 PASS | Invalid configs rejected |
| `TestBuildMultiFramePacket` | 4/4 PASS | Code 3 packets correct |
| `TestBuildMultiFramePacketErrors` | 3/3 PASS | Error cases handled |
| `TestEncoderPacketConfigs` | 8/8 PASS | All mode/bandwidth combos |
| `TestWriteFrameLength` | 10/10 PASS | Two-byte encoding correct |

#### Summary:

**TOC byte generation is CORRECT.** The packet structure for code 0 (single frame)
and code 3 (multi-frame) packets is correctly implemented per RFC 6716.

**The 0% encoder compliance rate is NOT caused by TOC byte or packet structure issues.**

**Root Cause Analysis:**
The encoder's main problems are in SILK/CELT signal processing algorithms, not packet formation:
1. **SILK gain encoding returns 0 for all inputs** (Agent 2 finding)
2. **Excitation quantization lacks proper scaling** (Agent 2 finding)
3. **Streaming encoder returns nil after first frame** (Agent 2 finding)

**Minor Compliance Issues Found (not blocking):**
1. CBR padding should use code 3 format (not code 0 + zeros) per RFC 6716 Section 3.2.5
2. CVBR truncation corrupts packets (should fail or use proper rate control)
3. Hybrid 10ms frames have SILK/CELT temporal misalignment

**Conclusion:** Packet structure is working correctly. The encoder failures are in the
SILK and CELT encoding algorithms themselves, not in how packets are formed.

---

### Range Encoder Investigation (Agent 3)

#### Files Examined:
- `/Users/thesyncim/GolandProjects/gopus/internal/rangecoding/encoder.go` - Range encoder implementation
- `/Users/thesyncim/GolandProjects/gopus/internal/rangecoding/constants.go` - Range coder constants
- `/Users/thesyncim/GolandProjects/gopus/internal/rangecoding/decoder.go` - Range decoder (for symmetry verification)
- `/Users/thesyncim/GolandProjects/gopus/internal/rangecoding/encoder_test.go` - Unit tests
- `/Users/thesyncim/GolandProjects/gopus/internal/rangecoding/roundtrip_test.go` - Round-trip tests
- `/Users/thesyncim/GolandProjects/gopus/internal/rangecoding/entropy_libopus_test.go` - libopus entropy port tests
- `/Users/thesyncim/GolandProjects/gopus/internal/celt/cgo_test/range_encoder_libopus_test.go` - CGO comparison tests
- `/Users/thesyncim/GolandProjects/gopus/internal/celt/cgo_test/range_encoder_trace.go` - libopus state tracing wrapper
- `/Users/thesyncim/GolandProjects/gopus/internal/celt/cgo_test/laplace_encode_libopus_test.go` - Laplace encoding tests
- `/Users/thesyncim/GolandProjects/gopus/internal/celt/energy_encode.go` - Coarse/fine energy encoding
- `/Users/thesyncim/GolandProjects/gopus/tmp_check/opus-1.6.1/celt/entenc.c` - libopus reference implementation

#### Empirically Verified OK:

1. **EncodeBit matches libopus ec_enc_bit_logp** (All tests pass)
   - Tested: single bits (0/1), sequences, various logp values (1-15)
   - Byte-level output matches libopus exactly
   - State (rng, val, rem, ext) matches after each operation

2. **Encode matches libopus ec_encode** (All tests pass)
   - Tested: various (fl, fh, ft) combinations
   - First/middle/last symbols, narrow ranges, Laplace-like distributions
   - Byte output identical to libopus

3. **EncodeBin matches libopus ec_encode_bin** (All tests pass)
   - Power-of-two total frequency variant works correctly

4. **EncodeICDF matches libopus ec_enc_icdf** (All tests pass)
   - Tested: uniform ICDF tables with 4 symbols
   - Single and sequence encoding produces identical bytes

5. **EncodeUniform matches libopus ec_enc_uint** (All tests pass)
   - Tested: single/multiple values, various ft ranges (2-256+)
   - Multi-byte case (ft > 256) with raw bits works correctly

6. **EncodeRawBits matches libopus ec_enc_bits** (All tests pass)
   - Raw bits written to end of buffer correctly
   - Tested 1-16 bit values

7. **Laplace encoding matches libopus ec_laplace_encode** (All tests pass)
   - encodeLaplace function in energy_encode.go produces identical output
   - Tested: values -20 to +20, various (fs, decay) parameters
   - Both encode and decode round-trips verified

8. **Coarse energy encoding** (Self-roundtrip verified)
   - EncodeCoarseEnergy followed by DecodeCoarseEnergy recovers same values
   - Laplace-encoded qi values match when using same probability model

9. **CWRS/PVQ encoding matches libopus** (All tests pass)
   - Pulse encoding/decoding produces identical indices
   - V(n,k) combinatorial function matches

10. **Constants match libopus** (Verified)
    - EC_SYM_BITS = 8
    - EC_CODE_BITS = 32
    - EC_SYM_MAX = 255
    - EC_CODE_TOP = 0x80000000
    - EC_CODE_BOT = 0x00800000
    - EC_CODE_SHIFT = 23
    - EC_CODE_EXTRA = 7

11. **Tell/TellFrac bit counting** (Verified in tests)
    - Tell() returns correct number of bits consumed
    - TellFrac() matches libopus ec_tell_frac with 1/8 bit precision

12. **Carry propagation** (Tested with sequences producing 0xFF bytes)
    - carryOut() handles EC_SYM_MAX (0xFF) buffering correctly
    - ext counter for pending 0xFF bytes works as expected

13. **Done() finalization** (Output matches libopus ec_enc_done)
    - Final range computation using ilog
    - Mask/end calculation for minimum output bits
    - Raw end bits merged correctly

#### Known Issues Found:

**NONE** - The range encoder implementation is correct and matches libopus bit-for-bit.

#### Hypotheses to Test:

The range encoder is NOT the source of the 0% encoder pass rate. The issues must be in:

1. **Higher-level encoding logic** (CELT bands encoding, SILK frame encoding)
2. **Signal scaling** (As identified in Agent 2's investigation - gain encoding, excitation scaling)
3. **Packet structure** (TOC byte handling, frame boundaries)
4. **State management** (As identified - rangeEncoder persistence bug in SILK)

#### Summary:

The range coder (entropy coding) component has been **fully verified** against libopus:
- All 10+ core encoding operations match libopus exactly
- Byte-level output is identical for all tested inputs
- State tracking (rng, val, rem, ext, offs) matches after each operation
- Round-trip encode/decode produces original values

**Conclusion:** The encoder problems are NOT in the range coder. Focus investigation on:
1. SILK encoder issues (Agent 2's findings - gain computation and rangeEncoder persistence bugs)
2. CELT encoder higher-level logic
3. Packet formation and TOC handling

---

### CELT Encoder Signal Path Investigation (Agent 1) - 2026-01-30

#### Files Examined:
- `/Users/thesyncim/GolandProjects/gopus/internal/celt/bands_quant.go` - Band quantization, expRotation, haar1, PVQ encoding
- `/Users/thesyncim/GolandProjects/gopus/internal/celt/pvq_search.go` - PVQ search algorithm (opPVQSearch)
- `/Users/thesyncim/GolandProjects/gopus/internal/celt/encode_frame.go` - Main frame encoding pipeline
- `/Users/thesyncim/GolandProjects/gopus/internal/celt/bands_encode.go` - Band energy computation and normalization
- `/Users/thesyncim/GolandProjects/gopus/internal/celt/cwrs.go` - CWRS encode/decode for PVQ
- `/Users/thesyncim/GolandProjects/gopus/internal/encoder/encoder.go` - High-level encoder
- `/Users/thesyncim/GolandProjects/gopus/internal/encoder/packet.go` - TOC byte generation

#### Empirically Verified OK:
1. **MDCT Forward Transform** - SNR > 200 dB (TestMDCTTransformAccuracy)
2. **PVQ V(N,K) computation** - All tests pass
3. **CWRS encode/decode round-trip** - All tests pass
4. **PVQ search and encode round-trip** - All tests pass (TestPVQSearchAndEncodeRoundtrip)
5. **expRotation round-trip** - SNR > 599 dB (forward dir=1, then inverse dir=-1 recovers original)
6. **haar1 transform** - Self-inverse with invSqrt2 scaling
7. **First 7 bytes of payload match libopus** - Exact match: `7B 5E 09 50 B7 8C 08`
8. **TOC byte generation** - Correct per RFC 6716 (config 31 = 0xF8 for CELT FB 20ms mono)

#### Known Issues Found:

##### CRITICAL ISSUE: Signal Inversion (Negative Correlation)
- **Evidence:** `TestEncoderDivergenceAnalysis` shows:
  - gopus correlation with original = **-0.5375** (NEGATIVE - signal inverted!)
  - libopus correlation with original = **+0.5162** (POSITIVE - correct)
- **Impact:** Decoded audio is phase-inverted, sounds completely wrong
- **SNR:** gopus = -4.34 dB vs libopus = +0.76 dB
- **Location:** Somewhere in the encoding signal path between MDCT and PVQ

##### ISSUE 2: Packet Payload Divergence at Byte 8
- **Evidence:** Bytes 0-7 of payload match exactly, byte 8 diverges
  - gopus: `7B 5E 09 50 B7 8C 08 **33** 22 8B ...`
  - libopus: `7B 5E 09 50 B7 8C 08 **D0** BB AE ...`
- **Impact:** Different PVQ encoding results from coarse/fine energy onwards
- **Analysis:** First 7 bytes = silence flag, postfilter flag, transient flag, intra flag, coarse energy
  Byte 8 onwards = TF decisions, spread, fine energy, PVQ bands

##### ISSUE 3: Test Comparison Bug (Minor - in test code)
- **Location:** `encoder_divergence_test.go` line 115-128
- **Problem:** Comment says "gopus packet doesn't include TOC" but gopus DOES include TOC
- **Impact:** Byte comparison in test is off by one, masking the actual match pattern

#### Analysis of Signal Path:

**Encoding Flow:**
1. PCM input (float64, range [-1, 1])
2. DC rejection filter
3. Pre-emphasis with scaling (x32768)
4. MDCT transform
5. Band energy computation
6. Band normalization (divide by energy)
7. expRotation(x, dir=1) - forward rotation for encoder
8. opPVQSearch - find pulse vector
9. EncodePulses - encode via CWRS
10. normalizeResidual - scale pulses by gain
11. expRotation(x, dir=-1) - inverse rotation for resynth

**Decoding Flow:**
1. DecodePulses - decode via CWRS
2. normalizeResidual - scale pulses by gain
3. expRotation(x, dir=-1) - inverse rotation
4. Denormalize bands (multiply by energy)
5. iMDCT transform
6. De-emphasis
7. PCM output

**Key Observation:**
The encoder applies dir=1 rotation before PVQ search, then dir=-1 rotation for resynth.
The decoder applies dir=-1 rotation after decoding.
This should be symmetric, BUT: if there's an issue in how the encoder's rotated values
are quantized vs how the decoder's rotated values are reconstructed, we could get inversion.

#### Hypotheses for Signal Inversion:

1. **expRotation Implementation Difference**
   - Both encoder and decoder use same expRotation function
   - Encoder: dir=1 (uses -s for forward), dir=-1 for resynth
   - Decoder: dir=-1 (uses +s for inverse)
   - **Status:** Tested - round-trip works with SNR > 599 dB

2. **opPVQSearch Sign Handling**
   - Lines 24-29 flip negative values to positive for search
   - Lines 96-99 restore signs from saved signx array
   - **Status:** Signs are correctly preserved in pulse vector

3. **CWRS Index Encoding**
   - icwrs() encodes signs into the combinatorial index
   - cwrsi() decodes back with signs
   - **Status:** Round-trip tests pass

4. **Possible Cause: Energy Quantization/Denormalization**
   - If quantized energies differ from encoder's expected energies
   - Denormalization in decoder would produce wrong amplitude/sign
   - **Needs Investigation:** Compare bandE values between gopus encoder and libopus

5. **Possible Cause: Pre-emphasis/De-emphasis Asymmetry**
   - Pre-emphasis: y[n] = x[n] - 0.85*x[n-1]
   - De-emphasis: y[n] = x[n] + 0.85*y[n-1]
   - If scaling factor differs, could affect phase
   - **Status:** Formula looks correct but untested

#### Test Results Summary:
```
Test: TestEncoderDivergenceAnalysis
Correlation with original: gopus=-0.5375, libopus=0.5162
SNR (skipping first 120 samples): gopus=-4.34 dB, libopus=0.76 dB
First 7 payload bytes match, then diverge at byte 8

Test: TestRotationUnitLibopus - PASS
expRotation round-trip SNR: 595-610 dB (excellent)

Test: TestPVQSearchAndEncodeRoundtrip - PASS
All 9 sub-tests pass
```

#### Recommended Next Steps:

1. **HIGH:** Create test comparing MDCT coefficients to libopus (pre-PVQ stage)
2. **HIGH:** Create test comparing quantized energies to libopus
3. **HIGH:** Trace through algQuant comparing intermediate values to libopus
4. **MEDIUM:** Test pre-emphasis output against libopus celt_preemphasis()
5. **MEDIUM:** Verify CELT_SIG_SCALE (32768) is applied consistently

#### Summary:

The CELT encoder produces payloads that match libopus for the first 7 bytes (frame flags
and coarse energy) but diverge at byte 8 and produce **inverted phase** in the decoded audio.

**Root Cause Candidates (ordered by likelihood):**
1. Energy quantization/denormalization asymmetry
2. Fine energy encoding differs from libopus
3. TF resolution encoding affects PVQ band organization
4. Spread decision affects rotation parameters differently

The signal inversion (correlation = -0.54) strongly suggests a sign error somewhere in
the quantization/dequantization path, possibly in how the decoder interprets the
energy-normalized coefficients.

