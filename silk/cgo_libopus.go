//go:build cgo_libopus
// +build cgo_libopus

package silk

/*
#cgo CFLAGS: -I${SRCDIR}/../tmp_check/opus-1.6.1/include -I${SRCDIR}/../tmp_check/opus-1.6.1/celt -I${SRCDIR}/../tmp_check/opus-1.6.1 -I${SRCDIR}/../tmp_check/opus-1.6.1/silk -I${SRCDIR}/../tmp_check/opus-1.6.1/silk/float -DHAVE_CONFIG_H
#cgo LDFLAGS: -L${SRCDIR}/../tmp_check/opus-1.6.1/.libs -lopus -lm

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
*/
import "C"

import "unsafe"

type libopusLTPQuantResult struct {
	PerIndex     int8
	LTPIndex     [maxNbSubfr]int8
	BQ14         [maxNbSubfr * ltpOrderConst]int16
	PredGainQ7   int32
	SumLogGainQ7 int32
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
