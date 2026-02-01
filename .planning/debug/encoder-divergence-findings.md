# CELT Encoder Divergence Investigation - Consolidated Findings

**Date:** 2026-02-01
**Status:** In Progress - 4 parallel agents working
**Current Divergence:** Byte 8 (8 matching bytes)

## Quick Summary

The gopus encoder produces valid, decodable packets but differs from libopus byte-by-byte. The interop test confirms packets are spec-compliant.

## Verified WORKING Components

These have been verified to match libopus:

1. **Range Encoder Primitives** ✅
   - EncodeUniform/ec_enc_uint: MATCH
   - EncodeRawBits/ec_enc_bits: MATCH
   - Header encoding (bytes 0-9): MATCH

2. **Pre-emphasis Filter** ✅
   - Verified identical when run in isolation
   - Ratio = 1.0 between gopus and libopus

3. **MDCT Transform** ✅
   - Matches with 88dB SNR when using same input
   - Forward transform verified correct

4. **Band Energy Computation** ✅
   - All 21 bands match with diff < 0.0000005
   - log2 amplitude computation correct

5. **Coarse Energy Encoding** ✅
   - All 21 bands match perfectly
   - qi values identical for coarse encoding

6. **TF, Spread, Trim Parameters** ✅
   - All match between encoders

## DIVERGENCE Points Identified

### 1. Fine Energy qi Values (Bands 17-20)
```
| Band | libopus qi | gopus qi | fineBits | Energy diff |
|------|------------|----------|----------|-------------|
| 17   | 0          | 5        | 3        | ~+0.625     |
| 18   | 1          | 3        | 3        | ~+0.25      |
| 19   | 0          | 1        | 1        | ~+0.5       |
| 20   | 1          | 0        | 1        | ~-0.5       |
```

### 2. PVQ Indices
- ALL PVQ indices differ starting from band 0
- Normalized directions have opposite signs in some bands
- gopus coeff[0]=-0.9009 vs libopus coeff[0]=+0.1564 for band 0

### 3. Packet Size Difference
- gopus: 218 bytes
- libopus: 261 bytes
- This 43-byte difference suggests different bit allocation or PVQ outcomes

## ROOT CAUSE Analysis

### Primary Suspect: DC Rejection Filter

Agent 38 found the smoking gun:

```
Full encoder (with DC reject): MDCT coeff[0] = -585.39
Direct preemph (no DC reject): MDCT coeff[0] = -607.78
Libopus:                       MDCT coeff[0] = -607.81
```

**The DC rejection filter is adding a ~22 difference in MDCT coefficients!**

The DC rejection filter state management may differ between gopus and libopus:
- Both have DC rejection
- But the STATE tracking or filter implementation differs
- This small difference propagates through pre-emphasis → MDCT → normalization → PVQ

### Cascade Effect
1. DC rejection adds slight offset to samples
2. Offset propagates through pre-emphasis
3. MDCT coefficients differ by ~22 in some bins
4. Normalized directions change significantly
5. PVQ search finds different optimal pulse vectors
6. Completely different encoded indices

## Files to Investigate

1. **DC Rejection:**
   - `internal/celt/encoder.go` - `ApplyDCReject()` function
   - Compare with libopus `opus_encoder.c` DC rejection

2. **Normalization:**
   - `internal/celt/bands_encode.go` - `NormalizeBandsToArray()`
   - Already fixed to use direct linear amplitudes (Agent 37)

3. **Fine Energy:**
   - `internal/celt/energy_encode.go` - `EncodeFineEnergy()`
   - qi values for bands 17-20 differ

## Fixes Applied So Far

### Agent 37: Normalization Fix ✅ (Merged)
- Changed from log-domain energy reconstruction to direct linear amplitude computation
- Added `ComputeLinearBandAmplitudes()`
- Commit: 476a14b on fix-agent-37

### Agent 35: Decoder Float32 IMDCT ✅ (Merged)
- Added float32 IMDCT functions for decoder precision
- Improved decoder SNR to 133-138 dB
- Commit: 2c9dc45 on fix-agent-35

### Agent 36: CGO Wrapper Fix ✅
- Fixed `pack_ec_enc_local` packet length calculation
- Changed to `enc->nend_bits % 8` for partial bits

## Interop Test Created ✅

Agent 40 created comprehensive interop tests:
- `/internal/celt/cgo_test/interop_encode_test.go`
- 23 test cases, all passing
- Confirms gopus packets are valid and decodable by libopus

## Latest Findings (Agent 38) - Updated

### 1. DC Rejection Issue
**DC Rejection should NOT be applied for float CELT path!**

```
gopus WITHOUT DC rejection: matches libopus MDCT with 88dB SNR
gopus WITH DC rejection: ~22 difference per coefficient
```

### 2. tonalAverage Initialization Bug ✅ FIXED
**BUG:** gopus initialized `tonalAverage` to 0, libopus initializes to 256

```go
// WRONG (gopus)
tonalAverage: 0

// CORRECT (libopus)
tonal_average = 256
```

This affects spread decision hysteresis calculation.

### 3. shortBlocks Semantic Difference
**Issue:** Different semantics for shortBlocks variable

- **libopus:** 0 for long blocks, 1 for short blocks (boolean)
- **gopus:** actual count (8 for transient)

The condition `shortBlocks > 1` in gopus may not work the same as `shortBlocks` in libopus.

### 4. Spread Encoding Bug 🔴 NEW FINDING
**Critical:** gopus computes spread=2 but ENCODES spread=0!

```
SPREAD_DEBUG: shortBlocks=8 complexity=10 nbAvailableBytes=159 C=1 isTransient=1 spread=2
Decoded gopus packet: Spread=0  <-- Wrong!
Decoded libopus packet: Spread=2
```

There's a bug in how spread value is written to the bitstream, not in the spread computation.

## Latest Findings (Agent 39) - Fine Energy

### Confirmed Root Cause
Fine energy qi values differ for bands 17-20 because the `energies` array passed to
`EncodeFineEnergy()` has different values compared to libopus:

| Band | libopus qi | gopus qi | fineBits | Implied energy diff |
|------|------------|----------|----------|---------------------|
| 17   | 0          | 5        | 3        | ~+0.625             |
| 18   | 1          | 3        | 3        | ~+0.250             |
| 19   | 0          | 1        | 1        | ~+0.500             |
| 20   | 1          | 0        | 1        | ~-0.500             |

**Key insight:** Coarse encoding is CORRECT (first 16 bytes match perfectly). The issue is
only in fine energy for upper bands (17-20). This points to MDCT/pre-emphasis state differences
for high-frequency content.

## Next Steps for Investigation

1. **Spread Decision State:**
   - Check `tonalAverage` and `spreadDecision` initialization
   - Compare spread computation algorithm with libopus

2. **Verify DC rejection removal doesn't break other tests:**
   - Need to conditionally disable for CELT float path only

2. **Trace exact input to PVQ:**
   - Log normalized coefficients before PVQ
   - Compare with libopus normalized input

3. **Check transient mode interleaving:**
   - Short block MDCT interleaving may differ
   - Band coefficient layout in transient mode

## Debug Session Files

- Agent 37: `.planning/debug/resolved/pvq-normalization.md`
- Agent 35: `.planning/debug/resolved/packet-61-divergence.md`
- Agent 36: `.planning/debug/range-encoder-state-divergence.md` (in worktree)
- Agent 38: `.planning/debug/bit-allocation-divergence.md` (in worktree)
- Agent 39: `.planning/debug/energy-precision-divergence.md` (in worktree)
