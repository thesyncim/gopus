---
phase: 15-celt-decoder-quality
verified: 2026-01-23T18:07:32Z
status: gaps_found
score: 3/5 must-haves verified
re_verification:
  previous_status: gaps_found
  previous_score: 3/5
  gaps_closed:
    - "Multi-frame packet handling fixed - sample counts now match for stereo vectors"
  gaps_remaining:
    - "CELT decoder output correlates with reference audio (energy ratio > 50%)"
    - "CELT-only test vectors achieve Q >= 0 threshold"
  regressions: []
gaps:
  - truth: "CELT decoder output correlates with reference audio (energy ratio > 50%)"
    status: failed
    reason: "RFC 8251 test vectors still show Q=-100 after structural fixes and multi-frame packet handling"
    artifacts:
      - path: "internal/celt/decoder.go"
        issue: "Decoder produces incorrect output despite all component verification passing"
      - path: "decoder.go"
        issue: "Multi-frame handling fixed but underlying CELT synthesis still incorrect"
    missing:
      - "Root cause investigation beyond component-level verification"
      - "Bitstream-level comparison with libopus reference decoder"
      - "End-to-end trace comparison for divergence point identification"
  - truth: "CELT-only test vectors achieve Q >= 0 threshold"
    status: failed
    reason: "All CELT test vectors (01, 07, 11) fail with Q=-100; mono vector (07) has 2x sample count discrepancy"
    artifacts:
      - path: "internal/testvectors/compliance_test.go"
        issue: "testvector01: Q=-100 (stereo, samples match); testvector07: Q=-100 (mono, samples 1085040 vs 2170080); testvector11: Q=-100 (stereo, samples match)"
    missing:
      - "Fix for mono CELT sample count (2x discrepancy in testvector07)"
      - "Investigation of why output doesn't correlate despite correct sample counts"
      - "Comparison of actual decoded values with reference intermediate states"
---

# Phase 15: CELT Decoder Quality Re-Verification Report

**Phase Goal:** Fix CELT decoder algorithm issues to achieve reference-matching output
**Verified:** 2026-01-23T18:07:32Z
**Status:** gaps_found
**Re-verification:** Yes — after gap closure attempts (plans 15-06 through 15-09)

## Re-Verification Summary

**Previous verification (2026-01-23T11:15:00Z):** gaps_found (3/5)
**Current verification (2026-01-23T18:07:32Z):** gaps_found (3/5)

**Work completed since last verification:**
- Plan 15-06: Added comprehensive debug trace infrastructure (Tracer interface, LogTracer, NoopTracer)
- Plan 15-07: Verified PVQ/CWRS decoding correctness via exhaustive enumeration
- Plan 15-08: Added bit allocation verification and range decoder bit consumption tests
- Plan 15-09: Fixed multi-frame Opus packet handling per RFC 6716 Section 3.2

**Progress made:**
- Sample counts now match reference for stereo CELT vectors (testvector01, testvector11)
- All component-level verification tests pass (BetaCoef, DecodeSymbol, denormalization, IMDCT, PVQ, CWRS, bit allocation)
- Debug infrastructure in place for bitstream-level analysis

**Remaining gaps:**
- Q=-100 persists on all CELT test vectors despite structural fixes
- Mono CELT vector (testvector07) has 2x sample count discrepancy (1085040 vs 2170080)
- Root cause unknown - requires deeper investigation beyond component verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence | Change from Previous |
|---|-------|--------|----------|---------------------|
| 1 | CELT decoder output correlates with reference audio (energy ratio > 50%) | ✗ FAILED | RFC 8251 test vectors show Q=-100, complete output mismatch | No change |
| 2 | CELT 2.5ms frames synthesize without audible artifacts | ✓ VERIFIED | TestDecodeFrame120Samples passes, output is finite | No change |
| 3 | CELT 5ms frames synthesize without audible artifacts | ✓ VERIFIED | TestDecodeFrame240Samples passes, output is finite | No change |
| 4 | CELT 10ms frames synthesize without audible artifacts | ✓ VERIFIED | TestDecodeFrame480Samples passes, output is finite | No change |
| 5 | CELT-only test vectors achieve Q >= 0 threshold | ✗ FAILED | All CELT vectors (01, 07, 11) fail with Q=-100 | Partial improvement: sample counts match for stereo |

**Score:** 3/5 truths verified (unchanged)

**Critical Note:** Truths 2-4 are STRUCTURALLY verified (no crashes, finite output, correct sample count for stereo) but NOT QUALITY verified. The phase goal requires "synthesize without audible artifacts" which implies reference-matching quality, not just structural correctness.

### Required Artifacts

All artifacts from previous verification remain verified. New artifacts added:

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/celt/debug_trace.go` | Trace infrastructure | ✓ VERIFIED | Tracer interface, LogTracer, NoopTracer implemented |
| `internal/celt/debug_trace_test.go` | Trace tests | ✓ VERIFIED | Format, truncation, integration tests pass |
| `internal/celt/pvq_test.go` | PVQ verification tests | ✓ VERIFIED | Unit norm, determinism, energy distribution tests pass |
| `internal/celt/cwrs_test.go` | CWRS exhaustive tests | ✓ VERIFIED | Exhaustive enumeration for small codebooks, all pass |
| `internal/celt/alloc_test.go` | Bit allocation tests | ✓ VERIFIED | Budget, distribution, trim, LM tests pass |
| `internal/testvectors/celt_debug_test.go` | CELT trace tests | ✓ VERIFIED | Energy progression tracking tests exist |
| `decoder.go` (extractFrameData) | Multi-frame packet handling | ✓ VERIFIED | Handles FrameCode 0/1/2/3 per RFC 6716 |
| `decoder.go` (getTotalSamples) | Sample count calculation | ✓ VERIFIED | Calculates total samples for all frame codes |

**All planned artifacts exist and are substantive.** No stubs, no missing files.

### Key Link Verification

All previous key links remain verified. New links verified:

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| decoder.go | extractFrameData | Multi-frame packet parsing | ✓ WIRED | Calls extractFrameData(data, pktInfo) |
| extractFrameData | PacketInfo | Frame count/sizes | ✓ WIRED | Uses info.FrameCount, info.FrameSizes |
| decoder.go | getTotalSamples | Buffer allocation | ✓ WIRED | Calls getTotalSamples(data) for buffer sizing |
| CELT decoder | DefaultTracer | Debug output | ✓ WIRED | Calls DefaultTracer.Trace*() methods |
| PVQ tests | DecodePulses | Exhaustive verification | ✓ WIRED | Tests enumerate all V(n,k) codewords |
| Allocation tests | bitsToK | Budget verification | ✓ WIRED | Tests verify monotonicity and budget constraints |

**All key links verified.** Components are properly wired together.

### Requirements Coverage

| Requirement | Status | Blocking Issue | Change from Previous |
|-------------|--------|----------------|---------------------|
| DEQ-02 (CELT decoder quality) | ✗ BLOCKED | Q=-100 on all test vectors | No change |
| FRM-01 (2.5ms frames) | ⚠️ PARTIAL | Frames decode but quality unverified | No change |
| FRM-02 (5ms frames) | ⚠️ PARTIAL | Frames decode but quality unverified | No change |
| FRM-03 (10ms frames) | ⚠️ PARTIAL | Frames decode but quality unverified | No change |

### Anti-Patterns Found

None blocking. Code quality remains good:
- No TODO/FIXME comments in modified files
- No placeholder content
- No empty implementations
- Proper error handling throughout
- Debug infrastructure properly uses zero-overhead NoopTracer by default

### Human Verification Required

#### 1. Bitstream-Level Divergence Analysis

**Test:** Use debug trace infrastructure to decode CELT test vector and compare with libopus intermediate values
**Expected:** Identify exact stage where gopus decoder diverges from reference (header, energy, allocation, PVQ, coeffs, synthesis)
**Why human:** Requires:
- Building libopus with debug output
- Running same test vector through both decoders
- Comparing trace output stage-by-stage
- Interpreting divergence patterns to identify root cause

#### 2. Mono CELT Sample Count Investigation

**Test:** Investigate why testvector07 (mono CELT) produces 1085040 samples vs 2170080 reference
**Expected:** Determine if reference file is stereo output for mono source, or if decoder has mono-specific issue
**Why human:** Requires:
- Inspecting reference file format/headers
- Understanding libopus mono output conventions
- Determining if decoder needs mono-specific handling

#### 3. End-to-End Test Vector Decode

**Test:** Decode complete CELT test vector with trace enabled, capture all intermediate states
**Expected:** Full pipeline trace showing header, energy, allocation, PVQ, coeffs, synthesis for each frame
**Why human:** Requires:
- Running extended trace capture (potentially large output)
- Analyzing patterns across multiple frames
- Identifying systematic vs per-frame issues

### Gaps Summary

**Structural Success, Quality Failure (Unchanged):**

Phase 15 has achieved comprehensive structural verification:
- All coefficient tables match libopus exactly (BetaCoefInter, BetaIntra)
- Range decoder properly updates state (DecodeSymbol verified)
- Denormalization formula is correct (math.Exp2 with clamping)
- IMDCT matches RFC 6716 specification (all frame sizes verified)
- PVQ/CWRS decoding verified correct via exhaustive enumeration
- Bit allocation respects budget constraints
- Multi-frame packet handling fixed (sample counts match for stereo)
- All frame sizes decode without crashes
- Output is always finite (no NaN/Inf)
- Debug infrastructure in place for deeper analysis

**However, the phase goal was NOT achieved:**
- Test vector quality remains Q=-100 (complete failure)
- Decoder output doesn't correlate with reference audio
- Energy ratio is near zero or wildly incorrect

**Root cause still unknown.** The fixes addressed identified structural issues and added comprehensive component verification, but a deeper problem remains. Possible causes (in priority order based on investigation):

1. **Bitstream desynchronization:** Range decoder may be consuming incorrect number of bits at some stage, causing all subsequent decoding to be wrong. Plan 15-08 found silence flag detection in synthetic data, but real test vectors need analysis.

2. **Coefficient organization/de-interleaving:** Band coefficients may be assembled incorrectly before IMDCT. PVQ and denormalization are verified correct individually, but their integration into the full spectrum may have bugs.

3. **Synthesis pipeline ordering:** IMDCT, windowing, overlap-add, de-emphasis may be in wrong order or have state management issues.

4. **Energy decoding edge cases:** While BetaCoef tables are correct, energy prediction may have bugs in fine energy, prediction state update, or band-to-band propagation.

5. **Mono-specific issue:** The 2x sample count discrepancy in testvector07 suggests mono handling may differ from stereo in ways not yet investigated.

**Next steps require bitstream-level analysis, not more component testing.**

---

## Test Results

### Frame Size Support (Unchanged)
```
TestDecodeFrame120Samples: PASS
TestDecodeFrame240Samples: PASS
TestDecodeFrame480Samples: PASS
TestDecodeFrame960Samples: PASS
TestCELTDecoderQualitySummary: PASS (all sizes decode, finite output)
```

### Component Verification (All PASS - Expanded Coverage)
```
TestBetaCoefInter: PASS (values match libopus)
TestBetaIntra: PASS (value = 0.15)
TestDecodeSymbol: PASS (range decoder state updates correctly)
TestDenormalizeBand: PASS (math.Exp2 with clamping)
TestIMDCTDirectCELTSizes: PASS (all sizes produce 2*N output)
TestIMDCTEnergyConservation: PASS (energy preserved within tolerance)
TestDecoderFiniteOutput: PASS (no NaN/Inf in output)

NEW from Plan 15-07 (PVQ/CWRS Verification):
TestDecodePulsesKnownVectors: PASS (all known vectors decode correctly)
TestDecodePulsesExhaustiveProperties: PASS (all V(n,k) vectors verified)
TestPVQ_VRecurrence: PASS (recurrence relation correct)
TestNormalizeVectorUnit: PASS (PVQ normalization produces unit L2 norm)
TestPVQUnitNorm: PASS (all PVQ outputs have unit norm)
TestPVQDeterminism: PASS (same input produces same output)
TestPVQEnergyDistribution: PASS (energy distributed correctly)

NEW from Plan 15-08 (Allocation Verification):
TestBitAllocationBudget: PASS (respects total bit budget)
TestBitAllocationDistribution: PASS (allocates across bands reasonably)
TestBitsToKMonotonic: PASS (more bits never reduces pulse count)
TestBitAllocationWithTrim: PASS (trim parameter works correctly)
TestBitAllocationWithLM: PASS (LM affects allocation correctly)
TestBitAllocationCaps: PASS (caps respected)

NEW from Plan 15-06 (Debug Infrastructure):
TestDebugTraceFormat: PASS (trace format correct)
TestDebugTraceTruncation: PASS (array truncation works)
TestDecodeWithTrace: PASS (tracing doesn't break decode)
```

### Multi-Frame Packet Handling (NEW from Plan 15-09)
```
Multi-frame packet extraction: IMPLEMENTED
- FrameCode 0: 1 frame (handled)
- FrameCode 1: 2 equal-sized frames (handled)
- FrameCode 2: 2 different-sized frames (handled)
- FrameCode 3: M frames with VBR (handled)

Sample count verification:
- testvector01 (stereo): 2830080 = 2830080 ✓ (FIXED)
- testvector07 (mono): 1085040 vs 2170080 ✗ (2x discrepancy)
- testvector11 (stereo): 2881920 = 2881920 ✓ (FIXED)
```

### RFC 8251 Compliance (CRITICAL FAILURE - Unchanged)
```
testvector01 (CELT stereo, mixed sizes): Q=-100.00 FAIL (samples match after 15-09)
testvector07 (CELT mono, mixed sizes): Q=-100.00 FAIL (sample count 2x off)
testvector11 (CELT stereo, 960 samples): Q=-100.00 FAIL (samples match after 15-09)

All CELT-only vectors fail with Q=-100 (complete mismatch).
Mixed-mode vectors (08, 09, 10) also fail with Q=-102 to Q=-109.
```

### Energy Correlation (Not Achieved)
Decoder produces near-zero or incorrect output. Energy ratio far below 50% threshold.

---

## Comparison with Previous Verification

**What improved:**
- Multi-frame packet handling: Sample counts now match for stereo CELT vectors
- Component verification: Exhaustive PVQ/CWRS testing added, all pass
- Debug infrastructure: Comprehensive trace system in place for future analysis
- Bit allocation: Verified correct budget and distribution behavior

**What didn't improve:**
- Q scores: Still -100 on all CELT test vectors
- Energy correlation: Still near zero
- Overall goal achievement: Still blocked

**New issues discovered:**
- Mono CELT vector has 2x sample count discrepancy (not noticed before)
- Silence flag detection in range decoder (explains some test data behavior)

**Net assessment:** Significant progress on tools and component verification, but no progress on actual quality goal. The investigation has ruled out many potential causes (coefficient tables, range decoder mechanics, PVQ decoding, IMDCT, bit allocation), which narrows the search space. However, the root cause remains unidentified.

---

_Verified: 2026-01-23T18:07:32Z_
_Verifier: Claude (gsd-verifier)_
_Re-verification: 2 (initial + 1 re-verification after plans 15-06 through 15-09)_
