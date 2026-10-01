#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#ifndef FIXED_POINT
#error "surround-analysis oracle requires the matched FIXED_POINT reference config"
#endif

#include "celt.h"
#include "bands.h"
#include "mdct.h"
#include "opus_private.h"
#include "modes.h"
#include "quant_bands.h"
#include "cpu_support.h"

#define GSRI_MAGIC "GSRI"
#define GSRO_MAGIC "GSRO"
#define SURROUND_BANDS 21
#define MAX_SURROUND_CHANNELS 8
#define CELT_OVERLAP 120

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

static int read_i16s(opus_int16 *out, size_t n) {
  for (size_t i = 0; i < n; i++) {
    unsigned char b[2];
    if (!read_exact(b, sizeof(b))) return 0;
    out[i] = (opus_int16)((uint16_t)b[0] | ((uint16_t)b[1] << 8));
  }
  return 1;
}

static int write_i32(opus_int32 value) {
  return write_u32((uint32_t)value);
}

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 || _setmode(_fileno(stdout), _O_BINARY) == -1) {
    return 0;
  }
#endif
  return 1;
}

static void copy_channel_in_short(opus_res *dst, int dst_stride, const void *src,
                                  int src_stride, int src_channel, int frame_size,
                                  void *user_data) {
  const opus_int16 *samples = (const opus_int16 *)src;
  (void)user_data;
  for (int i = 0; i < frame_size; i++) {
    dst[i * dst_stride] = INT16TORES(samples[i * src_stride + src_channel]);
  }
}

void surround_analysis(const CELTMode *celt_mode, const void *pcm, celt_glog *band_log_e,
                       opus_val32 *mem, opus_val32 *preemph_mem, int len, int overlap,
                       int channels, int rate, opus_copy_channel_in_func copy_channel_in,
                       int arch);

static void surround_raw_band_log_e(const CELTMode *mode, const opus_int16 *pcm,
                                    celt_glog *band_log_e, opus_val32 *mem,
                                    opus_val32 *preemph_mem, int len, int channels,
                                    int rate, int arch) {
  const int upsample = resampling_factor(rate);
  const int frame_size = len * upsample;
  int LM;
  for (LM = 0; LM < mode->maxLM; LM++) {
    if ((mode->shortMdctSize << LM) == frame_size) break;
  }
  const int freq_size = mode->shortMdctSize << LM;
  const int nb_frames = frame_size / freq_size;
  celt_sig *in = (celt_sig *)calloc((size_t)(frame_size + mode->overlap), sizeof(*in));
  celt_sig *freq = (celt_sig *)malloc((size_t)freq_size * sizeof(*freq));
  opus_res *x = (opus_res *)malloc((size_t)len * sizeof(*x));
  if (in == NULL || freq == NULL || x == NULL) {
    free(in);
    free(freq);
    free(x);
    return;
  }

  for (int channel = 0; channel < channels; channel++) {
    celt_ener bandE[SURROUND_BANDS] = {0};
    celt_ener tmpE[SURROUND_BANDS];
    memcpy(in, mem + channel * mode->overlap, (size_t)mode->overlap * sizeof(*in));
    for (int i = 0; i < len; i++) {
      x[i] = INT16TORES(pcm[i * channels + channel]);
    }
    celt_preemphasis(x, in + mode->overlap, frame_size, 1, upsample,
                     mode->preemph, preemph_mem + channel, 0);
    for (int frame = 0; frame < nb_frames; frame++) {
      clt_mdct_forward(&mode->mdct, in + freq_size * frame, freq, mode->window,
                       mode->overlap, mode->maxLM - LM, 1, arch);
      if (upsample != 1) {
        int i;
        const int bound = freq_size / upsample;
        for (i = 0; i < bound; i++) freq[i] *= upsample;
        for (; i < freq_size; i++) freq[i] = 0;
      }
      compute_band_energies(mode, freq, tmpE, SURROUND_BANDS, 1, LM, arch);
      for (int band = 0; band < SURROUND_BANDS; band++) {
        if (tmpE[band] > bandE[band]) bandE[band] = tmpE[band];
      }
    }
    amp2Log2(mode, SURROUND_BANDS, SURROUND_BANDS, bandE,
             band_log_e + channel * SURROUND_BANDS, 1);
    memcpy(mem + channel * mode->overlap, in + frame_size,
           (size_t)mode->overlap * sizeof(*in));
  }
  free(in);
  free(freq);
  free(x);
}

int main(void) {
  unsigned char magic[4];
  uint32_t version = 0, sample_rate = 0, channels = 0, frame_size = 0, frame_count = 0;
  if (!set_binary_stdio() || !read_exact(magic, sizeof(magic)) || memcmp(magic, GSRI_MAGIC, sizeof(magic)) != 0 ||
      !read_u32(&version) || version != 2 || !read_u32(&sample_rate) ||
      !read_u32(&channels) || !read_u32(&frame_size) || !read_u32(&frame_count) ||
      channels < 3 || channels > MAX_SURROUND_CHANNELS || frame_size == 0 || frame_count == 0) {
    fprintf(stderr, "invalid surround-analysis request\n");
    return 1;
  }

  int err = OPUS_OK;
  OpusEncoder *mode_encoder = opus_encoder_create((opus_int32)sample_rate, 2,
                                                    OPUS_APPLICATION_AUDIO, &err);
  const CELTMode *mode = NULL;
  if (mode_encoder == NULL || err != OPUS_OK ||
      opus_encoder_ctl(mode_encoder, CELT_GET_MODE(&mode)) != OPUS_OK || mode == NULL) {
    fprintf(stderr, "failed to get CELT mode\n");
    if (mode_encoder != NULL) opus_encoder_destroy(mode_encoder);
    return 1;
  }

  size_t sample_count = (size_t)channels * frame_size;
  opus_int16 *pcm = (opus_int16 *)malloc(sample_count * sizeof(*pcm));
  celt_glog *band_log_e = (celt_glog *)malloc((size_t)channels * SURROUND_BANDS * sizeof(*band_log_e));
  celt_glog *raw_band_log_e = (celt_glog *)malloc((size_t)channels * SURROUND_BANDS * sizeof(*raw_band_log_e));
  opus_val32 mem[MAX_SURROUND_CHANNELS * CELT_OVERLAP] = {0};
  opus_val32 preemph_mem[MAX_SURROUND_CHANNELS] = {0};
  opus_val32 raw_mem[MAX_SURROUND_CHANNELS * CELT_OVERLAP] = {0};
  opus_val32 raw_preemph_mem[MAX_SURROUND_CHANNELS] = {0};
  if (pcm == NULL || band_log_e == NULL || raw_band_log_e == NULL) {
    fprintf(stderr, "allocation failed\n");
    free(pcm);
    free(band_log_e);
    free(raw_band_log_e);
    opus_encoder_destroy(mode_encoder);
    return 1;
  }

  if (!write_exact(GSRO_MAGIC, 4) || !write_u32(2) || !write_u32(channels) || !write_u32(frame_count)) {
    fprintf(stderr, "failed to write response header\n");
    free(pcm);
    free(band_log_e);
    free(raw_band_log_e);
    opus_encoder_destroy(mode_encoder);
    return 1;
  }

  for (uint32_t frame = 0; frame < frame_count; frame++) {
    uint32_t reset_before = 0;
    if (!read_u32(&reset_before) || reset_before > 1 || !read_i16s(pcm, sample_count)) {
      fprintf(stderr, "invalid input frame %u\n", frame);
      free(pcm);
      free(band_log_e);
      free(raw_band_log_e);
      opus_encoder_destroy(mode_encoder);
      return 1;
    }
    if (reset_before) {
      memset(mem, 0, sizeof(mem));
      memset(preemph_mem, 0, sizeof(preemph_mem));
      memset(raw_mem, 0, sizeof(raw_mem));
      memset(raw_preemph_mem, 0, sizeof(raw_preemph_mem));
    }
    surround_raw_band_log_e(mode, pcm, raw_band_log_e, raw_mem, raw_preemph_mem,
                            (int)frame_size, (int)channels, (int)sample_rate, opus_select_arch());
    surround_analysis(mode, pcm, band_log_e, mem, preemph_mem, (int)frame_size, mode->overlap,
                      (int)channels, (int)sample_rate, copy_channel_in_short, opus_select_arch());
    for (size_t i = 0; i < (size_t)channels * SURROUND_BANDS; i++) {
      if (!write_i32(raw_band_log_e[i])) {
        fprintf(stderr, "failed to write frame %u raw analyzer result\n", frame);
        free(pcm);
        free(band_log_e);
        free(raw_band_log_e);
        opus_encoder_destroy(mode_encoder);
        return 1;
      }
    }
    for (size_t i = 0; i < (size_t)channels * SURROUND_BANDS; i++) {
      if (!write_i32(band_log_e[i])) {
        fprintf(stderr, "failed to write frame %u analyzer result\n", frame);
        free(pcm);
        free(band_log_e);
        free(raw_band_log_e);
        opus_encoder_destroy(mode_encoder);
        return 1;
      }
    }
  }

  free(pcm);
  free(band_log_e);
  free(raw_band_log_e);
  opus_encoder_destroy(mode_encoder);
  return 0;
}
