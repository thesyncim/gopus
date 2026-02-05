//go:build cgo_libopus
// +build cgo_libopus

// Package cgo provides CGO helpers to capture libopus SILK encoder NSQ state.
package cgo

/*
#cgo CFLAGS: -I${SRCDIR}/../../tmp_check/opus-1.6.1/include -I${SRCDIR}/../../tmp_check/opus-1.6.1/celt -I${SRCDIR}/../../tmp_check/opus-1.6.1 -I${SRCDIR}/../../tmp_check/opus-1.6.1/silk -DHAVE_CONFIG_H
#cgo LDFLAGS: ${SRCDIR}/../../tmp_check/opus-1.6.1/.libs/libopus.a -lm

#include <stdlib.h>
#include <string.h>
#include "silk/API.h"
#include "silk/main.h"
#include "silk/float/structs_FLP.h"
#include "entenc.h"

static int test_silk_capture_nsq_state(
    const opus_res *samples,
    int num_samples,
    int sample_rate,
    int bitrate,
    int frame_size,
    int frame_index,
    opus_int16 *out_xq,
    int out_xq_len,
    opus_int32 *out_sltp_shp,
    int out_sltp_shp_len,
    opus_int32 *out_slpc,
    int out_slpc_len,
    opus_int32 *out_sar2,
    int out_sar2_len,
    opus_int32 *out_lf_ar,
    opus_int32 *out_diff,
    int *out_lag_prev,
    int *out_sltp_buf_idx,
    int *out_sltp_shp_buf_idx,
    opus_int32 *out_rand_seed,
    opus_int32 *out_prev_gain_q16,
    int *out_rewhite_flag
) {
    opus_int encSizeBytes = 0;
    silk_EncControlStruct encCtrl;
    void *encState = NULL;
    int ret = 0;
    int i;
    ec_enc rangeEnc;
    unsigned char rangeBuf[2048];
    opus_int32 nBytesOut;

    if (silk_Get_Encoder_Size(&encSizeBytes, 1) != 0 || encSizeBytes <= 0) {
        return -1;
    }
    encState = malloc((size_t)encSizeBytes);
    if (!encState) {
        return -1;
    }
    memset(encState, 0, (size_t)encSizeBytes);

    if (silk_InitEncoder(encState, 1, 0, &encCtrl) != 0) {
        free(encState);
        return -1;
    }

    memset(&encCtrl, 0, sizeof(encCtrl));
    encCtrl.nChannelsAPI = 1;
    encCtrl.nChannelsInternal = 1;
    encCtrl.API_sampleRate = sample_rate;
    encCtrl.maxInternalSampleRate = 16000;
    encCtrl.minInternalSampleRate = 16000;
    encCtrl.desiredInternalSampleRate = 16000;
    encCtrl.payloadSize_ms = 20;
    encCtrl.bitRate = bitrate;
    encCtrl.packetLossPercentage = 0;
    encCtrl.complexity = 10;
    encCtrl.useInBandFEC = 0;
    encCtrl.useDRED = 0;
    encCtrl.LBRR_coded = 0;
    encCtrl.useDTX = 0;
    encCtrl.useCBR = 0;
    encCtrl.maxBits = 0;
    encCtrl.toMono = 0;
    encCtrl.opusCanSwitch = 1;
    encCtrl.reducedDependency = 0;

    for (i = 0; i < num_samples; i += frame_size) {
        int frame = i / frame_size;
        silk_encoder *psEnc;
        silk_encoder_state_FLP *st;
        silk_nsq_state *nsq;
        int n;
        if (i + frame_size > num_samples) {
            break;
        }

        // Capture the encoder NSQ state BEFORE encoding frame_index.
        // This matches Go NSQ trace snapshots, which are captured pre-NSQ.
        if (frame == frame_index) {
            psEnc = (silk_encoder *)encState;
            st = &psEnc->state_Fxx[0];
            nsq = &st->sCmn.sNSQ;
            if (out_xq && out_xq_len > 0) {
                n = out_xq_len < (int)(sizeof(nsq->xq)/sizeof(nsq->xq[0])) ? out_xq_len : (int)(sizeof(nsq->xq)/sizeof(nsq->xq[0]));
                memcpy(out_xq, nsq->xq, n * sizeof(opus_int16));
            }
            if (out_sltp_shp && out_sltp_shp_len > 0) {
                n = out_sltp_shp_len < (int)(sizeof(nsq->sLTP_shp_Q14)/sizeof(nsq->sLTP_shp_Q14[0])) ? out_sltp_shp_len : (int)(sizeof(nsq->sLTP_shp_Q14)/sizeof(nsq->sLTP_shp_Q14[0]));
                memcpy(out_sltp_shp, nsq->sLTP_shp_Q14, n * sizeof(opus_int32));
            }
            if (out_slpc && out_slpc_len > 0) {
                n = out_slpc_len < (int)(sizeof(nsq->sLPC_Q14)/sizeof(nsq->sLPC_Q14[0])) ? out_slpc_len : (int)(sizeof(nsq->sLPC_Q14)/sizeof(nsq->sLPC_Q14[0]));
                memcpy(out_slpc, nsq->sLPC_Q14, n * sizeof(opus_int32));
            }
            if (out_sar2 && out_sar2_len > 0) {
                n = out_sar2_len < (int)(sizeof(nsq->sAR2_Q14)/sizeof(nsq->sAR2_Q14[0])) ? out_sar2_len : (int)(sizeof(nsq->sAR2_Q14)/sizeof(nsq->sAR2_Q14[0]));
                memcpy(out_sar2, nsq->sAR2_Q14, n * sizeof(opus_int32));
            }
            if (out_lf_ar) {
                *out_lf_ar = nsq->sLF_AR_shp_Q14;
            }
            if (out_diff) {
                *out_diff = nsq->sDiff_shp_Q14;
            }
            if (out_lag_prev) {
                *out_lag_prev = nsq->lagPrev;
            }
            if (out_sltp_buf_idx) {
                *out_sltp_buf_idx = nsq->sLTP_buf_idx;
            }
            if (out_sltp_shp_buf_idx) {
                *out_sltp_shp_buf_idx = nsq->sLTP_shp_buf_idx;
            }
            if (out_rand_seed) {
                *out_rand_seed = nsq->rand_seed;
            }
            if (out_prev_gain_q16) {
                *out_prev_gain_q16 = nsq->prev_gain_Q16;
            }
            if (out_rewhite_flag) {
                *out_rewhite_flag = nsq->rewhite_flag;
            }
            break;
        }

        ec_enc_init(&rangeEnc, rangeBuf, sizeof(rangeBuf));
        nBytesOut = (opus_int32)sizeof(rangeBuf);
        ret = silk_Encode(encState, &encCtrl, samples + i, frame_size, &rangeEnc, &nBytesOut, 0, 0);
        if (ret != 0) {
            free(encState);
            return -1;
        }
    }

    free(encState);
    return 0;
}
*/
import "C"

import "unsafe"

type SilkNSQStateSnapshot struct {
	XQ           []int16
	SLTPShpQ14   []int32
	SLPCQ14      []int32
	SAR2Q14      []int32
	LFARQ14      int32
	DiffQ14      int32
	LagPrev      int
	SLTPBufIdx   int
	SLTPShpBufIdx int
	RandSeed     int32
	PrevGainQ16  int32
	RewhiteFlag  int
}

// SilkCaptureNSQStateAtFrame captures libopus NSQ state immediately before
// encoding frameIndex.
func SilkCaptureNSQStateAtFrame(samples []float32, sampleRate, bitrate, frameSize, frameIndex int) (SilkNSQStateSnapshot, bool) {
	if len(samples) < frameSize || frameIndex < 0 {
		return SilkNSQStateSnapshot{}, false
	}
	xq := make([]int16, 2*320)
	sltp := make([]int32, 2*320)
	slpc := make([]int32, 80+16)
	sar2 := make([]int32, 24)
	var lfAr C.opus_int32
	var diff C.opus_int32
	var lagPrev C.int
	var sltpBufIdx C.int
	var sltpShpBufIdx C.int
	var randSeed C.opus_int32
	var prevGain C.opus_int32
	var rewhite C.int

	ret := C.test_silk_capture_nsq_state(
		(*C.opus_res)(unsafe.Pointer(&samples[0])),
		C.int(len(samples)),
		C.int(sampleRate),
		C.int(bitrate),
		C.int(frameSize),
		C.int(frameIndex),
		(*C.opus_int16)(unsafe.Pointer(&xq[0])),
		C.int(len(xq)),
		(*C.opus_int32)(unsafe.Pointer(&sltp[0])),
		C.int(len(sltp)),
		(*C.opus_int32)(unsafe.Pointer(&slpc[0])),
		C.int(len(slpc)),
		(*C.opus_int32)(unsafe.Pointer(&sar2[0])),
		C.int(len(sar2)),
		&lfAr,
		&diff,
		&lagPrev,
		&sltpBufIdx,
		&sltpShpBufIdx,
		&randSeed,
		&prevGain,
		&rewhite,
	)
	if ret != 0 {
		return SilkNSQStateSnapshot{}, false
	}
	return SilkNSQStateSnapshot{
		XQ:            xq,
		SLTPShpQ14:    sltp,
		SLPCQ14:       slpc,
		SAR2Q14:       sar2,
		LFARQ14:       int32(lfAr),
		DiffQ14:       int32(diff),
		LagPrev:       int(lagPrev),
		SLTPBufIdx:    int(sltpBufIdx),
		SLTPShpBufIdx: int(sltpShpBufIdx),
		RandSeed:      int32(randSeed),
		PrevGainQ16:   int32(prevGain),
		RewhiteFlag:   int(rewhite),
	}, true
}
