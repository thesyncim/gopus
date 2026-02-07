//go:build cgo_libopus
// +build cgo_libopus

package celt

/*
#cgo CFLAGS: -I${SRCDIR}/../tmp_check/opus-1.6.1/include -I${SRCDIR}/../tmp_check/opus-1.6.1/celt -I${SRCDIR}/../tmp_check/opus-1.6.1 -DHAVE_CONFIG_H
#cgo LDFLAGS: ${SRCDIR}/../tmp_check/opus-1.6.1/.libs/libopus.a -lm

#include <stdlib.h>
#include "opus_types.h"
#include "pitch.h"

static void test_libopus_prefilter_pitch_core(
	const float *pre,
	int channels,
	int N,
	int max_period,
	int min_period,
	int prev_period,
	float prev_gain,
	int *out_pitch,
	float *out_gain
) {
	int down_len = (max_period + N) >> 1;
	opus_val16 *pitch_buf = (opus_val16*)malloc((size_t)down_len * sizeof(opus_val16));
	opus_val16 *x[2];
	int pitch_index = 0;
	opus_val16 gain1;

	x[0] = (opus_val16*)pre;
	x[1] = channels == 2 ? ((opus_val16*)pre + (N + max_period)) : NULL;

	pitch_downsample(x, pitch_buf, down_len, channels, 2, 0);
	pitch_search(pitch_buf + (max_period >> 1), pitch_buf, N,
		max_period - 3*min_period, &pitch_index, 0);
	pitch_index = max_period - pitch_index;
	gain1 = remove_doubling(pitch_buf, max_period, min_period,
		N, &pitch_index, prev_period, prev_gain, 0);
	if (pitch_index > max_period - 2) {
		pitch_index = max_period - 2;
	}
	gain1 = 0.7f * gain1;

	*out_pitch = pitch_index;
	*out_gain = gain1;
	free(pitch_buf);
}
*/
import "C"

import "unsafe"

func libopusPrefilterPitchCore(pre []float64, channels, frameSize, maxPeriod, minPeriod, prevPeriod int, prevGain float64) (int, float64) {
	pre32 := make([]float32, len(pre))
	for i, v := range pre {
		pre32[i] = float32(v)
	}

	var cPitch C.int
	var cGain C.float
	C.test_libopus_prefilter_pitch_core(
		(*C.float)(unsafe.Pointer(&pre32[0])),
		C.int(channels),
		C.int(frameSize),
		C.int(maxPeriod),
		C.int(minPeriod),
		C.int(prevPeriod),
		C.float(prevGain),
		&cPitch,
		&cGain,
	)
	return int(cPitch), float64(cGain)
}

