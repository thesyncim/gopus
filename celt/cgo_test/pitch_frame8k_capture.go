//go:build cgo_libopus
// +build cgo_libopus

package cgo

/*
#cgo CFLAGS: -I${SRCDIR}/../../tmp_check/opus-1.6.1/include -I${SRCDIR}/../../tmp_check/opus-1.6.1/celt -I${SRCDIR}/../../tmp_check/opus-1.6.1 -I${SRCDIR}/../../tmp_check/opus-1.6.1/silk -DHAVE_CONFIG_H
#cgo LDFLAGS: ${SRCDIR}/../../tmp_check/opus-1.6.1/.libs/libopus.a -lm

#include "silk/main.h"
#include "silk/define.h"
#include "silk/structs.h"
#include "silk/float/SigProc_FLP.h"
#include "silk/resampler_private.h"
#include "silk/pitch_est_defines.h"

static void opus_silk_pitch_frame8k(const float *frame, int fs_kHz, int nb_subfr, float *out_8k, int out_len) {
    int frame_length = ( PE_LTP_MEM_LENGTH_MS + nb_subfr * PE_SUBFR_LENGTH_MS ) * fs_kHz;
    int frame_length_8kHz = ( PE_LTP_MEM_LENGTH_MS + nb_subfr * PE_SUBFR_LENGTH_MS ) * 8;
    if (out_len < frame_length_8kHz) {
        return;
    }

    opus_int16 frame_8_FIX[ PE_MAX_FRAME_LENGTH_MS * 8 ];
    opus_int16 frame_fix[ 16 * PE_MAX_FRAME_LENGTH_MS ];
    opus_int32 filt_state[ 6 ];

    if (fs_kHz == 16) {
        silk_float2short_array(frame_fix, frame, frame_length);
        silk_memset(filt_state, 0, 2 * sizeof(opus_int32));
        silk_resampler_down2(filt_state, frame_8_FIX, frame_fix, frame_length);
        silk_short2float_array(out_8k, frame_8_FIX, frame_length_8kHz);
    } else if (fs_kHz == 12) {
        silk_float2short_array(frame_fix, frame, frame_length);
        silk_memset(filt_state, 0, 6 * sizeof(opus_int32));
        silk_resampler_down2_3(filt_state, frame_8_FIX, frame_fix, frame_length);
        silk_short2float_array(out_8k, frame_8_FIX, frame_length_8kHz);
    } else {
        silk_float2short_array(frame_8_FIX, frame, frame_length_8kHz);
        silk_short2float_array(out_8k, frame_8_FIX, frame_length_8kHz);
    }
}
*/
import "C"

import "unsafe"

// SilkPitchFrame8k returns the libopus frame_8kHz buffer used in pitch analysis.
func SilkPitchFrame8k(frame []float32, fsKHz, nbSubfr int) []float32 {
	if len(frame) == 0 {
		return nil
	}
	frameLen8k := (20 + nbSubfr*5) * 8
	out := make([]float32, frameLen8k)
	C.opus_silk_pitch_frame8k(
		(*C.float)(unsafe.Pointer(&frame[0])),
		C.int(fsKHz),
		C.int(nbSubfr),
		(*C.float)(unsafe.Pointer(&out[0])),
		C.int(len(out)),
	)
	return out
}
