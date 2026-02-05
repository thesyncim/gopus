//go:build cgo_libopus
// +build cgo_libopus

// Package cgo provides CGO wrappers for SILK NSQ delayed-decision comparison.
package cgo

/*
#cgo CFLAGS: -I${SRCDIR}/../../tmp_check/opus-1.6.1/include -I${SRCDIR}/../../tmp_check/opus-1.6.1/celt -I${SRCDIR}/../../tmp_check/opus-1.6.1 -I${SRCDIR}/../../tmp_check/opus-1.6.1/silk -DHAVE_CONFIG_H
#cgo LDFLAGS: ${SRCDIR}/../../tmp_check/opus-1.6.1/.libs/libopus.a -lm

#include <stdlib.h>
#include <string.h>
#include "silk/main.h"
#include "silk/define.h"
#include "silk/structs.h"

void test_silk_nsq_del_dec(
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
    int *out_seed
) {
    silk_encoder_state enc;
    silk_nsq_state nsq;
    SideInfoIndices indices;
    memset(&enc, 0, sizeof(enc));
    memset(&nsq, 0, sizeof(nsq));
    memset(&indices, 0, sizeof(indices));

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

    silk_NSQ_del_dec_c(&enc, &nsq, &indices, x16, out_pulses, PredCoef_Q12, LTPCoef_Q14, AR_Q13,
                       HarmShapeGain_Q14, Tilt_Q14, LF_shp_Q14, Gains_Q16, pitchL, Lambda_Q10, LTP_scale_Q14);

    if (out_xq) {
        memcpy(out_xq, &nsq.xq[ltp_mem_length], frame_length * sizeof(opus_int16));
    }
    if (out_seed) {
        *out_seed = indices.Seed;
    }
}
*/
import "C"

import "unsafe"

// SilkNSQDelDec runs libopus silk_NSQ_del_dec_c with provided inputs and returns pulses, xq, and seed.
func SilkNSQDelDec(
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
) ([]int8, []int16, int) {
	if frameLength <= 0 || len(x16) < frameLength {
		return nil, nil, seed
	}
	pulses := make([]int8, frameLength)
	xq := make([]int16, frameLength)

	if len(predCoefQ12) == 0 || len(ltpCoefQ14) == 0 || len(arShpQ13) == 0 ||
		len(harmShapeGainQ14) == 0 || len(tiltQ14) == 0 || len(lfShpQ14) == 0 ||
		len(gainsQ16) == 0 || len(pitchL) == 0 {
		return pulses, xq, seed
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

	C.test_silk_nsq_del_dec(
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
	)

	return pulses, xq, int(cSeed)
}
