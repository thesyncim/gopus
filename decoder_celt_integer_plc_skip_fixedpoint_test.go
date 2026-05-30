//go:build gopus_fixedpoint

package gopus

// celtIntegerPLCActive is true under -tags gopus_fixedpoint, where a CELT-only
// packet-loss frame is concealed by the integer FIXED_POINT celt_decode_lost and
// emitted as the libopus-exact opus_res int16/int24 output. That concealment is a
// different (fixed) computation than the float32 PLC, so float-equality and
// float-oracle PLC assertions for CELT no longer hold there; their correctness is
// covered by TestDecoderFixedPointCELTPLCParity (FIXED_POINT opus_decode oracle).
const celtIntegerPLCActive = true
