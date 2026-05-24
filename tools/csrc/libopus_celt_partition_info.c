#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "opus_custom.h"

#define INPUT_MAGIC "GVPI"
#define OUTPUT_MAGIC "GVPO"

enum {
  MODE_ZERO_PULSE = 0
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

static int read_float(float *out) {
  uint32_t bits;
  if (!read_u32(&bits)) return 0;
  memcpy(out, &bits, sizeof(*out));
  return 1;
}

static int write_float(float value) {
  uint32_t bits;
  memcpy(&bits, &value, sizeof(bits));
  return write_u32(bits);
}

#include "bands.c"

static int eval_zero_pulse(void) {
  uint32_t n_u, b_u, lm_u, band_u, fill_u, seed_u, lowband_u;
  float gain_f;
  int err = OPUS_OK;
  const CELTMode *mode;
  celt_norm *x;
  celt_norm *lowband = NULL;
  struct band_ctx ctx;
  ec_ctx ec;
  unsigned cm;
  uint32_t i;

  if (!read_u32(&n_u) || !read_u32(&b_u) || !read_u32(&lm_u) ||
      !read_u32(&band_u) || !read_u32(&fill_u) || !read_u32(&seed_u) ||
      !read_u32(&lowband_u) || !read_float(&gain_f)) {
    return 0;
  }
  if (n_u == 0 || n_u > 512 || b_u == 0 || b_u > 16 ||
      band_u >= 21 || lowband_u > 1) {
    return 0;
  }
  mode = opus_custom_mode_create(48000, 960, &err);
  if (mode == NULL || err != OPUS_OK) return 0;
  x = (celt_norm *)calloc(n_u, sizeof(*x));
  if (x == NULL) return 0;
  if (lowband_u) {
    lowband = (celt_norm *)malloc((size_t)n_u * sizeof(*lowband));
    if (lowband == NULL) {
      free(x);
      return 0;
    }
    for (i = 0; i < n_u; i++) {
      if (!read_float(&lowband[i])) {
        free(lowband);
        free(x);
        return 0;
      }
    }
  }

  memset(&ctx, 0, sizeof(ctx));
  memset(&ec, 0, sizeof(ec));
  ctx.encode = 0;
  ctx.resynth = 1;
  ctx.m = mode;
  ctx.i = (int)band_u;
  ctx.spread = SPREAD_NORMAL;
  ctx.ec = &ec;
  ctx.remaining_bits = 0;
  ctx.seed = seed_u;
  ctx.arch = 0;

  cm = quant_partition(&ctx, x, (int)n_u, 0, (int)b_u, lowband,
      (int)(int32_t)lm_u, (opus_val32)gain_f, (int)fill_u);

  if (!write_u32(cm) || !write_u32(ctx.seed) || !write_u32(n_u)) {
    free(lowband);
    free(x);
    return 0;
  }
  for (i = 0; i < n_u; i++) {
    if (!write_float(x[i])) {
      free(lowband);
      free(x);
      return 0;
    }
  }
  free(lowband);
  free(x);
  return 1;
}

int main(void) {
  char magic[4];
  uint32_t version;
  uint32_t mode;
  uint32_t count;
  uint32_t i;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, sizeof(magic)) ||
      memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) {
    return 1;
  }
  if (!read_u32(&version) || version != 1 ||
      !read_u32(&mode) || !read_u32(&count)) {
    return 1;
  }
  if (mode != MODE_ZERO_PULSE) return 1;
  if (!write_exact(OUTPUT_MAGIC, sizeof(magic)) ||
      !write_u32(1) || !write_u32(count)) {
    return 1;
  }
  for (i = 0; i < count; i++) {
    if (!eval_zero_pulse()) return 1;
  }
  return 0;
}
