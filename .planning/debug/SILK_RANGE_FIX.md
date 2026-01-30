# SILK Encoder Range Encoder Lifecycle Fix

## Date: 2026-01-30

## Bug Summary

Multi-frame streaming encoding was broken - only the first frame produced output, while frames 1+ returned nil/0 bytes.

## Root Cause

In `/Users/thesyncim/GolandProjects/gopus/internal/silk/encode_frame.go`, the `rangeEncoder` field was not being cleared after standalone encoding completed.

### The Bug Flow

1. **Frame 0**:
   - Line 14: `useSharedEncoder := e.rangeEncoder != nil` evaluates to `false` (rangeEncoder is nil)
   - Line 26: `e.rangeEncoder = &rangecoding.Encoder{}` - creates new encoder
   - Encoding proceeds normally
   - Line 114: `return e.rangeEncoder.Done()` - returns 106 bytes
   - **BUG**: `e.rangeEncoder` is NOT cleared

2. **Frame 1+**:
   - Line 14: `useSharedEncoder := e.rangeEncoder != nil` evaluates to `true` (leftover from frame 0)
   - The encoder incorrectly believes it's in "hybrid mode" (shared encoder)
   - Line 110-112: `if useSharedEncoder { return nil }` - returns nil!
   - **Result**: 0 bytes returned for all subsequent frames

## The Fix

Added `e.rangeEncoder = nil` after `e.rangeEncoder.Done()` in the standalone encoding path:

```go
// Before (buggy):
return e.rangeEncoder.Done()

// After (fixed):
result := e.rangeEncoder.Done()
e.rangeEncoder = nil
return result
```

### Location
- File: `/Users/thesyncim/GolandProjects/gopus/internal/silk/encode_frame.go`
- Lines: 110-118

## rangeEncoder Lifecycle Design

The `rangeEncoder` field serves dual purposes:

1. **Standalone Mode** (rangeEncoder is nil at start):
   - Encoder creates its own range encoder per frame
   - Returns encoded bytes via `Done()`
   - **Must clear rangeEncoder after Done()** for next frame

2. **Hybrid Mode** (rangeEncoder pre-set via `SetRangeEncoder()`):
   - Caller manages the shared range encoder
   - Encoder writes to shared encoder
   - Returns `nil` (caller extracts data from shared encoder)

## Test Verification

### Existing Test
`TestEncodeStreaming` in `encode_test.go` now passes:
```
Frame 0: 106 bytes
Frame 1: 106 bytes
Frame 2: 106 bytes
Frame 3: 106 bytes
Frame 4: 106 bytes
```

### New Test Added
`TestMultiFrameRangeEncoderLifecycle` - validates 10 consecutive frames all produce output using the raw `Encoder` directly.

## Impact

- **Fixed**: Streaming SILK encoding now works correctly
- **Not Affected**: Decoder tests (all pass)
- **Not Affected**: Hybrid mode (SetRangeEncoder usage)

## Pre-existing Failures (Unrelated)

The following tests were already failing before this fix (gain encoding issues):
- `TestComputeLogGainIndexBoundary`
- `TestGainEncodeDecode`

These are separate issues in the gain encoding logic, not related to the rangeEncoder lifecycle.
