//go:build cgo_libopus
// +build cgo_libopus

package celt

/*
#cgo CFLAGS: -I${SRCDIR}/../tmp_check/opus-1.6.1/include -I${SRCDIR}/../tmp_check/opus-1.6.1/celt -I${SRCDIR}/../tmp_check/opus-1.6.1 -DHAVE_CONFIG_H
#cgo LDFLAGS: ${SRCDIR}/../tmp_check/opus-1.6.1/.libs/libopus.a -lm

#include <stdlib.h>
#include <math.h>

static int test_libopus_tone_lpc_ref(const float *x, int len, int delay, float *lpc0, float *lpc1) {
	int i;
	float r00 = 0, r01 = 0, r11 = 0, r02 = 0, r12 = 0, r22 = 0;
	float edges;
	float num0, num1, den;

	if (len <= 2*delay) return 1;

	for (i = 0; i < len - 2*delay; i++) {
		r00 += x[i] * x[i];
		r01 += x[i] * x[i + delay];
		r02 += x[i] * x[i + 2*delay];
	}

	edges = 0;
	for (i = 0; i < delay; i++) edges += x[len+i-2*delay]*x[len+i-2*delay] - x[i]*x[i];
	r11 = r00 + edges;

	edges = 0;
	for (i = 0; i < delay; i++) edges += x[len+i-delay]*x[len+i-delay] - x[i+delay]*x[i+delay];
	r22 = r11 + edges;

	edges = 0;
	for (i = 0; i < delay; i++) edges += x[len+i-2*delay]*x[len+i-delay] - x[i]*x[i+delay];
	r12 = r01 + edges;

	{
		float R00 = r00 + r22;
		float R01 = r01 + r12;
		float R11 = 2*r11;
		float R02 = 2*r02;
		float R12 = r12 + r01;
		r00 = R00;
		r01 = R01;
		r11 = R11;
		r02 = R02;
		r12 = R12;
	}

	den = r00*r11 - r01*r01;
	if (den < .001f*r00*r11) return 1;

	num1 = r02*r11 - r01*r12;
	if (num1 >= den) *lpc1 = 1.f;
	else if (num1 <= -den) *lpc1 = -1.f;
	else *lpc1 = num1 / den;

	num0 = r00*r12 - r02*r01;
	if (.5f*num0 >= den) *lpc0 = 1.999999f;
	else if (.5f*num0 <= -den) *lpc0 = -1.999999f;
	else *lpc0 = num0 / den;

	return 0;
}

static void test_libopus_tone_detect_ref(const float *in, int channels, int N, int Fs, float *freq, float *toneishness) {
	int i;
	int delay = 1;
	int fail;
	float lpc0 = 0, lpc1 = 0;
	float *x = (float*)malloc((size_t)N * sizeof(float));
	if (!x) {
		*freq = -1.f;
		*toneishness = 0.f;
		return;
	}

	if (channels == 2) {
		for (i = 0; i < N; i++) x[i] = in[i] + in[i + N];
	} else {
		for (i = 0; i < N; i++) x[i] = in[i];
	}

	fail = test_libopus_tone_lpc_ref(x, N, delay, &lpc0, &lpc1);
	while (delay <= Fs/3000 && (fail || (lpc0 > 1.f && lpc1 < 0.f))) {
		delay *= 2;
		if (delay*2 >= N) break;
		fail = test_libopus_tone_lpc_ref(x, N, delay, &lpc0, &lpc1);
	}

	if (!fail && lpc0*lpc0 + 3.999999f*lpc1 < 0.f) {
		*toneishness = -lpc1;
		*freq = acosf(.5f*lpc0) / (float)delay;
	} else {
		*freq = -1.f;
		*toneishness = 0.f;
	}

	free(x);
}
*/
import "C"

import "unsafe"

func libopusToneDetectRef(interleaved []float64, channels, sampleRate int) (float64, float64) {
	if channels <= 0 || len(interleaved) < channels {
		return -1, 0
	}
	n := len(interleaved) / channels
	if n <= 0 {
		return -1, 0
	}

	// Convert interleaved Go input to channel-contiguous C layout matching libopus "in".
	in := make([]float32, n*channels)
	if channels == 2 {
		for i := 0; i < n; i++ {
			in[i] = float32(interleaved[2*i])
			in[n+i] = float32(interleaved[2*i+1])
		}
	} else {
		for i := 0; i < n; i++ {
			in[i] = float32(interleaved[i])
		}
	}

	var cFreq C.float
	var cTone C.float
	C.test_libopus_tone_detect_ref(
		(*C.float)(unsafe.Pointer(&in[0])),
		C.int(channels),
		C.int(n),
		C.int(sampleRate),
		&cFreq,
		&cTone,
	)
	return float64(cFreq), float64(cTone)
}

