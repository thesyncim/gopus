/* Oracle for the libopus FIXED_POINT warped noise-shaping filter kernels from
 * silk/fixed/noise_shape_analysis_FIX.c (warped_gain, ...).
 *
 * Those helpers are declared static OPUS_INLINE inside the translation unit, so
 * they cannot be linked against directly. The kernel bodies are reproduced here
 * verbatim from the libopus 1.6.1 source; everything they call (silk_SMLAWB,
 * SILK_FIX_CONST, silk_INVERSE32_varQ) comes from the linked FIXED_POINT
 * libopus.a via SigProc_FIX.h.
 *
 * Must be compiled and linked against a libopus configured with
 * --enable-fixed-point (defines FIXED_POINT). Reads a little-endian payload of
 * cases from stdin and writes the bit-exact results to stdout. */

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

#define INPUT_MAGIC "GWLI"
#define OUTPUT_MAGIC "GWLO"

#define MAX_ORDER 24 /* MAX_SHAPE_LPC_ORDER */

/* Verbatim from silk/fixed/noise_shape_analysis_FIX.c (warped_gain). */
static OPUS_INLINE opus_int32 warped_gain( /* gain in Q16*/
    const opus_int32     *coefs_Q24,
    opus_int             lambda_Q16,
    opus_int             order
) {
    opus_int   i;
    opus_int32 gain_Q24;

    lambda_Q16 = -lambda_Q16;
    gain_Q24 = coefs_Q24[ order - 1 ];
    for( i = order - 2; i >= 0; i-- ) {
        gain_Q24 = silk_SMLAWB( coefs_Q24[ i ], gain_Q24, lambda_Q16 );
    }
    gain_Q24  = silk_SMLAWB( SILK_FIX_CONST( 1.0, 24 ), gain_Q24, -lambda_Q16 );
    return silk_INVERSE32_varQ( gain_Q24, 40 );
}

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

  for (uint32_t c = 0; c < count; c++) {
    int32_t lambda_Q16, order;
    if (!read_i32(&lambda_Q16) || !read_i32(&order)) return 1;
    if (order < 1 || order > MAX_ORDER) return 1;

    opus_int32 coefs_Q24[MAX_ORDER];
    for (int32_t i = 0; i < order; i++) {
      if (!read_i32(&coefs_Q24[i])) return 1;
    }

    opus_int32 gain_Q16 =
        warped_gain(coefs_Q24, (opus_int)lambda_Q16, (opus_int)order);

    if (!write_i32((int32_t)gain_Q16)) return 1;
  }

  return 0;
}
