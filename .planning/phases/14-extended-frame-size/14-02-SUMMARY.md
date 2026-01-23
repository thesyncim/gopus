---
phase: 14
plan: 02
status: complete
subsystem: celt-decoder
tags: [celt, short-frames, overlap-add, rfc8251]

dependency-graph:
  requires: ["14-01"]
  provides: ["celt-short-frame-decode", "correct-sample-output"]
  affects: ["14-03", "14-04"]

tech-stack:
  added: []
  patterns: ["mdct-overlap-add", "frame-size-agnostic-synthesis"]

key-files:
  created: []
  modified:
    - "internal/celt/synthesis.go"
    - "internal/celt/synthesis_test.go"
    - "internal/celt/decoder_test.go"
    - "internal/celt/modes_test.go"

decisions:
  - id: D14-02-01
    decision: "OverlapAdd produces frameSize samples (n/2 from 2n IMDCT output)"
    rationale: "Aligns with MDCT/IMDCT theory per RFC 6716"
    impact: "All frame sizes now produce exactly frameSize output samples"

metrics:
  duration: "~6m"
  completed: "2026-01-23"
---

# Phase 14 Plan 02: CELT 2.5ms and 5ms Frame Decoding Summary

OverlapAdd fixed to produce frameSize samples; short frame mode configs verified correct.

## Objective

Enable CELT 2.5ms and 5ms frame decoding for RFC 8251 test vector support.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Verify short frame mode configuration | 443549b | modes_test.go |
| 2 | Fix overlap-add for correct sample output | 4ecaca9 | synthesis.go, synthesis_test.go, decoder_test.go |
| 3 | Add short frame decode tests | 8ed290d | decoder_test.go |

## Implementation Details

### Task 1: Mode Configuration Verification

Added `TestModeConfigShortFrames` to verify mode parameters for short frames:
- 2.5ms (120 samples): LM=0, EffBands=13, ShortBlocks=1, MDCTSize=120
- 5ms (240 samples): LM=1, EffBands=17, ShortBlocks=2, MDCTSize=240

Mode configurations were already correct in `modes.go`; this task confirms them.

### Task 2: Overlap-Add Fix

Fixed `OverlapAdd` and `OverlapAddInPlace` to produce correct output size.

**Before (incorrect):**
- IMDCT produces 2N samples
- Output was `2N - overlap` samples (e.g., 1800 for N=960)

**After (correct):**
- IMDCT produces 2N samples
- Output is N samples (frameSize)
- First `overlap` samples: add prevOverlap + current[0:overlap]
- Middle samples: copy from current[overlap:frameSize]
- Save current[frameSize:frameSize+overlap] for next frame

This aligns with RFC 6716 MDCT/IMDCT theory where each frame contributes exactly frameSize samples after overlap-add.

### Task 3: Short Frame Decode Tests

Added comprehensive tests:
- `TestDecodeFrame_ShortFrames`: Mono decode for all frame sizes with actual CELT silence frames
- `TestDecodeFrame_ShortFrameStereo`: Stereo decode for all frame sizes
- `TestDecodeFrame_ShortFrameConsecutive`: Verifies state consistency across frames

## Deviations from Plan

None - plan executed exactly as written.

## Key Decisions

**D14-02-01: OverlapAdd produces frameSize samples**
- Output is n/2 where n is IMDCT output length (2*frameSize)
- This is correct MDCT/IMDCT behavior per RFC 6716 Section 4.3.5
- Previous implementation produced 2*frameSize - overlap which was incorrect

## Verification Results

All tests pass:
```
go build ./...                                    # SUCCESS
go test ./internal/celt -v                        # PASS (all tests)
TestModeConfigShortFrames                         # PASS
TestOverlapAdd_OutputSize                         # PASS (all frame sizes)
TestDecodeFrame_ShortFrames                       # PASS (120, 240, 480, 960)
TestDecodeFrame_ShortFrameStereo                  # PASS (all frame sizes)
```

## Success Criteria Verification

- [x] CELT 2.5ms (120 samples) frames decode to 120 samples
- [x] CELT 5ms (240 samples) frames decode to 240 samples
- [x] OverlapAdd produces frameSize output for all sizes
- [x] Mode configuration uses correct EffBands per frame size (13 for 2.5ms, 17 for 5ms)

## Next Phase Readiness

Ready for Plan 14-03 (SILK long frame decode verification). The CELT decoder now correctly handles all frame sizes with proper sample output counts.
