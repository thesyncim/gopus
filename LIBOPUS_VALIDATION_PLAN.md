# Libopus Validation Plan

Reference baseline: `tmp_check/opus-1.6.1` (libopus 1.6.1). This is treated as
the ground truth for behavior (including the libwebrtc integration path).

Legend: [x] validated vs libopus, [!] mismatch/suspect, [ ] pending

## Acceptance Criteria
- Reference build: libopus float build from `tmp_check/opus-1.6.1` (same inputs,
  same config, same packet loss pattern).
- Parity scope: Decoder parity is mandatory for libwebrtc usage (float, 48 kHz,
  2 channels, mixed frame sizes, PLC and DTX paths). Encoder parity is required
  for deterministic modes; otherwise decoder parity still must hold.
- Initial state parity: decoder/encoder reset states must match libopus (e.g.
  prevLogE/prevLogE2 = -28, postfilter state zeroed, PLC buffers cleared).
- Bit-exact components (range coder, tables, window): exact value match or
  integer bit-for-bit match.
- Transform parity (MDCT/IMDCT, DFT): max abs diff <= 1e-6 and correlation
  >= 0.999999 vs libopus float reference.
- Decoder parity: per-frame PCM max abs diff <= 1e-6 and SNR >= 90 dB vs libopus
  float output; compliance vectors must pass libopus thresholds and match SNR
  within 0.1 dB where we track it. Per-packet compliance threshold is 40 dB
  minimum SNR unless libopus allows lower for a known corner case (document it).
- Encoder parity: for deterministic settings, encoded payload bytes and packet
  sizes must match libopus; otherwise decoded PCM must still meet decoder parity
  (no regressions allowed).

## Shared / Utilities
- [x] Vorbis window (overlap-based) values match `static_modes_float.h` and
  `celt/modes.c` generation formula.
- [x] IMDCT + TDAC overlap for overlap=120 matches libopus output.
- [x] MDCT forward (analysis) for short frame sizes (120/240) vs libopus -> `internal/celt/cgo_test/mdct_libopus_test.go` (SNR > 138 dB)
- [x] Range coder (entdec/entenc) bit-exactness vs libopus -> `internal/rangecoding/entropy_libopus_test.go`
- [x] Tables (bands, allocation, pulse cache) parity vs libopus -> `internal/celt/libopus_tables_test.go`

## CELT Decoder
- [!] PVQ band decode / quant_bands (remaining divergence after RNG fix; see testvector07).
  - Previous suspicion about `expRotation()` mismatch is not confirmed in float build;
    revisit only if later traces point to PVQ/rotation.
- [x] Stereo coupling (intensity + mid/side + sign handling): Code review shows N=2 stereo path
  matches libopus (quantBandStereo lines 674-712 vs bands.c lines 1454-1507).
- [x] Energy state layout fix: Fixed updateLogE layout mismatch in decodeMonoPacketToStereo
  (stereoEnergies now uses [c*end+band] layout as expected by updateLogE).
- [ ] Energy decode (coarse/fine/amp) vs libopus.
- [x] Time/frequency resolution (TF) decode vs libopus -> `internal/celt/tf_libopus_test.go`
- [!] **CRITICAL** Transient short-block handling (LM/B/interleave) vs libopus:
  - **Root cause identified**: `synthesizeChannelWithOverlap()` in synthesis.go (lines 144-175)
    uses per-block overlap semantics while libopus uses stride-based global overlap.
  - Go incorrectly de-interleaves coefficients: `shortCoeffs[i] = coeffs[b + i*shortBlocks]`
  - Problem: `quantAllBandsDecode()` outputs sequential coefficients (NOT interleaved)
  - libopus expects stride B for IMDCT: `clt_mdct_backward(&freq[b], ..., stride=B)`
  - Go IMDCT has no stride parameter support
  - Impact: 30.8% bad packet rate for 120-sample stereo frames in testvector07
  - Reference: tmp_check/opus-1.6.1/celt/celt_decoder.c lines 448-512
- [!] Anti-collapse vs libopus -> `internal/celt/anticollapse_libopus_test.go`
  - Algorithm is correct (unit tests pass: threshold, amplitude, PRNG, masking)
  - Integration tests fail due to test isolation issues (cannot isolate from PVQ/energy/synthesis)
  - Potential RNG state mutation issue: seed is not returned/updated after antiCollapse()
  - Reference: tmp_check/opus-1.6.1/celt/bands.c lines 261-355
- [x] Decoder init parity: `NewDecoder` now calls `Reset()` to initialize
  prevLogE/prevLogE2 to -28 and clear buffers (matches libopus). This fixes
  early-frame anti-collapse noise injection.
- [x] RNG / uncoded-band noise parity: seed now matches libopus (reset=0, update from range decoder),
  and noise uses signed shift before renormalization. This fixes early divergence in testvector07.
- [x] Postfilter (comb filter) vs libopus -> `internal/celt/cgo_test/postfilter_libopus_test.go`
  - Tests now pass after aligning libopus wrapper to in-place comb_filter usage.
- [!] Overlap-add synthesis vs libopus:
  - Stereo overlap buffer management issue when transitioning between mono and stereo frames
  - Go uses 240-sample split buffer [0:120] (L) + [120:240] (R)
  - libopus uses contiguous per-channel buffers with OPUS_MOVE shifting
  - After mono decode, overlapBuffer[120:240] contains stale stereo data
  - Causes divergence at sample 68 (within 120-sample overlap region)
  - Reference: tmp_check/opus-1.6.1/celt/celt_decoder.c lines 1279-1280, 1521
- [ ] Silence / packet loss behavior vs libopus.
- [ ] PLC behavior vs libopus.

## CELT Encoder
- [ ] Transient detection / short-block selection vs libopus.
- [ ] TF analysis vs libopus.
- [ ] Energy quantization (coarse/fine) vs libopus.
- [ ] PVQ encoding / pulse allocation / stereo coupling vs libopus.
- [ ] Postfilter control vs libopus.
- [ ] MDCT forward short-overlap path vs libopus.

## SILK / Hybrid (if in scope)
- [x] Resampler timing: Fixed and validated (lag=0, correlation=1.0 at all positions).
  - sMid buffering correctly implemented
  - First non-zero sample position matches libopus
- [!] SILK decoder vs libopus -> Tables and unit tests pass; integration tests fail (Q=-137).
  - **Root cause identified**: Divergence in frame 2 (3rd 20ms frame), subframe k=2, native sample ~111
  - Packet 0: Perfect match (SNR=+∞)
  - Packet 1: First 2586/2880 samples match (SNR=999 dB for first 5 windows)
  - Divergence at sample 2587 (48kHz) = native sample 431 in 60ms packet
  - Mapping: Frame 2, subframe k=2 (samples 80-119), offset 31
  - This is exactly at the NLSF interpolation boundary (k=2 && interpFlag)
  - Potential issues:
    1. LPC analysis filter rewhitening at k=2
    2. sLTP_Q15 buffer population after rewhitening
    3. State accumulated from frames 0-1 not matching libopus
    4. outBuf state management between frames
- [ ] SILK encoder vs libopus.
- [ ] Hybrid switching (CELT <-> SILK) vs libopus.

## Test Vectors / Compliance
- [x] Test harness fix: Now uses single stereo decoder for all packets (like libopus).
  - Mono->stereo transition at packet 2128 works correctly (SNR=71 dB)
  - Sample counts match 12/12 test vectors
- [x] `testvector01` PASS (Q=68.44): Pure CELT, stereo, all frame sizes
- [x] `testvector11` PASS (Q=69.61): Pure CELT, stereo, 20ms frames only
- [!] `testvector07` FAIL (Q=-40.30): CELT mixed mono/stereo
  - First divergence now at packet 62 (mono, 960-sample frame), SNR ≈ 36.2 dB.
  - Earlier divergence at packet 4 fixed by RNG/uncoded-band noise parity.
  - Remaining issues likely in overlap/short-block handling or other CELT synthesis paths.
  - Debug test: `internal/testvectors/packet31_debug_test.go` traces packet 31 pipeline
- [!] `testvector02/03/04` FAIL (Q~-137): Pure SILK - k=2 interpolation divergence
  - Divergence at packet 1, sample 2587 (native sample 111 in frame 2, subframe k=2)
  - LPC state, outBuf management, or LTP buffer population issue
- [!] `testvector05/06` FAIL (Q~-149): Pure Hybrid - SILK layer issues
- [!] `testvector08/09` FAIL (Q~-85 to -93): Mixed SILK+CELT
- [!] `testvector10` FAIL (Q=-119): Mixed CELT+Hybrid
- [!] `testvector12` FAIL (Q=-157): Mixed SILK+Hybrid

## C Tests to Port (Basic Coverage)
- [x] `celt/tests/test_unit_mdct.c` (MDCT/IMDCT sanity) -> `internal/celt/cgo_test/mdct_libopus_test.go` (SNR > 138 dB vs libopus)
- [x] `celt/tests/test_unit_dft.c` (DFT correctness) -> `internal/celt/dft_unit_test.go` (SNR > 250 dB)
- [x] `celt/tests/test_unit_rotation.c` (rotation/inversion) -> `internal/celt/rotation_unit_test.go`
- [x] `celt/tests/test_unit_cwrs32.c` (CWRS/pulse enumeration) -> `internal/celt/cwrs_unit_test.go`
- [x] `celt/tests/test_unit_entropy.c` (range coder parity) -> `internal/rangecoding/entropy_libopus_test.go`
- [x] `celt/tests/test_unit_laplace.c` (Laplace coder) -> `internal/celt/laplace_unit_test.go` (10000 values roundtrip pass)
- [ ] `tests/test_opus_decode.c` (decoder API + edge cases)
- [ ] `tests/test_opus_encode.c` (encoder API + constraints)
- [ ] `celt/tests/test_unit_mathops.c` (math ops sanity)
- [ ] `celt/tests/test_unit_pitch.c` (pitch/xcorr basics)

## Component Checklist (Libopus/Libwebrtc Parity)
- [x] Bitstream parser + range coder (entropy)
- [x] Modes/tables (bands/alloc/pulse cache)
- [x] Window + MDCT/IMDCT + TDAC overlap
- [ ] PVQ (rotation + pulse allocation + resynthesis)
- [ ] Energy decode (coarse/fine/amp)
- [ ] Stereo coupling (validate with testvectors, not just code review)
- [x] Postfilter/comb filter (in-place behavior, state transitions)
- [ ] Overlap/add buffer management (mono↔stereo transitions)
- [ ] PLC + loss concealment (both CELT and SILK)
- [ ] DTX/silence handling (background energy updates)

## CGO Comparison Tests (internal/celt/cgo_test/)
- [x] `decode_libopus_test.go` - Full packet decode comparison vs libopus:
  - TestDecodePacketVsLibopus: Per-packet SNR comparison
  - TestDecodeDivergencePoint: Finds first divergent packet
  - TestDecodeAllPacketsSNR: Overall stream quality metrics
  - TestAnalyzeSNRByFrameSize: SNR breakdown by frame size and stereo flag
  - TestAnalyzeWorstPacket: Detailed sample-level comparison for worst packet
  - TestAnalyzeBadPacketPattern: Categorizes bad packets by frame size/stereo
- [x] `energy_libopus_test.go` - Laplace decode, range state, ICDF vs libopus
- [!] `postfilter_libopus_test.go` - Comb filter / postfilter comparison vs libopus:
  - TestCombFilterVsLibopus: Basic comb filter output comparison (FAILING - divergence detected)
  - TestCombFilterCrossfadeVsLibopus: Parameter transition crossfade (FAILING)
  - TestVorbisWindowVsLibopus: Window computation (PASSING)
  - TestCombFilterZeroGainVsLibopus: Zero gain early return (PASSING)
  - TestCombFilterPeriodClampingVsLibopus: Period clamping behavior (FAILING)
  - TestCombFilterAllTapsetsVsLibopus: All tapsets and gains (FAILING - scales with gain)
  - TestPostfilterParameterDecodeVsLibopus: Bitstream decode validation (PASSING)
  - TestPostfilterStateTransitionVsLibopus: State persistence across frames (FAILING)
  - TestCombFilterEdgeCasesVsLibopus: Edge case coverage (PARTIAL - some pass)
  - TestCombFilterGainTableVsLibopus: Gain table verification (PASSING)
  - TestPostfilterGainComputationVsLibopus: Gain from qg (PASSING)
  - TestPostfilterPeriodComputationVsLibopus: Period from octave/bits (PASSING)
  - TestCombFilterImpulseResponseVsLibopus: Impulse response match (PASSING)
- [x] `mdct_libopus_test.go` - MDCT/IMDCT transform comparison vs libopus:
  - TestMDCT_LibopusForward: Forward MDCT with CELT sizes (PASSING)
  - TestMDCT_LibopusInverse: Inverse MDCT with CELT sizes (PASSING)
  - TestMDCT_LibopusRoundTrip: Round-trip correlation test (PASSING - corr > 0.9)
  - TestMDCT_GoVsLibopusIMDCT: Go vs libopus IMDCT (PASSING - SNR > 138 dB)
  - TestMDCT_GoVsLibopusMDCT: Go vs libopus MDCT (PASSING - SNR > 138 dB)
  - TestMDCT_ReferenceFormula: Reference formula self-check (PASSING - SNR = Inf)
  - TestMDCT_CELTSizes: All CELT frame sizes 120/240/480/960 (PASSING)
  - TestMDCT_WindowValues: Vorbis window match (PASSING - diff < 3.3e-8)
- [x] `libopus_wrapper.go` - CGO bindings to libopus for comparison testing
- [x] `packet31_debug_test.go` - Detailed packet 31 debugging (disabled, use internal/testvectors version)

## Debug Tests (internal/testvectors/)
- [x] `packet31_debug_test.go` - Detailed analysis of packet 31 divergence:
  - TestPacket31GopusDetailedAnalysis: Basic output analysis
  - TestPacket31BitExactAnalysis: Bit-level frame header decoding trace
  - TestPacket31EnergyDecode: Energy state before/after packet 31
  - TestPacket31NeighboringPackets: Analysis of packets 28-35
  - TestPacket31DivergenceWindow: Focus on samples 60-80
  - TestPacket31CompareWithReference: Sample-by-sample comparison vs .dec file
  - TestPacket31SurroundingPacketSNR: SNR tracking for packets 25-40

## Priority Fixes (testvector07)

### Fix #1: Short-Block IMDCT Synthesis - VALIDATED CORRECT
**File**: `internal/celt/synthesis.go` (lines 144-175)
**Status**: ✓ CORRECT - The de-interleaving IS correct!

**Verification**:
- libopus bands.c line 1345: `deinterleave_hadamard()` is only for encoding
- libopus bands.c line ~640 (in quantBand): For decoding with `resynth=true`, calls `interleave_hadamard()`
- Go bands_quant.go line 636: Also calls `interleaveHadamard()` when `ctx.resynth` is true
- Therefore coefficients from quantAllBandsDecode ARE interleaved: `coef[b + i*B]`
- The synthesis de-interleaving `idx := b + i*shortBlocks` is CORRECT

**Previous assumption was wrong**: The validation plan incorrectly stated coefficients were sequential.

### Fix #2: RNG / Uncoded-Band Noise Parity - DONE
**Files**: `internal/celt/decoder.go`, `internal/celt/bands_quant.go`
**Status**: ✓ FIXED

**Details**:
- Decoder RNG now matches libopus (reset to 0, updated from range decoder state).
- Uncoded-band noise uses signed shift on seed (matches libopus).
- Result: packet-4 divergence resolved; band 12+ coefficients now match libopus.

### Fix #3: expRotation() Coefficient Computation - UNCONFIRMED
**File**: `internal/celt/bands_quant.go` (lines 145-177)
**Status**: TBD (not confirmed as root cause in float build)
**Note**: Revisit only if later traces indicate PVQ/rotation mismatch.

### Fix #4: Overlap Buffer State Management
**File**: `internal/celt/synthesis.go`, `internal/celt/decoder.go`
**Problem**: Stereo overlap buffer corrupted on mono→stereo transitions
**Impact**: Divergence at sample 68 (within overlap region)

**Fix**: Explicitly manage overlap buffer state transitions when switching channel counts

## Priority Fixes (testvector02/03/04 - SILK)

### Fix #5: SILK Inter-Packet State Reset - ROOT CAUSE IDENTIFIED
**File**: `internal/silk/libopus_decode.go` (silkDecoderSetFs function, lines 5-56)
**Status**: Root cause identified, fix pending

**Root Cause**:
State buffers (`outBuf`, `sLPCQ14Buf`) are only reset when sample rate CHANGES.
When packet 1 arrives with SAME sample rate as packet 0, buffers retain stale state.
At frame 2, subframe k=2 rewhitening, stale `outBuf` data causes LTP prediction divergence.

**Evidence**:
- Packet 0: SNR=999 dB (perfect - buffers zeroed on init)
- Packet 1: SNR=16.6 dB (divergence - buffers NOT reset)
- Packet 3: SNR=999 dB (statistical convergence, not correctness)

**The Bug** (libopus_decode.go line 24):
```go
if st.fsKHz != fsKHz {  // FALSE when same rate - buffers NOT reset!
    st.firstFrameAfterReset = true
    for i := range st.outBuf { st.outBuf[i] = 0 }
    for i := range st.sLPCQ14Buf { st.sLPCQ14Buf[i] = 0 }
}
```

**Fix Required**:
Investigate libopus `dec_API.c` to understand exact per-packet reset semantics.
May need to reset `outBuf`/`sLPCQ14Buf` at packet boundaries regardless of fsKHz.

**Full Analysis**: See `SILK_DIVERGENCE_ANALYSIS.md`

**Reference Code**:
- libopus: `tmp_check/opus-1.6.1/silk/decoder_set_fs.c` lines 72-97
- libopus: `tmp_check/opus-1.6.1/silk/dec_API.c` (packet handling)
- Go: `internal/silk/libopus_decode.go` lines 5-56
