//go:build cgo_libopus
// +build cgo_libopus

// Package cgo provides CGO helpers for libopus NSQ delayed-decision capture.
package cgo

/*
#cgo CFLAGS: -I${SRCDIR}/../../tmp_check/opus-1.6.1/include -I${SRCDIR}/../../tmp_check/opus-1.6.1/celt -I${SRCDIR}/../../tmp_check/opus-1.6.1 -I${SRCDIR}/../../tmp_check/opus-1.6.1/silk -DHAVE_CONFIG_H
#cgo LDFLAGS: ${SRCDIR}/../../tmp_check/opus-1.6.1/.libs/libopus.a -lm

#include <string.h>
#include <stdlib.h>
#include "silk/main.h"
#include "silk/define.h"
#include "silk/structs.h"
#include "silk/NSQ.h"
#undef VAR_ARRAYS
#define USE_ALLOCA
#include "stack_alloc.h"

// Static buffer to capture sLTP_Q15.
static opus_int32 g_sltp_q15[ MAX_FRAME_LENGTH * 2 ];
static int g_sltp_q15_len = 0;
static opus_int32 *g_sltp_q15_dyn = NULL;

// Track heap allocations for cleanup.
static void *g_psDelDec_ptr = NULL;
static void *g_sLTP_ptr = NULL;
static void *g_x_sc_ptr = NULL;
static void *g_delayedGain_ptr = NULL;
static void *g_psSampleState_ptr = NULL;

static void *test_alloc_psDelDec(size_t n, size_t sz) {
    g_psDelDec_ptr = malloc(n * sz);
    return g_psDelDec_ptr;
}
static void *test_alloc_sLTP_Q15(size_t n, size_t sz) {
    g_sltp_q15_len = (int)n;
    if (n <= (size_t)(MAX_FRAME_LENGTH * 2)) {
        return g_sltp_q15;
    }
    g_sltp_q15_dyn = (opus_int32*)malloc(n * sz);
    return g_sltp_q15_dyn;
}
static void *test_alloc_sLTP(size_t n, size_t sz) {
    g_sLTP_ptr = malloc(n * sz);
    return g_sLTP_ptr;
}
static void *test_alloc_x_sc_Q10(size_t n, size_t sz) {
    g_x_sc_ptr = malloc(n * sz);
    return g_x_sc_ptr;
}
static void *test_alloc_delayedGain_Q10(size_t n, size_t sz) {
    g_delayedGain_ptr = malloc(n * sz);
    if (g_delayedGain_ptr) {
        memset(g_delayedGain_ptr, 0, n * sz);
    }
    return g_delayedGain_ptr;
}
static void *test_alloc_psSampleState(size_t n, size_t sz) {
    g_psSampleState_ptr = malloc(n * sz);
    return g_psSampleState_ptr;
}

// Override stack allocator to use static buffers for capture.
#undef ALLOC
#define ALLOC(var, size, type) var = (type*)test_alloc_##var((size_t)(size), sizeof(type))

// Rename libopus function to avoid symbol clash.
#define silk_NSQ_del_dec_c test_silk_NSQ_del_dec_capture_internal
#include "silk/NSQ_del_dec.c"
#undef silk_NSQ_del_dec_c

void test_silk_nsq_del_dec_capture(
    int frame_length, int subfr_length, int nb_subfr, int ltp_mem_length,
    int pred_lpc_order, int shape_lpc_order, int warping_Q16, int nStates,
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
    opus_int8 *out_pulses,
    opus_int16 *out_xq,
    int *out_seed,
    opus_int32 *out_sltp_q15,
    int out_sltp_q15_len,
    opus_int16 *out_sltp,
    int out_sltp_len,
    opus_int32 *out_delayed_gain_q10,
    int out_delayed_gain_len
) {
    silk_encoder_state enc;
    silk_nsq_state nsq;
    SideInfoIndices indices;
    memset(&enc, 0, sizeof(enc));
    memset(&nsq, 0, sizeof(nsq));
    memset(&indices, 0, sizeof(indices));

    memset(g_sltp_q15, 0, sizeof(g_sltp_q15));
    if (g_sltp_q15_dyn) {
        memset(g_sltp_q15_dyn, 0, g_sltp_q15_len * (int)sizeof(opus_int32));
    }

    enc.nb_subfr = nb_subfr;
    enc.frame_length = frame_length;
    enc.subfr_length = subfr_length;
    enc.ltp_mem_length = ltp_mem_length;
    enc.predictLPCOrder = pred_lpc_order;
    enc.shapingLPCOrder = shape_lpc_order;
    enc.warping_Q16 = warping_Q16;
    enc.nStatesDelayedDecision = nStates;
    enc.arch = 0;

    indices.signalType = (opus_int8)signalType;
    indices.quantOffsetType = (opus_int8)quantOffsetType;
    indices.NLSFInterpCoef_Q2 = (opus_int8)nlsfInterpCoef_Q2;
    indices.Seed = (opus_int8)seed;

    nsq.prev_gain_Q16 = 65536;

    test_silk_NSQ_del_dec_capture_internal(&enc, &nsq, &indices, x16, out_pulses,
        PredCoef_Q12, LTPCoef_Q14, AR_Q13, HarmShapeGain_Q14, Tilt_Q14, LF_shp_Q14,
        Gains_Q16, pitchL, Lambda_Q10, LTP_scale_Q14);

    if (out_xq) {
        memcpy(out_xq, &nsq.xq[ltp_mem_length], frame_length * sizeof(opus_int16));
    }
    if (out_seed) {
        *out_seed = indices.Seed;
    }
    if (out_sltp_q15 && out_sltp_q15_len > 0) {
        opus_int32 *src = g_sltp_q15_dyn ? g_sltp_q15_dyn : g_sltp_q15;
        int max_len = g_sltp_q15_dyn ? g_sltp_q15_len : (int)(sizeof(g_sltp_q15) / sizeof(g_sltp_q15[0]));
        int n = out_sltp_q15_len < max_len ? out_sltp_q15_len : max_len;
        memcpy(out_sltp_q15, src, n * sizeof(opus_int32));
    }
    if (out_sltp && out_sltp_len > 0 && g_sLTP_ptr) {
        int n = out_sltp_len;
        memcpy(out_sltp, g_sLTP_ptr, n * sizeof(opus_int16));
    }
    if (out_delayed_gain_q10 && out_delayed_gain_len > 0 && g_delayedGain_ptr) {
        int n = out_delayed_gain_len;
        memcpy(out_delayed_gain_q10, g_delayedGain_ptr, n * sizeof(opus_int32));
    }

    if (g_psDelDec_ptr) { free(g_psDelDec_ptr); g_psDelDec_ptr = NULL; }
    if (g_sLTP_ptr) { free(g_sLTP_ptr); g_sLTP_ptr = NULL; }
    if (g_x_sc_ptr) { free(g_x_sc_ptr); g_x_sc_ptr = NULL; }
    if (g_delayedGain_ptr) { free(g_delayedGain_ptr); g_delayedGain_ptr = NULL; }
    if (g_psSampleState_ptr) { free(g_psSampleState_ptr); g_psSampleState_ptr = NULL; }
    if (g_sltp_q15_dyn) { free(g_sltp_q15_dyn); g_sltp_q15_dyn = NULL; }
}

void test_silk_nsq_del_dec_capture_with_state(
    int frame_length, int subfr_length, int nb_subfr, int ltp_mem_length,
    int pred_lpc_order, int shape_lpc_order, int warping_Q16, int nStates,
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
    int *out_seed,
    opus_int32 *out_sltp_q15,
    int out_sltp_q15_len,
    opus_int16 *out_sltp,
    int out_sltp_len,
    opus_int32 *out_delayed_gain_q10,
    int out_delayed_gain_len
) {
    silk_encoder_state enc;
    silk_nsq_state nsq;
    SideInfoIndices indices;
    int n;
    memset(&enc, 0, sizeof(enc));
    memset(&nsq, 0, sizeof(nsq));
    memset(&indices, 0, sizeof(indices));

    memset(g_sltp_q15, 0, sizeof(g_sltp_q15));
    if (g_sltp_q15_dyn) {
        memset(g_sltp_q15_dyn, 0, g_sltp_q15_len * (int)sizeof(opus_int32));
    }

    enc.nb_subfr = nb_subfr;
    enc.frame_length = frame_length;
    enc.subfr_length = subfr_length;
    enc.ltp_mem_length = ltp_mem_length;
    enc.predictLPCOrder = pred_lpc_order;
    enc.shapingLPCOrder = shape_lpc_order;
    enc.warping_Q16 = warping_Q16;
    enc.nStatesDelayedDecision = nStates;
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

    test_silk_NSQ_del_dec_capture_internal(&enc, &nsq, &indices, x16, out_pulses,
        PredCoef_Q12, LTPCoef_Q14, AR_Q13, HarmShapeGain_Q14, Tilt_Q14, LF_shp_Q14,
        Gains_Q16, pitchL, Lambda_Q10, LTP_scale_Q14);

    if (out_xq) {
        memcpy(out_xq, &nsq.xq[ltp_mem_length], frame_length * sizeof(opus_int16));
    }
    if (out_seed) {
        *out_seed = indices.Seed;
    }
    if (out_sltp_q15 && out_sltp_q15_len > 0) {
        opus_int32 *src = g_sltp_q15_dyn ? g_sltp_q15_dyn : g_sltp_q15;
        int max_len = g_sltp_q15_dyn ? g_sltp_q15_len : (int)(sizeof(g_sltp_q15) / sizeof(g_sltp_q15[0]));
        n = out_sltp_q15_len < max_len ? out_sltp_q15_len : max_len;
        memcpy(out_sltp_q15, src, n * sizeof(opus_int32));
    }
    if (out_sltp && out_sltp_len > 0 && g_sLTP_ptr) {
        n = out_sltp_len;
        memcpy(out_sltp, g_sLTP_ptr, n * sizeof(opus_int16));
    }
    if (out_delayed_gain_q10 && out_delayed_gain_len > 0 && g_delayedGain_ptr) {
        n = out_delayed_gain_len;
        memcpy(out_delayed_gain_q10, g_delayedGain_ptr, n * sizeof(opus_int32));
    }

    if (g_psDelDec_ptr) { free(g_psDelDec_ptr); g_psDelDec_ptr = NULL; }
    if (g_sLTP_ptr) { free(g_sLTP_ptr); g_sLTP_ptr = NULL; }
    if (g_x_sc_ptr) { free(g_x_sc_ptr); g_x_sc_ptr = NULL; }
    if (g_delayedGain_ptr) { free(g_delayedGain_ptr); g_delayedGain_ptr = NULL; }
    if (g_psSampleState_ptr) { free(g_psSampleState_ptr); g_psSampleState_ptr = NULL; }
    if (g_sltp_q15_dyn) { free(g_sltp_q15_dyn); g_sltp_q15_dyn = NULL; }
}
*/
import "C"

import "unsafe"

const delayedGainLen = 40

// SilkNSQDelDecCaptureSLTPQ15 runs libopus NSQ_del_dec and returns sLTP_Q15.
func SilkNSQDelDecCaptureSLTPQ15(
	frameLength, subfrLength, nbSubfr, ltpMemLength int,
	predLPCOrder, shapeLPCOrder, warpingQ16, nStates int,
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
) ([]int8, []int16, int, []int32, []int16, []int32) {
	if frameLength <= 0 || len(x16) < frameLength {
		return nil, nil, seed, nil, nil, nil
	}
	pulses := make([]int8, frameLength)
	xq := make([]int16, frameLength)
	sltp := make([]int32, ltpMemLength+frameLength)
	sltpRaw := make([]int16, ltpMemLength+frameLength)
	delayedGain := make([]int32, delayedGainLen)

	if len(predCoefQ12) == 0 || len(ltpCoefQ14) == 0 || len(arShpQ13) == 0 ||
		len(harmShapeGainQ14) == 0 || len(tiltQ14) == 0 || len(lfShpQ14) == 0 ||
		len(gainsQ16) == 0 || len(pitchL) == 0 {
		return pulses, xq, seed, sltp, sltpRaw, delayedGain
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

	var cSeed C.int
	C.test_silk_nsq_del_dec_capture(
		C.int(frameLength), C.int(subfrLength), C.int(nbSubfr), C.int(ltpMemLength),
		C.int(predLPCOrder), C.int(shapeLPCOrder), C.int(warpingQ16), C.int(nStates),
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
		(*C.opus_int8)(unsafe.Pointer(&pulses[0])),
		(*C.opus_int16)(unsafe.Pointer(&xq[0])),
		&cSeed,
		(*C.opus_int32)(unsafe.Pointer(&sltp[0])),
		C.int(len(sltp)),
		(*C.opus_int16)(unsafe.Pointer(&sltpRaw[0])),
		C.int(len(sltpRaw)),
		(*C.opus_int32)(unsafe.Pointer(&delayedGain[0])),
		C.int(len(delayedGain)),
	)

	return pulses, xq, int(cSeed), sltp, sltpRaw, delayedGain
}

// SilkNSQDelDecCaptureWithState runs libopus NSQ_del_dec with a seeded nsq state snapshot.
func SilkNSQDelDecCaptureWithState(
	frameLength, subfrLength, nbSubfr, ltpMemLength int,
	predLPCOrder, shapeLPCOrder, warpingQ16, nStates int,
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
	stateLFARQ14 int32,
	stateDiffQ14 int32,
	stateLagPrev int,
	stateSLTPBufIdx int,
	stateSLTPShpBufIdx int,
	stateRandSeed int32,
	statePrevGainQ16 int32,
	stateRewhiteFlag int,
) ([]int8, []int16, int, []int32, []int16, []int32) {
	if frameLength <= 0 || len(x16) < frameLength {
		return nil, nil, seed, nil, nil, nil
	}
	pulses := make([]int8, frameLength)
	xq := make([]int16, frameLength)
	sltp := make([]int32, ltpMemLength+frameLength)
	sltpRaw := make([]int16, ltpMemLength+frameLength)
	delayedGain := make([]int32, delayedGainLen)

	if len(predCoefQ12) == 0 || len(ltpCoefQ14) == 0 || len(arShpQ13) == 0 ||
		len(harmShapeGainQ14) == 0 || len(tiltQ14) == 0 || len(lfShpQ14) == 0 ||
		len(gainsQ16) == 0 || len(pitchL) == 0 {
		return pulses, xq, seed, sltp, sltpRaw, delayedGain
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

	var cSeed C.int
	C.test_silk_nsq_del_dec_capture_with_state(
		C.int(frameLength), C.int(subfrLength), C.int(nbSubfr), C.int(ltpMemLength),
		C.int(predLPCOrder), C.int(shapeLPCOrder), C.int(warpingQ16), C.int(nStates),
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
		&cSeed,
		(*C.opus_int32)(unsafe.Pointer(&sltp[0])),
		C.int(len(sltp)),
		(*C.opus_int16)(unsafe.Pointer(&sltpRaw[0])),
		C.int(len(sltpRaw)),
		(*C.opus_int32)(unsafe.Pointer(&delayedGain[0])),
		C.int(len(delayedGain)),
	)

	return pulses, xq, int(cSeed), sltp, sltpRaw, delayedGain
}
