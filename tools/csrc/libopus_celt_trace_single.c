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
#include "arch.h"
#include "celt.h"
#include "modes.h"

#define GCTI_MAGIC "GCTI"
#define GCTO_MAGIC "GCTO"

typedef struct {
  int celt_dec_offset;
} OpusDecoderPrefix;

typedef struct {
  const CELTMode *mode;
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
  opus_val16 postfilter_gain;
  opus_val16 postfilter_gain_old;
  int postfilter_tapset;
  int postfilter_tapset_old;
  int prefilter_and_fold;
  celt_sig preemph_memD[2];
  celt_sig _decode_mem[1];
} CELTDecoderTraceView;

static int read_exact(void *dst, size_t n) {
  return fread(dst, 1, n, stdin) == n;
}

static int write_exact(const void *src, size_t n) {
  return fwrite(src, 1, n, stdout) == n;
}

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact(b, 4)) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) | ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int write_u32(uint32_t v) {
  unsigned char b[4];
  b[0] = (unsigned char)(v & 0xFF);
  b[1] = (unsigned char)((v >> 8) & 0xFF);
  b[2] = (unsigned char)((v >> 16) & 0xFF);
  b[3] = (unsigned char)((v >> 24) & 0xFF);
  return write_exact(b, 4);
}

static int write_float(float v) {
  union {
    float f;
    uint32_t u;
  } bits;
  bits.f = v;
  return write_u32(bits.u);
}

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

static int celt_decode_buffer_size(const CELTDecoderTraceView *st) {
#ifdef ENABLE_QEXT
  return st->qext_scale * DEC_PITCH_BUF_SIZE;
#else
  (void)st;
  return DEC_PITCH_BUF_SIZE;
#endif
}

int main(void) {
  unsigned char magic[4];
  uint32_t version = 0;
  uint32_t sample_rate = 48000;
  uint32_t channels = 0;
  uint32_t frame_size = 0;
  uint32_t target_step = 0;
  uint32_t target_channel = 0;
  uint32_t start_sample = 0;
  uint32_t sample_count = 0;
  uint32_t packet_count = 0;
  float *frame = NULL;
  float *final_window = NULL;
  float *pre_window = NULL;
  float *old_band_e = NULL;
  OpusDecoder *dec = NULL;
  int err = OPUS_OK;
  uint32_t found = 0;
  uint32_t trace_decoded_samples = 0;
  uint32_t trace_internal_samples = 0;
  uint32_t trace_downsample = 0;
  uint32_t trace_overlap = 0;
  uint32_t trace_decode_buffer = 0;
  uint32_t trace_final_range = 0;
  uint32_t trace_celt_rng = 0;
  uint32_t trace_loss_duration = 0;
  uint32_t trace_plc_duration = 0;
  uint32_t trace_postfilter_period = 0;
  uint32_t trace_postfilter_period_old = 0;
  uint32_t trace_old_band_e_count = 0;
  uint32_t i;

  if (!set_binary_stdio()) {
    fprintf(stderr, "failed to set binary stdio mode\n");
    return 1;
  }

  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, GCTI_MAGIC, sizeof(magic)) != 0) {
    fprintf(stderr, "invalid input magic\n");
    return 1;
  }
  if (!read_u32(&version) || version != 1) {
    fprintf(stderr, "unsupported input version\n");
    return 1;
  }
  if (!read_u32(&sample_rate) || !read_u32(&channels) || !read_u32(&frame_size) ||
      !read_u32(&target_step) || !read_u32(&target_channel) ||
      !read_u32(&start_sample) || !read_u32(&sample_count) ||
      !read_u32(&packet_count)) {
    fprintf(stderr, "failed to read header\n");
    return 1;
  }
  if (channels == 0 || channels > 2 || frame_size == 0 || target_channel >= channels) {
    fprintf(stderr, "invalid decoder dimensions\n");
    return 1;
  }
  if (sample_rate != 8000 && sample_rate != 12000 && sample_rate != 16000 &&
      sample_rate != 24000 && sample_rate != 48000) {
    fprintf(stderr, "invalid sample rate\n");
    return 1;
  }
  if (sample_count == 0 || sample_count > 4096 || start_sample > frame_size ||
      sample_count > frame_size - start_sample) {
    fprintf(stderr, "invalid trace window\n");
    return 1;
  }
  if (target_step >= packet_count) {
    fprintf(stderr, "target step outside packet sequence\n");
    return 1;
  }

  frame = (float *)malloc((size_t)channels * (size_t)frame_size * sizeof(float));
  final_window = (float *)malloc((size_t)sample_count * sizeof(float));
  pre_window = (float *)malloc((size_t)sample_count * sizeof(float));
  old_band_e = (float *)malloc(42 * sizeof(float));
  if (frame == NULL || final_window == NULL || pre_window == NULL || old_band_e == NULL) {
    fprintf(stderr, "failed to allocate trace buffers\n");
    free(frame);
    free(final_window);
    free(pre_window);
    free(old_band_e);
    return 1;
  }

  dec = opus_decoder_create((opus_int32)sample_rate, (int)channels, &err);
  if (dec == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_decoder_create failed: %d\n", err);
    free(frame);
    free(final_window);
    free(pre_window);
    free(old_band_e);
    return 1;
  }

  for (i = 0; i < packet_count; i++) {
    uint32_t decode_fec = 0;
    uint32_t packet_len = 0;
    unsigned char *packet = NULL;
    int decoded_samples;

    if (!read_u32(&decode_fec) || decode_fec > 1 || !read_u32(&packet_len)) {
      fprintf(stderr, "failed to read packet header\n");
      opus_decoder_destroy(dec);
      free(frame);
      free(final_window);
      free(pre_window);
      free(old_band_e);
      return 1;
    }
    if (packet_len > 0) {
      packet = (unsigned char *)malloc(packet_len);
      if (packet == NULL || !read_exact(packet, packet_len)) {
        fprintf(stderr, "failed to read packet payload\n");
        free(packet);
        opus_decoder_destroy(dec);
        free(frame);
        free(final_window);
        free(pre_window);
        free(old_band_e);
        return 1;
      }
    }

    decoded_samples = opus_decode_float(dec, packet, (opus_int32)packet_len, frame, (int)frame_size, (int)decode_fec);
    free(packet);
    if (decoded_samples < 0) {
      fprintf(stderr, "opus_decode_float failed: %d\n", decoded_samples);
      opus_decoder_destroy(dec);
      free(frame);
      free(final_window);
      free(pre_window);
      free(old_band_e);
      return 1;
    }

    if (i == target_step) {
      OpusDecoderPrefix *prefix = (OpusDecoderPrefix *)dec;
      CELTDecoderTraceView *celt = (CELTDecoderTraceView *)((char *)dec + prefix->celt_dec_offset);
      opus_uint32 final_range = 0;
      int downsample = celt->downsample;
      int internal_samples = decoded_samples * downsample;
      int decode_buffer_size = celt_decode_buffer_size(celt);
      int stride = decode_buffer_size + celt->overlap;
      celt_sig *pre = celt->_decode_mem + (int)target_channel * stride + decode_buffer_size - internal_samples;
      celt_glog *bands = (celt_glog *)(celt->_decode_mem + celt->channels * stride);
      uint32_t j;

      if (downsample <= 0 || internal_samples <= 0 || start_sample + sample_count > (uint32_t)decoded_samples) {
        fprintf(stderr, "invalid decoded trace range\n");
        opus_decoder_destroy(dec);
        free(frame);
        free(final_window);
        free(pre_window);
        free(old_band_e);
        return 1;
      }
      if (downsample != 1) {
        fprintf(stderr, "CELT internal trace currently expects 48 kHz output\n");
        opus_decoder_destroy(dec);
        free(frame);
        free(final_window);
        free(pre_window);
        free(old_band_e);
        return 1;
      }

      for (j = 0; j < sample_count; j++) {
        uint32_t sample = start_sample + j;
        final_window[j] = frame[(size_t)sample * (size_t)channels + (size_t)target_channel];
        pre_window[j] = pre[sample] * (1.f / CELT_SIG_SCALE);
      }
      found = 1;
      trace_decoded_samples = (uint32_t)decoded_samples;
      trace_internal_samples = (uint32_t)internal_samples;
      trace_downsample = (uint32_t)downsample;
      trace_overlap = (uint32_t)celt->overlap;
      trace_decode_buffer = (uint32_t)decode_buffer_size;
      if (opus_decoder_ctl(dec, OPUS_GET_FINAL_RANGE(&final_range)) == OPUS_OK) {
        trace_final_range = final_range;
      }
      trace_celt_rng = celt->rng;
      trace_loss_duration = (uint32_t)celt->loss_duration;
      trace_plc_duration = (uint32_t)celt->plc_duration;
      trace_postfilter_period = (uint32_t)celt->postfilter_period;
      trace_postfilter_period_old = (uint32_t)celt->postfilter_period_old;
      trace_old_band_e_count = (uint32_t)(2 * celt->mode->nbEBands);
      if (trace_old_band_e_count > 42) trace_old_band_e_count = 42;
      for (j = 0; j < trace_old_band_e_count; j++) {
        old_band_e[j] = bands[j];
      }
    }
  }

  opus_decoder_destroy(dec);

  if (!found) {
    fprintf(stderr, "target trace step was not decoded\n");
    free(frame);
    free(final_window);
    free(pre_window);
    free(old_band_e);
    return 1;
  }

  if (!write_exact(GCTO_MAGIC, 4) || !write_u32(1) ||
      !write_u32(trace_decoded_samples) || !write_u32(trace_internal_samples) ||
      !write_u32(trace_downsample) || !write_u32(trace_overlap) ||
      !write_u32(trace_decode_buffer) || !write_u32(target_channel) ||
      !write_u32(start_sample) || !write_u32(sample_count) ||
      !write_u32(trace_final_range) || !write_u32(trace_celt_rng) ||
      !write_u32(trace_loss_duration) || !write_u32(trace_plc_duration) ||
      !write_u32(trace_postfilter_period) || !write_u32(trace_postfilter_period_old) ||
      !write_u32(trace_old_band_e_count)) {
    fprintf(stderr, "failed to write output header\n");
    free(frame);
    free(final_window);
    free(pre_window);
    free(old_band_e);
    return 1;
  }
  for (i = 0; i < sample_count; i++) {
    if (!write_float(final_window[i])) {
      fprintf(stderr, "failed to write final samples\n");
      free(frame);
      free(final_window);
      free(pre_window);
      free(old_band_e);
      return 1;
    }
  }
  for (i = 0; i < sample_count; i++) {
    if (!write_float(pre_window[i])) {
      fprintf(stderr, "failed to write pre-deemphasis samples\n");
      free(frame);
      free(final_window);
      free(pre_window);
      free(old_band_e);
      return 1;
    }
  }
  for (i = 0; i < trace_old_band_e_count; i++) {
    if (!write_float(old_band_e[i])) {
      fprintf(stderr, "failed to write oldBandE\n");
      free(frame);
      free(final_window);
      free(pre_window);
      free(old_band_e);
      return 1;
    }
  }

  free(frame);
  free(final_window);
  free(pre_window);
  free(old_band_e);
  return 0;
}
