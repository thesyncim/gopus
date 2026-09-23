/* Float CELT PVQ oracle linked directly with libopus's x86 SSE2 source. */
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

#include "arch.h"
#include "celt/vq.h"

#if !defined(OPUS_X86_MAY_HAVE_SSE2) || !defined(__SSE2__) || defined(FIXED_POINT)
#error "the SSE2 PVQ oracle requires the float x86 SSE2 source"
#endif

opus_val16 op_pvq_search_sse2(celt_norm *_X, int *iy, int K, int N, int arch);

void celt_fatal(const char *str, const char *file, int line) {
  (void)str;
  (void)file;
  (void)line;
  abort();
}

#define INPUT_MAGIC "GPS2"
#define OUTPUT_MAGIC "GPSO"

static int read_exact(void *dst, size_t n) { return fread(dst, 1, n, stdin) == n; }
static int write_exact(const void *src, size_t n) { return fwrite(src, 1, n, stdout) == n; }

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact(b, sizeof(b))) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) | ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int write_u32(uint32_t v) {
  unsigned char b[4] = {(unsigned char)v, (unsigned char)(v >> 8), (unsigned char)(v >> 16), (unsigned char)(v >> 24)};
  return write_exact(b, sizeof(b));
}

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

int main(void) {
  uint32_t n, k, i;
  celt_norm *x = NULL;
  int *iy = NULL;
  opus_val16 yy;
  union { float f; uint32_t u; } value;

  if (!set_binary_stdio()) return 1;
  {
    char magic[4];
    uint32_t version;
    if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) return 1;
    if (!read_u32(&version) || version != 1 || !read_u32(&n) || !read_u32(&k)) return 1;
  }
  if (n == 0 || n > 512 || k == 0 || k > 512) return 1;
  x = (celt_norm *)malloc((size_t)n * sizeof(*x));
  iy = (int *)calloc((size_t)n + 3, sizeof(*iy));
  if (x == NULL || iy == NULL) { free(x); free(iy); return 1; }
  for (i = 0; i < n; i++) {
    if (!read_u32(&value.u)) { free(x); free(iy); return 1; }
    x[i] = (celt_norm)value.f;
  }
  yy = op_pvq_search_sse2(x, iy, (int)k, (int)n, 0);
  value.f = (float)yy;
  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(value.u) || !write_u32(n)) {
    free(x); free(iy); return 1;
  }
  for (i = 0; i < n; i++) {
    if (!write_u32((uint32_t)(int32_t)iy[i])) { free(x); free(iy); return 1; }
  }
  free(x);
  free(iy);
  return 0;
}
