#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#ifdef GOPUS_DIRECT_SCALAR_DNN
#include "dnn/vec.h"
#else
#include "celt/cpu_support.h"
#include "dnn/nnet.h"
#endif

#define INPUT_MAGIC "GDKI"
#define OUTPUT_MAGIC "GDKO"

enum {
  MODE_SGEMV = 0,
  MODE_CGEMV8X4 = 1,
  MODE_LINEAR_CGEMV8X4 = 2,
  MODE_LINEAR_SPARSE_SGEMV = 3,
  MODE_CONV2D_3X3 = 4
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
  unsigned char bytes[4];
  if (!read_exact(bytes, sizeof(bytes))) return 0;
  *out = (uint32_t)bytes[0] | ((uint32_t)bytes[1] << 8) |
      ((uint32_t)bytes[2] << 16) | ((uint32_t)bytes[3] << 24);
  return 1;
}

static int write_u32(uint32_t value) {
  unsigned char bytes[4];
  bytes[0] = (unsigned char)value;
  bytes[1] = (unsigned char)(value >> 8);
  bytes[2] = (unsigned char)(value >> 16);
  bytes[3] = (unsigned char)(value >> 24);
  return write_exact(bytes, sizeof(bytes));
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

static int write_output(float *out, uint32_t rows, int arch) {
  uint32_t i;
  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(rows) || !write_u32((uint32_t)arch)) return 0;
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
  int arch = 0;

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
#ifdef GOPUS_DIRECT_SCALAR_DNN
  sgemv(out, weights, (int)rows, (int)cols, (int)col_stride, x);
#else
  if (col_stride != rows) goto fail;
  arch = opus_select_arch();
#if defined(OPUS_HAVE_RTCD) && defined(OPUS_X86_MAY_HAVE_AVX2)
  if (arch == 4 && DNN_COMPUTE_LINEAR_IMPL[arch & OPUS_ARCHMASK] != compute_linear_avx2) goto fail;
#endif
  {
    LinearLayer layer = {0};
    layer.float_weights = weights;
    layer.nb_inputs = (int)cols;
    layer.nb_outputs = (int)rows;
    compute_linear(&layer, out, x, arch);
  }
#endif
  ok = write_output(out, rows, arch);
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
  int arch = 0;

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
#ifdef GOPUS_DIRECT_SCALAR_DNN
  cgemv8x4(out, weights, scale, (int)rows, (int)cols, x);
#else
  arch = opus_select_arch();
#if defined(OPUS_HAVE_RTCD) && defined(OPUS_X86_MAY_HAVE_AVX2)
  if (arch == 4 && DNN_COMPUTE_LINEAR_IMPL[arch & OPUS_ARCHMASK] != compute_linear_avx2) goto fail;
#endif
  {
    LinearLayer layer = {0};
    layer.weights = weights;
    layer.scale = scale;
    layer.nb_inputs = (int)cols;
    layer.nb_outputs = (int)rows;
    compute_linear(&layer, out, x, arch);
  }
#endif
  ok = write_output(out, rows, arch);
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

#ifndef GOPUS_DIRECT_SCALAR_DNN
static int run_linear(uint32_t rows, uint32_t cols, uint32_t idx_count, int float_sparse) {
  uint32_t weight_count;
  int *idx = NULL;
  opus_int8 *weights = NULL;
  float *float_weights = NULL;
  float *scale = NULL;
  float *x = NULL;
  float *bias = NULL;
  float *subias = NULL;
  float *out = NULL;
  uint32_t i;
  int arch;
  int ok = 0;
  LinearLayer layer = {0};

  if (rows == 0 || cols == 0 || (rows & 7) != 0 || (cols & 7) != 0 || rows > 8192 || cols > 2048) return 0;
  if (float_sparse && idx_count == 0) return 0;
  if (idx_count > 0) {
    uint32_t pos = 0;
    uint32_t blocks = 0;
    if (idx_count > rows / 8 + (rows / 8) * (cols / 4)) return 0;
    idx = (int *)malloc(idx_count * sizeof(*idx));
    if (idx == NULL) goto done;
    for (i = 0; i < idx_count; i++) {
      uint32_t value;
      if (!read_u32(&value) || value > (uint32_t)INT32_MAX) goto done;
      idx[i] = (int)value;
    }
    for (i = 0; i < rows / 8; i++) {
      uint32_t count;
      if (pos >= idx_count) goto done;
      count = (uint32_t)idx[pos++];
      if (count > cols / 4 || pos + count > idx_count) goto done;
      for (uint32_t j = 0; j < count; j++) {
        if (idx[pos + j] < 0 || (uint32_t)idx[pos + j] > cols - 4) goto done;
      }
      pos += count;
      blocks += count;
    }
    if (pos != idx_count) goto done;
    weight_count = blocks * 32;
  } else {
    if (rows > UINT32_MAX / cols) return 0;
    weight_count = rows * cols;
  }
  if (float_sparse) float_weights = (float *)malloc((weight_count ? weight_count : 1) * sizeof(*float_weights));
  else weights = (opus_int8 *)malloc(weight_count ? weight_count : 1);
  scale = (float *)malloc(rows * sizeof(*scale));
  x = (float *)malloc(cols * sizeof(*x));
  bias = (float *)malloc(rows * sizeof(*bias));
  subias = (float *)malloc(rows * sizeof(*subias));
  out = (float *)malloc(rows * sizeof(*out));
  if ((float_sparse ? float_weights == NULL : weights == NULL) || scale == NULL || x == NULL || bias == NULL || subias == NULL || out == NULL) goto done;
  if (float_sparse) {
    for (i = 0; i < weight_count; i++) if (!read_float(&float_weights[i])) goto done;
  } else if (!read_exact(weights, weight_count)) goto done;
  for (i = 0; i < rows; i++) if (!read_float(&scale[i])) goto done;
  for (i = 0; i < cols; i++) if (!read_float(&x[i])) goto done;
  for (i = 0; i < rows; i++) if (!read_float(&bias[i])) goto done;
  for (i = 0; i < rows; i++) if (!read_float(&subias[i])) goto done;
  layer.bias = bias;
  layer.subias = subias;
  layer.weights = weights;
  layer.float_weights = float_weights;
  layer.weights_idx = idx;
  layer.scale = scale;
  layer.nb_inputs = (int)cols;
  layer.nb_outputs = (int)rows;
  arch = opus_select_arch();
#if defined(OPUS_HAVE_RTCD) && defined(OPUS_X86_MAY_HAVE_AVX2)
  if (arch == 4 && DNN_COMPUTE_LINEAR_IMPL[arch & OPUS_ARCHMASK] != compute_linear_avx2) goto done;
#endif
  compute_linear(&layer, out, x, arch);
  ok = write_output(out, rows, arch);
done:
  free(idx);
  free(weights);
  free(float_weights);
  free(scale);
  free(x);
  free(bias);
  free(subias);
  free(out);
  return ok;
}
#endif

#ifndef GOPUS_DIRECT_SCALAR_DNN
static int check_selected_conv2d_arch(int arch) {
#if defined(OPUS_HAVE_RTCD) && defined(OPUS_X86_MAY_HAVE_AVX2)
  if (arch == 4 && DNN_COMPUTE_CONV2D_IMPL[arch & OPUS_ARCHMASK] != compute_conv2d_avx2) return 0;
#endif
  (void)arch;
  return 1;
}

/* compute_conv2d with a 3x3 kernel, bias and tanh activation. The payload
   carries in_channels (cols) and out_channels (rows); height is the third
   header field and hstride equals height. */
static int run_conv2d_3x3(uint32_t out_channels, uint32_t in_channels, uint32_t height) {
  float *weights = NULL;
  float *bias = NULL;
  float *mem = NULL;
  float *in = NULL;
  float *out = NULL;
  uint32_t i;
  uint32_t weight_count;
  uint32_t time_stride;
  int arch;
  int ok = 0;
  Conv2dLayer conv = {0};

  if (out_channels == 0 || in_channels == 0 || height == 0 || out_channels > 64 || in_channels > 64 || height > 512) return 0;
  time_stride = in_channels * (height + 2);
  if (3 * time_stride > 8192) return 0;
  weight_count = out_channels * in_channels * 9;
  weights = (float *)malloc(weight_count * sizeof(*weights));
  bias = (float *)malloc(out_channels * sizeof(*bias));
  mem = (float *)malloc(2 * time_stride * sizeof(*mem));
  in = (float *)malloc(time_stride * sizeof(*in));
  out = (float *)malloc(out_channels * height * sizeof(*out));
  if (weights == NULL || bias == NULL || mem == NULL || in == NULL || out == NULL) goto done;
  for (i = 0; i < weight_count; i++) if (!read_float(&weights[i])) goto done;
  for (i = 0; i < out_channels; i++) if (!read_float(&bias[i])) goto done;
  for (i = 0; i < 2 * time_stride; i++) if (!read_float(&mem[i])) goto done;
  for (i = 0; i < time_stride; i++) if (!read_float(&in[i])) goto done;
  conv.bias = bias;
  conv.float_weights = weights;
  conv.in_channels = (int)in_channels;
  conv.out_channels = (int)out_channels;
  conv.ktime = 3;
  conv.kheight = 3;
  arch = opus_select_arch();
  if (!check_selected_conv2d_arch(arch)) goto done;
  compute_conv2d(&conv, out, mem, in, (int)height, (int)height, ACTIVATION_TANH, arch);
  ok = write_output(out, out_channels * height, arch);
done:
  free(weights);
  free(bias);
  free(mem);
  free(in);
  free(out);
  return ok;
}
#endif

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
    case MODE_LINEAR_CGEMV8X4:
#ifdef GOPUS_DIRECT_SCALAR_DNN
      return 1;
#else
      return run_linear(rows, cols, col_stride, 0) ? 0 : 1;
#endif
    case MODE_LINEAR_SPARSE_SGEMV:
#ifdef GOPUS_DIRECT_SCALAR_DNN
      return 1;
#else
      return run_linear(rows, cols, col_stride, 1) ? 0 : 1;
#endif
    case MODE_CONV2D_3X3:
#ifdef GOPUS_DIRECT_SCALAR_DNN
      return 1;
#else
      return run_conv2d_3x3(rows, cols, col_stride) ? 0 : 1;
#endif
  }
  return 1;
}
