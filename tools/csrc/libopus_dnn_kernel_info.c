#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "dnn/vec.h"

#define INPUT_MAGIC "GDKI"
#define OUTPUT_MAGIC "GDKO"

enum {
  MODE_SGEMV = 0,
  MODE_CGEMV8X4 = 1
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

static int write_output(float *out, uint32_t rows) {
  uint32_t i;
  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(rows)) return 0;
  for (i = 0; i < rows; i++) {
    if (!write_float(out[i])) return 0;
  }
  return 1;
}

static int run_sgemv(uint32_t rows, uint32_t cols, uint32_t col_stride) {
  uint32_t weights_count;
  float *weights = NULL;
  float *x = NULL;
  float *out = NULL;
  uint32_t i;
  int ok;

  if (rows == 0 || cols == 0 || col_stride < rows || rows > 8192 || cols > 2048) return 0;
  weights_count = cols * col_stride;
  if (weights_count / col_stride != cols) return 0;
  weights = (float *)malloc(weights_count * sizeof(*weights));
  x = (float *)malloc(cols * sizeof(*x));
  out = (float *)malloc(rows * sizeof(*out));
  if (weights == NULL || x == NULL || out == NULL) goto fail;
  for (i = 0; i < weights_count; i++) {
    if (!read_float(&weights[i])) goto fail;
  }
  for (i = 0; i < cols; i++) {
    if (!read_float(&x[i])) goto fail;
  }
  sgemv(out, weights, (int)rows, (int)cols, (int)col_stride, x);
  ok = write_output(out, rows);
  free(weights);
  free(x);
  free(out);
  return ok;
fail:
  free(weights);
  free(x);
  free(out);
  return 0;
}

static int run_cgemv8x4(uint32_t rows, uint32_t cols) {
  uint32_t weights_count;
  opus_int8 *weights = NULL;
  float *scale = NULL;
  float *x = NULL;
  float *out = NULL;
  uint32_t i;
  int ok;

  if (rows == 0 || cols == 0 || (rows & 7) != 0 || (cols & 7) != 0 || rows > 8192 || cols > 2048) return 0;
  weights_count = rows * cols;
  if (weights_count / cols != rows) return 0;
  weights = (opus_int8 *)malloc(weights_count * sizeof(*weights));
  scale = (float *)malloc(rows * sizeof(*scale));
  x = (float *)malloc(cols * sizeof(*x));
  out = (float *)malloc(rows * sizeof(*out));
  if (weights == NULL || scale == NULL || x == NULL || out == NULL) goto fail;
  if (!read_exact(weights, weights_count * sizeof(*weights))) goto fail;
  for (i = 0; i < rows; i++) {
    if (!read_float(&scale[i])) goto fail;
  }
  for (i = 0; i < cols; i++) {
    if (!read_float(&x[i])) goto fail;
  }
  cgemv8x4(out, weights, scale, (int)rows, (int)cols, x);
  ok = write_output(out, rows);
  free(weights);
  free(scale);
  free(x);
  free(out);
  return ok;
fail:
  free(weights);
  free(scale);
  free(x);
  free(out);
  return 0;
}

int main(void) {
  char magic[4];
  uint32_t version;
  uint32_t mode;
  uint32_t rows;
  uint32_t cols;
  uint32_t col_stride;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) return 1;
  if (!read_u32(&version) || version != 1 || !read_u32(&mode) || !read_u32(&rows) || !read_u32(&cols) || !read_u32(&col_stride)) return 1;
  switch (mode) {
    case MODE_SGEMV:
      return run_sgemv(rows, cols, col_stride) ? 0 : 1;
    case MODE_CGEMV8X4:
      return run_cgemv8x4(rows, cols) ? 0 : 1;
  }
  return 1;
}
