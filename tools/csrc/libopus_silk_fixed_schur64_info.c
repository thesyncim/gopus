/* Oracle for the libopus FIXED_POINT silk_schur64 + silk_k2a_Q16 kernels.
 *
 * Must be compiled and linked against a libopus configured with
 * --enable-fixed-point (defines FIXED_POINT). Reads a little-endian payload of
 * cases from stdin and writes the bit-exact residual energy, Q16 reflection
 * coefficients, and Q24 LPC coefficients to stdout. */

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

#include "SigProc_FIX.h"

#define INPUT_MAGIC "GS6I"
#define OUTPUT_MAGIC "GS6O"

#define MAX_ORDER 24 /* SILK_MAX_ORDER_LPC */

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

static int read_i32(int32_t *out) {
  uint32_t u;
  if (!read_u32(&u)) return 0;
  *out = (int32_t)u;
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

  for (uint32_t cidx = 0; cidx < count; cidx++) {
    int32_t order;
    if (!read_i32(&order)) return 1;
    if (order < 0 || order > MAX_ORDER) return 1;

    opus_int32 c[MAX_ORDER + 1];
    opus_int32 rc_Q16[MAX_ORDER];
    opus_int32 A_Q24[MAX_ORDER];

    for (int32_t i = 0; i < order + 1; i++) {
      if (!read_i32(&c[i])) return 1;
    }

    opus_int32 res = silk_schur64(rc_Q16, c, (opus_int32)order);

    for (int32_t i = 0; i < order; i++) {
      A_Q24[i] = 0;
    }
    silk_k2a_Q16(A_Q24, rc_Q16, (opus_int32)order);

    if (!write_i32((int32_t)res)) return 1;
    for (int32_t i = 0; i < order; i++) {
      if (!write_i32((int32_t)rc_Q16[i])) return 1;
    }
    for (int32_t i = 0; i < order; i++) {
      if (!write_i32((int32_t)A_Q24[i])) return 1;
    }
  }

  return 0;
}
