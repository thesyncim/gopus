//go:build cgo_libopus
// +build cgo_libopus

// Package cgo provides CGO wrappers for SILK noise shape analysis comparison.
package cgo

/*
#cgo CFLAGS: -I${SRCDIR}/../../tmp_check/opus-1.6.1/include -I${SRCDIR}/../../tmp_check/opus-1.6.1/celt -I${SRCDIR}/../../tmp_check/opus-1.6.1 -I${SRCDIR}/../../tmp_check/opus-1.6.1/silk -I${SRCDIR}/../../tmp_check/opus-1.6.1/silk/float -DHAVE_CONFIG_H
#cgo LDFLAGS: ${SRCDIR}/../../tmp_check/opus-1.6.1/.libs/libopus.a -lm

#include <string.h>
#include "silk/main.h"
#include "silk/float/main_FLP.h"

typedef struct {
    float gains[MAX_NB_SUBFR];
    float ar[MAX_NB_SUBFR * MAX_SHAPE_LPC_ORDER];
    float lf_ma[MAX_NB_SUBFR];
    float lf_ar[MAX_NB_SUBFR];
    float tilt[MAX_NB_SUBFR];
    float harm_shape[MAX_NB_SUBFR];
    float lambda;
    float input_quality;
    float coding_quality;
    int quant_offset_type;
    float harm_shape_smth_out;
    float tilt_smth_out;
} silk_noise_shape_snapshot;

static int test_silk_noise_shape_analysis_flp(
    const float *x_with_la,
    int x_len,
    const float *pitch_res_frame,
    int pitch_res_len,
    int la_shape,
    int fs_kHz,
    int nb_subfr,
    int subfr_length,
    int shape_win_length,
    int shaping_lpc_order,
    int warping_q16,
    int snr_db_q7,
    int use_cbr,
    int speech_activity_q8,
    int signal_type,
    int quant_offset_type,
    const int *input_quality_bands_q15,
    const int *pitch_l,
    float ltp_corr,
    float pred_gain,
    float harm_shape_smth_in,
    float tilt_smth_in,
    silk_noise_shape_snapshot *out
) {
    silk_encoder_state_FLP enc;
    silk_encoder_control_FLP ctrl;
    int i;
    int frame_length = nb_subfr * subfr_length;

    if (!x_with_la || !pitch_res_frame || !out || !input_quality_bands_q15 || !pitch_l) {
        return -1;
    }
    if (nb_subfr <= 0 || nb_subfr > MAX_NB_SUBFR) {
        return -2;
    }
    if (shaping_lpc_order <= 0 || shaping_lpc_order > MAX_SHAPE_LPC_ORDER) {
        return -3;
    }
    if (x_len < frame_length + 2 * la_shape) {
        return -4;
    }
    if (pitch_res_len < frame_length) {
        return -5;
    }

    memset(&enc, 0, sizeof(enc));
    memset(&ctrl, 0, sizeof(ctrl));
    memset(out, 0, sizeof(*out));

    enc.sCmn.la_shape = la_shape;
    enc.sCmn.fs_kHz = fs_kHz;
    enc.sCmn.nb_subfr = nb_subfr;
    enc.sCmn.subfr_length = subfr_length;
    enc.sCmn.shapeWinLength = shape_win_length;
    enc.sCmn.shapingLPCOrder = shaping_lpc_order;
    enc.sCmn.warping_Q16 = warping_q16;
    enc.sCmn.SNR_dB_Q7 = snr_db_q7;
    enc.sCmn.useCBR = use_cbr;
    enc.sCmn.speech_activity_Q8 = speech_activity_q8;
    enc.sCmn.indices.signalType = (opus_int8)signal_type;
    enc.sCmn.indices.quantOffsetType = (opus_int8)quant_offset_type;
    enc.LTPCorr = ltp_corr;
    enc.sShape.HarmShapeGain_smth = harm_shape_smth_in;
    enc.sShape.Tilt_smth = tilt_smth_in;

    for (i = 0; i < VAD_N_BANDS; i++) {
        enc.sCmn.input_quality_bands_Q15[i] = input_quality_bands_q15[i];
    }
    for (i = 0; i < nb_subfr; i++) {
        ctrl.pitchL[i] = pitch_l[i];
    }
    ctrl.predGain = pred_gain;

    // Pass x_frame pointer; function internally accesses x - la_shape.
    silk_noise_shape_analysis_FLP(&enc, &ctrl, pitch_res_frame, x_with_la + la_shape);

    for (i = 0; i < nb_subfr; i++) {
        out->gains[i] = ctrl.Gains[i];
        out->lf_ma[i] = ctrl.LF_MA_shp[i];
        out->lf_ar[i] = ctrl.LF_AR_shp[i];
        out->tilt[i] = ctrl.Tilt[i];
        out->harm_shape[i] = ctrl.HarmShapeGain[i];
    }
    for (i = 0; i < nb_subfr * MAX_SHAPE_LPC_ORDER; i++) {
        out->ar[i] = ctrl.AR[i];
    }
    out->lambda = ctrl.Lambda;
    out->input_quality = ctrl.input_quality;
    out->coding_quality = ctrl.coding_quality;
    out->quant_offset_type = enc.sCmn.indices.quantOffsetType;
    out->harm_shape_smth_out = enc.sShape.HarmShapeGain_smth;
    out->tilt_smth_out = enc.sShape.Tilt_smth;

    return 0;
}
*/
import "C"

import "unsafe"

const maxShapeLPCOrderWrapper = 24

// SilkNoiseShapeSnapshot captures key outputs from libopus silk_noise_shape_analysis_FLP.
type SilkNoiseShapeSnapshot struct {
	Gains            []float32
	AR               []float32
	LFMAShp          []float32
	LFARShp          []float32
	Tilt             []float32
	HarmShapeGain    []float32
	Lambda           float32
	InputQuality     float32
	CodingQuality    float32
	QuantOffsetType  int
	HarmShapeSmthOut float32
	TiltSmthOut      float32
}

// SilkNoiseShapeAnalysisFLP runs libopus silk_noise_shape_analysis_FLP with caller-provided inputs/state.
func SilkNoiseShapeAnalysisFLP(
	xWithLA []float32,
	pitchResFrame []float32,
	laShape int,
	fsKHz int,
	nbSubfr int,
	subfrLength int,
	shapeWinLength int,
	shapingLPCOrder int,
	warpingQ16 int,
	snrDBQ7 int,
	useCBR bool,
	speechActivityQ8 int,
	signalType int,
	quantOffsetType int,
	inputQualityBandsQ15 [4]int,
	pitchL []int,
	ltpCorr float32,
	predGain float32,
	harmShapeSmthIn float32,
	tiltSmthIn float32,
) (SilkNoiseShapeSnapshot, bool) {
	if len(xWithLA) == 0 || len(pitchResFrame) == 0 || nbSubfr <= 0 {
		return SilkNoiseShapeSnapshot{}, false
	}
	if len(pitchL) < nbSubfr {
		return SilkNoiseShapeSnapshot{}, false
	}

	var out C.silk_noise_shape_snapshot
	cUseCBR := C.int(0)
	if useCBR {
		cUseCBR = 1
	}

	var iq [4]C.int
	for i := 0; i < 4; i++ {
		iq[i] = C.int(inputQualityBandsQ15[i])
	}
	cPitchL := make([]C.int, nbSubfr)
	for i := 0; i < nbSubfr; i++ {
		cPitchL[i] = C.int(pitchL[i])
	}

	ret := C.test_silk_noise_shape_analysis_flp(
		(*C.float)(unsafe.Pointer(&xWithLA[0])),
		C.int(len(xWithLA)),
		(*C.float)(unsafe.Pointer(&pitchResFrame[0])),
		C.int(len(pitchResFrame)),
		C.int(laShape),
		C.int(fsKHz),
		C.int(nbSubfr),
		C.int(subfrLength),
		C.int(shapeWinLength),
		C.int(shapingLPCOrder),
		C.int(warpingQ16),
		C.int(snrDBQ7),
		cUseCBR,
		C.int(speechActivityQ8),
		C.int(signalType),
		C.int(quantOffsetType),
		(*C.int)(unsafe.Pointer(&iq[0])),
		(*C.int)(unsafe.Pointer(&cPitchL[0])),
		C.float(ltpCorr),
		C.float(predGain),
		C.float(harmShapeSmthIn),
		C.float(tiltSmthIn),
		&out,
	)
	if ret != 0 {
		return SilkNoiseShapeSnapshot{}, false
	}

	s := SilkNoiseShapeSnapshot{
		Gains:            make([]float32, nbSubfr),
		AR:               make([]float32, nbSubfr*maxShapeLPCOrderWrapper),
		LFMAShp:          make([]float32, nbSubfr),
		LFARShp:          make([]float32, nbSubfr),
		Tilt:             make([]float32, nbSubfr),
		HarmShapeGain:    make([]float32, nbSubfr),
		Lambda:           float32(out.lambda),
		InputQuality:     float32(out.input_quality),
		CodingQuality:    float32(out.coding_quality),
		QuantOffsetType:  int(out.quant_offset_type),
		HarmShapeSmthOut: float32(out.harm_shape_smth_out),
		TiltSmthOut:      float32(out.tilt_smth_out),
	}
	for i := 0; i < nbSubfr; i++ {
		s.Gains[i] = float32(out.gains[i])
		s.LFMAShp[i] = float32(out.lf_ma[i])
		s.LFARShp[i] = float32(out.lf_ar[i])
		s.Tilt[i] = float32(out.tilt[i])
		s.HarmShapeGain[i] = float32(out.harm_shape[i])
	}
	for i := 0; i < len(s.AR); i++ {
		s.AR[i] = float32(out.ar[i])
	}
	return s, true
}
