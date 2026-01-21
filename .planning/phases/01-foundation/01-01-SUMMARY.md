---
phase: 01-foundation
plan: 01
subsystem: entropy-coding
tags: [range-coder, decoder, rfc6716]
dependency-graph:
  requires: []
  provides: [rangecoding-decoder, icdf-decode, bit-decode]
  affects: [01-02, 02-silk-decoder, 03-celt-decoder]
tech-stack:
  added: []
  patterns: [table-driven-tests, bit-exact-implementation]
key-files:
  created:
    - internal/rangecoding/constants.go
    - internal/rangecoding/decoder.go
    - internal/rangecoding/decoder_test.go
    - internal/rangecoding/constants_test.go
  modified: []
decisions:
  - id: D01-01-01
    decision: "Set nbitsTotal before normalize() in Init"
    rationale: "Matches libopus initialization order"
    scope: "internal/rangecoding/decoder.go"
metrics:
  duration: ~4 minutes
  completed: 2026-01-21
---

# Phase 01 Plan 01: Range Decoder Implementation Summary

**One-liner:** Range decoder for Opus entropy coding with DecodeICDF and DecodeBit following RFC 6716 Section 4.1

## What Was Built

Implemented the range decoder component per RFC 6716 Section 4.1. This is the entropy coding foundation used by both SILK and CELT layers in Opus. All symbol decoding goes through this component.

### Files Created

| File | Purpose | Lines |
|------|---------|-------|
| `internal/rangecoding/constants.go` | EC_CODE_BITS, EC_SYM_BITS, EC_CODE_TOP, EC_CODE_BOT constants | 14 |
| `internal/rangecoding/decoder.go` | Decoder struct with Init, DecodeICDF, DecodeBit, Tell methods | 185 |
| `internal/rangecoding/decoder_test.go` | Comprehensive unit tests for all decoder operations | 349 |
| `internal/rangecoding/constants_test.go` | Constant value verification tests | 28 |

### Key Components

1. **Constants (constants.go)**
   - `EC_SYM_BITS = 8` - Bits output at a time
   - `EC_CODE_BITS = 32` - Total state register bits
   - `EC_SYM_MAX = 255` - Maximum symbol value
   - `EC_CODE_TOP = 0x80000000` - Top of code range
   - `EC_CODE_BOT = 0x00800000` - Bottom threshold for normalization
   - `EC_CODE_SHIFT = 23` - Shift amount
   - `EC_CODE_EXTRA = 7` - Extra bits

2. **Decoder (decoder.go)**
   - `Decoder` struct with state: buf, rng, val, rem, nbitsTotal
   - `Init(buf []byte)` - Initialize from byte buffer
   - `DecodeICDF(icdf []uint8, ftb uint) int` - Decode symbol using ICDF table
   - `DecodeBit(logp uint) int` - Decode single bit with log probability
   - `Tell() int` - Return bits consumed
   - `TellFrac() int` - Return bits consumed with 1/8 bit precision
   - `normalize()` - Internal renormalization loop

3. **Tests (decoder_test.go)**
   - TestDecoderInit - Various buffer inputs
   - TestDecodeBit - Different log probabilities
   - TestDecodeICDF - Uniform and skewed distributions
   - TestTell / TestTellFrac - Bit counting accuracy
   - TestDecoderSequence - Mixed operations
   - TestDecoderDeterminism - Reproducibility
   - TestIlog - Integer log function
   - TestBytesUsed - Buffer consumption

## Verification Results

| Check | Result |
|-------|--------|
| `go build ./...` | Pass |
| `go test ./internal/rangecoding/` | Pass (11 tests) |
| Test coverage | 96.2% |
| Range invariant (rng > EC_CODE_BOT) | Verified in all tests |

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Fixed TellFrac shift calculation**
- **Found during:** Task 3 (writing tests)
- **Issue:** `l - 16` shift could produce incorrect results when l < 16
- **Fix:** Added conditional shift direction based on l value
- **Files modified:** internal/rangecoding/decoder.go
- **Commit:** aa899ae

**2. [Rule 1 - Bug] Fixed nbitsTotal initialization order**
- **Found during:** Task 3 (writing tests)
- **Issue:** nbitsTotal was set after normalize(), causing negative Tell() values
- **Fix:** Set nbitsTotal = EC_CODE_BITS + 1 before calling normalize()
- **Files modified:** internal/rangecoding/decoder.go
- **Commit:** aa899ae

## Commits

| Hash | Type | Description |
|------|------|-------------|
| 5e22e7a | feat | Create range coder constants and project structure |
| 8efc609 | feat | Implement range decoder core |
| aa899ae | test | Add comprehensive decoder tests (includes bug fixes) |

## Decisions Made

| ID | Decision | Rationale |
|----|----------|-----------|
| D01-01-01 | Set nbitsTotal before normalize() | Matches libopus ec_dec_init order, ensures Tell() returns correct values |

## Next Phase Readiness

**Ready for:** Plan 01-02 (Range Encoder Implementation)

**Dependencies provided:**
- `rangecoding.Decoder` struct
- `Decoder.Init()` for buffer initialization
- `Decoder.DecodeICDF()` for symbol decoding
- `Decoder.DecodeBit()` for bit decoding
- `Decoder.Tell()` / `Decoder.TellFrac()` for bit counting
- Constants: EC_CODE_BITS, EC_SYM_BITS, EC_CODE_TOP, EC_CODE_BOT, etc.

**No blockers identified.**
