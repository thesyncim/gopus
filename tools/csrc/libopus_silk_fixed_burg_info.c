/* Oracle for the libopus FIXED_POINT silk_burg_modified_c kernel.
 *
 * Must be compiled and linked against a libopus configured with
 * --enable-fixed-point (defines FIXED_POINT). Reads a little-endian payload of
 * cases from stdin and writes the bit-exact res_nrg, res_nrg_Q and A_Q16[]
 * results to stdout. */

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
#include "define.h"

#define INPUT_MAGIC "GBMI"
#define OUTPUT_MAGIC "GBMO"

#define MAX_FRAME 384 /* subfr_length * nb_subfr <= MAX_FRAME_SIZE */
#define MAX_ORDER MAX_LPC_ORDER

void silk_burg_modified_c(opus_int32 *res_nrg, opus_int *res_nrg_Q,
                          opus_int32 A_Q16[], const opus_int16 x[],
                          const opus_int32 minInvGain_Q30,
                          const opus_int subfr_length, const opus_int nb_subfr,
                          const opus_int D, int arch);

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
    int32_t minInvGain_Q30, subfr_length, nb_subfr, order;
    if (!read_i32(&minInvGain_Q30) || !read_i32(&subfr_length) ||
        !read_i32(&nb_subfr) || !read_i32(&order)) {
      return 1;
    }
    if (subfr_length < 0 || nb_subfr < 0 || order < 0 || order > MAX_ORDER ||
        subfr_length * nb_subfr > MAX_FRAME) {
      return 1;
    }

    static int16_t x[MAX_FRAME];
    opus_int32 A_Q16[MAX_ORDER];
    opus_int32 res_nrg = 0;
    opus_int res_nrg_Q = 0;
    int32_t total = subfr_length * nb_subfr;

    for (int32_t i = 0; i < total; i++) {
      if (!read_i16(&x[i])) return 1;
    }
    memset(A_Q16, 0, sizeof(A_Q16));

    silk_burg_modified_c(&res_nrg, &res_nrg_Q, A_Q16, x,
                         (opus_int32)minInvGain_Q30, (opus_int)subfr_length,
                         (opus_int)nb_subfr, (opus_int)order, 0);

    if (!write_i32((int32_t)res_nrg)) return 1;
    if (!write_i32((int32_t)res_nrg_Q)) return 1;
    for (int32_t i = 0; i < order; i++) {
      if (!write_i32((int32_t)A_Q16[i])) return 1;
    }
  }

  return 0;
}
