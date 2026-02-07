//go:build cgo_libopus
// +build cgo_libopus

package celt

/*
#cgo CFLAGS: -I${SRCDIR}/../tmp_check/opus-1.6.1/include -I${SRCDIR}/../tmp_check/opus-1.6.1/celt -I${SRCDIR}/../tmp_check/opus-1.6.1 -DHAVE_CONFIG_H
#cgo LDFLAGS: ${SRCDIR}/../tmp_check/opus-1.6.1/.libs/libopus.a -lm

#include <stdlib.h>
#include <math.h>
#include "opus_types.h"
#include "pitch.h"
#include "celt.h"

static void test_libopus_run_prefilter_ref(
	const float *pre,
	int channels,
	int N,
	int prefilter_tapset,
	int prev_period,
	float prev_gain,
	int prev_tapset,
	int enabled,
	int complexity,
	float tf_estimate,
	int nbAvailableBytes,
	float tone_freq,
	float toneishness,
	float max_pitch_ratio,
	const float *window,
	int overlap,
	int *out_pf_on,
	int *out_pitch,
	int *out_qg,
	float *out_gain
) {
	const int max_period = COMBFILTER_MAXPERIOD;
	const int min_period = COMBFILTER_MINPERIOD;
	int pitch_index = min_period;
	float gain1 = 0;
	float pf_threshold;
	int pf_on;
	int qg;
	float before[2] = {0,0};
	float after[2] = {0,0};
	int cancel_pitch = 0;
	int c, i;
	int shortMdctSize = 120;
	int offset = shortMdctSize - overlap;
	if (offset < 0) offset = 0;

	float *out = (float*)malloc((size_t)channels * (size_t)(N + max_period) * sizeof(float));
	if (!out) {
		*out_pf_on = 0;
		*out_pitch = min_period;
		*out_qg = 0;
		*out_gain = 0;
		return;
	}
	for (c = 0; c < channels; c++) {
		const float *pre_c = pre + c*(N + max_period);
		float *out_c = out + c*(N + max_period);
		for (i = 0; i < N + max_period; i++) out_c[i] = pre_c[i];
	}

	if (prefilter_tapset < 0) prefilter_tapset = 0;
	if (prefilter_tapset > 2) prefilter_tapset = 2;
	if (prev_tapset < 0) prev_tapset = 0;
	if (prev_tapset > 2) prev_tapset = 2;

	if (enabled && toneishness > .99f) {
		int multiple = 1;
		if (tone_freq >= 3.1416f) tone_freq = 3.141593f - tone_freq;
		while (tone_freq >= multiple * .39f) multiple++;
		if (tone_freq > .006148f) {
			pitch_index = (int)floorf(.5f + 2.f * (float)M_PI * multiple / tone_freq);
			if (pitch_index > max_period - 2) pitch_index = max_period - 2;
		} else {
			pitch_index = min_period;
		}
		gain1 = .75f;
	} else if (enabled && complexity >= 5) {
		int down_len = (max_period + N) >> 1;
		opus_val16 *pitch_buf = (opus_val16*)malloc((size_t)down_len * sizeof(opus_val16));
		opus_val16 *x[2];
		if (!pitch_buf) {
			free(out);
			*out_pf_on = 0;
			*out_pitch = min_period;
			*out_qg = 0;
			*out_gain = 0;
			return;
		}
		x[0] = (opus_val16*)pre;
		x[1] = channels == 2 ? ((opus_val16*)pre + (N + max_period)) : NULL;
		pitch_downsample(x, pitch_buf, down_len, channels, 2, 0);
		pitch_search(pitch_buf + (max_period >> 1), pitch_buf, N,
			max_period - 3*min_period, &pitch_index, 0);
		pitch_index = max_period - pitch_index;
		gain1 = remove_doubling(pitch_buf, max_period, min_period,
			N, &pitch_index, prev_period, prev_gain, 0);
		if (pitch_index > max_period - 2) pitch_index = max_period - 2;
		gain1 = .7f * gain1;
		free(pitch_buf);
	} else {
		gain1 = 0;
		pitch_index = min_period;
	}

	if (max_pitch_ratio < 0) max_pitch_ratio = 0;
	if (max_pitch_ratio > 1) max_pitch_ratio = 1;
	gain1 *= max_pitch_ratio;

	pf_threshold = .2f;
	if (abs(pitch_index - prev_period) * 10 > pitch_index) {
		pf_threshold += .2f;
		if (tf_estimate > .98f) gain1 = 0;
	}
	if (nbAvailableBytes < 25) pf_threshold += .1f;
	if (nbAvailableBytes < 35) pf_threshold += .1f;
	if (prev_gain > .4f) pf_threshold -= .1f;
	if (prev_gain > .55f) pf_threshold -= .1f;
	if (pf_threshold < .2f) pf_threshold = .2f;

	if (gain1 < pf_threshold) {
		gain1 = 0;
		pf_on = 0;
		qg = 0;
	} else {
		if (fabsf(gain1 - prev_gain) < .1f) gain1 = prev_gain;
		qg = (int)floorf(.5f + gain1 * 32.f / 3.f) - 1;
		if (qg < 0) qg = 0;
		if (qg > 7) qg = 7;
		gain1 = .09375f * (qg + 1);
		pf_on = 1;
	}

	if (prev_period < min_period) prev_period = min_period;
	for (c = 0; c < channels; c++) {
		const float *pre_c = pre + c*(N + max_period);
		float *out_c = out + c*(N + max_period);
		for (i = 0; i < N; i++) before[c] += fabsf(pre_c[max_period + i]);
		if (offset > 0) {
			comb_filter(out_c + max_period, (opus_val32*)(pre_c + max_period),
				prev_period, prev_period, offset,
				-prev_gain, -prev_gain, prev_tapset, prev_tapset,
				NULL, 0, 0);
		}
		comb_filter(out_c + max_period + offset, (opus_val32*)(pre_c + max_period + offset),
			prev_period, pitch_index, N - offset,
			-prev_gain, -gain1, prev_tapset, prefilter_tapset,
			(opus_val16*)window, overlap, 0);
		for (i = 0; i < N; i++) after[c] += fabsf(out_c[max_period + i]);
	}

	if (channels == 2) {
		float thresh0 = .25f*gain1*before[0] + .01f*before[1];
		float thresh1 = .25f*gain1*before[1] + .01f*before[0];
		if (after[0] - before[0] > thresh0 || after[1] - before[1] > thresh1) cancel_pitch = 1;
		if (before[0] - after[0] < thresh0 && before[1] - after[1] < thresh1) cancel_pitch = 1;
	} else {
		if (after[0] > before[0]) cancel_pitch = 1;
	}

	if (cancel_pitch) {
		gain1 = 0;
		pf_on = 0;
		qg = 0;
	}

	free(out);
	*out_pf_on = pf_on;
	*out_pitch = pitch_index;
	*out_qg = qg;
	*out_gain = gain1;
}
*/
import "C"

import "unsafe"

type libopusRunPrefilterResult struct {
	on    bool
	pitch int
	qg    int
	gain  float64
}

func libopusRunPrefilterRef(pre []float64, channels, frameSize int, prefilterTapset, prevPeriod int, prevGain float64, prevTapset int, enabled bool, complexity int, tfEstimate float64, nbAvailableBytes int, toneFreq, toneishness, maxPitchRatio float64, window []float64, overlap int) libopusRunPrefilterResult {
	if channels <= 0 || frameSize <= 0 || len(pre) < channels*(combFilterMaxPeriod+frameSize) {
		return libopusRunPrefilterResult{pitch: combFilterMinPeriod}
	}
	pre32 := make([]float32, len(pre))
	for i := range pre {
		pre32[i] = float32(pre[i])
	}
	win32 := make([]float32, len(window))
	for i := range window {
		win32[i] = float32(window[i])
	}

	cEnabled := C.int(0)
	if enabled {
		cEnabled = 1
	}
	var cOn C.int
	var cPitch C.int
	var cQG C.int
	var cGain C.float

	C.test_libopus_run_prefilter_ref(
		(*C.float)(unsafe.Pointer(&pre32[0])),
		C.int(channels),
		C.int(frameSize),
		C.int(prefilterTapset),
		C.int(prevPeriod),
		C.float(prevGain),
		C.int(prevTapset),
		cEnabled,
		C.int(complexity),
		C.float(tfEstimate),
		C.int(nbAvailableBytes),
		C.float(toneFreq),
		C.float(toneishness),
		C.float(maxPitchRatio),
		(*C.float)(unsafe.Pointer(&win32[0])),
		C.int(overlap),
		&cOn,
		&cPitch,
		&cQG,
		&cGain,
	)
	return libopusRunPrefilterResult{
		on:    cOn != 0,
		pitch: int(cPitch),
		qg:    int(cQG),
		gain:  float64(cGain),
	}
}
