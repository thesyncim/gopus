# Encoder Debug Session - 2026-01-30

## Session Status: ACTIVE

## Parallel Debugging Agents

| Agent | Focus Area | Status | Findings File |
|-------|-----------|--------|---------------|
| Agent 1 | SILK Gain Computation | RUNNING | - |
| Agent 2 | SILK Range Encoder Persistence | **FIXED** | SILK_RANGE_FIX.md |
| Agent 3 | CELT Signal Inversion | RUNNING | - |
| Agent 4 | CELT Energy Quantization | RUNNING | - |
| Agent 5 | SILK Excitation Scaling | RUNNING | - |
| Agent 6 | SILK LSF/NLSF Encoding | **DOCUMENTED** | SILK_LSF_FIX.md |

## Current Test Status

### Decoder: 11/12 passing
- testvector12 failing (SILK/Hybrid mix, Q=-32.06)

### Encoder: 0/9 passing
- All configs show Q ≈ -100 (essentially no signal)

## Verified Fixes ✅

### SILK Range Encoder Lifecycle (FIXED)
- **Bug**: `e.rangeEncoder` not cleared after standalone encoding
- **Fix**: Added `e.rangeEncoder = nil` after `Done()`
- **Result**: TestEncodeStreaming now passes (all 5 frames produce 106 bytes)
- **See**: SILK_RANGE_FIX.md

## Documented Issues 📝

### SILK LSF/NLSF Quantization
- gopus uses floating-point Chebyshev root finding
- libopus uses fixed-point with piecewise-linear cosine tables
- Stage 2 codebooks in gopus are placeholder/simplified
- Missing: multi-survivor search, trellis quantization, RD optimization
- **See**: SILK_LSF_FIX.md
- **Impact**: Medium - affects quality, not blocking

## Critical Bugs Still Under Investigation 🔍

### SILK Issues
1. **Gain Computation Returns 0** - `gain_encode.go`
   - Formula expects gains in [0.25, 16] but receives [0.001, 0.27]
   - Input is Q16, must divide by 65536 before taking log
   - **Status**: Agent investigating

### CELT Issues
1. **Signal Inversion** - Correlation = -0.5375 (should be +0.51)
   - Decoded audio is phase-inverted
   - **Status**: Agent investigating

2. **Packet Diverges at Byte 7** (payload offset, not including TOC)
   - First 7 payload bytes match libopus: `7B 5E 09 50 B7 8C 08`
   - Diverges at byte 7: gopus=0x33 vs libopus=0xD0
   - **Status**: Agent investigating

## Reference Code Location
- libopus C reference: `tmp_check/opus-1.6.1/`
- SILK gain: `tmp_check/opus-1.6.1/silk/gain_quant.c`
- CELT encoder: `tmp_check/opus-1.6.1/celt/bands.c`

## Constraints
- DO NOT break decoder (11/12 tests passing)
- Test changes against existing decoder tests
- Document all empirical findings

## Progress Log
- Started: 2026-01-30 18:42
- 6 parallel agents launched
- SILK Range Encoder bug fixed
- SILK LSF/NLSF differences documented
