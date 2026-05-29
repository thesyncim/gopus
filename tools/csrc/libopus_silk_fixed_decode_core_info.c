/* Oracle for the libopus FIXED_POINT silk_decode_core synthesis math.
 *
 * Must be compiled and linked against a libopus configured with
 * --enable-fixed-point (defines FIXED_POINT). It evaluates the two
 * self-contained synthesis loops of silk/decode_core.c on fully specified
 * inputs:
 *   - the voiced long-term prediction (LTP) loop that produces res_Q14 and
 *     pushes the doubled residual into the sLTP_Q15 ring buffer, and
 *   - the short-term LPC synthesis loop that produces sLPC_Q14 and the final
 *     gain-scaled int16 output.
 *
 * Reads a little-endian payload of cases from stdin and writes the bit-exact
 * int16 outputs (followed by the per-case res_Q14 int32 values for the voiced
 * cases) to stdout. */

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

#define INPUT_MAGIC "GSDI"
#define OUTPUT_MAGIC "GSDO"

#define LTP_ORDER 5
#define MAX_LPC_ORDER 16
#define MAX_SUBFR 80
/* sLTP_Q15 ring buffer large enough for max lag + history + one subframe. */
#define SLTP_SIZE 2048

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

static int write_i32(int32_t value) {
  return write_u32((uint32_t)value);
}

static int write_i16(int16_t value) {
  uint16_t u = (uint16_t)value;
  unsigned char b[2];
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
    uint32_t voiced, order, subfr_length, lag;
    int32_t gain_q10;
    if (!read_u32(&voiced) || !read_u32(&order) || !read_u32(&subfr_length) ||
        !read_u32(&lag) || !read_i32(&gain_q10)) {
      return 1;
    }
    if (order > MAX_LPC_ORDER || subfr_length > MAX_SUBFR) return 1;

    opus_int16 A_Q12[MAX_LPC_ORDER];
    opus_int16 B_Q14[LTP_ORDER];
    opus_int32 sLPC_Q14[MAX_LPC_ORDER + MAX_SUBFR];
    static opus_int32 sLTP_Q15[SLTP_SIZE];
    opus_int32 exc_Q14[MAX_SUBFR];
    opus_int32 res_Q14[MAX_SUBFR];
    opus_int16 xq[MAX_SUBFR];

    for (uint32_t i = 0; i < order; i++) {
      if (!read_i16(&A_Q12[i])) return 1;
    }
    for (uint32_t i = 0; i < LTP_ORDER; i++) {
      if (!read_i16(&B_Q14[i])) return 1;
    }
    for (uint32_t i = 0; i < MAX_LPC_ORDER; i++) {
      if (!read_i32(&sLPC_Q14[i])) return 1;
    }
    for (uint32_t i = 0; i < subfr_length; i++) {
      if (!read_i32(&exc_Q14[i])) return 1;
    }

    /* sLTP buffer index: place the write head past the history needed for the
     * pitch lag so reads at pred_lag_ptr stay in-bounds. The host fills the
     * preceding lag + LTP_ORDER/2 entries with the provided sLTP_Q15 history. */
    opus_int sLTP_buf_idx = (opus_int)(lag + LTP_ORDER) + 8;
    if (voiced) {
      for (uint32_t i = 0; i < lag + LTP_ORDER; i++) {
        if (!read_i32(&sLTP_Q15[sLTP_buf_idx - 1 - i])) return 1;
      }
    }

    opus_int32 *pres_Q14;
    if (voiced) {
      opus_int32 *pred_lag_ptr = &sLTP_Q15[sLTP_buf_idx - (opus_int)lag + LTP_ORDER / 2];
      pres_Q14 = res_Q14;
      for (uint32_t i = 0; i < subfr_length; i++) {
        opus_int32 LTP_pred_Q13 = 2;
        LTP_pred_Q13 = silk_SMLAWB(LTP_pred_Q13, pred_lag_ptr[0], B_Q14[0]);
        LTP_pred_Q13 = silk_SMLAWB(LTP_pred_Q13, pred_lag_ptr[-1], B_Q14[1]);
        LTP_pred_Q13 = silk_SMLAWB(LTP_pred_Q13, pred_lag_ptr[-2], B_Q14[2]);
        LTP_pred_Q13 = silk_SMLAWB(LTP_pred_Q13, pred_lag_ptr[-3], B_Q14[3]);
        LTP_pred_Q13 = silk_SMLAWB(LTP_pred_Q13, pred_lag_ptr[-4], B_Q14[4]);
        pred_lag_ptr++;

        pres_Q14[i] = silk_ADD_LSHIFT32(exc_Q14[i], LTP_pred_Q13, 1);
        sLTP_Q15[sLTP_buf_idx] = silk_LSHIFT(pres_Q14[i], 1);
        sLTP_buf_idx++;
      }
    } else {
      pres_Q14 = exc_Q14;
    }

    for (uint32_t i = 0; i < subfr_length; i++) {
      opus_int32 LPC_pred_Q10 = silk_RSHIFT((opus_int32)order, 1);
      for (uint32_t j = 0; j < order; j++) {
        LPC_pred_Q10 = silk_SMLAWB(LPC_pred_Q10,
                                   sLPC_Q14[MAX_LPC_ORDER + (int)i - (int)j - 1],
                                   A_Q12[j]);
      }
      sLPC_Q14[MAX_LPC_ORDER + i] =
          silk_ADD_SAT32(pres_Q14[i], silk_LSHIFT_SAT32(LPC_pred_Q10, 4));
      xq[i] = (opus_int16)silk_SAT16(
          silk_RSHIFT_ROUND(silk_SMULWW(sLPC_Q14[MAX_LPC_ORDER + i], gain_q10), 8));
    }

    for (uint32_t i = 0; i < subfr_length; i++) {
      if (!write_i16(xq[i])) return 1;
    }
    for (uint32_t i = 0; i < subfr_length; i++) {
      int32_t v = voiced ? res_Q14[i] : exc_Q14[i];
      if (!write_i32(v)) return 1;
    }
  }

  return 0;
}
