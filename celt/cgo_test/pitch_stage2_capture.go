//go:build cgo_libopus
// +build cgo_libopus

package cgo

/*
#cgo CFLAGS: -I${SRCDIR}/../../tmp_check/opus-1.6.1/include -I${SRCDIR}/../../tmp_check/opus-1.6.1/celt -I${SRCDIR}/../../tmp_check/opus-1.6.1 -I${SRCDIR}/../../tmp_check/opus-1.6.1/silk -DHAVE_CONFIG_H
#cgo LDFLAGS: ${SRCDIR}/../../tmp_check/opus-1.6.1/.libs/libopus.a -lm

#include <string.h>
#include "silk/main.h"
#include "silk/define.h"
#include "silk/structs.h"
#include "silk/pitch_est_defines.h"
#include "silk/pitch_est_tables.c"
#include "silk/float/SigProc_FLP.h"
#include "silk/resampler_private.h"

typedef struct {
    float ccmax_new;
    int cbimax;
} stage2_result;

static stage2_result opus_silk_pitch_stage2_eval(const float *frame, int fs_kHz, int nb_subfr, int complexity, int d) {
    stage2_result out;
    out.ccmax_new = -1000.0f;
    out.cbimax = 0;

    int frame_length = ( PE_LTP_MEM_LENGTH_MS + nb_subfr * PE_SUBFR_LENGTH_MS ) * fs_kHz;
    int frame_length_8kHz = ( PE_LTP_MEM_LENGTH_MS + nb_subfr * PE_SUBFR_LENGTH_MS ) * 8;
    int sf_length_8kHz = PE_SUBFR_LENGTH_MS * 8;

    opus_int16 frame_8_FIX[ PE_MAX_FRAME_LENGTH_MS * 8 ];
    opus_int16 frame_fix[ 16 * PE_MAX_FRAME_LENGTH_MS ];
    opus_int32 filt_state[ 6 ];
    float frame_8kHz[ PE_MAX_FRAME_LENGTH_MS * 8 ];

    if (fs_kHz == 16) {
        silk_float2short_array(frame_fix, frame, frame_length);
        silk_memset(filt_state, 0, 2 * sizeof(opus_int32));
        silk_resampler_down2(filt_state, frame_8_FIX, frame_fix, frame_length);
        silk_short2float_array(frame_8kHz, frame_8_FIX, frame_length_8kHz);
    } else if (fs_kHz == 12) {
        silk_float2short_array(frame_fix, frame, frame_length);
        silk_memset(filt_state, 0, 6 * sizeof(opus_int32));
        silk_resampler_down2_3(filt_state, frame_8_FIX, frame_fix, frame_length);
        silk_short2float_array(frame_8kHz, frame_8_FIX, frame_length_8kHz);
    } else {
        silk_float2short_array(frame_8_FIX, frame, frame_length_8kHz);
        silk_short2float_array(frame_8kHz, frame_8_FIX, frame_length_8kHz);
    }

    const int cbk_size = PE_NB_CBKS_STAGE2_EXT;
    const opus_int8 *Lag_CB_ptr = &silk_CB_lags_stage2[0][0];
    int nb_cbk_search = (fs_kHz == 8 && complexity > SILK_PE_MIN_COMPLEX) ? PE_NB_CBKS_STAGE2_EXT : PE_NB_CBKS_STAGE2;

    const float *target_ptr = &frame_8kHz[ PE_LTP_MEM_LENGTH_MS * 8 ];

    for (int j = 0; j < nb_cbk_search; j++) {
        float cc = 0.0f;
        const float *target = target_ptr;
        for (int k = 0; k < nb_subfr; k++) {
            float energy_tmp = (float)(silk_energy_FLP(target, sf_length_8kHz) + 1.0);
            int lag = d + matrix_ptr(Lag_CB_ptr, k, j, cbk_size);
            const float *basis = target - lag;
            double cross_corr = silk_inner_product_FLP(basis, target, sf_length_8kHz, 0);
            if (cross_corr > 0.0) {
                double energy = silk_energy_FLP(basis, sf_length_8kHz);
                cc += (float)(2.0 * cross_corr / (energy + energy_tmp));
            }
            target += sf_length_8kHz;
        }
        if (cc > out.ccmax_new) {
            out.ccmax_new = cc;
            out.cbimax = j;
        }
    }

    return out;
}

static int opus_silk_pitch_stage2_frame8(const float *frame, int fs_kHz, int nb_subfr, float *out, int out_len) {
    int frame_length = ( PE_LTP_MEM_LENGTH_MS + nb_subfr * PE_SUBFR_LENGTH_MS ) * fs_kHz;
    int frame_length_8kHz = ( PE_LTP_MEM_LENGTH_MS + nb_subfr * PE_SUBFR_LENGTH_MS ) * 8;

    opus_int16 frame_8_FIX[ PE_MAX_FRAME_LENGTH_MS * 8 ];
    opus_int16 frame_fix[ 16 * PE_MAX_FRAME_LENGTH_MS ];
    opus_int32 filt_state[ 6 ];
    float frame_8kHz[ PE_MAX_FRAME_LENGTH_MS * 8 ];

    if (fs_kHz == 16) {
        silk_float2short_array(frame_fix, frame, frame_length);
        silk_memset(filt_state, 0, 2 * sizeof(opus_int32));
        silk_resampler_down2(filt_state, frame_8_FIX, frame_fix, frame_length);
        silk_short2float_array(frame_8kHz, frame_8_FIX, frame_length_8kHz);
    } else if (fs_kHz == 12) {
        silk_float2short_array(frame_fix, frame, frame_length);
        silk_memset(filt_state, 0, 6 * sizeof(opus_int32));
        silk_resampler_down2_3(filt_state, frame_8_FIX, frame_fix, frame_length);
        silk_short2float_array(frame_8kHz, frame_8_FIX, frame_length_8kHz);
    } else {
        silk_float2short_array(frame_8_FIX, frame, frame_length_8kHz);
        silk_short2float_array(frame_8kHz, frame_8_FIX, frame_length_8kHz);
    }

    int n = frame_length_8kHz;
    if (n > out_len) {
        n = out_len;
    }
    if (n > 0) {
        memcpy(out, frame_8kHz, n * sizeof(float));
    }
    return n;
}

static void opus_silk_pitch_stage2_contrib(const float *frame, int fs_kHz, int nb_subfr, int complexity,
    int d, int cbimax, float *energy_tmp_out, float *energy_out, float *xcorr_out) {
    int frame_length = ( PE_LTP_MEM_LENGTH_MS + nb_subfr * PE_SUBFR_LENGTH_MS ) * fs_kHz;
    int frame_length_8kHz = ( PE_LTP_MEM_LENGTH_MS + nb_subfr * PE_SUBFR_LENGTH_MS ) * 8;
    int sf_length_8kHz = PE_SUBFR_LENGTH_MS * 8;

    opus_int16 frame_8_FIX[ PE_MAX_FRAME_LENGTH_MS * 8 ];
    opus_int16 frame_fix[ 16 * PE_MAX_FRAME_LENGTH_MS ];
    opus_int32 filt_state[ 6 ];
    float frame_8kHz[ PE_MAX_FRAME_LENGTH_MS * 8 ];

    if (fs_kHz == 16) {
        silk_float2short_array(frame_fix, frame, frame_length);
        silk_memset(filt_state, 0, 2 * sizeof(opus_int32));
        silk_resampler_down2(filt_state, frame_8_FIX, frame_fix, frame_length);
        silk_short2float_array(frame_8kHz, frame_8_FIX, frame_length_8kHz);
    } else if (fs_kHz == 12) {
        silk_float2short_array(frame_fix, frame, frame_length);
        silk_memset(filt_state, 0, 6 * sizeof(opus_int32));
        silk_resampler_down2_3(filt_state, frame_8_FIX, frame_fix, frame_length);
        silk_short2float_array(frame_8kHz, frame_8_FIX, frame_length_8kHz);
    } else {
        silk_float2short_array(frame_8_FIX, frame, frame_length_8kHz);
        silk_short2float_array(frame_8kHz, frame_8_FIX, frame_length_8kHz);
    }

    const int cbk_size = (nb_subfr == PE_MAX_NB_SUBFR) ? PE_NB_CBKS_STAGE2_EXT : PE_NB_CBKS_STAGE2_10MS;
    const opus_int8 *Lag_CB_ptr = (nb_subfr == PE_MAX_NB_SUBFR) ? &silk_CB_lags_stage2[0][0] : &silk_CB_lags_stage2_10_ms[0][0];
    int nb_cbk_search = (nb_subfr == PE_MAX_NB_SUBFR)
        ? ((fs_kHz == 8 && complexity > SILK_PE_MIN_COMPLEX) ? PE_NB_CBKS_STAGE2_EXT : PE_NB_CBKS_STAGE2)
        : PE_NB_CBKS_STAGE2_10MS;
    if (cbimax < 0) cbimax = 0;
    if (cbimax >= nb_cbk_search) cbimax = nb_cbk_search - 1;

    const float *target_ptr = &frame_8kHz[ PE_LTP_MEM_LENGTH_MS * 8 ];
    for (int k = 0; k < nb_subfr; k++) {
        float energy_tmp = (float)(silk_energy_FLP(target_ptr, sf_length_8kHz) + 1.0);
        int lag = d + matrix_ptr(Lag_CB_ptr, k, cbimax, cbk_size);
        const float *basis = target_ptr - lag;
        double cross_corr = silk_inner_product_FLP(basis, target_ptr, sf_length_8kHz, 0);
        double energy = silk_energy_FLP(basis, sf_length_8kHz);
        energy_tmp_out[k] = energy_tmp;
        energy_out[k] = (float)energy;
        xcorr_out[k] = (float)cross_corr;
        target_ptr += sf_length_8kHz;
    }
}
*/
import "C"

import "unsafe"

type Stage2EvalResult struct {
	CCmaxNew float32
	CBimax   int
}

func SilkPitchStage2Eval(frame []float32, fsKHz, nbSubfr, complexity, d int) Stage2EvalResult {
	if len(frame) == 0 {
		return Stage2EvalResult{}
	}
	res := C.opus_silk_pitch_stage2_eval((*C.float)(&frame[0]), C.int(fsKHz), C.int(nbSubfr), C.int(complexity), C.int(d))
	return Stage2EvalResult{
		CCmaxNew: float32(res.ccmax_new),
		CBimax:   int(res.cbimax),
	}
}

func SilkPitchStage2Frame8kHz(frame []float32, fsKHz, nbSubfr int) []float32 {
	if len(frame) == 0 {
		return nil
	}
	frameLen8k := (20 + nbSubfr*5) * 8
	out := make([]float32, frameLen8k)
	n := C.opus_silk_pitch_stage2_frame8((*C.float)(&frame[0]), C.int(fsKHz), C.int(nbSubfr), (*C.float)(&out[0]), C.int(len(out)))
	if n <= 0 {
		return nil
	}
	return out[:int(n)]
}

type Stage2ContribResult struct {
	EnergyTmp []float32
	Energy    []float32
	Xcorr     []float32
}

func SilkPitchStage2Contrib(frame []float32, fsKHz, nbSubfr, complexity, d, cbimax int) Stage2ContribResult {
	if len(frame) == 0 || nbSubfr <= 0 {
		return Stage2ContribResult{}
	}
	energyTmp := make([]float32, nbSubfr)
	energy := make([]float32, nbSubfr)
	xcorr := make([]float32, nbSubfr)
	C.opus_silk_pitch_stage2_contrib(
		(*C.float)(unsafe.Pointer(&frame[0])),
		C.int(fsKHz),
		C.int(nbSubfr),
		C.int(complexity),
		C.int(d),
		C.int(cbimax),
		(*C.float)(unsafe.Pointer(&energyTmp[0])),
		(*C.float)(unsafe.Pointer(&energy[0])),
		(*C.float)(unsafe.Pointer(&xcorr[0])),
	)
	return Stage2ContribResult{
		EnergyTmp: energyTmp,
		Energy:    energy,
		Xcorr:     xcorr,
	}
}
