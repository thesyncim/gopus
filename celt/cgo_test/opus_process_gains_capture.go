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
	int cond_coding;

	int signal_type;
	int quant_offset_before;
	int quant_offset_after;

	int nb_subfr;
	int subfr_length;
	int n_states_delayed_decision;
	int input_tilt_q15;
	int snr_db_q7;
	int speech_activity_q8;
	float ltp_pred_cod_gain;
	float lambda;

	int last_gain_index_prev;
	int last_gain_index_out;
	int gains_indices[4];
	int gains_unq_q16[4];
	float gains_before[4];
	float gains_after[4];
	float res_nrg_before[4];
} opus_process_gains_frame_snapshot;

static opus_process_gains_frame_snapshot g_pg_snapshot;
static int g_pg_target_frame = -1;
static int g_pg_current_frame = -1;

#define silk_process_gains_FLP real_silk_process_gains_FLP
#include "silk/float/process_gains_FLP.c"
#undef silk_process_gains_FLP

void silk_process_gains_FLP(
	silk_encoder_state_FLP *psEnc,
	silk_encoder_control_FLP *psEncCtrl,
	opus_int condCoding
) {
	int capture = (g_pg_current_frame >= 0 && g_pg_current_frame == g_pg_target_frame);
	int k;
	int nb_subfr = psEnc->sCmn.nb_subfr;
	if (nb_subfr > 4) {
		nb_subfr = 4;
	}

	if (capture) {
		g_pg_snapshot.valid = 1;
		g_pg_snapshot.encode_frame = g_pg_current_frame;
		g_pg_snapshot.calls_in_frame += 1;
		g_pg_snapshot.cond_coding = condCoding;
		g_pg_snapshot.signal_type = psEnc->sCmn.indices.signalType;
		g_pg_snapshot.quant_offset_before = psEnc->sCmn.indices.quantOffsetType;
		g_pg_snapshot.nb_subfr = nb_subfr;
		g_pg_snapshot.subfr_length = psEnc->sCmn.subfr_length;
		g_pg_snapshot.n_states_delayed_decision = psEnc->sCmn.nStatesDelayedDecision;
		g_pg_snapshot.input_tilt_q15 = psEnc->sCmn.input_tilt_Q15;
		g_pg_snapshot.snr_db_q7 = psEnc->sCmn.SNR_dB_Q7;
		g_pg_snapshot.speech_activity_q8 = psEnc->sCmn.speech_activity_Q8;
		g_pg_snapshot.ltp_pred_cod_gain = psEncCtrl->LTPredCodGain;
		g_pg_snapshot.last_gain_index_prev = psEnc->sShape.LastGainIndex;

		for (k = 0; k < 4; k++) {
			g_pg_snapshot.gains_before[k] = 0.0f;
			g_pg_snapshot.gains_after[k] = 0.0f;
			g_pg_snapshot.res_nrg_before[k] = 0.0f;
			g_pg_snapshot.gains_indices[k] = 0;
			g_pg_snapshot.gains_unq_q16[k] = 0;
		}
		for (k = 0; k < nb_subfr; k++) {
			g_pg_snapshot.gains_before[k] = psEncCtrl->Gains[k];
			g_pg_snapshot.res_nrg_before[k] = psEncCtrl->ResNrg[k];
		}
	}

	real_silk_process_gains_FLP(psEnc, psEncCtrl, condCoding);

	if (capture) {
		g_pg_snapshot.quant_offset_after = psEnc->sCmn.indices.quantOffsetType;
		g_pg_snapshot.lambda = psEncCtrl->Lambda;
		g_pg_snapshot.last_gain_index_out = psEnc->sShape.LastGainIndex;
		for (k = 0; k < nb_subfr; k++) {
			g_pg_snapshot.gains_after[k] = psEncCtrl->Gains[k];
			g_pg_snapshot.gains_indices[k] = psEnc->sCmn.indices.GainsIndices[k];
			g_pg_snapshot.gains_unq_q16[k] = psEncCtrl->GainsUnq_Q16[k];
		}
	}
}

static int test_capture_opus_process_gains_frame(
	const float *samples,
	int total_samples,
	int sample_rate,
	int channels,
	int bitrate,
	int frame_size,
	int frame_index,
	opus_process_gains_frame_snapshot *out
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

		memset(&g_pg_snapshot, 0, sizeof(g_pg_snapshot));
		g_pg_target_frame = frame_index;
		g_pg_current_frame = -1;

		for (i = 0; i <= frame_index; i++) {
			const float *frame = samples + i * samples_per_frame;
			int n;
			g_pg_current_frame = i;
			n = opus_encode_float(enc, frame, frame_size, packet, (opus_int32)sizeof(packet));
			g_pg_current_frame = -1;
			if (n < 0) {
				g_pg_target_frame = -1;
				opus_encoder_destroy(enc);
				return -4;
			}
		}
	}

	g_pg_target_frame = -1;
	if (!g_pg_snapshot.valid) {
		opus_encoder_destroy(enc);
		return -5;
	}
	if (out) {
		*out = g_pg_snapshot;
	}
	opus_encoder_destroy(enc);
	return 0;
}
*/
import "C"

import "unsafe"

// OpusProcessGainsFrameSnapshot captures libopus process_gains inputs/outputs from a full Opus encode.
type OpusProcessGainsFrameSnapshot struct {
	EncodeFrame int
	CallsInFrame int
	CondCoding int

	SignalType int
	QuantOffsetBefore int
	QuantOffsetAfter int

	NumSubframes int
	SubframeLength int
	NStatesDelayedDecision int
	InputTiltQ15 int
	SNRDBQ7 int
	SpeechActivityQ8 int
	LTPPredCodGain float32
	Lambda float32

	LastGainIndexPrev int
	LastGainIndexOut int
	GainsIndices [4]int8
	GainsUnqQ16 [4]int32
	GainsBefore [4]float32
	GainsAfter [4]float32
	ResNrgBefore [4]float32
}

// CaptureOpusProcessGainsAtFrame captures process_gains state from libopus full encode at `frameIndex`.
func CaptureOpusProcessGainsAtFrame(samples []float32, sampleRate, channels, bitrate, frameSize, frameIndex int) (OpusProcessGainsFrameSnapshot, bool) {
	if len(samples) == 0 || frameSize <= 0 || channels <= 0 || frameIndex < 0 {
		return OpusProcessGainsFrameSnapshot{}, false
	}
	var out C.opus_process_gains_frame_snapshot
	ret := C.test_capture_opus_process_gains_frame(
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
		return OpusProcessGainsFrameSnapshot{}, false
	}

	snap := OpusProcessGainsFrameSnapshot{
		EncodeFrame: int(out.encode_frame),
		CallsInFrame: int(out.calls_in_frame),
		CondCoding: int(out.cond_coding),
		SignalType: int(out.signal_type),
		QuantOffsetBefore: int(out.quant_offset_before),
		QuantOffsetAfter: int(out.quant_offset_after),
		NumSubframes: int(out.nb_subfr),
		SubframeLength: int(out.subfr_length),
		NStatesDelayedDecision: int(out.n_states_delayed_decision),
		InputTiltQ15: int(out.input_tilt_q15),
		SNRDBQ7: int(out.snr_db_q7),
		SpeechActivityQ8: int(out.speech_activity_q8),
		LTPPredCodGain: float32(out.ltp_pred_cod_gain),
		Lambda: float32(out.lambda),
		LastGainIndexPrev: int(out.last_gain_index_prev),
		LastGainIndexOut: int(out.last_gain_index_out),
	}
	for i := 0; i < 4; i++ {
		snap.GainsIndices[i] = int8(out.gains_indices[i])
		snap.GainsUnqQ16[i] = int32(out.gains_unq_q16[i])
		snap.GainsBefore[i] = float32(out.gains_before[i])
		snap.GainsAfter[i] = float32(out.gains_after[i])
		snap.ResNrgBefore[i] = float32(out.res_nrg_before[i])
	}
	return snap, true
}

