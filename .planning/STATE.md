# Project State

## Project Reference

See: .planning/PROJECT.md (updated 2026-01-21)

**Core value:** Correct, pure-Go Opus encoding and decoding that passes official test vectors - no cgo, no external dependencies.
**Current focus:** Phase 5: Multistream Decoder - In Progress

## Current Position

Phase: 5 of 12 (Multistream Decoder)
Plan: 1 of 2 in current phase - COMPLETE
Status: In progress
Last activity: 2026-01-22 - Completed 05-01-PLAN.md (Multistream Foundation)

Progress: [██████████████████████████████████████████░░] ~46% (17/37 plans)

## Performance Metrics

**Velocity:**
- Total plans completed: 17
- Average duration: ~8 minutes
- Total execution time: ~134 minutes

**By Phase:**

| Phase | Plans | Total | Avg/Plan |
|-------|-------|-------|----------|
| 01-foundation | 3/3 | ~29m | ~10m |
| 02-silk-decoder | 5/5 | ~31m | ~6m |
| 03-celt-decoder | 5/5 | ~50m | ~10m |
| 04-hybrid-decoder | 3/3 | ~22m | ~7m |
| 05-multistream-decoder | 1/2 | ~2m | ~2m |

**Recent Trend:**
- Last 5 plans: 03-05 (~10m), 04-01 (~5m), 04-02 (~11m), 04-03 (~6m), 05-01 (~2m)
- Trend: Foundation plan fast due to focused scope (struct/validation only)

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
| D04-02-01 | Interface-based PLC design | 04-02 | Avoids silk/celt circular imports |
| D04-02-02 | FadePerFrame = 0.5 (~-6dB per frame) | 04-02 | Smooth exponential decay |
| D04-02-03 | MaxConcealedFrames = 5 (~100ms) | 04-02 | Typical real-time streaming limit |
| D04-02-04 | Nil data signals PLC | 04-02 | Clean API for packet loss handling |
| D04-02-05 | EnergyDecayPerFrame = 0.85 | 04-02 | CELT band energy fade |
| D04-03-01 | Hardcoded packets for reliable testing | 04-03 | Programmatic encoding too complex |
| D04-03-02 | Add bounds checking for corrupted bitstream | 04-03 | Graceful degradation vs panics |
| D05-01-01 | Use hybrid.Decoder for all streams | 05-01 | Handles mode detection via TOC |
| D05-01-02 | streamDecoder interface wraps concrete decoders | 05-01 | Uniform decoder management |
| D05-01-03 | Validate mapping values against streams+coupledStreams | 05-01 | Prevents invalid channel routing |

### Pending Todos

- Complete Phase 05 Plan 02 (Decode method and channel routing)

### Known Gaps

- **Encoder-decoder round-trip:** Encoder produces valid output but exact byte format matching with decoder needs additional work. Does not block SILK/CELT implementation.

### Blockers/Concerns

None.

## Session Continuity

Last session: 2026-01-22
Stopped at: Completed 05-01-PLAN.md (Multistream Foundation)
Resume file: .planning/phases/05-multistream-decoder/05-02-PLAN.md

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

## Phase 03 Summary - COMPLETE

**CELT Decoder phase complete:**
- All 5 plans executed successfully
- Total duration: ~50 minutes
- 61 tests passing

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

## Phase 04 Summary - COMPLETE

**Hybrid Decoder phase complete:**
- All 3 plans executed successfully (including gap closure)
- Total duration: ~22 minutes
- 37 tests passing (22 in hybrid, 15 in plc)

**04-01 Hybrid Decoder Foundation complete:**
- Decoder struct coordinating SILK (WB) and CELT sub-decoders
- DecodeFrameHybrid added to CELT for band-limited decoding (bands 17-21)
- 60-sample delay compensation for SILK-CELT time alignment
- 3x upsampling from SILK 16kHz to 48kHz output
- Public Decode/DecodeStereo/DecodeToInt16/DecodeToFloat32 API

**04-02 PLC Implementation complete:**
- PLC package with State tracking, fade factor, mode handling
- SILK PLC: LPC extrapolation (voiced) and comfort noise (unvoiced)
- CELT PLC: energy decay with noise-filled bands
- Hybrid PLC: coordinated SILK+CELT concealment
- Interface-based design avoiding circular imports
- 15 comprehensive PLC tests

**Key artifacts:**
- `internal/hybrid/decoder.go` - Hybrid decoder struct, coordination logic
- `internal/hybrid/hybrid.go` - Public API with PLC support
- `internal/hybrid/hybrid_test.go` - 15 unit tests
- `internal/plc/plc.go` - State struct, Mode enum, fade constants
- `internal/plc/silk_plc.go` - ConcealSILK, voiced/unvoiced PLC
- `internal/plc/celt_plc.go` - ConcealCELT, ConcealCELTHybrid
- `internal/plc/plc_test.go` - 15 PLC tests
- `internal/celt/decoder.go` - Added DecodeFrameHybrid, PLC integration

**04-03 Gap Closure: Integration Tests complete:**
- Packet construction helpers using range encoder
- 7 new integration tests with real range-coded packets
- Corrupted bitstream robustness fixes in SILK decoder
- Verified hybrid decoder processes real SILK+CELT bitstreams
- 22 total hybrid tests (15 original + 7 new)

**Additional artifacts:**
- `internal/hybrid/testdata_test.go` - Packet construction helpers
- `internal/silk/excitation.go` - Bounds checking fixes
- `internal/silk/stereo.go` - Bounds checking fixes

## Phase 05 Summary - In Progress

**05-01 Multistream Foundation complete:**
- MultistreamDecoder struct with comprehensive parameter validation
- Vorbis channel mapping tables for 1-8 channels (mono through 7.1 surround)
- Self-delimiting packet parser per RFC 6716 Appendix B
- streamDecoder interface for uniform decoder handling
- Duration: ~2 minutes

**Key artifacts:**
- `internal/multistream/decoder.go` - Decoder struct, NewDecoder, validation
- `internal/multistream/mapping.go` - DefaultMapping, resolveMapping, vorbisChannelOrder
- `internal/multistream/stream.go` - parseMultistreamPacket, parseSelfDelimitedLength
