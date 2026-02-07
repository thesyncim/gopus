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
	int nb_subfr;
	int subfr_length;
	int lpc_order;
	int use_interp;
	int first_frame_after_reset;
	float min_inv_gain;
	int x_len;
	float x[ MAX_FRAME_LENGTH + MAX_NB_SUBFR * MAX_LPC_ORDER ];
	opus_int16 prev_nlsf_q15[ MAX_LPC_ORDER ];
	opus_int16 nlsf_q15[ MAX_LPC_ORDER ];
	int interp_q2;
} opus_find_lpc_frame_snapshot;

static opus_find_lpc_frame_snapshot g_lpc_snapshot;
static int g_lpc_target_frame = -1;
static int g_lpc_current_frame = -1;

#define silk_find_LPC_FLP real_silk_find_LPC_FLP
#include "silk/float/find_LPC_FLP.c"
#undef silk_find_LPC_FLP

void silk_find_LPC_FLP(
	silk_encoder_state *psEncC,
	opus_int16 NLSF_Q15[ MAX_LPC_ORDER ],
	const silk_float *x,
	silk_float minInvGain,
	int arch
) {
	int capture = (g_lpc_current_frame >= 0 && g_lpc_current_frame == g_lpc_target_frame);
	int i;
	int nb_subfr = psEncC->nb_subfr;
	int subfr_length = psEncC->subfr_length;
	int lpc_order = psEncC->predictLPCOrder;
	int x_len = nb_subfr * (subfr_length + lpc_order);
	int x_cap = (int)(sizeof(g_lpc_snapshot.x) / sizeof(g_lpc_snapshot.x[0]));

	if (capture) {
		g_lpc_snapshot.valid = 1;
		g_lpc_snapshot.encode_frame = g_lpc_current_frame;
		g_lpc_snapshot.calls_in_frame += 1;
		g_lpc_snapshot.nb_subfr = nb_subfr;
		g_lpc_snapshot.subfr_length = subfr_length;
		g_lpc_snapshot.lpc_order = lpc_order;
		g_lpc_snapshot.use_interp = psEncC->useInterpolatedNLSFs;
		g_lpc_snapshot.first_frame_after_reset = psEncC->first_frame_after_reset;
		g_lpc_snapshot.min_inv_gain = minInvGain;

		if (x_len < 0) {
			x_len = 0;
		}
		if (x_len > x_cap) {
			x_len = x_cap;
		}
		g_lpc_snapshot.x_len = x_len;
		for (i = 0; i < x_cap; i++) {
			g_lpc_snapshot.x[i] = 0.0f;
		}
		for (i = 0; i < x_len; i++) {
			g_lpc_snapshot.x[i] = x[i];
		}
		for (i = 0; i < MAX_LPC_ORDER; i++) {
			g_lpc_snapshot.prev_nlsf_q15[i] = 0;
			g_lpc_snapshot.nlsf_q15[i] = 0;
		}
		for (i = 0; i < lpc_order && i < MAX_LPC_ORDER; i++) {
			g_lpc_snapshot.prev_nlsf_q15[i] = psEncC->prev_NLSFq_Q15[i];
		}
	}

	real_silk_find_LPC_FLP(psEncC, NLSF_Q15, x, minInvGain, arch);

	if (capture) {
		g_lpc_snapshot.interp_q2 = psEncC->indices.NLSFInterpCoef_Q2;
		for (i = 0; i < lpc_order && i < MAX_LPC_ORDER; i++) {
			g_lpc_snapshot.nlsf_q15[i] = NLSF_Q15[i];
		}
	}
}

static int test_capture_opus_find_lpc_frame(
	const float *samples,
	int total_samples,
	int sample_rate,
	int channels,
	int bitrate,
	int frame_size,
	int frame_index,
	opus_find_lpc_frame_snapshot *out
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

		memset(&g_lpc_snapshot, 0, sizeof(g_lpc_snapshot));
		g_lpc_target_frame = frame_index;
		g_lpc_current_frame = -1;

		for (i = 0; i <= frame_index; i++) {
			const float *frame = samples + i * samples_per_frame;
			int n;
			g_lpc_current_frame = i;
			n = opus_encode_float(enc, frame, frame_size, packet, (opus_int32)sizeof(packet));
			g_lpc_current_frame = -1;
			if (n < 0) {
				g_lpc_target_frame = -1;
				opus_encoder_destroy(enc);
				return -4;
			}
		}
	}

	g_lpc_target_frame = -1;
	if (!g_lpc_snapshot.valid) {
		opus_encoder_destroy(enc);
		return -5;
	}
	if (out) {
		*out = g_lpc_snapshot;
	}
	opus_encoder_destroy(enc);
	return 0;
}
*/
import "C"

import "unsafe"

// OpusFindLPCFrameSnapshot captures libopus silk_find_LPC_FLP inputs/outputs from a full encode.
type OpusFindLPCFrameSnapshot struct {
	EncodeFrame int
	CallsInFrame int
	NumSubframes int
	SubframeLength int
	LPCOrder int
	UseInterp bool
	FirstFrameAfterReset bool
	MinInvGain float32
	X []float32
	PrevNLSFQ15 []int16
	NLSFQ15 []int16
	InterpQ2 int
}

// CaptureOpusFindLPCAtFrame captures silk_find_LPC_FLP state from libopus full encode.
func CaptureOpusFindLPCAtFrame(samples []float32, sampleRate, channels, bitrate, frameSize, frameIndex int) (OpusFindLPCFrameSnapshot, bool) {
	if len(samples) == 0 || frameSize <= 0 || channels <= 0 || frameIndex < 0 {
		return OpusFindLPCFrameSnapshot{}, false
	}
	var out C.opus_find_lpc_frame_snapshot
	ret := C.test_capture_opus_find_lpc_frame(
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
		return OpusFindLPCFrameSnapshot{}, false
	}

	lpcOrder := int(out.lpc_order)
	if lpcOrder < 0 {
		lpcOrder = 0
	}
	if lpcOrder > 16 {
		lpcOrder = 16
	}
	xLen := int(out.x_len)
	xCap := int(C.MAX_FRAME_LENGTH + C.MAX_NB_SUBFR*C.MAX_LPC_ORDER)
	if xLen < 0 {
		xLen = 0
	}
	if xLen > xCap {
		xLen = xCap
	}

	snap := OpusFindLPCFrameSnapshot{
		EncodeFrame: int(out.encode_frame),
		CallsInFrame: int(out.calls_in_frame),
		NumSubframes: int(out.nb_subfr),
		SubframeLength: int(out.subfr_length),
		LPCOrder: lpcOrder,
		UseInterp: int(out.use_interp) != 0,
		FirstFrameAfterReset: int(out.first_frame_after_reset) != 0,
		MinInvGain: float32(out.min_inv_gain),
		X: make([]float32, xLen),
		PrevNLSFQ15: make([]int16, lpcOrder),
		NLSFQ15: make([]int16, lpcOrder),
		InterpQ2: int(out.interp_q2),
	}

	xC := unsafe.Slice((*C.float)(unsafe.Pointer(&out.x[0])), xLen)
	for i := 0; i < xLen; i++ {
		snap.X[i] = float32(xC[i])
	}
	prevC := unsafe.Slice((*C.opus_int16)(unsafe.Pointer(&out.prev_nlsf_q15[0])), lpcOrder)
	nlsfC := unsafe.Slice((*C.opus_int16)(unsafe.Pointer(&out.nlsf_q15[0])), lpcOrder)
	for i := 0; i < lpcOrder; i++ {
		snap.PrevNLSFQ15[i] = int16(prevC[i])
		snap.NLSFQ15[i] = int16(nlsfC[i])
	}

	return snap, true
}
