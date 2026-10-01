#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "celt/arch.h"
#include "celt/mdct.h"

static const opus_int16 eband5ms[] = {0};
static const unsigned char band_allocation[] = {0};
#include "celt/static_modes_fixed.h"

#if !defined(FIXED_POINT) || !defined(ENABLE_QEXT)
#error "fixed-QEXT smooth fade oracle requires FIXED_POINT + ENABLE_QEXT"
#endif

static int read_exact(void *dst, size_t n) { return fread(dst, 1, n, stdin) == n; }
static int write_exact(const void *src, size_t n) { return fwrite(src, 1, n, stdout) == n; }

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact(b, sizeof(b))) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) |
      ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int write_u32(uint32_t value) {
  unsigned char b[4] = {
    (unsigned char)value, (unsigned char)(value >> 8),
    (unsigned char)(value >> 16), (unsigned char)(value >> 24)
  };
  return write_exact(b, sizeof(b));
}

static void libopus_smooth_fade_qext(const opus_res *in1, const opus_res *in2,
      opus_res *out, int overlap, int channels, const celt_coef *window,
      opus_int32 sample_rate) {
  int i, c;
  int inc = 48000 / sample_rate;
  for (c = 0; c < channels; c++) {
    for (i = 0; i < overlap; i++) {
      celt_coef w = MULT_COEF(window[i * inc], window[i * inc]);
      out[i * channels + c] = ADD32(MULT_COEF_32(w, in2[i * channels + c]),
          MULT_COEF_32(COEF_ONE - w, in1[i * channels + c]));
    }
  }
}

int main(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 1;
#endif
  unsigned char magic[4];
  uint32_t version, sample_rate, channels, overlap, count;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, "GSQI", 4) != 0 ||
      !read_u32(&version) || version != 1 || !read_u32(&sample_rate) ||
      !read_u32(&channels) || !read_u32(&overlap) || !read_u32(&count)) return 1;
  if ((sample_rate != 8000 && sample_rate != 12000 && sample_rate != 16000 &&
       sample_rate != 24000 && sample_rate != 48000 && sample_rate != 96000) ||
      (channels != 1 && channels != 2) || overlap != sample_rate / 400 ||
      count != overlap * channels) return 1;

  const CELTMode *mode = sample_rate == 96000 ? &mode96000_1920_240 : &mode48000_960_120;
  opus_res *in1 = (opus_res *)malloc((size_t)count * sizeof(*in1));
  opus_res *in2 = (opus_res *)malloc((size_t)count * sizeof(*in2));
  opus_res *out = (opus_res *)malloc((size_t)count * sizeof(*out));
  if (in1 == NULL || in2 == NULL || out == NULL) {
    free(in1); free(in2); free(out);
    return 1;
  }
  for (uint32_t i = 0; i < count; i++) {
    uint32_t value;
    if (!read_u32(&value)) { free(in1); free(in2); free(out); return 1; }
    in1[i] = (opus_res)(opus_int32)value;
  }
  for (uint32_t i = 0; i < count; i++) {
    uint32_t value;
    if (!read_u32(&value)) { free(in1); free(in2); free(out); return 1; }
    in2[i] = (opus_res)(opus_int32)value;
  }

  libopus_smooth_fade_qext(in1, in2, out, (int)overlap, (int)channels,
      mode->window, (opus_int32)sample_rate);
  if (!write_exact("GSQO", 4) || !write_u32(1) || !write_u32(count)) {
    free(in1); free(in2); free(out);
    return 1;
  }
  for (uint32_t i = 0; i < count; i++) {
    if (!write_u32((uint32_t)(opus_int32)out[i])) {
      free(in1); free(in2); free(out);
      return 1;
    }
  }
  free(in1); free(in2); free(out);
  return 0;
}
