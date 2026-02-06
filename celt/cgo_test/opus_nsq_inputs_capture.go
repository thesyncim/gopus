//go:build cgo_libopus
// +build cgo_libopus

package cgo

/*
#cgo CFLAGS: -I${SRCDIR}/../../tmp_check/opus-1.6.1/include -I${SRCDIR}/../../tmp_check/opus-1.6.1/celt -I${SRCDIR}/../../tmp_check/opus-1.6.1 -I${SRCDIR}/../../tmp_check/opus-1.6.1/src -I${SRCDIR}/../../tmp_check/opus-1.6.1/silk -I${SRCDIR}/../../tmp_check/opus-1.6.1/silk/float -DHAVE_CONFIG_H
#cgo LDFLAGS: ${SRCDIR}/../../tmp_check/opus-1.6.1/.libs/libopus.a -lm

#include <string.h>
#include "opus.h"
#include "silk/main.h"
#include "silk/NSQ.h"
#undef VAR_ARRAYS
#define USE_ALLOCA
#include "stack_alloc.h"

typedef struct {
    int valid;
    int encode_frame;
    int calls_in_frame;

    int frame_length;
    int subfr_length;
    int nb_subfr;
    int ltp_mem_length;
    int pred_lpc_order;
    int shape_lpc_order;
    int warping_q16;
    int n_states_delayed_decision;

    int signal_type;
    int quant_offset_type;
    int nlsf_interp_coef_q2;
    int seed_in;
    int seed_out;
    int lambda_q10;
    int ltp_scale_q14;

    opus_int16 x16[MAX_FRAME_LENGTH];
    opus_int16 pred_coef_q12[2 * MAX_LPC_ORDER];
    opus_int16 ltp_coef_q14[LTP_ORDER * MAX_NB_SUBFR];
    opus_int16 ar_q13[MAX_NB_SUBFR * MAX_SHAPE_LPC_ORDER];
    opus_int harm_shape_gain_q14[MAX_NB_SUBFR];
    opus_int tilt_q14[MAX_NB_SUBFR];
    opus_int32 lf_shp_q14[MAX_NB_SUBFR];
    opus_int32 gains_q16[MAX_NB_SUBFR];
    opus_int pitch_l[MAX_NB_SUBFR];
} opus_nsq_input_snapshot;

static opus_nsq_input_snapshot g_nsq_input_snapshot;
static int g_nsq_input_target_frame = -1;
static int g_nsq_input_current_frame = -1;

#define silk_NSQ_del_dec_c real_silk_NSQ_del_dec_c
#include "silk/NSQ_del_dec.c"
#undef silk_NSQ_del_dec_c

void silk_NSQ_del_dec_c(
    const silk_encoder_state    *psEncC,
    silk_nsq_state              *NSQ,
    SideInfoIndices             *psIndices,
    const opus_int16            x16[],
    opus_int8                   pulses[],
    const opus_int16            *PredCoef_Q12,
    const opus_int16            LTPCoef_Q14[ LTP_ORDER * MAX_NB_SUBFR ],
    const opus_int16            AR_Q13[ MAX_NB_SUBFR * MAX_SHAPE_LPC_ORDER ],
    const opus_int              HarmShapeGain_Q14[ MAX_NB_SUBFR ],
    const opus_int              Tilt_Q14[ MAX_NB_SUBFR ],
    const opus_int32            LF_shp_Q14[ MAX_NB_SUBFR ],
    const opus_int32            Gains_Q16[ MAX_NB_SUBFR ],
    const opus_int              pitchL[ MAX_NB_SUBFR ],
    const opus_int              Lambda_Q10,
    const opus_int              LTP_scale_Q14
) {
    int capture = (g_nsq_input_current_frame >= 0 && g_nsq_input_current_frame == g_nsq_input_target_frame);
    if (capture) {
        g_nsq_input_snapshot.valid = 1;
        g_nsq_input_snapshot.encode_frame = g_nsq_input_current_frame;
        g_nsq_input_snapshot.calls_in_frame += 1;

        g_nsq_input_snapshot.frame_length = psEncC->frame_length;
        g_nsq_input_snapshot.subfr_length = psEncC->subfr_length;
        g_nsq_input_snapshot.nb_subfr = psEncC->nb_subfr;
        g_nsq_input_snapshot.ltp_mem_length = psEncC->ltp_mem_length;
        g_nsq_input_snapshot.pred_lpc_order = psEncC->predictLPCOrder;
        g_nsq_input_snapshot.shape_lpc_order = psEncC->shapingLPCOrder;
        g_nsq_input_snapshot.warping_q16 = psEncC->warping_Q16;
        g_nsq_input_snapshot.n_states_delayed_decision = psEncC->nStatesDelayedDecision;

        g_nsq_input_snapshot.signal_type = psIndices->signalType;
        g_nsq_input_snapshot.quant_offset_type = psIndices->quantOffsetType;
        g_nsq_input_snapshot.nlsf_interp_coef_q2 = psIndices->NLSFInterpCoef_Q2;
        g_nsq_input_snapshot.seed_in = psIndices->Seed;
        g_nsq_input_snapshot.lambda_q10 = Lambda_Q10;
        g_nsq_input_snapshot.ltp_scale_q14 = LTP_scale_Q14;

        memset(g_nsq_input_snapshot.x16, 0, sizeof(g_nsq_input_snapshot.x16));
        memset(g_nsq_input_snapshot.pred_coef_q12, 0, sizeof(g_nsq_input_snapshot.pred_coef_q12));
        memset(g_nsq_input_snapshot.ltp_coef_q14, 0, sizeof(g_nsq_input_snapshot.ltp_coef_q14));
        memset(g_nsq_input_snapshot.ar_q13, 0, sizeof(g_nsq_input_snapshot.ar_q13));
        memset(g_nsq_input_snapshot.harm_shape_gain_q14, 0, sizeof(g_nsq_input_snapshot.harm_shape_gain_q14));
        memset(g_nsq_input_snapshot.tilt_q14, 0, sizeof(g_nsq_input_snapshot.tilt_q14));
        memset(g_nsq_input_snapshot.lf_shp_q14, 0, sizeof(g_nsq_input_snapshot.lf_shp_q14));
        memset(g_nsq_input_snapshot.gains_q16, 0, sizeof(g_nsq_input_snapshot.gains_q16));
        memset(g_nsq_input_snapshot.pitch_l, 0, sizeof(g_nsq_input_snapshot.pitch_l));

        if (x16 && psEncC->frame_length > 0) {
            int n = psEncC->frame_length;
            if (n > MAX_FRAME_LENGTH) {
                n = MAX_FRAME_LENGTH;
            }
            memcpy(g_nsq_input_snapshot.x16, x16, n * sizeof(opus_int16));
        }
        if (PredCoef_Q12) {
            memcpy(g_nsq_input_snapshot.pred_coef_q12, PredCoef_Q12, sizeof(g_nsq_input_snapshot.pred_coef_q12));
        }
        if (LTPCoef_Q14) {
            memcpy(g_nsq_input_snapshot.ltp_coef_q14, LTPCoef_Q14, sizeof(g_nsq_input_snapshot.ltp_coef_q14));
        }
        if (AR_Q13) {
            memcpy(g_nsq_input_snapshot.ar_q13, AR_Q13, sizeof(g_nsq_input_snapshot.ar_q13));
        }
        if (HarmShapeGain_Q14) {
            memcpy(g_nsq_input_snapshot.harm_shape_gain_q14, HarmShapeGain_Q14, sizeof(g_nsq_input_snapshot.harm_shape_gain_q14));
        }
        if (Tilt_Q14) {
            memcpy(g_nsq_input_snapshot.tilt_q14, Tilt_Q14, sizeof(g_nsq_input_snapshot.tilt_q14));
        }
        if (LF_shp_Q14) {
            memcpy(g_nsq_input_snapshot.lf_shp_q14, LF_shp_Q14, sizeof(g_nsq_input_snapshot.lf_shp_q14));
        }
        if (Gains_Q16) {
            memcpy(g_nsq_input_snapshot.gains_q16, Gains_Q16, sizeof(g_nsq_input_snapshot.gains_q16));
        }
        if (pitchL) {
            memcpy(g_nsq_input_snapshot.pitch_l, pitchL, sizeof(g_nsq_input_snapshot.pitch_l));
        }
    }

    real_silk_NSQ_del_dec_c(
        psEncC, NSQ, psIndices, x16, pulses, PredCoef_Q12, LTPCoef_Q14, AR_Q13,
        HarmShapeGain_Q14, Tilt_Q14, LF_shp_Q14, Gains_Q16, pitchL, Lambda_Q10, LTP_scale_Q14
    );

    if (capture) {
        g_nsq_input_snapshot.seed_out = psIndices->Seed;
    }
}

static int test_capture_opus_nsq_inputs_frame(
    const float *samples,
    int total_samples,
    int sample_rate,
    int channels,
    int bitrate,
    int frame_size,
    int frame_index,
    opus_nsq_input_snapshot *out
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

        memset(&g_nsq_input_snapshot, 0, sizeof(g_nsq_input_snapshot));
        g_nsq_input_target_frame = frame_index;
        g_nsq_input_current_frame = -1;

        for (i = 0; i <= frame_index; i++) {
            const float *frame = samples + i * samples_per_frame;
            int n;
            g_nsq_input_current_frame = i;
            n = opus_encode_float(enc, frame, frame_size, packet, (opus_int32)sizeof(packet));
            g_nsq_input_current_frame = -1;
            if (n < 0) {
                g_nsq_input_target_frame = -1;
                opus_encoder_destroy(enc);
                return -4;
            }
        }
    }

    g_nsq_input_target_frame = -1;
    if (!g_nsq_input_snapshot.valid) {
        opus_encoder_destroy(enc);
        return -5;
    }
    if (out) {
        *out = g_nsq_input_snapshot;
    }
    opus_encoder_destroy(enc);
    return 0;
}
*/
import "C"

import "unsafe"

const (
	opusNSQInputMaxFrameLength = int(C.MAX_FRAME_LENGTH)
	opusNSQInputMaxSubfr       = int(C.MAX_NB_SUBFR)
	opusNSQInputPredCoefLen    = int(2 * C.MAX_LPC_ORDER)
	opusNSQInputLTPCoefLen     = int(C.LTP_ORDER * C.MAX_NB_SUBFR)
	opusNSQInputARLen          = int(C.MAX_NB_SUBFR * C.MAX_SHAPE_LPC_ORDER)
)

// OpusNSQInputSnapshot captures the exact arrays/scalars passed to libopus NSQ_del_dec_c.
type OpusNSQInputSnapshot struct {
	EncodeFrame            int
	CallsInFrame           int
	FrameLength            int
	SubfrLength            int
	NumSubframes           int
	LTPMemLength           int
	PredLPCOrder           int
	ShapeLPCOrder          int
	WarpingQ16             int
	NStatesDelayedDecision int

	SignalType       int
	QuantOffsetType  int
	NLSFInterpCoefQ2 int
	SeedIn           int
	SeedOut          int
	LambdaQ10        int
	LTPScaleQ14      int

	X16              []int16
	PredCoefQ12      []int16
	LTPCoefQ14       []int16
	ARQ13            []int16
	HarmShapeGainQ14 []int
	TiltQ14          []int
	LFShpQ14         []int32
	GainsQ16         []int32
	PitchL           []int
}

// CaptureOpusNSQInputsAtFrame captures the libopus top-level NSQ inputs used when encoding frameIndex.
func CaptureOpusNSQInputsAtFrame(samples []float32, sampleRate, channels, bitrate, frameSize, frameIndex int) (OpusNSQInputSnapshot, bool) {
	if len(samples) == 0 || frameSize <= 0 || channels <= 0 || frameIndex < 0 {
		return OpusNSQInputSnapshot{}, false
	}
	var out C.opus_nsq_input_snapshot
	ret := C.test_capture_opus_nsq_inputs_frame(
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
		return OpusNSQInputSnapshot{}, false
	}

	frameLen := int(out.frame_length)
	if frameLen < 0 {
		frameLen = 0
	}
	if frameLen > opusNSQInputMaxFrameLength {
		frameLen = opusNSQInputMaxFrameLength
	}
	numSubfr := int(out.nb_subfr)
	if numSubfr < 0 {
		numSubfr = 0
	}
	if numSubfr > opusNSQInputMaxSubfr {
		numSubfr = opusNSQInputMaxSubfr
	}

	x16 := make([]int16, frameLen)
	pred := make([]int16, opusNSQInputPredCoefLen)
	ltp := make([]int16, opusNSQInputLTPCoefLen)
	ar := make([]int16, opusNSQInputARLen)
	harm := make([]int, numSubfr)
	tilt := make([]int, numSubfr)
	lf := make([]int32, numSubfr)
	gains := make([]int32, numSubfr)
	pitch := make([]int, numSubfr)

	x16C := unsafe.Slice((*C.opus_int16)(unsafe.Pointer(&out.x16[0])), frameLen)
	predC := unsafe.Slice((*C.opus_int16)(unsafe.Pointer(&out.pred_coef_q12[0])), opusNSQInputPredCoefLen)
	ltpC := unsafe.Slice((*C.opus_int16)(unsafe.Pointer(&out.ltp_coef_q14[0])), opusNSQInputLTPCoefLen)
	arC := unsafe.Slice((*C.opus_int16)(unsafe.Pointer(&out.ar_q13[0])), opusNSQInputARLen)
	harmC := unsafe.Slice((*C.opus_int)(unsafe.Pointer(&out.harm_shape_gain_q14[0])), numSubfr)
	tiltC := unsafe.Slice((*C.opus_int)(unsafe.Pointer(&out.tilt_q14[0])), numSubfr)
	lfC := unsafe.Slice((*C.opus_int32)(unsafe.Pointer(&out.lf_shp_q14[0])), numSubfr)
	gainsC := unsafe.Slice((*C.opus_int32)(unsafe.Pointer(&out.gains_q16[0])), numSubfr)
	pitchC := unsafe.Slice((*C.opus_int)(unsafe.Pointer(&out.pitch_l[0])), numSubfr)

	for i := 0; i < frameLen; i++ {
		x16[i] = int16(x16C[i])
	}
	for i := 0; i < opusNSQInputPredCoefLen; i++ {
		pred[i] = int16(predC[i])
	}
	for i := 0; i < opusNSQInputLTPCoefLen; i++ {
		ltp[i] = int16(ltpC[i])
	}
	for i := 0; i < opusNSQInputARLen; i++ {
		ar[i] = int16(arC[i])
	}
	for i := 0; i < numSubfr; i++ {
		harm[i] = int(harmC[i])
		tilt[i] = int(tiltC[i])
		lf[i] = int32(lfC[i])
		gains[i] = int32(gainsC[i])
		pitch[i] = int(pitchC[i])
	}

	return OpusNSQInputSnapshot{
		EncodeFrame:            int(out.encode_frame),
		CallsInFrame:           int(out.calls_in_frame),
		FrameLength:            frameLen,
		SubfrLength:            int(out.subfr_length),
		NumSubframes:           numSubfr,
		LTPMemLength:           int(out.ltp_mem_length),
		PredLPCOrder:           int(out.pred_lpc_order),
		ShapeLPCOrder:          int(out.shape_lpc_order),
		WarpingQ16:             int(out.warping_q16),
		NStatesDelayedDecision: int(out.n_states_delayed_decision),
		SignalType:             int(out.signal_type),
		QuantOffsetType:        int(out.quant_offset_type),
		NLSFInterpCoefQ2:       int(out.nlsf_interp_coef_q2),
		SeedIn:                 int(out.seed_in),
		SeedOut:                int(out.seed_out),
		LambdaQ10:              int(out.lambda_q10),
		LTPScaleQ14:            int(out.ltp_scale_q14),
		X16:                    x16,
		PredCoefQ12:            pred,
		LTPCoefQ14:             ltp,
		ARQ13:                  ar,
		HarmShapeGainQ14:       harm,
		TiltQ14:                tilt,
		LFShpQ14:               lf,
		GainsQ16:               gains,
		PitchL:                 pitch,
	}, true
}
