/* Oracle for the libopus FIXED_POINT static silk_limit_warped_coefs helper
 * (silk/fixed/noise_shape_analysis_FIX.c).
 *
 * The helper is `static OPUS_INLINE` so it is not exported; its body is
 * reproduced verbatim below and exercised through the libopus fixed-point
 * macros (silk_SMLAWB, silk_SMULWW, silk_DIV32_varQ, silk_INVERSE32_varQ,
 * silk_SMULWB, silk_SMLABB, silk_bwexpander_32) so the result is bit-exact.
 *
 * Must be compiled and linked against a libopus configured with
 * --enable-fixed-point (defines FIXED_POINT). Reads a little-endian payload of
 * cases from stdin and writes the limited coefs_Q24 arrays to stdout. */

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

#define INPUT_MAGIC "LWCI"
#define OUTPUT_MAGIC "LWCO"

#define MAX_ORDER 24

#if defined(ENABLE_ASSERTIONS) || defined(ENABLE_HARDENING)
void celt_fatal(const char *str, const char *file, int line) {
  (void)str;
  (void)file;
  (void)line;
  abort();
}
#endif

/* Verbatim copy of the static helper from noise_shape_analysis_FIX.c. */
static OPUS_INLINE void limit_warped_coefs(
    opus_int32           *coefs_Q24,
    opus_int             lambda_Q16,
    opus_int32           limit_Q24,
    opus_int             order
) {
    opus_int   i, iter, ind = 0;
    opus_int32 tmp, maxabs_Q24, chirp_Q16, gain_Q16;
    opus_int32 nom_Q16, den_Q24;
    opus_int32 limit_Q20, maxabs_Q20;

    /* Convert to monic coefficients */
    lambda_Q16 = -lambda_Q16;
    for( i = order - 1; i > 0; i-- ) {
        coefs_Q24[ i - 1 ] = silk_SMLAWB( coefs_Q24[ i - 1 ], coefs_Q24[ i ], lambda_Q16 );
    }
    lambda_Q16 = -lambda_Q16;
    nom_Q16  = silk_SMLAWB( SILK_FIX_CONST( 1.0, 16 ), -(opus_int32)lambda_Q16, lambda_Q16 );
    den_Q24  = silk_SMLAWB( SILK_FIX_CONST( 1.0, 24 ), coefs_Q24[ 0 ], lambda_Q16 );
    gain_Q16 = silk_DIV32_varQ( nom_Q16, den_Q24, 24 );
    for( i = 0; i < order; i++ ) {
        coefs_Q24[ i ] = silk_SMULWW( gain_Q16, coefs_Q24[ i ] );
    }
    limit_Q20 = silk_RSHIFT(limit_Q24, 4);
    for( iter = 0; iter < 10; iter++ ) {
        /* Find maximum absolute value */
        maxabs_Q24 = -1;
        for( i = 0; i < order; i++ ) {
            tmp = silk_abs_int32( coefs_Q24[ i ] );
            if( tmp > maxabs_Q24 ) {
                maxabs_Q24 = tmp;
                ind = i;
            }
        }
        /* Use Q20 to avoid any overflow when multiplying by (ind + 1) later. */
        maxabs_Q20 = silk_RSHIFT(maxabs_Q24, 4);
        if( maxabs_Q20 <= limit_Q20 ) {
            /* Coefficients are within range - done */
            return;
        }

        /* Convert back to true warped coefficients */
        for( i = 1; i < order; i++ ) {
            coefs_Q24[ i - 1 ] = silk_SMLAWB( coefs_Q24[ i - 1 ], coefs_Q24[ i ], lambda_Q16 );
        }
        gain_Q16 = silk_INVERSE32_varQ( gain_Q16, 32 );
        for( i = 0; i < order; i++ ) {
            coefs_Q24[ i ] = silk_SMULWW( gain_Q16, coefs_Q24[ i ] );
        }

        /* Apply bandwidth expansion */
        chirp_Q16 = SILK_FIX_CONST( 0.99, 16 ) - silk_DIV32_varQ(
            silk_SMULWB( maxabs_Q20 - limit_Q20, silk_SMLABB( SILK_FIX_CONST( 0.8, 10 ), SILK_FIX_CONST( 0.1, 10 ), iter ) ),
            silk_MUL( maxabs_Q20, ind + 1 ), 22 );
        silk_bwexpander_32( coefs_Q24, order, chirp_Q16 );

        /* Convert to monic warped coefficients */
        lambda_Q16 = -lambda_Q16;
        for( i = order - 1; i > 0; i-- ) {
            coefs_Q24[ i - 1 ] = silk_SMLAWB( coefs_Q24[ i - 1 ], coefs_Q24[ i ], lambda_Q16 );
        }
        lambda_Q16 = -lambda_Q16;
        nom_Q16  = silk_SMLAWB( SILK_FIX_CONST( 1.0, 16 ), -(opus_int32)lambda_Q16,        lambda_Q16 );
        den_Q24  = silk_SMLAWB( SILK_FIX_CONST( 1.0, 24 ), coefs_Q24[ 0 ], lambda_Q16 );
        gain_Q16 = silk_DIV32_varQ( nom_Q16, den_Q24, 24 );
        for( i = 0; i < order; i++ ) {
            coefs_Q24[ i ] = silk_SMULWW( gain_Q16, coefs_Q24[ i ] );
        }
    }
    silk_assert( 0 );
}

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
  uint32_t v;
  if (!read_u32(&v)) return 0;
  *out = (int32_t)v;
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
    int32_t lambdaQ16, limitQ24, order;
    if (!read_i32(&lambdaQ16) || !read_i32(&limitQ24) || !read_i32(&order)) {
      return 1;
    }
    if (order < 1 || order > MAX_ORDER) return 1;

    opus_int32 coefs_Q24[MAX_ORDER];
    for (int32_t i = 0; i < order; i++) {
      if (!read_i32(&coefs_Q24[i])) return 1;
    }

    limit_warped_coefs(coefs_Q24, (opus_int)lambdaQ16, limitQ24, (opus_int)order);

    for (int32_t i = 0; i < order; i++) {
      if (!write_i32(coefs_Q24[i])) return 1;
    }
  }

  return 0;
}
