/* Oracle for the libopus FIXED_POINT silk_residual_energy_FIX kernel.
 *
 * Must be compiled and linked against a libopus configured with
 * --enable-fixed-point (defines FIXED_POINT). Reads a little-endian payload of
 * cases from stdin and writes the bit-exact per-subframe residual energies
 * (int32 nrgs) and Q values (int32 nrgsQ) to stdout. */

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

#define INPUT_MAGIC "GSRI"
#define OUTPUT_MAGIC "GSRO"

#define MAX_X_LEN 4096

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
    uint32_t subfr_length, nb_subfr, lpc_order;
    if (!read_u32(&subfr_length) || !read_u32(&nb_subfr) ||
        !read_u32(&lpc_order)) {
      return 1;
    }
    if (nb_subfr > MAX_NB_SUBFR || lpc_order > MAX_LPC_ORDER) return 1;

    /* Total input length: nb_subfr/2 frame halves, each of
       (MAX_NB_SUBFR/2)*(lpc_order + subfr_length) samples. */
    uint32_t offset = lpc_order + subfr_length;
    uint32_t x_len = (uint32_t)(nb_subfr >> 1) * (MAX_NB_SUBFR >> 1) * offset;
    if (x_len > MAX_X_LEN) return 1;

    static opus_int16 x[MAX_X_LEN];
    opus_int16 a_Q12[2][MAX_LPC_ORDER];
    opus_int32 gains[MAX_NB_SUBFR];

    memset(a_Q12, 0, sizeof(a_Q12));
    for (uint32_t h = 0; h < 2; h++) {
      for (uint32_t i = 0; i < lpc_order; i++) {
        if (!read_i16(&a_Q12[h][i])) return 1;
      }
    }
    for (uint32_t i = 0; i < nb_subfr; i++) {
      if (!read_i32(&gains[i])) return 1;
    }
    for (uint32_t i = 0; i < x_len; i++) {
      if (!read_i16(&x[i])) return 1;
    }

    opus_int32 nrgs[MAX_NB_SUBFR];
    opus_int nrgsQ[MAX_NB_SUBFR];

    silk_residual_energy_FIX(nrgs, nrgsQ, x, a_Q12, gains,
                             (opus_int)subfr_length, (opus_int)nb_subfr,
                             (opus_int)lpc_order, 0);

    for (uint32_t i = 0; i < nb_subfr; i++) {
      if (!write_i32(nrgs[i])) return 1;
      if (!write_i32((int32_t)nrgsQ[i])) return 1;
    }
  }

  return 0;
}
