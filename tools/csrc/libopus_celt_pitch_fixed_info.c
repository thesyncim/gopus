/* Fixed-point CELT pitch/correlation kernel oracle.
 *
 * Built against the --enable-fixed-point libopus reference (config.h defines
 * FIXED_POINT). The ARM NEON integer overrides are undefined here so the
 * canonical pure-C kernels (celt_inner_prod_c / dual_inner_prod_c /
 * xcorr_kernel_c) are exercised; libopus guarantees these are bit-exact to the
 * NEON paths via its own OPUS_CHECK_ASM asserts.
 *
 * celt_pitch_xcorr_c is reproduced here exactly as in celt/pitch.c (the
 * unrolled production variant) so the oracle stays self-contained and does not
 * pull in the NEON object symbols that linking pitch.c would require.
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

/* Force the canonical scalar kernels regardless of the reference config. */
#undef OPUS_ARM_MAY_HAVE_NEON_INTR
#undef OPUS_ARM_PRESUME_NEON_INTR
#undef OPUS_ARM_MAY_HAVE_NEON
#undef OPUS_ARM_PRESUME_NEON
#undef OPUS_HAVE_RTCD

#include "arch.h"
#include "pitch.h"

#define GPFI_MAGIC "GPFI"
#define GPFO_MAGIC "GPFO"

enum {
  MODE_INNER_PROD = 0,
  MODE_DUAL_INNER_PROD = 1,
  MODE_XCORR_KERNEL = 2,
  MODE_PITCH_XCORR = 3
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

static int read_i16(opus_val16 *out) {
  unsigned char b[2];
  if (!read_exact(b, 2)) return 0;
  *out = (opus_val16)(int16_t)((uint16_t)b[0] | ((uint16_t)b[1] << 8));
  return 1;
}

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

static opus_val16 *read_i16_array(uint32_t n) {
  opus_val16 *buf = (opus_val16 *)malloc((size_t)(n == 0 ? 1 : n) * sizeof(opus_val16));
  uint32_t i;
  if (buf == NULL) return NULL;
  for (i = 0; i < n; i++) {
    if (!read_i16(&buf[i])) {
      free(buf);
      return NULL;
    }
  }
  return buf;
}

/* Exact copy of the unrolled celt_pitch_xcorr_c production path from
 * celt/pitch.c under FIXED_POINT (returns maxcorr). */
static opus_val32 pitch_xcorr_ref(const opus_val16 *_x, const opus_val16 *_y,
                                  opus_val32 *xcorr, int len, int max_pitch) {
  int i;
  opus_val32 maxcorr = 1;
  for (i = 0; i < max_pitch - 3; i += 4) {
    opus_val32 sum[4] = {0, 0, 0, 0};
    xcorr_kernel_c(_x, _y + i, sum, len);
    xcorr[i] = sum[0];
    xcorr[i + 1] = sum[1];
    xcorr[i + 2] = sum[2];
    xcorr[i + 3] = sum[3];
    sum[0] = MAX32(sum[0], sum[1]);
    sum[2] = MAX32(sum[2], sum[3]);
    sum[0] = MAX32(sum[0], sum[2]);
    maxcorr = MAX32(maxcorr, sum[0]);
  }
  for (; i < max_pitch; i++) {
    opus_val32 sum;
    sum = celt_inner_prod_c(_x, _y + i, len);
    xcorr[i] = sum;
    maxcorr = MAX32(maxcorr, sum);
  }
  return maxcorr;
}

static int run_inner_prod(void) {
  uint32_t n;
  opus_val16 *x = NULL;
  opus_val16 *y = NULL;
  opus_val32 r;
  if (!read_u32(&n) || n == 0) return 0;
  x = read_i16_array(n);
  y = read_i16_array(n);
  if (x == NULL || y == NULL) {
    free(x);
    free(y);
    return 0;
  }
  r = celt_inner_prod_c(x, y, (int)n);
  free(x);
  free(y);
  if (!write_u32(MODE_INNER_PROD)) return 0;
  return write_i32(r);
}

static int run_dual_inner_prod(void) {
  uint32_t n;
  opus_val16 *x = NULL;
  opus_val16 *y01 = NULL;
  opus_val16 *y02 = NULL;
  opus_val32 xy1 = 0, xy2 = 0;
  if (!read_u32(&n) || n == 0) return 0;
  x = read_i16_array(n);
  y01 = read_i16_array(n);
  y02 = read_i16_array(n);
  if (x == NULL || y01 == NULL || y02 == NULL) {
    free(x);
    free(y01);
    free(y02);
    return 0;
  }
  dual_inner_prod_c(x, y01, y02, (int)n, &xy1, &xy2);
  free(x);
  free(y01);
  free(y02);
  if (!write_u32(MODE_DUAL_INNER_PROD)) return 0;
  if (!write_i32(xy1)) return 0;
  return write_i32(xy2);
}

static int run_xcorr_kernel(void) {
  uint32_t len;
  opus_val16 *x = NULL;
  opus_val16 *y = NULL;
  opus_val32 sum[4] = {0, 0, 0, 0};
  uint32_t i;
  /* y must provide len+3 samples for the 4-lag kernel. */
  if (!read_u32(&len) || len < 3) return 0;
  x = read_i16_array(len);
  y = read_i16_array(len + 3);
  if (x == NULL || y == NULL) {
    free(x);
    free(y);
    return 0;
  }
  xcorr_kernel_c(x, y, sum, (int)len);
  free(x);
  free(y);
  if (!write_u32(MODE_XCORR_KERNEL)) return 0;
  for (i = 0; i < 4; i++) {
    if (!write_i32(sum[i])) return 0;
  }
  return 1;
}

static int run_pitch_xcorr(void) {
  uint32_t len, max_pitch;
  opus_val16 *x = NULL;
  opus_val16 *y = NULL;
  opus_val32 *xcorr = NULL;
  opus_val32 maxcorr;
  uint32_t i;
  if (!read_u32(&len) || !read_u32(&max_pitch)) return 0;
  if (len < 3 || max_pitch == 0) return 0;
  x = read_i16_array(len);
  /* y spans len + max_pitch - 1 samples (i in [0,max_pitch), j in [0,len)). */
  y = read_i16_array(len + max_pitch - 1);
  xcorr = (opus_val32 *)calloc((size_t)max_pitch, sizeof(opus_val32));
  if (x == NULL || y == NULL || xcorr == NULL) {
    free(x);
    free(y);
    free(xcorr);
    return 0;
  }
  maxcorr = pitch_xcorr_ref(x, y, xcorr, (int)len, (int)max_pitch);
  if (!write_u32(MODE_PITCH_XCORR)) {
    free(x);
    free(y);
    free(xcorr);
    return 0;
  }
  if (!write_i32(maxcorr)) {
    free(x);
    free(y);
    free(xcorr);
    return 0;
  }
  if (!write_u32(max_pitch)) {
    free(x);
    free(y);
    free(xcorr);
    return 0;
  }
  for (i = 0; i < max_pitch; i++) {
    if (!write_i32(xcorr[i])) {
      free(x);
      free(y);
      free(xcorr);
      return 0;
    }
  }
  free(x);
  free(y);
  free(xcorr);
  return 1;
}

int main(void) {
  char magic[4];
  uint32_t version;
  uint32_t mode;
  int ok;
  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, 4) || memcmp(magic, GPFI_MAGIC, 4) != 0) {
    fprintf(stderr, "bad input magic\n");
    return 1;
  }
  if (!read_u32(&version) || version != 1) {
    fprintf(stderr, "bad input version\n");
    return 1;
  }
  if (!write_exact(GPFO_MAGIC, 4)) return 1;
  if (!write_u32(1)) return 1; /* protocol version */
  if (!read_u32(&mode)) {
    fprintf(stderr, "failed to read mode\n");
    return 1;
  }
  switch (mode) {
    case MODE_INNER_PROD: ok = run_inner_prod(); break;
    case MODE_DUAL_INNER_PROD: ok = run_dual_inner_prod(); break;
    case MODE_XCORR_KERNEL: ok = run_xcorr_kernel(); break;
    case MODE_PITCH_XCORR: ok = run_pitch_xcorr(); break;
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
