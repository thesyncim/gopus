---
phase: 15-celt-decoder-quality
verified: 2026-01-23T11:15:00Z
status: gaps_found
score: 3/5 must-haves verified
gaps:
  - truth: "CELT decoder output correlates with reference audio (energy ratio > 50%)"
    status: failed
    reason: "RFC 8251 test vectors show Q=-100 (complete mismatch), decoder produces incorrect output"
    artifacts:
      - path: "internal/celt/decoder.go"
        issue: "Decoder produces near-zero or incorrect output despite structural fixes"
    missing:
      - "Root cause analysis of why Q=-100 persists after coefficient/range decoder fixes"
      - "Verification of band decoding (PVQ) correctness"
      - "Verification of coefficient de-interleaving and band assembly"
      - "Debug logging to trace where decoded values diverge from reference"
  - truth: "CELT-only test vectors achieve Q >= 0 threshold"
    status: failed
    reason: "All CELT test vectors (01, 07, 11) fail with Q=-100"
    artifacts:
      - path: "internal/testvectors/compliance_test.go"
        issue: "TestDecoderCompliance fails for all CELT vectors"
    missing:
      - "Investigation of specific CELT decoding pipeline failures"
      - "Comparison with libopus reference implementation"
---

# Phase 15: CELT Decoder Quality Verification Report

**Phase Goal:** Fix CELT decoder algorithm issues to achieve reference-matching output
**Verified:** 2026-01-23T11:15:00Z
**Status:** gaps_found
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | CELT decoder output correlates with reference audio (energy ratio > 50%) | ✗ FAILED | RFC 8251 test vectors show Q=-100, complete output mismatch |
| 2 | CELT 2.5ms frames synthesize without audible artifacts | ✓ VERIFIED | TestDecodeFrame120Samples passes, output is finite |
| 3 | CELT 5ms frames synthesize without audible artifacts | ✓ VERIFIED | TestDecodeFrame240Samples passes, output is finite |
| 4 | CELT 10ms frames synthesize without audible artifacts | ✓ VERIFIED | TestDecodeFrame480Samples passes, output is finite |
| 5 | CELT-only test vectors achieve Q >= 0 threshold | ✗ FAILED | All CELT vectors (01, 07, 11) fail with Q=-100 |

**Score:** 3/5 truths verified

**Critical Gap:** Truths 2-4 are STRUCTURALLY verified (no crashes, finite output, correct sample count) but NOT QUALITY verified (output doesn't match reference). The phase goal requires "synthesize without audible artifacts" which implies reference-matching quality, not just structural correctness.

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/celt/tables.go` | Corrected BetaCoef array | ✓ VERIFIED | BetaCoefInter[4] with values 0.92, 0.68, 0.37, 0.20; BetaIntra = 0.15 |
| `internal/celt/energy.go` | Fixed energy prediction | ✓ VERIFIED | Uses BetaIntra for intra mode, BetaCoefInter[lm] for inter mode |
| `internal/celt/tables_test.go` | Coefficient validation tests | ✓ VERIFIED | TestBetaCoefInter, TestBetaIntra pass |
| `internal/rangecoding/decoder.go` | DecodeSymbol method | ✓ VERIFIED | Implements libopus ec_dec_update() semantics |
| `internal/celt/energy.go` | updateRange uses DecodeSymbol | ✓ VERIFIED | Calls rd.DecodeSymbol(fl, fh, ft) |
| `internal/celt/energy_test.go` | Laplace decoding tests | ✓ VERIFIED | Tests verify entropy consumption |
| `internal/celt/bands.go` | math.Exp2 denormalization | ✓ VERIFIED | Uses math.Exp2(energy) with clamping to 32 |
| `internal/celt/bands_test.go` | Denormalization tests | ✓ VERIFIED | TestDenormalizeBand, TestDenormalizeEnergyClamping pass |
| `internal/celt/mdct.go` | Verified IMDCT implementation | ✓ VERIFIED | IMDCTDirect matches RFC 6716 Section 4.3.5 formula |
| `internal/celt/mdct_test.go` | IMDCT verification tests | ✓ VERIFIED | Tests for all CELT sizes (120, 240, 480, 960) pass |
| `internal/celt/decoder_test.go` | Frame-size decode tests | ✓ VERIFIED | TestDecodeFrame120/240/480/960Samples pass |
| `internal/celt/crossval_test.go` | Energy correlation tests | ✓ VERIFIED | Tests exist (though correlation is poor) |

**All planned artifacts exist and are substantive.** No stubs, no missing files.

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| energy.go | tables.go | BetaCoefInter lookup | ✓ WIRED | `beta = BetaCoefInter[lm]` in line 173 |
| energy.go | rangecoding/decoder.go | DecodeSymbol call | ✓ WIRED | `rd.DecodeSymbol(fl, fh, ft)` in line 141 |
| bands.go | energy.go | energy values | ✓ WIRED | `energies[band]` used in denormalization |
| synthesis.go | mdct.go | IMDCT call | ✓ WIRED | IMDCT used in synthesis pipeline |
| decoder_test.go | decoder.go | DecodeFrame call | ✓ WIRED | Tests call d.DecodeFrame(frameData, frameSize) |

**All key links verified.** Components are properly wired together.

### Requirements Coverage

| Requirement | Status | Blocking Issue |
|-------------|--------|----------------|
| DEQ-02 (CELT decoder quality) | ✗ BLOCKED | Q=-100 on all test vectors |
| FRM-01 (2.5ms frames) | ⚠️ PARTIAL | Frames decode but quality unverified |
| FRM-02 (5ms frames) | ⚠️ PARTIAL | Frames decode but quality unverified |
| FRM-03 (10ms frames) | ⚠️ PARTIAL | Frames decode but quality unverified |

### Anti-Patterns Found

None blocking. Code quality is good:
- No TODO/FIXME comments in modified files
- No placeholder content
- No empty implementations
- Proper error handling throughout

### Human Verification Required

#### 1. CELT Test Vector Quality Investigation

**Test:** Run decoder on CELT-only test vectors (01, 07, 11) with debug logging
**Expected:** Identify where decoded values diverge from reference (energy decoding? band decoding? PVQ? synthesis?)
**Why human:** Requires interactive debugging, comparison with libopus reference implementation, and domain expertise to interpret intermediate values

#### 2. Band Decoding (PVQ) Verification

**Test:** Verify PVQ decoding produces correct unit vectors for each band
**Expected:** PVQ output should be unit-norm vectors matching libopus behavior
**Why human:** PVQ is complex (Phase 3 implementation), needs verification that it wasn't broken by Phase 15 changes or has pre-existing bugs

#### 3. Coefficient Assembly Verification

**Test:** Verify band coefficients are correctly assembled into full spectrum before IMDCT
**Expected:** Coefficients should be properly de-interleaved and scaled
**Why human:** Requires understanding of CELT band structure and coefficient organization

### Gaps Summary

**Structural Success, Quality Failure:**

Phase 15 achieved structural correctness:
- All coefficient tables match libopus exactly
- Range decoder properly updates state
- Denormalization formula is correct
- IMDCT matches RFC 6716 specification
- All frame sizes decode without crashes
- Output is always finite (no NaN/Inf)

**However, the phase goal was NOT achieved:**
- Test vector quality remains Q=-100 (complete failure)
- Decoder output doesn't correlate with reference audio
- Energy ratio is near zero or wildly incorrect

**Root cause unknown.** The fixes addressed the issues identified in research (coefficient tables, range decoder, denormalization, IMDCT), but a deeper problem remains. Possible causes:

1. **Band decoding (PVQ):** May produce incorrect unit vectors
2. **Coefficient organization:** May have de-interleaving or scaling bugs
3. **Bit allocation:** May allocate bits incorrectly, causing range decoder desync
4. **Energy decoding:** May have bugs beyond coefficient values (e.g., fine energy, prediction state)
5. **Pre-existing bugs from Phase 3:** CELT decoder may have had structural bugs that Phase 15 didn't address

**Next steps require investigation, not just table fixes.**

---

## Test Results

### Frame Size Support
```
TestDecodeFrame120Samples: PASS
TestDecodeFrame240Samples: PASS
TestDecodeFrame480Samples: PASS
TestDecodeFrame960Samples: PASS
TestCELTDecoderQualitySummary: PASS (all sizes decode, finite output)
```

### Component Verification
```
TestBetaCoefInter: PASS (values match libopus)
TestBetaIntra: PASS (value = 0.15)
TestDecodeSymbol: PASS (range decoder state updates correctly)
TestDenormalizeBand: PASS (math.Exp2 with clamping)
TestIMDCTDirectCELTSizes: PASS (all sizes produce 2*N output)
TestIMDCTEnergyConservation: PASS (energy preserved within tolerance)
TestDecoderFiniteOutput: PASS (no NaN/Inf in output)
```

### RFC 8251 Compliance (CRITICAL FAILURE)
```
testvector01 (CELT stereo, mixed sizes): Q=-100.00 FAIL
testvector07 (CELT mono, mixed sizes): Q=-100.00 FAIL
testvector11 (CELT stereo, 960 samples): Q=-100.00 FAIL

All CELT-only vectors fail with Q=-100 (complete mismatch).
Mixed-mode vectors (08, 09, 10) also fail with Q=-102 to Q=-109.
```

### Energy Correlation (from crossval_test.go)
Not achieved. Decoder produces near-zero or incorrect output.

---

_Verified: 2026-01-23T11:15:00Z_
_Verifier: Claude (gsd-verifier)_
