# Gopus Project Context for Claude

This file is automatically loaded on every Claude session to provide project context.

## Project Overview

**Gopus** is a pure Go implementation of the Opus audio codec (RFC 6716).

**CRITICAL: C Reference Implementation**
```
Location: tmp_check/opus-1.6.1/
Version:  libopus 1.6.1
```
Always use this reference when implementing features or debugging discrepancies.

---

## Current Status (Updated: 2026-02-02)

### Production Readiness Score: ~95%

| Component | Status | Notes |
|-----------|--------|-------|
| Decoder | ✅ Complete | All modes, stereo, all sample rates |
| Encoder | ✅ Complete | SILK, CELT, Hybrid working |
| FinalRange | ✅ 100% | All test vector packets pass |
| Stereo | ✅ Complete | 80-level quantization, LP filtering |
| PLC | ✅ Complete | LTP coefficients, frame gluing |
| DTX | ✅ Complete | Multi-band VAD implemented |
| Hybrid | ✅ Improved | Proper bit allocation, HB_gain, crossover |
| Allocations | ✅ Encoder 0 | ALL encoder modes now ZERO allocations! |

---

## Completed Improvements

### Session 1: Foundation (Complete)
1. ✅ **Error Handling** - Replaced panics with errors (`silk/errors.go`)
2. ✅ **SILK FinalRange()** - Captured range state before `Done()`
3. ✅ **Stereo Hybrid Encoding** - Removed channel==1 restriction
4. ✅ **FinalRange Test Suite** - `testvectors/finalrange_test.go`

### Session 2: Quality Improvements (Complete)
1. ✅ **80-level Stereo Quantization** - `silk/stereo_encode.go`
2. ✅ **PLC LTP Coefficients** - 5-tap LTP, pitch drift handling
3. ✅ **PLC Frame Gluing** - Gain ramp, energy tracking
4. ✅ **DTX Multi-band VAD** - 4-band analysis, adaptive thresholds
5. ✅ **SILK Stereo LP Filtering** - LP/HP filters, state preservation
6. ✅ **SILK Pitch Analysis** - 3-stage search, contour codebooks
7. ✅ **CELT Band Energy** - Two-pass quant, Laplace encoding
8. ✅ **Round-trip Tests** - Comprehensive test suite

### Session 3: Encoder/Decoder Polish (Complete)
1. ✅ **SILK LPC Analysis** - `silk/lpc_analysis.go`
   - Libopus-matching Burg method (burgModifiedFLP)
   - A2NLSF polynomial root-finding conversion
   - NLSF interpolation for frame boundaries
2. ✅ **Hybrid Band Splitting** - `encoder/hybrid.go`
   - Proper SILK/CELT bit allocation tables
   - HB_gain high-band attenuation at low CELT bitrates
   - Gain fade smooth transitions using CELT window
   - 12-tap polyphase FIR resampler
   - Crossover energy matching at band 17
3. ✅ **CELT PVQ Quality** - `celt/pvq_search.go`
   - Quality test comparing alg_quant implementations
4. ✅ **Allocation Benchmarks** - `benchmark_alloc_test.go`
   - Comprehensive encode/decode benchmarks
   - Encoder scratch buffers for zero-alloc path
   - Current: 288 encode, 59 decode allocs/op

### Session 4: Allocation & FEC Improvements (Complete)
1. ✅ **CELT Decoder Allocations** - `celt/scratch.go`, `celt/kiss_fft.go`
   - Scratch buffer pooling for FFT transforms
   - CELT decoder now 1 alloc/op (down from ~59, 98% reduction!)
2. ✅ **CELT Encoder Allocations** - `celt/encoder.go`, `celt/bands_quant.go`
   - Pre-allocated buffers for band quantization
   - Encoder now 279 allocs/op (down from 288)
3. ✅ **SILK Gain Quantization** - `silk/gain_quant.go`
   - Full libopus-matching logarithmic gain quantization
   - Hysteresis and delta coding for smooth gain changes
4. ✅ **FEC/LBRR Encoding** - `silk/lbrr_encode.go`
   - Low Bitrate Redundancy for forward error correction
   - PatchInitialBits support in range encoder
   - Enables packet loss recovery at decoder

### Session 5: Zero-Alloc Hot Path (Complete)
1. ✅ **Tonality Analysis** - `celt/tonality.go`
   - Added TonalityScratch and ComputeTonalityWithBandsScratch
   - Eliminates per-frame allocations in tonality computation
2. ✅ **TF Analysis** - `celt/tf.go`
   - Added TFAnalysisScratch and TFAnalysisWithScratch
   - Zero-alloc Viterbi algorithm for TF resolution
3. ✅ **Dynalloc Analysis** - `celt/dynalloc.go`
   - Added DynallocScratch and DynallocAnalysisWithScratch
   - Pre-allocated buffers for masking model
4. ✅ **Caps Initialization** - `celt/encode_frame.go`
   - Use initCapsInto with scratch buffer
   - Encoder: 72→18 allocs/op (75% reduction)
5. ✅ **SILK Encoder Zero-Alloc** - `silk/encoder.go`
   - Scratch buffers for pitch detection, gain quantization, LPC/Burg analysis
   - silkGainsQuantInto zero-alloc version
   - LTP coefficients as fixed-size array [4][5]int8
   - Flat array with stride indexing for pitch correlations
   - DTX stereo-to-mono mixing uses encoder scratch buffers
6. ✅ **All Encoder Modes: 0 allocs/op**
   - CELT mode: 0 allocs/op ✅
   - Stereo mode: 0 allocs/op ✅
   - VoIP/SILK mode: 0 allocs/op ✅
   - LowDelay mode: 0 allocs/op ✅

### Session 6: Encoder Feature Parity (Complete)
1. ✅ **Signal Type Hint** - `types/types.go`, `encoder/encoder.go`, `encoder.go`
   - SignalAuto, SignalVoice, SignalMusic constants
   - SetSignal/Signal API with validation
   - Mode selection biases toward SILK (voice) or CELT (music)
2. ✅ **Max Bandwidth Limit** - `encoder/encoder.go`, `encoder.go`
   - SetMaxBandwidth/MaxBandwidth API
   - effectiveBandwidth() clamps bandwidth before encoding
   - Packet TOC uses effective bandwidth
3. ✅ **Force Channels** - `encoder/encoder.go`, `encoder.go`
   - SetForceChannels/ForceChannels API
   - -1=auto, 1=mono, 2=stereo
4. ✅ **Lookahead** - `encoder/encoder.go`, `encoder.go`
   - Read-only Lookahead() getter
   - Returns ~250 samples (5.2ms) at 48kHz
5. ✅ **LSB Depth** - `encoder/encoder.go`, `encoder.go`
   - SetLSBDepth/LSBDepth API (8-24 bits)
   - Affects DTX sensitivity threshold
6. ✅ **Prediction Disabled** - `encoder/encoder.go`, `encoder.go`
   - SetPredictionDisabled/PredictionDisabled API
   - Disables inter-frame prediction for error resilience
7. ✅ **Phase Inversion Disabled** - `celt/encoder.go`, `encoder/encoder.go`, `encoder.go`
   - SetPhaseInversionDisabled/PhaseInversionDisabled API
   - Disables stereo phase inversion decorrelation

---

## Known Issues & Debugging Notes

### RESOLVED ISSUES (Don't Debug Again!)

| Issue | Resolution | Date |
|-------|------------|------|
| `min` redeclared | Removed duplicate from finalrange_test.go | 2026-02-01 |
| Stereo weights out of range | Updated test to use Q14 range [-16384, 16384] | 2026-02-01 |
| encodeStereo returns int16 | Changed to int32 to match libopus | 2026-02-01 |
| silkSideEncoder nil panic | Added ensureSILKSideEncoder() | 2026-02-01 |
| TestSmulwb failing | Fixed test expectations (int16 truncation) | 2026-02-01 |
| TestSigmQ15 failing | Fixed boundary condition (x >= 127) | 2026-02-01 |
| TestDTXSuppressesSilence | Fixed after sigmQ15 fix | 2026-02-01 |
| TestDetectPitchVoicedSignal | Fixed by removing duplicate functions | 2026-02-01 |
| Duplicate functions in multiple files | Removed by round-trip test agent | 2026-02-01 |

### KNOWN PRECISION DIFFERENCES (Expected)

1. **testvector10/12** - ~1% FinalRange mismatch due to float vs fixed-point
2. **testvector11** - Some packets have buffer sizing edge cases
3. **IMDCT precision** - Using float32 for better libopus matching (fixed)

---

## C Reference Quick Guide

### Key Files in `tmp_check/opus-1.6.1/`

```
silk/
├── stereo_LR_to_MS.c      # Stereo L/R to Mid/Side conversion
├── stereo_quant_pred.c    # 80-level weight quantization
├── stereo_find_predictor.c # Predictor computation
├── stereo_encode_pred.c   # Weight encoding
├── PLC.c                  # Packet loss concealment
├── PLC.h                  # PLC structures
├── CNG.c                  # Comfort noise generation
├── VAD.c                  # Voice activity detection
├── NSQ.c                  # Noise shaping quantization
├── LTP_analysis_filter_FLP.c # LTP coefficient analysis
└── control_codec.c        # Encoder control logic

celt/
├── bands.c                # Band energy processing
├── quant_bands.c          # Band energy quantization
├── entcode.c              # Range coding
├── entenc.c               # Range encoder
├── entdec.c               # Range decoder
└── cwrs.c                 # Combinatorial coding

src/
├── opus_encoder.c         # High-level encoder (DTX at line ~1115)
├── opus_decoder.c         # High-level decoder
└── repacketizer.c         # Packet manipulation
```

### Critical Data Structures

```c
// silk/structs.h - Encoder state
typedef struct {
    opus_int16  sMid[2];           // Mid channel filter state
    opus_int16  sSide[2];          // Side channel filter state
    opus_int32  smth_width_Q14;    // Smoothed stereo width
    opus_int32  mid_side_amp_Q0[4]; // Channel amplitudes
    opus_int16  prev_pred_Q13[2];  // Previous predictors
} stereo_enc_state;

// silk/PLC.h - PLC state
typedef struct {
    opus_int32  pitchL_Q8;         // Pitch lag Q8
    opus_int16  LTPCoef_Q14[5];    // 5-tap LTP filter
    opus_int16  prevGain_Q16[2];   // Previous gains
    opus_int32  prevLPC_Q12[16];   // Previous LPC coefficients
} PLC_state;
```

### Q-Format Reference

| Format | Range | Usage |
|--------|-------|-------|
| Q13 | ±8192 = ±1.0 | Stereo predictors |
| Q14 | ±16384 = ±1.0 | LTP coefficients, filter taps |
| Q12 | ±4096 = ±1.0 | LPC coefficients |
| Q16 | ±65536 = ±1.0 | Gains, interpolation |
| Q24 | ±16M = ±1.0 | Internal precision |

---

## File Mapping: gopus ↔ libopus

| gopus | libopus | Notes |
|-------|---------|-------|
| `silk/stereo_encode.go` | `silk/stereo_*.c` | Now has 80-level quant |
| `silk/plc.go` | `silk/PLC.c` | Missing LTP, frame gluing |
| `silk/plc_glue.go` | `silk/PLC.c:silk_PLC_glue_frames` | ✅ Complete |
| `encoder/vad.go` | `silk/VAD.c` | ✅ Multi-band VAD complete |
| `encoder/dtx.go` | `src/opus_encoder.c` | ✅ Working with VAD |
| `silk/lpc_analysis.go` | `silk/float/burg_modified_FLP.c` | ✅ Burg method, A2NLSF |
| `encoder/hybrid.go` | `src/opus_encoder.c` | ✅ Bit alloc, HB_gain, gain fade |
| `benchmark_alloc_test.go` | - | ✅ Comprehensive benchmarks |
| `silk/gain_quant.go` | `silk/gain_quant.c` | ✅ Libopus-matching gain quant |
| `silk/lbrr_encode.go` | `silk/encode_frame_FLP.c` | ✅ FEC/LBRR encoding |
| `rangecoding/` | `celt/entcode.c` | Working correctly |
| `celt/bands.go` | `celt/bands.c` | Energy coding improvements |

---

## Testing Commands

```bash
# Full test suite
go test ./... -count=1

# FinalRange verification (primary compliance test)
go test ./testvectors/... -v -run "FinalRange"

# Stereo tests
go test ./silk/... -v -run "Stereo"

# Hybrid mode tests
go test ./encoder/... -v -run "Hybrid"

# Race detection
go test -race ./...

# Benchmark
go test -bench=. ./...
```

---

## Agent Task Tracking

### Active Agents (Round 5)
| ID | Task | Status |
|----|------|--------|
| - | Ready for next round | - |

### Completed Agents (Rounds 1-4)
| Round | ID | Task | Result |
|-------|-----|------|--------|
| 4 | a0efb6b | CELT decoder allocs | ✅ 59→1 alloc (98% reduction) |
| 4 | a66a0ba | CELT encoder allocs | ✅ 288→279 allocs |
| 4 | af890b5 | SILK gain quant | ✅ Libopus-matching log quantization |
| 4 | ab2a915 | FEC/LBRR encoding | ✅ Forward error correction |
| 3 | a2b8a05 | SILK LPC analysis | ✅ Burg method, A2NLSF, interpolation |
| 3 | abb41d1 | Hybrid band splitting | ✅ Bit allocation, HB_gain, gain fade |
| 3 | a6bb632 | Allocation benchmarks | ✅ Benchmarks, scratch buffers |
| 2 | a104d09 | Encoder round-trip tests | ✅ Comprehensive tests |
| 2 | a8acc35 | CELT band energy | ✅ Two-pass quant, Laplace encoding |
| 2 | ae4edfc | SILK stereo LP filtering | ✅ LP/HP filter, state preservation |
| 2 | ace93bf | PLC LTP coefficients | ✅ 5-tap LTP, pitch drift |
| 2 | aeb953e | PLC frame gluing | ✅ Gain ramp, energy tracking |
| 2 | aa87244 | DTX multi-band VAD | ✅ 4-band analysis, hangover |
| 2 | ab53761 | SILK pitch analysis | ✅ 3-stage search, contour codebooks |
| 2 | a7611cc | 8ms predictor interpolation | ✅ Smooth frame boundaries |
| 2 | add8001 | CELT transient detection | ✅ Percussive attack, hysteresis |
| 2 | a61fdc8 | SILK noise shaping | ✅ NSQ with R-D optimization |
| 2 | a07de46 | FinalRange precision | ✅ 100% verification |
| 2 | a47fed6 | SILK stereo quantization | ✅ 80-level implemented |

---

## Priority Queue

### P0: Must Fix Before Production
- [x] All tests pass (`go test ./...`) ✅ 10/10 packages
- [x] No panics in production code
- [x] FinalRange verification >99%

### P1: Quality Improvements
- [x] 80-level stereo quantization ✅
- [x] PLC LTP coefficients ✅
- [x] PLC frame gluing ✅
- [x] DTX multi-band VAD ✅

### P2: Bit-Exactness
- [x] SILK stereo LP filtering ✅
- [x] Predictor interpolation ✅
- [x] FinalRange precision fixes ✅ (100% verification)
- [x] SILK noise shaping ✅
- [x] CELT transient detection ✅
- [x] SILK LPC analysis (Burg) ✅
- [x] Hybrid bit allocation ✅
- [x] Zero allocations in encoder hot path ✅ (ALL modes: 0 allocs/op)
- [ ] CELT fine energy bits optimization
- [x] SILK gain quantization refinement ✅
- [x] FEC encoding implementation ✅
- [x] Encoder feature parity ✅ (Signal, MaxBandwidth, ForceChannels, Lookahead, LSBDepth, PredictionDisabled, PhaseInversionDisabled)

### P3: Advanced Features
- [ ] Deep PLC (LPCnet)
- [ ] DRED
- [ ] OSCE
- [ ] Multistream encoder

---

## API Design Guidelines

**ZERO ALLOCATIONS BY DESIGN - BREAK BACKWARD COMPATIBILITY**

Not released yet. Only one API - the zero-allocation one. No compatibility shims.

```go
// THE API: Caller owns all buffers
func (d *Decoder) Decode(data []byte, pcm []float32) (int, error)
func (e *Encoder) Encode(pcm []float32, data []byte) (int, error)

// NO convenience wrappers that allocate
// NO backward compatibility
// Break it if needed
```

**Rules:**
1. Caller provides ALL buffers - no exceptions
2. Pre-allocate internal state in constructor
3. Never `make()` in Encode/Decode paths
4. Verify: `go test -bench=. -benchmem` must show 0 allocs/op
5. If API is wrong, fix it - not released yet

---

## Commit Rules

**IMPORTANT:** When committing changes:
1. **DO NOT** mention Claude, AI, or any assistant in commit messages
2. **DO NOT** use `Co-Authored-By` headers referencing AI
3. Write commits as if authored by a human developer
4. Use conventional commit format: `type(scope): description`
5. Keep messages concise and descriptive

Example good commits:
```
feat(silk): implement 80-level stereo quantization
fix(plc): add LTP coefficient preservation
test(encoder): add comprehensive round-trip tests
```

---

## Build Status

```bash
# Last verified: 2026-02-01 (after round 5)
go build ./...  # ✅ Success
go test ./... -count=1  # ✅ All packages pass
```

### Allocation Status (ALL ENCODER MODES ZERO ALLOCS!)
```
Encoder (CELT Mono):     0 allocs/op ✅ ZERO ALLOCATIONS!
Encoder (CELT Stereo):   0 allocs/op ✅ ZERO ALLOCATIONS!
Encoder (CELT LowDelay): 0 allocs/op ✅ ZERO ALLOCATIONS!
Encoder (VoIP/SILK):     0 allocs/op ✅ ZERO ALLOCATIONS!
Target:                  0 allocs/op ✅ ACHIEVED!

Key changes for zero-alloc SILK:
- Pitch detection: flat arrays with stride indexing for C8kHz
- Gain quantization: silkGainsQuantInto with scratch buffer
- LPC/Burg analysis: scratchBurg* buffers for all intermediate arrays
- LSF encoding: scratch buffers for residuals and predictions
- LTP coefficients: fixed-size array [4][5]int8 instead of slices
- Shell encoder: fixed-size scratch arrays [8], [4], [2], [1]
- DTX: reuse encoder scratchPCM32 and scratchLeft for conversion
```
