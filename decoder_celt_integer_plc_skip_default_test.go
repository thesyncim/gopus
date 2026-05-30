//go:build !gopus_fixedpoint

package gopus

// celtIntegerPLCActive is false in the default (float) build, where CELT-only
// packet-loss concealment uses the float32 PLC and the int16/int24 wrappers are
// the float32 output quantized -- so the float-equality and float-oracle PLC
// assertions for CELT are exercised.
const celtIntegerPLCActive = false
