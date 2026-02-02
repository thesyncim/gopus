//go:build cgo_libopus
// +build cgo_libopus

// Package cgo provides CGO wrappers for SILK gain quantization comparison.
package cgo

/*
#cgo CFLAGS: -I${SRCDIR}/../../tmp_check/opus-1.6.1/include -I${SRCDIR}/../../tmp_check/opus-1.6.1/celt -I${SRCDIR}/../../tmp_check/opus-1.6.1 -I${SRCDIR}/../../tmp_check/opus-1.6.1/silk -DHAVE_CONFIG_H
#cgo LDFLAGS: -L${SRCDIR}/../../tmp_check/opus-1.6.1/.libs -lopus -lm

#include <stdlib.h>
#include <string.h>
#include "silk/main.h"
#include "silk/define.h"
#include "silk/macros.h"
#include "silk/Inlines.h"

// Constants from gain_quant.c
#define GAIN_OFFSET                  ( ( MIN_QGAIN_DB * 128 ) / 6 + 16 * 128 )
#define GAIN_SCALE_Q16               ( ( 65536 * ( N_LEVELS_QGAIN - 1 ) ) / ( ( ( MAX_QGAIN_DB - MIN_QGAIN_DB ) * 128 ) / 6 ) )
#define GAIN_INV_SCALE_Q16           ( ( 65536 * ( ( ( MAX_QGAIN_DB - MIN_QGAIN_DB ) * 128 ) / 6 ) ) / ( N_LEVELS_QGAIN - 1 ) )

// Export the constants for Go to use
int gain_get_offset(void) { return GAIN_OFFSET; }
int gain_get_scale_q16(void) { return GAIN_SCALE_Q16; }
int gain_get_inv_scale_q16(void) { return GAIN_INV_SCALE_Q16; }
int gain_get_n_levels(void) { return N_LEVELS_QGAIN; }
int gain_get_min_delta(void) { return MIN_DELTA_GAIN_QUANT; }
int gain_get_max_delta(void) { return MAX_DELTA_GAIN_QUANT; }

// Wrapper for silk_lin2log
opus_int32 gain_silk_lin2log(opus_int32 inLin) {
    return silk_lin2log(inLin);
}

// Wrapper for silk_log2lin
opus_int32 gain_silk_log2lin(opus_int32 inLog_Q7) {
    return silk_log2lin(inLog_Q7);
}

// Compute raw gain index (before hysteresis/delta processing)
// This is the core computation from silk_gains_quant
opus_int32 gain_compute_raw_index(opus_int32 gain_Q16) {
    opus_int32 log_val = silk_lin2log(gain_Q16);
    opus_int32 idx = silk_SMULWB(GAIN_SCALE_Q16, log_val - GAIN_OFFSET);
    return idx;
}

// Full gain quantization for a single gain value
// Returns the quantized index [0, 63]
opus_int32 gain_quantize_single(opus_int32 gain_Q16) {
    opus_int32 log_val = silk_lin2log(gain_Q16);
    opus_int32 idx = silk_SMULWB(GAIN_SCALE_Q16, log_val - GAIN_OFFSET);

    // Clamp to valid range (no hysteresis for this test)
    if (idx < 0) idx = 0;
    if (idx > N_LEVELS_QGAIN - 1) idx = N_LEVELS_QGAIN - 1;
    return idx;
}

// Full gain dequantization: index -> Q16 gain
opus_int32 gain_dequantize(opus_int32 idx) {
    opus_int32 log_Q7 = silk_SMULWB(GAIN_INV_SCALE_Q16, idx) + GAIN_OFFSET;
    if (log_Q7 > 3967) log_Q7 = 3967;
    return silk_log2lin(log_Q7);
}

// silk_SMULWB wrapper for testing
opus_int32 gain_silk_smulwb(opus_int32 a, opus_int32 b) {
    return silk_SMULWB(a, b);
}
*/
import "C"

// GainGetOffset returns the OFFSET constant from libopus
func GainGetOffset() int {
	return int(C.gain_get_offset())
}

// GainGetScaleQ16 returns the SCALE_Q16 constant from libopus
func GainGetScaleQ16() int {
	return int(C.gain_get_scale_q16())
}

// GainGetInvScaleQ16 returns the INV_SCALE_Q16 constant from libopus
func GainGetInvScaleQ16() int {
	return int(C.gain_get_inv_scale_q16())
}

// GainGetNLevels returns N_LEVELS_QGAIN from libopus
func GainGetNLevels() int {
	return int(C.gain_get_n_levels())
}

// GainGetMinDelta returns MIN_DELTA_GAIN_QUANT from libopus
func GainGetMinDelta() int {
	return int(C.gain_get_min_delta())
}

// GainGetMaxDelta returns MAX_DELTA_GAIN_QUANT from libopus
func GainGetMaxDelta() int {
	return int(C.gain_get_max_delta())
}

// GainSilkLin2Log wraps silk_lin2log
func GainSilkLin2Log(in int32) int32 {
	return int32(C.gain_silk_lin2log(C.opus_int32(in)))
}

// GainSilkLog2Lin wraps silk_log2lin
func GainSilkLog2Lin(in int32) int32 {
	return int32(C.gain_silk_log2lin(C.opus_int32(in)))
}

// GainComputeRawIndex computes raw gain index before clamping
func GainComputeRawIndex(gainQ16 int32) int32 {
	return int32(C.gain_compute_raw_index(C.opus_int32(gainQ16)))
}

// GainQuantizeSingle quantizes a single gain value (with clamping)
func GainQuantizeSingle(gainQ16 int32) int {
	return int(C.gain_quantize_single(C.opus_int32(gainQ16)))
}

// GainDequantize dequantizes an index to Q16 gain
func GainDequantize(idx int) int32 {
	return int32(C.gain_dequantize(C.opus_int32(idx)))
}

// GainSilkSMULWB wraps silk_SMULWB
func GainSilkSMULWB(a, b int32) int32 {
	return int32(C.gain_silk_smulwb(C.opus_int32(a), C.opus_int32(b)))
}
