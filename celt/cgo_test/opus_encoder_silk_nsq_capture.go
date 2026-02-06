//go:build cgo_libopus
// +build cgo_libopus

package cgo

/*
#cgo CFLAGS: -I${SRCDIR}/../../tmp_check/opus-1.6.1/include -I${SRCDIR}/../../tmp_check/opus-1.6.1/celt -I${SRCDIR}/../../tmp_check/opus-1.6.1 -I${SRCDIR}/../../tmp_check/opus-1.6.1/src -I${SRCDIR}/../../tmp_check/opus-1.6.1/silk -I${SRCDIR}/../../tmp_check/opus-1.6.1/silk/float -DHAVE_CONFIG_H
#cgo LDFLAGS: ${SRCDIR}/../../tmp_check/opus-1.6.1/.libs/libopus.a -lm

#include <string.h>
#include "opus.h"
#include "silk/main.h"
#include "silk/float/structs_FLP.h"

typedef struct {
    int celt_enc_offset;
    int silk_enc_offset;
    silk_EncControlStruct silk_mode;
} OpusEncoderInternalHeadNSQ;

typedef struct {
    opus_int16 xq[2 * MAX_FRAME_LENGTH];
    opus_int32 sLTP_shp_Q14[2 * MAX_FRAME_LENGTH];
    opus_int32 sLPC_Q14[MAX_SUB_FRAME_LENGTH + NSQ_LPC_BUF_LENGTH];
    opus_int32 sAR2_Q14[MAX_SHAPE_LPC_ORDER];
    opus_int32 sLF_AR_shp_Q14;
    opus_int32 sDiff_shp_Q14;
    int lagPrev;
    int sLTP_buf_idx;
    int sLTP_shp_buf_idx;
    opus_int32 rand_seed;
    opus_int32 prev_gain_Q16;
    int rewhite_flag;
} opus_silk_nsq_state_snapshot;

static void fill_opus_silk_nsq_state_snapshot(OpusEncoder *enc, opus_silk_nsq_state_snapshot *out) {
    OpusEncoderInternalHeadNSQ *st = (OpusEncoderInternalHeadNSQ *)enc;
    silk_encoder *silk_enc = (silk_encoder *)((char *)enc + st->silk_enc_offset);
    silk_encoder_state_FLP *st0 = &silk_enc->state_Fxx[0];
    silk_nsq_state *nsq = &st0->sCmn.sNSQ;

    memcpy(out->xq, nsq->xq, sizeof(out->xq));
    memcpy(out->sLTP_shp_Q14, nsq->sLTP_shp_Q14, sizeof(out->sLTP_shp_Q14));
    memcpy(out->sLPC_Q14, nsq->sLPC_Q14, sizeof(out->sLPC_Q14));
    memcpy(out->sAR2_Q14, nsq->sAR2_Q14, sizeof(out->sAR2_Q14));
    out->sLF_AR_shp_Q14 = nsq->sLF_AR_shp_Q14;
    out->sDiff_shp_Q14 = nsq->sDiff_shp_Q14;
    out->lagPrev = nsq->lagPrev;
    out->sLTP_buf_idx = nsq->sLTP_buf_idx;
    out->sLTP_shp_buf_idx = nsq->sLTP_shp_buf_idx;
    out->rand_seed = nsq->rand_seed;
    out->prev_gain_Q16 = nsq->prev_gain_Q16;
    out->rewhite_flag = nsq->rewhite_flag;
}

static int test_capture_opus_silk_nsq_state(
    const float *samples,
    int total_samples,
    int sample_rate,
    int channels,
    int bitrate,
    int frame_size,
    int frame_index,
    int capture_before,
    opus_silk_nsq_state_snapshot *out
) {
    int err = OPUS_OK;
    int i;
    unsigned char packet[1500];

    OpusEncoder *enc = opus_encoder_create(sample_rate, channels, OPUS_APPLICATION_RESTRICTED_SILK, &err);
    if (err != OPUS_OK || !enc) {
        return -1;
    }

    opus_encoder_ctl(enc, OPUS_SET_BITRATE(bitrate));
    opus_encoder_ctl(enc, OPUS_SET_BANDWIDTH(OPUS_BANDWIDTH_WIDEBAND));
    opus_encoder_ctl(enc, OPUS_SET_COMPLEXITY(10));
    opus_encoder_ctl(enc, OPUS_SET_INBAND_FEC(0));
    opus_encoder_ctl(enc, OPUS_SET_DTX(0));
    opus_encoder_ctl(enc, OPUS_SET_VBR(1));

    const int samples_per_frame = frame_size * channels;
    if (samples_per_frame <= 0) {
        opus_encoder_destroy(enc);
        return -2;
    }

    const int n_frames = total_samples / samples_per_frame;
    if (frame_index < 0 || frame_index >= n_frames) {
        opus_encoder_destroy(enc);
        return -3;
    }

    memset(out, 0, sizeof(*out));

    for (i = 0; i < n_frames; i++) {
        if (capture_before && i == frame_index) {
            fill_opus_silk_nsq_state_snapshot(enc, out);
            break;
        }

        {
            const float *frame = samples + i * samples_per_frame;
            int n = opus_encode_float(enc, frame, frame_size, packet, (opus_int32)sizeof(packet));
            if (n < 0) {
                opus_encoder_destroy(enc);
                return -4;
            }
        }

        if (!capture_before && i == frame_index) {
            fill_opus_silk_nsq_state_snapshot(enc, out);
            break;
        }
    }

    opus_encoder_destroy(enc);
    return 0;
}
*/
import "C"

import "unsafe"

const (
	opusNSQXQLen      = int(C.MAX_FRAME_LENGTH * 2)
	opusNSQSLTPShpLen = int(C.MAX_FRAME_LENGTH * 2)
	opusNSQSLPCLen    = int(C.MAX_SUB_FRAME_LENGTH + C.NSQ_LPC_BUF_LENGTH)
	opusNSQSAR2Len    = int(C.MAX_SHAPE_LPC_ORDER)
)

// OpusSilkNSQStateSnapshot captures the full top-level libopus SILK NSQ state.
type OpusSilkNSQStateSnapshot struct {
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

// CaptureOpusSilkNSQStateBeforeFrame captures top-level libopus SILK NSQ state
// immediately before encoding frameIndex.
func CaptureOpusSilkNSQStateBeforeFrame(samples []float32, sampleRate, channels, bitrate, frameSize, frameIndex int) (OpusSilkNSQStateSnapshot, bool) {
	return captureOpusSilkNSQStateAtFrame(samples, sampleRate, channels, bitrate, frameSize, frameIndex, true)
}

func captureOpusSilkNSQStateAtFrame(samples []float32, sampleRate, channels, bitrate, frameSize, frameIndex int, before bool) (OpusSilkNSQStateSnapshot, bool) {
	if len(samples) == 0 || frameSize <= 0 || channels <= 0 || frameIndex < 0 {
		return OpusSilkNSQStateSnapshot{}, false
	}
	captureBefore := C.int(0)
	if before {
		captureBefore = 1
	}
	var out C.opus_silk_nsq_state_snapshot
	ret := C.test_capture_opus_silk_nsq_state(
		(*C.float)(unsafe.Pointer(&samples[0])),
		C.int(len(samples)),
		C.int(sampleRate),
		C.int(channels),
		C.int(bitrate),
		C.int(frameSize),
		C.int(frameIndex),
		captureBefore,
		&out,
	)
	if ret != 0 {
		return OpusSilkNSQStateSnapshot{}, false
	}

	xq := make([]int16, opusNSQXQLen)
	sltp := make([]int32, opusNSQSLTPShpLen)
	slpc := make([]int32, opusNSQSLPCLen)
	sar2 := make([]int32, opusNSQSAR2Len)

	xqC := unsafe.Slice((*C.opus_int16)(unsafe.Pointer(&out.xq[0])), opusNSQXQLen)
	sltpC := unsafe.Slice((*C.opus_int32)(unsafe.Pointer(&out.sLTP_shp_Q14[0])), opusNSQSLTPShpLen)
	slpcC := unsafe.Slice((*C.opus_int32)(unsafe.Pointer(&out.sLPC_Q14[0])), opusNSQSLPCLen)
	sar2C := unsafe.Slice((*C.opus_int32)(unsafe.Pointer(&out.sAR2_Q14[0])), opusNSQSAR2Len)

	for i := 0; i < opusNSQXQLen; i++ {
		xq[i] = int16(xqC[i])
	}
	for i := 0; i < opusNSQSLTPShpLen; i++ {
		sltp[i] = int32(sltpC[i])
	}
	for i := 0; i < opusNSQSLPCLen; i++ {
		slpc[i] = int32(slpcC[i])
	}
	for i := 0; i < opusNSQSAR2Len; i++ {
		sar2[i] = int32(sar2C[i])
	}

	return OpusSilkNSQStateSnapshot{
		XQ:            xq,
		SLTPShpQ14:    sltp,
		SLPCQ14:       slpc,
		SAR2Q14:       sar2,
		LFARQ14:       int32(out.sLF_AR_shp_Q14),
		DiffQ14:       int32(out.sDiff_shp_Q14),
		LagPrev:       int(out.lagPrev),
		SLTPBufIdx:    int(out.sLTP_buf_idx),
		SLTPShpBufIdx: int(out.sLTP_shp_buf_idx),
		RandSeed:      int32(out.rand_seed),
		PrevGainQ16:   int32(out.prev_gain_Q16),
		RewhiteFlag:   int(out.rewhite_flag),
	}, true
}
