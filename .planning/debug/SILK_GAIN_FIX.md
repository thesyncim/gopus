# SILK Gain Encoding/Decoding Fix

## Date: 2026-01-30

## Summary

**ROOT CAUSE FOUND**: The `GainDequantTable` has incorrect values (~1000x too small).

## The Bug

### GainDequantTable Values

```go
// WRONG values in codebook.go:
var GainDequantTable = [64]int32{
    81, 89, 97, 106, ...  // First value is 81
}

// CORRECT values (per libopus formula):
var GainDequantTable = [64]int32{
    81920, 96256, 112640, ...  // First value is 81920
}
```

The table values are approximately **1000x too small**.

### Why Decoder Tests Pass

There are TWO decoder implementations:
1. `internal/silk/libopus_decode.go` → uses `silkGainsDequant()` → **CORRECT** (computes gains dynamically)
2. `internal/silk/gain.go` → uses `GainDequantTable` → **WRONG** (lookup in bad table)

The compliance tests use `libopus_decode.go` which computes gains correctly via:
```go
logGainQ7 := silkSMULWB(int32(invScaleQ16Val), int32(prev)) + int32(gainOffsetQ7)
gainsQ16[k] = silkLog2Lin(logGainQ7)
```

### Why Encoder Tests Fail

The encoder's `computeLogGainIndex()` tries to compute quantization indices using formulas that assume the dequant table has CORRECT large values. With the wrong small table values:

```
gainQ16=17830 → silkLin2Log(17830) = 1806
idx = silkSMULWB(2251, 1806 - 2090)  // 1806 - 2090 = -284
idx = -10 → clamped to 0  // WRONG! Should be 63
```

## Fix Options

### Option 1: Fix GainDequantTable (Recommended)

Replace the table values with correct libopus-computed values:

```go
var GainDequantTable = [64]int32{
    81920, 96256, 112640, 131072, 153600, 180224, 210944, 246784,
    286720, 335872, 397312, 464896, 540672, 634880, 745472, 872448,
    1019904, 1187840, 1392640, 1646592, 1925120, 2244608, 2621440, 3080192,
    3604480, 4194304, 4915200, 5767168, 6782976, 7929856, 9240576, 10813440,
    12713984, 14876672, 17301504, 20316160, 23855104, 28049408, 32768000, 38273024,
    44826624, 52690944, 61603840, 71827456, 83886080, 98566144, 115867648, 135266304,
    158334976, 185597952, 217055232, 253755392, 295698432, 346030080, 406847488, 480247808,
    557842432, 654311424, 767557632, 897581056, 1048576000, 1224736768, 1434451968, 1686110208,
}
```

### Option 2: Remove GainDequantTable

Delete the table and always use `silkGainsDequant()` for decoding.

### Option 3: Fix Encoder to Not Use Table

The encoder should use the quantization formula directly without the table.

## Impact

### With Fix
- Encoder gain quantization will work correctly
- The `gain.go` decoder path will produce correct gains
- All paths become consistent

### Without Fix
- Encoder continues to produce idx=0 for all gains
- SILK encoded audio is essentially silent
- Q scores remain at -100

## Files to Modify

1. `/Users/thesyncim/GolandProjects/gopus/internal/silk/codebook.go` - Fix GainDequantTable
2. `/Users/thesyncim/GolandProjects/gopus/internal/silk/decode_params_test.go` - Update test expectations

## Verification

After fix, run:
```bash
go test ./internal/silk/... -run "TestGain|TestComputeLog" -v
```

Expected: All 64 gain indices should round-trip correctly.
