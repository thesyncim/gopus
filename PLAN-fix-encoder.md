# Plan: Fix gopus Encoder

## Summary

The gopus encoder currently produces invalid output (0/80 configurations pass compliance tests). This plan addresses 10 identified issues by fixing them in priority order, with the libopus implementation at `tmp_check/opus-1.6.1` as reference.

---

## Phase 1: Critical Bitstream Alignment Fixes

These must be fixed first - they cause the bitstream to become unreadable.

### Issue 3: Skip Bit Stream Misalignment
- [x] **Fix `alloc.go` lines 693-699**: Encoder only encodes skip bit when NOT skipping, but decoder always reads it
- [x] Match libopus behavior: `clt_compute_allocation()` in `rate.c` encodes skip bit unconditionally in the loop
- [ ] Reference: `tmp_check/opus-1.6.1/celt/rate.c` lines 500-530
- [x] Add test: verify encoder/decoder produce same bit consumption per band

### Issue 8: Skip Bit Semantics Reversed
- [x] **Fix skip bit polarity in `alloc.go`**: Encoder encodes `1` for "keep", decoder reads `1` as "skip"
- [x] Align semantics with libopus: `1` = keep band (continue), `0` = skip remaining bands
- [ ] Reference: `tmp_check/opus-1.6.1/celt/rate.c` line 519 `ec_dec_bit_logp()`

---

## Phase 2: Critical Energy Encoding Fixes

Energy mismatch causes completely wrong audio magnitude.

### Issue 1: eMeans Double-Subtraction
- [x] **Fix `energy_encode.go` lines 86-95**: Energy is mean-relative but then eMeans added back during normalization
- [x] Solution: Either keep energies absolute everywhere, or ensure consistent subtraction/addition
- [ ] Reference: `tmp_check/opus-1.6.1/celt/quant_bands.c` `quant_coarse_energy_impl()` - uses raw log energies, eMeans applied separately
- [ ] Verify: Compare `bandLogE` values with libopus for same input

### Issue 6: Energy State Updated Mid-Encoding
- [x] **Fix `energy_encode.go` line 259**: `e.prevEnergy` modified during `EncodeCoarseEnergy()` but `prev1LogE` captured before
- [x] Solution: Don't modify state during encoding; update state after encoding completes
- [x] Move `e.prevEnergy[c*MaxBands+band] = quantizedEnergy` to happen AFTER encoding loop
- [ ] Reference: `tmp_check/opus-1.6.1/celt/quant_bands.c` lines 260-270 - state update after loop

### Issue 9: Normalization Using Wrong Energy Domain
- [x] **Fix `bands_encode.go` lines 59-62**: Ensure normalization uses consistent energy domain
- [x] Verify gain computation matches decoder's `denormalize_bands()` exactly
- [ ] Reference: `tmp_check/opus-1.6.1/celt/bands.c` `denormalise_bands()`

---

## Phase 3: Frame State Management Fixes

These cause inter-frame prediction to fail.

### Issue 2: Frame Counter Incremented Twice
- [x] **Fix `encode_frame.go` line 233**: Remove duplicate `e.frameCount++`
- [x] Verify: intra mode used only for frame 0, inter mode for all subsequent frames
- [x] Add test: encode 5 frames, verify frame 0 is intra, frames 1-4 are inter

### Issue 7: Overlap Buffer Not Persisted
- [x] **Fix `encode_frame.go` lines 72-108**: Store updated overlap buffer back to encoder state
- [x] Add `e.overlapBuffer` persistent field to encoder struct
- [x] Update after MDCT: `copy(e.overlapBuffer, updatedHistory)`
- [ ] Reference: `tmp_check/opus-1.6.1/celt/celt_encoder.c` lines 1937-2021 - maintains `in_mem[]`

---

## Phase 4: Stereo Encoding Fixes

These cause stereo audio to be corrupted.

### Issue 4: Intensity Stereo State Mismatch
- [x] **Fix `alloc.go` lines 716-743**: Align intensity/dualStereo encoding with decoder expectations
- [x] Ensure `intensityRsv` and `dualStereoRsv` computed same as decoder
- [ ] Reference: `tmp_check/opus-1.6.1/celt/rate.c` `clt_compute_allocation()` lines 580-620

### Issue 5: Missing Stereo Parameter Encoding
- [x] **Add `EncodeStereoParams()` call to `encode_frame.go`**:
  - After coarse energy, before allocation
  - Encode intensity band index (if stereo)
  - Encode dual_stereo flag
- [x] Match decoder's `decodeStereoParams()` exactly
- [ ] Reference: `tmp_check/opus-1.6.1/celt/celt_encoder.c` lines 2302-2340
  - Note: main CELT path encodes intensity/dual_stereo inside allocation (uniform). Decoder does not call `DecodeStereoParams()` in that path, so no separate stereo params are emitted.

---

## Phase 5: Configuration and Quality Fixes

### Issue 10: Missing Bitrate Configuration
- [x] **Fix `encode_frame.go` `computeTargetBits()`**: Provide sensible defaults
- [x] Default bitrate: 64kbps mono, 128kbps stereo
- [x] Validate bitrate range: 6kbps - 510kbps per RFC 6716
- [ ] Reference: `tmp_check/opus-1.6.1/celt/celt_encoder.c` lines 1725-1850 bitrate handling

---

## Phase 6: Residual Quantization Accuracy (In Progress)

### Issue 11: Missing quant_all_bands Encoder Path
- [x] **Add encoder-side `quant_all_bands` plumbing** (Go port, simplified)
  - Added `quantAllBandsEncode()` (mirrors decode loop)
  - Added encode path for `quantBand`/`quantPartition`/`computeTheta`
  - Added encode-time TF mapping + normalization using original energies
- [x] **Replace simplified PVQ quantization** (`vectorToPulses`) with libopus-style `op_pvq_search` (float path)
  - Note: QEXT/extra-bits branches now implemented but not wired to an ext payload (no QEXT packets yet)
- [ ] **Implement full stereo mid/side path** (proper `stereo_split`, `intensity_stereo`, theta biasing)
  - [x] Mid/side enabled by default (dualStereo=0), encode-side `stereo_split`/`intensity_stereo` wired
  - [x] Added energy-weighted intensity using per-band amplitudes (approx. libopus bandE)
  - [ ] Still missing theta RDO biasing
- [ ] **Enable TF analysis / spread decision** (currently fixed defaults)

---

## Additional Alignment (Implemented)

- [x] Encode anti-collapse flag (reserved bit when applicable)
- [x] Encode energy finalization bits using leftover budget

---

## Verification

After each phase, run:

```bash
# Quick test (4 configs)
go test ./internal/celt/cgo_test -run "TestEncoderComplianceQuick_CGO" -v

# Full summary (80 configs)
go test ./internal/celt/cgo_test -run "TestEncoderComplianceSummary_CGO" -v

# Compare with existing opusdec test
go test ./internal/testvectors -run "TestEncoderComplianceSummary" -v
```

**Success criteria per phase:**
- Phase 1: Tests no longer panic, bitstream parses
- Phase 2: Q values improve from ~-100 to ~-50 range
- Phase 3: Multi-frame encoding stabilizes, no degradation over time
- Phase 4: Stereo tests pass (currently all fail)
- Phase 5: Q >= 0 (48 dB SNR) for all configurations

---

## Key Reference Files

**libopus (in tmp_check/opus-1.6.1/):**
- `celt/celt_encoder.c` - Main encoder
- `celt/quant_bands.c` - Energy quantization
- `celt/rate.c` - Bit allocation
- `celt/bands.c` - PVQ band encoding
- `celt/entenc.c` - Range encoder

**gopus (to fix):**
- `internal/celt/encode_frame.go` - Main encoder frame
- `internal/celt/energy_encode.go` - Energy encoding
- `internal/celt/alloc.go` - Bit allocation
- `internal/celt/bands_encode.go` - Band normalization
- `internal/rangecoding/encoder.go` - Range encoder
