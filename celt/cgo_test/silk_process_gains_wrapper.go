//go:build cgo_libopus
// +build cgo_libopus

package cgo

/*
#cgo CFLAGS: -I${SRCDIR}/../../tmp_check/opus-1.6.1/include -I${SRCDIR}/../../tmp_check/opus-1.6.1/celt -I${SRCDIR}/../../tmp_check/opus-1.6.1 -I${SRCDIR}/../../tmp_check/opus-1.6.1/silk -I${SRCDIR}/../../tmp_check/opus-1.6.1/silk/float -DHAVE_CONFIG_H
#cgo LDFLAGS: ${SRCDIR}/../../tmp_check/opus-1.6.1/.libs/libopus.a -lm

#include <string.h>
#include "main_FLP.h"

typedef struct {
    int gains_indices[MAX_NB_SUBFR];
    int gains_unq_q16[MAX_NB_SUBFR];
    int quant_offset_type;
    int last_gain_index_prev;
    int last_gain_index_out;
    float gains[MAX_NB_SUBFR];
    float lambda;
} silk_process_gains_snapshot;

static int test_silk_process_gains_flp(
    const float *gains_in,
    const float *res_nrg_in,
    int nb_subfr,
    int subfr_length,
    int signal_type,
    float ltp_pred_cod_gain,
    int input_tilt_q15,
    int snr_db_q7,
    int speech_activity_q8,
    int n_states_delayed_decision,
    int last_gain_index_in,
    int conditional,
    silk_process_gains_snapshot *out
) {
    silk_encoder_state_FLP enc;
    silk_encoder_control_FLP ctrl;
    int i;

    if (!gains_in || !res_nrg_in || !out) {
        return -1;
    }
    if (nb_subfr <= 0 || nb_subfr > MAX_NB_SUBFR) {
        return -2;
    }

    memset(&enc, 0, sizeof(enc));
    memset(&ctrl, 0, sizeof(ctrl));
    memset(out, 0, sizeof(*out));

    enc.sCmn.nb_subfr = nb_subfr;
    enc.sCmn.subfr_length = subfr_length;
    enc.sCmn.indices.signalType = (opus_int8)signal_type;
    enc.sCmn.input_tilt_Q15 = input_tilt_q15;
    enc.sCmn.SNR_dB_Q7 = snr_db_q7;
    enc.sCmn.speech_activity_Q8 = speech_activity_q8;
    enc.sCmn.nStatesDelayedDecision = n_states_delayed_decision;
    enc.sShape.LastGainIndex = (opus_int8)last_gain_index_in;

    ctrl.LTPredCodGain = ltp_pred_cod_gain;
    ctrl.input_quality = 0.0f;
    ctrl.coding_quality = 0.0f;
    for (i = 0; i < nb_subfr; i++) {
        ctrl.Gains[i] = gains_in[i];
        ctrl.ResNrg[i] = res_nrg_in[i];
    }

    silk_process_gains_FLP(&enc, &ctrl, conditional ? CODE_CONDITIONALLY : CODE_INDEPENDENTLY);

    out->quant_offset_type = enc.sCmn.indices.quantOffsetType;
    out->last_gain_index_prev = ctrl.lastGainIndexPrev;
    out->last_gain_index_out = enc.sShape.LastGainIndex;
    out->lambda = ctrl.Lambda;
    for (i = 0; i < nb_subfr; i++) {
        out->gains_indices[i] = enc.sCmn.indices.GainsIndices[i];
        out->gains_unq_q16[i] = ctrl.GainsUnq_Q16[i];
        out->gains[i] = ctrl.Gains[i];
    }
    return 0;
}
*/
import "C"

import "unsafe"

// SilkProcessGainsSnapshot captures key outputs from libopus silk_process_gains_FLP.
type SilkProcessGainsSnapshot struct {
	GainsIndices     [4]int8
	GainsUnqQ16      [4]int32
	QuantOffsetType  int
	LastGainIndexIn  int8
	LastGainIndexOut int8
	Gains            [4]float32
	Lambda           float32
}

// SilkProcessGainsFLP runs libopus silk_process_gains_FLP with caller-provided inputs.
func SilkProcessGainsFLP(
	gainsIn []float32,
	resNrgIn []float32,
	nbSubfr int,
	subfrLength int,
	signalType int,
	ltpPredCodGain float32,
	inputTiltQ15 int,
	snrDBQ7 int,
	speechActivityQ8 int,
	nStatesDelayedDecision int,
	lastGainIndexIn int8,
	conditional bool,
) (SilkProcessGainsSnapshot, bool) {
	if nbSubfr <= 0 || nbSubfr > 4 || len(gainsIn) < nbSubfr || len(resNrgIn) < nbSubfr {
		return SilkProcessGainsSnapshot{}, false
	}
	var out C.silk_process_gains_snapshot
	cConditional := C.int(0)
	if conditional {
		cConditional = 1
	}
	ret := C.test_silk_process_gains_flp(
		(*C.float)(unsafe.Pointer(&gainsIn[0])),
		(*C.float)(unsafe.Pointer(&resNrgIn[0])),
		C.int(nbSubfr),
		C.int(subfrLength),
		C.int(signalType),
		C.float(ltpPredCodGain),
		C.int(inputTiltQ15),
		C.int(snrDBQ7),
		C.int(speechActivityQ8),
		C.int(nStatesDelayedDecision),
		C.int(lastGainIndexIn),
		cConditional,
		&out,
	)
	if ret != 0 {
		return SilkProcessGainsSnapshot{}, false
	}

	s := SilkProcessGainsSnapshot{
		QuantOffsetType:  int(out.quant_offset_type),
		LastGainIndexIn:  int8(out.last_gain_index_prev),
		LastGainIndexOut: int8(out.last_gain_index_out),
		Lambda:           float32(out.lambda),
	}
	for i := 0; i < nbSubfr && i < 4; i++ {
		s.GainsIndices[i] = int8(out.gains_indices[i])
		s.GainsUnqQ16[i] = int32(out.gains_unq_q16[i])
		s.Gains[i] = float32(out.gains[i])
	}
	return s, true
}
