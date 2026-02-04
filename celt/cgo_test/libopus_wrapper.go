//go:build cgo_libopus
// +build cgo_libopus

// Package cgo provides CGO wrappers for libopus comparison tests.
// This is in a separate package to enable CGO in tests.
package cgo

/*
#cgo CFLAGS: -I${SRCDIR}/../../tmp_check/opus-1.6.1/include -I${SRCDIR}/../../tmp_check/opus-1.6.1/celt -I${SRCDIR}/../../tmp_check/opus-1.6.1 -DHAVE_CONFIG_H
#cgo LDFLAGS: ${SRCDIR}/../../tmp_check/opus-1.6.1/.libs/libopus.a -lm

#include <stdlib.h>
#include <string.h>
#include <math.h>
#include <stdio.h>
#include "opus.h"
#include "celt.h"
#include "entdec.h"
#include "entenc.h"
#include "laplace.h"
#include "silk/main.h"
#include "silk/structs.h"
#include "silk/Inlines.h"
#include "silk/macros.h"

// Toggle libopus debug range tracing (prints to stderr when enabled)
extern int opus_debug_range;
void opus_set_debug_range(int v) {
    opus_debug_range = v;
}

// Forward declaration for packing range encoder output.
static int pack_ec_enc(ec_enc *enc);

// Flush all stdio streams (useful for trace capture).
void opus_flush_stdio(void) {
    fflush(NULL);
}

// Mirror of OpusDecoder structure from opus_decoder.c
// Based on https://github.com/xiph/opus/blob/main/src/opus_decoder.c
typedef struct {
    int celt_dec_offset;
    int silk_dec_offset;
    int channels;
    opus_int32 Fs;
    silk_DecControlStruct DecControl;
    int decode_gain;
    int complexity;
    int ignore_extensions;
    int arch;
    // OPUS_DECODER_RESET_START - everything after here gets cleared on reset
    int stream_channels;
    int bandwidth;
    int mode;
    int prev_mode;
    int frame_size;
    int prev_redundancy;
    int last_packet_duration;
    // Note: softclip_mem[2] exists only in floating point builds
    opus_uint32 rangeFinal;
} OpusDecoderInternal;

// Get prev_redundancy state from libopus decoder
int test_get_prev_redundancy(OpusDecoder* dec) {
    if (dec == NULL) return -1;
    OpusDecoderInternal *st = (OpusDecoderInternal*)dec;
    return st->prev_redundancy;
}

// Get prev_mode state from libopus decoder
int test_get_prev_mode(OpusDecoder* dec) {
    if (dec == NULL) return -1;
    OpusDecoderInternal *st = (OpusDecoderInternal*)dec;
    return st->prev_mode;
}

// Mirror of CELTDecoder structure from celt_decoder.c
// This must match the layout used by the compiled libopus
typedef struct {
    const void *mode;
    int overlap;
    int channels;
    int stream_channels;
    int downsample;
    int start, end;
    int signalling;
    int disable_inv;
    int complexity;
    int arch;
    // #ifdef ENABLE_QEXT: int qext_scale; - NOT ENABLED in our build
    opus_uint32 rng;
    int error;
    int last_pitch_index;
    int loss_duration;
    int plc_duration;
    int last_frame_type;
    int skip_plc;
    int postfilter_period;
    int postfilter_period_old;
    opus_val16 postfilter_gain;
    opus_val16 postfilter_gain_old;
    int postfilter_tapset;
    int postfilter_tapset_old;
    int prefilter_and_fold;
    celt_sig preemph_memD[2];
    // ... followed by _decode_mem and other dynamic arrays
} CELTDecoderInternal;

// Get CELT decoder preemphasis memory state
void test_get_preemph_state(OpusDecoder* dec, float *mem0, float *mem1) {
    if (dec == NULL) {
        *mem0 = 0;
        *mem1 = 0;
        return;
    }
    OpusDecoderInternal *st = (OpusDecoderInternal*)dec;
    CELTDecoderInternal *celt_dec = (CELTDecoderInternal*)((char*)dec + st->celt_dec_offset);
    *mem0 = (float)celt_dec->preemph_memD[0];
    *mem1 = (float)celt_dec->preemph_memD[1];
}

// Get CELT decoder overlap (to verify structure alignment)
int test_get_celt_overlap(OpusDecoder* dec) {
    if (dec == NULL) return -1;
    OpusDecoderInternal *st = (OpusDecoderInternal*)dec;
    CELTDecoderInternal *celt_dec = (CELTDecoderInternal*)((char*)dec + st->celt_dec_offset);
    return celt_dec->overlap;
}

// Get CELT decoder channels (to verify structure alignment)
int test_get_celt_channels(OpusDecoder* dec) {
    if (dec == NULL) return -1;
    OpusDecoderInternal *st = (OpusDecoderInternal*)dec;
    CELTDecoderInternal *celt_dec = (CELTDecoderInternal*)((char*)dec + st->celt_dec_offset);
    return celt_dec->channels;
}

// ============================================================================
// End internal state access
// ============================================================================

// Test harness to decode a Laplace symbol
int test_laplace_decode(const unsigned char *data, int data_len, int fs, int decay, int *out_val) {
    ec_dec dec;
    ec_dec_init(&dec, (unsigned char*)data, data_len);
    *out_val = ec_laplace_decode(&dec, fs, decay);
    return 0;
}

// Test harness to encode a Laplace symbol and return the bytes
// Returns the output length, or -1 on error
int test_laplace_encode(unsigned char *out_buf, int max_size, int val, unsigned int fs, int decay, int *out_val, int *out_len) {
    ec_enc enc;
    ec_enc_init(&enc, out_buf, max_size);
    int in_val = val;
    ec_laplace_encode(&enc, &in_val, fs, decay);
    *out_val = in_val;  // Return the possibly-clamped value
    ec_enc_done(&enc);
    *out_len = pack_ec_enc(&enc);
    return enc.error;
}

// Test harness to encode multiple Laplace symbols (for coarse energy)
int test_laplace_encode_sequence(unsigned char *out_buf, int max_size,
                                  int *vals, unsigned int *fs_arr, int *decay_arr,
                                  int count, int *out_vals, int *out_len) {
    ec_enc enc;
    ec_enc_init(&enc, out_buf, max_size);

    for (int i = 0; i < count; i++) {
        int v = vals[i];
        ec_laplace_encode(&enc, &v, fs_arr[i], decay_arr[i]);
        out_vals[i] = v;
    }

    ec_enc_done(&enc);
    *out_len = pack_ec_enc(&enc);
    return enc.error;
}

// Get range coder state after init
void test_get_range_state(const unsigned char *data, int data_len, unsigned int *out_rng, unsigned int *out_val) {
    ec_dec dec;
    ec_dec_init(&dec, (unsigned char*)data, data_len);
    *out_rng = dec.rng;
    *out_val = dec.val;
}

// Decode a bit with logp probability
int test_decode_bit_logp(const unsigned char *data, int data_len, int logp) {
    ec_dec dec;
    ec_dec_init(&dec, (unsigned char*)data, data_len);
    return ec_dec_bit_logp(&dec, logp);
}

// Decode using ICDF
int test_decode_icdf(const unsigned char *data, int data_len, const unsigned char *icdf, int ftb) {
    ec_dec dec;
    ec_dec_init(&dec, (unsigned char*)data, data_len);
    return ec_dec_icdf(&dec, icdf, ftb);
}

// Decode NLSF from indices using libopus (NB/MB or WB codebook)
void test_silk_nlsf_decode(const opus_int8 *indices, int use_wb, opus_int16 *out) {
    const silk_NLSF_CB_struct *cb = use_wb ? &silk_NLSF_CB_WB : &silk_NLSF_CB_NB_MB;
    silk_NLSF_decode(out, (opus_int8*)indices, cb);
}

// Convert NLSF to LPC coefficients using libopus
void test_silk_nlsf2a(const opus_int16 *nlsf, int order, opus_int16 *out) {
    silk_NLSF2A(out, nlsf, order, 0);
}

// Decode SILK core with provided state/control and return output
void test_silk_decode_core(
    int fs_kHz, int nb_subfr, int frame_length, int subfr_length, int ltp_mem_length, int lpc_order,
    opus_int32 prev_gain_Q16, int lossCnt, int prevSignalType,
    opus_int8 signalType, opus_int8 quantOffsetType, opus_int8 NLSFInterpCoef_Q2, opus_int8 Seed,
    const opus_int16 *outBuf, const opus_int32 *sLPC_Q14_buf,
    const opus_int32 *gains_Q16, const opus_int16 *predCoef_Q12, const opus_int16 *ltpCoef_Q14,
    const int *pitchL, int LTP_scale_Q14,
    const opus_int16 *pulses, opus_int16 *out)
{
    silk_decoder_state st;
    silk_decoder_control ctrl;
    memset(&st, 0, sizeof(st));
    memset(&ctrl, 0, sizeof(ctrl));

    st.fs_kHz = fs_kHz;
    st.nb_subfr = nb_subfr;
    st.frame_length = frame_length;
    st.subfr_length = subfr_length;
    st.ltp_mem_length = ltp_mem_length;
    st.LPC_order = lpc_order;
    st.prev_gain_Q16 = prev_gain_Q16;
    st.lossCnt = lossCnt;
    st.prevSignalType = prevSignalType;

    st.indices.signalType = signalType;
    st.indices.quantOffsetType = quantOffsetType;
    st.indices.NLSFInterpCoef_Q2 = NLSFInterpCoef_Q2;
    st.indices.Seed = Seed;

    memcpy(st.outBuf, outBuf, sizeof(st.outBuf));
    memcpy(st.sLPC_Q14_buf, sLPC_Q14_buf, sizeof(st.sLPC_Q14_buf));

    memcpy(ctrl.Gains_Q16, gains_Q16, sizeof(ctrl.Gains_Q16));
    memcpy(ctrl.PredCoef_Q12, predCoef_Q12, sizeof(ctrl.PredCoef_Q12));
    memcpy(ctrl.LTPCoef_Q14, ltpCoef_Q14, sizeof(ctrl.LTPCoef_Q14));
    memcpy(ctrl.pitchL, pitchL, sizeof(ctrl.pitchL));
    ctrl.LTP_scale_Q14 = LTP_scale_Q14;

    silk_decode_core(&st, &ctrl, out, pulses, 0);
}

// Decode SILK indices and pulses for a specific frame in a packet.
// Returns 0 on success, -1 on failure (e.g., bad frame index).
int test_silk_decode_indices_pulses(
    const unsigned char *data, int data_len,
    int fs_kHz, int nb_subfr, int frames_per_packet, int frame_index,
    opus_int8 *out_gains, opus_int8 *out_ltp, opus_int8 *out_nlsf,
    opus_int16 *out_lag, opus_int8 *out_contour, opus_int8 *out_signalType,
    opus_int8 *out_quantOffset, opus_int8 *out_nlsfInterp, opus_int8 *out_perIndex,
    opus_int8 *out_ltpScale, opus_int8 *out_seed,
    opus_int16 *out_pulses, int out_pulses_len)
{
    if (frame_index < 0 || frame_index >= frames_per_packet) {
        return -1;
    }

    ec_dec dec;
    silk_decoder_state st;
    opus_int16 pulses_buf[MAX_FRAME_LENGTH + SHELL_CODEC_FRAME_LENGTH];
    int i;

    ec_dec_init(&dec, (unsigned char*)data, data_len);
    silk_init_decoder(&st);
    st.nb_subfr = nb_subfr;
    st.nFramesPerPacket = frames_per_packet;
    silk_decoder_set_fs(&st, fs_kHz, fs_kHz * 1000);

    // Decode VAD flags
    for (i = 0; i < frames_per_packet; i++) {
        st.VAD_flags[i] = ec_dec_bit_logp(&dec, 1);
    }
    // Decode LBRR flags
    st.LBRR_flag = ec_dec_bit_logp(&dec, 1);
    silk_memset(st.LBRR_flags, 0, sizeof(st.LBRR_flags));
    if (st.LBRR_flag) {
        if (frames_per_packet == 1) {
            st.LBRR_flags[0] = 1;
        } else {
            opus_int32 LBRR_symbol = ec_dec_icdf(&dec, silk_LBRR_flags_iCDF_ptr[frames_per_packet - 2], 8) + 1;
            for (i = 0; i < frames_per_packet; i++) {
                st.LBRR_flags[i] = (LBRR_symbol >> i) & 1;
            }
        }
    }

    // Decode LBRR payload to advance range decoder
    if (st.LBRR_flag) {
        for (i = 0; i < frames_per_packet; i++) {
            if (st.LBRR_flags[i] == 0) {
                continue;
            }
            int condCoding = CODE_INDEPENDENTLY;
            if (i > 0 && st.LBRR_flags[i - 1]) {
                condCoding = CODE_CONDITIONALLY;
            }
            silk_decode_indices(&st, &dec, i, 1, condCoding);
            int pulses_len = (st.frame_length + SHELL_CODEC_FRAME_LENGTH - 1) & ~(SHELL_CODEC_FRAME_LENGTH - 1);
            if (pulses_len > (int)(sizeof(pulses_buf)/sizeof(pulses_buf[0]))) {
                return -1;
            }
            silk_decode_pulses(&dec, pulses_buf, st.indices.signalType, st.indices.quantOffsetType, st.frame_length);
        }
    }

    // Decode normal frames
    for (i = 0; i < frames_per_packet; i++) {
        int condCoding = (i == 0) ? CODE_INDEPENDENTLY : CODE_CONDITIONALLY;
        silk_decode_indices(&st, &dec, i, 0, condCoding);
        int pulses_len = (st.frame_length + SHELL_CODEC_FRAME_LENGTH - 1) & ~(SHELL_CODEC_FRAME_LENGTH - 1);
        if (pulses_len > (int)(sizeof(pulses_buf)/sizeof(pulses_buf[0]))) {
            return -1;
        }
        silk_decode_pulses(&dec, pulses_buf, st.indices.signalType, st.indices.quantOffsetType, st.frame_length);

        if (i == frame_index) {
            // Copy indices
            memcpy(out_gains, st.indices.GainsIndices, MAX_NB_SUBFR * sizeof(opus_int8));
            memcpy(out_ltp, st.indices.LTPIndex, MAX_NB_SUBFR * sizeof(opus_int8));
            memcpy(out_nlsf, st.indices.NLSFIndices, (MAX_LPC_ORDER + 1) * sizeof(opus_int8));
            *out_lag = st.indices.lagIndex;
            *out_contour = st.indices.contourIndex;
            *out_signalType = st.indices.signalType;
            *out_quantOffset = st.indices.quantOffsetType;
            *out_nlsfInterp = st.indices.NLSFInterpCoef_Q2;
            *out_perIndex = st.indices.PERIndex;
            *out_ltpScale = st.indices.LTP_scaleIndex;
            *out_seed = st.indices.Seed;

            // Copy pulses (frame_length samples)
            if (out_pulses_len < st.frame_length) {
                return -1;
            }
            memcpy(out_pulses, pulses_buf, st.frame_length * sizeof(opus_int16));
            return 0;
        }
    }
    return -1;
}

// Decode SILK LBRR indices and pulses for a specific frame in a packet.
// Returns 0 on success, -1 on failure (e.g., bad frame index or no LBRR).
int test_silk_decode_lbrr_indices_pulses(
    const unsigned char *data, int data_len,
    int fs_kHz, int nb_subfr, int frames_per_packet, int frame_index,
    opus_int8 *out_gains, opus_int8 *out_ltp, opus_int8 *out_nlsf,
    opus_int16 *out_lag, opus_int8 *out_contour, opus_int8 *out_signalType,
    opus_int8 *out_quantOffset, opus_int8 *out_nlsfInterp, opus_int8 *out_perIndex,
    opus_int8 *out_ltpScale, opus_int8 *out_seed,
    opus_int16 *out_pulses, int out_pulses_len)
{
    if (frame_index < 0 || frame_index >= frames_per_packet) {
        return -1;
    }

    ec_dec dec;
    silk_decoder_state st;
    opus_int16 pulses_buf[MAX_FRAME_LENGTH + SHELL_CODEC_FRAME_LENGTH];
    int i;

    ec_dec_init(&dec, (unsigned char*)data, data_len);
    silk_init_decoder(&st);
    st.nb_subfr = nb_subfr;
    st.nFramesPerPacket = frames_per_packet;
    silk_decoder_set_fs(&st, fs_kHz, fs_kHz * 1000);

    // Decode VAD flags
    for (i = 0; i < frames_per_packet; i++) {
        st.VAD_flags[i] = ec_dec_bit_logp(&dec, 1);
    }
    // Decode LBRR flags
    st.LBRR_flag = ec_dec_bit_logp(&dec, 1);
    silk_memset(st.LBRR_flags, 0, sizeof(st.LBRR_flags));
    if (!st.LBRR_flag) {
        return -1;
    }
    if (frames_per_packet == 1) {
        st.LBRR_flags[0] = 1;
    } else {
        opus_int32 LBRR_symbol = ec_dec_icdf(&dec, silk_LBRR_flags_iCDF_ptr[frames_per_packet - 2], 8) + 1;
        for (i = 0; i < frames_per_packet; i++) {
            st.LBRR_flags[i] = (LBRR_symbol >> i) & 1;
        }
    }

    // Decode LBRR payload and return requested frame
    for (i = 0; i < frames_per_packet; i++) {
        if (st.LBRR_flags[i] == 0) {
            continue;
        }
        int condCoding = CODE_INDEPENDENTLY;
        if (i > 0 && st.LBRR_flags[i - 1]) {
            condCoding = CODE_CONDITIONALLY;
        }
        silk_decode_indices(&st, &dec, i, 1, condCoding);
        int pulses_len = (st.frame_length + SHELL_CODEC_FRAME_LENGTH - 1) & ~(SHELL_CODEC_FRAME_LENGTH - 1);
        if (pulses_len > (int)(sizeof(pulses_buf)/sizeof(pulses_buf[0]))) {
            return -1;
        }
        silk_decode_pulses(&dec, pulses_buf, st.indices.signalType, st.indices.quantOffsetType, st.frame_length);

        if (i == frame_index) {
            memcpy(out_gains, st.indices.GainsIndices, MAX_NB_SUBFR * sizeof(opus_int8));
            memcpy(out_ltp, st.indices.LTPIndex, MAX_NB_SUBFR * sizeof(opus_int8));
            memcpy(out_nlsf, st.indices.NLSFIndices, (MAX_LPC_ORDER + 1) * sizeof(opus_int8));
            *out_lag = st.indices.lagIndex;
            *out_contour = st.indices.contourIndex;
            *out_signalType = st.indices.signalType;
            *out_quantOffset = st.indices.quantOffsetType;
            *out_nlsfInterp = st.indices.NLSFInterpCoef_Q2;
            *out_perIndex = st.indices.PERIndex;
            *out_ltpScale = st.indices.LTP_scaleIndex;
            *out_seed = st.indices.Seed;

            if (out_pulses_len < st.frame_length) {
                return -1;
            }
            memcpy(out_pulses, pulses_buf, st.frame_length * sizeof(opus_int16));
            return 0;
        }
    }
    return -1;
}

// Decode SILK pulses from a payload that contains only pulse data.
// Returns 0 on success, -1 on failure.
int test_silk_decode_pulses_only(
    const unsigned char *data, int data_len,
    int signalType, int quantOffsetType, int frame_length,
    opus_int16 *out_pulses, int out_pulses_len)
{
    if (data_len <= 0 || frame_length <= 0) {
        return -1;
    }
    if (out_pulses_len < frame_length) {
        return -1;
    }
    ec_dec dec;
    ec_dec_init(&dec, (unsigned char*)data, data_len);
    silk_decode_pulses(&dec, out_pulses, signalType, quantOffsetType, frame_length);
    return 0;
}

// Decode LBRR indices and return ec_tell() bit count after indices for a frame.
// Returns 0 on success, -1 on failure.
int test_silk_decode_lbrr_indices_bits(
    const unsigned char *data, int data_len,
    int fs_kHz, int nb_subfr, int frames_per_packet, int frame_index,
    int *out_bits)
{
    if (frame_index < 0 || frame_index >= frames_per_packet) {
        return -1;
    }
    ec_dec dec;
    silk_decoder_state st;
    int i;

    ec_dec_init(&dec, (unsigned char*)data, data_len);
    silk_init_decoder(&st);
    st.nb_subfr = nb_subfr;
    st.nFramesPerPacket = frames_per_packet;
    silk_decoder_set_fs(&st, fs_kHz, fs_kHz * 1000);

    for (i = 0; i < frames_per_packet; i++) {
        st.VAD_flags[i] = ec_dec_bit_logp(&dec, 1);
    }
    st.LBRR_flag = ec_dec_bit_logp(&dec, 1);
    silk_memset(st.LBRR_flags, 0, sizeof(st.LBRR_flags));
    if (!st.LBRR_flag) {
        return -1;
    }
    if (frames_per_packet == 1) {
        st.LBRR_flags[0] = 1;
    } else {
        opus_int32 LBRR_symbol = ec_dec_icdf(&dec, silk_LBRR_flags_iCDF_ptr[frames_per_packet - 2], 8) + 1;
        for (i = 0; i < frames_per_packet; i++) {
            st.LBRR_flags[i] = (LBRR_symbol >> i) & 1;
        }
    }

    for (i = 0; i < frames_per_packet; i++) {
        if (st.LBRR_flags[i] == 0) {
            continue;
        }
        int condCoding = CODE_INDEPENDENTLY;
        if (i > 0 && st.LBRR_flags[i - 1]) {
            condCoding = CODE_CONDITIONALLY;
        }
        silk_decode_indices(&st, &dec, i, 1, condCoding);
        if (i == frame_index) {
            if (out_bits) {
                *out_bits = ec_tell(&dec);
            }
            return 0;
        }
    }
    return -1;
}

// Decode LBRR indices and return ec_dec rng/val state after indices for a frame.
// Returns 0 on success, -1 on failure.
int test_silk_decode_lbrr_indices_state(
    const unsigned char *data, int data_len,
    int fs_kHz, int nb_subfr, int frames_per_packet, int frame_index,
    opus_uint32 *out_rng, opus_uint32 *out_val)
{
    if (frame_index < 0 || frame_index >= frames_per_packet) {
        return -1;
    }
    ec_dec dec;
    silk_decoder_state st;
    int i;

    ec_dec_init(&dec, (unsigned char*)data, data_len);
    silk_init_decoder(&st);
    st.nb_subfr = nb_subfr;
    st.nFramesPerPacket = frames_per_packet;
    silk_decoder_set_fs(&st, fs_kHz, fs_kHz * 1000);

    for (i = 0; i < frames_per_packet; i++) {
        st.VAD_flags[i] = ec_dec_bit_logp(&dec, 1);
    }
    st.LBRR_flag = ec_dec_bit_logp(&dec, 1);
    silk_memset(st.LBRR_flags, 0, sizeof(st.LBRR_flags));
    if (!st.LBRR_flag) {
        return -1;
    }
    if (frames_per_packet == 1) {
        st.LBRR_flags[0] = 1;
    } else {
        opus_int32 LBRR_symbol = ec_dec_icdf(&dec, silk_LBRR_flags_iCDF_ptr[frames_per_packet - 2], 8) + 1;
        for (i = 0; i < frames_per_packet; i++) {
            st.LBRR_flags[i] = (LBRR_symbol >> i) & 1;
        }
    }

    for (i = 0; i < frames_per_packet; i++) {
        if (st.LBRR_flags[i] == 0) {
            continue;
        }
        int condCoding = CODE_INDEPENDENTLY;
        if (i > 0 && st.LBRR_flags[i - 1]) {
            condCoding = CODE_CONDITIONALLY;
        }
        silk_decode_indices(&st, &dec, i, 1, condCoding);
        if (i == frame_index) {
            if (out_rng) {
                *out_rng = dec.rng;
            }
            if (out_val) {
                *out_val = dec.val;
            }
            return 0;
        }
    }
    return -1;
}

// Create/destroy a persistent SILK decoder state for native (pre-resampler) decode.
silk_decoder_state* test_silk_decoder_state_create(void) {
    silk_decoder_state *st = (silk_decoder_state*)malloc(sizeof(silk_decoder_state));
    if (st == NULL) {
        return NULL;
    }
    silk_init_decoder(st);
    return st;
}

void test_silk_decoder_state_destroy(silk_decoder_state *st) {
    if (st != NULL) {
        free(st);
    }
}

// Decode a SILK packet to native samples using libopus core path (no PLC/CNG),
// updating the provided decoder state. Returns 0 on success, -1 on failure.
int test_silk_decode_packet_native_core_state(
    silk_decoder_state *st,
    const unsigned char *data, int data_len,
    int fs_kHz, int nb_subfr, int frames_per_packet,
    opus_int16 *out, int out_len)
{
    if (st == NULL || data == NULL || out == NULL) {
        return -1;
    }
    ec_dec dec;
    silk_decoder_control ctrl;
    opus_int16 pulses_buf[MAX_FRAME_LENGTH + SHELL_CODEC_FRAME_LENGTH];
    int i;

    ec_dec_init(&dec, (unsigned char*)data, data_len);
    st->nb_subfr = nb_subfr;
    st->nFramesPerPacket = frames_per_packet;
    st->nFramesDecoded = 0;
    st->arch = 0;
    silk_decoder_set_fs(st, fs_kHz, fs_kHz * 1000);

    // Decode VAD flags
    for (i = 0; i < frames_per_packet; i++) {
        st->VAD_flags[i] = ec_dec_bit_logp(&dec, 1);
    }
    // Decode LBRR flags
    st->LBRR_flag = ec_dec_bit_logp(&dec, 1);
    silk_memset(st->LBRR_flags, 0, sizeof(st->LBRR_flags));
    if (st->LBRR_flag) {
        if (frames_per_packet == 1) {
            st->LBRR_flags[0] = 1;
        } else {
            opus_int32 LBRR_symbol = ec_dec_icdf(&dec, silk_LBRR_flags_iCDF_ptr[frames_per_packet - 2], 8) + 1;
            for (i = 0; i < frames_per_packet; i++) {
                st->LBRR_flags[i] = (LBRR_symbol >> i) & 1;
            }
        }
    }

    // Skip LBRR payload to advance range decoder
    if (st->LBRR_flag) {
        for (i = 0; i < frames_per_packet; i++) {
            if (st->LBRR_flags[i] == 0) {
                continue;
            }
            int condCoding = CODE_INDEPENDENTLY;
            if (i > 0 && st->LBRR_flags[i - 1]) {
                condCoding = CODE_CONDITIONALLY;
            }
            silk_decode_indices(st, &dec, i, 1, condCoding);
            silk_decode_pulses(&dec, pulses_buf, st->indices.signalType,
                st->indices.quantOffsetType, st->frame_length);
        }
    }

    int frame_length = st->frame_length;
    if (out_len < frames_per_packet * frame_length) {
        return -1;
    }

    for (i = 0; i < frames_per_packet; i++) {
        int condCoding = (i == 0) ? CODE_INDEPENDENTLY : CODE_CONDITIONALLY;
        st->nFramesDecoded = i;
        silk_decode_indices(st, &dec, i, 0, condCoding);
        silk_decode_pulses(&dec, pulses_buf, st->indices.signalType,
            st->indices.quantOffsetType, st->frame_length);
        silk_decode_parameters(st, &ctrl, condCoding);
        silk_decode_core(st, &ctrl, &out[i * frame_length], pulses_buf, 0);

        // Update output buffer (matches silk_decode_frame)
        int mv_len = st->ltp_mem_length - st->frame_length;
        if (mv_len > 0) {
            silk_memmove(st->outBuf, &st->outBuf[st->frame_length], mv_len * sizeof(opus_int16));
        }
        silk_memcpy(&st->outBuf[mv_len], &out[i * frame_length], st->frame_length * sizeof(opus_int16));

        st->lossCnt = 0;
        st->prevSignalType = st->indices.signalType;
        st->first_frame_after_reset = 0;
        st->lagPrev = ctrl.pitchL[ st->nb_subfr - 1 ];
    }

    return 0;
}

// Simple post-processing state for resampling + sMid buffering (mono).
typedef struct {
    silk_resampler_state_struct resampler;
    opus_int16 sMid[2];
    int fs_kHz;
    int fs_API_Hz;
    opus_int16 tmp[MAX_FRAME_LENGTH + 2];
} silk_postproc_state;

silk_postproc_state* test_silk_postproc_create(int fs_kHz, int fs_API_Hz) {
    silk_postproc_state *st = (silk_postproc_state*)malloc(sizeof(silk_postproc_state));
    if (st == NULL) {
        return NULL;
    }
    memset(st, 0, sizeof(*st));
    st->fs_kHz = fs_kHz;
    st->fs_API_Hz = fs_API_Hz;
    silk_resampler_init(&st->resampler, fs_kHz * 1000, fs_API_Hz, 0);
    return st;
}

void test_silk_postproc_destroy(silk_postproc_state *st) {
    if (st != NULL) {
        free(st);
    }
}

// Reset resampler for a new input/output rate while preserving sMid.
void test_silk_postproc_reset_resampler(silk_postproc_state *st, int fs_kHz, int fs_API_Hz) {
    if (st == NULL) {
        return;
    }
    st->fs_kHz = fs_kHz;
    st->fs_API_Hz = fs_API_Hz;
    silk_resampler_init(&st->resampler, fs_kHz * 1000, fs_API_Hz, 0);
}

// Set sMid state.
void test_silk_postproc_set_smid(silk_postproc_state *st, opus_int16 s0, opus_int16 s1) {
    if (st == NULL) {
        return;
    }
    st->sMid[0] = s0;
    st->sMid[1] = s1;
}

// Get sMid state.
void test_silk_postproc_get_smid(silk_postproc_state *st, opus_int16 *s0, opus_int16 *s1) {
    if (st == NULL || s0 == NULL || s1 == NULL) {
        return;
    }
    *s0 = st->sMid[0];
    *s1 = st->sMid[1];
}

// Post-process one native frame: apply sMid buffering and resample to API rate.
// Returns number of output samples on success, -1 on failure.
int test_silk_postproc_frame(
    silk_postproc_state *st,
    const opus_int16 *frame, int frame_len,
    opus_int16 *out, int out_len)
{
    if (st == NULL || frame == NULL || out == NULL || frame_len <= 0) {
        return -1;
    }
    int nSamplesOut = (int)((opus_int64)frame_len * st->fs_API_Hz / (st->fs_kHz * 1000));
    if (out_len < nSamplesOut) {
        return -1;
    }
    st->tmp[0] = st->sMid[0];
    st->tmp[1] = st->sMid[1];
    memcpy(&st->tmp[2], frame, frame_len * sizeof(opus_int16));
    st->sMid[0] = st->tmp[frame_len];
    st->sMid[1] = st->tmp[frame_len + 1];
    silk_resampler(&st->resampler, out, &st->tmp[1], frame_len);
    return nSamplesOut;
}

// Per-frame decoded parameters from libopus.
typedef struct {
    opus_int32 Gains_Q16[MAX_NB_SUBFR];
    opus_int16 PredCoef_Q12[2][MAX_LPC_ORDER];
    opus_int16 LTPCoef_Q14[LTP_ORDER * MAX_NB_SUBFR];
    opus_int pitchL[MAX_NB_SUBFR];
    opus_int LTP_scale_Q14;
    opus_int8 NLSFIndices[MAX_LPC_ORDER + 1];
    opus_int8 NLSFInterpCoef_Q2;
} silk_frame_params;

// Decode a packet and return per-frame parameters (Gains, LPC, LTP, pitch).
// Updates decoder state to match normal decode flow. Returns number of frames decoded or -1 on failure.
int test_silk_decode_packet_params_state(
    silk_decoder_state *st,
    const unsigned char *data, int data_len,
    int fs_kHz, int nb_subfr, int frames_per_packet,
    silk_frame_params *out_params, int out_params_len)
{
    if (st == NULL || data == NULL || out_params == NULL) {
        return -1;
    }
    if (out_params_len < frames_per_packet) {
        return -1;
    }
    ec_dec dec;
    silk_decoder_control ctrl;
    opus_int16 pulses_buf[MAX_FRAME_LENGTH + SHELL_CODEC_FRAME_LENGTH];
    opus_int16 out_buf[MAX_FRAME_LENGTH];
    int i;

    ec_dec_init(&dec, (unsigned char*)data, data_len);
    st->nb_subfr = nb_subfr;
    st->nFramesPerPacket = frames_per_packet;
    st->nFramesDecoded = 0;
    st->arch = 0;
    silk_decoder_set_fs(st, fs_kHz, fs_kHz * 1000);

    // Decode VAD flags
    for (i = 0; i < frames_per_packet; i++) {
        st->VAD_flags[i] = ec_dec_bit_logp(&dec, 1);
    }
    // Decode LBRR flags
    st->LBRR_flag = ec_dec_bit_logp(&dec, 1);
    silk_memset(st->LBRR_flags, 0, sizeof(st->LBRR_flags));
    if (st->LBRR_flag) {
        if (frames_per_packet == 1) {
            st->LBRR_flags[0] = 1;
        } else {
            opus_int32 LBRR_symbol = ec_dec_icdf(&dec, silk_LBRR_flags_iCDF_ptr[frames_per_packet - 2], 8) + 1;
            for (i = 0; i < frames_per_packet; i++) {
                st->LBRR_flags[i] = (LBRR_symbol >> i) & 1;
            }
        }
    }

    // Skip LBRR payload
    if (st->LBRR_flag) {
        for (i = 0; i < frames_per_packet; i++) {
            if (st->LBRR_flags[i] == 0) {
                continue;
            }
            int condCoding = CODE_INDEPENDENTLY;
            if (i > 0 && st->LBRR_flags[i - 1]) {
                condCoding = CODE_CONDITIONALLY;
            }
            silk_decode_indices(st, &dec, i, 1, condCoding);
            silk_decode_pulses(&dec, pulses_buf, st->indices.signalType,
                st->indices.quantOffsetType, st->frame_length);
        }
    }

    for (i = 0; i < frames_per_packet; i++) {
        int condCoding = (i == 0) ? CODE_INDEPENDENTLY : CODE_CONDITIONALLY;
        st->nFramesDecoded = i;
        silk_decode_indices(st, &dec, i, 0, condCoding);
        silk_decode_pulses(&dec, pulses_buf, st->indices.signalType,
            st->indices.quantOffsetType, st->frame_length);
        silk_decode_parameters(st, &ctrl, condCoding);

        // Copy parameters
        memcpy(out_params[i].Gains_Q16, ctrl.Gains_Q16, sizeof(ctrl.Gains_Q16));
        memcpy(out_params[i].PredCoef_Q12, ctrl.PredCoef_Q12, sizeof(ctrl.PredCoef_Q12));
        memcpy(out_params[i].LTPCoef_Q14, ctrl.LTPCoef_Q14, sizeof(ctrl.LTPCoef_Q14));
        memcpy(out_params[i].pitchL, ctrl.pitchL, sizeof(ctrl.pitchL));
        out_params[i].LTP_scale_Q14 = ctrl.LTP_scale_Q14;
        memcpy(out_params[i].NLSFIndices, st->indices.NLSFIndices, sizeof(st->indices.NLSFIndices));
        out_params[i].NLSFInterpCoef_Q2 = st->indices.NLSFInterpCoef_Q2;

        // Decode core to advance state
        silk_decode_core(st, &ctrl, out_buf, pulses_buf, 0);

        // Update output buffer
        int mv_len = st->ltp_mem_length - st->frame_length;
        if (mv_len > 0) {
            silk_memmove(st->outBuf, &st->outBuf[st->frame_length], mv_len * sizeof(opus_int16));
        }
        silk_memcpy(&st->outBuf[mv_len], out_buf, st->frame_length * sizeof(opus_int16));

        st->lossCnt = 0;
        st->prevSignalType = st->indices.signalType;
        st->first_frame_after_reset = 0;
        st->lagPrev = ctrl.pitchL[ st->nb_subfr - 1 ];
    }

    return frames_per_packet;
}

// Declaration for comb_filter from celt.h
void comb_filter(opus_val32 *y, opus_val32 *x, int T0, int T1, int N,
      opus_val16 g0, opus_val16 g1, int tapset0, int tapset1,
      const opus_val16 *window, int overlap, int arch);

// Test harness for comb_filter
// Allocates internal buffer, copies input, applies filter, copies output.
// Input x and output y are float arrays of length n.
// Window is float array of length overlap.
// Uses arch=0 (generic implementation).
void test_comb_filter(float *y, float *x, int history, int T0, int T1, int n,
                      float g0, float g1, int tapset0, int tapset1,
                      const float *window, int overlap) {
    // Apply comb filter (x pointer starts at history, so x[-T] is valid)
    comb_filter(y + history, x + history, T0, T1, n, g0, g1, tapset0, tapset1, window, overlap, 0);
}

// Compute Vorbis window value at position i for overlap length
float test_vorbis_window(int i, int overlap) {
    float x = (float)(i) + 0.5f;
    float sinArg = 0.5f * M_PI * x / (float)(overlap);
    float s = sinf(sinArg);
    return sinf(0.5f * M_PI * s * s);
}

// Create an opus decoder
OpusDecoder* test_decoder_create(int sample_rate, int channels, int *error) {
    return opus_decoder_create(sample_rate, channels, error);
}

// Destroy an opus decoder
void test_decoder_destroy(OpusDecoder* dec) {
    opus_decoder_destroy(dec);
}

// Decode a single packet with persistent decoder state
int test_decode_float(OpusDecoder* dec, const unsigned char *data, int data_len,
                      float *pcm_out, int max_samples) {
    return opus_decode_float(dec, data, data_len, pcm_out, max_samples, 0);
}

// MDCT/IMDCT test functions using internal libopus modes
#include "modes.h"
#include "mdct.h"
#include "kiss_fft.h"
#include "_kiss_fft_guts.h"

// Get the static mode for 48kHz / 960 samples
CELTMode* test_get_celt_mode_48000_960() {
    return opus_custom_mode_create(48000, 960, NULL);
}

// Get the window from the mode
const float* test_get_mode_window(CELTMode* mode) {
    return mode->window;
}

// Get overlap from the mode
int test_get_mode_overlap(CELTMode* mode) {
    return mode->overlap;
}

// Get MDCT size for a given shift
int test_get_mdct_size(CELTMode* mode, int shift) {
    return mode->mdct.n >> shift;
}

// Perform IMDCT using libopus clt_mdct_backward
// Input: N2 frequency coefficients
// Output: N2 + overlap time samples (windowed overlap-add format)
// shift: 0=1920, 1=960, 2=480, 3=240 (MDCT size = 1920 >> shift)
void test_imdct_backward(CELTMode* mode, float* in, float* out, int shift) {
    int n = mode->mdct.n >> shift;
    int n2 = n >> 1;
    int overlap = mode->overlap;

    // Zero output buffer
    memset(out, 0, (n2 + overlap) * sizeof(float));

    // Call libopus IMDCT
    clt_mdct_backward(&mode->mdct, in, out, mode->window, overlap, shift, 1, 0);
}

// Perform MDCT using libopus clt_mdct_forward
// Input: N2 + overlap time samples
// Output: N2 frequency coefficients
// shift: 0=1920, 1=960, 2=480, 3=240 (MDCT size = 1920 >> shift)
void test_mdct_forward(CELTMode* mode, float* in, float* out, int shift) {
    int n = mode->mdct.n >> shift;
    int n2 = n >> 1;
    int overlap = mode->overlap;

    // Call libopus MDCT
    clt_mdct_forward(&mode->mdct, in, out, mode->window, overlap, shift, 1, 0);
}

// Get the twiddle factors from the CELT mode's MDCT lookup.
// Returns n2 twiddle values (cos(2*pi*(i+0.125)/n) for i in [0, n2)).
void test_get_mdct_twiddles(const CELTMode* mode, int shift, float *out) {
    int n = mode->mdct.n >> shift;
    int n2 = n >> 1;
    const kiss_twiddle_scalar *trig = mode->mdct.trig;

    // Advance trig pointer for shift
    for (int i = 0; i < shift; i++) {
        trig += (mode->mdct.n >> i) >> 1;
    }

    for (int i = 0; i < n2; i++) {
        out[i] = trig[i];
    }
}

// Get the FFT bitrev table.
void test_get_fft_bitrev(const CELTMode* mode, int shift, int16_t *out, int n4) {
    const kiss_fft_state *st = mode->mdct.kfft[shift];
    for (int i = 0; i < n4; i++) {
        out[i] = st->bitrev[i];
    }
}

// Get the FFT scale factor.
float test_get_fft_scale(const CELTMode* mode, int shift) {
    return mode->mdct.kfft[shift]->scale;
}

// Get window values from CELT mode
void test_get_celt_window(const CELTMode* mode, float *out) {
    int overlap = mode->overlap;
    for (int i = 0; i < overlap; i++) {
        out[i] = mode->window[i];
    }
}

// Perform MDCT forward and expose intermediate stages.
// stage 0: window/fold output (f buffer, size n2)
// stage 1: pre-rotation output in sequential order (before bitrev, size n4*2)
// stage 2: FFT input in bitrev order (f2 buffer, size n4*2)
// stage 3: FFT output (f2 buffer after opus_fft_impl, size n4*2)
// stage 4: final MDCT coefficients (size n2)
void test_mdct_forward_stages(
    const CELTMode* mode, const float* in, float* stage_out,
    int shift, int stage)
{
    int n = mode->mdct.n >> shift;
    int n2 = n >> 1;
    int n4 = n >> 2;
    int overlap = mode->overlap;
    const kiss_fft_state *st = mode->mdct.kfft[shift];
    const kiss_twiddle_scalar *trig = mode->mdct.trig;
    float scale = st->scale;
    int i;

    // Advance trig pointer for shift
    for (i = 0; i < shift; i++) {
        trig += (mode->mdct.n >> i) >> 1;
    }

    // Allocate intermediate buffers
    float *f = (float*)malloc(n2 * sizeof(float));
    kiss_fft_cpx *f2 = (kiss_fft_cpx*)malloc(n4 * sizeof(kiss_fft_cpx));

    // Stage 0: Window, shuffle, fold
    {
        const float *xp1 = in + (overlap >> 1);
        const float *xp2 = in + n2 - 1 + (overlap >> 1);
        float *yp = f;
        const float *wp1 = mode->window + (overlap >> 1);
        const float *wp2 = mode->window + (overlap >> 1) - 1;

        for (i = 0; i < ((overlap + 3) >> 2); i++) {
            *yp++ = xp1[n2] * (*wp2) + (*xp2) * (*wp1);
            *yp++ = (*xp1) * (*wp1) - xp2[-n2] * (*wp2);
            xp1 += 2;
            xp2 -= 2;
            wp1 += 2;
            wp2 -= 2;
        }
        wp1 = mode->window;
        wp2 = mode->window + overlap - 1;
        for (; i < n4 - ((overlap + 3) >> 2); i++) {
            *yp++ = *xp2;
            *yp++ = *xp1;
            xp1 += 2;
            xp2 -= 2;
        }
        for (; i < n4; i++) {
            *yp++ = -xp1[-n2] * (*wp1) + (*xp2) * (*wp2);
            *yp++ = (*xp1) * (*wp2) + xp2[n2] * (*wp1);
            xp1 += 2;
            xp2 -= 2;
            wp1 += 2;
            wp2 -= 2;
        }
    }

    if (stage == 0) {
        memcpy(stage_out, f, n2 * sizeof(float));
        free(f);
        free(f2);
        return;
    }

    // Stage 1: Pre-rotation (before bitrev storage) - output sequential order
    if (stage == 1) {
        float *yp = f;
        for (i = 0; i < n4; i++) {
            float t0 = trig[i];
            float t1 = trig[n4 + i];
            float re = *yp++;
            float im = *yp++;
            float yr = re * t0 - im * t1;
            float yi = im * t0 + re * t1;
            stage_out[2*i] = yr * scale;
            stage_out[2*i+1] = yi * scale;
        }
        free(f);
        free(f2);
        return;
    }

    // Store in bitrev order for FFT
    {
        float *yp = f;
        for (i = 0; i < n4; i++) {
            kiss_fft_cpx yc;
            float t0 = trig[i];
            float t1 = trig[n4 + i];
            float re = *yp++;
            float im = *yp++;
            float yr = re * t0 - im * t1;
            float yi = im * t0 + re * t1;
            yc.r = yr * scale;
            yc.i = yi * scale;
            f2[st->bitrev[i]] = yc;
        }
    }

    if (stage == 2) {
        // Output FFT input (in bitrev order)
        for (i = 0; i < n4; i++) {
            stage_out[2*i] = f2[i].r;
            stage_out[2*i+1] = f2[i].i;
        }
        free(f);
        free(f2);
        return;
    }

    // Stage 3: FFT
    opus_fft_impl(st, f2);

    if (stage == 3) {
        // Output FFT output
        for (i = 0; i < n4; i++) {
            stage_out[2*i] = f2[i].r;
            stage_out[2*i+1] = f2[i].i;
        }
        free(f);
        free(f2);
        return;
    }

    // Stage 4: Post-rotation
    {
        const kiss_fft_cpx *fp = f2;
        float *yp1 = stage_out;
        float *yp2 = stage_out + n2 - 1;

        for (i = 0; i < n4; i++) {
            float yr, yi;
            float t0 = trig[i];
            float t1 = trig[n4 + i];
            yr = fp->i * t1 - fp->r * t0;
            yi = fp->r * t1 + fp->i * t0;
            *yp1 = yr;
            *yp2 = yi;
            fp++;
            yp1 += 2;
            yp2 -= 2;
        }
    }

    free(f);
    free(f2);
}

// Test LPC analysis filter with provided data
void test_lpc_analysis_filter(
    opus_int16 *out,
    const opus_int16 *in,
    const opus_int16 *B,
    int len,
    int order)
{
    silk_LPC_analysis_filter(out, in, B, len, order, 0);
}

// Test specific arithmetic operations
opus_int32 test_silk_SMLABB(opus_int32 a, opus_int32 b, opus_int32 c) {
    return silk_SMLABB(a, b, c);
}

opus_int32 test_silk_SMLABB_ovflw(opus_int32 a, opus_int32 b, opus_int32 c) {
    return silk_SMLABB_ovflw(a, b, c);
}

opus_int32 test_silk_ADD32_ovflw(opus_int32 a, opus_int32 b) {
    return silk_ADD32_ovflw(a, b);
}

opus_int32 test_silk_SMULBB(opus_int32 a, opus_int32 b) {
    return silk_SMULBB(a, b);
}

opus_int32 test_silk_SMULWB(opus_int32 a, opus_int32 b) {
    return silk_SMULWB(a, b);
}

opus_int32 test_silk_SMLAWB(opus_int32 a, opus_int32 b, opus_int32 c) {
    return silk_SMLAWB(a, b, c);
}

// Test silk_DIV32_varQ
opus_int32 test_silk_DIV32_varQ(opus_int32 a, opus_int32 b, opus_int Qres) {
    return silk_DIV32_varQ(a, b, Qres);
}

// Test silk_INVERSE32_varQ
opus_int32 test_silk_INVERSE32_varQ(opus_int32 b, opus_int Qres) {
    return silk_INVERSE32_varQ(b, Qres);
}

// Test silk_SMULWW
opus_int32 test_silk_SMULWW(opus_int32 a, opus_int32 b) {
    return silk_SMULWW(a, b);
}

// Test silk_SMLAWW
opus_int32 test_silk_SMLAWW(opus_int32 a, opus_int32 b, opus_int32 c) {
    return silk_SMLAWW(a, b, c);
}

// Get outBuf state after decoding frames up to frame_index.
int test_silk_get_outbuf_state(
    const unsigned char *data, int data_len,
    int fs_kHz, int nb_subfr, int frames_per_packet, int frame_index,
    opus_int16 *out_buf, int out_buf_len,
    opus_int32 *out_sLPC_Q14_buf, int slpc_buf_len,
    opus_int32 *out_prev_gain_Q16)
{
    if (data == NULL || out_buf == NULL) {
        return -1;
    }

    ec_dec dec;
    silk_decoder_state st;
    silk_decoder_control ctrl;
    opus_int16 pulses_buf[MAX_FRAME_LENGTH + SHELL_CODEC_FRAME_LENGTH];
    opus_int16 frame_out[MAX_FRAME_LENGTH];
    int i;

    ec_dec_init(&dec, (unsigned char*)data, data_len);
    silk_init_decoder(&st);
    st.nb_subfr = nb_subfr;
    st.nFramesPerPacket = frames_per_packet;
    st.nFramesDecoded = 0;
    st.arch = 0;
    silk_decoder_set_fs(&st, fs_kHz, fs_kHz * 1000);

    // Decode VAD flags
    for (i = 0; i < frames_per_packet; i++) {
        st.VAD_flags[i] = ec_dec_bit_logp(&dec, 1);
    }
    // Decode LBRR flags
    st.LBRR_flag = ec_dec_bit_logp(&dec, 1);
    silk_memset(st.LBRR_flags, 0, sizeof(st.LBRR_flags));
    if (st.LBRR_flag) {
        if (frames_per_packet == 1) {
            st.LBRR_flags[0] = 1;
        } else {
            opus_int32 LBRR_symbol = ec_dec_icdf(&dec, silk_LBRR_flags_iCDF_ptr[frames_per_packet - 2], 8) + 1;
            for (i = 0; i < frames_per_packet; i++) {
                st.LBRR_flags[i] = (LBRR_symbol >> i) & 1;
            }
        }
    }

    // Skip LBRR payload
    if (st.LBRR_flag) {
        for (i = 0; i < frames_per_packet; i++) {
            if (st.LBRR_flags[i] == 0) {
                continue;
            }
            int condCoding = CODE_INDEPENDENTLY;
            if (i > 0 && st.LBRR_flags[i - 1]) {
                condCoding = CODE_CONDITIONALLY;
            }
            silk_decode_indices(&st, &dec, i, 1, condCoding);
            silk_decode_pulses(&dec, pulses_buf, st.indices.signalType,
                st.indices.quantOffsetType, st.frame_length);
        }
    }

    // Decode all frames up to and including frame_index
    for (i = 0; i <= frame_index && i < frames_per_packet; i++) {
        int condCoding = (i == 0) ? CODE_INDEPENDENTLY : CODE_CONDITIONALLY;
        st.nFramesDecoded = i;
        silk_decode_indices(&st, &dec, i, 0, condCoding);
        silk_decode_pulses(&dec, pulses_buf, st.indices.signalType,
            st.indices.quantOffsetType, st.frame_length);
        silk_decode_parameters(&st, &ctrl, condCoding);
        silk_decode_core(&st, &ctrl, frame_out, pulses_buf, 0);

        // Update output buffer
        int mv_len = st.ltp_mem_length - st.frame_length;
        if (mv_len > 0) {
            silk_memmove(st.outBuf, &st.outBuf[st.frame_length], mv_len * sizeof(opus_int16));
        }
        silk_memcpy(&st.outBuf[mv_len], frame_out, st.frame_length * sizeof(opus_int16));

        st.lossCnt = 0;
        st.prevSignalType = st.indices.signalType;
        st.first_frame_after_reset = 0;
        st.lagPrev = ctrl.pitchL[st.nb_subfr - 1];
    }

    // Copy output buffer state
    int copy_len = out_buf_len;
    if (copy_len > (int)sizeof(st.outBuf)/sizeof(st.outBuf[0])) {
        copy_len = sizeof(st.outBuf)/sizeof(st.outBuf[0]);
    }
    memcpy(out_buf, st.outBuf, copy_len * sizeof(opus_int16));

    // Copy sLPC_Q14_buf
    if (out_sLPC_Q14_buf != NULL && slpc_buf_len >= MAX_LPC_ORDER) {
        memcpy(out_sLPC_Q14_buf, st.sLPC_Q14_buf, MAX_LPC_ORDER * sizeof(opus_int32));
    }

    if (out_prev_gain_Q16 != NULL) {
        *out_prev_gain_Q16 = st.prev_gain_Q16;
    }

    return 0;
}

// Decode SILK packet and return per-frame NLSF/LPC state for debugging.
// Decodes up to frames_to_decode frames and populates the output arrays.
// Returns 0 on success, -1 on failure.
int test_silk_decode_nlsf_state(
    const unsigned char *data, int data_len,
    int fs_kHz, int nb_subfr, int frames_per_packet, int frames_to_decode,
    // Output arrays: [frame_index][lpc_order] for NLSF, [frame_index][2][lpc_order] for LPC
    opus_int16 *out_prevNLSF_Q15,     // [MAX_FRAMES][MAX_LPC_ORDER]
    opus_int16 *out_currNLSF_Q15,     // [MAX_FRAMES][MAX_LPC_ORDER]
    opus_int16 *out_interpNLSF_Q15,   // [MAX_FRAMES][MAX_LPC_ORDER] (nlsf0 when interp)
    opus_int16 *out_predCoef0_Q12,    // [MAX_FRAMES][MAX_LPC_ORDER] (PredCoef_Q12[0])
    opus_int16 *out_predCoef1_Q12,    // [MAX_FRAMES][MAX_LPC_ORDER] (PredCoef_Q12[1])
    opus_int8 *out_nlsfInterpCoef,    // [MAX_FRAMES]
    int lpc_order)
{
    if (data == NULL || frames_to_decode <= 0 || frames_to_decode > frames_per_packet) {
        return -1;
    }

    ec_dec dec;
    silk_decoder_state st;
    silk_decoder_control ctrl;
    opus_int16 pulses_buf[MAX_FRAME_LENGTH + SHELL_CODEC_FRAME_LENGTH];
    opus_int16 frame_out[MAX_FRAME_LENGTH];
    int i, k;

    ec_dec_init(&dec, (unsigned char*)data, data_len);
    silk_init_decoder(&st);
    st.nb_subfr = nb_subfr;
    st.nFramesPerPacket = frames_per_packet;
    st.nFramesDecoded = 0;
    st.arch = 0;
    silk_decoder_set_fs(&st, fs_kHz, fs_kHz * 1000);

    // Decode VAD flags
    for (i = 0; i < frames_per_packet; i++) {
        st.VAD_flags[i] = ec_dec_bit_logp(&dec, 1);
    }
    // Decode LBRR flags
    st.LBRR_flag = ec_dec_bit_logp(&dec, 1);
    silk_memset(st.LBRR_flags, 0, sizeof(st.LBRR_flags));
    if (st.LBRR_flag) {
        if (frames_per_packet == 1) {
            st.LBRR_flags[0] = 1;
        } else {
            opus_int32 LBRR_symbol = ec_dec_icdf(&dec, silk_LBRR_flags_iCDF_ptr[frames_per_packet - 2], 8) + 1;
            for (i = 0; i < frames_per_packet; i++) {
                st.LBRR_flags[i] = (LBRR_symbol >> i) & 1;
            }
        }
    }

    // Skip LBRR payload
    if (st.LBRR_flag) {
        for (i = 0; i < frames_per_packet; i++) {
            if (st.LBRR_flags[i] == 0) {
                continue;
            }
            int condCoding = CODE_INDEPENDENTLY;
            if (i > 0 && st.LBRR_flags[i - 1]) {
                condCoding = CODE_CONDITIONALLY;
            }
            silk_decode_indices(&st, &dec, i, 1, condCoding);
            silk_decode_pulses(&dec, pulses_buf, st.indices.signalType,
                st.indices.quantOffsetType, st.frame_length);
        }
    }

    // Decode normal frames and capture NLSF state
    for (i = 0; i < frames_to_decode; i++) {
        int condCoding = (i == 0) ? CODE_INDEPENDENTLY : CODE_CONDITIONALLY;
        st.nFramesDecoded = i;

        // Copy prevNLSF before decoding (this is what will be used for interp)
        for (k = 0; k < lpc_order; k++) {
            out_prevNLSF_Q15[i * MAX_LPC_ORDER + k] = st.prevNLSF_Q15[k];
        }

        silk_decode_indices(&st, &dec, i, 0, condCoding);
        silk_decode_pulses(&dec, pulses_buf, st.indices.signalType,
            st.indices.quantOffsetType, st.frame_length);

        // Save NLSFInterpCoef
        out_nlsfInterpCoef[i] = st.indices.NLSFInterpCoef_Q2;

        // Call silk_decode_parameters which does NLSF decode and interp
        silk_decode_parameters(&st, &ctrl, condCoding);

        // Now extract the values:
        // currNLSF is now in st.prevNLSF_Q15 (it was copied at end of silk_decode_parameters)
        for (k = 0; k < lpc_order; k++) {
            out_currNLSF_Q15[i * MAX_LPC_ORDER + k] = st.prevNLSF_Q15[k];
        }

        // PredCoef_Q12[0] and [1]
        for (k = 0; k < lpc_order; k++) {
            out_predCoef0_Q12[i * MAX_LPC_ORDER + k] = ctrl.PredCoef_Q12[0][k];
            out_predCoef1_Q12[i * MAX_LPC_ORDER + k] = ctrl.PredCoef_Q12[1][k];
        }

        // Compute interpolated NLSF if interp is active
        if (st.indices.NLSFInterpCoef_Q2 < 4) {
            // Interp was done, so nlsf0 = prevNLSF + (NLSFInterpCoef * (currNLSF - prevNLSF)) >> 2
            // But prevNLSF was already overwritten, so we recompute from predCoef relationship
            // Actually, let's just store what was used: the interpolated coefficients are in PredCoef_Q12[0]
            // We can't easily get nlsf0 back without storing it during decode_parameters
            // For now, just mark it as unavailable
            for (k = 0; k < lpc_order; k++) {
                out_interpNLSF_Q15[i * MAX_LPC_ORDER + k] = -1; // marker
            }
        } else {
            // No interp, nlsf0 = currNLSF
            for (k = 0; k < lpc_order; k++) {
                out_interpNLSF_Q15[i * MAX_LPC_ORDER + k] = st.prevNLSF_Q15[k];
            }
        }

        // Decode core to update state for next frame
        silk_decode_core(&st, &ctrl, frame_out, pulses_buf, 0);

        // Update output buffer
        int mv_len = st.ltp_mem_length - st.frame_length;
        if (mv_len > 0) {
            silk_memmove(st.outBuf, &st.outBuf[st.frame_length], mv_len * sizeof(opus_int16));
        }
        silk_memcpy(&st.outBuf[mv_len], frame_out, st.frame_length * sizeof(opus_int16));

        st.lossCnt = 0;
        st.prevSignalType = st.indices.signalType;
        st.first_frame_after_reset = 0;
        st.lagPrev = ctrl.pitchL[st.nb_subfr - 1];
    }

    return 0;
}

// ====================================================================
// Fine Energy Encoding C wrappers
// ====================================================================

#include "quant_bands.h"

// Forward declaration (defined later in Allocation section)
static CELTMode* get_celt_mode_48000_alloc(void);

// Wrapper for quant_fine_energy (libopus float path)
// oldEBands: I/O - current quantized energies, updated with fine offsets
// error: I/O - residual energies, updated after encoding
// extra_quant: fine bits per band (from allocation)
// Returns 0 on success
int test_quant_fine_energy(
    float *oldEBands,     // I/O: quantized energies [C * nbEBands]
    float *error,         // I/O: residual error [C * nbEBands]
    int *extra_quant,     // In: fine bits per band [nbEBands]
    int start, int end, int C,
    unsigned char *out_buf, int max_size, int *out_len)
{
    CELTMode *mode = get_celt_mode_48000_alloc();
    if (mode == NULL) return -1;

    ec_enc enc;
    ec_enc_init(&enc, out_buf, max_size);

    // prev_quant is NULL for first pass (no previous fine quantization)
    quant_fine_energy(mode, start, end, oldEBands, error, NULL, extra_quant, &enc, C);

    ec_enc_done(&enc);
    *out_len = pack_ec_enc(&enc);
    return enc.error;
}

// Wrapper for quant_energy_finalise (libopus float path)
// oldEBands: I/O - current quantized energies, updated with finalise offsets
// error: I/O - residual error
// fine_quant: fine bits already used per band
// fine_priority: priority flags per band
// bits_left: remaining bits to use
// Returns 0 on success
int test_quant_energy_finalise(
    float *oldEBands,     // I/O: quantized energies [C * nbEBands]
    float *error,         // I/O: residual error [C * nbEBands]
    int *fine_quant,      // In: fine bits per band [nbEBands]
    int *fine_priority,   // In: priority per band [nbEBands]
    int bits_left,
    int start, int end, int C,
    unsigned char *out_buf, int max_size, int *out_len)
{
    CELTMode *mode = get_celt_mode_48000_alloc();
    if (mode == NULL) return -1;

    ec_enc enc;
    ec_enc_init(&enc, out_buf, max_size);

    quant_energy_finalise(mode, start, end, oldEBands, error, fine_quant, fine_priority, bits_left, &enc, C);

    ec_enc_done(&enc);
    *out_len = pack_ec_enc(&enc);
    return enc.error;
}

// Combined wrapper: encode coarse, fine, and finalise in sequence
// Matches the libopus encoder pipeline order
int test_encode_energy_full(
    float *eBands,        // In: target band energies [C * nbEBands]
    float *oldEBands,     // I/O: previous frame's quantized energies, output: updated quantized energies
    float *error_out,     // Out: final residual error [C * nbEBands]
    int *fine_quant,      // In: fine bits per band [nbEBands]
    int *fine_priority,   // In: priority per band [nbEBands]
    int bits_left_finalise, // In: bits for finalise step
    int start, int end, int C, int LM, int intra,
    int nbAvailableBytes,
    unsigned char *out_buf, int max_size, int *out_len)
{
    CELTMode *mode = get_celt_mode_48000_alloc();
    if (mode == NULL) return -1;

    // Allocate temp error array
    float error[42*2]; // Max bands * 2 channels
    memset(error, 0, sizeof(error));

    ec_enc enc;
    ec_enc_init(&enc, out_buf, max_size);

    // Force intra=1 for first frame (matches the typical test scenario)
    opus_val32 delayedIntra = 0;

    // Coarse energy encoding
    quant_coarse_energy(mode, start, end, end,
        eBands, oldEBands, max_size*8,
        error, &enc, C, LM, nbAvailableBytes,
        intra, &delayedIntra, 0, 0, 0);

    // Fine energy encoding
    quant_fine_energy(mode, start, end, oldEBands, error, NULL, fine_quant, &enc, C);

    // Energy finalise
    int actual_bits_left = bits_left_finalise;
    quant_energy_finalise(mode, start, end, oldEBands, error, fine_quant, fine_priority, actual_bits_left, &enc, C);

    // Copy error to output
    memcpy(error_out, error, C * mode->nbEBands * sizeof(float));

    ec_enc_done(&enc);
    *out_len = pack_ec_enc(&enc);
    return enc.error;
}

// ====================================================================
// Allocation comparison C wrappers
// ====================================================================

#include "rate.h"

// Cached mode for allocation tests - use the same mode as other tests
static CELTMode* get_celt_mode_48000_alloc() {
    static CELTMode *cached_mode = NULL;
    if (cached_mode == NULL) {
        cached_mode = opus_custom_mode_create(48000, 960, NULL);
    }
    return cached_mode;
}

// Compute allocation using libopus clt_compute_allocation (decode path - no encoding)
// Returns coded bands count.
// pulses, ebits, fine_priority are output arrays (size nbEBands)
int test_clt_compute_allocation(
    int start, int end,
    const int *offsets,
    const int *cap,
    int alloc_trim,
    int *intensity,
    int *dual_stereo,
    int total,    // total bits in Q3
    int *balance, // output balance
    int *pulses,
    int *ebits,
    int *fine_priority,
    int C,        // channels
    int LM,       // log mode (0=2.5ms, 1=5ms, 2=10ms, 3=20ms)
    int prev,
    int signalBandwidth)
{
    CELTMode *mode = get_celt_mode_48000_alloc();
    if (mode == NULL) {
        fprintf(stderr, "ERROR: get_celt_mode_48000_alloc returned NULL\n");
        return -1;
    }

    opus_int32 bal = 0;

    // Create ec_enc for encoder path - doesn't read from stream
    unsigned char buf[256];
    memset(buf, 0, sizeof(buf));
    ec_enc enc;
    ec_enc_init(&enc, buf, sizeof(buf));

    // Call with encode=1 for encode path (doesn't need to read skip bits from stream)
    int codedBands = clt_compute_allocation(mode, start, end, offsets, cap, alloc_trim,
        intensity, dual_stereo, total, &bal, pulses, ebits, fine_priority, C, LM, (ec_ctx*)&enc, 1, prev, signalBandwidth);

    *balance = (int)bal;

    return codedBands;
}

// Get eBands array from libopus mode
void test_get_ebands(int *out, int max_len) {
    CELTMode *mode = get_celt_mode_48000_alloc();
    if (mode == NULL) return;

    int n = mode->nbEBands + 1;
    if (n > max_len) n = max_len;
    for (int i = 0; i < n; i++) {
        out[i] = mode->eBands[i];
    }
}

// Get logN array from libopus mode
void test_get_logN(int *out, int max_len) {
    CELTMode *mode = get_celt_mode_48000_alloc();
    if (mode == NULL) return;

    int n = mode->nbEBands;
    if (n > max_len) n = max_len;
    for (int i = 0; i < n; i++) {
        out[i] = mode->logN[i];
    }
}

// Get cache caps from libopus mode
void test_get_cache_caps(unsigned char *out, int max_len) {
    CELTMode *mode = get_celt_mode_48000_alloc();
    if (mode == NULL) return;

    // caps are organized as [LM+1][C][nbEBands]
    // Total size = 4 * 2 * nbEBands = 8 * 21 = 168
    int n = (mode->maxLM + 1) * 2 * mode->nbEBands;
    if (n > max_len) n = max_len;
    memcpy(out, mode->cache.caps, n);
}

// Compute caps for allocation (same as libopus logic)
void test_compute_caps(int *caps, int nbEBands, int LM, int C) {
    CELTMode *mode = get_celt_mode_48000_alloc();
    if (mode == NULL) return;

    int i;
    for (i = 0; i < nbEBands; i++) {
        int N = (mode->eBands[i+1] - mode->eBands[i]) << LM;
        int cap_idx = (2*LM + (C-1)) * mode->nbEBands + i;
        int cap_val = mode->cache.caps[cap_idx];
        caps[i] = (cap_val + 64) * C * N >> 2;
    }
}

// Get nbAllocVectors from mode
int test_get_nb_alloc_vectors() {
    CELTMode *mode = get_celt_mode_48000_alloc();
    if (mode == NULL) return 0;
    return mode->nbAllocVectors;
}

// Get allocVectors from mode
void test_get_alloc_vectors(int *out, int row, int max_len) {
    CELTMode *mode = get_celt_mode_48000_alloc();
    if (mode == NULL) return;

    int n = mode->nbEBands;
    if (n > max_len) n = max_len;
    for (int i = 0; i < n; i++) {
        out[i] = mode->allocVectors[row * mode->nbEBands + i];
    }
}

// ====================================================================
// Encoder comparison wrappers
// ====================================================================

// Create an opus encoder
OpusEncoder* test_encoder_create(int sample_rate, int channels, int application, int *error) {
    return opus_encoder_create(sample_rate, channels, application, error);
}

// Destroy an opus encoder
void test_encoder_destroy(OpusEncoder* enc) {
    opus_encoder_destroy(enc);
}

// Encode float samples
int test_encode_float(OpusEncoder* enc, const float *pcm, int frame_size, unsigned char *data, int max_data_bytes) {
    return opus_encode_float(enc, pcm, frame_size, data, max_data_bytes);
}

// Set encoder int option
int test_encoder_ctl_set_int(OpusEncoder* enc, int request, int value) {
    return opus_encoder_ctl(enc, request, value);
}

// Get encoder int option
int test_encoder_ctl_get_int(OpusEncoder* enc, int request, int *value) {
    return opus_encoder_ctl(enc, request, value);
}

// Reset encoder state
int test_encoder_reset(OpusEncoder* enc) {
    return opus_encoder_ctl(enc, OPUS_RESET_STATE);
}

// Get final range for verification
int test_encoder_get_final_range(OpusEncoder* enc, opus_uint32 *range) {
    return opus_encoder_ctl(enc, OPUS_GET_FINAL_RANGE(range));
}

// ====================================================================
// PVQ Search comparison wrappers
// ====================================================================

#include "vq.h"
#include "cwrs.h"

// Wrapper to call op_pvq_search with float inputs (matching Go's float path)
// X: normalized input vector (will be modified - signs removed)
// iy: output pulse vector
// K: number of pulses
// N: vector dimension
// Returns yy (energy of output)
float test_op_pvq_search(float *X, int *iy, int K, int N) {
    return op_pvq_search(X, iy, K, N, 0);
}

// ====================================================================
// Range encoder comparison wrappers
// ====================================================================

#include "entenc.h"

// Pack libopus range-encoder output into a contiguous buffer.
// This mirrors gopus's Encoder.Done() packing: [range bytes][pad byte if needed][end bytes].
// Returns the packed length in bytes.
//
// After ec_enc_done(), the buffer contains:
//   [0..offs-1] : range-coded bytes
//   [offs..storage-end_offs-1] : zeros (padding)
//   [storage-end_offs..storage-1] : raw bytes written from end
//
// The nend_bits field contains the ORIGINAL number of bits written to raw area,
// NOT the remaining bits after flushing. We need to compute remaining bits as:
//   remaining = nend_bits - (end_offs * 8)
// If remaining > 0, there's a partial byte at storage-end_offs-1 that needs to be
// included as a pad byte.
static int pack_ec_enc(ec_enc *enc) {
    int offs = (int)enc->offs;
    int end_offs = (int)enc->end_offs;

    // Compute remaining bits that didn't fill a full byte
    // These bits were OR'd into buf[storage-end_offs-1] by ec_enc_done
    int remaining_bits = enc->nend_bits - (end_offs * 8);
    int pad = (remaining_bits > 0) ? 1 : 0;

    if (pad) {
        // The partial byte is at storage - end_offs - 1
        // Copy it to position offs (right after range bytes)
        int idx = (int)enc->storage - end_offs - 1;
        if (idx >= 0 && idx < (int)enc->storage) {
            enc->buf[offs] = enc->buf[idx];
        }
    }

    if (end_offs > 0) {
        // Move raw bytes to right after range bytes (and pad byte if any)
        memmove(enc->buf + offs + pad,
                enc->buf + ((int)enc->storage - end_offs),
                end_offs);
    }

    return offs + end_offs + pad;
}

// Test range encoder: encode a sequence of uniform values and return the bytes
int test_encode_uniform_sequence(unsigned char *out_buf, int max_size,
                                  unsigned int *vals, unsigned int *fts, int count,
                                  int *out_len) {
    ec_enc enc;
    ec_enc_init(&enc, out_buf, max_size);

    for (int i = 0; i < count; i++) {
        ec_enc_uint(&enc, vals[i], fts[i]);
    }

    ec_enc_done(&enc);
    *out_len = pack_ec_enc(&enc);
    return enc.error;
}

// Test encode pulses: encode a pulse vector to bytes
int test_encode_pulses_to_bytes(unsigned char *out_buf, int max_size,
                                 int *pulses, int n, int k, int *out_len) {
    ec_enc enc;
    ec_enc_init(&enc, out_buf, max_size);

    encode_pulses(pulses, n, k, &enc);

    ec_enc_done(&enc);
    *out_len = pack_ec_enc(&enc);
    return enc.error;
}

// Test range encoder with detailed state output
int test_encode_uniform_detailed(unsigned char *out_buf, int max_size,
                                  unsigned int val, unsigned int ft,
                                  int *out_len, int *out_offs, int *out_end_offs, int *out_nend_bits) {
    ec_enc enc;
    ec_enc_init(&enc, out_buf, max_size);

    ec_enc_uint(&enc, val, ft);

    ec_enc_done(&enc);
    *out_offs = (int)enc.offs;
    *out_end_offs = (int)enc.end_offs;
    *out_nend_bits = enc.nend_bits;
    *out_len = pack_ec_enc(&enc);
    return enc.error;
}

// Structure to hold encoder state for tracing
typedef struct {
    opus_uint32 rng;
    opus_uint32 val;
    opus_uint32 ext;
    int rem;
    opus_uint32 offs;
    int tell;
} ec_enc_state_trace;

// Encode a bit with logp and return state BEFORE and AFTER
void test_encode_bit_logp_trace(ec_enc *enc, int val, unsigned int logp,
                                 ec_enc_state_trace *before, ec_enc_state_trace *after) {
    before->rng = enc->rng;
    before->val = enc->val;
    before->ext = enc->ext;
    before->rem = enc->rem;
    before->offs = enc->offs;
    before->tell = ec_tell(enc);

    ec_enc_bit_logp(enc, val, logp);

    after->rng = enc->rng;
    after->val = enc->val;
    after->ext = enc->ext;
    after->rem = enc->rem;
    after->offs = enc->offs;
    after->tell = ec_tell(enc);
}

// Encode ICDF and return state BEFORE and AFTER
void test_encode_icdf_trace(ec_enc *enc, int s, const unsigned char *icdf, unsigned int ftb,
                             ec_enc_state_trace *before, ec_enc_state_trace *after) {
    before->rng = enc->rng;
    before->val = enc->val;
    before->ext = enc->ext;
    before->rem = enc->rem;
    before->offs = enc->offs;
    before->tell = ec_tell(enc);

    ec_enc_icdf(enc, s, icdf, ftb);

    after->rng = enc->rng;
    after->val = enc->val;
    after->ext = enc->ext;
    after->rem = enc->rem;
    after->offs = enc->offs;
    after->tell = ec_tell(enc);
}

// Encode a uniform value and return state BEFORE and AFTER
void test_encode_uint_trace(ec_enc *enc, opus_uint32 val, opus_uint32 ft,
                             ec_enc_state_trace *before, ec_enc_state_trace *after) {
    before->rng = enc->rng;
    before->val = enc->val;
    before->ext = enc->ext;
    before->rem = enc->rem;
    before->offs = enc->offs;
    before->tell = ec_tell(enc);

    ec_enc_uint(enc, val, ft);

    after->rng = enc->rng;
    after->val = enc->val;
    after->ext = enc->ext;
    after->rem = enc->rem;
    after->offs = enc->offs;
    after->tell = ec_tell(enc);
}

// Get current encoder state
void test_get_enc_state(ec_enc *enc, ec_enc_state_trace *state) {
    state->rng = enc->rng;
    state->val = enc->val;
    state->ext = enc->ext;
    state->rem = enc->rem;
    state->offs = enc->offs;
    state->tell = ec_tell(enc);
}

// Initialize encoder and return initial state
void test_enc_init_with_state(ec_enc *enc, unsigned char *buf, int size, ec_enc_state_trace *state) {
    ec_enc_init(enc, buf, size);
    test_get_enc_state(enc, state);
}

// ====================================================================
// SILK LSF/NLSF Encoding Comparison Wrappers
// ====================================================================

// Wrapper for silk_A2NLSF (LPC to NLSF conversion)
// Note: a_Q16 is I/O because it may be modified by bandwidth expansion
void test_silk_a2nlsf(opus_int32 *a_Q16, int order, opus_int16 *nlsf_out) {
    silk_A2NLSF(nlsf_out, a_Q16, order);
}

// Wrapper for silk_NLSF_encode (full NLSF VQ encoding)
// Returns the RD value in Q25
opus_int32 test_silk_nlsf_encode(
    opus_int8 *nlsf_indices,      // Output: indices [LPC_ORDER + 1]
    opus_int16 *pNLSF_Q15,        // I/O: input NLSF, output quantized NLSF [LPC_ORDER]
    int use_wb,                    // 0 = NB/MB, 1 = WB
    const opus_int16 *pW_Q2,      // Weights [LPC_ORDER]
    int NLSF_mu_Q20,              // Rate weight
    int nSurvivors,               // Number of survivors
    int signalType                // Signal type: 0/1/2
) {
    const silk_NLSF_CB_struct *cb = use_wb ? &silk_NLSF_CB_WB : &silk_NLSF_CB_NB_MB;
    return silk_NLSF_encode(nlsf_indices, pNLSF_Q15, cb, pW_Q2, NLSF_mu_Q20, nSurvivors, signalType);
}

// Wrapper for silk_NLSF_stabilize
void test_silk_nlsf_stabilize(opus_int16 *NLSF_Q15, int use_wb) {
    const silk_NLSF_CB_struct *cb = use_wb ? &silk_NLSF_CB_WB : &silk_NLSF_CB_NB_MB;
    silk_NLSF_stabilize(NLSF_Q15, cb->deltaMin_Q15, cb->order);
}

// Get silk_LSFCosTab_FIX_Q12 entry
opus_int16 test_silk_lsf_cos_tab(int idx) {
    if (idx < 0 || idx > LSF_COS_TAB_SZ_FIX) return 0;
    return silk_LSFCosTab_FIX_Q12[idx];
}

// Get LSF_COS_TAB_SZ_FIX
int test_silk_lsf_cos_tab_size(void) {
    return LSF_COS_TAB_SZ_FIX;
}

// Get NLSF codebook parameters
void test_silk_get_nlsf_cb_params(int use_wb, int *nVectors, int *order, int *quantStepSize_Q16, int *invQuantStepSize_Q6) {
    const silk_NLSF_CB_struct *cb = use_wb ? &silk_NLSF_CB_WB : &silk_NLSF_CB_NB_MB;
    *nVectors = cb->nVectors;
    *order = cb->order;
    *quantStepSize_Q16 = cb->quantStepSize_Q16;
    *invQuantStepSize_Q6 = cb->invQuantStepSize_Q6;
}

// Get NLSF stage 1 codebook entry
void test_silk_get_nlsf_cb1(int use_wb, int idx, opus_uint8 *out, opus_int16 *wgt_out) {
    const silk_NLSF_CB_struct *cb = use_wb ? &silk_NLSF_CB_WB : &silk_NLSF_CB_NB_MB;
    int order = cb->order;
    memcpy(out, &cb->CB1_NLSF_Q8[idx * order], order);
    memcpy(wgt_out, &cb->CB1_Wght_Q9[idx * order], order * sizeof(opus_int16));
}

// Get NLSF deltaMin values
void test_silk_get_nlsf_delta_min(int use_wb, opus_int16 *out) {
    const silk_NLSF_CB_struct *cb = use_wb ? &silk_NLSF_CB_WB : &silk_NLSF_CB_NB_MB;
    memcpy(out, cb->deltaMin_Q15, (cb->order + 1) * sizeof(opus_int16));
}

*/
import "C"

import (
	"unsafe"
)

func init() {
	SetLibopusDebugRange(false)
}

// DecodeLaplace calls libopus ec_laplace_decode
func DecodeLaplace(data []byte, fs, decay int) int {
	var val C.int
	cData := (*C.uchar)(unsafe.Pointer(&data[0]))
	C.test_laplace_decode(cData, C.int(len(data)), C.int(fs), C.int(decay), &val)
	return int(val)
}

// EncodeLaplace calls libopus ec_laplace_encode and returns the encoded bytes and the possibly-clamped value.
func EncodeLaplace(val, fs, decay int) ([]byte, int, error) {
	maxSize := 256
	outBuf := make([]byte, maxSize)
	var outVal, outLen C.int

	err := C.test_laplace_encode(
		(*C.uchar)(unsafe.Pointer(&outBuf[0])),
		C.int(maxSize),
		C.int(val),
		C.uint(fs),
		C.int(decay),
		&outVal,
		&outLen,
	)

	if err != 0 {
		return nil, 0, nil
	}

	return outBuf[:int(outLen)], int(outVal), nil
}

// EncodeLaplaceSequence calls libopus ec_laplace_encode for a sequence of values.
func EncodeLaplaceSequence(vals []int, fsArr []int, decayArr []int) ([]byte, []int, error) {
	if len(vals) == 0 || len(vals) != len(fsArr) || len(vals) != len(decayArr) {
		return nil, nil, nil
	}

	maxSize := 4096
	outBuf := make([]byte, maxSize)
	outVals := make([]C.int, len(vals))
	cVals := make([]C.int, len(vals))
	cFs := make([]C.uint, len(vals))
	cDecay := make([]C.int, len(vals))

	for i := range vals {
		cVals[i] = C.int(vals[i])
		cFs[i] = C.uint(fsArr[i])
		cDecay[i] = C.int(decayArr[i])
	}

	var outLen C.int

	err := C.test_laplace_encode_sequence(
		(*C.uchar)(unsafe.Pointer(&outBuf[0])),
		C.int(maxSize),
		(*C.int)(unsafe.Pointer(&cVals[0])),
		(*C.uint)(unsafe.Pointer(&cFs[0])),
		(*C.int)(unsafe.Pointer(&cDecay[0])),
		C.int(len(vals)),
		(*C.int)(unsafe.Pointer(&outVals[0])),
		&outLen,
	)

	if err != 0 {
		return nil, nil, nil
	}

	result := make([]int, len(vals))
	for i := range vals {
		result[i] = int(outVals[i])
	}

	return outBuf[:int(outLen)], result, nil
}

// GetRangeState gets the range coder state after initialization
func GetRangeState(data []byte) (rng, val uint32) {
	var cRng, cVal C.uint
	cData := (*C.uchar)(unsafe.Pointer(&data[0]))
	C.test_get_range_state(cData, C.int(len(data)), &cRng, &cVal)
	return uint32(cRng), uint32(cVal)
}

// DecodeBitLogp calls libopus ec_dec_bit_logp
func DecodeBitLogp(data []byte, logp int) int {
	cData := (*C.uchar)(unsafe.Pointer(&data[0]))
	return int(C.test_decode_bit_logp(cData, C.int(len(data)), C.int(logp)))
}

// DecodeICDF calls libopus ec_dec_icdf
func DecodeICDF(data []byte, icdf []byte, ftb int) int {
	cData := (*C.uchar)(unsafe.Pointer(&data[0]))
	cICDF := (*C.uchar)(unsafe.Pointer(&icdf[0]))
	return int(C.test_decode_icdf(cData, C.int(len(data)), cICDF, C.int(ftb)))
}

// SilkNLSFDecode calls libopus silk_NLSF_decode for NB/MB or WB codebooks.
func SilkNLSFDecode(indices []int8, useWB bool) []int16 {
	if len(indices) == 0 {
		return nil
	}
	out := make([]int16, 16)
	cIdx := (*C.opus_int8)(unsafe.Pointer(&indices[0]))
	cOut := (*C.opus_int16)(unsafe.Pointer(&out[0]))
	wb := 0
	if useWB {
		wb = 1
	}
	C.test_silk_nlsf_decode(cIdx, C.int(wb), cOut)
	return out
}

// SilkNLSF2A calls libopus silk_NLSF2A.
func SilkNLSF2A(nlsf []int16, order int) []int16 {
	if len(nlsf) < order || order <= 0 {
		return nil
	}
	out := make([]int16, 16)
	cNLSF := (*C.opus_int16)(unsafe.Pointer(&nlsf[0]))
	cOut := (*C.opus_int16)(unsafe.Pointer(&out[0]))
	C.test_silk_nlsf2a(cNLSF, C.int(order), cOut)
	return out
}

// SilkDecodeCore calls libopus silk_decode_core with provided state/control.
func SilkDecodeCore(
	fsKHz, nbSubfr, frameLength, subfrLength, ltpMemLength, lpcOrder int,
	prevGainQ16 int32, lossCnt, prevSignalType int,
	signalType, quantOffsetType, nlsfInterpCoefQ2, seed int8,
	outBuf []int16, sLPCQ14Buf []int32,
	gainsQ16 []int32, predCoefQ12 []int16, ltpCoefQ14 []int16, pitchL []int, ltpScaleQ14 int32,
	pulses []int16,
) []int16 {
	if frameLength <= 0 {
		return nil
	}
	out := make([]int16, frameLength)
	if len(outBuf) == 0 || len(sLPCQ14Buf) == 0 || len(gainsQ16) == 0 || len(predCoefQ12) == 0 || len(ltpCoefQ14) == 0 || len(pitchL) == 0 || len(pulses) == 0 {
		return out
	}

	cOutBuf := (*C.opus_int16)(unsafe.Pointer(&outBuf[0]))
	cSLPC := (*C.opus_int32)(unsafe.Pointer(&sLPCQ14Buf[0]))
	cGains := (*C.opus_int32)(unsafe.Pointer(&gainsQ16[0]))
	cPred := (*C.opus_int16)(unsafe.Pointer(&predCoefQ12[0]))
	cLtp := (*C.opus_int16)(unsafe.Pointer(&ltpCoefQ14[0]))
	cPulses := (*C.opus_int16)(unsafe.Pointer(&pulses[0]))
	cOut := (*C.opus_int16)(unsafe.Pointer(&out[0]))

	cPitch := make([]C.int, len(pitchL))
	for i, v := range pitchL {
		cPitch[i] = C.int(v)
	}

	C.test_silk_decode_core(
		C.int(fsKHz), C.int(nbSubfr), C.int(frameLength), C.int(subfrLength), C.int(ltpMemLength), C.int(lpcOrder),
		C.opus_int32(prevGainQ16), C.int(lossCnt), C.int(prevSignalType),
		C.opus_int8(signalType), C.opus_int8(quantOffsetType), C.opus_int8(nlsfInterpCoefQ2), C.opus_int8(seed),
		cOutBuf, cSLPC,
		cGains, cPred, cLtp, (*C.int)(unsafe.Pointer(&cPitch[0])), C.int(ltpScaleQ14),
		cPulses, cOut,
	)
	return out
}

// SilkDecodedFrame holds decoded indices and pulses for a single frame.
type SilkDecodedFrame struct {
	GainsIndices    [4]int8
	LTPIndex        [4]int8
	NLSFIndices     [17]int8
	LagIndex        int16
	ContourIndex    int8
	SignalType      int8
	QuantOffsetType int8
	NLSFInterpCoef  int8
	PERIndex        int8
	LTPScaleIndex   int8
	Seed            int8
	Pulses          []int16
}

// SilkDecodeIndicesPulses decodes indices and pulses for a frame using libopus.
func SilkDecodeIndicesPulses(
	data []byte,
	fsKHz, nbSubfr, framesPerPacket, frameIndex, frameLength int,
) (*SilkDecodedFrame, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var out SilkDecodedFrame
	out.Pulses = make([]int16, frameLength)

	cData := (*C.uchar)(unsafe.Pointer(&data[0]))
	ret := C.test_silk_decode_indices_pulses(
		cData, C.int(len(data)),
		C.int(fsKHz), C.int(nbSubfr), C.int(framesPerPacket), C.int(frameIndex),
		(*C.opus_int8)(unsafe.Pointer(&out.GainsIndices[0])),
		(*C.opus_int8)(unsafe.Pointer(&out.LTPIndex[0])),
		(*C.opus_int8)(unsafe.Pointer(&out.NLSFIndices[0])),
		(*C.opus_int16)(unsafe.Pointer(&out.LagIndex)),
		(*C.opus_int8)(unsafe.Pointer(&out.ContourIndex)),
		(*C.opus_int8)(unsafe.Pointer(&out.SignalType)),
		(*C.opus_int8)(unsafe.Pointer(&out.QuantOffsetType)),
		(*C.opus_int8)(unsafe.Pointer(&out.NLSFInterpCoef)),
		(*C.opus_int8)(unsafe.Pointer(&out.PERIndex)),
		(*C.opus_int8)(unsafe.Pointer(&out.LTPScaleIndex)),
		(*C.opus_int8)(unsafe.Pointer(&out.Seed)),
		(*C.opus_int16)(unsafe.Pointer(&out.Pulses[0])),
		C.int(len(out.Pulses)),
	)
	if ret != 0 {
		return nil, nil
	}
	return &out, nil
}

// SilkDecodeLBRRIndicesPulses decodes LBRR indices and pulses for a frame using libopus.
func SilkDecodeLBRRIndicesPulses(
	data []byte,
	fsKHz, nbSubfr, framesPerPacket, frameIndex, frameLength int,
) (*SilkDecodedFrame, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var out SilkDecodedFrame
	out.Pulses = make([]int16, frameLength)

	cData := (*C.uchar)(unsafe.Pointer(&data[0]))
	ret := C.test_silk_decode_lbrr_indices_pulses(
		cData, C.int(len(data)),
		C.int(fsKHz), C.int(nbSubfr), C.int(framesPerPacket), C.int(frameIndex),
		(*C.opus_int8)(unsafe.Pointer(&out.GainsIndices[0])),
		(*C.opus_int8)(unsafe.Pointer(&out.LTPIndex[0])),
		(*C.opus_int8)(unsafe.Pointer(&out.NLSFIndices[0])),
		(*C.opus_int16)(unsafe.Pointer(&out.LagIndex)),
		(*C.opus_int8)(unsafe.Pointer(&out.ContourIndex)),
		(*C.opus_int8)(unsafe.Pointer(&out.SignalType)),
		(*C.opus_int8)(unsafe.Pointer(&out.QuantOffsetType)),
		(*C.opus_int8)(unsafe.Pointer(&out.NLSFInterpCoef)),
		(*C.opus_int8)(unsafe.Pointer(&out.PERIndex)),
		(*C.opus_int8)(unsafe.Pointer(&out.LTPScaleIndex)),
		(*C.opus_int8)(unsafe.Pointer(&out.Seed)),
		(*C.opus_int16)(unsafe.Pointer(&out.Pulses[0])),
		C.int(len(out.Pulses)),
	)
	if ret != 0 {
		return nil, nil
	}
	return &out, nil
}

// SilkDecodePulsesOnly decodes a pulse-only payload using libopus.
func SilkDecodePulsesOnly(data []byte, signalType, quantOffsetType, frameLength int) ([]int16, error) {
	if len(data) == 0 {
		return nil, nil
	}
	pulsesLen := (frameLength + 15) & ^15
	if pulsesLen < frameLength {
		pulsesLen = frameLength
	}
	out := make([]int16, pulsesLen)
	cData := (*C.uchar)(unsafe.Pointer(&data[0]))
	ret := C.test_silk_decode_pulses_only(
		cData, C.int(len(data)),
		C.int(signalType), C.int(quantOffsetType), C.int(frameLength),
		(*C.opus_int16)(unsafe.Pointer(&out[0])),
		C.int(len(out)),
	)
	if ret != 0 {
		return nil, nil
	}
	return out[:frameLength], nil
}

// SilkDecodeLBRRIndexBits returns the number of bits consumed after LBRR indices decode.
func SilkDecodeLBRRIndexBits(data []byte, fsKHz, nbSubfr, framesPerPacket, frameIndex int) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	var bits C.int
	cData := (*C.uchar)(unsafe.Pointer(&data[0]))
	ret := C.test_silk_decode_lbrr_indices_bits(
		cData, C.int(len(data)),
		C.int(fsKHz), C.int(nbSubfr), C.int(framesPerPacket), C.int(frameIndex),
		&bits,
	)
	if ret != 0 {
		return 0, nil
	}
	return int(bits), nil
}

// SilkDecodeLBRRIndexState returns the libopus range decoder state after LBRR indices.
func SilkDecodeLBRRIndexState(data []byte, fsKHz, nbSubfr, framesPerPacket, frameIndex int) (uint32, uint32, error) {
	if len(data) == 0 {
		return 0, 0, nil
	}
	var rng C.opus_uint32
	var val C.opus_uint32
	cData := (*C.uchar)(unsafe.Pointer(&data[0]))
	ret := C.test_silk_decode_lbrr_indices_state(
		cData, C.int(len(data)),
		C.int(fsKHz), C.int(nbSubfr), C.int(framesPerPacket), C.int(frameIndex),
		&rng, &val,
	)
	if ret != 0 {
		return 0, 0, nil
	}
	return uint32(rng), uint32(val), nil
}

// SilkDecoderState wraps a persistent libopus silk_decoder_state.
type SilkDecoderState struct {
	ptr *C.silk_decoder_state
}

// NewSilkDecoderState creates a persistent SILK decoder state (libopus).
func NewSilkDecoderState() *SilkDecoderState {
	ptr := C.test_silk_decoder_state_create()
	if ptr == nil {
		return nil
	}
	return &SilkDecoderState{ptr: ptr}
}

// Free releases the decoder state.
func (s *SilkDecoderState) Free() {
	if s == nil || s.ptr == nil {
		return
	}
	C.test_silk_decoder_state_destroy(s.ptr)
	s.ptr = nil
}

// DecodePacketNativeCore decodes a packet to native samples using libopus core path.
// data should be the SILK payload (without TOC).
func (s *SilkDecoderState) DecodePacketNativeCore(data []byte, fsKHz, nbSubfr, framesPerPacket int) ([]int16, error) {
	if s == nil || s.ptr == nil || len(data) == 0 {
		return nil, nil
	}
	frameLength := nbSubfr * 5 * fsKHz
	if frameLength <= 0 {
		return nil, nil
	}
	out := make([]int16, framesPerPacket*frameLength)
	ret := C.test_silk_decode_packet_native_core_state(
		s.ptr,
		(*C.uchar)(unsafe.Pointer(&data[0])),
		C.int(len(data)),
		C.int(fsKHz),
		C.int(nbSubfr),
		C.int(framesPerPacket),
		(*C.opus_int16)(unsafe.Pointer(&out[0])),
		C.int(len(out)),
	)
	if ret != 0 {
		return nil, nil
	}
	return out, nil
}

// SilkPostprocState wraps libopus resampler + sMid buffering for mono.
type SilkPostprocState struct {
	ptr *C.silk_postproc_state
}

// NewSilkPostprocState creates a post-processing state for native->API resampling.
func NewSilkPostprocState(fsKHz, fsAPIHz int) *SilkPostprocState {
	ptr := C.test_silk_postproc_create(C.int(fsKHz), C.int(fsAPIHz))
	if ptr == nil {
		return nil
	}
	return &SilkPostprocState{ptr: ptr}
}

// Free releases the post-processing state.
func (s *SilkPostprocState) Free() {
	if s == nil || s.ptr == nil {
		return
	}
	C.test_silk_postproc_destroy(s.ptr)
	s.ptr = nil
}

// ResetResampler reinitializes the resampler for a new input/output rate while preserving sMid.
func (s *SilkPostprocState) ResetResampler(fsKHz, fsAPIHz int) {
	if s == nil || s.ptr == nil {
		return
	}
	C.test_silk_postproc_reset_resampler(s.ptr, C.int(fsKHz), C.int(fsAPIHz))
}

// SetSMid sets the sMid state in the post-processing chain.
func (s *SilkPostprocState) SetSMid(s0, s1 int16) {
	if s == nil || s.ptr == nil {
		return
	}
	C.test_silk_postproc_set_smid(s.ptr, C.opus_int16(s0), C.opus_int16(s1))
}

// GetSMid returns the current sMid state from the post-processing chain.
func (s *SilkPostprocState) GetSMid() (int16, int16) {
	if s == nil || s.ptr == nil {
		return 0, 0
	}
	var s0, s1 C.opus_int16
	C.test_silk_postproc_get_smid(s.ptr, &s0, &s1)
	return int16(s0), int16(s1)
}

// ProcessFrame resamples a native frame to API rate using libopus logic.
func (s *SilkPostprocState) ProcessFrame(frame []int16) ([]int16, int) {
	if s == nil || s.ptr == nil || len(frame) == 0 {
		return nil, -1
	}
	// Max output length for 48k is 6x for 8k, 4x for 12k, 3x for 16k.
	// Allocate a generous buffer.
	out := make([]int16, len(frame)*6+8)
	n := int(C.test_silk_postproc_frame(
		s.ptr,
		(*C.opus_int16)(unsafe.Pointer(&frame[0])),
		C.int(len(frame)),
		(*C.opus_int16)(unsafe.Pointer(&out[0])),
		C.int(len(out)),
	))
	if n <= 0 {
		return nil, n
	}
	return out[:n], n
}

// SilkFrameParams mirrors libopus decoder control parameters for one frame.
type SilkFrameParams struct {
	GainsQ16         [4]int32
	PredCoefQ12      [2][16]int16
	LTPCoefQ14       [20]int16
	PitchL           [4]int32
	LTPScaleQ14      int32
	NLSFIndices      [17]int8
	NLSFInterpCoefQ2 int8
}

// DecodePacketParams decodes a packet and returns per-frame parameters.
// data should be the SILK payload (without TOC).
func (s *SilkDecoderState) DecodePacketParams(data []byte, fsKHz, nbSubfr, framesPerPacket int) ([]SilkFrameParams, error) {
	if s == nil || s.ptr == nil || len(data) == 0 {
		return nil, nil
	}
	out := make([]SilkFrameParams, framesPerPacket)
	ret := C.test_silk_decode_packet_params_state(
		s.ptr,
		(*C.uchar)(unsafe.Pointer(&data[0])),
		C.int(len(data)),
		C.int(fsKHz),
		C.int(nbSubfr),
		C.int(framesPerPacket),
		(*C.silk_frame_params)(unsafe.Pointer(&out[0])),
		C.int(len(out)),
	)
	if ret <= 0 {
		return nil, nil
	}
	return out, nil
}

// CombFilter calls libopus comb_filter function.
// x is the input buffer (includes history), y is the output buffer.
// history is the offset where the actual frame data starts.
// T0, T1 are the old and new pitch periods.
// g0, g1 are the old and new gains.
// tapset0, tapset1 are the old and new tapsets.
// window is the Vorbis window for crossfade.
// n is the number of samples to process.
// overlap is the crossfade length.
func CombFilter(x []float32, history, T0, T1, n int, g0, g1 float32, tapset0, tapset1 int, window []float32, overlap int) []float32 {
	y := make([]float32, len(x))
	copy(y, x) // libopus comb_filter modifies y in-place

	cY := (*C.float)(unsafe.Pointer(&y[0]))
	cWindow := (*C.float)(unsafe.Pointer(&window[0]))

	// Pass y for both input and output to match the in-place usage in the decoder.
	C.test_comb_filter(cY, cY, C.int(history), C.int(T0), C.int(T1), C.int(n),
		C.float(g0), C.float(g1), C.int(tapset0), C.int(tapset1),
		cWindow, C.int(overlap))

	return y
}

// VorbisWindow computes the Vorbis window value using libopus formula.
func VorbisWindow(i, overlap int) float32 {
	return float32(C.test_vorbis_window(C.int(i), C.int(overlap)))
}

// LibopusDecoder wraps an opus decoder for comparison tests.
type LibopusDecoder struct {
	dec *C.OpusDecoder
}

// NewLibopusDecoder creates a new libopus decoder.
func NewLibopusDecoder(sampleRate, channels int) (*LibopusDecoder, error) {
	var err C.int
	dec := C.test_decoder_create(C.int(sampleRate), C.int(channels), &err)
	if err != 0 || dec == nil {
		return nil, nil // Return nil to indicate failure
	}
	return &LibopusDecoder{dec: dec}, nil
}

// Destroy frees the decoder resources.
func (d *LibopusDecoder) Destroy() {
	if d.dec != nil {
		C.test_decoder_destroy(d.dec)
		d.dec = nil
	}
}

// SetLibopusDebugRange toggles libopus internal trace output.
// When disabled, CELT debug prints are suppressed.
func SetLibopusDebugRange(enabled bool) {
	v := C.int(0)
	if enabled {
		v = 1
	}
	C.opus_set_debug_range(v)
}

// FlushLibopusTrace flushes stdio streams to ensure trace output is captured.
func FlushLibopusTrace() {
	C.opus_flush_stdio()
}

// DecodeFloat decodes a packet to float32 samples.
func (d *LibopusDecoder) DecodeFloat(data []byte, maxSamples int) ([]float32, int) {
	if d.dec == nil || len(data) == 0 {
		return nil, -1
	}

	pcm := make([]float32, maxSamples*2) // stereo
	cData := (*C.uchar)(unsafe.Pointer(&data[0]))
	cPcm := (*C.float)(unsafe.Pointer(&pcm[0]))

	samples := int(C.test_decode_float(d.dec, cData, C.int(len(data)), cPcm, C.int(maxSamples)))
	if samples < 0 {
		return nil, samples
	}
	return pcm, samples
}

// GetPreemphState returns the de-emphasis filter state (mem0, mem1) from the internal CELT decoder.
// This is useful for debugging state drift between gopus and libopus.
func (d *LibopusDecoder) GetPreemphState() (float32, float32) {
	if d.dec == nil {
		return 0, 0
	}
	var mem0, mem1 C.float
	C.test_get_preemph_state(d.dec, &mem0, &mem1)
	return float32(mem0), float32(mem1)
}

// GetCELTOverlap returns the CELT overlap parameter to verify structure alignment.
func (d *LibopusDecoder) GetCELTOverlap() int {
	if d.dec == nil {
		return -1
	}
	return int(C.test_get_celt_overlap(d.dec))
}

// GetCELTChannels returns the CELT channel count to verify structure alignment.
func (d *LibopusDecoder) GetCELTChannels() int {
	if d.dec == nil {
		return -1
	}
	return int(C.test_get_celt_channels(d.dec))
}

// GetPrevRedundancy returns the prev_redundancy state from libopus.
func (d *LibopusDecoder) GetPrevRedundancy() int {
	if d.dec == nil {
		return -1
	}
	return int(C.test_get_prev_redundancy(d.dec))
}

// GetPrevMode returns the prev_mode state from libopus.
func (d *LibopusDecoder) GetPrevMode() int {
	if d.dec == nil {
		return -1
	}
	return int(C.test_get_prev_mode(d.dec))
}

// CELTMode wraps a libopus CELT mode for MDCT tests.
type CELTMode struct {
	mode *C.CELTMode
}

// GetCELTMode48000_960 returns the standard CELT mode for 48kHz/960 samples.
func GetCELTMode48000_960() *CELTMode {
	mode := C.test_get_celt_mode_48000_960()
	if mode == nil {
		return nil
	}
	return &CELTMode{mode: mode}
}

// Overlap returns the overlap size for this mode.
func (m *CELTMode) Overlap() int {
	return int(C.test_get_mode_overlap(m.mode))
}

// MDCTSize returns the MDCT size for a given shift value.
// shift: 0=1920, 1=960, 2=480, 3=240
func (m *CELTMode) MDCTSize(shift int) int {
	return int(C.test_get_mdct_size(m.mode, C.int(shift)))
}

// MDCTForward computes forward MDCT using libopus.
// Input: n2 + overlap time samples
// Output: n2 frequency coefficients
// shift: 0=1920, 1=960, 2=480, 3=240 (MDCT size = 1920 >> shift)
func (m *CELTMode) MDCTForward(input []float32, shift int) []float32 {
	nfft := m.MDCTSize(shift)
	n2 := nfft / 2
	output := make([]float32, n2)

	cIn := (*C.float)(unsafe.Pointer(&input[0]))
	cOut := (*C.float)(unsafe.Pointer(&output[0]))
	C.test_mdct_forward(m.mode, cIn, cOut, C.int(shift))

	return output
}

// IMDCTBackward computes inverse MDCT using libopus.
// Input: n2 frequency coefficients
// Output: n2 + overlap time samples
// shift: 0=1920, 1=960, 2=480, 3=240 (MDCT size = 1920 >> shift)
func (m *CELTMode) IMDCTBackward(input []float32, shift int) []float32 {
	nfft := m.MDCTSize(shift)
	n2 := nfft / 2
	overlap := m.Overlap()
	output := make([]float32, n2+overlap)

	cIn := (*C.float)(unsafe.Pointer(&input[0]))
	cOut := (*C.float)(unsafe.Pointer(&output[0]))
	C.test_imdct_backward(m.mode, cIn, cOut, C.int(shift))

	return output
}

// GetWindow returns the Vorbis window values for the mode's overlap.
func (m *CELTMode) GetWindow() []float32 {
	overlap := m.Overlap()
	cWindow := C.test_get_mode_window(m.mode)

	window := make([]float32, overlap)
	for i := 0; i < overlap; i++ {
		// Access C array directly
		window[i] = float32(*(*C.float)(unsafe.Pointer(uintptr(unsafe.Pointer(cWindow)) + uintptr(i)*unsafe.Sizeof(*cWindow))))
	}
	return window
}

// GetMDCTTwiddles returns the twiddle factors for MDCT at the given shift.
func (m *CELTMode) GetMDCTTwiddles(shift int) []float32 {
	nfft := m.MDCTSize(shift)
	n2 := nfft / 2
	trig := make([]float32, n2)
	C.test_get_mdct_twiddles(m.mode, C.int(shift), (*C.float)(unsafe.Pointer(&trig[0])))
	return trig
}

// GetFFTBitrev returns the FFT bit-reversal table for the given shift.
func (m *CELTMode) GetFFTBitrev(shift int) []int16 {
	nfft := m.MDCTSize(shift)
	n4 := nfft / 4
	bitrev := make([]int16, n4)
	C.test_get_fft_bitrev(m.mode, C.int(shift), (*C.int16_t)(unsafe.Pointer(&bitrev[0])), C.int(n4))
	return bitrev
}

// GetFFTScale returns the FFT scale factor (1.0/n4 for float).
func (m *CELTMode) GetFFTScale(shift int) float32 {
	return float32(C.test_get_fft_scale(m.mode, C.int(shift)))
}

// GetCELTWindow returns the CELT mode window values.
func (m *CELTMode) GetCELTWindow() []float32 {
	overlap := m.Overlap()
	window := make([]float32, overlap)
	C.test_get_celt_window(m.mode, (*C.float)(unsafe.Pointer(&window[0])))
	return window
}

// MDCTForwardStage computes a specific stage of the MDCT forward transform.
// Stages:
//   - 0: window/fold output (size n2)
//   - 1: pre-rotation output in sequential order (size n4*2)
//   - 2: FFT input in bitrev order (size n4*2)
//   - 3: FFT output (size n4*2)
//   - 4: final MDCT coefficients (size n2)
func (m *CELTMode) MDCTForwardStage(input []float32, shift int, stage int) []float32 {
	nfft := m.MDCTSize(shift)
	n2 := nfft / 2
	n4 := nfft / 4

	var outputSize int
	switch stage {
	case 0, 4:
		outputSize = n2
	case 1, 2, 3:
		outputSize = n4 * 2
	default:
		return nil
	}

	output := make([]float32, outputSize)
	cIn := (*C.float)(unsafe.Pointer(&input[0]))
	cOut := (*C.float)(unsafe.Pointer(&output[0]))
	C.test_mdct_forward_stages(m.mode, cIn, cOut, C.int(shift), C.int(stage))
	return output
}

// SilkLPCAnalysisFilter calls libopus silk_LPC_analysis_filter.
func SilkLPCAnalysisFilter(in, B []int16, length, order int) []int16 {
	out := make([]int16, length)
	cOut := (*C.opus_int16)(unsafe.Pointer(&out[0]))
	cIn := (*C.opus_int16)(unsafe.Pointer(&in[0]))
	cB := (*C.opus_int16)(unsafe.Pointer(&B[0]))
	C.test_lpc_analysis_filter(cOut, cIn, cB, C.int(length), C.int(order))
	return out
}

// TestSilkSMLABB calls libopus silk_SMLABB.
func TestSilkSMLABB(a, b, c int32) int32 {
	return int32(C.test_silk_SMLABB(C.opus_int32(a), C.opus_int32(b), C.opus_int32(c)))
}

// TestSilkSMLABBOvflw calls libopus silk_SMLABB_ovflw.
func TestSilkSMLABBOvflw(a, b, c int32) int32 {
	return int32(C.test_silk_SMLABB_ovflw(C.opus_int32(a), C.opus_int32(b), C.opus_int32(c)))
}

// TestSilkADD32Ovflw calls libopus silk_ADD32_ovflw.
func TestSilkADD32Ovflw(a, b int32) int32 {
	return int32(C.test_silk_ADD32_ovflw(C.opus_int32(a), C.opus_int32(b)))
}

// TestSilkSMULBB calls libopus silk_SMULBB.
func TestSilkSMULBB(a, b int32) int32 {
	return int32(C.test_silk_SMULBB(C.opus_int32(a), C.opus_int32(b)))
}

// TestSilkSMULWB calls libopus silk_SMULWB.
func TestSilkSMULWB(a, b int32) int32 {
	return int32(C.test_silk_SMULWB(C.opus_int32(a), C.opus_int32(b)))
}

// TestSilkSMLAWB calls libopus silk_SMLAWB.
func TestSilkSMLAWB(a, b, c int32) int32 {
	return int32(C.test_silk_SMLAWB(C.opus_int32(a), C.opus_int32(b), C.opus_int32(c)))
}

// TestSilkDIV32VarQ calls libopus silk_DIV32_varQ.
func TestSilkDIV32VarQ(a, b int32, qres int) int32 {
	return int32(C.test_silk_DIV32_varQ(C.opus_int32(a), C.opus_int32(b), C.int(qres)))
}

// TestSilkINVERSE32VarQ calls libopus silk_INVERSE32_varQ.
func TestSilkINVERSE32VarQ(b int32, qres int) int32 {
	return int32(C.test_silk_INVERSE32_varQ(C.opus_int32(b), C.int(qres)))
}

// TestSilkSMULWW calls libopus silk_SMULWW.
func TestSilkSMULWW(a, b int32) int32 {
	return int32(C.test_silk_SMULWW(C.opus_int32(a), C.opus_int32(b)))
}

// TestSilkSMLAWW calls libopus silk_SMLAWW.
func TestSilkSMLAWW(a, b, c int32) int32 {
	return int32(C.test_silk_SMLAWW(C.opus_int32(a), C.opus_int32(b), C.opus_int32(c)))
}

// SilkNLSFStateFrame holds NLSF state for one frame from libopus.
type SilkNLSFStateFrame struct {
	PrevNLSFQ15      []int16 // prevNLSF_Q15 before this frame's decode
	CurrNLSFQ15      []int16 // current NLSF (after decode)
	InterpNLSFQ15    []int16 // interpolated NLSF (if interp active, else same as curr)
	PredCoef0Q12     []int16 // PredCoef_Q12[0] (used for first half of frame)
	PredCoef1Q12     []int16 // PredCoef_Q12[1] (used for second half of frame)
	NLSFInterpCoefQ2 int8    // interpolation coefficient
}

// SilkDecodeNLSFState decodes a packet and returns per-frame NLSF/LPC state.
func SilkDecodeNLSFState(data []byte, fsKHz, nbSubfr, framesPerPacket, framesToDecode, lpcOrder int) ([]SilkNLSFStateFrame, error) {
	if len(data) == 0 || framesToDecode <= 0 || framesToDecode > framesPerPacket {
		return nil, nil
	}

	maxLPCOrder := 16
	maxFrames := 3

	// Allocate output arrays
	prevNLSF := make([]int16, maxFrames*maxLPCOrder)
	currNLSF := make([]int16, maxFrames*maxLPCOrder)
	interpNLSF := make([]int16, maxFrames*maxLPCOrder)
	predCoef0 := make([]int16, maxFrames*maxLPCOrder)
	predCoef1 := make([]int16, maxFrames*maxLPCOrder)
	nlsfInterp := make([]int8, maxFrames)

	cData := (*C.uchar)(unsafe.Pointer(&data[0]))
	ret := C.test_silk_decode_nlsf_state(
		cData, C.int(len(data)),
		C.int(fsKHz), C.int(nbSubfr), C.int(framesPerPacket), C.int(framesToDecode),
		(*C.opus_int16)(unsafe.Pointer(&prevNLSF[0])),
		(*C.opus_int16)(unsafe.Pointer(&currNLSF[0])),
		(*C.opus_int16)(unsafe.Pointer(&interpNLSF[0])),
		(*C.opus_int16)(unsafe.Pointer(&predCoef0[0])),
		(*C.opus_int16)(unsafe.Pointer(&predCoef1[0])),
		(*C.opus_int8)(unsafe.Pointer(&nlsfInterp[0])),
		C.int(lpcOrder),
	)

	if ret != 0 {
		return nil, nil
	}

	result := make([]SilkNLSFStateFrame, framesToDecode)
	for i := 0; i < framesToDecode; i++ {
		off := i * maxLPCOrder
		result[i] = SilkNLSFStateFrame{
			PrevNLSFQ15:      make([]int16, lpcOrder),
			CurrNLSFQ15:      make([]int16, lpcOrder),
			InterpNLSFQ15:    make([]int16, lpcOrder),
			PredCoef0Q12:     make([]int16, lpcOrder),
			PredCoef1Q12:     make([]int16, lpcOrder),
			NLSFInterpCoefQ2: nlsfInterp[i],
		}
		copy(result[i].PrevNLSFQ15, prevNLSF[off:off+lpcOrder])
		copy(result[i].CurrNLSFQ15, currNLSF[off:off+lpcOrder])
		copy(result[i].InterpNLSFQ15, interpNLSF[off:off+lpcOrder])
		copy(result[i].PredCoef0Q12, predCoef0[off:off+lpcOrder])
		copy(result[i].PredCoef1Q12, predCoef1[off:off+lpcOrder])
	}

	return result, nil
}

// GetSilkOutBufState gets outBuf state from libopus after decoding frames.
func GetSilkOutBufState(data []byte, fsKHz, nbSubfr, framesPerPacket, frameIndex int) ([]int16, []int32, int32, error) {
	if len(data) == 0 {
		return nil, nil, 0, nil
	}

	outBuf := make([]int16, 480) // MAX_DECODER_BUF_LENGTH
	sLPCQ14Buf := make([]int32, 16)
	var prevGainQ16 int32

	cData := (*C.uchar)(unsafe.Pointer(&data[0]))
	ret := C.test_silk_get_outbuf_state(
		cData, C.int(len(data)),
		C.int(fsKHz), C.int(nbSubfr), C.int(framesPerPacket), C.int(frameIndex),
		(*C.opus_int16)(unsafe.Pointer(&outBuf[0])), C.int(len(outBuf)),
		(*C.opus_int32)(unsafe.Pointer(&sLPCQ14Buf[0])), C.int(len(sLPCQ14Buf)),
		(*C.opus_int32)(unsafe.Pointer(&prevGainQ16)),
	)
	if ret != 0 {
		return nil, nil, 0, nil
	}

	return outBuf, sLPCQ14Buf, prevGainQ16, nil
}

// ====================================================================
// Allocation comparison CGO wrappers
// ====================================================================

// LibopusComputeAllocation calls libopus clt_compute_allocation via CGO.
func LibopusComputeAllocation(
	start, end int,
	offsets, cap []int,
	allocTrim int,
	intensity, dualStereo int,
	totalBitsQ3 int,
	channels, lm int,
	prev, signalBandwidth int,
) (codedBands, balance int, pulses, ebits, finePriority []int, intensityOut, dualStereoOut int) {
	nbBands := end - start
	if nbBands <= 0 {
		return 0, 0, nil, nil, nil, 0, 0
	}

	// Create C arrays
	cOffsets := make([]C.int, end)
	cCap := make([]C.int, end)
	cPulses := make([]C.int, end)
	cEbits := make([]C.int, end)
	cFinePriority := make([]C.int, end)

	for i := start; i < end; i++ {
		if offsets != nil && i < len(offsets) {
			cOffsets[i] = C.int(offsets[i])
		}
		if cap != nil && i < len(cap) {
			cCap[i] = C.int(cap[i])
		}
	}

	var cIntensity C.int = C.int(intensity)
	var cDualStereo C.int = C.int(dualStereo)
	var cBalance C.int

	cb := C.test_clt_compute_allocation(
		C.int(start), C.int(end),
		(*C.int)(unsafe.Pointer(&cOffsets[0])),
		(*C.int)(unsafe.Pointer(&cCap[0])),
		C.int(allocTrim),
		&cIntensity,
		&cDualStereo,
		C.int(totalBitsQ3),
		&cBalance,
		(*C.int)(unsafe.Pointer(&cPulses[0])),
		(*C.int)(unsafe.Pointer(&cEbits[0])),
		(*C.int)(unsafe.Pointer(&cFinePriority[0])),
		C.int(channels),
		C.int(lm),
		C.int(prev),
		C.int(signalBandwidth),
	)

	codedBands = int(cb)
	balance = int(cBalance)
	intensityOut = int(cIntensity)
	dualStereoOut = int(cDualStereo)

	pulses = make([]int, end)
	ebits = make([]int, end)
	finePriority = make([]int, end)
	for i := 0; i < end; i++ {
		pulses[i] = int(cPulses[i])
		ebits[i] = int(cEbits[i])
		finePriority[i] = int(cFinePriority[i])
	}

	return
}

// LibopusGetEBands returns the eBands array from libopus mode.
func LibopusGetEBands() []int {
	out := make([]C.int, 22)
	C.test_get_ebands((*C.int)(unsafe.Pointer(&out[0])), C.int(22))
	result := make([]int, 22)
	for i := 0; i < 22; i++ {
		result[i] = int(out[i])
	}
	return result
}

// LibopusGetLogN returns the logN array from libopus mode.
func LibopusGetLogN() []int {
	out := make([]C.int, 21)
	C.test_get_logN((*C.int)(unsafe.Pointer(&out[0])), C.int(21))
	result := make([]int, 21)
	for i := 0; i < 21; i++ {
		result[i] = int(out[i])
	}
	return result
}

// LibopusComputeCaps computes caps using libopus logic.
func LibopusComputeCaps(nbBands, lm, channels int) []int {
	out := make([]C.int, nbBands)
	C.test_compute_caps((*C.int)(unsafe.Pointer(&out[0])), C.int(nbBands), C.int(lm), C.int(channels))
	result := make([]int, nbBands)
	for i := 0; i < nbBands; i++ {
		result[i] = int(out[i])
	}
	return result
}

// LibopusGetAllocVectors returns a single allocation vector row from libopus.
func LibopusGetAllocVectors(row int) []int {
	out := make([]C.int, 21)
	C.test_get_alloc_vectors((*C.int)(unsafe.Pointer(&out[0])), C.int(row), C.int(21))
	result := make([]int, 21)
	for i := 0; i < 21; i++ {
		result[i] = int(out[i])
	}
	return result
}

// LibopusGetNbAllocVectors returns the number of allocation vectors from libopus.
func LibopusGetNbAllocVectors() int {
	return int(C.test_get_nb_alloc_vectors())
}

// ====================================================================
// PVQ Search comparison wrappers
// ====================================================================

// LibopusEncodeUniformSequence encodes a sequence of uniform values using libopus range encoder.
// Returns the encoded bytes.
func LibopusEncodeUniformSequence(vals []uint32, fts []uint32) ([]byte, error) {
	if len(vals) != len(fts) || len(vals) == 0 {
		return nil, nil
	}

	maxSize := 4096
	outBuf := make([]byte, maxSize)
	var outLen C.int

	err := C.test_encode_uniform_sequence(
		(*C.uchar)(unsafe.Pointer(&outBuf[0])),
		C.int(maxSize),
		(*C.uint)(unsafe.Pointer(&vals[0])),
		(*C.uint)(unsafe.Pointer(&fts[0])),
		C.int(len(vals)),
		&outLen,
	)

	if err != 0 {
		return nil, nil
	}

	return outBuf[:int(outLen)], nil
}

// LibopusEncodeUniformDetailed encodes a single uniform value and returns detailed state.
// Returns (bytes, offs, end_offs, nend_bits).
func LibopusEncodeUniformDetailed(val uint32, ft uint32) ([]byte, int, int, int) {
	maxSize := 4096
	outBuf := make([]byte, maxSize)
	var outLen, offs, endOffs, nendBits C.int

	err := C.test_encode_uniform_detailed(
		(*C.uchar)(unsafe.Pointer(&outBuf[0])),
		C.int(maxSize),
		C.uint(val),
		C.uint(ft),
		&outLen,
		&offs,
		&endOffs,
		&nendBits,
	)

	if err != 0 {
		return nil, 0, 0, 0
	}

	return outBuf[:int(outLen)], int(offs), int(endOffs), int(nendBits)
}

// LibopusEncodePulsesToBytes encodes pulses using libopus CWRS encoder.
// Returns the encoded bytes.
func LibopusEncodePulsesToBytes(pulses []int, n, k int) ([]byte, error) {
	if len(pulses) != n || k <= 0 {
		return nil, nil
	}

	maxSize := 4096
	outBuf := make([]byte, maxSize)
	pulsesInt32 := make([]int32, n)
	for i, p := range pulses {
		pulsesInt32[i] = int32(p)
	}

	var outLen C.int

	err := C.test_encode_pulses_to_bytes(
		(*C.uchar)(unsafe.Pointer(&outBuf[0])),
		C.int(maxSize),
		(*C.int)(unsafe.Pointer(&pulsesInt32[0])),
		C.int(n),
		C.int(k),
		&outLen,
	)

	if err != 0 {
		return nil, nil
	}

	return outBuf[:int(outLen)], nil
}

// LibopusPVQSearch calls libopus op_pvq_search via CGO.
// Input x is copied since libopus modifies it (removes signs).
// Returns the pulse vector and yy (sum of y^2).
func LibopusPVQSearch(x []float64, k int) ([]int, float64) {
	n := len(x)
	if n == 0 || k <= 0 {
		return make([]int, n), 0
	}

	// Convert to float32 for C (libopus float path uses float)
	xCopy := make([]float32, n)
	for i := range x {
		xCopy[i] = float32(x[i])
	}

	iy := make([]int32, n)

	yy := float64(C.test_op_pvq_search(
		(*C.float)(unsafe.Pointer(&xCopy[0])),
		(*C.int)(unsafe.Pointer(&iy[0])),
		C.int(k),
		C.int(n),
	))

	result := make([]int, n)
	for i := range iy {
		result[i] = int(iy[i])
	}
	return result, yy
}

// ====================================================================
// Encoder CGO wrappers
// ====================================================================

// Encoder CTL constants
const (
	OpusApplicationVoIP            = 2048
	OpusApplicationAudio           = 2049
	OpusApplicationRestrictedDelay = 2051

	OpusSetBitrateRequest       = 4002
	OpusSetComplexityRequest    = 4010
	OpusSetBandwidthRequest     = 4008
	OpusSetVBRRequest           = 4006
	OpusSetSignalRequest        = 4024
	OpusSetForceChannelsRequest = 4022

	OpusBandwidthNarrowband    = 1101
	OpusBandwidthMediumband    = 1102
	OpusBandwidthWideband      = 1103
	OpusBandwidthSuperwideband = 1104
	OpusBandwidthFullband      = 1105

	OpusSignalAuto  = -1000
	OpusSignalVoice = 3001
	OpusSignalMusic = 3002
)

// LibopusEncoder wraps a libopus encoder for comparison tests.
type LibopusEncoder struct {
	enc *C.OpusEncoder
}

// NewLibopusEncoder creates a new libopus encoder.
// application: OpusApplicationVoIP, OpusApplicationAudio, or OpusApplicationRestrictedDelay
func NewLibopusEncoder(sampleRate, channels, application int) (*LibopusEncoder, error) {
	var err C.int
	enc := C.test_encoder_create(C.int(sampleRate), C.int(channels), C.int(application), &err)
	if err != 0 || enc == nil {
		return nil, nil
	}
	return &LibopusEncoder{enc: enc}, nil
}

// Destroy frees the encoder resources.
func (e *LibopusEncoder) Destroy() {
	if e.enc != nil {
		C.test_encoder_destroy(e.enc)
		e.enc = nil
	}
}

// Reset resets the encoder state.
func (e *LibopusEncoder) Reset() {
	if e.enc != nil {
		C.test_encoder_reset(e.enc)
	}
}

// SetBitrate sets the target bitrate in bits per second.
func (e *LibopusEncoder) SetBitrate(bitrate int) {
	if e.enc != nil {
		C.test_encoder_ctl_set_int(e.enc, C.int(OpusSetBitrateRequest), C.int(bitrate))
	}
}

// SetComplexity sets the encoding complexity (0-10).
func (e *LibopusEncoder) SetComplexity(complexity int) {
	if e.enc != nil {
		C.test_encoder_ctl_set_int(e.enc, C.int(OpusSetComplexityRequest), C.int(complexity))
	}
}

// SetBandwidth sets the audio bandwidth.
func (e *LibopusEncoder) SetBandwidth(bandwidth int) {
	if e.enc != nil {
		C.test_encoder_ctl_set_int(e.enc, C.int(OpusSetBandwidthRequest), C.int(bandwidth))
	}
}

// SetVBR enables or disables VBR.
func (e *LibopusEncoder) SetVBR(enabled bool) {
	if e.enc != nil {
		v := 0
		if enabled {
			v = 1
		}
		C.test_encoder_ctl_set_int(e.enc, C.int(OpusSetVBRRequest), C.int(v))
	}
}

// SetSignal sets the signal type hint.
func (e *LibopusEncoder) SetSignal(signal int) {
	if e.enc != nil {
		C.test_encoder_ctl_set_int(e.enc, C.int(OpusSetSignalRequest), C.int(signal))
	}
}

// SetForceChannels forces mono or stereo encoding.
func (e *LibopusEncoder) SetForceChannels(channels int) {
	if e.enc != nil {
		C.test_encoder_ctl_set_int(e.enc, C.int(OpusSetForceChannelsRequest), C.int(channels))
	}
}

// GetFinalRange returns the final range coder state for verification.
func (e *LibopusEncoder) GetFinalRange() uint32 {
	if e.enc == nil {
		return 0
	}
	var rng C.opus_uint32
	C.test_encoder_get_final_range(e.enc, &rng)
	return uint32(rng)
}

// EncodeFloat encodes float32 samples.
func (e *LibopusEncoder) EncodeFloat(pcm []float32, frameSize int) ([]byte, int) {
	if e.enc == nil || len(pcm) == 0 {
		return nil, -1
	}

	maxBytes := 1275 // Maximum Opus packet size
	data := make([]byte, maxBytes)
	cPcm := (*C.float)(unsafe.Pointer(&pcm[0]))
	cData := (*C.uchar)(unsafe.Pointer(&data[0]))

	n := int(C.test_encode_float(e.enc, cPcm, C.int(frameSize), cData, C.int(maxBytes)))
	if n < 0 {
		return nil, n
	}

	return data[:n], n
}

// ====================================================================
// SILK LSF/NLSF Encoding Comparison Go Wrappers
// ====================================================================

// SilkA2NLSF converts LPC coefficients to NLSF using libopus.
// Input: lpcQ16 in Q16 format, output: NLSF in Q15 format [0, 32767].
// Note: lpcQ16 may be modified by bandwidth expansion if roots aren't found.
func SilkA2NLSF(lpcQ16 []int32, order int) []int16 {
	if len(lpcQ16) < order || order <= 0 || order > 24 {
		return nil
	}
	// Make a copy since libopus may modify the input
	lpcCopy := make([]int32, order)
	copy(lpcCopy, lpcQ16[:order])

	nlsfOut := make([]int16, order)
	C.test_silk_a2nlsf(
		(*C.opus_int32)(unsafe.Pointer(&lpcCopy[0])),
		C.int(order),
		(*C.opus_int16)(unsafe.Pointer(&nlsfOut[0])),
	)
	return nlsfOut
}

// SilkNLSFEncode performs full NLSF VQ encoding using libopus.
// Returns indices, quantized NLSF, and RD value.
func SilkNLSFEncode(nlsfQ15 []int16, useWB bool, weightsQ2 []int16, nlsfMuQ20, nSurvivors, signalType int) ([]int8, []int16, int32) {
	order := len(nlsfQ15)
	if order == 0 {
		return nil, nil, 0
	}

	// Make copy since libopus modifies pNLSF_Q15
	nlsfCopy := make([]int16, order)
	copy(nlsfCopy, nlsfQ15)

	// Ensure weights are provided
	weights := weightsQ2
	if len(weights) < order {
		weights = make([]int16, order)
		for i := range weights {
			weights[i] = 256 // Default uniform weight
		}
	}

	indices := make([]int8, order+1)
	wb := 0
	if useWB {
		wb = 1
	}

	rd := C.test_silk_nlsf_encode(
		(*C.opus_int8)(unsafe.Pointer(&indices[0])),
		(*C.opus_int16)(unsafe.Pointer(&nlsfCopy[0])),
		C.int(wb),
		(*C.opus_int16)(unsafe.Pointer(&weights[0])),
		C.int(nlsfMuQ20),
		C.int(nSurvivors),
		C.int(signalType),
	)

	return indices, nlsfCopy, int32(rd)
}

// SilkNLSFStabilize stabilizes NLSF values using libopus.
func SilkNLSFStabilize(nlsfQ15 []int16, useWB bool) {
	if len(nlsfQ15) == 0 {
		return
	}
	wb := 0
	if useWB {
		wb = 1
	}
	C.test_silk_nlsf_stabilize(
		(*C.opus_int16)(unsafe.Pointer(&nlsfQ15[0])),
		C.int(wb),
	)
}

// SilkLSFCosTab returns the value at the given index from silk_LSFCosTab_FIX_Q12.
func SilkLSFCosTab(idx int) int16 {
	return int16(C.test_silk_lsf_cos_tab(C.int(idx)))
}

// SilkLSFCosTabSize returns the size constant LSF_COS_TAB_SZ_FIX.
func SilkLSFCosTabSize() int {
	return int(C.test_silk_lsf_cos_tab_size())
}

// SilkNLSFCBParams holds NLSF codebook parameters.
type SilkNLSFCBParams struct {
	NVectors           int
	Order              int
	QuantStepSizeQ16   int
	InvQuantStepSizeQ6 int
}

// SilkGetNLSFCBParams returns the NLSF codebook parameters.
func SilkGetNLSFCBParams(useWB bool) SilkNLSFCBParams {
	var nVectors, order, quantStepQ16, invQuantStepQ6 C.int
	wb := 0
	if useWB {
		wb = 1
	}
	C.test_silk_get_nlsf_cb_params(
		C.int(wb),
		&nVectors, &order, &quantStepQ16, &invQuantStepQ6,
	)
	return SilkNLSFCBParams{
		NVectors:           int(nVectors),
		Order:              int(order),
		QuantStepSizeQ16:   int(quantStepQ16),
		InvQuantStepSizeQ6: int(invQuantStepQ6),
	}
}

// SilkGetNLSFCB1 returns a stage 1 codebook entry and its weights.
func SilkGetNLSFCB1(useWB bool, idx int) ([]uint8, []int16) {
	order := 10
	if useWB {
		order = 16
	}
	cb1 := make([]uint8, order)
	wgt := make([]int16, order)
	wb := 0
	if useWB {
		wb = 1
	}
	C.test_silk_get_nlsf_cb1(
		C.int(wb),
		C.int(idx),
		(*C.opus_uint8)(unsafe.Pointer(&cb1[0])),
		(*C.opus_int16)(unsafe.Pointer(&wgt[0])),
	)
	return cb1, wgt
}

// SilkGetNLSFDeltaMin returns the minimum spacing constraints for NLSF.
func SilkGetNLSFDeltaMin(useWB bool) []int16 {
	order := 10
	if useWB {
		order = 16
	}
	deltaMin := make([]int16, order+1)
	wb := 0
	if useWB {
		wb = 1
	}
	C.test_silk_get_nlsf_delta_min(
		C.int(wb),
		(*C.opus_int16)(unsafe.Pointer(&deltaMin[0])),
	)
	return deltaMin
}

// ====================================================================
// Fine Energy Encoding Go wrappers
// ====================================================================

// FineEnergyResult holds the output from quant_fine_energy.
type FineEnergyResult struct {
	EncodedBytes    []byte    // Encoded bits from fine energy
	UpdatedEnergies []float32 // Updated oldEBands after fine encoding
	UpdatedError    []float32 // Updated error after fine encoding
}

// LibopusQuantFineEnergy calls libopus quant_fine_energy and returns the results.
// oldEBands and error are the current quantized energies and residuals.
// extraQuant is the fine bits per band (from allocation).
func LibopusQuantFineEnergy(oldEBands, errorIn []float32, extraQuant []int, start, end, channels int) (*FineEnergyResult, error) {
	nbBands := end
	if nbBands > len(extraQuant) {
		nbBands = len(extraQuant)
	}
	if len(oldEBands) < nbBands*channels || len(errorIn) < nbBands*channels {
		return nil, nil
	}

	// Make copies since libopus modifies in-place
	oldECopy := make([]float32, len(oldEBands))
	copy(oldECopy, oldEBands)
	errorCopy := make([]float32, len(errorIn))
	copy(errorCopy, errorIn)

	// Convert extraQuant to C ints
	extraQuantC := make([]int32, len(extraQuant))
	for i, v := range extraQuant {
		extraQuantC[i] = int32(v)
	}

	maxSize := 4096
	outBuf := make([]byte, maxSize)
	var outLen C.int

	ret := C.test_quant_fine_energy(
		(*C.float)(unsafe.Pointer(&oldECopy[0])),
		(*C.float)(unsafe.Pointer(&errorCopy[0])),
		(*C.int)(unsafe.Pointer(&extraQuantC[0])),
		C.int(start), C.int(end), C.int(channels),
		(*C.uchar)(unsafe.Pointer(&outBuf[0])),
		C.int(maxSize),
		&outLen,
	)

	if ret != 0 {
		return nil, nil
	}

	return &FineEnergyResult{
		EncodedBytes:    outBuf[:int(outLen)],
		UpdatedEnergies: oldECopy,
		UpdatedError:    errorCopy,
	}, nil
}

// LibopusQuantEnergyFinalise calls libopus quant_energy_finalise and returns the results.
// oldEBands and error are the current quantized energies and residuals.
// fineQuant is the fine bits used per band.
// finePriority is the priority per band (0 or 1).
// bitsLeft is the number of remaining bits to use.
func LibopusQuantEnergyFinalise(oldEBands, errorIn []float32, fineQuant, finePriority []int, bitsLeft, start, end, channels int) (*FineEnergyResult, error) {
	nbBands := end
	if nbBands > len(fineQuant) || nbBands > len(finePriority) {
		return nil, nil
	}
	if len(oldEBands) < nbBands*channels || len(errorIn) < nbBands*channels {
		return nil, nil
	}

	// Make copies since libopus modifies in-place
	oldECopy := make([]float32, len(oldEBands))
	copy(oldECopy, oldEBands)
	errorCopy := make([]float32, len(errorIn))
	copy(errorCopy, errorIn)

	// Convert to C ints
	fineQuantC := make([]int32, len(fineQuant))
	for i, v := range fineQuant {
		fineQuantC[i] = int32(v)
	}
	finePriorityC := make([]int32, len(finePriority))
	for i, v := range finePriority {
		finePriorityC[i] = int32(v)
	}

	maxSize := 4096
	outBuf := make([]byte, maxSize)
	var outLen C.int

	ret := C.test_quant_energy_finalise(
		(*C.float)(unsafe.Pointer(&oldECopy[0])),
		(*C.float)(unsafe.Pointer(&errorCopy[0])),
		(*C.int)(unsafe.Pointer(&fineQuantC[0])),
		(*C.int)(unsafe.Pointer(&finePriorityC[0])),
		C.int(bitsLeft),
		C.int(start), C.int(end), C.int(channels),
		(*C.uchar)(unsafe.Pointer(&outBuf[0])),
		C.int(maxSize),
		&outLen,
	)

	if ret != 0 {
		return nil, nil
	}

	return &FineEnergyResult{
		EncodedBytes:    outBuf[:int(outLen)],
		UpdatedEnergies: oldECopy,
		UpdatedError:    errorCopy,
	}, nil
}

// ====================================================================
// Range Encoder State Tracing Wrappers
// ====================================================================

// ECEncStateTrace holds the range encoder state for debugging.
type ECEncStateTrace struct {
	Rng  uint32 // Range
	Val  uint32 // Low value
	Ext  uint32 // Extension count (pending 0xFF bytes)
	Rem  int    // Remainder byte
	Offs uint32 // Output offset
	Tell int    // Bits written (approx)
}

// LibopusEncoderTracer allows step-by-step encoding with state tracking.
// Uses C-allocated memory to avoid CGO pointer issues.
type LibopusEncoderTracer struct {
	enc    *C.ec_enc
	buf    *C.uchar
	bufLen int
}

// NewLibopusEncoderTracer creates a new encoder tracer.
func NewLibopusEncoderTracer(bufSize int) *LibopusEncoderTracer {
	t := &LibopusEncoderTracer{
		enc:    (*C.ec_enc)(C.malloc(C.size_t(unsafe.Sizeof(C.ec_enc{})))),
		buf:    (*C.uchar)(C.malloc(C.size_t(bufSize))),
		bufLen: bufSize,
	}
	var state C.ec_enc_state_trace
	C.test_enc_init_with_state(
		t.enc,
		t.buf,
		C.int(bufSize),
		&state,
	)
	return t
}

// Destroy frees the C-allocated memory.
func (t *LibopusEncoderTracer) Destroy() {
	if t.enc != nil {
		C.free(unsafe.Pointer(t.enc))
		t.enc = nil
	}
	if t.buf != nil {
		C.free(unsafe.Pointer(t.buf))
		t.buf = nil
	}
}

// GetState returns the current encoder state.
func (t *LibopusEncoderTracer) GetState() ECEncStateTrace {
	var state C.ec_enc_state_trace
	C.test_get_enc_state(t.enc, &state)
	return ECEncStateTrace{
		Rng:  uint32(state.rng),
		Val:  uint32(state.val),
		Ext:  uint32(state.ext),
		Rem:  int(state.rem),
		Offs: uint32(state.offs),
		Tell: int(state.tell),
	}
}

// EncodeBitLogp encodes a bit with log probability and returns before/after state.
func (t *LibopusEncoderTracer) EncodeBitLogp(val int, logp uint) (before, after ECEncStateTrace) {
	var bef, aft C.ec_enc_state_trace
	C.test_encode_bit_logp_trace(t.enc, C.int(val), C.uint(logp), &bef, &aft)
	return ECEncStateTrace{
			Rng:  uint32(bef.rng),
			Val:  uint32(bef.val),
			Ext:  uint32(bef.ext),
			Rem:  int(bef.rem),
			Offs: uint32(bef.offs),
			Tell: int(bef.tell),
		}, ECEncStateTrace{
			Rng:  uint32(aft.rng),
			Val:  uint32(aft.val),
			Ext:  uint32(aft.ext),
			Rem:  int(aft.rem),
			Offs: uint32(aft.offs),
			Tell: int(aft.tell),
		}
}

// EncodeICDF encodes using ICDF and returns before/after state.
func (t *LibopusEncoderTracer) EncodeICDF(s int, icdf []uint8, ftb uint) (before, after ECEncStateTrace) {
	var bef, aft C.ec_enc_state_trace
	C.test_encode_icdf_trace(t.enc, C.int(s), (*C.uchar)(unsafe.Pointer(&icdf[0])), C.uint(ftb), &bef, &aft)
	return ECEncStateTrace{
			Rng:  uint32(bef.rng),
			Val:  uint32(bef.val),
			Ext:  uint32(bef.ext),
			Rem:  int(bef.rem),
			Offs: uint32(bef.offs),
			Tell: int(bef.tell),
		}, ECEncStateTrace{
			Rng:  uint32(aft.rng),
			Val:  uint32(aft.val),
			Ext:  uint32(aft.ext),
			Rem:  int(aft.rem),
			Offs: uint32(aft.offs),
			Tell: int(aft.tell),
		}
}

// EncodeUniform encodes a uniform value and returns before/after state.
func (t *LibopusEncoderTracer) EncodeUniform(val, ft uint32) (before, after ECEncStateTrace) {
	var bef, aft C.ec_enc_state_trace
	C.test_encode_uint_trace(t.enc, C.uint(val), C.uint(ft), &bef, &aft)
	return ECEncStateTrace{
			Rng:  uint32(bef.rng),
			Val:  uint32(bef.val),
			Ext:  uint32(bef.ext),
			Rem:  int(bef.rem),
			Offs: uint32(bef.offs),
			Tell: int(bef.tell),
		}, ECEncStateTrace{
			Rng:  uint32(aft.rng),
			Val:  uint32(aft.val),
			Ext:  uint32(aft.ext),
			Rem:  int(aft.rem),
			Offs: uint32(aft.offs),
			Tell: int(aft.tell),
		}
}
