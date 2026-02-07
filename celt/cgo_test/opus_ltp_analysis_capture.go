//go:build cgo_libopus
// +build cgo_libopus

package cgo

/*
#cgo CFLAGS: -I${SRCDIR}/../../tmp_check/opus-1.6.1/include -I${SRCDIR}/../../tmp_check/opus-1.6.1/celt -I${SRCDIR}/../../tmp_check/opus-1.6.1 -I${SRCDIR}/../../tmp_check/opus-1.6.1/src -I${SRCDIR}/../../tmp_check/opus-1.6.1/silk -I${SRCDIR}/../../tmp_check/opus-1.6.1/silk/float -DHAVE_CONFIG_H
#cgo LDFLAGS: ${SRCDIR}/../../tmp_check/opus-1.6.1/.libs/libopus.a -lm

#include <string.h>
#include "opus.h"
#include "silk/float/main_FLP.h"

typedef struct {
	int valid;
	int encode_frame;
	int calls_in_frame;
	int subfr_length;
	int nb_subfr;
	int pre_length;
	int x_len;
	float x[ MAX_FRAME_LENGTH + MAX_NB_SUBFR * MAX_LPC_ORDER ];
	float b[ MAX_NB_SUBFR * LTP_ORDER ];
	int pitch[ MAX_NB_SUBFR ];
	float inv_gains[ MAX_NB_SUBFR ];
	int out_len;
	float out[ MAX_FRAME_LENGTH + MAX_NB_SUBFR * MAX_LPC_ORDER ];
} opus_ltp_analysis_frame_snapshot;

static opus_ltp_analysis_frame_snapshot g_ltp_snapshot;
static int g_ltp_target_frame = -1;
static int g_ltp_current_frame = -1;

#define silk_LTP_analysis_filter_FLP real_silk_LTP_analysis_filter_FLP
#include "silk/float/LTP_analysis_filter_FLP.c"
#undef silk_LTP_analysis_filter_FLP

void silk_LTP_analysis_filter_FLP(
	silk_float *LTP_res,
	const silk_float *x,
	const silk_float B[ LTP_ORDER * MAX_NB_SUBFR ],
	const opus_int pitchL[ MAX_NB_SUBFR ],
	const silk_float invGains[ MAX_NB_SUBFR ],
	const opus_int subfr_length,
	const opus_int nb_subfr,
	const opus_int pre_length
) {
	int capture = (g_ltp_current_frame >= 0 && g_ltp_current_frame == g_ltp_target_frame);
	int i;
	int x_len = nb_subfr * (subfr_length + pre_length);
	int x_cap = (int)(sizeof(g_ltp_snapshot.x) / sizeof(g_ltp_snapshot.x[0]));
	int out_len = x_len;
	int out_cap = (int)(sizeof(g_ltp_snapshot.out) / sizeof(g_ltp_snapshot.out[0]));
	int max_subfr = nb_subfr;
	if (max_subfr > MAX_NB_SUBFR) {
		max_subfr = MAX_NB_SUBFR;
	}

	if (capture) {
		g_ltp_snapshot.valid = 1;
		g_ltp_snapshot.encode_frame = g_ltp_current_frame;
		g_ltp_snapshot.calls_in_frame += 1;
		g_ltp_snapshot.subfr_length = subfr_length;
		g_ltp_snapshot.nb_subfr = max_subfr;
		g_ltp_snapshot.pre_length = pre_length;
		g_ltp_snapshot.x_len = x_len;
		if (x_len < 0) {
			x_len = 0;
		}
		if (x_len > x_cap) {
			x_len = x_cap;
		}
		for (i = 0; i < x_cap; i++) {
			g_ltp_snapshot.x[i] = 0.0f;
		}
		for (i = 0; i < x_len; i++) {
			g_ltp_snapshot.x[i] = x[i];
		}
		for (i = 0; i < MAX_NB_SUBFR * LTP_ORDER; i++) {
			g_ltp_snapshot.b[i] = 0.0f;
		}
		for (i = 0; i < max_subfr * LTP_ORDER; i++) {
			g_ltp_snapshot.b[i] = B[i];
		}
		for (i = 0; i < MAX_NB_SUBFR; i++) {
			g_ltp_snapshot.pitch[i] = 0;
			g_ltp_snapshot.inv_gains[i] = 0.0f;
		}
		for (i = 0; i < max_subfr; i++) {
			g_ltp_snapshot.pitch[i] = pitchL[i];
			g_ltp_snapshot.inv_gains[i] = invGains[i];
		}
		g_ltp_snapshot.out_len = out_len;
	}

	real_silk_LTP_analysis_filter_FLP(LTP_res, x, B, pitchL, invGains, subfr_length, nb_subfr, pre_length);

	if (capture) {
		if (out_len < 0) {
			out_len = 0;
		}
		if (out_len > out_cap) {
			out_len = out_cap;
		}
		for (i = 0; i < out_cap; i++) {
			g_ltp_snapshot.out[i] = 0.0f;
		}
		for (i = 0; i < out_len; i++) {
			g_ltp_snapshot.out[i] = LTP_res[i];
		}
	}
}

static int test_capture_opus_ltp_analysis_frame(
	const float *samples,
	int total_samples,
	int sample_rate,
	int channels,
	int bitrate,
	int frame_size,
	int frame_index,
	opus_ltp_analysis_frame_snapshot *out
) {
	int err = OPUS_OK;
	int i;
	unsigned char packet[1500];

	OpusEncoder *enc = opus_encoder_create(sample_rate, channels, OPUS_APPLICATION_RESTRICTED_SILK, &err);
	if (err != OPUS_OK || !enc) {
		return -1;
	}

	opus_encoder_ctl(enc, OPUS_SET_BITRATE(bitrate));
	opus_encoder_ctl(enc, OPUS_SET_BANDWIDTH(OPUS_BANDWIDTH_WIDEBAND));
	opus_encoder_ctl(enc, OPUS_SET_COMPLEXITY(10));
	opus_encoder_ctl(enc, OPUS_SET_INBAND_FEC(0));
	opus_encoder_ctl(enc, OPUS_SET_DTX(0));
	opus_encoder_ctl(enc, OPUS_SET_VBR(1));

	{
		const int samples_per_frame = frame_size * channels;
		const int n_frames = total_samples / samples_per_frame;
		if (samples_per_frame <= 0) {
			opus_encoder_destroy(enc);
			return -2;
		}
		if (frame_index < 0 || frame_index >= n_frames) {
			opus_encoder_destroy(enc);
			return -3;
		}

		memset(&g_ltp_snapshot, 0, sizeof(g_ltp_snapshot));
		g_ltp_target_frame = frame_index;
		g_ltp_current_frame = -1;

		for (i = 0; i <= frame_index; i++) {
			const float *frame = samples + i * samples_per_frame;
			int n;
			g_ltp_current_frame = i;
			n = opus_encode_float(enc, frame, frame_size, packet, (opus_int32)sizeof(packet));
			g_ltp_current_frame = -1;
			if (n < 0) {
				g_ltp_target_frame = -1;
				opus_encoder_destroy(enc);
				return -4;
			}
		}
	}

	g_ltp_target_frame = -1;
	if (!g_ltp_snapshot.valid) {
		opus_encoder_destroy(enc);
		return -5;
	}
	if (out) {
		*out = g_ltp_snapshot;
	}
	opus_encoder_destroy(enc);
	return 0;
}
*/
import "C"

import "unsafe"

// OpusLTPAnalysisFrameSnapshot captures libopus silk_LTP_analysis_filter_FLP inputs/outputs from full encode.
type OpusLTPAnalysisFrameSnapshot struct {
	EncodeFrame int
	CallsInFrame int
	SubframeLength int
	NumSubframes int
	PreLength int
	X []float32
	B []float32
	Pitch []int
	InvGains []float32
	Out []float32
}

// CaptureOpusLTPAnalysisAtFrame captures silk_LTP_analysis_filter_FLP state from libopus full encode.
func CaptureOpusLTPAnalysisAtFrame(samples []float32, sampleRate, channels, bitrate, frameSize, frameIndex int) (OpusLTPAnalysisFrameSnapshot, bool) {
	if len(samples) == 0 || frameSize <= 0 || channels <= 0 || frameIndex < 0 {
		return OpusLTPAnalysisFrameSnapshot{}, false
	}
	var out C.opus_ltp_analysis_frame_snapshot
	ret := C.test_capture_opus_ltp_analysis_frame(
		(*C.float)(unsafe.Pointer(&samples[0])),
		C.int(len(samples)),
		C.int(sampleRate),
		C.int(channels),
		C.int(bitrate),
		C.int(frameSize),
		C.int(frameIndex),
		&out,
	)
	if ret != 0 {
		return OpusLTPAnalysisFrameSnapshot{}, false
	}

	numSubfr := int(out.nb_subfr)
	if numSubfr < 0 {
		numSubfr = 0
	}
	if numSubfr > 4 {
		numSubfr = 4
	}
	xLen := int(out.x_len)
	capLen := int(C.MAX_FRAME_LENGTH + C.MAX_NB_SUBFR*C.MAX_LPC_ORDER)
	if xLen < 0 {
		xLen = 0
	}
	if xLen > capLen {
		xLen = capLen
	}
	outLen := int(out.out_len)
	if outLen < 0 {
		outLen = 0
	}
	if outLen > capLen {
		outLen = capLen
	}

	snap := OpusLTPAnalysisFrameSnapshot{
		EncodeFrame: int(out.encode_frame),
		CallsInFrame: int(out.calls_in_frame),
		SubframeLength: int(out.subfr_length),
		NumSubframes: numSubfr,
		PreLength: int(out.pre_length),
		X: make([]float32, xLen),
		B: make([]float32, numSubfr*5),
		Pitch: make([]int, numSubfr),
		InvGains: make([]float32, numSubfr),
		Out: make([]float32, outLen),
	}

	xC := unsafe.Slice((*C.float)(unsafe.Pointer(&out.x[0])), xLen)
	for i := 0; i < xLen; i++ {
		snap.X[i] = float32(xC[i])
	}
	bC := unsafe.Slice((*C.float)(unsafe.Pointer(&out.b[0])), numSubfr*5)
	for i := 0; i < numSubfr*5; i++ {
		snap.B[i] = float32(bC[i])
	}
	pC := unsafe.Slice((*C.int)(unsafe.Pointer(&out.pitch[0])), numSubfr)
	gC := unsafe.Slice((*C.float)(unsafe.Pointer(&out.inv_gains[0])), numSubfr)
	for i := 0; i < numSubfr; i++ {
		snap.Pitch[i] = int(pC[i])
		snap.InvGains[i] = float32(gC[i])
	}
	oC := unsafe.Slice((*C.float)(unsafe.Pointer(&out.out[0])), outLen)
	for i := 0; i < outLen; i++ {
		snap.Out[i] = float32(oC[i])
	}

	return snap, true
}
