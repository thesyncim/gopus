# Encoder Debug Findings

## Status: Active Debugging

## Known Issues
1. **Audio Quality**: TestAudioAudibility failing with SNR=-4.39 dB (corrupted audio)
2. **Bitstream Divergence**: TestBitExactComparison shows divergence from libopus at byte 1-6

## Divergence Pattern
- TOC bytes match between gopus and libopus (0xf8, 0xf0 = CELT mode)
- Payload diverges early (byte 1-6)
- This suggests issues in:
  - Range coding state
  - Energy encoding
  - Band allocation
  - PVQ encoding

## C Reference
- Located at: `tmp_check/opus-1.6.1/`
- Comparison tool: `tmp_check/check_frame_glue.go`

## Decoder Status
- Decoder is working fine (per user)
- Focus is exclusively on encoder issues

## Debug Agents

### Agent Worktrees
| Agent | Worktree Path | Branch | Focus Area | Status |
|-------|--------------|--------|------------|--------|
| agent-1 | gopus-worktrees/agent-1 | fix-agent-1 | Range Coder | Active |
| agent-2 | gopus-worktrees/agent-2 | fix-agent-2 | CELT Energy Encoding | Active |
| agent-3 | gopus-worktrees/agent-3 | fix-agent-3 | PVQ Encoding | Active |
| agent-4 | gopus-worktrees/agent-4 | fix-agent-4 | Band Allocation | Active |
| agent-5 | gopus-worktrees/agent-5 | fix-agent-5 | SILK Encoder | Active |

---

## Findings Log

### [2026-01-31] Initial Analysis
- Identified encoder divergence in CELT mode
- Decoder confirmed working
- Main encoder files identified:
  - `encoder.go` (public API)
  - `internal/encoder/encoder.go` (main encoder logic)
  - `internal/celt/encoder.go` (CELT encoding)
  - `internal/silk/encoder.go` (SILK encoding)
  - `internal/rangecoding/encoder.go` (range coder)

---

## Verified Fixes (Do Not Regress)
*None yet - add fixes here after verification*

---

## Areas Under Investigation

### 1. Range Coding
- File: `internal/rangecoding/encoder.go`
- Status: Not started
- Agent: TBD

### 2. CELT Energy Quantization
- File: `internal/celt/encoder.go`
- Status: Not started
- Agent: TBD

### 3. CELT Band Allocation
- File: `internal/celt/alloc_tables.go`
- Status: Not started
- Agent: TBD

### 4. CELT PVQ Encoding
- File: `internal/celt/pvq.go`
- Status: Not started
- Agent: TBD

### 5. SILK Encoder
- Files: `internal/silk/encoder.go`, `internal/silk/silk_encode.go`
- Status: Agent 5 investigating
- Note: SILK compliance tests showing SNR -100 to -130 dB

---

## Test Commands
```bash
# Run encoder tests
go test -v ./internal/encoder/...

# Run testvector tests
go test -v ./internal/testvectors/...

# Run bit-exact comparison
go test -v -run TestBitExact ./internal/testvectors/...

# Run audio audibility test
go test -v -run TestAudioAudibility ./internal/testvectors/...

# Run CELT encoder tests with CGO comparison
go test -v ./internal/celt/cgo_test/...
```

---

### Band Allocation Investigation (Agent 4)

**Date**: 2026-01-31
**Worktree**: `/Users/thesyncim/GolandProjects/gopus-worktrees/agent-4`
**Branch**: `fix-agent-4`

#### Files Examined

1. **`internal/celt/alloc.go`** - Main allocation logic
   - `ComputeAllocation()` - base allocation computation
   - `ComputeAllocationWithEncoder()` - encoding path with skip/intensity/dual-stereo bits
   - `cltComputeAllocation()` - core bit allocation algorithm
   - `interpBits2Pulses()` / `interpBits2PulsesEncode()` - pulse interpolation

2. **`internal/celt/alloc_tables.go`** - Band allocation tables
   - `BandAlloc[11][21]` - Quality-based bit allocation per band
   - Values represent bits in 1/32 bit/sample

3. **`internal/celt/bands.go`** / **`internal/celt/bands_quant.go`** - Band quantization
   - `bitsToK()` - converts bits to pulse count using cache
   - `quantBand()` / `quantBandStereo()` - PVQ encoding per band

4. **`internal/celt/pulse_cache.go`** - Rate tables
   - `bitsToPulses()` - lookup in pulse cache
   - `pulsesToBits()` - reverse lookup
   - `getPulses()` - decode pseudo-pulse to actual pulse count

5. **`internal/celt/encode_frame.go`** - Frame encoding pipeline
   - Shows order: flags -> coarse energy -> TF -> spread -> dynalloc -> trim -> allocation -> fine energy -> PVQ

#### Tests Run

```
TestComputeAllocationBudget - PASS
  - Budget respected within 1 bit for all test cases
  - 100 bits -> 99 allocated (99.0%)
  - 500 bits -> 499 allocated (99.8%)
  - 1000 bits -> 999 allocated (99.9%)
  - 2000 bits -> 1999 allocated (100.0%)

TestAllocationEncodeDecodeRoundTrip - PASS
  - Encoder and decoder compute identical allocations
  - codedBands, intensity, dualStereo all match

TestDebugAllocation - PASS
  - For 64kbps @ 20ms with ~200 bits used for flags/energy:
    - Available for allocation: 1079 bits
    - Coded bands: 20 (band 20 skipped)
    - Total PVQ bits: 1016 bits
    - Total Fine bits: 62 bits
    - Sum: 1078 bits (within budget)
```

#### Key Observations

1. **Allocation Logic Appears Correct**:
   - Budget is respected
   - Bit distribution follows expected pattern (lower bands get more bits)
   - Caps are respected

2. **Table Verification**:
   - `BandAlloc` table matches libopus structure (11 quality levels x 21 bands)
   - `cacheCaps` and `cacheBits50` tables present
   - `EBands` table verified: [0,1,2,3,4,5,6,7,8,10,12,14,16,20,24,28,34,40,48,60,78,100]
   - `LogN` table verified: [0,0,0,0,0,0,0,0,8,8,8,8,16,16,16,21,21,24,29,34,36]

3. **Potential Issues Identified**:

   a. **CGO comparison tests cannot build** - Missing libopus headers (`celt.h`, `entenc.h`)
      - Cannot verify against libopus directly
      - Would need to set up libopus build path

   b. **initCaps() implementation**:
      ```go
      // gopus:
      cap := int(cacheCaps[idx])
      caps[i] = (cap + 64) * channels * N >> 2

      // libopus (celt/rate.c init_caps):
      // Same formula, but we should verify bit-exact behavior
      ```

   c. **Trim offset calculation** (line ~155 in alloc.go):
      ```go
      trimOffset[j] = int(int64(channels*width*(allocTrim-5-lm)*(end-j-1)*(1<<(lm+bitRes))) >> 6)
      ```
      - Uses int64 to avoid overflow, but need to verify matches libopus exactly

4. **Spreading Decision**:
   - `SpreadingDecision()` in `spread_decision.go` looks correct
   - Threshold comparisons match libopus pattern
   - HF average and tapset decision logic present

#### Root Cause Assessment

**Band allocation itself appears correctly implemented.** The issue is likely:
1. Upstream: Energy quantization produces different values
2. Downstream: PVQ encoding uses correct allocation but encodes differently
3. Skip/intensity/dual-stereo decisions differ from libopus

#### Recommended Next Steps

1. **Enable CGO tests** by setting up libopus include path
2. **Trace actual allocation values** during a failing encode vs libopus
3. **Compare skip decision logic** - the encoder skip decision in `interpBits2PulsesEncode()` may differ
4. **Verify bitsToPulses/pulsesToBits** roundtrip matches libopus exactly

#### Status: Investigation Complete - No Obvious Bugs Found

The band allocation code appears structurally correct. The corrupted audio is likely caused by issues in other stages of the encoding pipeline (energy quantization, PVQ encoding, or range coding).

---

## Merge Protocol
When a fix is verified:
1. Ensure all tests pass in the worktree
2. Commit with descriptive message
3. Merge to `compliance` branch on origin
4. Document fix in this file under "Verified Fixes"
5. Update other agents if the fix affects their work
