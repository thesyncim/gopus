#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"

#define INPUT_MAGIC "GVRI"
#define OUTPUT_MAGIC "GVRO"

enum {
  MODE_NORMALISE_RESIDUAL = 0
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

#include "vq.c"

static int eval_normalise_residual(void) {
  uint32_t n_u, b_u;
  float gain_f, energy_f;
  int *iy;
  celt_norm *x;
  unsigned collapse;
  uint32_t i;

  if (!read_u32(&n_u) || !read_u32(&b_u) ||
      !read_float(&gain_f) || !read_float(&energy_f)) {
    return 0;
  }
  if (n_u == 0 || n_u > 512 || b_u == 0 || b_u > n_u || energy_f <= 0) {
    return 0;
  }
  iy = (int *)malloc((size_t)n_u * sizeof(*iy));
  x = (celt_norm *)malloc((size_t)n_u * sizeof(*x));
  if (iy == NULL || x == NULL) {
    free(iy);
    free(x);
    return 0;
  }
  for (i = 0; i < n_u; i++) {
    uint32_t v;
    if (!read_u32(&v)) {
      free(iy);
      free(x);
      return 0;
    }
    iy[i] = (int)(int32_t)v;
  }

  collapse = extract_collapse_mask(iy, (int)n_u, (int)b_u);
  normalise_residual(iy, x, (int)n_u, (opus_val32)energy_f,
      (opus_val32)gain_f, 0);

  if (!write_u32(collapse) || !write_u32(n_u)) {
    free(iy);
    free(x);
    return 0;
  }
  for (i = 0; i < n_u; i++) {
    if (!write_float(x[i])) {
      free(iy);
      free(x);
      return 0;
    }
  }
  free(iy);
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
  if (mode != MODE_NORMALISE_RESIDUAL) return 1;
  if (!write_exact(OUTPUT_MAGIC, sizeof(magic)) ||
      !write_u32(1) || !write_u32(count)) {
    return 1;
  }
  for (i = 0; i < count; i++) {
    if (!eval_normalise_residual()) return 1;
  }
  return 0;
}
