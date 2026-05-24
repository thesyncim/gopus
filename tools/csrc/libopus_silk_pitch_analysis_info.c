#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "silk/float/SigProc_FLP.h"

#define INPUT_MAGIC "GSPA"
#define OUTPUT_MAGIC "GSPB"

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

static int eval_record(void) {
  int32_t raw;
  int fs_khz;
  int nb_subfr;
  int complexity;
  int prev_lag;
  int frame_len;
  int expected_len;
  int ret;
  int i;
  float ltp_corr;
  float search_thres1;
  float search_thres2;
  silk_float *frame;
  opus_int pitch_out[MAX_NB_SUBFR] = {0};
  opus_int16 lag_index = 0;
  opus_int8 contour_index = 0;

  if (!read_i32(&raw)) return 0;
  fs_khz = (int)raw;
  if (!read_i32(&raw)) return 0;
  nb_subfr = (int)raw;
  if (!read_i32(&raw)) return 0;
  complexity = (int)raw;
  if (!read_i32(&raw)) return 0;
  prev_lag = (int)raw;
  if (!read_f32(&ltp_corr)) return 0;
  if (!read_f32(&search_thres1)) return 0;
  if (!read_f32(&search_thres2)) return 0;
  if (!read_i32(&raw)) return 0;
  frame_len = (int)raw;

  if ((fs_khz != 8 && fs_khz != 12 && fs_khz != 16) ||
      (nb_subfr != 2 && nb_subfr != 4) ||
      complexity < 0 || complexity > 2 ||
      frame_len <= 0) {
    return 0;
  }
  expected_len = (20 + nb_subfr * 5) * fs_khz;
  if (frame_len != expected_len) return 0;

  frame = (silk_float *)malloc((size_t)frame_len * sizeof(*frame));
  if (frame == NULL) return 0;
  for (i = 0; i < frame_len; i++) {
    float sample;
    if (!read_f32(&sample)) {
      free(frame);
      return 0;
    }
    frame[i] = (silk_float)sample;
  }

  ret = silk_pitch_analysis_core_FLP(frame, pitch_out, &lag_index, &contour_index,
      &ltp_corr, prev_lag, search_thres1, search_thres2, fs_khz, complexity, nb_subfr, 0);

  free(frame);

  if (!write_i32((int32_t)ret)) return 0;
  for (i = 0; i < MAX_NB_SUBFR; i++) {
    if (!write_i32((int32_t)pitch_out[i])) return 0;
  }
  if (!write_i32((int32_t)lag_index)) return 0;
  if (!write_i32((int32_t)contour_index)) return 0;
  if (!write_f32(ltp_corr)) return 0;
  return 1;
}

static int write_type_sizes(void) {
  return write_u32((uint32_t)sizeof(silk_float)) &&
      write_u32((uint32_t)sizeof(opus_val32)) &&
      write_u32((uint32_t)sizeof(opus_int16));
}

int main(void) {
  uint32_t version;
  uint32_t count;
  uint32_t i;

  if (!set_binary_stdio()) return 1;
  if (!read_magic(INPUT_MAGIC)) return 1;
  if (!read_u32(&version) || version != 1) return 1;
  if (!read_u32(&count)) return 1;

  if (!write_exact(OUTPUT_MAGIC, 4)) return 1;
  if (!write_u32(1)) return 1;
  if (!write_u32(count)) return 1;
  if (!write_type_sizes()) return 1;
  for (i = 0; i < count; i++) {
    if (!eval_record()) return 1;
  }
  return 0;
}
