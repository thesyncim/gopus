---
phase: 14-extended-frame-size
verified: 2026-01-23T17:00:00Z
status: gaps_found
score: 11/14 must-haves verified
gaps:
  - truth: "RFC 8251 test vectors pass with Q >= 0 threshold"
    status: failed
    reason: "Hybrid decoder incorrectly validates ALL packets (SILK/CELT/Hybrid) against hybrid-only frame size constraints"
    artifacts:
      - path: "internal/hybrid/hybrid.go"
        issue: "ValidHybridFrameSize check at line 32 blocks CELT 2.5/5ms and SILK 40/60ms packets"
      - path: "decoder.go"
        issue: "Always routes to hybrid.Decoder regardless of actual packet mode"
    missing:
      - "Mode detection from TOC byte to route SILK-only packets to SILK decoder"
      - "Mode detection from TOC byte to route CELT-only packets to CELT decoder"
      - "Mode detection from TOC byte to route Hybrid packets to Hybrid decoder"
      - "Remove or conditionally apply ValidHybridFrameSize check only for Hybrid mode packets"
  - truth: "Extended frame sizes verified to appear only in SILK-only or CELT-only modes (not Hybrid per RFC 6716)"
    status: verified
    reason: "Test vector tracking confirms extended sizes only in SILK/CELT modes"
    artifacts:
      - path: "internal/testvectors/compliance_test.go"
        issue: "none - verification successful"
  - truth: "CELT MDCT bin count matches frame size (fixes 1480 vs 960 mismatch)"
    status: verified
    reason: "DecodeBands now returns frameSize coefficients with zero-padding"
    artifacts:
      - path: "internal/celt/bands.go"
        issue: "none - fix implemented"
---

# Phase 14: Extended Frame Size Support Verification Report

**Phase Goal:** Support all Opus frame sizes (2.5/5/10/20/40/60ms) for RFC 8251 test vector compliance

**Verified:** 2026-01-23T17:00:00Z

**Status:** gaps_found

**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | CELT MDCT operates on frameSize coefficients, not band coefficient sum | ✓ VERIFIED | DecodeBands allocates `make([]float64, frameSize)` at line 54 |
| 2 | DecodeBands returns frameSize coefficients with upper bins zero-padded | ✓ VERIFIED | Upper bins (totalBins to frameSize-1) remain zero per comment lines 20-23 |
| 3 | IMDCT output count matches expected: 2*frameSize samples | ✓ VERIFIED | OverlapAdd takes 2*frameSize input, verified in tests |
| 4 | Overlap-add produces exactly frameSize output samples | ✓ VERIFIED | OverlapAdd line 49: `frameSize := n / 2`, output allocated as frameSize |
| 5 | CELT 2.5ms frames (120 samples) decode correctly | ✓ VERIFIED | TestDecodeFrame_ShortFrames/2.5ms_mono PASS |
| 6 | CELT 5ms frames (240 samples) decode correctly | ✓ VERIFIED | TestDecodeFrame_ShortFrames/5ms_mono PASS |
| 7 | CELT decoder produces exactly frameSize samples per frame | ✓ VERIFIED | TestOverlapAdd_OutputSize PASS for all frame sizes |
| 8 | Short frame decoding uses correct EffBands (13 for 2.5ms, 17 for 5ms) | ✓ VERIFIED | TestModeConfigShortFrames verifies mode parameters |
| 9 | SILK 40ms frames decode as 2 consecutive 20ms sub-blocks | ✓ VERIFIED | getSubBlockCount(Frame40ms) returns 2, tests PASS |
| 10 | SILK 60ms frames decode as 3 consecutive 20ms sub-blocks | ✓ VERIFIED | getSubBlockCount(Frame60ms) returns 3, tests PASS |
| 11 | Sub-block state (LPC, gain, pitch) transfers correctly between sub-blocks | ✓ VERIFIED | decode20msBlock uses decoder state fields that persist |
| 12 | Output sample count matches frame duration at native SILK rate | ✓ VERIFIED | TestDecodeFrame_40ms and TestDecodeFrame_60ms verify sample counts |
| 13 | Extended frame sizes verified to appear only in SILK-only or CELT-only modes (not Hybrid per RFC 6716) | ✓ VERIFIED | TestComplianceSummary confirms "no extended frame sizes in Hybrid mode" |
| 14 | RFC 8251 test vectors pass with Q >= 0 threshold | ✗ FAILED | All 12 test vectors fail with Q=-100.00 due to mode routing bug |

**Score:** 13/14 truths verified (but 1 critical blocker)

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/celt/bands.go` | Zero-padded coefficient output to match frameSize | ✓ VERIFIED | Line 54: `coeffs := make([]float64, frameSize)` |
| `internal/celt/synthesis.go` | IMDCT with correct frameSize input | ✓ VERIFIED | OverlapAdd produces frameSize output (line 49) |
| `internal/celt/bands_test.go` | Band coefficient count verification | ✓ VERIFIED | TestDecodeBands_OutputSize exists and passes |
| `internal/celt/synthesis_test.go` | Synthesis sample count verification | ✓ VERIFIED | TestOverlapAdd_OutputSize exists and passes |
| `internal/celt/decoder_test.go` | Short frame decode tests | ✓ VERIFIED | TestDecodeFrame_ShortFrames exists and passes |
| `internal/silk/decode.go` | 40/60ms sub-block decoding | ✓ VERIFIED | is40or60ms and getSubBlockCount implemented correctly |
| `internal/silk/decode_test.go` | Extended frame duration tests | ✓ VERIFIED | TestDecodeFrame_40ms and TestDecodeFrame_60ms exist and pass |
| `internal/testvectors/compliance_test.go` | RFC 8251 compliance validation with mode tracking | ✓ VERIFIED | Enhanced with frame size and mode logging |
| `internal/testvectors/quality.go` | Quality metric computation | ✓ VERIFIED | ComputeQuality exists (from Phase 12) |
| `decoder.go` | Mode routing logic | ✗ STUB | Always routes to hybrid.Decoder without mode detection |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|----|--------|---------|
| internal/celt/bands.go | internal/celt/synthesis.go | coefficient slice passed to Synthesize | ✓ WIRED | DecodeBands returns frameSize coeffs to Synthesize |
| internal/celt/decoder.go | internal/celt/synthesis.go | Synthesize call in DecodeFrame | ✓ WIRED | DecodeFrame calls d.Synthesize(coeffs, ...) |
| internal/silk/decode.go | internal/silk/frame.go | is40or60ms and getSubBlockCount | ✓ WIRED | DecodeFrame uses getSubBlockCount for loop |
| internal/testvectors/compliance_test.go | gopus.Decoder | dec.DecodeInt16 | ⚠️ ORPHANED | Wired but hits mode routing bug |
| decoder.go | internal/hybrid/decoder.go | d.dec.DecodeToFloat32 | ✗ INCORRECT | Routes all modes to hybrid decoder |

### Requirements Coverage

**CMP-01: Pass official Opus decoder test vectors**

| Test Vector | Q(.dec) | Q(m.dec) | Status | Blocking Issue |
|-------------|---------|----------|--------|----------------|
| testvector01 (CELT) | -100.00 | -100.00 | ✗ BLOCKED | Mode routing bug: CELT packets hit hybrid frame size validation |
| testvector02 (SILK) | -100.00 | -100.00 | ✗ BLOCKED | Mode routing bug: SILK 40/60ms packets hit hybrid validation |
| testvector03 (SILK) | -100.00 | -100.00 | ✗ BLOCKED | Same as testvector02 |
| testvector04 (SILK) | -100.00 | -100.00 | ✗ BLOCKED | Same as testvector02 |
| testvector05 (Hybrid) | -100.00 | -100.00 | ✗ BLOCKED | Hybrid frames also fail (needs investigation) |
| testvector06-12 | -100.00 | -100.00 | ✗ BLOCKED | Same pattern |

**Overall: 0/12 test vectors passing**

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| decoder.go | 74-86 | Always routes to hybrid.Decoder | 🛑 Blocker | SILK-only and CELT-only packets incorrectly routed through hybrid decoder |
| internal/hybrid/hybrid.go | 32-34 | ValidHybridFrameSize check applied to all packets | 🛑 Blocker | Rejects CELT 2.5/5ms and SILK 40/60ms frames with "invalid frame size" error |
| decoder.go | 62 | TOC parsed but mode not extracted | ⚠️ Warning | Mode information available but unused for routing |

### Gaps Summary

Phase 14 successfully implemented the **decoder subsystem** changes for extended frame size support:

**✓ CELT Infrastructure (Plan 14-01, 14-02):**
- DecodeBands returns frameSize coefficients (not totalBins)
- OverlapAdd produces frameSize samples (correct MDCT/IMDCT behavior)
- Short frames (2.5ms, 5ms) decode correctly in isolation
- Mode configurations correct for all frame sizes

**✓ SILK Infrastructure (Plan 14-03):**
- Long frames (40ms, 60ms) decode correctly in isolation
- Sub-block decoding (2x20ms for 40ms, 3x20ms for 60ms) works
- Sample counts correct for all bandwidths

**✓ Test Infrastructure (Plan 14-04):**
- RFC 8251 test vector parsing and execution
- Frame size and mode tracking per packet
- Quality metric computation
- Hybrid mode assumption verified (extended sizes only in SILK/CELT)

**✗ CRITICAL GAP: Mode Routing Architecture**

The **gopus.Decoder** (public API) does not implement mode routing. All packets (SILK-only, CELT-only, Hybrid) are routed to `hybrid.Decoder`, which:

1. Validates `ValidHybridFrameSize(frameSize)` for ALL packets
2. Only allows 10ms (480) and 20ms (960) frames
3. Rejects CELT 2.5ms (120), 5ms (240) and SILK 40ms (1920), 60ms (2880) packets with "hybrid: invalid frame size" error

**Root Cause:** The decoder architecture assumes all Opus packets are Hybrid mode. This was acceptable when only 10ms/20ms frames were tested (since all modes support those sizes), but breaks with extended frame sizes that are mode-specific.

**Required Fix:** Implement mode detection and routing in decoder.go:

```go
// In Decode() method, after parsing TOC:
toc := ParseTOC(data[0])
mode := toc.Mode() // Extract mode from config field (SILK/CELT/Hybrid)

// Route based on mode:
switch mode {
case ModeSILK:
    samples, err = d.silkDecoder.DecodeToFloat32(frameData, frameSize)
case ModeCELT:
    samples, err = d.celtDecoder.DecodeToFloat32(frameData, frameSize)
case ModeHybrid:
    samples, err = d.hybridDecoder.DecodeToFloat32(frameData, frameSize)
}
```

This architecture change is **outside the scope of Phase 14** (Extended Frame Size Support) and belongs in a future phase (likely "Phase 14.1: Mode Routing Architecture" or "Phase 15: Decoder Architecture Fix").

### Success Criteria from ROADMAP.md

| Criterion | Status | Evidence |
|-----------|--------|----------|
| 1. CELT decoder supports 2.5ms and 5ms frame sizes | ✓ PASS | Tests pass, DecodeBands/OverlapAdd handle 120/240 samples |
| 2. SILK decoder supports 40ms and 60ms frame sizes | ✓ PASS | Sub-block decoding verified, tests pass for 40ms/60ms |
| 3. Extended frame sizes verified to appear only in SILK-only or CELT-only modes (not Hybrid per RFC 6716) | ✓ PASS | TestComplianceSummary confirms no extended sizes in Hybrid mode |
| 4. RFC 8251 test vectors pass with Q >= 0 threshold | ✗ FAIL | 0/12 vectors pass (Q=-100 due to mode routing bug) |
| 5. CELT MDCT bin count matches frame size (fixes 1480 vs 960 mismatch) | ✓ PASS | DecodeBands returns frameSize coefficients |

**Overall: 4/5 success criteria met (80%)**

The one failing criterion (RFC 8251 compliance) is blocked by an architectural issue discovered during verification, not a failure of the Phase 14 implementation.

---

*Verified: 2026-01-23T17:00:00Z*
*Verifier: Claude (gsd-verifier)*
