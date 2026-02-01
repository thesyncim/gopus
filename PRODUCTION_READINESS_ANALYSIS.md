# Gopus Production Readiness Analysis

**Date:** 2026-02-01
**Comparison Base:** libopus v1.6.1 reference implementation

---

## Executive Summary

The gopus **decoder** is feature-complete and production-ready for all three Opus modes (SILK, CELT, Hybrid). The **encoder** has notable gaps. This document details what's missing and prioritizes work items for production deployment.

---

## Current Implementation Status

### Decoder: Production Ready

| Component | Status | Notes |
|-----------|--------|-------|
| SILK Decoder | Complete | All bandwidths (NB/MB/WB), stereo, PLC |
| CELT Decoder | Complete | Full MDCT pipeline, postfilter, anti-collapse |
| Hybrid Decoder | Complete | SILK-CELT coordination, delay alignment |
| Packet Parsing | Complete | All 32 TOC configs, multi-frame (codes 0-3) |
| Stereo | Complete | Mid-side decoding, interleaved output |
| Resampling | Complete | All rates 8-48kHz |
| PLC | Basic | Works, but simplified vs libopus |

### Encoder: Gaps Exist

| Component | Status | Notes |
|-----------|--------|-------|
| SILK Encoder | Partial | FinalRange() returns 0, stereo limited |
| CELT Encoder | Working | Missing tonality/surround features |
| Hybrid Encoder | Partial | Disabled for stereo |

---

## Missing Features vs Reference libopus

### 1. CRITICAL - Not Implemented

| Feature | Description | Impact |
|---------|-------------|--------|
| **Deep PLC** | Neural network-based packet loss concealment (LPCNet) | Quality degradation on packet loss |
| **DRED** | Deep Redundancy for improved resilience | No neural-enhanced error recovery |
| **OSCE** | Opus Speech Codec Enhancement | No bandwidth extension enhancement |
| **DTX (full)** | Discontinuous Transmission | Basic stub only, not functional |
| **Custom Modes** | Non-standard frame sizes | Cannot use opus_custom API |

### 2. HIGH PRIORITY - Partial/Incomplete

| Feature | Current State | libopus Has |
|---------|--------------|-------------|
| **FEC (In-band)** | Framework exists | Full LBRR redundancy encoding |
| **Stereo Hybrid Encoding** | Disabled | Full stereo hybrid support |
| **SILK FinalRange()** | Returns 0 | Proper entropy state verification |
| **Complexity Levels** | Only level 10 | Levels 0-9 with quality tradeoffs |
| **Tonality Analysis** | Not used | Full music/speech detection |
| **Surround Trim** | Not implemented | Multi-channel optimization |

### 3. MEDIUM PRIORITY - Quality Improvements

| Feature | Gap Description |
|---------|-----------------|
| **PLC Quality** | Uses simple repetition vs libopus full algorithm |
| **Postfilter Tuning** | Basic implementation, not matched to libopus |
| **Energy Anti-collapse** | Simplified algorithm |
| **Bandwidth Prediction** | Missing tonality slope allocation |
| **Phase Inversion** | Not configurable |

### 4. LOW PRIORITY - Edge Features

| Feature | Notes |
|---------|-------|
| Multistream projection | Ambisonics support missing |
| 24-bit decode | Extended precision decode |
| VBR Constraint mode | VBR exists, constraint mode unclear |
| Hardening | Security bounds checks |

---

## Decoder-Specific Gaps (Production Blockers)

### Potential Panic Points

These locations could crash the decoder on malformed input:

```go
silk/resample.go:17        - panic("upsampleTo48k: invalid source rate")
silk/resample_sinc.go:149  - panic("upsampleTo48kSinc: invalid source rate")
silk/stereo.go:62          - panic("stereoUnmix: mismatched lengths")
```

**Recommendation:** Replace panics with error returns for production safety.

### Missing Decoder Features

1. **Deep PLC** - libopus 1.2+ has LPCNet-based PLC for better audio quality during packet loss
2. **OSCE** - Bandwidth extension for enhanced speech quality
3. **DRED Decoding** - Cannot decode DRED-enabled streams
4. **opus_decode24** - No 24-bit output precision

---

## Encoder-Specific Gaps

### Non-Working Features

| Feature | Location | Issue |
|---------|----------|-------|
| `FinalRange()` for SILK | `encoder/encoder.go:295` | Returns 0, breaks bitstream verification |
| Stereo Hybrid | `encoder/encoder.go:438` | Explicitly disabled, falls back to CELT |
| DTX | - | Stub only, doesn't work |
| Full FEC | - | Basic framework, not production-ready |

### Quality Gaps

| Feature | Issue |
|---------|-------|
| Tonality detection | Not integrated into mode selection |
| Surround trim | Returns 0.0 hardcoded |
| Complexity 0-9 | Only level 10 tuned |

---

## Test Coverage Analysis

### Well-Tested

- TOC parsing (all 32 configurations)
- Multi-frame packets
- PLC basic operation
- Mono and stereo decoding
- All sample rates
- Frame sizes across modes

### Needs More Testing

- Corrupted packet recovery
- Hybrid edge cases
- SILK stereo transitions
- Large frames (40ms, 60ms)
- Redundancy handling with lossy chains
- Performance benchmarks

---

## Production Readiness Checklist

### Decoder

- [x] SILK decoding (all bandwidths)
- [x] CELT decoding (all bandwidths)
- [x] Hybrid decoding
- [x] Stereo support
- [x] Multi-frame packets
- [x] Basic PLC
- [ ] Deep PLC (neural network)
- [ ] DRED decoding
- [ ] OSCE support
- [ ] Panic-free error handling
- [ ] 24-bit output

### Encoder

- [x] CELT encoding (working)
- [x] SILK encoding (partial)
- [x] Basic hybrid (mono only)
- [ ] Full stereo hybrid
- [ ] SILK FinalRange()
- [ ] DTX
- [ ] Full FEC
- [ ] Tonality analysis
- [ ] Complexity levels
- [ ] DRED encoding

---

## Recommended Priority for Production

### Phase 1: Stability (Must Have)

1. **Remove panics** - Replace all panic() with error returns
2. **Complete SILK FinalRange()** - Enable bitstream verification
3. **Enable stereo hybrid encoding** - Common production use case
4. **Add comprehensive test vectors** - Use libopus test suite

### Phase 2: Quality (Should Have)

5. **Improve PLC** - Match libopus algorithm for packet loss resilience
6. **Tonality analysis** - Better mode selection for music
7. **Complexity levels 0-9** - CPU/quality tradeoffs
8. **DTX** - Bandwidth savings during silence

### Phase 3: Advanced (Nice to Have)

9. **Deep PLC** - Neural network-based concealment
10. **DRED** - Deep redundancy encoding/decoding
11. **OSCE** - Speech enhancement
12. **Multistream projection** - Ambisonics

---

## Comparison Summary

| Category | gopus | libopus | Gap |
|----------|-------|---------|-----|
| Decoder Core | 100% | 100% | None |
| Encoder Core | 80% | 100% | Stereo hybrid, SILK range |
| PLC | Basic | Advanced (Deep PLC) | Neural PLC |
| FEC | Partial | Full | LBRR encoding |
| DTX | Stub | Full | Functional DTX |
| Neural Features | None | DRED, OSCE, Deep PLC | All missing |
| Test Coverage | Good | Extensive | More vectors needed |
| Error Handling | Panics | Errors | Replace panics |

---

## Conclusion

**For decoder use:** Gopus is production-ready for standard Opus decoding. The main concerns are:
1. Replace panic points with error returns
2. Test with libopus test vectors
3. Deep PLC is missing (affects quality on packet loss)

**For encoder use:** Gopus needs work before production:
1. SILK FinalRange() must be implemented
2. Stereo hybrid encoding must be enabled
3. DTX and FEC need completion

**Overall:** The decoder is ready for production with minor hardening. The encoder needs Phase 1 work before deployment.
