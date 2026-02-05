//go:build cgo_libopus
// +build cgo_libopus

package cgo

/*
#cgo CFLAGS: -I${SRCDIR}/../../tmp_check/opus-1.6.1/include -I${SRCDIR}/../../tmp_check/opus-1.6.1/celt -I${SRCDIR}/../../tmp_check/opus-1.6.1 -I${SRCDIR}/../../tmp_check/opus-1.6.1/src -I${SRCDIR}/../../tmp_check/opus-1.6.1/silk -I${SRCDIR}/../../tmp_check/opus-1.6.1/silk/float -DHAVE_CONFIG_H
#cgo LDFLAGS: ${SRCDIR}/../../tmp_check/opus-1.6.1/.libs/libopus.a -lm

#include <string.h>
#include <stdint.h>
#include "opus.h"
#include "silk/main.h"
#include "silk/float/structs_FLP.h"

typedef struct {
    int celt_enc_offset;
    int silk_enc_offset;
    silk_EncControlStruct silk_mode;
} OpusEncoderInternalHead;

typedef struct {
    int signal_type;
    int lag_index;
    int contour_index;
    int prev_lag;
    int prev_signal_type;
    float ltp_corr;
    int first_frame_after_reset;
    int nsq_lag_prev;
    int nsq_sltp_buf_idx;
    int nsq_sltp_shp_buf_idx;
    int nsq_prev_gain_q16;
    int nsq_rand_seed;
    int nsq_rewhite_flag;
    int ec_prev_lag_index;
    int ec_prev_signal_type;
    int silk_mode_signal_type;
    int silk_mode_internal_sample_rate;
    int silk_mode_payload_size_ms;
    int silk_mode_use_cbr;
    int silk_mode_max_bits;
    int speech_activity_q8;
    int input_tilt_q15;
    int pitch_estimation_threshold_q16;
    int n_states_delayed_decision;
    int warping_q16;
    int sum_log_gain_q7;
    int target_rate_bps;
    int snr_db_q7;
    int n_bits_exceeded;
    int gain_indices[4];
    int last_gain_index;
    unsigned long long pitch_x_buf_hash;
    int pitch_buf_len;
    unsigned long long pitch_win_hash;
    int pitch_win_len;
} opus_silk_encoder_state_snapshot;

static unsigned long long hash_float32_array(const float *data, int n) {
    const uint64_t fnv_offset = 1469598103934665603ULL;
    const uint64_t fnv_prime = 1099511628211ULL;
    uint64_t h = fnv_offset;
    int i;
    for (i = 0; i < n; i++) {
        uint32_t bits = 0;
        memcpy(&bits, &data[i], sizeof(bits));
        h ^= (uint64_t)bits;
        h *= fnv_prime;
    }
    return (unsigned long long)h;
}

static void fill_opus_silk_encoder_state_snapshot(OpusEncoder *enc, opus_silk_encoder_state_snapshot *out) {
    OpusEncoderInternalHead *st = (OpusEncoderInternalHead *)enc;
    silk_encoder *silk_enc = (silk_encoder *)((char *)enc + st->silk_enc_offset);
    silk_encoder_state_FLP *st0 = &silk_enc->state_Fxx[0];

    out->signal_type = st0->sCmn.indices.signalType;
    out->lag_index = st0->sCmn.indices.lagIndex;
    out->contour_index = st0->sCmn.indices.contourIndex;
    out->prev_lag = st0->sCmn.prevLag;
    out->prev_signal_type = st0->sCmn.prevSignalType;
    out->ltp_corr = st0->LTPCorr;
    out->first_frame_after_reset = st0->sCmn.first_frame_after_reset;

    out->nsq_lag_prev = st0->sCmn.sNSQ.lagPrev;
    out->nsq_sltp_buf_idx = st0->sCmn.sNSQ.sLTP_buf_idx;
    out->nsq_sltp_shp_buf_idx = st0->sCmn.sNSQ.sLTP_shp_buf_idx;
    out->nsq_prev_gain_q16 = st0->sCmn.sNSQ.prev_gain_Q16;
    out->nsq_rand_seed = st0->sCmn.sNSQ.rand_seed;
    out->nsq_rewhite_flag = st0->sCmn.sNSQ.rewhite_flag;

    out->ec_prev_lag_index = st0->sCmn.ec_prevLagIndex;
    out->ec_prev_signal_type = st0->sCmn.ec_prevSignalType;

    out->silk_mode_signal_type = st->silk_mode.signalType;
    out->silk_mode_internal_sample_rate = st->silk_mode.internalSampleRate;
    out->silk_mode_payload_size_ms = st->silk_mode.payloadSize_ms;
    out->silk_mode_use_cbr = st->silk_mode.useCBR;
    out->silk_mode_max_bits = st->silk_mode.maxBits;
    out->speech_activity_q8 = st0->sCmn.speech_activity_Q8;
    out->input_tilt_q15 = st0->sCmn.input_tilt_Q15;
    out->pitch_estimation_threshold_q16 = st0->sCmn.pitchEstimationThreshold_Q16;
    out->n_states_delayed_decision = st0->sCmn.nStatesDelayedDecision;
    out->warping_q16 = st0->sCmn.warping_Q16;
    out->sum_log_gain_q7 = st0->sCmn.sum_log_gain_Q7;
    out->target_rate_bps = st0->sCmn.TargetRate_bps;
    out->snr_db_q7 = st0->sCmn.SNR_dB_Q7;
    out->n_bits_exceeded = silk_enc->nBitsExceeded;
    out->gain_indices[0] = st0->sCmn.indices.GainsIndices[0];
    out->gain_indices[1] = st0->sCmn.indices.GainsIndices[1];
    out->gain_indices[2] = st0->sCmn.indices.GainsIndices[2];
    out->gain_indices[3] = st0->sCmn.indices.GainsIndices[3];
    out->last_gain_index = st0->sShape.LastGainIndex;

    {
        int buf_len = st0->sCmn.la_pitch + st0->sCmn.frame_length + st0->sCmn.ltp_mem_length;
        int max_x_buf = (int)(2 * MAX_FRAME_LENGTH + LA_SHAPE_MAX);
        if (buf_len < 0) {
            buf_len = 0;
        } else if (buf_len > max_x_buf) {
            buf_len = max_x_buf;
        }
        out->pitch_buf_len = buf_len;
        out->pitch_x_buf_hash = hash_float32_array(st0->x_buf, buf_len);

        {
            int win_len = st0->sCmn.pitch_LPC_win_length;
            if (win_len < 0) {
                win_len = 0;
            } else if (win_len > buf_len) {
                win_len = buf_len;
            }
            out->pitch_win_len = win_len;
            out->pitch_win_hash = hash_float32_array(st0->x_buf + (buf_len - win_len), win_len);
        }
    }
}

static int test_capture_opus_silk_encoder_state(
    const float *samples,
    int total_samples,
    int sample_rate,
    int channels,
    int bitrate,
    int frame_size,
    int frame_index,
    int capture_before,
    opus_silk_encoder_state_snapshot *out
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
            fill_opus_silk_encoder_state_snapshot(enc, out);
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
            fill_opus_silk_encoder_state_snapshot(enc, out);
            break;
        }
    }

    opus_encoder_destroy(enc);
    return 0;
}

static int fill_opus_silk_pitch_xbuf(
    OpusEncoder *enc,
    float *out,
    int out_len,
    int *actual_len
) {
    OpusEncoderInternalHead *st = (OpusEncoderInternalHead *)enc;
    silk_encoder *silk_enc = (silk_encoder *)((char *)enc + st->silk_enc_offset);
    silk_encoder_state_FLP *st0 = &silk_enc->state_Fxx[0];
    int buf_len = st0->sCmn.la_pitch + st0->sCmn.frame_length + st0->sCmn.ltp_mem_length;
    int max_x_buf = (int)(sizeof(st0->x_buf) / sizeof(st0->x_buf[0]));
    if (buf_len < 0) {
        buf_len = 0;
    } else if (buf_len > max_x_buf) {
        buf_len = max_x_buf;
    }
    if (actual_len) {
        *actual_len = buf_len;
    }
    if (out && out_len > 0 && buf_len > 0) {
        int n = buf_len;
        if (n > out_len) {
            n = out_len;
        }
        memcpy(out, st0->x_buf, n * sizeof(float));
    }
    return 0;
}

static int test_capture_opus_silk_pitch_xbuf(
    const float *samples,
    int total_samples,
    int sample_rate,
    int channels,
    int bitrate,
    int frame_size,
    int frame_index,
    int capture_before,
    float *out,
    int out_len,
    int *actual_len
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

    if (actual_len) {
        *actual_len = 0;
    }

    for (i = 0; i < n_frames; i++) {
        if (capture_before && i == frame_index) {
            fill_opus_silk_pitch_xbuf(enc, out, out_len, actual_len);
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
            fill_opus_silk_pitch_xbuf(enc, out, out_len, actual_len);
            break;
        }
    }

    opus_encoder_destroy(enc);
    return 0;
}
*/
import "C"

import "unsafe"

// OpusSilkEncoderStateSnapshot captures top-level Opus (restricted-silk)
// internal SILK state at a specific frame boundary.
type OpusSilkEncoderStateSnapshot struct {
	SignalType           int
	LagIndex             int
	ContourIndex         int
	PrevLag              int
	PrevSignalType       int
	LTPCorr              float32
	FirstFrameAfterReset int

	NSQLagPrev       int
	NSQSLTPBufIdx    int
	NSQSLTPShpBufIdx int
	NSQPrevGainQ16   int32
	NSQRandSeed      int32
	NSQRewhiteFlag   int

	ECPrevLagIndex    int
	ECPrevSignalType  int
	SilkModeSignal    int
	SilkInternalHz    int
	SilkPayloadSizeMs int
	SilkModeUseCBR    int
	SilkModeMaxBits   int
	SpeechActivityQ8  int
	InputTiltQ15      int
	PitchEstThresQ16  int32
	NStatesDelayedDec int
	WarpingQ16        int
	SumLogGainQ7      int32
	TargetRateBps     int
	SNRDBQ7           int
	NBitsExceeded     int
	GainIndices       [4]int8
	LastGainIndex     int
	PitchXBufHash     uint64
	PitchBufLen       int
	PitchWinHash      uint64
	PitchWinLen       int
}

// CaptureOpusSilkEncoderStateAtFrame captures internal SILK encoder state from
// libopus top-level Opus encoder after encoding the specified frame index.
func CaptureOpusSilkEncoderStateAtFrame(samples []float32, sampleRate, channels, bitrate, frameSize, frameIndex int) (OpusSilkEncoderStateSnapshot, bool) {
	return captureOpusSilkEncoderStateAtFrame(samples, sampleRate, channels, bitrate, frameSize, frameIndex, false)
}

// CaptureOpusSilkEncoderStateBeforeFrame captures internal SILK encoder state from
// libopus top-level Opus encoder before encoding the specified frame index.
func CaptureOpusSilkEncoderStateBeforeFrame(samples []float32, sampleRate, channels, bitrate, frameSize, frameIndex int) (OpusSilkEncoderStateSnapshot, bool) {
	return captureOpusSilkEncoderStateAtFrame(samples, sampleRate, channels, bitrate, frameSize, frameIndex, true)
}

func captureOpusSilkEncoderStateAtFrame(samples []float32, sampleRate, channels, bitrate, frameSize, frameIndex int, before bool) (OpusSilkEncoderStateSnapshot, bool) {
	if len(samples) == 0 || frameSize <= 0 || channels <= 0 || frameIndex < 0 {
		return OpusSilkEncoderStateSnapshot{}, false
	}
	captureBefore := C.int(0)
	if before {
		captureBefore = 1
	}
	var out C.opus_silk_encoder_state_snapshot
	ret := C.test_capture_opus_silk_encoder_state(
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
		return OpusSilkEncoderStateSnapshot{}, false
	}
	return OpusSilkEncoderStateSnapshot{
		SignalType:           int(out.signal_type),
		LagIndex:             int(out.lag_index),
		ContourIndex:         int(out.contour_index),
		PrevLag:              int(out.prev_lag),
		PrevSignalType:       int(out.prev_signal_type),
		LTPCorr:              float32(out.ltp_corr),
		FirstFrameAfterReset: int(out.first_frame_after_reset),
		NSQLagPrev:           int(out.nsq_lag_prev),
		NSQSLTPBufIdx:        int(out.nsq_sltp_buf_idx),
		NSQSLTPShpBufIdx:     int(out.nsq_sltp_shp_buf_idx),
		NSQPrevGainQ16:       int32(out.nsq_prev_gain_q16),
		NSQRandSeed:          int32(out.nsq_rand_seed),
		NSQRewhiteFlag:       int(out.nsq_rewhite_flag),
		ECPrevLagIndex:       int(out.ec_prev_lag_index),
		ECPrevSignalType:     int(out.ec_prev_signal_type),
		SilkModeSignal:       int(out.silk_mode_signal_type),
		SilkInternalHz:       int(out.silk_mode_internal_sample_rate),
		SilkPayloadSizeMs:    int(out.silk_mode_payload_size_ms),
		SilkModeUseCBR:       int(out.silk_mode_use_cbr),
		SilkModeMaxBits:      int(out.silk_mode_max_bits),
		SpeechActivityQ8:     int(out.speech_activity_q8),
		InputTiltQ15:         int(out.input_tilt_q15),
		PitchEstThresQ16:     int32(out.pitch_estimation_threshold_q16),
		NStatesDelayedDec:    int(out.n_states_delayed_decision),
		WarpingQ16:           int(out.warping_q16),
		SumLogGainQ7:         int32(out.sum_log_gain_q7),
		TargetRateBps:        int(out.target_rate_bps),
		SNRDBQ7:              int(out.snr_db_q7),
		NBitsExceeded:        int(out.n_bits_exceeded),
		GainIndices: [4]int8{
			int8(out.gain_indices[0]),
			int8(out.gain_indices[1]),
			int8(out.gain_indices[2]),
			int8(out.gain_indices[3]),
		},
		LastGainIndex: int(out.last_gain_index),
		PitchXBufHash: uint64(out.pitch_x_buf_hash),
		PitchBufLen:   int(out.pitch_buf_len),
		PitchWinHash:  uint64(out.pitch_win_hash),
		PitchWinLen:   int(out.pitch_win_len),
	}, true
}

// CaptureOpusSilkPitchXBufBeforeFrame captures the internal libopus x_buf
// immediately before encoding the specified frame index.
func CaptureOpusSilkPitchXBufBeforeFrame(samples []float32, sampleRate, channels, bitrate, frameSize, frameIndex int) ([]float32, bool) {
	return captureOpusSilkPitchXBufAtFrame(samples, sampleRate, channels, bitrate, frameSize, frameIndex, true)
}

func captureOpusSilkPitchXBufAtFrame(samples []float32, sampleRate, channels, bitrate, frameSize, frameIndex int, before bool) ([]float32, bool) {
	if len(samples) == 0 || frameSize <= 0 || channels <= 0 || frameIndex < 0 {
		return nil, false
	}
	captureBefore := C.int(0)
	if before {
		captureBefore = 1
	}

	var outLen C.int
	ret := C.test_capture_opus_silk_pitch_xbuf(
		(*C.float)(unsafe.Pointer(&samples[0])),
		C.int(len(samples)),
		C.int(sampleRate),
		C.int(channels),
		C.int(bitrate),
		C.int(frameSize),
		C.int(frameIndex),
		captureBefore,
		nil,
		0,
		&outLen,
	)
	if ret != 0 || outLen < 0 {
		return nil, false
	}
	if outLen == 0 {
		return []float32{}, true
	}
	out := make([]float32, int(outLen))
	ret = C.test_capture_opus_silk_pitch_xbuf(
		(*C.float)(unsafe.Pointer(&samples[0])),
		C.int(len(samples)),
		C.int(sampleRate),
		C.int(channels),
		C.int(bitrate),
		C.int(frameSize),
		C.int(frameIndex),
		captureBefore,
		(*C.float)(unsafe.Pointer(&out[0])),
		outLen,
		&outLen,
	)
	if ret != 0 {
		return nil, false
	}
	return out, true
}
