// Package cgo provides wrappers for libopus resampler functions
package cgo

/*
#cgo CFLAGS: -I${SRCDIR}/../../../tmp_check/opus-1.6.1/include -I${SRCDIR}/../../../tmp_check/opus-1.6.1/silk -I${SRCDIR}/../../../tmp_check/opus-1.6.1/celt -I${SRCDIR}/../../../tmp_check/opus-1.6.1
#cgo LDFLAGS: -L${SRCDIR}/../../../tmp_check/opus-1.6.1/.libs -lopus -lm
#include <stdlib.h>
#include <string.h>
#include "opus_types.h"
#include "silk/resampler_structs.h"

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
