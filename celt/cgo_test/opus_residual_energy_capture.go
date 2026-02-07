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
	int x_len;
	float x[ MAX_FRAME_LENGTH + MAX_NB_SUBFR * MAX_LPC_ORDER ];
	float a0[ MAX_LPC_ORDER ];
	float a1[ MAX_LPC_ORDER ];
	float gains[ MAX_NB_SUBFR ];
	float nrgs[ MAX_NB_SUBFR ];
} opus_residual_energy_frame_snapshot;

static opus_residual_energy_frame_snapshot g_re_snapshot;
static int g_re_target_frame = -1;
static int g_re_current_frame = -1;

#define silk_residual_energy_FLP real_silk_residual_energy_FLP
#include "silk/float/residual_energy_FLP.c"
#undef silk_residual_energy_FLP

void silk_residual_energy_FLP(
	silk_float nrgs[ MAX_NB_SUBFR ],
	const silk_float x[],
	silk_float a[ 2 ][ MAX_LPC_ORDER ],
	const silk_float gains[],
	const opus_int subfr_length,
	const opus_int nb_subfr,
	const opus_int LPC_order
) {
	int capture = (g_re_current_frame >= 0 && g_re_current_frame == g_re_target_frame);
	int i;
	int max_subfr = nb_subfr;
	int x_len = nb_subfr * (subfr_length + LPC_order);
	int x_cap = (int)(sizeof(g_re_snapshot.x) / sizeof(g_re_snapshot.x[0]));

	if (max_subfr > MAX_NB_SUBFR) {
		max_subfr = MAX_NB_SUBFR;
	}

	if (capture) {
		g_re_snapshot.valid = 1;
		g_re_snapshot.encode_frame = g_re_current_frame;
		g_re_snapshot.calls_in_frame += 1;
		g_re_snapshot.nb_subfr = max_subfr;
		g_re_snapshot.subfr_length = subfr_length;
		g_re_snapshot.lpc_order = LPC_order;
		g_re_snapshot.x_len = x_len;

		for (i = 0; i < MAX_NB_SUBFR; i++) {
			g_re_snapshot.gains[i] = 0.0f;
			g_re_snapshot.nrgs[i] = 0.0f;
		}
		for (i = 0; i < max_subfr; i++) {
			g_re_snapshot.gains[i] = gains[i];
		}
		for (i = 0; i < MAX_LPC_ORDER; i++) {
			g_re_snapshot.a0[i] = 0.0f;
			g_re_snapshot.a1[i] = 0.0f;
		}
		for (i = 0; i < LPC_order && i < MAX_LPC_ORDER; i++) {
			g_re_snapshot.a0[i] = a[0][i];
			g_re_snapshot.a1[i] = a[1][i];
		}
		if (x_len < 0) {
			x_len = 0;
		}
		if (x_len > x_cap) {
			x_len = x_cap;
		}
		for (i = 0; i < x_cap; i++) {
			g_re_snapshot.x[i] = 0.0f;
		}
		for (i = 0; i < x_len; i++) {
			g_re_snapshot.x[i] = x[i];
		}
	}

	real_silk_residual_energy_FLP(nrgs, x, a, gains, subfr_length, nb_subfr, LPC_order);

	if (capture) {
		for (i = 0; i < max_subfr; i++) {
			g_re_snapshot.nrgs[i] = nrgs[i];
		}
	}
}

static int test_capture_opus_residual_energy_frame(
	const float *samples,
	int total_samples,
	int sample_rate,
	int channels,
	int bitrate,
	int frame_size,
	int frame_index,
	opus_residual_energy_frame_snapshot *out
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

		memset(&g_re_snapshot, 0, sizeof(g_re_snapshot));
		g_re_target_frame = frame_index;
		g_re_current_frame = -1;

		for (i = 0; i <= frame_index; i++) {
			const float *frame = samples + i * samples_per_frame;
			int n;
			g_re_current_frame = i;
			n = opus_encode_float(enc, frame, frame_size, packet, (opus_int32)sizeof(packet));
			g_re_current_frame = -1;
			if (n < 0) {
				g_re_target_frame = -1;
				opus_encoder_destroy(enc);
				return -4;
			}
		}
	}

	g_re_target_frame = -1;
	if (!g_re_snapshot.valid) {
		opus_encoder_destroy(enc);
		return -5;
	}
	if (out) {
		*out = g_re_snapshot;
	}
	opus_encoder_destroy(enc);
	return 0;
}
*/
import "C"

import "unsafe"

// OpusResidualEnergyFrameSnapshot captures libopus residual_energy_FLP inputs from a full Opus encode.
type OpusResidualEnergyFrameSnapshot struct {
	EncodeFrame int
	CallsInFrame int
	NumSubframes int
	SubframeLength int
	LPCOrder int
	X []float32
	A0 []float32
	A1 []float32
	Gains []float32
	ResNrg []float32
}

// CaptureOpusResidualEnergyAtFrame captures silk_residual_energy_FLP state from libopus full encode.
func CaptureOpusResidualEnergyAtFrame(samples []float32, sampleRate, channels, bitrate, frameSize, frameIndex int) (OpusResidualEnergyFrameSnapshot, bool) {
	if len(samples) == 0 || frameSize <= 0 || channels <= 0 || frameIndex < 0 {
		return OpusResidualEnergyFrameSnapshot{}, false
	}
	var out C.opus_residual_energy_frame_snapshot
	ret := C.test_capture_opus_residual_energy_frame(
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
		return OpusResidualEnergyFrameSnapshot{}, false
	}

	numSubfr := int(out.nb_subfr)
	if numSubfr < 0 {
		numSubfr = 0
	}
	if numSubfr > 4 {
		numSubfr = 4
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

	snap := OpusResidualEnergyFrameSnapshot{
		EncodeFrame: int(out.encode_frame),
		CallsInFrame: int(out.calls_in_frame),
		NumSubframes: numSubfr,
		SubframeLength: int(out.subfr_length),
		LPCOrder: lpcOrder,
		X: make([]float32, xLen),
		A0: make([]float32, lpcOrder),
		A1: make([]float32, lpcOrder),
		Gains: make([]float32, numSubfr),
		ResNrg: make([]float32, numSubfr),
	}

	xC := unsafe.Slice((*C.float)(unsafe.Pointer(&out.x[0])), xLen)
	for i := 0; i < xLen; i++ {
		snap.X[i] = float32(xC[i])
	}
	a0C := unsafe.Slice((*C.float)(unsafe.Pointer(&out.a0[0])), lpcOrder)
	a1C := unsafe.Slice((*C.float)(unsafe.Pointer(&out.a1[0])), lpcOrder)
	for i := 0; i < lpcOrder; i++ {
		snap.A0[i] = float32(a0C[i])
		snap.A1[i] = float32(a1C[i])
	}
	gC := unsafe.Slice((*C.float)(unsafe.Pointer(&out.gains[0])), numSubfr)
	nC := unsafe.Slice((*C.float)(unsafe.Pointer(&out.nrgs[0])), numSubfr)
	for i := 0; i < numSubfr; i++ {
		snap.Gains[i] = float32(gC[i])
		snap.ResNrg[i] = float32(nC[i])
	}

	return snap, true
}
