# CELT Encoder Byte 7 Divergence Analysis

## Problem Summary

When encoding a 440 Hz sine wave at 64kbps, 48kHz, mono, 20ms frames:
- gopus produces: `7B 5E 09 50 B7 8C 08 33 22 8B D0 8D...`
- libopus produces: `7B 5E 09 50 B7 8C 08 D0 33 BA 79 2B...`

The first **7 bytes match perfectly**, then diverge at byte 7:
- libopus byte 7: `0xD0` = `11010000`
- gopus byte 7: `0x33` = `00110011`

## Self-decode Quality

When the gopus-encoded packet is decoded by libopus:
- SNR: **-3.89 dB** (negative! severe distortion)
- Correlation: **-0.54** (inverted signal!)
- Energy ratio: **0.61** (significant energy loss)

## Root Cause Analysis

### Packet Structure (at bit level)

1. **Header flags** (bits 1-9):
   - Silence (logp=15): 0
   - Postfilter (logp=1): 0
   - Transient (logp=3): 1 (transient detected)
   - Intra (logp=3): 1 (first frame)
   - Total: 9 bits

2. **Coarse energy** (bits 9-50):
   - Laplace-encoded qi values for 21 bands
   - Ends at approximately bit 50 (~byte 6.25)

3. **TF encoding** (bits 50-58):
   - Time-frequency resolution flags per band
   - This is where byte 7 diverges!
   - Starts in the middle of byte 6, affects byte 7

4. **Spread decision** (after TF)

### Key Findings

1. **Header matches**: Range encoder state after header flags is identical between gopus and libopus.

2. **Coarse energy qi values match**: When tested in isolation, the qi values computed by gopus match libopus.

3. **TF encoding diverges**: The TF (time-frequency) resolution encoding produces different bits.

### TF Analysis Investigation

The gopus TF analysis produces:
- tfSelect: 1
- tfRes: [0,0,0,0,0,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1]

The TF encoding uses run-length encoding:
- First band uses logp=2 (transient mode)
- Subsequent bands use logp=4

The divergence at byte 7 indicates the TF encoding is producing different bit sequences.

## Potential Causes

1. **TFAnalysis computes different values** than libopus:
   - The Haar transform or L1 metric computation may differ
   - The Viterbi search may use different parameters
   - The importance weights may differ

2. **TFEncodeWithSelect has bugs**:
   - The bit encoding order may be wrong
   - The tfSelect bit placement may be incorrect

3. **Pre-processing differences**:
   - DC rejection
   - Delay buffer handling
   - Pre-emphasis differences
   - These affect the MDCT coefficients used for TF analysis

## Verification Steps Needed

1. Compare normalized MDCT coefficients between gopus and libopus
2. Compare TF metric computation step-by-step
3. Trace TF encoding bit-by-bit
4. Verify tfSelect encoding position

## Impact on Decoder Tests

The current 7/12 decoder tests passing status should NOT be affected by encoder fixes, as the decoder is independent. However, encoder fixes are needed for:
- Round-trip encoding/decoding tests
- Compliance with libopus

## Files Involved

- `/Users/thesyncim/GolandProjects/gopus/internal/celt/tf.go` - TF analysis and encoding
- `/Users/thesyncim/GolandProjects/gopus/internal/celt/encode_frame.go` - Main encoding pipeline
- `/Users/thesyncim/GolandProjects/gopus/internal/celt/energy_encode.go` - Coarse/fine energy encoding
