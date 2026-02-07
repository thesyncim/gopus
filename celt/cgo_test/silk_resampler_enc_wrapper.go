//go:build cgo_libopus
// +build cgo_libopus

// Package cgo provides a wrapper for libopus silk_resampler in encoder mode
// (48kHz -> 16kHz downsampling) for comparison with gopus.
package cgo

/*
#cgo CFLAGS: -I${SRCDIR}/../../tmp_check/opus-1.6.1/include -I${SRCDIR}/../../tmp_check/opus-1.6.1/silk -I${SRCDIR}/../../tmp_check/opus-1.6.1/celt -I${SRCDIR}/../../tmp_check/opus-1.6.1 -DHAVE_CONFIG_H
#cgo LDFLAGS: ${SRCDIR}/../../tmp_check/opus-1.6.1/.libs/libopus.a -lm
#include <stdlib.h>
#include <string.h>
#include "opus_types.h"
#include "silk/resampler_structs.h"
#include "silk/SigProc_FIX.h"

// Single-frame encoder resampler: init fresh state (forEnc=1), resample one frame.
static int resample_enc_single(const opus_int16 *in, opus_int32 inLen,
                               opus_int16 *out, opus_int32 fsIn, opus_int32 fsOut) {
    silk_resampler_state_struct S;
    int ret = silk_resampler_init(&S, fsIn, fsOut, 1);
    if (ret != 0) return ret;
    return silk_resampler(&S, out, in, inLen);
}

// Multi-frame encoder resampler: process nFrames consecutive frames through
// a single persistent state so that delay buffer state accumulates.
static int resample_enc_multiframe(const opus_int16 *in, opus_int16 *out,
                                   int nFrames, int frameIn, int frameOut,
                                   opus_int32 fsIn, opus_int32 fsOut) {
    silk_resampler_state_struct S;
    int ret = silk_resampler_init(&S, fsIn, fsOut, 1);
    if (ret != 0) return ret;
    for (int f = 0; f < nFrames; f++) {
        ret = silk_resampler(&S, out + f * frameOut, in + f * frameIn, frameIn);
        if (ret != 0) return ret;
    }
    return 0;
}
*/
import "C"

import "unsafe"

// ProcessLibopusResamplerEncSingle processes a single frame through the libopus
// encoder resampler (forEnc=1). Returns the output int16 samples.
func ProcessLibopusResamplerEncSingle(in []int16, fsIn, fsOut int) ([]int16, int) {
	if len(in) == 0 {
		return nil, -1
	}
	outLen := len(in) * fsOut / fsIn
	out := make([]int16, outLen)
	ret := C.resample_enc_single(
		(*C.opus_int16)(unsafe.Pointer(&in[0])),
		C.opus_int32(len(in)),
		(*C.opus_int16)(unsafe.Pointer(&out[0])),
		C.opus_int32(fsIn),
		C.opus_int32(fsOut),
	)
	return out, int(ret)
}

// ProcessLibopusResamplerEncMultiframe processes nFrames consecutive frames
// through a single persistent libopus encoder resampler state.
// frameIn and frameOut are samples per frame at input and output rates.
func ProcessLibopusResamplerEncMultiframe(in []int16, nFrames, frameIn, frameOut, fsIn, fsOut int) ([]int16, int) {
	totalIn := nFrames * frameIn
	totalOut := nFrames * frameOut
	if len(in) < totalIn {
		return nil, -1
	}
	out := make([]int16, totalOut)
	ret := C.resample_enc_multiframe(
		(*C.opus_int16)(unsafe.Pointer(&in[0])),
		(*C.opus_int16)(unsafe.Pointer(&out[0])),
		C.int(nFrames),
		C.int(frameIn),
		C.int(frameOut),
		C.opus_int32(fsIn),
		C.opus_int32(fsOut),
	)
	return out, int(ret)
}
