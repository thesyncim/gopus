# Gopus Encoder Investigation Summary

## Current Status
The gopus encoder produces packets that decode to incorrect audio, resulting in Q ≈ -100 to -106 (SNR ≈ -3 to 0 dB) when the passing threshold is Q ≥ 0 (SNR ≥ 48 dB).

## Component Testing Results (All Pass)

| Component | Test Result | Details |
|-----------|-------------|---------|
| MDCT | 138+ dB SNR vs libopus | Byte-level accurate |
| PVQ Search | 440/440 exact matches | Pulse vectors match |
| Range Encoder | Byte-perfect | Uniform encoding matches |
| Normalization | Correct unit-norm | Bands properly normalized |
| Coarse Energy | Decodes correctly | Round-trip verified |

## Key Finding: Alignment Analysis

| Encoder | Best Delay | Correlation | Aligned SNR |
|---------|------------|-------------|-------------|
| libopus | +421 samples | 0.9998 | 33.25 dB |
| gopus | -484 samples | 0.5042 | -0.44 dB |

**Critical Observation**: gopus has a NEGATIVE delay while libopus has POSITIVE delay. This ~905 sample difference (almost 1 frame) suggests an off-by-one-frame issue or signal inversion in the encoding pipeline.

## Root Cause Hypothesis

Despite all individual components passing their tests, the integration produces wrong output. Possible causes:

1. **MDCT Overlap Mismatch**: The history buffer might not align correctly with the decoder's expectation
2. **Energy Encoding Mismatch**: The quantized energies might not match what the decoder expects
3. **Sign Inversion**: There may be a sign flip somewhere in the signal path
4. **Frame Timing**: The encoding might be off by one frame in how it processes the input

## Previous Fixes Applied

- **VBR floor_depth bug**: Fixed formula that was clamping VBR target to 1/4 for all signals instead of just quiet signals

## Test Files Created

- `internal/celt/cgo_test/multifreq_delay_test.go` - Tests delay alignment for multi-frequency signals
- `internal/celt/cgo_test/gopus_alignment_test.go` - Compares gopus vs libopus alignment
- `internal/celt/cgo_test/alignment_test.go` - Tests libopus encode-decode alignment
- `internal/celt/cgo_test/encoder_divergence_test.go` - Detailed comparison of packets
- `internal/celt/cgo_test/full_encode_trace_test.go` - Traces encoding steps

## Recommended Next Steps

1. **Trace-level debugging**: Add detailed logging to compare gopus vs libopus at each encoding step:
   - Flag bits
   - Coarse energy values
   - Fine energy values
   - PVQ pulses per band
   - Final range encoder state

2. **Check denormalization**: Verify that the decoder receives correct energy values to properly scale the coefficients

3. **Signal path verification**: Check for any sign flips or index errors in:
   - Pre-emphasis
   - MDCT windowing
   - Band normalization
   - Coefficient quantization

4. **Frame alignment**: Verify the MDCT overlap history buffer is being used correctly across frame boundaries

## Notes

- The delay compensation function was added to `internal/testvectors/quality.go`
- The compliance test was updated to use delay-compensated quality measurement
- All individual component tests pass but the integrated encoder fails
