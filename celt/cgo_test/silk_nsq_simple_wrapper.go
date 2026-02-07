//go:build cgo_libopus
// +build cgo_libopus

// Package cgo provides CGO wrappers for SILK NSQ simple (non-delayed-decision) comparison.
package cgo

/*
#cgo CFLAGS: -I${SRCDIR}/../../tmp_check/opus-1.6.1/include -I${SRCDIR}/../../tmp_check/opus-1.6.1/celt -I${SRCDIR}/../../tmp_check/opus-1.6.1 -I${SRCDIR}/../../tmp_check/opus-1.6.1/silk -DHAVE_CONFIG_H
#cgo LDFLAGS: ${SRCDIR}/../../tmp_check/opus-1.6.1/.libs/libopus.a -lm

#include <stdlib.h>
#include <string.h>
#include "silk/main.h"
#include "silk/define.h"
#include "silk/structs.h"

void test_silk_nsq_simple(
    int frame_length, int subfr_length, int nb_subfr, int ltp_mem_length,
    int pred_lpc_order, int shape_lpc_order,
    int signalType, int quantOffsetType, int nlsfInterpCoef_Q2, int seed,
    const opus_int16 *x16,
    const opus_int16 *PredCoef_Q12,
    const opus_int16 *LTPCoef_Q14,
    const opus_int16 *AR_Q13,
    const opus_int *HarmShapeGain_Q14,
    const opus_int *Tilt_Q14,
    const opus_int32 *LF_shp_Q14,
    const opus_int32 *Gains_Q16,
    const int *pitchL,
    int Lambda_Q10,
    int LTP_scale_Q14,
    const opus_int16 *state_xq,
    int state_xq_len,
    const opus_int32 *state_sltp_shp_q14,
    int state_sltp_shp_len,
    const opus_int32 *state_sLPC_Q14,
    int state_sLPC_len,
    const opus_int32 *state_sAR2_Q14,
    int state_sAR2_len,
    opus_int32 state_lf_ar_q14,
    opus_int32 state_diff_q14,
    int state_lag_prev,
    int state_sltp_buf_idx,
    int state_sltp_shp_buf_idx,
    opus_int32 state_rand_seed,
    opus_int32 state_prev_gain_q16,
    int state_rewhite_flag,
    opus_int8 *out_pulses,
    opus_int16 *out_xq,
    // Final state output:
    opus_int16 *out_final_xq, int out_final_xq_len,
    opus_int32 *out_final_sltp_shp, int out_final_sltp_shp_len,
    opus_int32 *out_final_slpc, int out_final_slpc_len,
    opus_int32 *out_final_sar2, int out_final_sar2_len,
    opus_int32 *out_final_lf_ar,
    opus_int32 *out_final_diff,
    int *out_final_lag_prev,
    int *out_final_sltp_buf_idx,
    int *out_final_sltp_shp_buf_idx,
    opus_int32 *out_final_rand_seed,
    opus_int32 *out_final_prev_gain,
    int *out_final_rewhite
) {
    silk_encoder_state enc;
    silk_nsq_state nsq;
    SideInfoIndices indices;
    int n;
    memset(&enc, 0, sizeof(enc));
    memset(&nsq, 0, sizeof(nsq));
    memset(&indices, 0, sizeof(indices));

    enc.nb_subfr = nb_subfr;
    enc.frame_length = frame_length;
    enc.subfr_length = subfr_length;
    enc.ltp_mem_length = ltp_mem_length;
    enc.predictLPCOrder = pred_lpc_order;
    enc.shapingLPCOrder = shape_lpc_order;
    enc.warping_Q16 = 0;
    enc.nStatesDelayedDecision = 1;
    enc.arch = 0;

    indices.signalType = (opus_int8)signalType;
    indices.quantOffsetType = (opus_int8)quantOffsetType;
    indices.NLSFInterpCoef_Q2 = (opus_int8)nlsfInterpCoef_Q2;
    indices.Seed = (opus_int8)seed;

    nsq.prev_gain_Q16 = state_prev_gain_q16;
    nsq.lagPrev = state_lag_prev;
    nsq.sLTP_buf_idx = state_sltp_buf_idx;
    nsq.sLTP_shp_buf_idx = state_sltp_shp_buf_idx;
    nsq.rand_seed = state_rand_seed;
    nsq.rewhite_flag = state_rewhite_flag;
    nsq.sLF_AR_shp_Q14 = state_lf_ar_q14;
    nsq.sDiff_shp_Q14 = state_diff_q14;

    if (state_xq && state_xq_len > 0) {
        n = state_xq_len < (int)(sizeof(nsq.xq)/sizeof(nsq.xq[0])) ? state_xq_len : (int)(sizeof(nsq.xq)/sizeof(nsq.xq[0]));
        memcpy(nsq.xq, state_xq, n * sizeof(opus_int16));
    }
    if (state_sltp_shp_q14 && state_sltp_shp_len > 0) {
        n = state_sltp_shp_len < (int)(sizeof(nsq.sLTP_shp_Q14)/sizeof(nsq.sLTP_shp_Q14[0])) ? state_sltp_shp_len : (int)(sizeof(nsq.sLTP_shp_Q14)/sizeof(nsq.sLTP_shp_Q14[0]));
        memcpy(nsq.sLTP_shp_Q14, state_sltp_shp_q14, n * sizeof(opus_int32));
    }
    if (state_sLPC_Q14 && state_sLPC_len > 0) {
        n = state_sLPC_len < (int)(sizeof(nsq.sLPC_Q14)/sizeof(nsq.sLPC_Q14[0])) ? state_sLPC_len : (int)(sizeof(nsq.sLPC_Q14)/sizeof(nsq.sLPC_Q14[0]));
        memcpy(nsq.sLPC_Q14, state_sLPC_Q14, n * sizeof(opus_int32));
    }
    if (state_sAR2_Q14 && state_sAR2_len > 0) {
        n = state_sAR2_len < (int)(sizeof(nsq.sAR2_Q14)/sizeof(nsq.sAR2_Q14[0])) ? state_sAR2_len : (int)(sizeof(nsq.sAR2_Q14)/sizeof(nsq.sAR2_Q14[0]));
        memcpy(nsq.sAR2_Q14, state_sAR2_Q14, n * sizeof(opus_int32));
    }

    silk_NSQ_c(&enc, &nsq, &indices, x16, out_pulses, PredCoef_Q12, LTPCoef_Q14, AR_Q13,
               HarmShapeGain_Q14, Tilt_Q14, LF_shp_Q14, Gains_Q16, pitchL, Lambda_Q10, LTP_scale_Q14);

    if (out_xq) {
        memcpy(out_xq, &nsq.xq[ltp_mem_length], frame_length * sizeof(opus_int16));
    }

    // Copy final state
    if (out_final_xq && out_final_xq_len > 0) {
        n = out_final_xq_len < (int)(sizeof(nsq.xq)/sizeof(nsq.xq[0])) ? out_final_xq_len : (int)(sizeof(nsq.xq)/sizeof(nsq.xq[0]));
        memcpy(out_final_xq, nsq.xq, n * sizeof(opus_int16));
    }
    if (out_final_sltp_shp && out_final_sltp_shp_len > 0) {
        n = out_final_sltp_shp_len < (int)(sizeof(nsq.sLTP_shp_Q14)/sizeof(nsq.sLTP_shp_Q14[0])) ? out_final_sltp_shp_len : (int)(sizeof(nsq.sLTP_shp_Q14)/sizeof(nsq.sLTP_shp_Q14[0]));
        memcpy(out_final_sltp_shp, nsq.sLTP_shp_Q14, n * sizeof(opus_int32));
    }
    if (out_final_slpc && out_final_slpc_len > 0) {
        n = out_final_slpc_len < (int)(sizeof(nsq.sLPC_Q14)/sizeof(nsq.sLPC_Q14[0])) ? out_final_slpc_len : (int)(sizeof(nsq.sLPC_Q14)/sizeof(nsq.sLPC_Q14[0]));
        memcpy(out_final_slpc, nsq.sLPC_Q14, n * sizeof(opus_int32));
    }
    if (out_final_sar2 && out_final_sar2_len > 0) {
        n = out_final_sar2_len < (int)(sizeof(nsq.sAR2_Q14)/sizeof(nsq.sAR2_Q14[0])) ? out_final_sar2_len : (int)(sizeof(nsq.sAR2_Q14)/sizeof(nsq.sAR2_Q14[0]));
        memcpy(out_final_sar2, nsq.sAR2_Q14, n * sizeof(opus_int32));
    }
    if (out_final_lf_ar) *out_final_lf_ar = nsq.sLF_AR_shp_Q14;
    if (out_final_diff) *out_final_diff = nsq.sDiff_shp_Q14;
    if (out_final_lag_prev) *out_final_lag_prev = nsq.lagPrev;
    if (out_final_sltp_buf_idx) *out_final_sltp_buf_idx = nsq.sLTP_buf_idx;
    if (out_final_sltp_shp_buf_idx) *out_final_sltp_shp_buf_idx = nsq.sLTP_shp_buf_idx;
    if (out_final_rand_seed) *out_final_rand_seed = nsq.rand_seed;
    if (out_final_prev_gain) *out_final_prev_gain = nsq.prev_gain_Q16;
    if (out_final_rewhite) *out_final_rewhite = nsq.rewhite_flag;
}
*/
import "C"

import "unsafe"

// SilkNSQSimpleFinalState holds the final NSQ state after a simple NSQ call.
type SilkNSQSimpleFinalState struct {
	XQ            []int16
	SLTPShpQ14    []int32
	SLPCQ14       []int32
	SAR2Q14       []int32
	LFARQ14       int32
	DiffQ14       int32
	LagPrev       int
	SLTPBufIdx    int
	SLTPShpBufIdx int
	RandSeed      int32
	PrevGainQ16   int32
	RewhiteFlag   int
}

// SilkNSQSimpleWithState runs libopus silk_NSQ_c with provided inputs and initial state.
func SilkNSQSimpleWithState(
	frameLength, subfrLength, nbSubfr, ltpMemLength int,
	predLPCOrder, shapeLPCOrder int,
	signalType, quantOffsetType, nlsfInterpCoefQ2, seed int,
	x16 []int16,
	predCoefQ12 []int16,
	ltpCoefQ14 []int16,
	arShpQ13 []int16,
	harmShapeGainQ14 []int,
	tiltQ14 []int,
	lfShpQ14 []int32,
	gainsQ16 []int32,
	pitchL []int,
	lambdaQ10, ltpScaleQ14 int,
	stateXQ []int16,
	stateSLTPShpQ14 []int32,
	stateSLPCQ14 []int32,
	stateSAR2Q14 []int32,
	stateLFARQ14, stateDiffQ14 int32,
	stateLagPrev, stateSLTPBufIdx, stateSLTPShpBufIdx int,
	stateRandSeed, statePrevGainQ16 int32,
	stateRewhiteFlag int,
) ([]int8, []int16, SilkNSQSimpleFinalState) {
	empty := SilkNSQSimpleFinalState{}
	if frameLength <= 0 || len(x16) < frameLength {
		return nil, nil, empty
	}
	pulses := make([]int8, frameLength)
	xq := make([]int16, frameLength)

	if len(predCoefQ12) == 0 || len(ltpCoefQ14) == 0 || len(arShpQ13) == 0 ||
		len(harmShapeGainQ14) == 0 || len(tiltQ14) == 0 || len(lfShpQ14) == 0 ||
		len(gainsQ16) == 0 || len(pitchL) == 0 {
		return pulses, xq, empty
	}

	cHarm := make([]C.int, len(harmShapeGainQ14))
	for i, v := range harmShapeGainQ14 {
		cHarm[i] = C.int(v)
	}
	cTilt := make([]C.int, len(tiltQ14))
	for i, v := range tiltQ14 {
		cTilt[i] = C.int(v)
	}
	cPitch := make([]C.int, len(pitchL))
	for i, v := range pitchL {
		cPitch[i] = C.int(v)
	}

	const xqLen = 640
	const sltpShpLen = 640
	const slpcLen = 96
	const sar2Len = 24

	finalXQ := make([]int16, xqLen)
	finalSLTPShp := make([]int32, sltpShpLen)
	finalSLPC := make([]int32, slpcLen)
	finalSAR2 := make([]int32, sar2Len)
	var finalLFAR, finalDiff C.opus_int32
	var finalLagPrev, finalSLTPBufIdx, finalSLTPShpBufIdx, finalRewhite C.int
	var finalRandSeed, finalPrevGain C.opus_int32

	C.test_silk_nsq_simple(
		C.int(frameLength), C.int(subfrLength), C.int(nbSubfr), C.int(ltpMemLength),
		C.int(predLPCOrder), C.int(shapeLPCOrder),
		C.int(signalType), C.int(quantOffsetType), C.int(nlsfInterpCoefQ2), C.int(seed),
		(*C.opus_int16)(unsafe.Pointer(&x16[0])),
		(*C.opus_int16)(unsafe.Pointer(&predCoefQ12[0])),
		(*C.opus_int16)(unsafe.Pointer(&ltpCoefQ14[0])),
		(*C.opus_int16)(unsafe.Pointer(&arShpQ13[0])),
		(*C.opus_int)(unsafe.Pointer(&cHarm[0])),
		(*C.opus_int)(unsafe.Pointer(&cTilt[0])),
		(*C.opus_int32)(unsafe.Pointer(&lfShpQ14[0])),
		(*C.opus_int32)(unsafe.Pointer(&gainsQ16[0])),
		(*C.int)(unsafe.Pointer(&cPitch[0])),
		C.int(lambdaQ10),
		C.int(ltpScaleQ14),
		(*C.opus_int16)(unsafe.Pointer(&stateXQ[0])),
		C.int(len(stateXQ)),
		(*C.opus_int32)(unsafe.Pointer(&stateSLTPShpQ14[0])),
		C.int(len(stateSLTPShpQ14)),
		(*C.opus_int32)(unsafe.Pointer(&stateSLPCQ14[0])),
		C.int(len(stateSLPCQ14)),
		(*C.opus_int32)(unsafe.Pointer(&stateSAR2Q14[0])),
		C.int(len(stateSAR2Q14)),
		C.opus_int32(stateLFARQ14),
		C.opus_int32(stateDiffQ14),
		C.int(stateLagPrev),
		C.int(stateSLTPBufIdx),
		C.int(stateSLTPShpBufIdx),
		C.opus_int32(stateRandSeed),
		C.opus_int32(statePrevGainQ16),
		C.int(stateRewhiteFlag),
		(*C.opus_int8)(unsafe.Pointer(&pulses[0])),
		(*C.opus_int16)(unsafe.Pointer(&xq[0])),
		// Final state outputs
		(*C.opus_int16)(unsafe.Pointer(&finalXQ[0])), C.int(xqLen),
		(*C.opus_int32)(unsafe.Pointer(&finalSLTPShp[0])), C.int(sltpShpLen),
		(*C.opus_int32)(unsafe.Pointer(&finalSLPC[0])), C.int(slpcLen),
		(*C.opus_int32)(unsafe.Pointer(&finalSAR2[0])), C.int(sar2Len),
		&finalLFAR, &finalDiff,
		&finalLagPrev, &finalSLTPBufIdx, &finalSLTPShpBufIdx,
		&finalRandSeed, &finalPrevGain, &finalRewhite,
	)

	return pulses, xq, SilkNSQSimpleFinalState{
		XQ:            finalXQ,
		SLTPShpQ14:    finalSLTPShp,
		SLPCQ14:       finalSLPC,
		SAR2Q14:       finalSAR2,
		LFARQ14:       int32(finalLFAR),
		DiffQ14:       int32(finalDiff),
		LagPrev:       int(finalLagPrev),
		SLTPBufIdx:    int(finalSLTPBufIdx),
		SLTPShpBufIdx: int(finalSLTPShpBufIdx),
		RandSeed:      int32(finalRandSeed),
		PrevGainQ16:   int32(finalPrevGain),
		RewhiteFlag:   int(finalRewhite),
	}
}
