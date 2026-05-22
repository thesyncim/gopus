#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"

#define dynalloc_analysis gopus_helper_dynalloc_analysis
#include "celt_encoder.c"
#undef dynalloc_analysis

#define INPUT_MAGIC "GCDI"
#define OUTPUT_MAGIC "GCDO"
#define HELPER_MAX_BANDS 21
#define HELPER_MAX_CHANNELS 2
#define HELPER_LEAK_BANDS 19

#if defined(ENABLE_ASSERTIONS) || defined(ENABLE_HARDENING)
void celt_fatal(const char *str, const char *file, int line) {
  (void)str;
  (void)file;
  (void)line;
  abort();
}
#endif

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

static int read_exact(void *dst, size_t size) {
  return fread(dst, 1, size, stdin) == size;
}

static int write_exact(const void *src, size_t size) {
  return fwrite(src, 1, size, stdout) == size;
}

static int read_u32(uint32_t *out) {
  return read_exact(out, sizeof(*out));
}

static int read_i32(int32_t *out) {
  uint32_t raw;
  if (!read_u32(&raw)) return 0;
  *out = (int32_t)raw;
  return 1;
}

static int read_f32(float *out) {
  uint32_t raw;
  if (!read_u32(&raw)) return 0;
  memcpy(out, &raw, sizeof(*out));
  return 1;
}

static int write_u32(uint32_t value) {
  return write_exact(&value, sizeof(value));
}

static int write_i32(int32_t value) {
  return write_u32((uint32_t)value);
}

static int write_f32(float value) {
  uint32_t raw;
  memcpy(&raw, &value, sizeof(raw));
  return write_u32(raw);
}

static int read_magic(const char *want) {
  char got[4];
  return read_exact(got, sizeof(got)) && memcmp(got, want, sizeof(got)) == 0;
}

static int eval_record(const CELTMode *mode) {
  int32_t raw;
  int nb_bands;
  int start;
  int end;
  int channels;
  int lsb_depth;
  int lm;
  int effective_bytes;
  int is_transient;
  int vbr;
  int constrained_vbr;
  int lfe;
  int analysis_valid;
  int i;
  int total;
  float tone_freq;
  float toneishness;
  celt_glog band_log_e[HELPER_MAX_CHANNELS * HELPER_MAX_BANDS];
  celt_glog band_log_e2[HELPER_MAX_CHANNELS * HELPER_MAX_BANDS];
  celt_glog old_band_e[HELPER_MAX_CHANNELS * HELPER_MAX_BANDS];
  celt_glog surround_dynalloc[HELPER_MAX_BANDS];
  int offsets[HELPER_MAX_BANDS] = {0};
  int importance[HELPER_MAX_BANDS] = {0};
  int spread_weight[HELPER_MAX_BANDS] = {0};
  opus_int32 tot_boost = 0;
  AnalysisInfo analysis;
  celt_glog max_depth;

  if (!read_i32(&raw)) return 0;
  nb_bands = (int)raw;
  if (!read_i32(&raw)) return 0;
  start = (int)raw;
  if (!read_i32(&raw)) return 0;
  end = (int)raw;
  if (!read_i32(&raw)) return 0;
  channels = (int)raw;
  if (!read_i32(&raw)) return 0;
  lsb_depth = (int)raw;
  if (!read_i32(&raw)) return 0;
  lm = (int)raw;
  if (!read_i32(&raw)) return 0;
  effective_bytes = (int)raw;
  if (!read_i32(&raw)) return 0;
  is_transient = (int)raw;
  if (!read_i32(&raw)) return 0;
  vbr = (int)raw;
  if (!read_i32(&raw)) return 0;
  constrained_vbr = (int)raw;
  if (!read_i32(&raw)) return 0;
  lfe = (int)raw;
  if (!read_f32(&tone_freq)) return 0;
  if (!read_f32(&toneishness)) return 0;
  if (!read_i32(&raw)) return 0;
  analysis_valid = (int)raw;

  if (nb_bands != mode->nbEBands || nb_bands > HELPER_MAX_BANDS ||
      start < 0 || start > end || end > nb_bands ||
      (channels != 1 && channels != 2) ||
      lsb_depth <= 0 || lm < 0 || lm > mode->maxLM ||
      effective_bytes < 0) {
    return 0;
  }

  total = channels * nb_bands;
  for (i = 0; i < total; i++) {
    float v;
    if (!read_f32(&v)) return 0;
    band_log_e[i] = (celt_glog)v;
  }
  for (i = 0; i < total; i++) {
    float v;
    if (!read_f32(&v)) return 0;
    band_log_e2[i] = (celt_glog)v;
  }
  for (i = 0; i < total; i++) {
    float v;
    if (!read_f32(&v)) return 0;
    old_band_e[i] = (celt_glog)v;
  }
  for (i = 0; i < nb_bands; i++) {
    float v;
    if (!read_f32(&v)) return 0;
    surround_dynalloc[i] = (celt_glog)v;
  }
  memset(&analysis, 0, sizeof(analysis));
  analysis.valid = analysis_valid != 0;
  for (i = 0; i < HELPER_LEAK_BANDS; i++) {
    uint32_t raw_u;
    if (!read_u32(&raw_u)) return 0;
    analysis.leak_boost[i] = (unsigned char)(raw_u & 0xff);
  }

  max_depth = gopus_helper_dynalloc_analysis(
      band_log_e, band_log_e2, old_band_e,
      nb_bands, start, end, channels, offsets, lsb_depth, mode->logN,
      is_transient, vbr, constrained_vbr, mode->eBands, lm,
      effective_bytes, &tot_boost, lfe, surround_dynalloc,
      &analysis, importance, spread_weight, tone_freq, toneishness
#ifdef ENABLE_QEXT
      , 1
#endif
  );

  if (!write_f32((float)max_depth)) return 0;
  if (!write_i32((int32_t)tot_boost)) return 0;
  for (i = 0; i < nb_bands; i++) {
    if (!write_i32((int32_t)offsets[i])) return 0;
  }
  for (i = 0; i < nb_bands; i++) {
    if (!write_i32((int32_t)importance[i])) return 0;
  }
  for (i = 0; i < nb_bands; i++) {
    if (!write_i32((int32_t)spread_weight[i])) return 0;
  }
  return 1;
}

int main(void) {
  uint32_t version;
  uint32_t count;
  uint32_t i;
  int err = 0;
  const CELTMode *mode;

  if (!set_binary_stdio()) return 1;
  if (!read_magic(INPUT_MAGIC)) return 1;
  if (!read_u32(&version) || version != 1) return 1;
  if (!read_u32(&count)) return 1;

  mode = opus_custom_mode_create(48000, 960, &err);
  if (mode == NULL || mode->nbEBands != HELPER_MAX_BANDS) return 1;

  if (!write_exact(OUTPUT_MAGIC, 4)) return 1;
  if (!write_u32(1)) return 1;
  if (!write_u32(count)) return 1;
  for (i = 0; i < count; i++) {
    if (!eval_record(mode)) return 1;
  }
  return 0;
}
