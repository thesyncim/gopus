//go:build cgo_libopus
// +build cgo_libopus

package silk

/*
#cgo CFLAGS: -I${SRCDIR}/../tmp_check/opus-1.6.1/include -I${SRCDIR}/../tmp_check/opus-1.6.1 -I${SRCDIR}/../tmp_check/opus-1.6.1/silk -I${SRCDIR}/../tmp_check/opus-1.6.1/silk/float -DHAVE_CONFIG_H
#cgo LDFLAGS: ${SRCDIR}/../tmp_check/opus-1.6.1/.libs/libopus.a -lm

#include <string.h>
#include <stdlib.h>
#include "silk/main.h"

typedef struct {
    int speech_activity_Q8;
    int input_tilt_Q15;
    int input_quality_bands_Q15[VAD_N_BANDS];
    int nrg_ratio_smth_Q8[VAD_N_BANDS];
    int nl[VAD_N_BANDS];
    int inv_nl[VAD_N_BANDS];
} vad_result;

typedef struct {
    int speech_activity_Q8;
    int input_tilt_Q15;
    int input_tilt;
    int input_quality_bands_Q15[VAD_N_BANDS];
    int nrg_ratio_smth_Q8[VAD_N_BANDS];
    int nl[VAD_N_BANDS];
    int inv_nl[VAD_N_BANDS];
    int xnrg[VAD_N_BANDS];
    int xnrg_subfr[VAD_N_BANDS];
    int subfr_energy[VAD_N_BANDS][VAD_INTERNAL_SUBFRAMES];
    int nrg_to_noise_ratio_Q8[VAD_N_BANDS];
    int snr_q7[VAD_N_BANDS];
    int snr_q7_tilt[VAD_N_BANDS];
    int snr_q7_smth[VAD_N_BANDS];
    int sum_squared;
    int pSNR_dB_Q7;
    int SA_Q15;
    int speech_nrg_pre;
    int speech_nrg_post;
    int smooth_coef_Q16;
    int hp_state;
} vad_trace;

static const opus_int32 tiltWeights[VAD_N_BANDS] = { 30000, 6000, -12000, -12000 };

static void opus_silk_vad_init(silk_VAD_state *st) {
    silk_VAD_Init(st);
}

static void opus_silk_vad_get(const opus_int16 *pcm, int frame_length, int fs_kHz, silk_VAD_state *state, vad_result *out) {
    silk_encoder_state enc;
    memset(&enc, 0, sizeof(enc));
    enc.frame_length = frame_length;
    enc.fs_kHz = fs_kHz;
    enc.sVAD = *state;
    silk_VAD_GetSA_Q8_c(&enc, pcm);
    *state = enc.sVAD;
    out->speech_activity_Q8 = enc.speech_activity_Q8;
    out->input_tilt_Q15 = enc.input_tilt_Q15;
    for (int i = 0; i < VAD_N_BANDS; i++) {
        out->input_quality_bands_Q15[i] = enc.input_quality_bands_Q15[i];
        out->nrg_ratio_smth_Q8[i] = state->NrgRatioSmth_Q8[i];
        out->nl[i] = state->NL[i];
        out->inv_nl[i] = state->inv_NL[i];
    }
}

static OPUS_INLINE void opus_silk_vad_get_noise_levels(const opus_int32 pX[VAD_N_BANDS], silk_VAD_state *psSilk_VAD) {
    opus_int k;
    opus_int32 nl, nrg, inv_nrg;
    opus_int coef, min_coef;

    if (psSilk_VAD->counter < 1000) {
        min_coef = silk_DIV32_16(silk_int16_MAX, silk_RSHIFT(psSilk_VAD->counter, 4) + 1);
        psSilk_VAD->counter++;
    } else {
        min_coef = 0;
    }

    for (k = 0; k < VAD_N_BANDS; k++) {
        nl = psSilk_VAD->NL[k];
        nrg = silk_ADD_POS_SAT32(pX[k], psSilk_VAD->NoiseLevelBias[k]);
        inv_nrg = silk_DIV32(silk_int32_MAX, nrg);

        if (nrg > silk_LSHIFT(nl, 3)) {
            coef = VAD_NOISE_LEVEL_SMOOTH_COEF_Q16 >> 3;
        } else if (nrg < nl) {
            coef = VAD_NOISE_LEVEL_SMOOTH_COEF_Q16;
        } else {
            coef = silk_SMULWB(silk_SMULWW(inv_nrg, nl), VAD_NOISE_LEVEL_SMOOTH_COEF_Q16 << 1);
        }

        coef = silk_max_int(coef, min_coef);

        psSilk_VAD->inv_NL[k] = silk_SMLAWB(psSilk_VAD->inv_NL[k], inv_nrg - psSilk_VAD->inv_NL[k], coef);
        if (psSilk_VAD->inv_NL[k] < 0) {
            psSilk_VAD->inv_NL[k] = 0;
        }
        nl = silk_DIV32(silk_int32_MAX, psSilk_VAD->inv_NL[k]);
        nl = silk_min(nl, 0x00FFFFFF);
        psSilk_VAD->NL[k] = nl;
    }
}

static void opus_silk_vad_get_trace(const opus_int16 *pIn, int frame_length, int fs_kHz, silk_VAD_state *psSilk_VAD, vad_trace *out) {
    opus_int   SA_Q15, pSNR_dB_Q7, input_tilt;
    opus_int   decimated_framelength1, decimated_framelength2;
    opus_int   decimated_framelength;
    opus_int   dec_subframe_length, dec_subframe_offset, SNR_Q7, i, b, s;
    opus_int32 sumSquared, smooth_coef_Q16;
    opus_int16 HPstateTmp;
    opus_int32 Xnrg[ VAD_N_BANDS ];
    opus_int32 NrgToNoiseRatio_Q8[ VAD_N_BANDS ];
    opus_int32 speech_nrg, x_tmp;
    opus_int   X_offset[ VAD_N_BANDS ];
    opus_int16 *X;

    if (out == NULL) {
        return;
    }
    memset(out, 0, sizeof(*out));

    decimated_framelength1 = silk_RSHIFT(frame_length, 1);
    decimated_framelength2 = silk_RSHIFT(frame_length, 2);
    decimated_framelength  = silk_RSHIFT(frame_length, 3);

    X_offset[0] = 0;
    X_offset[1] = decimated_framelength + decimated_framelength2;
    X_offset[2] = X_offset[1] + decimated_framelength;
    X_offset[3] = X_offset[2] + decimated_framelength2;

    X = (opus_int16*)malloc(sizeof(opus_int16) * (X_offset[3] + decimated_framelength1));
    if (X == NULL) {
        return;
    }

    silk_ana_filt_bank_1(pIn, &psSilk_VAD->AnaState[0], X, &X[X_offset[3]], frame_length);
    silk_ana_filt_bank_1(X, &psSilk_VAD->AnaState1[0], X, &X[X_offset[2]], decimated_framelength1);
    silk_ana_filt_bank_1(X, &psSilk_VAD->AnaState2[0], X, &X[X_offset[1]], decimated_framelength2);

    X[decimated_framelength - 1] = silk_RSHIFT(X[decimated_framelength - 1], 1);
    HPstateTmp = X[decimated_framelength - 1];
    for (i = decimated_framelength - 1; i > 0; i--) {
        X[i - 1] = silk_RSHIFT(X[i - 1], 1);
        X[i] -= X[i - 1];
    }
    X[0] -= psSilk_VAD->HPstate;
    psSilk_VAD->HPstate = HPstateTmp;
    out->hp_state = (int)psSilk_VAD->HPstate;

    for (b = 0; b < VAD_N_BANDS; b++) {
        decimated_framelength = silk_RSHIFT(frame_length, silk_min_int(VAD_N_BANDS - b, VAD_N_BANDS - 1));
        dec_subframe_length = silk_RSHIFT(decimated_framelength, VAD_INTERNAL_SUBFRAMES_LOG2);
        dec_subframe_offset = 0;

        Xnrg[b] = psSilk_VAD->XnrgSubfr[b];
        for (s = 0; s < VAD_INTERNAL_SUBFRAMES; s++) {
            sumSquared = 0;
            for (i = 0; i < dec_subframe_length; i++) {
                x_tmp = silk_RSHIFT(X[X_offset[b] + i + dec_subframe_offset], 3);
                sumSquared = silk_SMLABB(sumSquared, x_tmp, x_tmp);
            }
            out->subfr_energy[b][s] = (int)sumSquared;
            if (s < VAD_INTERNAL_SUBFRAMES - 1) {
                Xnrg[b] = silk_ADD_POS_SAT32(Xnrg[b], sumSquared);
            } else {
                Xnrg[b] = silk_ADD_POS_SAT32(Xnrg[b], silk_RSHIFT(sumSquared, 1));
            }
            dec_subframe_offset += dec_subframe_length;
        }
        psSilk_VAD->XnrgSubfr[b] = sumSquared;
    }

    opus_silk_vad_get_noise_levels(&Xnrg[0], psSilk_VAD);

    sumSquared = 0;
    input_tilt = 0;
    for (b = 0; b < VAD_N_BANDS; b++) {
        speech_nrg = Xnrg[b] - psSilk_VAD->NL[b];
        if (speech_nrg > 0) {
            if ((Xnrg[b] & 0xFF800000) == 0) {
                NrgToNoiseRatio_Q8[b] = silk_DIV32(silk_LSHIFT(Xnrg[b], 8), psSilk_VAD->NL[b] + 1);
            } else {
                NrgToNoiseRatio_Q8[b] = silk_DIV32(Xnrg[b], silk_RSHIFT(psSilk_VAD->NL[b], 8) + 1);
            }
            SNR_Q7 = silk_lin2log(NrgToNoiseRatio_Q8[b]) - 8 * 128;
            sumSquared = silk_SMLABB(sumSquared, SNR_Q7, SNR_Q7);
            out->snr_q7[b] = (int)SNR_Q7;
            if (speech_nrg < ((opus_int32)1 << 20)) {
                SNR_Q7 = silk_SMULWB(silk_LSHIFT(silk_SQRT_APPROX(speech_nrg), 6), SNR_Q7);
            }
            input_tilt = silk_SMLAWB(input_tilt, tiltWeights[b], SNR_Q7);
        } else {
            NrgToNoiseRatio_Q8[b] = 256;
            SNR_Q7 = 0;
            out->snr_q7[b] = 0;
        }
        out->nrg_to_noise_ratio_Q8[b] = (int)NrgToNoiseRatio_Q8[b];
        out->snr_q7_tilt[b] = (int)SNR_Q7;
    }

    sumSquared = silk_DIV32_16(sumSquared, VAD_N_BANDS);
    pSNR_dB_Q7 = (opus_int16)(3 * silk_SQRT_APPROX(sumSquared));

    SA_Q15 = silk_sigm_Q15(silk_SMULWB(VAD_SNR_FACTOR_Q16, pSNR_dB_Q7) - VAD_NEGATIVE_OFFSET_Q5);

    speech_nrg = 0;
    for (b = 0; b < VAD_N_BANDS; b++) {
        speech_nrg += (b + 1) * silk_RSHIFT(Xnrg[b] - psSilk_VAD->NL[b], 4);
    }
    out->speech_nrg_pre = (int)speech_nrg;

    if (frame_length == 20 * fs_kHz) {
        speech_nrg = silk_RSHIFT32(speech_nrg, 1);
    }
    if (speech_nrg <= 0) {
        SA_Q15 = silk_RSHIFT(SA_Q15, 1);
    } else if (speech_nrg < 16384) {
        speech_nrg = silk_LSHIFT32(speech_nrg, 16);
        speech_nrg = silk_SQRT_APPROX(speech_nrg);
        SA_Q15 = silk_SMULWB(32768 + speech_nrg, SA_Q15);
    }

    out->speech_nrg_post = (int)speech_nrg;

    smooth_coef_Q16 = silk_SMULWB(VAD_SNR_SMOOTH_COEF_Q18, silk_SMULWB((opus_int32)SA_Q15, SA_Q15));
    if (frame_length == 10 * fs_kHz) {
        smooth_coef_Q16 >>= 1;
    }

    for (b = 0; b < VAD_N_BANDS; b++) {
        psSilk_VAD->NrgRatioSmth_Q8[b] = silk_SMLAWB(psSilk_VAD->NrgRatioSmth_Q8[b],
            NrgToNoiseRatio_Q8[b] - psSilk_VAD->NrgRatioSmth_Q8[b], smooth_coef_Q16);

        SNR_Q7 = 3 * (silk_lin2log(psSilk_VAD->NrgRatioSmth_Q8[b]) - 8 * 128);
        out->snr_q7_smth[b] = (int)SNR_Q7;
        out->input_quality_bands_Q15[b] = silk_sigm_Q15(silk_RSHIFT(SNR_Q7 - 16 * 128, 4));
    }

    out->speech_activity_Q8 = silk_min_int(silk_RSHIFT(SA_Q15, 7), silk_uint8_MAX);
    out->input_tilt_Q15 = silk_LSHIFT(silk_sigm_Q15(input_tilt) - 16384, 1);
    out->input_tilt = input_tilt;
    out->pSNR_dB_Q7 = pSNR_dB_Q7;
    out->SA_Q15 = SA_Q15;
    out->sum_squared = sumSquared;
    out->smooth_coef_Q16 = smooth_coef_Q16;
    for (b = 0; b < VAD_N_BANDS; b++) {
        out->xnrg[b] = (int)Xnrg[b];
        out->xnrg_subfr[b] = (int)psSilk_VAD->XnrgSubfr[b];
        out->nrg_ratio_smth_Q8[b] = (int)psSilk_VAD->NrgRatioSmth_Q8[b];
        out->nl[b] = (int)psSilk_VAD->NL[b];
        out->inv_nl[b] = (int)psSilk_VAD->inv_NL[b];
    }

    free(X);
}

static opus_int32 opus_silk_lin2log(opus_int32 in) {
    return silk_lin2log(in);
}
*/
import "C"

import "unsafe"

// LibopusVADResult holds VAD outputs from libopus.
type LibopusVADResult struct {
	SpeechActivityQ8     int
	InputTiltQ15         int
	InputQualityBandsQ15 [4]int
	NrgRatioSmthQ8       [4]int
	NL                   [4]int
	InvNL                [4]int
}

// LibopusVADTrace holds detailed VAD intermediate values from libopus.
type LibopusVADTrace struct {
	SpeechActivityQ8     int
	InputTiltQ15         int
	InputTilt            int
	InputQualityBandsQ15 [4]int
	NrgRatioSmthQ8       [4]int
	NL                   [4]int
	InvNL                [4]int
	Xnrg                 [4]int
	XnrgSubfr            [4]int
	SubfrEnergy          [4][4]int
	NrgToNoiseRatioQ8    [4]int
	SNRQ7                [4]int
	SNRQ7Tilt            [4]int
	SNRQ7Smth            [4]int
	SumSquared           int
	PSNRdBQ7             int
	SAQ15                int
	SpeechNrgPre         int
	SpeechNrgPost        int
	SmoothCoefQ16        int
	HPState              int
}

// LibopusVADState wraps libopus VAD state.
type LibopusVADState struct {
	state C.silk_VAD_state
}

// NewLibopusVADState initializes a libopus VAD state.
func NewLibopusVADState() *LibopusVADState {
	st := &LibopusVADState{}
	C.opus_silk_vad_init(&st.state)
	return st
}

// GetSpeechActivity runs libopus VAD on the provided PCM frame.
// pcm must be int16 samples with length >= frameLength.
func (s *LibopusVADState) GetSpeechActivity(pcm []int16, frameLength, fsKHz int) LibopusVADResult {
	var res C.vad_result
	if frameLength <= 0 || fsKHz <= 0 || len(pcm) < frameLength {
		return LibopusVADResult{}
	}
	C.opus_silk_vad_get((*C.opus_int16)(unsafe.Pointer(&pcm[0])), C.int(frameLength), C.int(fsKHz), &s.state, &res)
	out := LibopusVADResult{
		SpeechActivityQ8: int(res.speech_activity_Q8),
		InputTiltQ15:     int(res.input_tilt_Q15),
	}
	for i := 0; i < 4; i++ {
		out.InputQualityBandsQ15[i] = int(res.input_quality_bands_Q15[i])
		out.NrgRatioSmthQ8[i] = int(res.nrg_ratio_smth_Q8[i])
		out.NL[i] = int(res.nl[i])
		out.InvNL[i] = int(res.inv_nl[i])
	}
	return out
}

// GetSpeechActivityTrace runs libopus VAD and returns detailed intermediate values.
func (s *LibopusVADState) GetSpeechActivityTrace(pcm []int16, frameLength, fsKHz int) LibopusVADTrace {
	var res C.vad_trace
	if frameLength <= 0 || fsKHz <= 0 || len(pcm) < frameLength {
		return LibopusVADTrace{}
	}
	C.opus_silk_vad_get_trace((*C.opus_int16)(unsafe.Pointer(&pcm[0])), C.int(frameLength), C.int(fsKHz), &s.state, &res)
	out := LibopusVADTrace{
		SpeechActivityQ8: int(res.speech_activity_Q8),
		InputTiltQ15:     int(res.input_tilt_Q15),
		InputTilt:        int(res.input_tilt),
		SumSquared:       int(res.sum_squared),
		PSNRdBQ7:         int(res.pSNR_dB_Q7),
		SAQ15:            int(res.SA_Q15),
		SpeechNrgPre:     int(res.speech_nrg_pre),
		SpeechNrgPost:    int(res.speech_nrg_post),
		SmoothCoefQ16:    int(res.smooth_coef_Q16),
		HPState:          int(res.hp_state),
	}
	for i := 0; i < 4; i++ {
		out.InputQualityBandsQ15[i] = int(res.input_quality_bands_Q15[i])
		out.NrgRatioSmthQ8[i] = int(res.nrg_ratio_smth_Q8[i])
		out.NL[i] = int(res.nl[i])
		out.InvNL[i] = int(res.inv_nl[i])
		out.Xnrg[i] = int(res.xnrg[i])
		out.XnrgSubfr[i] = int(res.xnrg_subfr[i])
		for s := 0; s < 4; s++ {
			out.SubfrEnergy[i][s] = int(res.subfr_energy[i][s])
		}
		out.NrgToNoiseRatioQ8[i] = int(res.nrg_to_noise_ratio_Q8[i])
		out.SNRQ7[i] = int(res.snr_q7[i])
		out.SNRQ7Tilt[i] = int(res.snr_q7_tilt[i])
		out.SNRQ7Smth[i] = int(res.snr_q7_smth[i])
	}
	return out
}

// LibopusLin2Log calls libopus silk_lin2log.
func LibopusLin2Log(v int32) int32 {
	return int32(C.opus_silk_lin2log(C.opus_int32(v)))
}
