# CELT Encoder Signal Inversion Investigation

## Problem Summary

When encoding audio with gopus CELT encoder and decoding with libopus decoder, the output signal has **negative correlation** (-0.5377) with the original input, indicating signal inversion.

## Key Findings

### 1. PVQ Search Input Modification Bug (FIXED)

**Location:** `/Users/thesyncim/GolandProjects/gopus/internal/celt/pvq_search.go`

**Issue:** The `opPVQSearch` function was modifying its input slice in-place, taking absolute values of all elements:

```go
// BEFORE (buggy):
for j := 0; j < n; j++ {
    if x[j] < 0 {
        signx[j] = 1
        x[j] = -x[j]  // MODIFIES INPUT!
    }
}
```

**Fix:** Create a local copy for the search:

```go
// AFTER (fixed):
absX := make([]float64, n)
for j := 0; j < n; j++ {
    if x[j] < 0 {
        signx[j] = 1
        absX[j] = -x[j]
    } else {
        absX[j] = x[j]
    }
}
// Use absX throughout the function instead of x
```

**Status:** Fixed. This was a correctness bug that needed to be fixed regardless of the inversion issue.

**Note:** The signal inversion persists after this fix. The root cause is elsewhere in the encoding pipeline.

### 2. Packet Structure Analysis

- **TOC byte:** Matches between gopus (0xF8) and libopus (0xF8)
  - Config = 31 (CELT Fullband 20ms)
  - Stereo = 0 (mono)
  - Frame code = 0 (single frame)

- **Frame data comparison:**
  - First 7 bytes: MATCH (coarse energy correct)
  - Bytes 7+: DIVERGE (TF/PVQ/fine energy differ)

### 3. CWRS Encoding/Decoding

Tested CWRS roundtrip - works correctly. Signs are preserved through encode/decode cycle.

### 4. MDCT Implementation

MDCT forward and inverse match libopus with SNR > 138 dB. Not the source of inversion.

### 5. Current Investigation Status

The inversion occurs somewhere between:
- Band normalization
- PVQ quantization
- Range coding

The first 7 bytes matching suggests:
- Coarse energy encoding is correct
- Silence/transient/intra flags are correct

The divergence at byte 7+ suggests:
- TF resolution encoding differs
- OR PVQ band encoding differs
- OR fine energy encoding differs

## Next Steps

1. Compare TF resolution encoding between gopus and libopus
2. Trace PVQ pulse vectors for first few bands
3. Compare range encoder state at key checkpoints
4. Consider if the issue is cumulative state divergence

## Test Commands

```bash
# Run PVQ sign test
go test -v -run "TestPVQSignPreservation" ./internal/celt/cgo_test/

# Run packet format comparison
go test -v -run "TestPacketFormatComparison" ./internal/celt/cgo_test/

# Run encoder divergence analysis
go test -v -run "TestEncoderDivergenceAnalysis" ./internal/celt/cgo_test/
```

## Related Files

- `/Users/thesyncim/GolandProjects/gopus/internal/celt/pvq_search.go` - PVQ search (fixed)
- `/Users/thesyncim/GolandProjects/gopus/internal/celt/bands_quant.go` - Band quantization
- `/Users/thesyncim/GolandProjects/gopus/internal/celt/encode_frame.go` - Frame encoding pipeline
- `/Users/thesyncim/GolandProjects/gopus/internal/celt/cwrs.go` - CWRS encoding

## Remaining Investigation

The signal inversion persists after the PVQ search fix. Further investigation is needed in:

1. **TF Resolution Encoding** - The time-frequency resolution may be encoded differently
2. **Bit Allocation** - The allocation algorithm may produce different results
3. **Fine Energy Encoding** - Fine energy quantization may differ
4. **Range Encoder State** - Cumulative state differences may cause bitstream divergence

The first 8 bytes of encoded data match between gopus and libopus, suggesting:
- Coarse energy encoding is correct
- Header flags (silence, transient, intra) are correct

Divergence starts at byte 8, which corresponds to:
- TF resolution data
- OR start of spread/dynalloc encoding
- OR fine energy bits

## Decoder Status

The decoder continues to pass 7/12 compliance tests. The PVQ search fix does not affect decoder behavior since `opPVQSearch` is only used in the encoder path.
