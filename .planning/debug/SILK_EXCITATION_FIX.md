# SILK Excitation Quantization Fix

## Date: 2026-01-30

## Summary

Fixed a critical scaling issue in the SILK encoder's excitation quantization that was causing near-zero pulse magnitudes.

## The Problem

### Root Cause

The `computeExcitation()` function in `/internal/silk/excitation_encode.go` was computing the LPC residual (excitation signal) on normalized float32 PCM samples in the range [-1, 1], then directly quantizing to integers:

```go
// BEFORE (WRONG):
residual := float64(pcm[i]) - prediction
excitation[i] = int32(math.Round(residual))
```

This produced mostly zeros because:
- Input PCM values were in range [-1, 1] (e.g., 0.3, -0.2)
- Prediction values were similarly small
- Residuals were typically < 0.5
- `math.Round()` of values < 0.5 produces 0

### libopus Reference

In libopus (`silk/NSQ.c`), the input is `opus_int16` (Q0 format, range -32768 to 32767):

```c
// silk_nsq_scale_states()
for(i = 0; i < psEncC->subfr_length; i++) {
    x_sc_Q10[i] = silk_SMULWW(x16[i], inv_gain_Q26);
}
```

The key insight: libopus works with Q0 integers throughout the encoding process, not normalized floats.

## The Fix

Scale the PCM input by 32768 (2^15) before computing the residual to convert from normalized float32 to Q0 integer range:

```go
// AFTER (CORRECT):
const q0Scale = 32768.0

for i := 0; i < n; i++ {
    // Scale input to Q0 range
    inputQ0 := float64(pcm[i]) * q0Scale

    // Compute LPC prediction in Q0 scale
    var prediction float64
    for k := 0; k < order && i-k-1 >= 0; k++ {
        prevInputQ0 := float64(pcm[i-k-1]) * q0Scale
        prediction += float64(lpcQ12[k]) * prevInputQ0 / 4096.0
    }

    // Residual = scaled_input - prediction (both in Q0 scale)
    residual := inputQ0 - prediction
    excitation[i] = int32(math.Round(residual))
}
```

## Test Verification

Created CGO test in `/internal/celt/cgo_test/silk_excitation_compare_test.go` that demonstrates:

1. **TestSILKResidualScalingComparison** shows the difference:
   - Wrong method (no scaling): 0 non-zero residuals out of 80 samples
   - Correct method (with 32768 scaling): 79 non-zero residuals out of 80 samples

2. **TestExcitationEncoding** output after fix:
   - Max excitation magnitude: 29088 (previously would have been near 0)

## Files Changed

1. `/internal/silk/excitation_encode.go` - Fixed `computeExcitation()` function
2. `/internal/celt/cgo_test/silk_excitation_compare_test.go` - Added CGO comparison test

## Testing

All existing tests pass:
- `go test ./internal/silk/...` - All PASS
- Decoder tests unaffected
- Encoder tests now produce proper excitation magnitudes

## Impact

- Encoded SILK frames now have proper excitation pulse magnitudes
- This should significantly improve audio quality for SILK-encoded streams
- Decoder is unaffected (this was encoder-only bug)

## Notes

The scaling factor 32768 (2^15) converts normalized [-1, 1] float to int16 range [-32768, 32767], matching libopus's Q0 integer representation.

This matches the "Q0 PCM integers" representation mentioned in RFC 6716 and the libopus implementation.
