//go:build gopus_fixedpoint

package gopus

// Build-gated flags that select the fixed-point variant of shared decode tests.
// Each has a !gopus_fixedpoint twin in fixedpoint_test_flags_default_test.go.

// robustFixedPointDecode is true under gopus_fixedpoint: gopus decodes int16/int24
// through the integer path, so the float-oracle PCM-value guard does not apply
// (see fixedpoint_test_flags_default_test.go for the rationale). The no-panic
// and accept/reject invariants are still enforced under this build.
const robustFixedPointDecode = true

// celtIntegerPLCActive is true under -tags gopus_fixedpoint, where a CELT-only
// packet-loss frame is concealed by the integer FIXED_POINT celt_decode_lost and
// emitted as the libopus-exact opus_res int16/int24 output. That concealment is a
// different (fixed) computation than the float32 PLC, so float-equality and
// float-oracle PLC assertions for CELT no longer hold there; their correctness is
// covered by TestDecoderFixedPointCELTPLCParity (FIXED_POINT opus_decode oracle).
const celtIntegerPLCActive = true

// hybridIntegerPLCActive is true under -tags gopus_fixedpoint, where a lost Hybrid
// frame advances the integer FIXED_POINT CELT highband state through the loss and
// accumulates the concealed highband onto the integer SILK lowband (see
// armFixedHybridLost / finishFixedHybridLost), emitting the libopus FIXED_POINT
// opus_res int16/int24 output. Like the CELT-only integer PLC that is a different
// (fixed) computation than the float32 PLC, so the float-equality and float-oracle
// PLC assertions for Hybrid no longer hold there; their correctness is covered by
// the FIXED_POINT differential gate (TestDecodeDifferentialFixedPointPLC).
const hybridIntegerPLCActive = true

// decodeInt24TracksFloat32Skip is true under -tags gopus_fixedpoint, where
// DecodeInt24 routes CELT-only frames to the integer decoder's libopus-exact
// opus_res int24 instead of the float32 decode.
const decodeInt24TracksFloat32Skip = true
