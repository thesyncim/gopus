/* Exact Q31 CELT comb_filter oracle for the FIXED_POINT + ENABLE_QEXT build. */
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
#include "celt/celt.h"

#if !defined(FIXED_POINT) || !defined(ENABLE_QEXT)
#error "Q31 comb oracle requires FIXED_POINT + ENABLE_QEXT"
#endif

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (fread(b, 1, 4, stdin) != 4) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) |
      ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int write_u32(uint32_t value) {
  unsigned char b[4];
  b[0] = (unsigned char)value;
  b[1] = (unsigned char)(value >> 8);
  b[2] = (unsigned char)(value >> 16);
  b[3] = (unsigned char)(value >> 24);
  return fwrite(b, 1, 4, stdout) == 4;
}

int main(void) {
  unsigned char magic[4];
  uint32_t version, y_len, y_off, x_len, x_off, n, overlap;
  uint32_t t0, t1, g0raw, g1raw, tap0, tap1, window_len, i;
  int32_t g0, g1;
  opus_val32 *y = NULL, *x = NULL;
  celt_coef *window = NULL;
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 1;
#endif
  if (fread(magic, 1, 4, stdin) != 4 || memcmp(magic, "GQCI", 4) ||
      !read_u32(&version) || version != 1 || !read_u32(&y_len) ||
      !read_u32(&y_off) || !read_u32(&x_len) || !read_u32(&x_off) ||
      !read_u32(&n) || !read_u32(&overlap) || !read_u32(&t0) ||
      !read_u32(&t1) || !read_u32(&g0raw) || !read_u32(&g1raw) ||
      !read_u32(&tap0) || !read_u32(&tap1) || !read_u32(&window_len)) return 1;
  g0 = (int32_t)g0raw;
  g1 = (int32_t)g1raw;
  if (y_len == 0 || x_len == 0 || n == 0 || y_off > y_len || n > y_len-y_off ||
      x_off > x_len || n > x_len-x_off || overlap > n || overlap > 240 ||
      window_len != overlap || tap0 > 2 || tap1 > 2 || g0 < -32768 || g0 > 32767 ||
      g1 < -32768 || g1 > 32767 || x_off < t0+2 || x_off < t1+2 ||
      y_len > 100000 || x_len > 100000) return 1;
  y = (opus_val32 *)malloc((size_t)y_len * sizeof(*y));
  x = (opus_val32 *)malloc((size_t)x_len * sizeof(*x));
  window = overlap ? (celt_coef *)malloc((size_t)overlap * sizeof(*window)) : NULL;
  if (!y || !x || (overlap && !window)) return 1;
  for (i = 0; i < y_len; i++) {
    uint32_t raw;
    if (!read_u32(&raw)) return 1;
    y[i] = (opus_val32)(int32_t)raw;
  }
  for (i = 0; i < x_len; i++) {
    uint32_t raw;
    if (!read_u32(&raw)) return 1;
    x[i] = (opus_val32)(int32_t)raw;
  }
  for (i = 0; i < overlap; i++) {
    uint32_t raw;
    if (!read_u32(&raw)) return 1;
    window[i] = (celt_coef)(int32_t)raw;
  }
  if (fwrite("GQCO", 1, 4, stdout) != 4 || !write_u32(1) || !write_u32(n)) return 1;
  comb_filter(y+y_off, x+x_off, (int)t0, (int)t1, (int)n,
      (opus_val16)(int16_t)g0, (opus_val16)(int16_t)g1,
      (int)tap0, (int)tap1, overlap ? window : NULL, (int)overlap, 0);
  for (i = 0; i < n; i++) {
    if (!write_u32((uint32_t)(int32_t)y[y_off+i])) return 1;
  }
  free(y);
  free(x);
  free(window);
  return 0;
}
