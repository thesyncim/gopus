//go:build cgo_libopus
// +build cgo_libopus

// Package cgo provides wrappers for libopus resampler functions
package cgo

/*
#cgo CFLAGS: -I${SRCDIR}/../../tmp_check/opus-1.6.1/include -I${SRCDIR}/../../tmp_check/opus-1.6.1/silk -I${SRCDIR}/../../tmp_check/opus-1.6.1/celt -I${SRCDIR}/../../tmp_check/opus-1.6.1
#cgo LDFLAGS: ${SRCDIR}/../../tmp_check/opus-1.6.1/.libs/libopus.a -lm
#include <stdlib.h>
#include <string.h>
#include "opus_types.h"
#include "silk/resampler_structs.h"
#include "silk/resampler_private.h"

// Direct access to libopus resampler
extern int silk_resampler_init(silk_resampler_state_struct *S, int Fs_Hz_in, int Fs_Hz_out, int forEnc);
extern int silk_resampler(silk_resampler_state_struct *S, short *out, const short *in, int inLen);

// Process samples through libopus resampler
void processLibopusResamplerC(int16_t* out, const int16_t* in, int inLen, int fsIn, int fsOut) {
	silk_resampler_state_struct S;
	memset(&S, 0, sizeof(S));
	silk_resampler_init(&S, fsIn, fsOut, 0);
	silk_resampler(&S, out, in, inLen);
}

void processLibopusDown2C(int16_t* out, const int16_t* in, int inLen) {
	opus_int32 state[2] = {0, 0};
	silk_resampler_down2(state, out, in, inLen);
}

void processLibopusDown2_3C(int16_t* out, const int16_t* in, int inLen) {
	opus_int32 state[6] = {0, 0, 0, 0, 0, 0};
	silk_resampler_down2_3(state, out, in, inLen);
}

// Get resampler parameters after init
void getResamplerParamsC(int fsIn, int fsOut, int* inputDelay, int* invRatioQ16, int* batchSize) {
	silk_resampler_state_struct S;
	memset(&S, 0, sizeof(S));
	silk_resampler_init(&S, fsIn, fsOut, 0);
	*inputDelay = S.inputDelay;
	*invRatioQ16 = S.invRatio_Q16;
	*batchSize = S.batchSize;
}
*/
import "C"

import "unsafe"

// LibopusResamplerParams holds resampler configuration
type LibopusResamplerParams struct {
	InputDelay  int
	InvRatioQ16 int
	BatchSize   int
}

// GetLibopusResamplerParams returns the resampler parameters from libopus
func GetLibopusResamplerParams(fsIn, fsOut int) LibopusResamplerParams {
	var inputDelay, invRatioQ16, batchSize C.int
	C.getResamplerParamsC(C.int(fsIn), C.int(fsOut), &inputDelay, &invRatioQ16, &batchSize)
	return LibopusResamplerParams{
		InputDelay:  int(inputDelay),
		InvRatioQ16: int(invRatioQ16),
		BatchSize:   int(batchSize),
	}
}

// ProcessLibopusResampler processes samples through the libopus resampler
func ProcessLibopusResampler(in []int16, fsIn, fsOut int) []int16 {
	if len(in) == 0 {
		return nil
	}
	outLen := len(in) * fsOut / fsIn
	out := make([]int16, outLen)
	C.processLibopusResamplerC(
		(*C.int16_t)(unsafe.Pointer(&out[0])),
		(*C.int16_t)(unsafe.Pointer(&in[0])),
		C.int(len(in)),
		C.int(fsIn),
		C.int(fsOut),
	)
	return out
}

// ProcessLibopusDown2 runs silk_resampler_down2 on the input.
func ProcessLibopusDown2(in []int16) []int16 {
	if len(in) == 0 {
		return nil
	}
	outLen := len(in) / 2
	out := make([]int16, outLen)
	C.processLibopusDown2C(
		(*C.int16_t)(unsafe.Pointer(&out[0])),
		(*C.int16_t)(unsafe.Pointer(&in[0])),
		C.int(len(in)),
	)
	return out
}

// ProcessLibopusDown2_3 runs silk_resampler_down2_3 on the input.
func ProcessLibopusDown2_3(in []int16) []int16 {
	if len(in) == 0 {
		return nil
	}
	outLen := len(in) * 2 / 3
	out := make([]int16, outLen)
	C.processLibopusDown2_3C(
		(*C.int16_t)(unsafe.Pointer(&out[0])),
		(*C.int16_t)(unsafe.Pointer(&in[0])),
		C.int(len(in)),
	)
	return out
}
