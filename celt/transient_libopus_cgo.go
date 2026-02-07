//go:build cgo_libopus
// +build cgo_libopus

package celt

/*
#cgo CFLAGS: -I${SRCDIR}/../tmp_check/opus-1.6.1/include -I${SRCDIR}/../tmp_check/opus-1.6.1/celt -I${SRCDIR}/../tmp_check/opus-1.6.1 -DHAVE_CONFIG_H
#cgo LDFLAGS: ${SRCDIR}/../tmp_check/opus-1.6.1/.libs/libopus.a -lm

#include <stdlib.h>
#include <math.h>

static void test_libopus_transient_analysis_ref(
	const float *in, int len, int channels, int allow_weak_transients,
	float tone_freq, float toneishness,
	int *is_transient_out, float *tf_estimate_out, int *tf_chan_out, int *weak_transient_out, int *mask_metric_out
) {
	int i, c;
	int len2 = len / 2;
	int mask_metric = 0;
	int tf_chan = 0;
	int weak_transient = 0;
	float tf_estimate = 0;
	const float EPS = 1e-15f;
	float forward_decay = allow_weak_transients ? 0.03125f : 0.0625f;
	static const unsigned char inv_table[128] = {
		255,255,156,110,86,70,59,51,45,40,37,33,31,28,26,25,
		23,22,21,20,19,18,17,16,16,15,15,14,13,13,12,12,
		12,12,11,11,11,10,10,10,9,9,9,9,9,9,8,8,
		8,8,8,7,7,7,7,7,7,6,6,6,6,6,6,6,
		6,6,6,6,6,6,6,6,6,5,5,5,5,5,5,5,
		5,5,5,5,5,4,4,4,4,4,4,4,4,4,4,4,
		4,4,4,4,4,4,4,4,4,4,4,4,4,4,3,3,
		3,3,3,3,3,3,3,3,3,3,3,3,3,3,3,2,
	};

	float *tmp = (float*)malloc((size_t)len * sizeof(float));
	if (!tmp) {
		*is_transient_out = 0;
		*tf_estimate_out = 0;
		*tf_chan_out = 0;
		*weak_transient_out = 0;
		*mask_metric_out = 0;
		return;
	}

	for (c = 0; c < channels; c++) {
		float mem0 = 0, mem1 = 0;
		float mean = 0;
		float maxE = 0;
		int unmask = 0;
		float norm;

		for (i = 0; i < len; i++) {
			float x = in[i + c*len];
			float y = mem0 + x;
			float mem00 = mem0;
			mem0 = mem0 - x + .5f*mem1;
			mem1 = x - mem00;
			tmp[i] = y;
		}
		for (i = 0; i < 12 && i < len; i++) tmp[i] = 0;

		mean = 0;
		mem0 = 0;
		for (i = 0; i < len2; i++) {
			float x2 = tmp[2*i]*tmp[2*i] + tmp[2*i+1]*tmp[2*i+1];
			mean += x2;
			mem0 = x2 + (1.f-forward_decay)*mem0;
			tmp[i] = forward_decay*mem0;
		}

		mem0 = 0;
		maxE = 0;
		for (i = len2-1; i >= 0; i--) {
			mem0 = tmp[i] + .875f*mem0;
			tmp[i] = .125f*mem0;
			if (tmp[i] > maxE) maxE = tmp[i];
		}

		mean = sqrtf(mean * maxE * .5f * len2);
		norm = (float)len2 / (EPS + .5f*mean);

		unmask = 0;
		for (i = 12; i < len2-5; i += 4) {
			int id = (int)fmaxf(0.f, fminf(127.f, floorf(64.f*norm*(tmp[i]+EPS))));
			unmask += inv_table[id];
		}

		if (len2 > 17) {
			unmask = 64*unmask*4/(6*(len2-17));
		} else {
			unmask = 0;
		}
		if (unmask > mask_metric) {
			tf_chan = c;
			mask_metric = unmask;
		}
	}

	int is_transient = mask_metric > 200;
	if (toneishness > .98f && tone_freq < .026f) {
		is_transient = 0;
		mask_metric = 0;
	}
	if (allow_weak_transients && is_transient && mask_metric < 600) {
		is_transient = 0;
		weak_transient = 1;
	}

	float tf_max = fmaxf(0.f, sqrtf(27.f*(float)mask_metric) - 42.f);
	float clamped = fminf(163.f, tf_max);
	float v = 0.0069f*clamped - 0.139f;
	if (v < 0) v = 0;
	tf_estimate = sqrtf(v);

	free(tmp);
	*is_transient_out = is_transient;
	*tf_estimate_out = tf_estimate;
	*tf_chan_out = tf_chan;
	*weak_transient_out = weak_transient;
	*mask_metric_out = mask_metric;
}
*/
import "C"

import "unsafe"

type libopusTransientRef struct {
	isTransient   bool
	tfEstimate    float64
	tfChan        int
	weakTransient bool
	maskMetric    float64
}

func libopusTransientAnalysisRef(interleaved []float64, channels int, allowWeakTransients bool, toneFreq, toneishness float64) libopusTransientRef {
	if channels <= 0 || len(interleaved) < channels {
		return libopusTransientRef{}
	}
	n := len(interleaved) / channels
	if n < 16 {
		return libopusTransientRef{}
	}

	// Convert interleaved input to channel-contiguous layout expected by libopus analysis.
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

	var cIsTransient C.int
	var cTfEstimate C.float
	var cTfChan C.int
	var cWeakTransient C.int
	var cMaskMetric C.int
	allowWeak := C.int(0)
	if allowWeakTransients {
		allowWeak = 1
	}

	C.test_libopus_transient_analysis_ref(
		(*C.float)(unsafe.Pointer(&in[0])),
		C.int(n),
		C.int(channels),
		allowWeak,
		C.float(toneFreq),
		C.float(toneishness),
		&cIsTransient,
		&cTfEstimate,
		&cTfChan,
		&cWeakTransient,
		&cMaskMetric,
	)

	return libopusTransientRef{
		isTransient:   cIsTransient != 0,
		tfEstimate:    float64(cTfEstimate),
		tfChan:        int(cTfChan),
		weakTransient: cWeakTransient != 0,
		maskMetric:    float64(cMaskMetric),
	}
}

