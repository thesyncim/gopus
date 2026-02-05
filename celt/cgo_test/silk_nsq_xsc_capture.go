//go:build cgo_libopus
// +build cgo_libopus

// Package cgo provides CGO helpers for libopus NSQ scaling capture.
package cgo

/*
#cgo CFLAGS: -I${SRCDIR}/../../tmp_check/opus-1.6.1/include -I${SRCDIR}/../../tmp_check/opus-1.6.1/celt -I${SRCDIR}/../../tmp_check/opus-1.6.1 -I${SRCDIR}/../../tmp_check/opus-1.6.1/silk -DHAVE_CONFIG_H
#cgo LDFLAGS: ${SRCDIR}/../../tmp_check/opus-1.6.1/.libs/libopus.a -lm

#include "silk/main.h"
#include "silk/define.h"
#include "silk/structs.h"
#include "silk/Inlines.h"

void test_silk_nsq_xsc_q10(
    const opus_int16 *x16,
    int length,
    opus_int32 gain_Q16,
    opus_int32 *out_x_sc_Q10
) {
    opus_int32 inv_gain_Q31, inv_gain_Q26;
    int i;
    inv_gain_Q31 = silk_INVERSE32_varQ( silk_max( gain_Q16, 1 ), 47 );
    inv_gain_Q26 = silk_RSHIFT_ROUND( inv_gain_Q31, 5 );
    for( i = 0; i < length; i++ ) {
        out_x_sc_Q10[ i ] = silk_SMULWW( x16[ i ], inv_gain_Q26 );
    }
}
*/
import "C"

import "unsafe"

// SilkNSQScaleXScQ10 computes libopus x_sc_Q10 for a subframe.
func SilkNSQScaleXScQ10(x16 []int16, gainQ16 int32) []int32 {
	if len(x16) == 0 {
		return nil
	}
	out := make([]int32, len(x16))
	C.test_silk_nsq_xsc_q10(
		(*C.opus_int16)(unsafe.Pointer(&x16[0])),
		C.int(len(x16)),
		C.opus_int32(gainQ16),
		(*C.opus_int32)(unsafe.Pointer(&out[0])),
	)
	return out
}
