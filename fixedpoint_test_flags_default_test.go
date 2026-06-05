//go:build !gopus_fixed_point

package gopus

// Default (float) build values for the flags that select the fixed-point variant
// of shared decode tests. Each has a gopus_fixed_point twin in
// fixedpoint_test_flags_test.go.

// robustFixedPointDecode reports whether the integer FIXED_POINT decode path is
// active (build tag gopus_fixed_point). The robustness gross-PCM-on-corrupt-input
// guard compares gopus float decode against the libopus FLOAT multistream oracle,
// an apples-to-apples comparison only in the default build. Under gopus_fixed_point
// gopus decodes via the integer path while this oracle is the float build, so the
// PCM-value guard would compare two different arithmetics on garbage input (the
// accept/reject + no-panic invariants stay enforced under both builds). The
// integer decode's PCM parity is covered by decode_differential_fuzz_fixedpoint_test.go
// against the FIXED_POINT oracle on clean packets.
const robustFixedPointDecode = false

// celtIntegerPLCActive is false in the default (float) build, where CELT-only
// packet-loss concealment uses the float32 PLC and the int16/int24 wrappers are
// the float32 output quantized -- so the float-equality and float-oracle PLC
// assertions for CELT are exercised.
const celtIntegerPLCActive = false

// hybridIntegerPLCActive is false in the default (float) build, where Hybrid
// packet-loss concealment uses the float32 PLC and the int16/int24 wrappers are
// the float32 output quantized -- so the float-equality and float-oracle PLC
// assertions for Hybrid are exercised.
const hybridIntegerPLCActive = false

// decodeInt24TracksFloat32Skip is false in the default (float) build, where
// DecodeInt24 derives int24 from the same float32 decode as Decode.
const decodeInt24TracksFloat32Skip = false
