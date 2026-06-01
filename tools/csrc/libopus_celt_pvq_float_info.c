/* Float CELT PVQ pulse-search kernel oracle.
 *
 * Built against the default (float) libopus reference. celt_norm is float.
 * The ARM NEON override for op_pvq_search is undefined here so the canonical
 * pure-C op_pvq_search_c is exercised (libopus has no arm NEON op_pvq_search,
 * so the float build already uses the scalar kernel here).
 *
 * Input/output X and yy are transported as raw IEEE-754 float bits in int32.
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
#include "vq.h"

#define GPVI_MAGIC "GPFI"
#define GPVO_MAGIC "GPFO"

enum { MODE_PVQ_SEARCH = 0 };

static int read_exact(void *dst, size_t n) { return fread(dst, 1, n, stdin) == n; }
static int write_exact(const void *src, size_t n) { return fwrite(src, 1, n, stdout) == n; }

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
static int write_i32(int32_t v) { return write_u32((uint32_t)v); }
static int read_i32(int32_t *out) {
  uint32_t v;
  if (!read_u32(&v)) return 0;
  *out = (int32_t)v;
  return 1;
}

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

static int run_pvq_search(void) {
  uint32_t n, k;
  int32_t i;
  celt_norm *X = NULL;
  int *iy = NULL;
  opus_val16 yy;
  union { float f; int32_t i; uint32_t u; } cvt;
  if (!read_u32(&n) || !read_u32(&k) || n == 0) return 0;
  X = (celt_norm *)malloc((size_t)n * sizeof(celt_norm));
  iy = (int *)malloc(((size_t)n + 3) * sizeof(int));
  if (X == NULL || iy == NULL) { free(X); free(iy); return 0; }
  for (i = 0; i < (int32_t)n; i++) {
    int32_t v;
    if (!read_i32(&v)) { free(X); free(iy); return 0; }
    cvt.i = v;
    X[i] = (celt_norm)cvt.f;
  }
  yy = op_pvq_search_c(X, iy, (int)k, (int)n, 0);
  if (!write_u32(MODE_PVQ_SEARCH)) { free(X); free(iy); return 0; }
  cvt.f = (float)yy;
  if (!write_i32(cvt.i)) { free(X); free(iy); return 0; }
  for (i = 0; i < (int32_t)n; i++) {
    if (!write_i32((int32_t)iy[i])) { free(X); free(iy); return 0; }
  }
  free(X); free(iy);
  return 1;
}

int main(void) {
  char magic[4];
  uint32_t version, mode;
  int ok;
  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, 4) || memcmp(magic, GPVI_MAGIC, 4) != 0) {
    fprintf(stderr, "bad input magic\n");
    return 1;
  }
  if (!read_u32(&version) || version != 1) { fprintf(stderr, "bad version\n"); return 1; }
  if (!write_exact(GPVO_MAGIC, 4)) return 1;
  if (!write_u32(1)) return 1;
  if (!read_u32(&mode)) { fprintf(stderr, "no mode\n"); return 1; }
  switch (mode) {
    case MODE_PVQ_SEARCH: ok = run_pvq_search(); break;
    default: fprintf(stderr, "unknown mode %u\n", mode); return 1;
  }
  if (!ok) { fprintf(stderr, "mode %u failed\n", mode); return 1; }
  fflush(stdout);
  return 0;
}
