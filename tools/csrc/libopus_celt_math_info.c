#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "celt/bands.h"
#include "celt/entcode.h"
#include "celt/mathops.h"
#include "celt/vq.h"

#define INPUT_MAGIC "GCMI"
#define OUTPUT_MAGIC "GCMO"

enum {
  MODE_LOG2 = 0,
  MODE_EXP2 = 1,
  MODE_FRAC_MUL16 = 2,
  MODE_BITEXACT_COS = 3,
  MODE_BITEXACT_LOG2TAN = 4,
  MODE_ISQRT32 = 5,
  MODE_CELT_UDIV = 6,
  MODE_CELT_SUDIV = 7,
  MODE_BITEXACT_LOG2TAN_THETA = 8,
  MODE_ATAN_NORM = 9,
  MODE_ATAN2P_NORM = 10,
  MODE_COS_NORM2 = 11,
  MODE_STEREO_ITHETA_Q30 = 12,
  MODE_LOG = 13,
  MODE_SIN = 14,
  MODE_BITEXACT_THETA_PAIR = 15
};

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
  return read_exact(out, sizeof(*out));
}

static int write_u32(uint32_t value) {
  return write_exact(&value, sizeof(value));
}

static int eval_record(uint32_t mode) {
  uint32_t a;
  uint32_t b;
  uint32_t out_bits;
  uint32_t n;
  uint32_t stereo;
  float x;
  float y;
  celt_norm *vx;
  celt_norm *vy;
  uint32_t i;

  switch (mode) {
    case MODE_LOG2:
      if (!read_u32(&a)) return 0;
      memcpy(&x, &a, sizeof(x));
      y = celt_log2(x);
      memcpy(&out_bits, &y, sizeof(out_bits));
      return write_u32(out_bits);
    case MODE_EXP2:
      if (!read_u32(&a)) return 0;
      memcpy(&x, &a, sizeof(x));
      y = celt_exp2(x);
      memcpy(&out_bits, &y, sizeof(out_bits));
      return write_u32(out_bits);
    case MODE_FRAC_MUL16:
      if (!read_u32(&a) || !read_u32(&b)) return 0;
      return write_u32((uint32_t)(int32_t)FRAC_MUL16((int32_t)a, (int32_t)b));
    case MODE_BITEXACT_COS:
      if (!read_u32(&a)) return 0;
      return write_u32((uint32_t)(int32_t)bitexact_cos((opus_int16)(int32_t)a));
    case MODE_BITEXACT_LOG2TAN:
      if (!read_u32(&a) || !read_u32(&b)) return 0;
      return write_u32((uint32_t)(int32_t)bitexact_log2tan((int)(int32_t)a, (int)(int32_t)b));
    case MODE_ISQRT32:
      if (!read_u32(&a)) return 0;
      return write_u32(a == 0 ? 0 : (uint32_t)isqrt32(a));
    case MODE_CELT_UDIV:
      if (!read_u32(&a) || !read_u32(&b) || b == 0) return 0;
      return write_u32(celt_udiv(a, b));
    case MODE_CELT_SUDIV:
      if (!read_u32(&a) || !read_u32(&b) || b == 0) return 0;
      return write_u32((uint32_t)(int32_t)celt_sudiv((int32_t)a, (int32_t)b));
    case MODE_BITEXACT_LOG2TAN_THETA:
      if (!read_u32(&a)) return 0;
      return write_u32((uint32_t)(int32_t)bitexact_log2tan(
          bitexact_cos((opus_int16)(16384 - (int32_t)a)),
          bitexact_cos((opus_int16)(int32_t)a)));
    case MODE_ATAN_NORM:
      if (!read_u32(&a)) return 0;
      memcpy(&x, &a, sizeof(x));
      y = celt_atan_norm(x);
      memcpy(&out_bits, &y, sizeof(out_bits));
      return write_u32(out_bits);
    case MODE_ATAN2P_NORM:
      if (!read_u32(&a) || !read_u32(&b)) return 0;
      memcpy(&x, &a, sizeof(x));
      memcpy(&y, &b, sizeof(y));
      x = celt_atan2p_norm(y, x);
      memcpy(&out_bits, &x, sizeof(out_bits));
      return write_u32(out_bits);
    case MODE_COS_NORM2:
      if (!read_u32(&a)) return 0;
      memcpy(&x, &a, sizeof(x));
      y = celt_cos_norm2(x);
      memcpy(&out_bits, &y, sizeof(out_bits));
      return write_u32(out_bits);
    case MODE_STEREO_ITHETA_Q30:
      if (!read_u32(&stereo) || !read_u32(&n) || n == 0 || n > 256) return 0;
      vx = (celt_norm *)malloc(n * sizeof(*vx));
      vy = (celt_norm *)malloc(n * sizeof(*vy));
      if (vx == NULL || vy == NULL) {
        free(vx);
        free(vy);
        return 0;
      }
      for (i = 0; i < n; i++) {
        if (!read_u32(&a)) {
          free(vx);
          free(vy);
          return 0;
        }
        memcpy(&vx[i], &a, sizeof(vx[i]));
      }
      for (i = 0; i < n; i++) {
        if (!read_u32(&a)) {
          free(vx);
          free(vy);
          return 0;
        }
        memcpy(&vy[i], &a, sizeof(vy[i]));
      }
      out_bits = (uint32_t)(int32_t)stereo_itheta(vx, vy, stereo != 0, (int)n, 0);
      free(vx);
      free(vy);
      return write_u32(out_bits);
    case MODE_LOG:
      if (!read_u32(&a)) return 0;
      memcpy(&x, &a, sizeof(x));
      y = celt_log(x);
      memcpy(&out_bits, &y, sizeof(out_bits));
      return write_u32(out_bits);
    case MODE_SIN:
      if (!read_u32(&a)) return 0;
      memcpy(&x, &a, sizeof(x));
      y = celt_sin(x);
      memcpy(&out_bits, &y, sizeof(out_bits));
      return write_u32(out_bits);
    case MODE_BITEXACT_THETA_PAIR:
      if (!read_u32(&a)) return 0;
      b = (uint32_t)(int32_t)bitexact_cos((opus_int16)(int32_t)a);
      out_bits = (uint32_t)(int32_t)bitexact_cos((opus_int16)(16384 - (int32_t)a));
      return write_u32(b) && write_u32(out_bits) &&
             write_u32((uint32_t)(int32_t)bitexact_log2tan((int)(int32_t)out_bits, (int)(int32_t)b));
  }
  return 0;
}

int main(void) {
  char magic[4];
  uint32_t version;
  uint32_t mode;
  uint32_t count;
  uint32_t i;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) return 1;
  if (!read_u32(&version) || version != 1 || !read_u32(&mode) || !read_u32(&count)) return 1;
  if (mode > MODE_BITEXACT_THETA_PAIR) return 1;

  if (!write_exact(OUTPUT_MAGIC, sizeof(magic)) || !write_u32(1) || !write_u32(count)) return 1;
  for (i = 0; i < count; i++) {
    if (!eval_record(mode)) return 1;
  }
  return 0;
}
