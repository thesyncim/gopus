---
phase: 12-compliance-polish
plan: 02
subsystem: test-infrastructure
tags: [rfc-8251, compliance, test-vectors, quality-metric, opus_demo]
depends_on:
  requires: [12-01]
  provides: [testvectors-package, compliance-tests, quality-metric]
  affects: [decoder-improvements]
tech-stack:
  added: []
  patterns: [big-endian-parsing, snr-quality-metric]
key-files:
  created:
    - internal/testvectors/parser.go
    - internal/testvectors/parser_test.go
    - internal/testvectors/quality.go
    - internal/testvectors/quality_test.go
    - internal/testvectors/compliance_test.go
  modified: []
decisions:
  - id: D12-02-01
    decision: "Use big-endian (network byte order) for opus_demo .bit file parsing"
    reason: "Discovered from actual test vector analysis; official files use big-endian"
    impact: "Parser correctly reads official RFC 8251 test vectors"
  - id: D12-02-02
    decision: "Implement simplified SNR-based quality metric instead of full opus_compare"
    reason: "Full psychoacoustic model is complex; SNR-based metric sufficient for initial compliance testing"
    impact: "Quality metric maps to Q scale: Q=0 at 48dB SNR threshold"
  - id: D12-02-03
    decision: "Check both .dec and m.dec references; pass if either matches"
    reason: "RFC 8251 allows phase shift variants; implementations may match either reference"
    impact: "More implementations pass compliance; follows RFC 8251 guidance"
metrics:
  duration: "7 minutes"
  completed: "2026-01-22"
  tasks: 3/3
  commits: 3
---

# Phase 12 Plan 02: RFC 8251 Test Vector Compliance Summary

**One-liner:** opus_demo .bit parser, SNR quality metric, and RFC 8251 compliance test infrastructure with auto-download

## What Was Built

### 1. opus_demo Bitstream Parser (`internal/testvectors/parser.go`)
- `ParseOpusDemoBitstream()` reads opus_demo .bit file format
- Format: uint32_be packet_length + uint32_be enc_final_range + packet_data
- `ReadBitstreamFile()` convenience function for file reading
- `GetBitstreamInfo()` for bitstream summary (packet count, duration, TOC)
- `Packet` struct holds data and FinalRange for verification

### 2. Quality Metric Computation (`internal/testvectors/quality.go`)
- `ComputeQuality()` computes SNR-based quality metric
- Maps SNR to Q scale: Q=0 at 48dB (threshold), Q=100 at 96dB
- `QualityPasses()` checks Q >= 0 threshold per RFC 8251
- `CompareSamples()` computes MSE between sample slices
- `NormalizedSNR()` computes signal-to-noise ratio in dB
- `ComputeNoiseVector()` with overflow clamping

### 3. Decoder Compliance Tests (`internal/testvectors/compliance_test.go`)
- `TestDecoderCompliance` runs all 12 RFC 8251 test vectors
- Automatic download from opus-codec.org and caching
- Parses TOC byte to determine decoder parameters
- Decodes all packets through gopus decoder
- Compares against both .dec and m.dec references
- Reports quality metrics and pass/fail status

## Key Implementation Details

### Bitstream Format Discovery
The official opus_demo .bit files use **big-endian** (network byte order), not little-endian as initially assumed from research. This was discovered by analyzing actual test vector files:
```
Bytes 0-3: 00 00 02 db  -> Big-endian: 731 bytes (reasonable)
                        -> Little-endian: 3.67GB (impossible)
```

### Quality Metric Formula
```go
SNR = 10 * log10(signalPower / noisePower)
Q = (SNR - 48) * (100/48)
// Q=0 at 48dB, Q=100 at 96dB, Q=-50 at 24dB
```

### Test Vector Coverage
| Vector | Packets | Mode | Config | Duration |
|--------|---------|------|--------|----------|
| 01 | 2147 | CELT FB | 31 | 43s |
| 02 | 1185 | SILK NB | 3 | 71s |
| 03 | 998 | SILK MB | 7 | 60s |
| 04 | 1265 | SILK WB | 11 | 76s |
| 05 | 2037 | Hybrid SWB | 13 | 41s |
| 06 | 1876 | Hybrid FB | 15 | 38s |
| 07 | 4186 | CELT FB | 31 | 84s |
| 08 | 1247 | CELT | - | 25s |
| 09 | 1337 | - | - | 27s |
| 10 | 1912 | - | - | 38s |
| 11 | 553 | - | - | 11s |
| 12 | 1332 | - | - | 27s |

## Test Results

**Parser Tests:** All pass - correctly parses opus_demo format
**Quality Tests:** All pass - SNR calculations verified
**Compliance Tests:** Tests run successfully; decoder fails quality threshold

The compliance tests correctly identify that the current decoder does not yet pass RFC 8251 compliance. This is expected - the test infrastructure is complete and will validate future decoder improvements.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Fixed big-endian parsing for opus_demo format**
- **Found during:** Task 3 (running compliance tests)
- **Issue:** Parser used little-endian, giving impossible packet lengths (3.67GB)
- **Fix:** Changed to big-endian (network byte order) per actual file format
- **Files modified:** internal/testvectors/parser.go, parser_test.go
- **Commit:** 8952bad

## Commits

1. `9f2653b` - feat(12-02): implement opus_demo bitstream parser
2. `f0a96ed` - feat(12-02): implement quality metric computation
3. `8952bad` - feat(12-02): implement RFC 8251 decoder compliance tests

## Files Created/Modified

### Created
- `internal/testvectors/parser.go` - opus_demo .bit file parser
- `internal/testvectors/parser_test.go` - Parser unit tests
- `internal/testvectors/quality.go` - Quality metric computation
- `internal/testvectors/quality_test.go` - Quality metric tests
- `internal/testvectors/compliance_test.go` - RFC 8251 compliance tests

### Downloaded (at runtime)
- `internal/testvectors/testdata/opus_testvectors/` - Cached test vector files

## Verification

- [x] `go build ./internal/testvectors/...` succeeds
- [x] Parser correctly handles opus_demo .bit format (big-endian)
- [x] Quality metric computation produces sensible values
- [x] Compliance tests run and log quality metrics
- [x] Tests skip gracefully if network unavailable

## Next Steps

1. Improve decoder to support all Opus modes and frame sizes
2. Run compliance tests after decoder improvements
3. Enhance quality metric to full opus_compare if needed
4. Add range coder final state verification using FinalRange
