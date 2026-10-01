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

#ifdef FIXED_POINT
#error "float surround-analysis oracle requires the matched floating-point reference"
#endif

#include "arch.h"
#include "celt.h"
#include "modes.h"
#include "opus.h"
#include "opus_private.h"
#include "cpu_support.h"

#define INPUT_MAGIC "GSFI"
#define OUTPUT_MAGIC "GSFO"
#define HELPER_VERSION 1
#define SURROUND_BANDS 21
#define MAX_SURROUND_CHANNELS 8
#define CELT_OVERLAP 120
#define MAX_FRAME_SIZE 5760

void surround_analysis(const CELTMode *celt_mode, const void *pcm, celt_glog *band_log_e,
                       opus_val32 *mem, opus_val32 *preemph_mem, int frame_size, int overlap,
                       int channels, int rate, opus_copy_channel_in_func copy_channel_in, int arch);

static int read_exact(void *dst, size_t size) {
  return fread(dst, 1, size, stdin) == size;
}

static int write_exact(const void *src, size_t size) {
  return fwrite(src, 1, size, stdout) == size;
}

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact(b, sizeof(b))) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) | ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int write_u32(uint32_t value) {
  unsigned char b[4];
  b[0] = (unsigned char)value;
  b[1] = (unsigned char)(value >> 8);
  b[2] = (unsigned char)(value >> 16);
  b[3] = (unsigned char)(value >> 24);
  return write_exact(b, sizeof(b));
}

static int read_i16s(opus_int16 *out, size_t count) {
  for (size_t i = 0; i < count; i++) {
    unsigned char b[2];
    if (!read_exact(b, sizeof(b))) return 0;
    out[i] = (opus_int16)((uint16_t)b[0] | ((uint16_t)b[1] << 8));
  }
  return 1;
}

static void copy_channel_in_short(opus_res *dst, int dst_stride, const void *src,
                                  int src_stride, int src_channel, int frame_size,
                                  void *user_data) {
  const opus_int16 *pcm = (const opus_int16 *)src;
  (void)user_data;
  for (int i = 0; i < frame_size; i++)
    dst[i * dst_stride] = INT16TORES(pcm[i * src_stride + src_channel]);
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
  uint32_t channels = 0;
  uint32_t frame_size = 0;
  uint32_t frame_count = 0;
  uint32_t reset_before = 0;
  uint32_t frame_size_u32 = 0;
  opus_int16 *pcm = NULL;
  celt_glog *band_log_e = NULL;
  opus_val32 mem[MAX_SURROUND_CHANNELS * CELT_OVERLAP] = {0};
  opus_val32 preemph_mem[MAX_SURROUND_CHANNELS] = {0};
  OpusEncoder *mode_encoder = NULL;
  const CELTMode *mode = NULL;
  int err = OPUS_OK;
  size_t sample_count;

  if (!set_binary_stdio()) {
    fprintf(stderr, "failed to set binary stdio mode\n");
    return 1;
  }
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0 ||
      !read_u32(&version) || version != HELPER_VERSION ||
      !read_u32(&sample_rate) || !read_u32(&channels) ||
      !read_u32(&frame_size) || !read_u32(&frame_count)) {
    fprintf(stderr, "invalid float surround-analysis input header\n");
    return 1;
  }
  if (sample_rate != 48000 || channels < 3 || channels > MAX_SURROUND_CHANNELS ||
      frame_size == 0 || frame_size > MAX_FRAME_SIZE || frame_count == 0 || frame_count > 16) {
    fprintf(stderr, "unsupported float surround-analysis case\n");
    return 1;
  }
  sample_count = (size_t)channels * frame_size;
  if (sample_count > SIZE_MAX / sizeof(*pcm)) {
    fprintf(stderr, "sample count overflow\n");
    return 1;
  }
  pcm = (opus_int16 *)malloc(sample_count * sizeof(*pcm));
  band_log_e = (celt_glog *)malloc((size_t)channels * SURROUND_BANDS * sizeof(*band_log_e));
  if (pcm == NULL || band_log_e == NULL) {
    fprintf(stderr, "allocation failed\n");
    free(pcm);
    free(band_log_e);
    return 1;
  }
  mode_encoder = opus_encoder_create((opus_int32)sample_rate, 2, OPUS_APPLICATION_AUDIO, &err);
  if (mode_encoder == NULL || err != OPUS_OK ||
      opus_encoder_ctl(mode_encoder, CELT_GET_MODE(&mode)) != OPUS_OK || mode == NULL) {
    fprintf(stderr, "failed to obtain selected CELT mode: %d\n", err);
    free(pcm);
    free(band_log_e);
    if (mode_encoder != NULL) opus_encoder_destroy(mode_encoder);
    return 1;
  }
  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(HELPER_VERSION) ||
      !write_u32(channels) || !write_u32(frame_count)) {
    fprintf(stderr, "failed to write float surround-analysis header\n");
    free(pcm);
    free(band_log_e);
    opus_encoder_destroy(mode_encoder);
    return 1;
  }
  frame_size_u32 = frame_size;
  for (uint32_t frame = 0; frame < frame_count; frame++) {
    if (!read_u32(&reset_before) || reset_before > 1 || !read_i16s(pcm, sample_count)) {
      fprintf(stderr, "invalid float surround-analysis frame %u\n", frame);
      free(pcm);
      free(band_log_e);
      opus_encoder_destroy(mode_encoder);
      return 1;
    }
    if (reset_before) {
      memset(mem, 0, sizeof(mem));
      memset(preemph_mem, 0, sizeof(preemph_mem));
    }
    surround_analysis(mode, pcm, band_log_e, mem, preemph_mem, (int)frame_size_u32,
                      mode->overlap, (int)channels, (int)sample_rate,
                      copy_channel_in_short, opus_select_arch());
    for (size_t i = 0; i < (size_t)channels * SURROUND_BANDS; i++) {
      uint32_t bits = 0;
      float value = (float)band_log_e[i];
      memcpy(&bits, &value, sizeof(bits));
      if (!write_u32(bits)) {
        fprintf(stderr, "failed to write float surround-analysis output\n");
        free(pcm);
        free(band_log_e);
        opus_encoder_destroy(mode_encoder);
        return 1;
      }
    }
  }
  free(pcm);
  free(band_log_e);
  opus_encoder_destroy(mode_encoder);
  return 0;
}
