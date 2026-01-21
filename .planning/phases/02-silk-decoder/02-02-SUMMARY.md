---
phase: 02-silk-decoder
plan: 02
subsystem: silk-decoder
tags: [silk, parameter-decoding, lsf, ltp, pitch, gain]

dependency-graph:
  requires: ["02-01"]
  provides: ["parameter-decoding", "frame-params-struct", "lsf-to-lpc"]
  affects: ["02-03"]

tech-stack:
  added: []
  patterns: ["two-stage-vq", "delta-coding", "chebyshev-recursion"]

file-tracking:
  key-files:
    created:
      - internal/silk/decode_params.go
      - internal/silk/gain.go
      - internal/silk/lsf.go
      - internal/silk/pitch.go
      - internal/silk/decode_params_test.go
    modified: []

decisions:
  - id: D02-02-01
    decision: "Use direct polynomial method for LSF-to-LPC as primary implementation"
    rationale: "Chebyshev recursion is complex; direct method is clearer and verifiable"

metrics:
  duration: ~8m
  completed: 2026-01-21
---

# Phase 2 Plan 02: SILK Parameter Decoding Summary

**One-liner:** SILK parameter decoding with frame type, gains, two-stage VQ LSF, and pitch/LTP extraction from range-coded bitstream.

## What Was Built

### 1. Frame Type and Classification (decode_params.go)
- **FrameParams struct** holds all decoded SILK frame parameters
- **DecodeFrameType** extracts signal type (0=inactive, 1=unvoiced, 2=voiced) and quantization offset (0=low, 1=high) from VAD flag
- Inactive frames bypass range decoding entirely

### 2. Gain Decoding (gain.go)
- **decodeSubframeGains** decodes Q16 gains for all subframes
- First subframe: absolute encoding (MSB + LSB from signal-type-specific tables)
- Subsequent subframes: delta-coded from previous (centered at 4)
- Uses GainDequantTable for log gain to linear Q16 conversion
- Gain limiting for first frame of stream

### 3. LSF Decoding and LPC Conversion (lsf.go)
- **decodeLSFCoefficients** implements two-stage VQ per RFC 6716:
  - Stage 1: codebook index selects base LSF vector
  - Stage 2: per-coefficient residuals refine values
- **applyLSFPrediction** interpolates with previous frame LSF
- **stabilizeLSF** enforces minimum spacing and ordering constraints
- **lsfToLPC** converts LSF to LPC using Chebyshev polynomial recursion
- Supports NB/MB (10 coefficients) and WB (16 coefficients)

### 4. Pitch and LTP Decoding (pitch.go)
- **decodePitchLag** decodes per-subframe pitch lags:
  - High part (coarse) + low bits (fine) for base lag
  - Contour delta for subframe variations
  - Clamped to [PitchLagMin, PitchLagMax] per bandwidth
- **decodeLTPCoefficients** decodes 5-tap Q7 LTP filters:
  - Periodicity index selects codebook (low/mid/high)
  - Per-subframe filter index lookup
- **decodeLTPScale** for gain adjustment

### 5. Unit Tests (decode_params_test.go)
- Bandwidth configuration validation
- Frame type inactive handling
- FrameParams struct verification
- GainDequantTable range and monotonicity
- LSF minimum spacing table lengths
- CosineTable boundary values
- LTP filter codebook sizes
- Pitch contour table dimensions
- stabilizeLSF ordering enforcement
- Decoder state management

## Key Technical Decisions

| ID | Decision | Rationale |
|----|----------|-----------|
| D02-02-01 | Direct polynomial LSF-to-LPC | Chebyshev recursion implementation is complex; direct method clearer |

## Integration Points

### Uses from 02-01 (SILK Foundation)
- **ICDF tables:** ICDFFrameTypeVADActive, ICDFGainMSB*, ICDFDeltaGain, ICDFLSFStage1*, ICDFLSFStage2*, ICDFPitchLag*, ICDFPitchContour*, ICDFLTPFilter*, ICDFLTPGain*
- **Codebooks:** LSFCodebookNBMB, LSFCodebookWB, LSFStage2Res*, LTPFilter*, PitchContour*
- **Tables:** GainDequantTable, CosineTable, LSFMinSpacing*
- **Decoder struct** with rangeDecoder, prevLSFQ15, previousLogGain state

### Provides for 02-03 (SILK Synthesis)
- **FrameParams** struct with all decoded parameters
- **Q16 gains** for amplitude scaling
- **Q12 LPC coefficients** for synthesis filtering
- **Pitch lags** for LTP lookback
- **Q7 LTP coefficients** for voiced prediction

## Verification Results

```
$ go build ./...
(no errors)

$ go test ./internal/silk/ -v
=== RUN   TestBandwidthConfig
--- PASS: TestBandwidthConfig (0.00s)
=== RUN   TestFrameTypeInactive
--- PASS: TestFrameTypeInactive (0.00s)
=== RUN   TestFrameParamsStruct
--- PASS: TestFrameParamsStruct (0.00s)
=== RUN   TestGainDequantTableRange
--- PASS: TestGainDequantTableRange (0.00s)
=== RUN   TestLSFMinSpacingLength
--- PASS: TestLSFMinSpacingLength (0.00s)
=== RUN   TestCosineTableRange
--- PASS: TestCosineTableRange (0.00s)
=== RUN   TestLTPFilterCodebookSizes
--- PASS: TestLTPFilterCodebookSizes (0.00s)
=== RUN   TestPitchContourSizes
--- PASS: TestPitchContourSizes (0.00s)
=== RUN   TestStabilizeLSF
--- PASS: TestStabilizeLSF (0.00s)
=== RUN   TestDecoderState
--- PASS: TestDecoderState (0.00s)
PASS
ok      gopus/internal/silk     0.168s
```

## Deviations from Plan

None - plan executed exactly as written.

## Files Created

| File | Purpose | Lines |
|------|---------|-------|
| internal/silk/decode_params.go | FrameParams struct, DecodeFrameType | 45 |
| internal/silk/gain.go | decodeSubframeGains, decodeFirstGainIndex | 92 |
| internal/silk/lsf.go | decodeLSFCoefficients, stabilizeLSF, lsfToLPC | 317 |
| internal/silk/pitch.go | decodePitchLag, decodeLTPCoefficients | 134 |
| internal/silk/decode_params_test.go | Unit tests | 185 |

## Next Phase Readiness

**Ready for 02-03 (SILK Synthesis):**
- All parameter decoding functions complete
- FrameParams populated with gains, LPC, pitch lags, LTP coefficients
- Decoder state management in place

**Remaining for SILK decoder:**
- Excitation signal decoding (shell coding, pulse positions)
- LTP synthesis filtering (voiced frames)
- LPC synthesis filtering (all frames)
- Output sample generation
