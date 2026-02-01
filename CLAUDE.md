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

## Current Status (Updated: 2026-02-01)

### Production Readiness Score: ~85%

| Component | Status | Notes |
|-----------|--------|-------|
| Decoder | ✅ Complete | All modes, stereo, all sample rates |
| Encoder | ✅ Complete | SILK, CELT, Hybrid working |
| FinalRange | ✅ 99.9% | 19,883/20,075 test vector packets pass |
| Stereo | ⚠️ Improved | 80-level quantization implemented |
| PLC | 🔄 In Progress | LTP coefficients, frame gluing being added |
| DTX | 🔄 In Progress | Multi-band VAD being implemented |

---

## Completed Improvements

### Session 1: Foundation (Complete)
1. ✅ **Error Handling** - Replaced panics with errors (`silk/errors.go`)
2. ✅ **SILK FinalRange()** - Captured range state before `Done()`
3. ✅ **Stereo Hybrid Encoding** - Removed channel==1 restriction
4. ✅ **FinalRange Test Suite** - `testvectors/finalrange_test.go`

### Session 2: Quality Improvements (In Progress)
1. ✅ **80-level Stereo Quantization** - `silk/stereo_encode.go`
   - `stereoQuantPred()` - 16 main levels × 5 sub-steps
   - `stereoEncodePred()` - joint index encoding
   - `stereoDecodePred()` - matching decoder
   - Delta coding between predictors

2. 🔄 **PLC LTP Coefficients** - Agent working
3. 🔄 **PLC Frame Gluing** - Agent working
4. 🔄 **DTX Multi-band VAD** - Agent working
5. 🔄 **SILK Stereo LP Filtering** - Agent working
6. 🔄 **SILK Pitch Analysis** - Agent working
7. 🔄 **CELT Band Energy** - Agent working
8. 🔄 **Round-trip Tests** - Agent working

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
| `silk/plc_glue.go` | `silk/PLC.c:silk_PLC_glue_frames` | NEW - being implemented |
| `silk/vad.go` | `silk/VAD.c` | NEW - being implemented |
| `encoder/dtx.go` | `src/opus_encoder.c` | Basic, needs multi-band |
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

### Completed Agents (2026-02-01)
| ID | Task | Result |
|----|------|--------|
| a104d09 | Encoder round-trip tests | ✅ Comprehensive tests + fixed duplicates |
| a8acc35 | CELT band energy | ✅ Two-pass quant, Laplace encoding, pred coefficients |
| ae4edfc | SILK stereo LP filtering | ✅ LP/HP filter, state preservation, 28 tests |
| ace93bf | PLC LTP coefficients | ✅ 5-tap LTP, pitch drift, attenuation profiles |
| aeb953e | PLC frame gluing | ✅ Gain ramp, energy tracking, onset preservation |
| aa87244 | DTX multi-band VAD | ✅ 4-band analysis, adaptive thresholds, hangover |
| ab53761 | SILK pitch analysis | ✅ 3-stage search, contour codebooks, Lagrangian interp |
| a47fed6 | SILK stereo quantization | ✅ 80-level implemented |

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
- [ ] Fixed-point arithmetic option
- [ ] Predictor interpolation

### P3: Advanced Features
- [ ] Deep PLC (LPCnet)
- [ ] DRED
- [ ] OSCE

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
# Last verified: 2026-02-01
go build ./...  # ✅ Success
go test ./... -count=1  # ✅ 10/10 packages pass
```
