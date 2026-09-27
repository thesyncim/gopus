/* FIXED_POINT + ENABLE_QEXT alg_unquant oracle for exact PVQ refinement. */

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
#include "celt/entdec.h"
#include "celt/vq.h"

#if !defined(FIXED_POINT) || !defined(ENABLE_QEXT)
#error "fixed QEXT alg_unquant oracle requires FIXED_POINT and ENABLE_QEXT"
#endif

void celt_fatal(const char *str, const char *file, int line) {
  (void)str;
  (void)file;
  (void)line;
  abort();
}

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

int main(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 1;
#endif
  unsigned char magic[4];
  uint32_t version, n, k, spread, blocks, extra_bits, raw_gain, main_len, ext_len, i;
  unsigned char *main_buf = NULL, *ext_buf = NULL;
  celt_norm *x = NULL;
  ec_dec dec, ext_dec;
  unsigned collapse;
  if (!read_exact(magic, 4) || memcmp(magic, "GQDI", 4) != 0 ||
      !read_u32(&version) || version != 1 || !read_u32(&n) || !read_u32(&k) ||
      !read_u32(&spread) || !read_u32(&blocks) || !read_u32(&extra_bits) ||
      !read_u32(&raw_gain) || !read_u32(&main_len) || !read_u32(&ext_len)) return 1;
  if (n < 2 || n > 176 || k == 0 || k > 512 || spread > 3 || blocks == 0 ||
      blocks > n || n % blocks != 0 || extra_bits < 2 || extra_bits > 12 ||
      main_len == 0 || main_len > 4096 || ext_len > 4096) return 1;
  main_buf = (unsigned char *)malloc(main_len);
  ext_buf = (unsigned char *)calloc(ext_len ? ext_len : 1, 1);
  x = (celt_norm *)calloc(n, sizeof(*x));
  if (main_buf == NULL || ext_buf == NULL || x == NULL ||
      !read_exact(main_buf, main_len) || !read_exact(ext_buf, ext_len)) return 1;
  ec_dec_init(&dec, main_buf, main_len);
  ec_dec_init(&ext_dec, ext_buf, ext_len);
  collapse = alg_unquant(x, (int)n, (int)k, (int)spread, (int)blocks,
      &dec, (opus_val32)(int32_t)raw_gain, &ext_dec, (int)extra_bits);
  if (!write_exact("GQDO", 4) || !write_u32(1) || !write_u32(1) ||
      !write_u32(collapse) || !write_u32(dec.rng) || !write_u32(dec.val) ||
      !write_u32((uint32_t)ec_tell(&dec)) || !write_u32(ec_tell_frac(&dec)) ||
      !write_u32((uint32_t)ec_get_error(&dec)) || !write_u32(ext_dec.rng) ||
      !write_u32(ext_dec.val) || !write_u32((uint32_t)ec_tell(&ext_dec)) ||
      !write_u32(ec_tell_frac(&ext_dec)) || !write_u32((uint32_t)ec_get_error(&ext_dec)) ||
      !write_u32(n)) return 1;
  for (i = 0; i < n; i++) {
    if (!write_u32((uint32_t)(int32_t)x[i])) return 1;
  }
  free(x);
  free(ext_buf);
  free(main_buf);
  return 0;
}
