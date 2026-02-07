//go:build cgo_libopus
// +build cgo_libopus

package cgo

/*
#cgo CFLAGS: -I${SRCDIR}/../../tmp_check/opus-1.6.1/include -I${SRCDIR}/../../tmp_check/opus-1.6.1/celt -I${SRCDIR}/../../tmp_check/opus-1.6.1 -I${SRCDIR}/../../tmp_check/opus-1.6.1/src -I${SRCDIR}/../../tmp_check/opus-1.6.1/silk -I${SRCDIR}/../../tmp_check/opus-1.6.1/silk/float -DHAVE_CONFIG_H
#cgo LDFLAGS: ${SRCDIR}/../../tmp_check/opus-1.6.1/.libs/libopus.a -lm

#include <string.h>
#include <math.h>
#include "opus.h"
#include "silk/float/main_FLP.h"
#include "silk/tuning_parameters.h"

typedef struct {
	int valid;
	int frame;
	int nb_subfr;
	int shaping_lpc_order;
	float gains_pre[MAX_NB_SUBFR];   // gains after sqrt(nrg)*warped_gain, before gain_mult/gain_add
	float gains_post[MAX_NB_SUBFR];  // gains after gain_mult + gain_add
	float nrg[MAX_NB_SUBFR];         // Schur residual energy (pre-sqrt)
	float auto_corr0[MAX_NB_SUBFR];  // auto_corr[0] after white noise add
	float warped_gain_factor[MAX_NB_SUBFR]; // warped_gain() return value
	float ar[MAX_NB_SUBFR * MAX_SHAPE_LPC_ORDER]; // AR coefficients (post-bwexp, pre-true2monic)
	float gain_mult;
	float gain_add;
	float warping;
	float bw_exp;
	float snr_adj_db;
} opus_noise_shape_gains_snapshot;

static opus_noise_shape_gains_snapshot g_ns_snapshot;
static int g_ns_target_frame = -1;
static int g_ns_current_frame = -1;

// Hook: rename the real function and include the .c file
#define silk_noise_shape_analysis_FLP real_silk_noise_shape_analysis_FLP
#include "silk/float/noise_shape_analysis_FLP.c"
#undef silk_noise_shape_analysis_FLP

void silk_noise_shape_analysis_FLP(
	silk_encoder_state_FLP *psEnc,
	silk_encoder_control_FLP *psEncCtrl,
	const silk_float *pitch_res,
	const silk_float *x
) {
	int is_target = (g_ns_current_frame >= 0 && g_ns_current_frame == g_ns_target_frame);

	// Save pre-call state for back-computation
	float saved_gains[MAX_NB_SUBFR];
	float saved_ar[MAX_NB_SUBFR * MAX_SHAPE_LPC_ORDER];

	// Call the real function
	real_silk_noise_shape_analysis_FLP(psEnc, psEncCtrl, pitch_res, x);

	if (is_target) {
		int k;
		silk_float SNR_adj_dB, b, strength;
		silk_float gain_mult, gain_add;

		// Recompute SNR_adj_dB to get gain_mult and gain_add
		SNR_adj_dB = psEnc->sCmn.SNR_dB_Q7 * (1 / 128.0f);
		silk_float coding_quality = psEncCtrl->coding_quality;
		silk_float input_quality = psEncCtrl->input_quality;

		if (psEnc->sCmn.useCBR == 0) {
			b = 1.0f - psEnc->sCmn.speech_activity_Q8 * (1.0f / 256.0f);
			SNR_adj_dB -= BG_SNR_DECR_dB * coding_quality * (0.5f + 0.5f * input_quality) * b * b;
		}
		if (psEnc->sCmn.indices.signalType == TYPE_VOICED) {
			SNR_adj_dB += HARM_SNR_INCR_dB * psEnc->LTPCorr;
		} else {
			SNR_adj_dB += (-0.4f * psEnc->sCmn.SNR_dB_Q7 * (1 / 128.0f) + 6.0f) * (1.0f - input_quality);
		}

		gain_mult = (silk_float)pow(2.0f, -0.16f * SNR_adj_dB);
		gain_add = (silk_float)pow(2.0f, 0.16f * MIN_QGAIN_DB);

		g_ns_snapshot.valid = 1;
		g_ns_snapshot.frame = g_ns_current_frame;
		g_ns_snapshot.nb_subfr = psEnc->sCmn.nb_subfr;
		g_ns_snapshot.shaping_lpc_order = psEnc->sCmn.shapingLPCOrder;
		g_ns_snapshot.gain_mult = gain_mult;
		g_ns_snapshot.gain_add = gain_add;
		g_ns_snapshot.snr_adj_db = SNR_adj_dB;

		strength = FIND_PITCH_WHITE_NOISE_FRACTION * psEncCtrl->predGain;
		g_ns_snapshot.bw_exp = BANDWIDTH_EXPANSION / (1.0f + strength * strength);
		g_ns_snapshot.warping = (silk_float)psEnc->sCmn.warping_Q16 / 65536.0f + 0.01f * coding_quality;

		for (k = 0; k < psEnc->sCmn.nb_subfr && k < MAX_NB_SUBFR; k++) {
			g_ns_snapshot.gains_post[k] = psEncCtrl->Gains[k];
			// Back-compute gains_pre: Gains[k] = gains_pre[k] * gain_mult + gain_add
			// => gains_pre[k] = (Gains[k] - gain_add) / gain_mult
			g_ns_snapshot.gains_pre[k] = (psEncCtrl->Gains[k] - gain_add) / gain_mult;
		}
	}
}

static int test_capture_opus_noise_shape_frame(
	const float *samples,
	int total_samples,
	int sample_rate,
	int channels,
	int bitrate,
	int frame_size,
	int frame_index,
	opus_noise_shape_gains_snapshot *out
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

		memset(&g_ns_snapshot, 0, sizeof(g_ns_snapshot));
		g_ns_target_frame = frame_index;
		g_ns_current_frame = -1;

		for (i = 0; i <= frame_index; i++) {
			const float *frame = samples + i * samples_per_frame;
			int n;
			g_ns_current_frame = i;
			n = opus_encode_float(enc, frame, frame_size, packet, (opus_int32)sizeof(packet));
			g_ns_current_frame = -1;
			if (n < 0) {
				g_ns_target_frame = -1;
				opus_encoder_destroy(enc);
				return -4;
			}
		}
	}

	g_ns_target_frame = -1;
	if (!g_ns_snapshot.valid) {
		opus_encoder_destroy(enc);
		return -5;
	}
	if (out) {
		*out = g_ns_snapshot;
	}
	opus_encoder_destroy(enc);
	return 0;
}
*/
import "C"

import "unsafe"

// OpusNoiseShapeGainsSnapshot captures libopus noise shaping gains at a specific frame.
type OpusNoiseShapeGainsSnapshot struct {
	Frame              int
	NumSubframes       int
	ShapingLPCOrder    int
	GainsPre           []float32 // Gains before gain_mult/gain_add (back-computed)
	GainsPost          []float32 // Gains after gain_mult + gain_add
	GainMult           float32
	GainAdd            float32
	Warping            float32
	BWExp              float32
	SNRAdjDB           float32
}

// CaptureOpusNoiseShapeGainsAtFrame captures the noise shaping gains from libopus at a specific frame.
func CaptureOpusNoiseShapeGainsAtFrame(samples []float32, sampleRate, channels, bitrate, frameSize, frameIndex int) (OpusNoiseShapeGainsSnapshot, bool) {
	if len(samples) == 0 || frameSize <= 0 || channels <= 0 || frameIndex < 0 {
		return OpusNoiseShapeGainsSnapshot{}, false
	}
	var out C.opus_noise_shape_gains_snapshot
	ret := C.test_capture_opus_noise_shape_frame(
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
		return OpusNoiseShapeGainsSnapshot{}, false
	}

	numSubfr := int(out.nb_subfr)
	if numSubfr > 4 {
		numSubfr = 4
	}

	snap := OpusNoiseShapeGainsSnapshot{
		Frame:           int(out.frame),
		NumSubframes:    numSubfr,
		ShapingLPCOrder: int(out.shaping_lpc_order),
		GainsPre:        make([]float32, numSubfr),
		GainsPost:       make([]float32, numSubfr),
		GainMult:        float32(out.gain_mult),
		GainAdd:         float32(out.gain_add),
		Warping:         float32(out.warping),
		BWExp:           float32(out.bw_exp),
		SNRAdjDB:        float32(out.snr_adj_db),
	}

	gPre := unsafe.Slice((*C.float)(unsafe.Pointer(&out.gains_pre[0])), numSubfr)
	gPost := unsafe.Slice((*C.float)(unsafe.Pointer(&out.gains_post[0])), numSubfr)
	for i := 0; i < numSubfr; i++ {
		snap.GainsPre[i] = float32(gPre[i])
		snap.GainsPost[i] = float32(gPost[i])
	}

	return snap, true
}
