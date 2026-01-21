# Project State

## Project Reference

See: .planning/PROJECT.md (updated 2026-01-21)

**Core value:** Correct, pure-Go Opus encoding and decoding that passes official test vectors - no cgo, no external dependencies.
**Current focus:** Phase 4: Hybrid Decoder - IN PROGRESS

## Current Position

Phase: 4 of 12 (Hybrid Decoder)
Plan: 1 of 2 in current phase - COMPLETE
Status: In progress
Last activity: 2026-01-21 - Completed 04-01-PLAN.md (Hybrid Decoder Foundation)

Progress: [██████████████████████████████████████░░░] ~38% (14/37 plans)

## Performance Metrics

**Velocity:**
- Total plans completed: 14
- Average duration: ~8 minutes
- Total execution time: ~115 minutes

**By Phase:**

| Phase | Plans | Total | Avg/Plan |
|-------|-------|-------|----------|
| 01-foundation | 3/3 | ~29m | ~10m |
| 02-silk-decoder | 5/5 | ~31m | ~6m |
| 03-celt-decoder | 5/5 | ~50m | ~10m |
| 04-hybrid-decoder | 1/2 | ~5m | ~5m |

**Recent Trend:**
- Last 5 plans: 03-02 (~12m), 03-03 (~12m), 03-04 (~12m), 03-05 (~10m), 04-01 (~5m)
- Trend: Hybrid plan faster due to reuse of existing SILK/CELT components

*Updated after each plan completion*

## Accumulated Context

### Decisions

Decisions are logged in PROJECT.md Key Decisions table.
Recent decisions affecting current work:

| ID | Decision | Phase | Impact |
|----|----------|-------|--------|
| D01-01-01 | Set nbitsTotal before normalize() | 01-01 | Matches libopus initialization |
| D01-02-01 | Encoder follows libopus structure | 01-02 | RFC 6716 compliance |
| D01-02-02 | Round-trip verification deferred | 01-02 | Known gap, tracked |
| D01-03-01 | Config table as fixed [32]configEntry array | 01-03 | O(1) lookup by config index |
| D01-03-02 | ParseFrameLength as internal helper | 01-03 | Two-byte encoding reused in Code 2 and Code 3 |
| D02-01-01 | ICDF tables use uint16 (256 overflows uint8) | 02-01 | Added DecodeICDF16 to range decoder |
| D02-01-02 | Export ICDF tables with uppercase names | 02-01 | Package access for parameter decoding |
| D02-02-01 | Direct polynomial method for LSF-to-LPC | 02-02 | Clearer than Chebyshev recursion |
| D02-03-01 | LPC chirp factor 0.96 for aggressive bandwidth expansion | 02-03 | Faster convergence for stability |
| D02-03-02 | LCG noise fill for zero excitation positions | 02-03 | Comfort noise with deterministic output |
| D02-04-01 | Stereo prediction weights in Q13 format | 02-04 | Per RFC 6716 Section 4.2.8 |
| D02-04-02 | 40/60ms frames as multiple 20ms sub-blocks | 02-04 | Simplified decode logic |
| D02-05-01 | Linear interpolation for upsampling | 02-05 | Sufficient for v1; polyphase deferred |
| D02-05-02 | Float32 intermediate format for Decode API | 02-05 | int16 via wrapper |
| D03-01-01 | Initialize prevEnergy to -28dB | 03-01 | Low but finite starting energy |
| D03-01-02 | RNG seed 22222 | 03-01 | Matches libopus convention |
| D03-01-03 | Linear energy array indexing | 03-01 | channel*MaxBands + band layout |
| D03-02-01 | V(1,K) = 2 for K > 0 | 03-02 | Only +K and -K valid for N=1 |
| D03-02-02 | Map-based V cache with uint64 key | 03-02 | Efficient memoization |
| D03-02-03 | Interleaved sign bits in decoding | 03-02 | Matches libopus CWRS scheme |
| D03-04-01 | DecodeUniform added to range decoder | 03-04 | Required for PVQ index decoding |
| D03-04-02 | bitsToK uses binary search with V(n,k) | 03-04 | Accurate conversion from bit allocation |
| D03-04-03 | FoldBand uses LCG constants 1664525/1013904223 | 03-04 | Matches libopus for deterministic folding |
| D03-04-04 | Stereo uses 8-step theta quantization | 03-04 | Balance between precision and bit cost |
| D03-05-01 | Direct IMDCT for CELT sizes (120,240,480,960) | 03-05 | Non-power-of-2 sizes handled correctly |
| D03-05-02 | Window computed over 2*overlap samples | 03-05 | Matches CELT's fixed 120-sample overlap |
| D03-05-03 | De-emphasis filter coefficient 0.85 | 03-05 | Matches libopus PreemphCoef constant |
| D04-01-01 | Zero bands 0-16 in CELT hybrid mode | 04-01 | Simpler than true band-limited decoding |
| D04-01-02 | Linear interpolation for 3x upsampling | 04-01 | Consistent with SILK upsampling approach |
| D04-01-03 | Delay compensation via shift buffer | 04-01 | 60 samples per channel for SILK-CELT alignment |

### Pending Todos

- Continue Phase 04 (plan 02 remaining)

### Known Gaps

- **Encoder-decoder round-trip:** Encoder produces valid output but exact byte format matching with decoder needs additional work. Does not block SILK/CELT implementation.

### Blockers/Concerns

None.

## Session Continuity

Last session: 2026-01-21
Stopped at: Completed 04-01-PLAN.md (Hybrid Decoder Foundation)
Resume file: .planning/phases/04-hybrid-decoder/04-02-PLAN.md (if exists)

## Phase 01 Summary

**Foundation phase complete:**
- Range decoder: Implemented and tested (96.2% coverage)
- Range encoder: Implemented and tested (90.7% combined coverage)
- TOC and packet parsing: Implemented and tested
- All tests passing

**Key artifacts:**
- `internal/rangecoding/decoder.go` - Range decoder
- `internal/rangecoding/encoder.go` - Range encoder
- `internal/rangecoding/constants.go` - EC_CODE_* constants
- `internal/packet/toc.go` - TOC parsing
- `internal/packet/packet.go` - Packet structure and parsing

## Phase 02 Summary - COMPLETE

**SILK Decoder phase complete:**
- All 5 plans executed successfully
- Total duration: ~31 minutes
- 46 tests passing

**02-01 SILK Foundation complete:**
- ICDF probability tables: 47 tables for entropy decoding
- Codebook tables: 20+ tables for LSF/LTP reconstruction
- Bandwidth config: NB (8kHz), MB (12kHz), WB (16kHz) with LPC orders
- Decoder struct: State management for frame persistence

**02-02 SILK Parameter Decoding complete:**
- FrameParams struct: holds all decoded frame parameters
- Frame type decoding: signal type (inactive/unvoiced/voiced) + quant offset
- Gain decoding: Q16 gains with delta coding between subframes
- LSF decoding: two-stage VQ with interpolation and stabilization
- LSF-to-LPC: Chebyshev polynomial conversion to Q12 LPC coefficients
- Pitch lag decoding: per-subframe lags with contour deltas
- LTP coefficient decoding: 5-tap Q7 filters by periodicity
- Unit tests: 10 tests covering tables, structs, and state

**02-03 SILK Excitation and Synthesis complete:**
- Excitation decoding: shell coding with recursive binary splits
- LTP synthesis: 5-tap pitch prediction filter for voiced frames
- LPC synthesis: all-pole filter with state persistence
- Stability: limitLPCFilterGain with iterative bandwidth expansion
- Unit tests: 11 tests covering all synthesis components

**02-04 SILK Stereo and Frame Orchestration complete:**
- Stereo prediction weight decoding (Q13 format)
- Mid-side to left-right unmixing with prediction
- Frame duration handling (10/20/40/60ms)
- DecodeFrame orchestration for mono decoding
- DecodeStereoFrame orchestration for stereo decoding
- 40/60ms frames as multiple 20ms sub-blocks
- Unit tests: 11 tests covering stereo and frame handling

**02-05 SILK Public API and Integration Tests complete:**
- Public Decode() API returning 48kHz float32 PCM
- DecodeStereo() with interleaved stereo output
- DecodeToInt16() convenience wrapper
- Linear interpolation upsampling (6x/4x/3x for NB/MB/WB)
- BandwidthFromOpus() conversion utility
- 14 new tests + benchmarks (46 total in silk package)

**Key artifacts:**
- `internal/silk/tables.go` - ICDF tables (uint16)
- `internal/silk/codebook.go` - LSF and LTP codebooks
- `internal/silk/bandwidth.go` - Bandwidth configuration
- `internal/silk/decoder.go` - Decoder struct with state
- `internal/silk/decode_params.go` - FrameParams, DecodeFrameType
- `internal/silk/gain.go` - Gain decoding
- `internal/silk/lsf.go` - LSF decoding and LSF-to-LPC
- `internal/silk/pitch.go` - Pitch and LTP decoding
- `internal/silk/excitation.go` - Shell-coded excitation decoding
- `internal/silk/ltp.go` - Long-term prediction synthesis
- `internal/silk/lpc.go` - LPC synthesis filter
- `internal/silk/stereo.go` - Stereo prediction and unmixing
- `internal/silk/frame.go` - Frame duration handling
- `internal/silk/decode.go` - Top-level decode orchestration
- `internal/silk/silk.go` - Public API (Decode, DecodeStereo, DecodeToInt16)
- `internal/silk/resample.go` - Upsampling to 48kHz
- `internal/silk/silk_test.go` - Integration tests and benchmarks
- `internal/silk/decode_params_test.go` - Parameter decoding tests
- `internal/silk/excitation_test.go` - Synthesis tests
- `internal/silk/stereo_test.go` - Stereo and frame tests

## Phase 03 Summary - COMPLETE

**CELT Decoder phase complete:**
- All 5 plans executed successfully
- Total duration: ~50 minutes
- 61 tests passing

**03-01 CELT Foundation complete:**
- Static tables: eBands[22], AlphaCoef[4], BetaCoef[4], LogN[21], SmallDiv[129]
- Mode configuration: ModeConfig struct for 120/240/480/960 sample frames
- Decoder struct: Energy, overlap, postfilter state with proper sizing
- CELTBandwidth enum: NB/MB/WB/SWB/FB with band count mapping
- Unit tests: 6 tests for modes, bandwidth, decoder, and tables

**03-02 CWRS Combinatorial Indexing complete:**
- PVQ_V function: codebook size via V(N,K) recurrence with memoization
- DecodePulses: CWRS index to pulse vector with interleaved sign bits
- EncodePulses: round-trip testing utility
- V(1,K) = 2 base case for correct PVQ counting
- Unit tests: 7 tests for V, U, decode, sum property, round-trip, benchmarks

**03-03 Energy Decoding and Bit Allocation complete:**
- Coarse energy decoding with Laplace distribution
- Fine energy decoding with uniform distribution
- Energy remainder decoding for leftover bits
- Bit allocation algorithm (compute_allocation equivalent)
- Unit tests: Energy decoding, allocation, and prediction tests

**03-04 PVQ Band Decoding complete:**
- PVQ vector decoding with CWRS index conversion
- NormalizeVector for L2 unit norm
- Band folding for uncoded bands with sign variation
- DecodeBands/DecodeBandsStereo orchestration
- Collapse mask tracking for anti-collapse
- DecodeUniform/DecodeRawBits added to range decoder
- Unit tests: Normalization, folding, bits-to-K, collapse mask

**03-05 IMDCT Synthesis complete:**
- IMDCT: Direct computation for CELT non-power-of-2 sizes
- IMDCTShort: Multiple short MDCTs for transient frames
- VorbisWindow: Power-complementary windowing
- OverlapAdd: Seamless frame concatenation
- Synthesize/SynthesizeStereo: Complete synthesis pipeline
- MidSideToLR/IntensityStereo: Stereo channel separation
- DecodeFrame: Complete frame decoding orchestration
- De-emphasis filter: Natural sound reconstruction
- Unit tests: 22 new tests, total 61 in CELT package

**Key artifacts:**
- `internal/celt/tables.go` - eBands, energy coefficients, logN, smallDiv
- `internal/celt/modes.go` - ModeConfig, GetModeConfig, CELTBandwidth
- `internal/celt/decoder.go` - Stateful decoder with DecodeFrame() API
- `internal/celt/cwrs.go` - PVQ_V, DecodePulses, EncodePulses, memoization
- `internal/celt/energy.go` - Coarse/fine energy decoding, bit allocation
- `internal/celt/alloc.go` - Bit allocation computation
- `internal/celt/pvq.go` - DecodePVQ, NormalizeVector, theta decoding
- `internal/celt/bands.go` - DecodeBands, bitsToK, denormalization
- `internal/celt/folding.go` - FoldBand, collapse mask tracking
- `internal/celt/mdct.go` - IMDCT, IMDCTShort, IMDCTDirect
- `internal/celt/window.go` - VorbisWindow, precomputed buffers
- `internal/celt/stereo.go` - MidSideToLR, IntensityStereo, GetStereoMode
- `internal/celt/synthesis.go` - OverlapAdd, Synthesize, SynthesizeStereo
- `internal/celt/cwrs_test.go` - Comprehensive CWRS tests
- `internal/celt/modes_test.go` - Mode and decoder tests
- `internal/celt/energy_test.go` - Energy and allocation tests
- `internal/celt/bands_test.go` - Band processing tests
- `internal/celt/mdct_test.go` - IMDCT, window, synthesis, integration tests

## Phase 04 Summary - IN PROGRESS

**Hybrid Decoder phase started:**
- Plan 04-01 complete: Hybrid Decoder Foundation
- Duration: ~5 minutes
- 15 tests passing (68.9% coverage)

**04-01 Hybrid Decoder Foundation complete:**
- Decoder struct coordinating SILK (WB) and CELT sub-decoders
- DecodeFrameHybrid added to CELT for band-limited decoding (bands 17-21)
- 60-sample delay compensation for SILK-CELT time alignment
- 3x upsampling from SILK 16kHz to 48kHz output
- Public Decode/DecodeStereo/DecodeToInt16/DecodeToFloat32 API
- Unit tests: initialization, frame sizes, delay, reset, conversion

**Key artifacts:**
- `internal/hybrid/decoder.go` - Hybrid decoder struct, coordination logic
- `internal/hybrid/hybrid.go` - Public API
- `internal/hybrid/hybrid_test.go` - Unit tests
- `internal/celt/decoder.go` - Added DecodeFrameHybrid, HybridCELTStartBand
