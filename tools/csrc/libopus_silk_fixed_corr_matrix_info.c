/* Oracle for the libopus FIXED_POINT silk_corrMatrix_FIX /
 * silk_corrVector_FIX kernels (silk/fixed/corrMatrix_FIX.c).
 *
 * Must be compiled and linked against a libopus configured with
 * --enable-fixed-point (defines FIXED_POINT). Reads a little-endian payload of
 * cases from stdin and writes the bit-exact correlation matrix XX (order*order
 * int32), the X'*t correlation vector Xt (order int32), the energy (int32) and
 * the right-shift count (int32) to stdout. */

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"

#ifndef FIXED_POINT
#error "this oracle requires a FIXED_POINT libopus build (--enable-fixed-point)"
#endif

#include "main_FIX.h"
#include "SigProc_FIX.h"

#define INPUT_MAGIC "GCMI"
#define OUTPUT_MAGIC "GCMO"

#define MAX_L 1024
#define MAX_ORDER 32
#define MAX_X_LEN (MAX_L + MAX_ORDER - 1)

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
  unsigned char b[4];
  if (!read_exact(b, sizeof(b))) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) | ((uint32_t)b[2] << 16) |
         ((uint32_t)b[3] << 24);
  return 1;
}

static int read_i16(int16_t *out) {
  unsigned char b[2];
  if (!read_exact(b, sizeof(b))) return 0;
  *out = (int16_t)((uint16_t)b[0] | ((uint16_t)b[1] << 8));
  return 1;
}

static int write_u32(uint32_t value) {
  unsigned char b[4];
  b[0] = (unsigned char)(value & 0xffu);
  b[1] = (unsigned char)((value >> 8) & 0xffu);
  b[2] = (unsigned char)((value >> 16) & 0xffu);
  b[3] = (unsigned char)((value >> 24) & 0xffu);
  return write_exact(b, sizeof(b));
}

static int write_i32(int32_t value) { return write_u32((uint32_t)value); }

int main(void) {
  if (!set_binary_stdio()) return 1;

  char magic[4];
  if (!read_exact(magic, sizeof(magic)) ||
      memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) {
    return 1;
  }
  uint32_t version;
  if (!read_u32(&version) || version != 1) return 1;
  uint32_t count;
  if (!read_u32(&count)) return 1;

  if (!write_exact(OUTPUT_MAGIC, 4)) return 1;
  if (!write_u32(1)) return 1; /* version */
  if (!write_u32(count)) return 1;

  for (uint32_t c = 0; c < count; c++) {
    uint32_t L, order;
    if (!read_u32(&L) || !read_u32(&order)) return 1;
    if (L < 1 || L > MAX_L || order < 1 || order > MAX_ORDER) return 1;

    uint32_t x_len = L + order - 1;

    static opus_int16 x[MAX_X_LEN];
    static opus_int16 t[MAX_L];
    for (uint32_t i = 0; i < x_len; i++) {
      if (!read_i16(&x[i])) return 1;
    }
    for (uint32_t i = 0; i < L; i++) {
      if (!read_i16(&t[i])) return 1;
    }

    opus_int32 XX[MAX_ORDER * MAX_ORDER];
    opus_int32 Xt[MAX_ORDER];
    opus_int32 nrg = 0;
    opus_int rshifts = 0;

    memset(XX, 0, sizeof(opus_int32) * order * order);

    silk_corrMatrix_FIX(x, (opus_int)L, (opus_int)order, XX, &nrg, &rshifts, 0);
    silk_corrVector_FIX(x, t, (opus_int)L, (opus_int)order, Xt, rshifts, 0);

    for (uint32_t i = 0; i < order * order; i++) {
      if (!write_i32(XX[i])) return 1;
    }
    for (uint32_t i = 0; i < order; i++) {
      if (!write_i32(Xt[i])) return 1;
    }
    if (!write_i32(nrg)) return 1;
    if (!write_i32((int32_t)rshifts)) return 1;
  }

  return 0;
}
