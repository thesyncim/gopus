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
#include "opus_private.h"
#include "silk/control.h"
#include "resampler_rom.h"
#include "structs.h"
#include "lpcnet_private.h"

#define INPUT_MAGIC "GDSI"
#define OUTPUT_MAGIC "GDSO"

#ifndef ENABLE_DEEP_PLC
#error "ENABLE_DEEP_PLC is required for decoder DRED sequence parity"
#endif

#define GOPUS_PLC_UPDATE_SAMPLES (4 * FRAME_SIZE)

typedef struct {
  silk_decoder_state channel_state[DECODER_NUM_CHANNELS];
  stereo_dec_state sStereo;
  opus_int nChannelsAPI;
  opus_int nChannelsInternal;
  opus_int prev_decode_only_middle;
} silk_decoder;

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
} GopusInternalCELTDecoder;

typedef struct {
  int ret;
  int blend;
  int loss_count;
  int analysis_gap;
  int analysis_pos;
  int predict_pos;
  int fec_read_pos;
  int fec_fill_pos;
  int fec_skip;
  int fargan_cont_initialized;
  int fargan_last_period;
  int celt_last_frame_type;
  int celt_plc_fill;
  int celt_plc_duration;
  int celt_skip_plc;
  float celt_plc_preemphasis_mem;
  float features[NB_TOTAL_FEATURES];
  float cont_features[CONT_VECTORS * NB_FEATURES];
  float pcm[PLC_BUF_SIZE];
  float plc_gru1[PLC_GRU1_STATE_SIZE];
  float plc_gru2[PLC_GRU2_STATE_SIZE];
  float plc_bak0_gru1[PLC_GRU1_STATE_SIZE];
  float plc_bak0_gru2[PLC_GRU2_STATE_SIZE];
  float plc_bak1_gru1[PLC_GRU1_STATE_SIZE];
  float plc_bak1_gru2[PLC_GRU2_STATE_SIZE];
  float fargan_deemph_mem;
  float fargan_pitch_buf[PITCH_MAX_PERIOD];
  float fargan_cond_conv1_state[COND_NET_FCONV1_STATE_SIZE];
  float fargan_fwc0_mem[SIG_NET_FWC0_STATE_SIZE];
  float fargan_gru1[SIG_NET_GRU1_STATE_SIZE];
  float fargan_gru2[SIG_NET_GRU2_STATE_SIZE];
  float fargan_gru3[SIG_NET_GRU3_STATE_SIZE];
  float celt_preemph_mem[2];
  float celt_plc_pcm[GOPUS_PLC_UPDATE_SAMPLES];
  int silk_lag_prev;
  int silk_last_gain_index;
  int silk_loss_cnt;
  int silk_prev_signal_type;
  float silk_smid[2];
  float silk_outbuf[MAX_FRAME_LENGTH + 2 * MAX_SUB_FRAME_LENGTH];
  float silk_resampler_iir[SILK_RESAMPLER_MAX_IIR_ORDER];
  float silk_resampler_fir[RESAMPLER_ORDER_FIR_12];
  float silk_resampler_delay[96];
} GopusSequenceSnapshot;

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

static int read_i32(int32_t *out) {
  return read_exact(out, sizeof(*out));
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

static void clear_snapshot(GopusSequenceSnapshot *snap) {
  if (snap == NULL) {
    return;
  }
  OPUS_CLEAR(snap, 1);
}

static void capture_snapshot(OpusDecoder *dec, int ret, GopusSequenceSnapshot *snap) {
  GopusInternalOpusDecoder *internal_dec;
  GopusInternalCELTDecoder *celt_dec;
  silk_decoder *silk_dec;
  silk_decoder_state *silk_state;
  if (dec == NULL || snap == NULL) {
    return;
  }
  clear_snapshot(snap);
  snap->ret = ret;
  internal_dec = (GopusInternalOpusDecoder *)dec;
  snap->blend = internal_dec->lpcnet.blend;
  snap->loss_count = internal_dec->lpcnet.loss_count;
  snap->analysis_gap = internal_dec->lpcnet.analysis_gap;
  snap->analysis_pos = internal_dec->lpcnet.analysis_pos;
  snap->predict_pos = internal_dec->lpcnet.predict_pos;
  snap->fec_read_pos = internal_dec->lpcnet.fec_read_pos;
  snap->fec_fill_pos = internal_dec->lpcnet.fec_fill_pos;
  snap->fec_skip = internal_dec->lpcnet.fec_skip;
  snap->fargan_cont_initialized = internal_dec->lpcnet.fargan.cont_initialized;
  snap->fargan_last_period = internal_dec->lpcnet.fargan.last_period;
  OPUS_COPY(snap->features, internal_dec->lpcnet.features, NB_TOTAL_FEATURES);
  OPUS_COPY(snap->cont_features, internal_dec->lpcnet.cont_features, CONT_VECTORS * NB_FEATURES);
  OPUS_COPY(snap->pcm, internal_dec->lpcnet.pcm, PLC_BUF_SIZE);
  OPUS_COPY(snap->plc_gru1, internal_dec->lpcnet.plc_net.gru1_state, PLC_GRU1_STATE_SIZE);
  OPUS_COPY(snap->plc_gru2, internal_dec->lpcnet.plc_net.gru2_state, PLC_GRU2_STATE_SIZE);
  OPUS_COPY(snap->plc_bak0_gru1, internal_dec->lpcnet.plc_bak[0].gru1_state, PLC_GRU1_STATE_SIZE);
  OPUS_COPY(snap->plc_bak0_gru2, internal_dec->lpcnet.plc_bak[0].gru2_state, PLC_GRU2_STATE_SIZE);
  OPUS_COPY(snap->plc_bak1_gru1, internal_dec->lpcnet.plc_bak[1].gru1_state, PLC_GRU1_STATE_SIZE);
  OPUS_COPY(snap->plc_bak1_gru2, internal_dec->lpcnet.plc_bak[1].gru2_state, PLC_GRU2_STATE_SIZE);
  snap->fargan_deemph_mem = internal_dec->lpcnet.fargan.deemph_mem;
  OPUS_COPY(snap->fargan_pitch_buf, internal_dec->lpcnet.fargan.pitch_buf, PITCH_MAX_PERIOD);
  OPUS_COPY(snap->fargan_cond_conv1_state, internal_dec->lpcnet.fargan.cond_conv1_state, COND_NET_FCONV1_STATE_SIZE);
  OPUS_COPY(snap->fargan_fwc0_mem, internal_dec->lpcnet.fargan.fwc0_mem, SIG_NET_FWC0_STATE_SIZE);
  OPUS_COPY(snap->fargan_gru1, internal_dec->lpcnet.fargan.gru1_state, SIG_NET_GRU1_STATE_SIZE);
  OPUS_COPY(snap->fargan_gru2, internal_dec->lpcnet.fargan.gru2_state, SIG_NET_GRU2_STATE_SIZE);
  OPUS_COPY(snap->fargan_gru3, internal_dec->lpcnet.fargan.gru3_state, SIG_NET_GRU3_STATE_SIZE);
  celt_dec = (GopusInternalCELTDecoder *)((char *)dec + internal_dec->celt_dec_offset);
  snap->celt_last_frame_type = celt_dec->last_frame_type;
  snap->celt_plc_fill = celt_dec->plc_fill;
  snap->celt_plc_duration = celt_dec->plc_duration;
  snap->celt_skip_plc = celt_dec->skip_plc;
  snap->celt_plc_preemphasis_mem = (1.0f / 32768.0f) * celt_dec->plc_preemphasis_mem;
  snap->celt_preemph_mem[0] = (1.0f / 32768.0f) * celt_dec->preemph_memD[0];
  snap->celt_preemph_mem[1] = (1.0f / 32768.0f) * celt_dec->preemph_memD[1];
  {
    int i;
    for (i = 0; i < GOPUS_PLC_UPDATE_SAMPLES; i++) {
      snap->celt_plc_pcm[i] = (1.0f / 32768.0f) * celt_dec->plc_pcm[i];
    }
  }
  silk_dec = (silk_decoder *)((char *)dec + internal_dec->silk_dec_offset);
  silk_state = &silk_dec->channel_state[0];
  snap->silk_lag_prev = silk_state->lagPrev;
  snap->silk_last_gain_index = silk_state->LastGainIndex;
  snap->silk_loss_cnt = silk_state->lossCnt;
  snap->silk_prev_signal_type = silk_state->prevSignalType;
  snap->silk_smid[0] = (1.0f / 32768.0f) * silk_dec->sStereo.sMid[0];
  snap->silk_smid[1] = (1.0f / 32768.0f) * silk_dec->sStereo.sMid[1];
  {
    int i;
    for (i = 0; i < MAX_FRAME_LENGTH + 2 * MAX_SUB_FRAME_LENGTH; i++) {
      snap->silk_outbuf[i] = (1.0f / 32768.0f) * silk_state->outBuf[i];
    }
    for (i = 0; i < SILK_RESAMPLER_MAX_IIR_ORDER; i++) {
      snap->silk_resampler_iir[i] = (float)silk_state->resampler_state.sIIR[i];
    }
    for (i = 0; i < RESAMPLER_ORDER_FIR_12; i++) {
      snap->silk_resampler_fir[i] = (1.0f / 32768.0f) * silk_state->resampler_state.sFIR.i16[i];
    }
    for (i = 0; i < 96; i++) {
      snap->silk_resampler_delay[i] = (1.0f / 32768.0f) * silk_state->resampler_state.delayBuf[i];
    }
  }
}

static int write_snapshot(const GopusSequenceSnapshot *snap) {
  if (!write_i32(snap->ret) ||
      !write_i32(snap->blend) ||
      !write_i32(snap->loss_count) ||
      !write_i32(snap->analysis_gap) ||
      !write_i32(snap->analysis_pos) ||
      !write_i32(snap->predict_pos) ||
      !write_i32(snap->fec_read_pos) ||
      !write_i32(snap->fec_fill_pos) ||
      !write_i32(snap->fec_skip) ||
      !write_i32(snap->fargan_cont_initialized) ||
      !write_i32(snap->fargan_last_period) ||
      !write_i32(snap->celt_last_frame_type) ||
      !write_i32(snap->celt_plc_fill) ||
      !write_i32(snap->celt_plc_duration) ||
      !write_i32(snap->celt_skip_plc) ||
      !write_f32(snap->celt_plc_preemphasis_mem)) {
    return 0;
  }
  return write_f32_array(snap->features, NB_TOTAL_FEATURES) &&
      write_f32_array(snap->cont_features, CONT_VECTORS * NB_FEATURES) &&
      write_f32_array(snap->pcm, PLC_BUF_SIZE) &&
      write_f32_array(snap->plc_gru1, PLC_GRU1_STATE_SIZE) &&
      write_f32_array(snap->plc_gru2, PLC_GRU2_STATE_SIZE) &&
      write_f32_array(snap->plc_bak0_gru1, PLC_GRU1_STATE_SIZE) &&
      write_f32_array(snap->plc_bak0_gru2, PLC_GRU2_STATE_SIZE) &&
      write_f32_array(snap->plc_bak1_gru1, PLC_GRU1_STATE_SIZE) &&
      write_f32_array(snap->plc_bak1_gru2, PLC_GRU2_STATE_SIZE) &&
      write_f32(snap->fargan_deemph_mem) &&
      write_f32_array(snap->fargan_pitch_buf, PITCH_MAX_PERIOD) &&
      write_f32_array(snap->fargan_cond_conv1_state, COND_NET_FCONV1_STATE_SIZE) &&
      write_f32_array(snap->fargan_fwc0_mem, SIG_NET_FWC0_STATE_SIZE) &&
      write_f32_array(snap->fargan_gru1, SIG_NET_GRU1_STATE_SIZE) &&
      write_f32_array(snap->fargan_gru2, SIG_NET_GRU2_STATE_SIZE) &&
      write_f32_array(snap->fargan_gru3, SIG_NET_GRU3_STATE_SIZE) &&
      write_f32_array(snap->celt_preemph_mem, 2) &&
      write_f32_array(snap->celt_plc_pcm, GOPUS_PLC_UPDATE_SAMPLES) &&
      write_i32(snap->silk_lag_prev) &&
      write_i32(snap->silk_last_gain_index) &&
      write_i32(snap->silk_loss_cnt) &&
      write_i32(snap->silk_prev_signal_type) &&
      write_f32_array(snap->silk_smid, 2) &&
      write_f32_array(snap->silk_outbuf, MAX_FRAME_LENGTH + 2 * MAX_SUB_FRAME_LENGTH) &&
      write_f32_array(snap->silk_resampler_iir, SILK_RESAMPLER_MAX_IIR_ORDER) &&
      write_f32_array(snap->silk_resampler_fir, RESAMPLER_ORDER_FIR_12) &&
      write_f32_array(snap->silk_resampler_delay, 96);
}

static int run_step(OpusDecoder *dec, const OpusDRED *dred, int dred_offset, int frame_size, float *out_pcm) {
  return opus_decoder_dred_decode_float(dec, dred, dred_offset, out_pcm, frame_size);
}

static int run_lost_step(OpusDecoder *dec, int frame_size, float *out_pcm) {
  return opus_decode_float(dec, NULL, 0, out_pcm, frame_size, 0);
}

int main(void) {
  unsigned char magic[4];
  uint32_t version = 0;
  uint32_t sample_rate = 0;
  uint32_t max_dred_samples = 0;
  uint32_t frame_size = 0;
  uint32_t seed_packet_len = 0;
  uint32_t carrier_packet_len = 0;
  uint32_t next_packet_len = 0;
  uint32_t decoder_model_blob_len = 0;
  uint32_t dred_model_blob_len = 0;
  uint32_t step0_source = 0;
  uint32_t step1_source = 0;
  uint32_t decode_next_packet = 0;
  int32_t step0_dred_offset = 0;
  int32_t step1_dred_offset = 0;
  unsigned char *seed_packet = NULL;
  unsigned char *carrier_packet = NULL;
  unsigned char *next_packet = NULL;
  unsigned char *decoder_model_blob = NULL;
  unsigned char *dred_model_blob = NULL;
  OpusDecoder *dec = NULL;
  OpusDREDDecoder *dred_dec = NULL;
  OpusDRED *carrier_dred = NULL;
  OpusDRED *next_dred = NULL;
  float *seed_pcm = NULL;
  float *carrier_pcm = NULL;
  float *step0_pcm = NULL;
  float *step1_pcm = NULL;
  float *next_pcm = NULL;
  GopusSequenceSnapshot step0_snap;
  GopusSequenceSnapshot step1_snap;
  GopusSequenceSnapshot next_snap;
  int err = OPUS_OK;
  int channels = 0;
  int carrier_parse_ret = OPUS_OK;
  int carrier_dred_end = 0;
  int next_parse_ret = OPUS_OK;
  int next_dred_end = 0;
  int carrier_ret = 0;
  int step0_ret = 0;
  int step1_ret = 0;
  int next_ret = 0;
  int seed_packet_samples = 0;
  int carrier_packet_samples = 0;
  int next_packet_samples = 0;
  const OpusDRED *step_dred = NULL;

  clear_snapshot(&step0_snap);
  clear_snapshot(&step1_snap);
  clear_snapshot(&next_snap);

  if (!set_binary_stdio()) {
    fprintf(stderr, "failed to set binary stdio mode\n");
    return 1;
  }
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) {
    fprintf(stderr, "invalid input magic\n");
    return 1;
  }
  if (!read_u32(&version) || version != 1 ||
      !read_u32(&sample_rate) ||
      !read_u32(&max_dred_samples) ||
      !read_u32(&frame_size) ||
      !read_u32(&seed_packet_len) ||
      !read_u32(&carrier_packet_len) ||
      !read_u32(&next_packet_len) ||
      !read_u32(&decoder_model_blob_len) ||
      !read_u32(&dred_model_blob_len) ||
      !read_u32(&step0_source) ||
      !read_i32(&step0_dred_offset) ||
      !read_u32(&step1_source) ||
      !read_i32(&step1_dred_offset) ||
      !read_u32(&decode_next_packet)) {
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
  if (carrier_packet_len > 0) {
    carrier_packet = (unsigned char *)malloc(carrier_packet_len);
    if (carrier_packet == NULL || !read_exact(carrier_packet, carrier_packet_len)) {
      fprintf(stderr, "failed to read carrier packet payload\n");
      free(seed_packet);
      free(carrier_packet);
      return 1;
    }
  }
  if (next_packet_len > 0) {
    next_packet = (unsigned char *)malloc(next_packet_len);
    if (next_packet == NULL || !read_exact(next_packet, next_packet_len)) {
      fprintf(stderr, "failed to read next packet payload\n");
      free(next_packet);
      free(seed_packet);
      free(carrier_packet);
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
      free(carrier_packet);
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
      free(carrier_packet);
      return 1;
    }
  }

  channels = opus_packet_get_nb_channels(carrier_packet);
  if (channels <= 0) {
    fprintf(stderr, "failed to get carrier channels\n");
    free(dred_model_blob);
    free(decoder_model_blob);
    free(next_packet);
    free(seed_packet);
    free(carrier_packet);
    return 1;
  }

  dec = opus_decoder_create((opus_int32)sample_rate, channels, &err);
  if (dec == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_decoder_create failed: %d\n", err);
    free(dred_model_blob);
    free(decoder_model_blob);
    free(next_packet);
    free(seed_packet);
    free(carrier_packet);
    return 1;
  }
  err = opus_decoder_ctl(dec, OPUS_SET_COMPLEXITY(10));
  if (err != OPUS_OK) {
    fprintf(stderr, "opus_decoder_ctl(OPUS_SET_COMPLEXITY) failed: %d\n", err);
    opus_decoder_destroy(dec);
    free(dred_model_blob);
    free(decoder_model_blob);
    free(next_packet);
    free(seed_packet);
    free(carrier_packet);
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
      free(carrier_packet);
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
    free(carrier_packet);
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
      free(carrier_packet);
      return 1;
    }
  }
#endif
  carrier_dred = opus_dred_alloc(&err);
  if (carrier_dred == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_dred_alloc(carrier) failed: %d\n", err);
    opus_dred_decoder_destroy(dred_dec);
    opus_decoder_destroy(dec);
    free(dred_model_blob);
    free(decoder_model_blob);
    free(next_packet);
    free(seed_packet);
    free(carrier_packet);
    return 1;
  }
  next_dred = opus_dred_alloc(&err);
  if (next_dred == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_dred_alloc(next) failed: %d\n", err);
    opus_dred_free(carrier_dred);
    opus_dred_decoder_destroy(dred_dec);
    opus_decoder_destroy(dec);
    free(dred_model_blob);
    free(decoder_model_blob);
    free(next_packet);
    free(seed_packet);
    free(carrier_packet);
    return 1;
  }

  if (seed_packet != NULL && seed_packet_len > 0) {
    seed_packet_samples = opus_decoder_get_nb_samples(dec, seed_packet, (opus_int32)seed_packet_len);
    if (seed_packet_samples > 0) {
      seed_pcm = (float *)calloc((size_t)seed_packet_samples * channels, sizeof(float));
      if (seed_pcm == NULL) {
        fprintf(stderr, "seed buffer alloc failed\n");
        goto cleanup_fail;
      }
      err = opus_decode_float(dec, seed_packet, (opus_int32)seed_packet_len, seed_pcm, seed_packet_samples, 0);
      if (err < 0) {
        goto cleanup_fail;
      }
    }
  }

  carrier_packet_samples = opus_decoder_get_nb_samples(dec, carrier_packet, (opus_int32)carrier_packet_len);
  if (carrier_packet_samples <= 0) {
    fprintf(stderr, "failed to get carrier packet samples\n");
    goto cleanup_fail;
  }
  carrier_pcm = (float *)calloc((size_t)carrier_packet_samples * channels, sizeof(float));
  if (carrier_pcm == NULL) {
    fprintf(stderr, "carrier buffer alloc failed\n");
    goto cleanup_fail;
  }
  carrier_ret = opus_decode_float(dec, carrier_packet, (opus_int32)carrier_packet_len, carrier_pcm, carrier_packet_samples, 0);
  if (carrier_ret < 0) {
    fprintf(stderr, "carrier decode failed: %d\n", carrier_ret);
    goto cleanup_fail;
  }

  carrier_parse_ret = opus_dred_parse(dred_dec, carrier_dred, carrier_packet, (opus_int32)carrier_packet_len, (opus_int32)max_dred_samples, (opus_int32)sample_rate, &carrier_dred_end, 0);
  if (next_packet != NULL && next_packet_len > 0) {
    next_parse_ret = opus_dred_parse(dred_dec, next_dred, next_packet, (opus_int32)next_packet_len, (opus_int32)max_dred_samples, (opus_int32)sample_rate, &next_dred_end, 0);
  }

  if (frame_size > 0) {
    step0_pcm = (float *)calloc((size_t)frame_size * channels, sizeof(float));
    step1_pcm = (float *)calloc((size_t)frame_size * channels, sizeof(float));
    if (step0_pcm == NULL || step1_pcm == NULL) {
      fprintf(stderr, "step buffer alloc failed\n");
      goto cleanup_fail;
    }
  }

  switch (step0_source) {
    case 0:
      step0_ret = 0;
      break;
    case 1:
      if (carrier_parse_ret < 0) {
        step0_ret = carrier_parse_ret;
      } else {
        step0_ret = run_lost_step(dec, (int)frame_size, step0_pcm);
      }
      break;
    case 2:
      if (next_parse_ret < 0) {
        step0_ret = next_parse_ret;
      } else {
        step_dred = next_dred;
        step0_ret = run_step(dec, step_dred, step0_dred_offset, (int)frame_size, step0_pcm);
      }
      break;
    default:
      step0_ret = 0;
      break;
  }
  capture_snapshot(dec, step0_ret, &step0_snap);

  switch (step1_source) {
    case 0:
      step1_ret = 0;
      break;
    case 1:
      if (carrier_parse_ret < 0) {
        step1_ret = carrier_parse_ret;
      } else {
        step1_ret = run_lost_step(dec, (int)frame_size, step1_pcm);
      }
      break;
    case 2:
      if (next_parse_ret < 0) {
        step1_ret = next_parse_ret;
      } else {
        step1_ret = run_step(dec, next_dred, step1_dred_offset, (int)frame_size, step1_pcm);
      }
      break;
    default:
      step1_ret = 0;
      break;
  }
  capture_snapshot(dec, step1_ret, &step1_snap);

  if (decode_next_packet && next_packet != NULL && next_packet_len > 0) {
    next_packet_samples = opus_decoder_get_nb_samples(dec, next_packet, (opus_int32)next_packet_len);
    if (next_packet_samples > 0) {
      next_pcm = (float *)calloc((size_t)next_packet_samples * channels, sizeof(float));
      if (next_pcm == NULL) {
        fprintf(stderr, "next buffer alloc failed\n");
        goto cleanup_fail;
      }
      next_ret = opus_decode_float(dec, next_packet, (opus_int32)next_packet_len, next_pcm, next_packet_samples, 0);
    }
  }
  capture_snapshot(dec, next_ret, &next_snap);

  if (!write_exact(OUTPUT_MAGIC, 4) ||
      !write_u32(1) ||
      !write_i32(carrier_parse_ret) ||
      !write_i32(carrier_dred_end) ||
      !write_i32(next_parse_ret) ||
      !write_i32(next_dred_end) ||
      !write_i32(carrier_ret) ||
      !write_i32(step0_ret) ||
      !write_i32(step1_ret) ||
      !write_i32(next_ret) ||
      !write_i32(channels)) {
    fprintf(stderr, "failed to write helper header\n");
    goto cleanup_fail;
  }
  if (step0_ret > 0) {
    if (!write_f32_array(step0_pcm, step0_ret * channels)) {
      fprintf(stderr, "failed to write step0 pcm\n");
      goto cleanup_fail;
    }
  }
  if (step1_ret > 0) {
    if (!write_f32_array(step1_pcm, step1_ret * channels)) {
      fprintf(stderr, "failed to write step1 pcm\n");
      goto cleanup_fail;
    }
  }
  if (next_ret > 0) {
    if (!write_f32_array(next_pcm, next_ret * channels)) {
      fprintf(stderr, "failed to write next pcm\n");
      goto cleanup_fail;
    }
  }
  if (!write_snapshot(&step0_snap) || !write_snapshot(&step1_snap) || !write_snapshot(&next_snap)) {
    fprintf(stderr, "failed to write helper snapshots\n");
    goto cleanup_fail;
  }

  free(next_pcm);
  free(step1_pcm);
  free(step0_pcm);
  free(carrier_pcm);
  free(seed_pcm);
  opus_dred_free(next_dred);
  opus_dred_free(carrier_dred);
  opus_dred_decoder_destroy(dred_dec);
  opus_decoder_destroy(dec);
  free(dred_model_blob);
  free(decoder_model_blob);
  free(next_packet);
  free(seed_packet);
  free(carrier_packet);
  return 0;

cleanup_fail:
  free(next_pcm);
  free(step1_pcm);
  free(step0_pcm);
  free(carrier_pcm);
  free(seed_pcm);
  if (next_dred != NULL) opus_dred_free(next_dred);
  if (carrier_dred != NULL) opus_dred_free(carrier_dred);
  if (dred_dec != NULL) opus_dred_decoder_destroy(dred_dec);
  if (dec != NULL) opus_decoder_destroy(dec);
  free(dred_model_blob);
  free(decoder_model_blob);
  free(next_packet);
  free(seed_packet);
  free(carrier_packet);
  return 1;
}
