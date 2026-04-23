#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#ifdef HAVE_CONFIG_H
#include "config.h"
#endif

#include "opus.h"
#include "celt/os_support.h"
#include "celt/float_cast.h"
#include "celt/modes.h"
#include "silk/control.h"
#include "resampler_rom.h"
#include "structs.h"
#include "lpcnet_private.h"

#define INPUT_MAGIC "GDDI"
#define OUTPUT_MAGIC "GDDO"

#ifndef ENABLE_DEEP_PLC
#error "ENABLE_DEEP_PLC is required for decoder DRED state parity"
#endif

#define GOPUS_PLC_UPDATE_SAMPLES (4 * FRAME_SIZE)
#define GOPUS_SINC_ORDER 48

typedef struct {
  silk_decoder_state channel_state[DECODER_NUM_CHANNELS];
  stereo_dec_state sStereo;
  opus_int nChannelsAPI;
  opus_int nChannelsInternal;
  opus_int prev_decode_only_middle;
} silk_decoder;

static const float gopus_sinc_filter[GOPUS_SINC_ORDER + 1] = {
  4.2931e-05f, -0.000190293f, -0.000816132f, -0.000637162f, 0.00141662f, 0.00354764f, 0.00184368f, -0.00428274f,
  -0.00856105f, -0.0034003f, 0.00930201f, 0.0159616f, 0.00489785f, -0.0169649f, -0.0259484f, -0.00596856f,
  0.0286551f, 0.0405872f, 0.00649994f, -0.0509284f, -0.0716655f, -0.00665212f, 0.134336f, 0.278927f,
  0.339995f, 0.278927f, 0.134336f, -0.00665212f, -0.0716655f, -0.0509284f, 0.00649994f, 0.0405872f,
  0.0286551f, -0.00596856f, -0.0259484f, -0.0169649f, 0.00489785f, 0.0159616f, 0.00930201f, -0.0034003f,
  -0.00856105f, -0.00428274f, 0.00184368f, 0.00354764f, 0.00141662f, -0.000637162f, -0.000816132f, -0.000190293f,
  4.2931e-05f,
};

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
  LPCNetPLCState lpcnet;
} GopusInternalOpusDecoder;

typedef struct {
  const void *mode;
  int overlap;
  int channels;
  int stream_channels;
  int downsample;
  int start;
  int end;
  int signalling;
  int disable_inv;
  int complexity;
  int arch;
#ifdef ENABLE_QEXT
  int qext_scale;
#endif
  opus_uint32 rng;
  int error;
  int last_pitch_index;
  int loss_duration;
  int plc_duration;
  int last_frame_type;
  int skip_plc;
  int postfilter_period;
  int postfilter_period_old;
  float postfilter_gain;
  float postfilter_gain_old;
  int postfilter_tapset;
  int postfilter_tapset_old;
  int prefilter_and_fold;
  float preemph_memD[2];
  opus_int16 plc_pcm[GOPUS_PLC_UPDATE_SAMPLES];
  int plc_fill;
  float plc_preemphasis_mem;
  celt_sig _decode_mem[1];
} GopusInternalCELTDecoder;

static void snapshot_warmup_plc_update(
    const GopusInternalCELTDecoder *celt_dec,
    int channels,
    float *preemph_mem,
    float *plc_preemphasis_mem,
    float *plc_update) {
  celt_sig buf48k[DEC_PITCH_BUF_SIZE];
  int offset;
  int i;
  if (celt_dec == NULL || channels <= 0 || preemph_mem == NULL || plc_preemphasis_mem == NULL || plc_update == NULL) {
    return;
  }
  preemph_mem[0] = (1.0f / 32768.0f) * celt_dec->preemph_memD[0];
  preemph_mem[1] = (1.0f / 32768.0f) * celt_dec->preemph_memD[1];
  if (channels == 1) {
    OPUS_COPY(buf48k, celt_dec->_decode_mem, DEC_PITCH_BUF_SIZE);
  } else {
    const celt_sig *decode_mem_l = celt_dec->_decode_mem;
    const celt_sig *decode_mem_r = celt_dec->_decode_mem + (DEC_PITCH_BUF_SIZE + celt_dec->overlap);
    for (i = 0; i < DEC_PITCH_BUF_SIZE; i++) {
      buf48k[i] = .5f * (decode_mem_l[i] + decode_mem_r[i]);
    }
  }
  for (i = 1; i < DEC_PITCH_BUF_SIZE; i++) {
    buf48k[i] += PREEMPHASIS * buf48k[i - 1];
  }
  *plc_preemphasis_mem = (1.0f / 32768.0f) * buf48k[DEC_PITCH_BUF_SIZE - 1];
  offset = DEC_PITCH_BUF_SIZE - GOPUS_SINC_ORDER - 1 - 3 * (GOPUS_PLC_UPDATE_SAMPLES - 1);
  for (i = 0; i < GOPUS_PLC_UPDATE_SAMPLES; i++) {
    int j;
    float sum = 0;
    for (j = 0; j < GOPUS_SINC_ORDER + 1; j++) {
      sum += buf48k[3 * i + j + offset] * gopus_sinc_filter[j];
    }
    plc_update[i] = (1.0f / 32768.0f) * float2int(MIN32(32767.f, MAX32(-32767.f, sum)));
  }
}

static int read_exact(void *dst, size_t n) {
  return fread(dst, 1, n, stdin) == n;
}

static int write_exact(const void *src, size_t n) {
  return fwrite(src, 1, n, stdout) == n;
}

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact(b, sizeof(b))) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) | ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int write_u32(uint32_t v) {
  unsigned char b[4];
  b[0] = (unsigned char)(v & 0xFF);
  b[1] = (unsigned char)((v >> 8) & 0xFF);
  b[2] = (unsigned char)((v >> 16) & 0xFF);
  b[3] = (unsigned char)((v >> 24) & 0xFF);
  return write_exact(b, sizeof(b));
}

static int write_i32(int32_t v) {
  return write_u32((uint32_t)v);
}

static int write_f32(float v) {
  union {
    float f;
    uint32_t u;
  } bits;
  bits.f = v;
  return write_u32(bits.u);
}

static int write_f32_array(const float *src, int count) {
  int i;
  for (i = 0; i < count; i++) {
    if (!write_f32(src[i])) return 0;
  }
  return 1;
}

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

int main(void) {
  unsigned char magic[4];
  uint32_t version = 0;
  uint32_t sample_rate = 0;
  uint32_t max_dred_samples = 0;
  int32_t warmup_dred_offset = -1;
  int32_t dred_offset = 0;
  uint32_t frame_size = 0;
  uint32_t seed_packet_len = 0;
  uint32_t packet_len = 0;
  uint32_t next_packet_len = 0;
  uint32_t decoder_model_blob_len = 0;
  uint32_t dred_model_blob_len = 0;
  unsigned char *seed_packet = NULL;
  unsigned char *packet = NULL;
  unsigned char *next_packet = NULL;
  unsigned char *decoder_model_blob = NULL;
  unsigned char *dred_model_blob = NULL;
  OpusDecoder *dec = NULL;
  OpusDREDDecoder *dred_dec = NULL;
  OpusDRED *dred = NULL;
  float *seed_pcm = NULL;
  float *out_pcm = NULL;
  float *next_out_pcm = NULL;
  int err = OPUS_OK;
  int parse_ret = OPUS_OK;
  int dred_end = 0;
  int seed_packet_samples = 0;
  int next_packet_samples = 0;
  int channels = 0;
  int warmup_ret = 0;
  int ret = 0;
  int next_ret = 0;
  int blend = 0;
  int loss_count = 0;
  int analysis_gap = 0;
  int analysis_pos = 0;
  int predict_pos = 0;
  int fec_read_pos = 0;
  int fec_fill_pos = 0;
  int fec_skip = 0;
  int fargan_cont_initialized = 0;
  int fargan_last_period = 0;
  int celt_last_frame_type = 0;
  int celt_plc_fill = 0;
  int celt_plc_duration = 0;
  int celt_skip_plc = 0;
  float celt_plc_preemphasis_mem = 0;
  int silk_lag_prev = 0;
  int silk_last_gain_index = 0;
  int silk_loss_cnt = 0;
  int silk_prev_signal_type = 0;
  float silk_smid[2] = {0, 0};
  float silk_outbuf[MAX_FRAME_LENGTH + 2 * MAX_SUB_FRAME_LENGTH];
  float silk_slpc_q14[MAX_LPC_ORDER];
  float silk_exc_q14[MAX_FRAME_LENGTH];
  float silk_resampler_iir[SILK_RESAMPLER_MAX_IIR_ORDER];
  float silk_resampler_fir[RESAMPLER_ORDER_FIR_12];
  float silk_resampler_delay[96];
  float warmup_preemph_mem[2] = {0, 0};
  float warmup_plc_preemphasis_mem = 0;
  float warmup_plc_update[GOPUS_PLC_UPDATE_SAMPLES];
  GopusInternalCELTDecoder *celt_dec = NULL;
  GopusInternalOpusDecoder *internal_dec = NULL;
  silk_decoder *silk_dec = NULL;
  silk_decoder_state *silk_state = NULL;
  int i;

  OPUS_CLEAR(warmup_plc_update, GOPUS_PLC_UPDATE_SAMPLES);
  OPUS_CLEAR(silk_outbuf, MAX_FRAME_LENGTH + 2 * MAX_SUB_FRAME_LENGTH);
  OPUS_CLEAR(silk_slpc_q14, MAX_LPC_ORDER);
  OPUS_CLEAR(silk_exc_q14, MAX_FRAME_LENGTH);
  OPUS_CLEAR(silk_resampler_iir, SILK_RESAMPLER_MAX_IIR_ORDER);
  OPUS_CLEAR(silk_resampler_fir, RESAMPLER_ORDER_FIR_12);
  OPUS_CLEAR(silk_resampler_delay, 96);

  if (!set_binary_stdio()) {
    fprintf(stderr, "failed to set binary stdio mode\n");
    return 1;
  }
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) {
    fprintf(stderr, "invalid input magic\n");
    return 1;
  }
  if (!read_u32(&version) || version != 6 ||
      !read_u32(&sample_rate) || !read_u32(&max_dred_samples) ||
      !read_exact(&warmup_dred_offset, sizeof(warmup_dred_offset)) ||
      !read_exact(&dred_offset, sizeof(dred_offset)) ||
      !read_u32(&frame_size) || !read_u32(&seed_packet_len) ||
      !read_u32(&packet_len) || !read_u32(&next_packet_len) ||
      !read_u32(&decoder_model_blob_len) ||
      !read_u32(&dred_model_blob_len)) {
    fprintf(stderr, "failed to read helper header\n");
    return 1;
  }

  if (seed_packet_len > 0) {
    seed_packet = (unsigned char *)malloc(seed_packet_len);
    if (seed_packet == NULL || !read_exact(seed_packet, seed_packet_len)) {
      fprintf(stderr, "failed to read seed packet payload\n");
      free(seed_packet);
      return 1;
    }
  }
  if (packet_len > 0) {
    packet = (unsigned char *)malloc(packet_len);
    if (packet == NULL || !read_exact(packet, packet_len)) {
      fprintf(stderr, "failed to read packet payload\n");
      free(seed_packet);
      free(packet);
      return 1;
    }
  }
  if (next_packet_len > 0) {
    next_packet = (unsigned char *)malloc(next_packet_len);
    if (next_packet == NULL || !read_exact(next_packet, next_packet_len)) {
      fprintf(stderr, "failed to read next packet payload\n");
      free(next_packet);
      free(seed_packet);
      free(packet);
      return 1;
    }
  }
  if (decoder_model_blob_len > 0) {
    decoder_model_blob = (unsigned char *)malloc(decoder_model_blob_len);
    if (decoder_model_blob == NULL || !read_exact(decoder_model_blob, decoder_model_blob_len)) {
      fprintf(stderr, "failed to read decoder model blob\n");
      free(decoder_model_blob);
      free(next_packet);
      free(seed_packet);
      free(packet);
      return 1;
    }
  }
  if (dred_model_blob_len > 0) {
    dred_model_blob = (unsigned char *)malloc(dred_model_blob_len);
    if (dred_model_blob == NULL || !read_exact(dred_model_blob, dred_model_blob_len)) {
      fprintf(stderr, "failed to read dred model blob\n");
      free(dred_model_blob);
      free(decoder_model_blob);
      free(next_packet);
      free(seed_packet);
      free(packet);
      return 1;
    }
  }

  channels = opus_packet_get_nb_channels(packet);
  if (channels <= 0) {
    fprintf(stderr, "failed to get packet channels\n");
    free(dred_model_blob);
    free(decoder_model_blob);
    free(next_packet);
    free(seed_packet);
    free(packet);
    return 1;
  }

  dec = opus_decoder_create((opus_int32)sample_rate, channels, &err);
  if (dec == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_decoder_create failed: %d\n", err);
    free(dred_model_blob);
    free(decoder_model_blob);
    free(seed_packet);
    free(packet);
    return 1;
  }
  err = opus_decoder_ctl(dec, OPUS_SET_COMPLEXITY(10));
  if (err != OPUS_OK) {
    fprintf(stderr, "opus_decoder_ctl(OPUS_SET_COMPLEXITY) failed: %d\n", err);
    opus_decoder_destroy(dec);
    free(dred_model_blob);
    free(decoder_model_blob);
    free(seed_packet);
    free(packet);
    return 1;
  }
#ifdef USE_WEIGHTS_FILE
  if (decoder_model_blob != NULL && decoder_model_blob_len > 0) {
    err = opus_decoder_ctl(dec, OPUS_SET_DNN_BLOB(decoder_model_blob, (opus_int32)decoder_model_blob_len));
    if (err != OPUS_OK) {
      fprintf(stderr, "opus_decoder_ctl(OPUS_SET_DNN_BLOB) failed: %d\n", err);
      opus_decoder_destroy(dec);
      free(dred_model_blob);
      free(decoder_model_blob);
      free(next_packet);
      free(seed_packet);
      free(packet);
      return 1;
    }
  }
#endif
  dred_dec = opus_dred_decoder_create(&err);
  if (dred_dec == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_dred_decoder_create failed: %d\n", err);
    opus_decoder_destroy(dec);
    free(dred_model_blob);
    free(decoder_model_blob);
    free(next_packet);
    free(seed_packet);
    free(packet);
    return 1;
  }
#ifdef USE_WEIGHTS_FILE
  if (dred_model_blob != NULL && dred_model_blob_len > 0) {
    err = opus_dred_decoder_ctl(dred_dec, OPUS_SET_DNN_BLOB(dred_model_blob, (opus_int32)dred_model_blob_len));
    if (err != OPUS_OK) {
      fprintf(stderr, "opus_dred_decoder_ctl(OPUS_SET_DNN_BLOB) failed: %d\n", err);
      opus_dred_decoder_destroy(dred_dec);
      opus_decoder_destroy(dec);
      free(dred_model_blob);
      free(decoder_model_blob);
      free(next_packet);
      free(seed_packet);
      free(packet);
      return 1;
    }
  }
#endif
  dred = opus_dred_alloc(&err);
  if (dred == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_dred_alloc failed: %d\n", err);
    opus_dred_decoder_destroy(dred_dec);
    opus_decoder_destroy(dec);
    free(dred_model_blob);
    free(decoder_model_blob);
    free(next_packet);
    free(seed_packet);
    free(packet);
    return 1;
  }

  if (seed_packet != NULL && seed_packet_len > 0) {
    seed_packet_samples = opus_decoder_get_nb_samples(dec, seed_packet, (opus_int32)seed_packet_len);
    if (seed_packet_samples > 0) {
      seed_pcm = (float *)calloc((size_t)seed_packet_samples * channels, sizeof(float));
      if (seed_pcm == NULL) {
        fprintf(stderr, "seed buffer alloc failed\n");
        opus_dred_free(dred);
        opus_dred_decoder_destroy(dred_dec);
        opus_decoder_destroy(dec);
        free(dred_model_blob);
        free(decoder_model_blob);
        free(next_packet);
        free(seed_packet);
        free(packet);
        return 1;
      }
      err = opus_decode_float(dec, seed_packet, (opus_int32)seed_packet_len, seed_pcm, seed_packet_samples, 0);
      if (err < 0) {
        parse_ret = err;
      }
    }
  }

  if (dec != NULL) {
    internal_dec = (GopusInternalOpusDecoder *)dec;
    celt_dec = (GopusInternalCELTDecoder *)((char *)dec + internal_dec->celt_dec_offset);
    snapshot_warmup_plc_update(celt_dec, channels, warmup_preemph_mem, &warmup_plc_preemphasis_mem, warmup_plc_update);
  }

  if (parse_ret >= 0) {
    parse_ret = opus_dred_parse(dred_dec, dred, packet, (opus_int32)packet_len, (opus_int32)max_dred_samples, (opus_int32)sample_rate, &dred_end, 0);
  }

  if (parse_ret >= 0) {
    out_pcm = (float *)calloc((size_t)frame_size * channels, sizeof(float));
    if (out_pcm == NULL) {
        fprintf(stderr, "output buffer alloc failed\n");
        free(seed_pcm);
        opus_dred_free(dred);
        opus_dred_decoder_destroy(dred_dec);
        opus_decoder_destroy(dec);
        free(dred_model_blob);
        free(decoder_model_blob);
        free(next_packet);
        free(seed_packet);
        free(packet);
        return 1;
    }
    if (warmup_dred_offset >= 0) {
      warmup_ret = opus_decoder_dred_decode_float(dec, dred, warmup_dred_offset, out_pcm, (opus_int32)frame_size);
      if (warmup_ret < 0) {
        ret = warmup_ret;
      } else {
        ret = opus_decoder_dred_decode_float(dec, dred, dred_offset, out_pcm, (opus_int32)frame_size);
      }
    } else {
      warmup_ret = 0;
      ret = opus_decoder_dred_decode_float(dec, dred, dred_offset, out_pcm, (opus_int32)frame_size);
    }
    if (ret >= 0 && next_packet != NULL && next_packet_len > 0) {
      next_packet_samples = opus_decoder_get_nb_samples(dec, next_packet, (opus_int32)next_packet_len);
      if (next_packet_samples <= 0) {
        next_ret = next_packet_samples;
      } else {
        next_out_pcm = (float *)calloc((size_t)next_packet_samples * channels, sizeof(float));
        if (next_out_pcm == NULL) {
          fprintf(stderr, "next output buffer alloc failed\n");
          free(out_pcm);
          free(seed_pcm);
          opus_dred_free(dred);
          opus_dred_decoder_destroy(dred_dec);
          opus_decoder_destroy(dec);
          free(dred_model_blob);
          free(decoder_model_blob);
          free(next_packet);
          free(seed_packet);
          free(packet);
          return 1;
        }
        next_ret = opus_decode_float(dec, next_packet, (opus_int32)next_packet_len, next_out_pcm, next_packet_samples, 0);
      }
    }
  } else {
    warmup_ret = parse_ret;
    ret = parse_ret;
    next_ret = parse_ret;
  }

  if (dec != NULL) {
    internal_dec = (GopusInternalOpusDecoder *)dec;
    blend = internal_dec->lpcnet.blend;
    loss_count = internal_dec->lpcnet.loss_count;
    analysis_gap = internal_dec->lpcnet.analysis_gap;
    analysis_pos = internal_dec->lpcnet.analysis_pos;
    predict_pos = internal_dec->lpcnet.predict_pos;
    fec_read_pos = internal_dec->lpcnet.fec_read_pos;
    fec_fill_pos = internal_dec->lpcnet.fec_fill_pos;
    fec_skip = internal_dec->lpcnet.fec_skip;
    fargan_cont_initialized = internal_dec->lpcnet.fargan.cont_initialized;
    fargan_last_period = internal_dec->lpcnet.fargan.last_period;
    celt_dec = (GopusInternalCELTDecoder *)((char *)dec + internal_dec->celt_dec_offset);
    silk_dec = (silk_decoder *)((char *)dec + internal_dec->silk_dec_offset);
    silk_state = &silk_dec->channel_state[0];
    celt_last_frame_type = celt_dec->last_frame_type;
    celt_plc_fill = celt_dec->plc_fill;
    celt_plc_duration = celt_dec->plc_duration;
    celt_skip_plc = celt_dec->skip_plc;
    celt_plc_preemphasis_mem = (1.0f / 32768.0f) * celt_dec->plc_preemphasis_mem;
    silk_lag_prev = silk_state->lagPrev;
    silk_last_gain_index = silk_state->LastGainIndex;
    silk_loss_cnt = silk_state->lossCnt;
    silk_prev_signal_type = silk_state->prevSignalType;
    silk_smid[0] = (1.0f / 32768.0f) * silk_dec->sStereo.sMid[0];
    silk_smid[1] = (1.0f / 32768.0f) * silk_dec->sStereo.sMid[1];
    for (i = 0; i < MAX_FRAME_LENGTH + 2 * MAX_SUB_FRAME_LENGTH; i++) {
      silk_outbuf[i] = (1.0f / 32768.0f) * silk_state->outBuf[i];
    }
    for (i = 0; i < MAX_LPC_ORDER; i++) {
      silk_slpc_q14[i] = (float)silk_state->sLPC_Q14_buf[i];
    }
    for (i = 0; i < MAX_FRAME_LENGTH; i++) {
      silk_exc_q14[i] = (float)silk_state->exc_Q14[i];
    }
    for (i = 0; i < SILK_RESAMPLER_MAX_IIR_ORDER; i++) {
      silk_resampler_iir[i] = (float)silk_state->resampler_state.sIIR[i];
    }
    for (i = 0; i < RESAMPLER_ORDER_FIR_12; i++) {
      silk_resampler_fir[i] = (1.0f / 32768.0f) * silk_state->resampler_state.sFIR.i16[i];
    }
    for (i = 0; i < 96; i++) {
      silk_resampler_delay[i] = (1.0f / 32768.0f) * silk_state->resampler_state.delayBuf[i];
    }
  }

  if (!write_exact(OUTPUT_MAGIC, 4) ||
      !write_u32(5) ||
      !write_i32(parse_ret) ||
      !write_i32(dred_end) ||
      !write_i32(warmup_ret) ||
      !write_i32(ret) ||
      !write_i32(next_ret) ||
      !write_i32(channels) ||
      !write_i32(blend) ||
      !write_i32(loss_count) ||
      !write_i32(analysis_gap) ||
      !write_i32(analysis_pos) ||
      !write_i32(predict_pos) ||
      !write_i32(fec_read_pos) ||
      !write_i32(fec_fill_pos) ||
      !write_i32(fec_skip) ||
      !write_i32(fargan_cont_initialized) ||
      !write_i32(fargan_last_period) ||
      !write_i32(celt_last_frame_type) ||
      !write_i32(celt_plc_fill) ||
      !write_i32(celt_plc_duration) ||
      !write_i32(celt_skip_plc) ||
      !write_f32(celt_plc_preemphasis_mem) ||
      !write_i32(silk_lag_prev) ||
      !write_i32(silk_last_gain_index) ||
      !write_i32(silk_loss_cnt) ||
      !write_i32(silk_prev_signal_type)) {
    fprintf(stderr, "failed to write helper header\n");
    free(out_pcm);
    free(seed_pcm);
    opus_dred_free(dred);
    opus_dred_decoder_destroy(dred_dec);
    opus_decoder_destroy(dec);
    free(dred_model_blob);
    free(decoder_model_blob);
    free(seed_packet);
    free(packet);
    return 1;
  }

  if (ret > 0) {
    for (i = 0; i < ret * channels; i++) {
      if (!write_f32(out_pcm[i])) {
        fprintf(stderr, "failed to write pcm\n");
        free(out_pcm);
        free(next_out_pcm);
        free(seed_pcm);
        opus_dred_free(dred);
        opus_dred_decoder_destroy(dred_dec);
        opus_decoder_destroy(dec);
        free(dred_model_blob);
        free(decoder_model_blob);
        free(next_packet);
        free(seed_packet);
        free(packet);
        return 1;
      }
    }
  }

  if (next_ret > 0) {
    for (i = 0; i < next_ret * channels; i++) {
      if (!write_f32(next_out_pcm[i])) {
        fprintf(stderr, "failed to write next pcm\n");
        free(out_pcm);
        free(next_out_pcm);
        free(seed_pcm);
        opus_dred_free(dred);
        opus_dred_decoder_destroy(dred_dec);
        opus_decoder_destroy(dec);
        free(dred_model_blob);
        free(decoder_model_blob);
        free(next_packet);
        free(seed_packet);
        free(packet);
        return 1;
      }
    }
  }

  if (internal_dec != NULL) {
    if (!write_f32_array(internal_dec->lpcnet.features, NB_TOTAL_FEATURES) ||
        !write_f32_array(internal_dec->lpcnet.cont_features, CONT_VECTORS * NB_FEATURES) ||
        !write_f32_array(internal_dec->lpcnet.pcm, PLC_BUF_SIZE) ||
        !write_f32_array(internal_dec->lpcnet.plc_net.gru1_state, PLC_GRU1_STATE_SIZE) ||
        !write_f32_array(internal_dec->lpcnet.plc_net.gru2_state, PLC_GRU2_STATE_SIZE) ||
        !write_f32_array(internal_dec->lpcnet.plc_bak[0].gru1_state, PLC_GRU1_STATE_SIZE) ||
        !write_f32_array(internal_dec->lpcnet.plc_bak[0].gru2_state, PLC_GRU2_STATE_SIZE) ||
        !write_f32_array(internal_dec->lpcnet.plc_bak[1].gru1_state, PLC_GRU1_STATE_SIZE) ||
        !write_f32_array(internal_dec->lpcnet.plc_bak[1].gru2_state, PLC_GRU2_STATE_SIZE) ||
        !write_f32_array(&internal_dec->lpcnet.fargan.deemph_mem, 1) ||
        !write_f32_array(internal_dec->lpcnet.fargan.pitch_buf, PITCH_MAX_PERIOD) ||
        !write_f32_array(internal_dec->lpcnet.fargan.cond_conv1_state, COND_NET_FCONV1_STATE_SIZE) ||
        !write_f32_array(internal_dec->lpcnet.fargan.fwc0_mem, SIG_NET_FWC0_STATE_SIZE) ||
        !write_f32_array(internal_dec->lpcnet.fargan.gru1_state, SIG_NET_GRU1_STATE_SIZE) ||
        !write_f32_array(internal_dec->lpcnet.fargan.gru2_state, SIG_NET_GRU2_STATE_SIZE) ||
        !write_f32_array(internal_dec->lpcnet.fargan.gru3_state, SIG_NET_GRU3_STATE_SIZE)) {
      fprintf(stderr, "failed to write decoder DRED state payload\n");
      free(out_pcm);
      free(next_out_pcm);
      free(seed_pcm);
    opus_dred_free(dred);
    opus_dred_decoder_destroy(dred_dec);
    opus_decoder_destroy(dec);
    free(dred_model_blob);
    free(decoder_model_blob);
    free(next_packet);
    free(seed_packet);
    free(packet);
    return 1;
  }
    if (celt_dec != NULL) {
      float preemph_mem[2];
      preemph_mem[0] = (1.0f / 32768.0f) * celt_dec->preemph_memD[0];
      preemph_mem[1] = (1.0f / 32768.0f) * celt_dec->preemph_memD[1];
      if (!write_f32_array(preemph_mem, 2)) {
        fprintf(stderr, "failed to write decoder DRED CELT preemph state\n");
        free(out_pcm);
        free(next_out_pcm);
        free(seed_pcm);
        opus_dred_free(dred);
        opus_dred_decoder_destroy(dred_dec);
        opus_decoder_destroy(dec);
        free(dred_model_blob);
        free(decoder_model_blob);
        free(next_packet);
        free(seed_packet);
        free(packet);
        return 1;
      }
      for (i = 0; i < GOPUS_PLC_UPDATE_SAMPLES; i++) {
        if (!write_f32((1.0f / 32768.0f) * celt_dec->plc_pcm[i])) {
          fprintf(stderr, "failed to write decoder DRED CELT plc queue\n");
          free(out_pcm);
          free(next_out_pcm);
          free(seed_pcm);
          opus_dred_free(dred);
          opus_dred_decoder_destroy(dred_dec);
          opus_decoder_destroy(dec);
          free(dred_model_blob);
          free(decoder_model_blob);
          free(next_packet);
          free(seed_packet);
          free(packet);
          return 1;
        }
      }
      if (!write_f32_array(silk_smid, 2) ||
          !write_f32_array(silk_outbuf, MAX_FRAME_LENGTH + 2 * MAX_SUB_FRAME_LENGTH) ||
          !write_f32_array(silk_slpc_q14, MAX_LPC_ORDER) ||
          !write_f32_array(silk_exc_q14, MAX_FRAME_LENGTH) ||
          !write_f32_array(silk_resampler_iir, SILK_RESAMPLER_MAX_IIR_ORDER) ||
          !write_f32_array(silk_resampler_fir, RESAMPLER_ORDER_FIR_12) ||
          !write_f32_array(silk_resampler_delay, 96)) {
        fprintf(stderr, "failed to write decoder DRED SILK state\n");
        free(out_pcm);
        free(next_out_pcm);
        free(seed_pcm);
        opus_dred_free(dred);
        opus_dred_decoder_destroy(dred_dec);
        opus_decoder_destroy(dec);
        free(dred_model_blob);
        free(decoder_model_blob);
        free(next_packet);
        free(seed_packet);
        free(packet);
        return 1;
      }
      if (!write_f32_array(warmup_preemph_mem, 2) ||
          !write_f32(warmup_plc_preemphasis_mem) ||
          !write_f32_array(warmup_plc_update, GOPUS_PLC_UPDATE_SAMPLES)) {
        fprintf(stderr, "failed to write decoder warmup CELT state\n");
        free(out_pcm);
        free(next_out_pcm);
        free(seed_pcm);
        opus_dred_free(dred);
        opus_dred_decoder_destroy(dred_dec);
        opus_decoder_destroy(dec);
        free(dred_model_blob);
        free(decoder_model_blob);
        free(next_packet);
        free(seed_packet);
        free(packet);
        return 1;
      }
    }
  }

  free(out_pcm);
  free(next_out_pcm);
  free(seed_pcm);
  opus_dred_free(dred);
  opus_dred_decoder_destroy(dred_dec);
  opus_decoder_destroy(dec);
  free(dred_model_blob);
  free(decoder_model_blob);
  free(next_packet);
  free(seed_packet);
  free(packet);
  return 0;
}
