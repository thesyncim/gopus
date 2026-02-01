---
status: resolved
trigger: "Investigate and fix MDCT coefficient normalization before PVQ encoding in gopus"
created: 2026-02-01T12:00:00Z
updated: 2026-02-01T14:30:00Z
---

## Current Focus

hypothesis: CONFIRMED - gopus normalizes MDCT coefficients using log-domain energies converted back to linear, but libopus uses linear amplitudes directly
test: Implemented fix and verified with tests
expecting: All normalization tests pass, each band has L2 norm = 1.0
next_action: COMPLETE - Fix committed to fix-agent-37 branch

## Symptoms

expected: Encoded packets produce good quality audio when decoded by libopus
actual: SNR is -1 to -4 dB (very poor quality), packets decode but sound bad
errors: None (bitstream is valid, just wrong content)
reproduction: Encode any audio with gopus, decode with libopus, measure SNR
started: Discovered by Agent 34 during UAT

## Evidence

- timestamp: 2026-02-01T12:00:00Z
  checked: libopus bands.c normalise_bands() function (lines 172-187)
  found: |
    libopus uses linear amplitude (bandE) directly:
    ```c
    opus_val16 g = 1.f/(1e-27f+bandE[i+c*m->nbEBands]);
    X[j+c*N] = freq[j+c*N]*g;
    ```
    bandE is celt_ener type = linear amplitude (sqrt of sum of squares)
  implication: Normalization divisor is original linear amplitude, not reconstructed from log

- timestamp: 2026-02-01T12:01:00Z
  checked: gopus bands_encode.go NormalizeBandsToArray() function
  found: |
    gopus converts log-domain energy back to linear:
    ```go
    eVal := energies[band]  // log2(amplitude) - eMeans[band]
    eVal += eMeans[band] * DB6  // Add back eMeans
    gain := math.Exp2(eVal / DB6)  // Convert log2 to linear
    norm[offset+i] = mdctCoeffs[offset+i] / gain
    ```
    This introduces floating-point roundtrip errors.
  implication: Normalization uses reconstructed linear value, not original

- timestamp: 2026-02-01T12:02:00Z
  checked: libopus celt_encoder.c encoding pipeline
  found: |
    Line 2096: compute_band_energies(mode, freq, bandE, ...)  -> linear amplitude
    Line 2106: amp2Log2(mode, ..., bandE, bandLogE, ...)     -> log domain for encoding
    Line 2240: normalise_bands(mode, freq, X, bandE, ...)    -> uses LINEAR bandE
    Line 2670: quant_all_bands(..., bandE, ...)              -> uses LINEAR bandE
  implication: libopus maintains separate linear and log representations

- timestamp: 2026-02-01T12:03:00Z
  checked: gopus encode_frame.go lines 313-328
  found: |
    gopus already computes linear bandE:
    ```go
    bandE := make([]float64, nbBands*e.channels)
    for c := 0; c < e.channels; c++ {
        for band := 0; band < nbBands; band++ {
            eVal := energies[idx]
            if band < len(eMeans) {
                eVal += eMeans[band] * DB6
            }
            bandE[idx] = math.Exp2(eVal / DB6)  // Linear amplitude
        }
    }
    ```
    But this is computed AFTER quantization of energies!
  implication: gopus computes bandE from quantized energies, not original

- timestamp: 2026-02-01T14:30:00Z
  checked: Fix implementation and tests
  found: |
    Implemented ComputeLinearBandAmplitudes() to compute sqrt(sum of squares) directly.
    Updated NormalizeBandsToArray() to use direct linear amplitudes.
    Updated encode_frame.go to use ComputeLinearBandAmplitudes() for bandE.
    All normalization tests pass - each band has L2 norm = 1.0.
  implication: Fix correctly matches libopus behavior

## ROOT CAUSE

The normalization in gopus uses the WRONG energy values:

1. **libopus flow**:
   - `compute_band_energies()` -> linear amplitude (`bandE`)
   - `amp2Log2()` -> log-domain (`bandLogE`) for coarse/fine encoding
   - `normalise_bands(freq, X, bandE)` -> uses ORIGINAL linear `bandE`

2. **gopus flow**:
   - `ComputeBandEnergies()` -> returns log-domain directly
   - `EncodeCoarseEnergy()` -> quantizes log-domain energies
   - `NormalizeBandsToArray()` -> converts quantized log back to linear

The problem: gopus normalizes with **quantized** energies, but libopus normalizes with **original** (unquantized) linear amplitudes.

This causes:
- Different normalized coefficient values
- Different PVQ pulse patterns encoded
- Decoder reconstructs different shapes
- Poor audio quality

## Resolution

root_cause: |
  gopus normalizes MDCT coefficients using energies reconstructed from log-domain (after quantization),
  but libopus normalizes using the original linear band amplitudes computed directly from MDCT coefficients.
  The log->linear roundtrip and quantization both introduce errors that corrupt the normalized coefficients.

fix: |
  1. Added ComputeLinearBandAmplitudes() to compute sqrt(sum of squares) directly from MDCT coefficients
  2. Updated NormalizeBandsToArray() to use direct linear amplitudes
  3. Updated encode_frame.go bandE computation to use ComputeLinearBandAmplitudes()
  4. Used float32 precision for amplitude computation to match libopus

verification: |
  - TestNormalizeBandsToArrayUnitNorm: PASS (all bands have L2 norm = 1.0)
  - TestComputeLinearBandAmplitudes: PASS
  - TestNormalizationUsesLinearAmplitudes: PASS
  - TestNormalizationRoundTrip: PASS
  - All encoder tests: PASS
  - All decoder tests: PASS (no regressions)

files_changed:
  - internal/celt/bands_encode.go
  - internal/celt/encode_frame.go
  - internal/celt/normalization_test.go

commit: 476a14b (fix-agent-37 branch)
