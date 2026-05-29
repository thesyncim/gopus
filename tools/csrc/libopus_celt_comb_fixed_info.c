/* Fixed-point CELT comb (pitch post-) filter oracle.
 *
 * Built against the --enable-fixed-point libopus reference (config.h defines
 * FIXED_POINT, ENABLE_QEXT off, OPUS_ARM_ASM off). The exported comb_filter()
 * from celt/celt.c is linked from the reference static library, exercising the
 * canonical scalar comb_filter_const_c through it.
 *
 * comb_filter operates on opus_val32 signal buffers (int32) with int16
 * celt_coef windows and opus_val16 gains. The caller provides the full input
 * buffer including the pitch history that precedes the processed region; the
 * processed pointer is offset to leave room for that history.
 */
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

#undef OPUS_ARM_MAY_HAVE_NEON_INTR
#undef OPUS_ARM_PRESUME_NEON_INTR
#undef OPUS_ARM_MAY_HAVE_NEON
#undef OPUS_ARM_PRESUME_NEON
#undef OPUS_HAVE_RTCD

#include "arch.h"
#include "celt.h"

#define GCFI_MAGIC "GCFI"
#define GCFO_MAGIC "GCFO"

enum {
  MODE_COMB_FILTER_CONST = 0,
  MODE_COMB_FILTER = 1
};

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

static int read_i32(int32_t *out) {
  uint32_t v;
  if (!read_u32(&v)) return 0;
  *out = (int32_t)v;
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

static int write_i32(int32_t v) {
  return write_u32((uint32_t)v);
}

static int read_i16(int16_t *out) {
  unsigned char b[2];
  if (!read_exact(b, 2)) return 0;
  *out = (int16_t)((uint16_t)b[0] | ((uint16_t)b[1] << 8));
  return 1;
}

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

static opus_val32 *read_i32_array(uint32_t n) {
  opus_val32 *buf = (opus_val32 *)malloc((size_t)(n == 0 ? 1 : n) * sizeof(opus_val32));
  uint32_t i;
  if (buf == NULL) return NULL;
  for (i = 0; i < n; i++) {
    int32_t v;
    if (!read_i32(&v)) { free(buf); return NULL; }
    buf[i] = (opus_val32)v;
  }
  return buf;
}

/* Exact copy of the scalar comb_filter_const_c from celt/celt.c under
 * FIXED_POINT (OPUS_ARM_ASM off). comb_filter_const_c is static in celt.c, so
 * it is reproduced here verbatim; the public comb_filter (MODE_COMB_FILTER)
 * exercises the same code through the library to cross-check this copy. */
static void comb_filter_const_ref(opus_val32 *y, opus_val32 *x, int T, int N,
                                  celt_coef g10, celt_coef g11, celt_coef g12) {
  opus_val32 x0, x1, x2, x3, x4;
  int i;
  x4 = x[-T - 2];
  x3 = x[-T - 1];
  x2 = x[-T];
  x1 = x[-T + 1];
  for (i = 0; i < N; i++) {
    x0 = x[i - T + 2];
    y[i] = x[i]
         + MULT_COEF_32(g10, x2)
         + MULT_COEF_32(g11, ADD32(x1, x3))
         + MULT_COEF_32(g12, ADD32(x0, x4));
    y[i] = SUB32(y[i], 1);
    y[i] = SATURATE(y[i], SIG_SAT);
    x4 = x3;
    x3 = x2;
    x2 = x1;
    x1 = x0;
  }
}

/* Common buffer protocol for both modes:
 *   u32 history          (# samples of pitch history before the region)
 *   u32 N                (# samples to process)
 *   i32[history+N] x     (full input buffer; processed region starts at x+history)
 * The output region (N samples) is written into a separate y buffer so x is
 * never aliased on the gopus side; libopus is given a non-aliasing y too.
 */
static int run_comb_filter_const(void) {
  uint32_t history, n;
  int32_t t;
  int16_t g10, g11, g12;
  opus_val32 *x = NULL;
  opus_val32 *y = NULL;
  uint32_t i;
  if (!read_u32(&history) || !read_u32(&n)) return 0;
  if (!read_i32(&t)) return 0;
  if (!read_i16(&g10) || !read_i16(&g11) || !read_i16(&g12)) return 0;
  x = read_i32_array(history + n);
  if (x == NULL) return 0;
  y = (opus_val32 *)calloc((size_t)(history + n == 0 ? 1 : history + n), sizeof(opus_val32));
  if (y == NULL) { free(x); return 0; }
  comb_filter_const_ref(y + history, x + history, (int)t, (int)n,
                        (celt_coef)g10, (celt_coef)g11, (celt_coef)g12);
  free(x);
  if (!write_u32(MODE_COMB_FILTER_CONST)) { free(y); return 0; }
  if (!write_u32(n)) { free(y); return 0; }
  for (i = 0; i < n; i++) {
    if (!write_i32(y[history + i])) { free(y); return 0; }
  }
  free(y);
  return 1;
}

static int run_comb_filter(void) {
  uint32_t history, n, overlap;
  int32_t t0, t1;
  int16_t g0, g1;
  uint32_t tapset0, tapset1;
  opus_val32 *x = NULL;
  opus_val32 *y = NULL;
  celt_coef *window = NULL;
  uint32_t i;
  if (!read_u32(&history) || !read_u32(&n) || !read_u32(&overlap)) return 0;
  if (!read_i32(&t0) || !read_i32(&t1)) return 0;
  if (!read_i16(&g0) || !read_i16(&g1)) return 0;
  if (!read_u32(&tapset0) || !read_u32(&tapset1)) return 0;
  x = read_i32_array(history + n);
  window = (celt_coef *)malloc((size_t)(overlap == 0 ? 1 : overlap) * sizeof(celt_coef));
  if (x == NULL || window == NULL) { free(x); free(window); return 0; }
  for (i = 0; i < overlap; i++) {
    int16_t w;
    if (!read_i16(&w)) { free(x); free(window); return 0; }
    window[i] = (celt_coef)w;
  }
  y = (opus_val32 *)calloc((size_t)(history + n == 0 ? 1 : history + n), sizeof(opus_val32));
  if (y == NULL) { free(x); free(window); return 0; }
  comb_filter(y + history, x + history, (int)t0, (int)t1, (int)n,
              g0, g1, (int)tapset0, (int)tapset1, window, (int)overlap, 0);
  free(x);
  free(window);
  if (!write_u32(MODE_COMB_FILTER)) { free(y); return 0; }
  if (!write_u32(n)) { free(y); return 0; }
  for (i = 0; i < n; i++) {
    if (!write_i32(y[history + i])) { free(y); return 0; }
  }
  free(y);
  return 1;
}

int main(void) {
  char magic[4];
  uint32_t version;
  uint32_t mode;
  int ok;
  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, 4) || memcmp(magic, GCFI_MAGIC, 4) != 0) {
    fprintf(stderr, "bad input magic\n");
    return 1;
  }
  if (!read_u32(&version) || version != 1) {
    fprintf(stderr, "bad input version\n");
    return 1;
  }
  if (!write_exact(GCFO_MAGIC, 4)) return 1;
  if (!write_u32(1)) return 1;
  if (!read_u32(&mode)) {
    fprintf(stderr, "failed to read mode\n");
    return 1;
  }
  switch (mode) {
    case MODE_COMB_FILTER_CONST: ok = run_comb_filter_const(); break;
    case MODE_COMB_FILTER: ok = run_comb_filter(); break;
    default:
      fprintf(stderr, "unknown mode %u\n", mode);
      return 1;
  }
  if (!ok) {
    fprintf(stderr, "mode %u failed\n", mode);
    return 1;
  }
  fflush(stdout);
  return 0;
}
