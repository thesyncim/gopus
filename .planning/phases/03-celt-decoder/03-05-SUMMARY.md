---
phase: 03-celt-decoder
plan: 05
subsystem: celt-synthesis
tags: [imdct, fft, window, overlap-add, stereo, synthesis]
depends_on:
  - requires: ["03-03", "03-04"]
  - provides: ["celt-synthesis", "celt-decode-frame"]
  - affects: ["04-hybrid-decoder"]
tech-stack:
  added: []
  patterns:
    - IMDCT via direct formula (O(n^2) for non-power-of-2 sizes)
    - Vorbis window for power-complementary overlap-add
    - Mid-side and intensity stereo unmixing
    - De-emphasis filter for natural sound
key-files:
  created:
    - internal/celt/mdct.go
    - internal/celt/window.go
    - internal/celt/stereo.go
    - internal/celt/synthesis.go
    - internal/celt/mdct_test.go
  modified:
    - internal/celt/decoder.go
decisions:
  - id: D03-05-01
    decision: "Direct IMDCT for CELT sizes (120,240,480,960)"
    rationale: "CELT uses non-power-of-2 sizes; direct O(n^2) computation handles all sizes correctly"
  - id: D03-05-02
    decision: "Window computed over 2*overlap samples"
    rationale: "Matches CELT's fixed 120-sample overlap at 48kHz"
  - id: D03-05-03
    decision: "De-emphasis filter coefficient 0.85"
    rationale: "Matches libopus PreemphCoef constant"
metrics:
  duration: ~10 minutes
  completed: 2026-01-21
---

# Phase 03 Plan 05: IMDCT Synthesis Summary

**One-liner:** Complete CELT synthesis pipeline with IMDCT, Vorbis windowing, overlap-add, stereo unmixing, and DecodeFrame integration.

## Completed Tasks

| # | Task | Commit | Key Changes |
|---|------|--------|-------------|
| 1 | FFT-based IMDCT | 55a8a61 | IMDCT(), IMDCTShort(), IMDCTDirect() |
| 2 | Vorbis window and overlap-add | c0e1d85 | VorbisWindow(), OverlapAdd(), Synthesize() |
| 3 | Stereo processing | a728187 | MidSideToLR(), IntensityStereo(), GetStereoMode() |
| 4 | DecodeFrame integration | c19df38 | DecodeFrame(), applyDeemphasis(), flag decoding |
| 5 | Synthesis tests | 79a2a00 | 22 new tests, benchmarks for IMDCT/synthesis |

## Implementation Details

### IMDCT (mdct.go)
- **IMDCT()**: Computes inverse MDCT for frequency-to-time conversion
  - Uses direct O(n^2) formula for CELT sizes (120, 240, 480, 960)
  - FFT-based path available for power-of-2 sizes
  - Output: 2n time samples from n frequency bins
- **IMDCTShort()**: Handles transient frames with multiple short blocks
  - Processes interleaved coefficients for 2, 4, or 8 short MDCTs
  - Enables better time resolution for transients
- **IMDCTDirect()**: Reference implementation for testing

### Vorbis Window (window.go)
- **VorbisWindow()**: sin(pi/2 * sin^2(pi*(i+0.5)/n))
  - Power-complementary for perfect reconstruction
  - Precomputed buffers for 120, 240, 480, 960 overlap sizes
- **ApplyWindow()**: In-place windowing of IMDCT output
  - Rising edge at beginning, falling edge at end

### Synthesis (synthesis.go)
- **OverlapAdd()**: Combines windowed frames for continuous audio
  - Returns output samples and new overlap buffer
- **Synthesize()**: Complete synthesis pipeline
  - IMDCT + windowing + overlap-add
  - Handles both normal and transient modes
- **SynthesizeStereo()**: Stereo synthesis with interleaved output

### Stereo Processing (stereo.go)
- **StereoMode**: Enum for MidSide, Intensity, Dual modes
- **MidSideToLR()**: Rotation matrix conversion
  - L = cos(theta)*M + sin(theta)*S
  - R = cos(theta)*M - sin(theta)*S
- **IntensityStereo()**: Mono with optional right inversion
- **GetStereoMode()**: Band mode selection based on intensity threshold

### DecodeFrame Integration (decoder.go)
- **DecodeFrame()**: Complete frame decoding pipeline
  1. Range decoder initialization
  2. Frame header flag decoding (silence, transient, intra)
  3. Coarse energy decoding with prediction
  4. Bit allocation computation
  5. Fine energy refinement
  6. PVQ band decoding
  7. Synthesis: IMDCT + window + overlap-add
  8. De-emphasis filter application
- **applyDeemphasis()**: IIR filter y[n] = x[n] + 0.85*y[n-1]
- Error types: ErrInvalidFrame, ErrInvalidFrameSize, ErrNilDecoder

## Decisions Made

| ID | Decision | Rationale |
|----|----------|-----------|
| D03-05-01 | Direct IMDCT for CELT sizes | Non-power-of-2 sizes (120,240,480,960) require direct computation |
| D03-05-02 | Window over 2*overlap samples | Matches CELT's fixed 120-sample overlap design |
| D03-05-03 | De-emphasis coefficient 0.85 | Matches libopus PreemphCoef constant |

## Test Coverage

**New tests:** 22
**Total CELT tests:** 61

Key test categories:
- IMDCT output length and value verification
- Transient short block handling
- Vorbis window value validation
- Overlap-add continuity
- Mid-side and intensity stereo conversion
- DecodeFrame for all frame sizes (120, 240, 480, 960)
- Stereo output interleaving
- De-emphasis filter behavior

Benchmarks added:
- BenchmarkIMDCT (960 bins)
- BenchmarkIMDCTShort (8 short blocks)
- BenchmarkOverlapAdd
- BenchmarkDecodeFrame (mono and stereo)
- BenchmarkMidSideToLR

## Deviations from Plan

None - plan executed exactly as written.

## Phase 03 Completion Status

**Phase 03 CELT Decoder is COMPLETE.**

All 5 plans executed:
- 03-01: CELT Foundation (modes, tables, decoder state)
- 03-02: CWRS Combinatorial Indexing (PVQ codebook)
- 03-03: Energy Decoding and Bit Allocation
- 03-04: PVQ Band Decoding and Folding
- 03-05: IMDCT Synthesis (this plan)

**Total artifacts:**
- 14 source files in internal/celt/
- 61 tests passing
- Complete CELT decoder with DecodeFrame() API

## Next Phase Readiness

Ready for Phase 04 (Hybrid Decoder):
- SILK decoder complete (Phase 02)
- CELT decoder complete (Phase 03)
- Both have public decode APIs
- Hybrid mode needs to combine SILK 8kHz with CELT 48kHz output
