/* Oracle for the libopus FIXED_POINT kernels silk_LTP_analysis_filter_FIX
 * (silk/fixed/LTP_analysis_filter_FIX.c) and silk_scale_copy_vector16
 * (silk/fixed/vector_ops_FIX.c).
 *
 * Must be compiled and linked against a libopus configured with
 * --enable-fixed-point (defines FIXED_POINT). Reads a little-endian payload of
 * cases from stdin.
 *
 * Each case carries:
 *   u32 x_len            full input buffer length (int16 samples)
 *   u32 x_start          index of x_ptr (first subframe) in x
 *   u32 subfr_length
 *   u32 nb_subfr
 *   u32 pre_length
 *   i16 x[x_len]
 *   i16 LTPCoef_Q14[nb_subfr*LTP_ORDER]
 *   u32 pitchL[nb_subfr]
 *   i32 invGains_Q16[nb_subfr]
 *   u32 scale_size                         silk_scale_copy_vector16 length
 *   i32 scale_gain_Q16
 *   i16 scale_in[scale_size]
 *
 * For each case it writes:
 *   i16 LTP_res[nb_subfr*(pre_length+subfr_length)]
 *   i16 scale_out[scale_size]
 */

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

#define INPUT_MAGIC "GLAI"
#define OUTPUT_MAGIC "GLAO"

#define MAX_X_LEN 16384
#define MAX_RES_LEN 16384
#define MAX_SCALE_LEN 4096

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

static int write_i16(int16_t value) {
  unsigned char b[2];
  uint16_t u = (uint16_t)value;
  b[0] = (unsigned char)(u & 0xffu);
  b[1] = (unsigned char)((u >> 8) & 0xffu);
  return write_exact(b, sizeof(b));
}

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
    uint32_t x_len, x_start, subfr_length, nb_subfr, pre_length;
    if (!read_u32(&x_len) || !read_u32(&x_start) || !read_u32(&subfr_length) ||
        !read_u32(&nb_subfr) || !read_u32(&pre_length)) {
      return 1;
    }
    if (x_len < 1 || x_len > MAX_X_LEN) return 1;
    if (nb_subfr < 1 || nb_subfr > MAX_NB_SUBFR) return 1;

    static opus_int16 x[MAX_X_LEN];
    for (uint32_t i = 0; i < x_len; i++) {
      if (!read_i16(&x[i])) return 1;
    }

    opus_int16 LTPCoef_Q14[MAX_NB_SUBFR * LTP_ORDER];
    for (uint32_t i = 0; i < nb_subfr * LTP_ORDER; i++) {
      if (!read_i16(&LTPCoef_Q14[i])) return 1;
    }

    opus_int pitchL[MAX_NB_SUBFR];
    for (uint32_t k = 0; k < nb_subfr; k++) {
      uint32_t pl;
      if (!read_u32(&pl)) return 1;
      pitchL[k] = (opus_int)pl;
    }

    opus_int32 invGains_Q16[MAX_NB_SUBFR];
    for (uint32_t k = 0; k < nb_subfr; k++) {
      if (!read_i32(&invGains_Q16[k])) return 1;
    }

    uint32_t scale_size;
    int32_t scale_gain_Q16;
    if (!read_u32(&scale_size) || !read_i32(&scale_gain_Q16)) return 1;
    if (scale_size > MAX_SCALE_LEN) return 1;

    static opus_int16 scale_in[MAX_SCALE_LEN];
    static opus_int16 scale_out[MAX_SCALE_LEN];
    for (uint32_t i = 0; i < scale_size; i++) {
      if (!read_i16(&scale_in[i])) return 1;
    }

    uint32_t res_len = nb_subfr * (pre_length + subfr_length);
    if (res_len > MAX_RES_LEN) return 1;

    static opus_int16 LTP_res[MAX_RES_LEN];
    memset(LTP_res, 0, sizeof(opus_int16) * res_len);

    silk_LTP_analysis_filter_FIX(LTP_res, x + x_start, LTPCoef_Q14, pitchL,
                                 invGains_Q16, (opus_int)subfr_length,
                                 (opus_int)nb_subfr, (opus_int)pre_length);

    silk_scale_copy_vector16(scale_out, scale_in, scale_gain_Q16,
                             (opus_int)scale_size);

    for (uint32_t i = 0; i < res_len; i++) {
      if (!write_i16(LTP_res[i])) return 1;
    }
    for (uint32_t i = 0; i < scale_size; i++) {
      if (!write_i16(scale_out[i])) return 1;
    }
  }

  return 0;
}
