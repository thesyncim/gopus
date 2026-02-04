//go:build cgo_libopus
// +build cgo_libopus

package silk

/*
#cgo CFLAGS: -I${SRCDIR}/../tmp_check/opus-1.6.1/include -I${SRCDIR}/../tmp_check/opus-1.6.1/celt -I${SRCDIR}/../tmp_check/opus-1.6.1 -I${SRCDIR}/../tmp_check/opus-1.6.1/silk -I${SRCDIR}/../tmp_check/opus-1.6.1/silk/float -DHAVE_CONFIG_H
#cgo LDFLAGS: ${SRCDIR}/../tmp_check/opus-1.6.1/.libs/libopus.a -lm

#include <stdlib.h>
#include <string.h>
#include "silk/float/main_FLP.h"
#include "silk/main.h"

typedef struct {
    opus_int8  perIndex;
    opus_int8  ltpIndex[MAX_NB_SUBFR];
    opus_int16 B_Q14[MAX_NB_SUBFR * LTP_ORDER];
    opus_int32 predGain_Q7;
    opus_int32 sumLogGain_Q7;
} ltp_quant_result;

typedef struct {
    opus_int   interp_Q2;
    opus_int16 nlsf_Q15[ MAX_LPC_ORDER ];
} lpc_interp_result;

typedef struct {
    opus_int   interp_Q2;
    opus_int16 nlsf_Q15[ MAX_LPC_ORDER ];
    float      res_nrg;
    float      res_nrg_last;
    float      res_nrg_interp[ 4 ];
} lpc_interp_debug_result;

typedef struct {
    opus_int8  indices[ MAX_LPC_ORDER + 1 ];
    opus_int16 nlsf_Q15[ MAX_LPC_ORDER ];
} nlsf_process_result;

typedef struct {
    opus_int   pitch[MAX_NB_SUBFR];
    opus_int16 lagIndex;
    opus_int8  contourIndex;
    float      ltpCorr;
    int        voiced;
} pitch_analysis_result;

typedef struct {
    int   buf_len;
    float thrhld;
    float predGain;
    int   lagIndex;
    int   contourIndex;
    float ltpCorr;
    int   voiced;
} pitch_lags_trace;

static void opus_silk_find_ltp_flt(const float *res, const int *lag, int subfr_len, int nb_subfr, float *XX, float *xX) {
    silk_find_LTP_FLP(XX, xX, res, lag, subfr_len, nb_subfr, 0);
}

static void opus_silk_quant_ltp_flt(const float *XX, const float *xX, int subfr_len, int nb_subfr, opus_int32 sum_log_gain_Q7, ltp_quant_result *out) {
    opus_int32 XX_Q17[MAX_NB_SUBFR * LTP_ORDER * LTP_ORDER];
    opus_int32 xX_Q17[MAX_NB_SUBFR * LTP_ORDER];
    opus_int   i;
    for (i = 0; i < nb_subfr * LTP_ORDER * LTP_ORDER; i++) {
        XX_Q17[i] = (opus_int32)silk_float2int(XX[i] * 131072.0f);
    }
    for (i = 0; i < nb_subfr * LTP_ORDER; i++) {
        xX_Q17[i] = (opus_int32)silk_float2int(xX[i] * 131072.0f);
    }
    opus_int8 cbk_index[MAX_NB_SUBFR];
    opus_int8 per_idx = 0;
    opus_int32 pred_gain_Q7 = 0;
    opus_int32 sum_log_gain = sum_log_gain_Q7;
    silk_quant_LTP_gains(out->B_Q14, cbk_index, &per_idx, &sum_log_gain, &pred_gain_Q7,
                         XX_Q17, xX_Q17, subfr_len, nb_subfr, 0);
    out->perIndex = per_idx;
    out->predGain_Q7 = pred_gain_Q7;
    out->sumLogGain_Q7 = sum_log_gain;
    memcpy(out->ltpIndex, cbk_index, nb_subfr * sizeof(opus_int8));
}

static void opus_silk_pitch_analysis(const float *frame, int fs_kHz, int nb_subfr, int complexity,
    float search_thres1, float search_thres2, int prevLag, float ltpCorrIn, pitch_analysis_result *out) {
    opus_int pitch_out[MAX_NB_SUBFR];
    opus_int16 lagIndex = 0;
    opus_int8 contourIndex = 0;
    float ltpCorr = ltpCorrIn;
    int ret = silk_pitch_analysis_core_FLP(frame, pitch_out, &lagIndex, &contourIndex, &ltpCorr,
        prevLag, search_thres1, search_thres2, fs_kHz, complexity, nb_subfr, 0);
    out->voiced = (ret == 0);
    out->lagIndex = lagIndex;
    out->contourIndex = contourIndex;
    out->ltpCorr = ltpCorr;
    memcpy(out->pitch, pitch_out, nb_subfr * sizeof(opus_int));
}

static void opus_silk_find_pitch_lags_trace(const float *x, int frame_length, int fs_kHz,
    int ltp_mem_length, int la_pitch, int pitch_LPC_win_length, int pitch_LPC_order,
    int nb_subfr, int subfr_length, int pitchEstimationThreshold_Q16, int pitchEstimationComplexity,
    int prevSignalType, int speech_activity_Q8, int input_tilt_Q15, int prevLag, int signalType, int first_frame_after_reset,
    float *res_out, int res_len, int *pitch_out, pitch_lags_trace *out) {
    silk_encoder_state_FLP st;
    silk_encoder_control_FLP ctrl;
    int buf_len;
    float thrhld;

    silk_memset(&st, 0, sizeof(st));
    silk_memset(&ctrl, 0, sizeof(ctrl));

    st.sCmn.frame_length = frame_length;
    st.sCmn.fs_kHz = fs_kHz;
    st.sCmn.ltp_mem_length = ltp_mem_length;
    st.sCmn.la_pitch = la_pitch;
    st.sCmn.pitch_LPC_win_length = pitch_LPC_win_length;
    st.sCmn.pitchEstimationLPCOrder = pitch_LPC_order;
    st.sCmn.pitchEstimationThreshold_Q16 = pitchEstimationThreshold_Q16;
    st.sCmn.pitchEstimationComplexity = pitchEstimationComplexity;
    st.sCmn.nb_subfr = nb_subfr;
    st.sCmn.subfr_length = subfr_length;
    st.sCmn.prevSignalType = prevSignalType;
    st.sCmn.prevLag = prevLag;
    st.sCmn.speech_activity_Q8 = speech_activity_Q8;
    st.sCmn.input_tilt_Q15 = input_tilt_Q15;
    st.sCmn.indices.signalType = signalType;
    st.sCmn.first_frame_after_reset = first_frame_after_reset;
    st.sCmn.arch = 0;

    buf_len = la_pitch + frame_length + ltp_mem_length;
    thrhld  = 0.6f;
    thrhld -= 0.004f * pitch_LPC_order;
    thrhld -= 0.1f * speech_activity_Q8 * (1.0f/256.0f);
    thrhld -= 0.15f * (prevSignalType >> 1);
    thrhld -= 0.1f * input_tilt_Q15 * (1.0f/32768.0f);

    if (res_out && res_len >= buf_len) {
        silk_find_pitch_lags_FLP(&st, &ctrl, res_out, x, 0);
    } else {
        float *tmp = (float*)malloc(sizeof(float) * buf_len);
        if (tmp) {
            silk_find_pitch_lags_FLP(&st, &ctrl, tmp, x, 0);
            if (res_out && res_len > 0) {
                int copy_len = res_len < buf_len ? res_len : buf_len;
                memcpy(res_out, tmp, copy_len * sizeof(float));
            }
            free(tmp);
        }
    }

    if (pitch_out) {
        memcpy(pitch_out, ctrl.pitchL, nb_subfr * sizeof(int));
    }

    if (out) {
        out->buf_len = buf_len;
        out->thrhld = thrhld;
        out->predGain = ctrl.predGain;
        out->lagIndex = st.sCmn.indices.lagIndex;
        out->contourIndex = st.sCmn.indices.contourIndex;
        out->ltpCorr = st.LTPCorr;
        out->voiced = (st.sCmn.indices.signalType == TYPE_VOICED);
    }
}

static void opus_silk_ltp_analysis_filter(const float *x, const float *B, const int *pitchL, const float *invGains,
    int subfr_len, int nb_subfr, int pre_len, float *out) {
    silk_LTP_analysis_filter_FLP(out, x, B, pitchL, invGains, subfr_len, nb_subfr, pre_len);
}

static void opus_silk_find_lpc_interp(const float *x, int nb_subfr, int subfr_length, int lpc_order,
    int use_interp, int first_frame_after_reset, const opus_int16 *prev_nlsf, float minInvGain, lpc_interp_result *out) {
    silk_encoder_state st;
    silk_memset(&st, 0, sizeof(st));
    st.nb_subfr = nb_subfr;
    st.subfr_length = subfr_length;
    st.predictLPCOrder = lpc_order;
    st.useInterpolatedNLSFs = use_interp;
    st.first_frame_after_reset = first_frame_after_reset;
    st.arch = 0;
    if (prev_nlsf && lpc_order > 0) {
        silk_memcpy(st.prev_NLSFq_Q15, prev_nlsf, sizeof(opus_int16) * lpc_order);
    }
    silk_find_LPC_FLP(&st, out->nlsf_Q15, x, minInvGain, 0);
    out->interp_Q2 = st.indices.NLSFInterpCoef_Q2;
}

static void opus_silk_find_lpc_interp_debug(const float *x, int nb_subfr, int subfr_length, int lpc_order,
    int use_interp, int first_frame_after_reset, const opus_int16 *prev_nlsf, float minInvGain, lpc_interp_debug_result *out) {
    opus_int k;
    silk_float a[ MAX_LPC_ORDER ];
    silk_float a_tmp[ MAX_LPC_ORDER ];
    opus_int16 NLSF_Q15[ MAX_LPC_ORDER ];
    opus_int16 NLSF0_Q15[ MAX_LPC_ORDER ];
    silk_float LPC_res[ MAX_FRAME_LENGTH + MAX_NB_SUBFR * MAX_LPC_ORDER ];
    silk_float res_nrg, res_nrg_2nd, res_nrg_interp, res_nrg_last;

    silk_memset(out, 0, sizeof(*out));
    for (k = 0; k < 4; k++) {
        out->res_nrg_interp[k] = -1.0f;
    }

    subfr_length += lpc_order;
    res_nrg = silk_burg_modified_FLP(a, x, minInvGain, subfr_length, nb_subfr, lpc_order, 0);
    res_nrg_last = 0.0f;
    out->interp_Q2 = 4;

    if (use_interp && !first_frame_after_reset && nb_subfr == MAX_NB_SUBFR) {
        res_nrg_last = silk_burg_modified_FLP(a_tmp, x + ( MAX_NB_SUBFR / 2 ) * subfr_length, minInvGain,
            subfr_length, MAX_NB_SUBFR / 2, lpc_order, 0);
        res_nrg -= res_nrg_last;

        silk_A2NLSF_FLP(NLSF_Q15, a_tmp, lpc_order);

        res_nrg_2nd = silk_float_MAX;
        for (k = 3; k >= 0; k--) {
            silk_interpolate(NLSF0_Q15, prev_nlsf, NLSF_Q15, k, lpc_order);
            silk_NLSF2A_FLP(a_tmp, NLSF0_Q15, lpc_order, 0);
            silk_LPC_analysis_filter_FLP(LPC_res, a_tmp, x, 2 * subfr_length, lpc_order);
            res_nrg_interp = (silk_float)(
                silk_energy_FLP( LPC_res + lpc_order,                subfr_length - lpc_order ) +
                silk_energy_FLP( LPC_res + lpc_order + subfr_length, subfr_length - lpc_order ) );
            out->res_nrg_interp[k] = res_nrg_interp;
            if (res_nrg_interp < res_nrg) {
                res_nrg = res_nrg_interp;
                out->interp_Q2 = k;
            } else if (res_nrg_interp > res_nrg_2nd) {
                break;
            }
            res_nrg_2nd = res_nrg_interp;
        }
    }

    if (out->interp_Q2 == 4) {
        silk_A2NLSF_FLP(NLSF_Q15, a, lpc_order);
    }

    out->res_nrg = res_nrg;
    out->res_nrg_last = res_nrg_last;
    silk_memcpy(out->nlsf_Q15, NLSF_Q15, sizeof(opus_int16) * lpc_order);
}

static void opus_silk_process_nlsfs(const opus_int16 *nlsf_Q15, const opus_int16 *prev_nlsf_Q15,
    int lpc_order, int nb_subfr, int signal_type, int speech_activity_Q8, int use_interp, int interp_Q2,
    int nlsf_survivors, int is_wideband, nlsf_process_result *out) {
    silk_encoder_state st;
    opus_int16 pred_coef_Q12[ 2 ][ MAX_LPC_ORDER ];
    opus_int16 nlsf_copy[ MAX_LPC_ORDER ];

    silk_memset(&st, 0, sizeof(st));
    st.predictLPCOrder = lpc_order;
    st.nb_subfr = nb_subfr;
    st.indices.signalType = signal_type;
    st.speech_activity_Q8 = speech_activity_Q8;
    st.useInterpolatedNLSFs = use_interp;
    st.indices.NLSFInterpCoef_Q2 = interp_Q2;
    st.NLSF_MSVQ_Survivors = nlsf_survivors;
    st.psNLSF_CB = is_wideband ? &silk_NLSF_CB_WB : &silk_NLSF_CB_NB_MB;
    st.arch = 0;
    if (prev_nlsf_Q15 && lpc_order > 0) {
        silk_memcpy(st.prev_NLSFq_Q15, prev_nlsf_Q15, sizeof(opus_int16) * lpc_order);
    }
    if (nlsf_Q15 && lpc_order > 0) {
        silk_memcpy(nlsf_copy, nlsf_Q15, sizeof(opus_int16) * lpc_order);
    }

    silk_process_NLSFs(&st, pred_coef_Q12, nlsf_copy, st.prev_NLSFq_Q15);

    if (out) {
        silk_memcpy(out->indices, st.indices.NLSFIndices, sizeof(opus_int8) * (lpc_order + 1));
        silk_memcpy(out->nlsf_Q15, nlsf_copy, sizeof(opus_int16) * lpc_order);
    }
}

static void opus_silk_a2nlsf(const opus_int32 *a_Q16, int lpc_order, opus_int16 *nlsf_Q15) {
    opus_int32 a_tmp[MAX_LPC_ORDER];
    silk_memcpy(a_tmp, a_Q16, sizeof(opus_int32) * lpc_order);
    silk_A2NLSF(nlsf_Q15, a_tmp, lpc_order);
}

static float opus_silk_burg_modified(const float *x, float minInvGain, int subfr_len, int nb_subfr, int order, float *A) {
    return silk_burg_modified_FLP(A, x, minInvGain, subfr_len, nb_subfr, order, 0);
}

static void opus_silk_nlsf2a(const opus_int16 *nlsf, int order, opus_int16 *a_q12) {
    silk_NLSF2A(a_q12, nlsf, order, 0);
}

static void opus_silk_lpc_analysis_filter(const float *x, const float *pred, int length, int order, float *out) {
    silk_LPC_analysis_filter_FLP(out, pred, x, length, order);
}

static int opus_silk_resample_once(const float *in, int in_len, int fs_in, int fs_out, int for_enc, opus_int16 *out) {
    silk_resampler_state_struct st;
    opus_int ret = silk_resampler_init(&st, fs_in, fs_out, for_enc);
    if (ret != 0) {
        return ret;
    }
    opus_int16 *buf = (opus_int16*)malloc(sizeof(opus_int16) * in_len);
    if (!buf) {
        return -1;
    }
    for (int i = 0; i < in_len; i++) {
        buf[i] = FLOAT2INT16(in[i]);
    }
    ret = silk_resampler(&st, out, buf, in_len);
    free(buf);
    return ret;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

type libopusLTPQuantResult struct {
	PerIndex     int8
	LTPIndex     [maxNbSubfr]int8
	BQ14         [maxNbSubfr * ltpOrderConst]int16
	PredGainQ7   int32
	SumLogGainQ7 int32
}

type libopusPitchAnalysisResult struct {
	Pitch        [maxNbSubfr]int
	LagIndex     int16
	ContourIndex int8
	LTPCorr      float32
	Voiced       bool
}

type LibopusPitchLagsTrace struct {
	Residual []float32
	Pitch    []int
	BufLen   int
	Thrhld   float64
	PredGain float64
	LagIndex int
	Contour  int
	LTPCorr  float32
	Voiced   bool
}

type libopusLPCInterpResult struct {
	NLSF     []int16
	InterpQ2 int
}

type libopusLPCInterpDebugResult struct {
	NLSF         []int16
	InterpQ2     int
	ResNrg       float32
	ResNrgLast   float32
	ResNrgInterp [4]float32
}

type libopusNLSFProcessResult struct {
	Indices []int8
	NLSFQ15 []int16
}

func libopusFindLTP(residual []float32, resStart int, pitchLags []int, subfrLen, nbSubfr int) ([]float32, []float32) {
	if len(residual) == 0 || nbSubfr <= 0 {
		return nil, nil
	}
	if resStart < 0 {
		resStart = 0
	}
	if resStart >= len(residual) {
		return nil, nil
	}
	xx := make([]float32, nbSubfr*ltpOrderConst*ltpOrderConst)
	xX := make([]float32, nbSubfr*ltpOrderConst)
	cLags := make([]C.int, nbSubfr)
	for i := 0; i < nbSubfr; i++ {
		if i < len(pitchLags) {
			cLags[i] = C.int(pitchLags[i])
		}
	}
	C.opus_silk_find_ltp_flt(
		(*C.float)(unsafe.Pointer(&residual[resStart])),
		(*C.int)(unsafe.Pointer(&cLags[0])),
		C.int(subfrLen),
		C.int(nbSubfr),
		(*C.float)(unsafe.Pointer(&xx[0])),
		(*C.float)(unsafe.Pointer(&xX[0])),
	)
	return xx, xX
}

func libopusQuantLTP(XX, xX []float32, subfrLen, nbSubfr int, sumLogGainQ7 int32) libopusLTPQuantResult {
	var out C.ltp_quant_result
	if len(XX) == 0 || len(xX) == 0 || nbSubfr <= 0 {
		return libopusLTPQuantResult{}
	}
	C.opus_silk_quant_ltp_flt(
		(*C.float)(unsafe.Pointer(&XX[0])),
		(*C.float)(unsafe.Pointer(&xX[0])),
		C.int(subfrLen),
		C.int(nbSubfr),
		C.opus_int32(sumLogGainQ7),
		&out,
	)
	var res libopusLTPQuantResult
	res.PerIndex = int8(out.perIndex)
	res.PredGainQ7 = int32(out.predGain_Q7)
	res.SumLogGainQ7 = int32(out.sumLogGain_Q7)
	for i := 0; i < nbSubfr && i < maxNbSubfr; i++ {
		res.LTPIndex[i] = int8(out.ltpIndex[i])
	}
	maxBQ := nbSubfr * ltpOrderConst
	if maxBQ > len(res.BQ14) {
		maxBQ = len(res.BQ14)
	}
	for i := 0; i < maxBQ; i++ {
		res.BQ14[i] = int16(out.B_Q14[i])
	}
	return res
}

func libopusPitchAnalysis(frame []float32, fsKHz, nbSubfr, complexity int, searchThres1, searchThres2 float64, prevLag int, ltpCorr float32) libopusPitchAnalysisResult {
	var out C.pitch_analysis_result
	if len(frame) == 0 || nbSubfr <= 0 {
		return libopusPitchAnalysisResult{}
	}
	C.opus_silk_pitch_analysis(
		(*C.float)(unsafe.Pointer(&frame[0])),
		C.int(fsKHz),
		C.int(nbSubfr),
		C.int(complexity),
		C.float(searchThres1),
		C.float(searchThres2),
		C.int(prevLag),
		C.float(ltpCorr),
		&out,
	)
	var res libopusPitchAnalysisResult
	for i := 0; i < nbSubfr && i < maxNbSubfr; i++ {
		res.Pitch[i] = int(out.pitch[i])
	}
	res.LagIndex = int16(out.lagIndex)
	res.ContourIndex = int8(out.contourIndex)
	res.LTPCorr = float32(out.ltpCorr)
	res.Voiced = out.voiced != 0
	return res
}

func LibopusFindPitchLagsTrace(xFrame []float32, frameLen, fsKHz, ltpMemLen, laPitch, pitchWinLen, pitchLPCOrder, nbSubfr, subfrLen int,
	pitchEstimationThresholdQ16 int32, pitchEstimationComplexity int, prevSignalType, speechActivityQ8, inputTiltQ15, prevLag, signalType int, firstFrame bool) LibopusPitchLagsTrace {
	if len(xFrame) == 0 || frameLen <= 0 || nbSubfr <= 0 {
		return LibopusPitchLagsTrace{}
	}
	bufLen := ltpMemLen + frameLen + laPitch
	residual := make([]float32, bufLen)
	pitch := make([]C.int, nbSubfr)
	firstFlag := 0
	if firstFrame {
		firstFlag = 1
	}
	var out C.pitch_lags_trace
	C.opus_silk_find_pitch_lags_trace(
		(*C.float)(unsafe.Pointer(&xFrame[0])),
		C.int(frameLen),
		C.int(fsKHz),
		C.int(ltpMemLen),
		C.int(laPitch),
		C.int(pitchWinLen),
		C.int(pitchLPCOrder),
		C.int(nbSubfr),
		C.int(subfrLen),
		C.int(pitchEstimationThresholdQ16),
		C.int(pitchEstimationComplexity),
		C.int(prevSignalType),
		C.int(speechActivityQ8),
		C.int(inputTiltQ15),
		C.int(prevLag),
		C.int(signalType),
		C.int(firstFlag),
		(*C.float)(unsafe.Pointer(&residual[0])),
		C.int(len(residual)),
		(*C.int)(unsafe.Pointer(&pitch[0])),
		&out,
	)
	res := LibopusPitchLagsTrace{
		Residual: residual,
		Pitch:    make([]int, nbSubfr),
		BufLen:   int(out.buf_len),
		Thrhld:   float64(out.thrhld),
		PredGain: float64(out.predGain),
		LagIndex: int(out.lagIndex),
		Contour:  int(out.contourIndex),
		LTPCorr:  float32(out.ltpCorr),
		Voiced:   out.voiced != 0,
	}
	for i := 0; i < nbSubfr; i++ {
		res.Pitch[i] = int(pitch[i])
	}
	if res.BufLen > 0 && res.BufLen < len(residual) {
		res.Residual = residual[:res.BufLen]
	}
	return res
}

func libopusFindLPCInterp(x []float32, nbSubfr, subfrLen, lpcOrder int, useInterp, firstFrame bool, prevNLSF []int16, minInvGain float32) libopusLPCInterpResult {
	var out C.lpc_interp_result
	if len(x) == 0 || nbSubfr <= 0 || lpcOrder <= 0 {
		return libopusLPCInterpResult{}
	}
	prev := make([]C.opus_int16, lpcOrder)
	for i := 0; i < lpcOrder && i < len(prevNLSF); i++ {
		prev[i] = C.opus_int16(prevNLSF[i])
	}
	useInterpFlag := 0
	if useInterp {
		useInterpFlag = 1
	}
	firstFlag := 0
	if firstFrame {
		firstFlag = 1
	}
	C.opus_silk_find_lpc_interp(
		(*C.float)(unsafe.Pointer(&x[0])),
		C.int(nbSubfr),
		C.int(subfrLen),
		C.int(lpcOrder),
		C.int(useInterpFlag),
		C.int(firstFlag),
		(*C.opus_int16)(unsafe.Pointer(&prev[0])),
		C.float(minInvGain),
		&out,
	)
	res := libopusLPCInterpResult{
		NLSF:     make([]int16, lpcOrder),
		InterpQ2: int(out.interp_Q2),
	}
	for i := 0; i < lpcOrder && i < len(res.NLSF); i++ {
		res.NLSF[i] = int16(out.nlsf_Q15[i])
	}
	return res
}

func libopusFindLPCInterpDebug(x []float32, nbSubfr, subfrLen, lpcOrder int, useInterp, firstFrame bool, prevNLSF []int16, minInvGain float32) libopusLPCInterpDebugResult {
	var out C.lpc_interp_debug_result
	if len(x) == 0 || nbSubfr <= 0 || lpcOrder <= 0 {
		return libopusLPCInterpDebugResult{}
	}
	prev := make([]C.opus_int16, lpcOrder)
	for i := 0; i < lpcOrder && i < len(prevNLSF); i++ {
		prev[i] = C.opus_int16(prevNLSF[i])
	}
	useInterpFlag := 0
	if useInterp {
		useInterpFlag = 1
	}
	firstFlag := 0
	if firstFrame {
		firstFlag = 1
	}
	C.opus_silk_find_lpc_interp_debug(
		(*C.float)(unsafe.Pointer(&x[0])),
		C.int(nbSubfr),
		C.int(subfrLen),
		C.int(lpcOrder),
		C.int(useInterpFlag),
		C.int(firstFlag),
		(*C.opus_int16)(unsafe.Pointer(&prev[0])),
		C.float(minInvGain),
		&out,
	)
	res := libopusLPCInterpDebugResult{
		NLSF:         make([]int16, lpcOrder),
		InterpQ2:     int(out.interp_Q2),
		ResNrg:       float32(out.res_nrg),
		ResNrgLast:   float32(out.res_nrg_last),
		ResNrgInterp: [4]float32{float32(out.res_nrg_interp[0]), float32(out.res_nrg_interp[1]), float32(out.res_nrg_interp[2]), float32(out.res_nrg_interp[3])},
	}
	for i := 0; i < lpcOrder && i < len(res.NLSF); i++ {
		res.NLSF[i] = int16(out.nlsf_Q15[i])
	}
	return res
}

func libopusProcessNLSF(nlsfQ15, prevNLSF []int16, lpcOrder, nbSubfr, signalType, speechActivityQ8 int, useInterp bool, interpQ2 int, nlsfSurvivors int, isWideband bool) libopusNLSFProcessResult {
	var out C.nlsf_process_result
	if lpcOrder <= 0 || len(nlsfQ15) < lpcOrder {
		return libopusNLSFProcessResult{}
	}
	nlsf := make([]C.opus_int16, lpcOrder)
	for i := 0; i < lpcOrder; i++ {
		nlsf[i] = C.opus_int16(nlsfQ15[i])
	}
	prev := make([]C.opus_int16, lpcOrder)
	for i := 0; i < lpcOrder && i < len(prevNLSF); i++ {
		prev[i] = C.opus_int16(prevNLSF[i])
	}
	useInterpFlag := 0
	if useInterp {
		useInterpFlag = 1
	}
	wideFlag := 0
	if isWideband {
		wideFlag = 1
	}
	C.opus_silk_process_nlsfs(
		(*C.opus_int16)(unsafe.Pointer(&nlsf[0])),
		(*C.opus_int16)(unsafe.Pointer(&prev[0])),
		C.int(lpcOrder),
		C.int(nbSubfr),
		C.int(signalType),
		C.int(speechActivityQ8),
		C.int(useInterpFlag),
		C.int(interpQ2),
		C.int(nlsfSurvivors),
		C.int(wideFlag),
		&out,
	)
	res := libopusNLSFProcessResult{
		Indices: make([]int8, lpcOrder+1),
		NLSFQ15: make([]int16, lpcOrder),
	}
	for i := 0; i < lpcOrder+1; i++ {
		res.Indices[i] = int8(out.indices[i])
	}
	for i := 0; i < lpcOrder; i++ {
		res.NLSFQ15[i] = int16(out.nlsf_Q15[i])
	}
	return res
}

func libopusA2NLSF(aQ16 []int32, order int) []int16 {
	if order <= 0 || len(aQ16) < order {
		return nil
	}
	a := make([]C.opus_int32, order)
	for i := 0; i < order; i++ {
		a[i] = C.opus_int32(aQ16[i])
	}
	out := make([]C.opus_int16, order)
	C.opus_silk_a2nlsf(
		(*C.opus_int32)(unsafe.Pointer(&a[0])),
		C.int(order),
		(*C.opus_int16)(unsafe.Pointer(&out[0])),
	)
	res := make([]int16, order)
	for i := 0; i < order; i++ {
		res[i] = int16(out[i])
	}
	return res
}

func libopusLTPAnalysisFilter(x []float32, b []float32, pitchLags []int, invGains []float32, subfrLen, nbSubfr, preLen int) []float32 {
	if len(x) == 0 || nbSubfr <= 0 {
		return nil
	}
	outLen := nbSubfr * (subfrLen + preLen)
	out := make([]float32, outLen)
	cLags := make([]C.int, nbSubfr)
	for i := 0; i < nbSubfr; i++ {
		if i < len(pitchLags) {
			cLags[i] = C.int(pitchLags[i])
		}
	}
	C.opus_silk_ltp_analysis_filter(
		(*C.float)(unsafe.Pointer(&x[0])),
		(*C.float)(unsafe.Pointer(&b[0])),
		(*C.int)(unsafe.Pointer(&cLags[0])),
		(*C.float)(unsafe.Pointer(&invGains[0])),
		C.int(subfrLen),
		C.int(nbSubfr),
		C.int(preLen),
		(*C.float)(unsafe.Pointer(&out[0])),
	)
	return out
}

func libopusBurgModified(x []float32, minInvGain float32, subfrLen, nbSubfr, order int) ([]float32, float32) {
	if len(x) == 0 || order <= 0 {
		return nil, 0
	}
	A := make([]float32, order)
	res := C.opus_silk_burg_modified(
		(*C.float)(unsafe.Pointer(&x[0])),
		C.float(minInvGain),
		C.int(subfrLen),
		C.int(nbSubfr),
		C.int(order),
		(*C.float)(unsafe.Pointer(&A[0])),
	)
	return A, float32(res)
}

func libopusNLSF2A(nlsf []int16, order int) []int16 {
	if len(nlsf) == 0 || order <= 0 {
		return nil
	}
	out := make([]int16, order)
	cNLSF := make([]C.opus_int16, order)
	for i := 0; i < order && i < len(nlsf); i++ {
		cNLSF[i] = C.opus_int16(nlsf[i])
	}
	C.opus_silk_nlsf2a(
		(*C.opus_int16)(unsafe.Pointer(&cNLSF[0])),
		C.int(order),
		(*C.opus_int16)(unsafe.Pointer(&out[0])),
	)
	return out
}

func libopusLPCAnalysisFilter(x []float32, pred []float32, length, order int) []float32 {
	if len(x) == 0 || len(pred) == 0 || length <= 0 || order <= 0 {
		return nil
	}
	out := make([]float32, length)
	C.opus_silk_lpc_analysis_filter(
		(*C.float)(unsafe.Pointer(&x[0])),
		(*C.float)(unsafe.Pointer(&pred[0])),
		C.int(length),
		C.int(order),
		(*C.float)(unsafe.Pointer(&out[0])),
	)
	return out
}

func libopusResampleOnce(in []float32, fsIn, fsOut int, forEnc bool) ([]int16, error) {
	if len(in) == 0 || fsIn <= 0 || fsOut <= 0 {
		return nil, fmt.Errorf("invalid resampler input")
	}
	outLen := len(in) * fsOut / fsIn
	if outLen <= 0 {
		return nil, fmt.Errorf("invalid resampler output length")
	}
	out := make([]int16, outLen)
	encFlag := 0
	if forEnc {
		encFlag = 1
	}
	ret := C.opus_silk_resample_once(
		(*C.float)(unsafe.Pointer(&in[0])),
		C.int(len(in)),
		C.int(fsIn),
		C.int(fsOut),
		C.int(encFlag),
		(*C.opus_int16)(unsafe.Pointer(&out[0])),
	)
	if ret != 0 {
		return nil, fmt.Errorf("libopus resampler failed: %d", int(ret))
	}
	return out, nil
}
