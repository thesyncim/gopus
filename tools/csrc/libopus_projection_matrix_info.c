#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "celt/arch.h"
#include "celt/float_cast.h"
#include "src/opus_private.h"
#include "opus_defines.h"
#include "src/mapping_matrix.h"

#define INPUT_MAGIC "GPMI"
#define OUTPUT_MAGIC "GPMO"

enum {
  MODE_CHANNEL_OUT_FLOAT = 0,
  MODE_CHANNEL_OUT_SHORT = 1
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

int main(void) {
  char magic[4];
  uint32_t version;
  uint32_t mode;
  uint32_t rows;
  uint32_t cols;
  uint32_t frame_size;
  size_t matrix_count;
  size_t sample_count;
  opus_int16 *matrix_data = NULL;
  opus_res *input = NULL;
  MappingMatrix *matrix = NULL;
  opus_int32 matrix_size;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, 4) || memcmp(magic, INPUT_MAGIC, 4) != 0) {
    fprintf(stderr, "bad input magic\n");
    return 1;
  }
  if (!read_u32(&version) || version != 1 ||
      !read_u32(&mode) ||
      !read_u32(&rows) ||
      !read_u32(&cols) ||
      !read_u32(&frame_size)) {
    fprintf(stderr, "bad header\n");
    return 1;
  }
  if ((mode != MODE_CHANNEL_OUT_FLOAT && mode != MODE_CHANNEL_OUT_SHORT) ||
      rows == 0 || cols == 0 || frame_size == 0 ||
      rows > 255 || cols > 255) {
    fprintf(stderr, "invalid dimensions\n");
    return 1;
  }
  if (rows > SIZE_MAX / cols || cols > SIZE_MAX / frame_size) {
    fprintf(stderr, "dimension overflow\n");
    return 1;
  }

  matrix_count = (size_t)rows * (size_t)cols;
  sample_count = (size_t)cols * (size_t)frame_size;
  matrix_data = (opus_int16 *)malloc(matrix_count * sizeof(*matrix_data));
  input = (opus_res *)malloc(sample_count * sizeof(*input));
  matrix_size = mapping_matrix_get_size((int)rows, (int)cols);
  matrix = (MappingMatrix *)malloc((size_t)matrix_size);
  if (matrix_data == NULL || input == NULL || matrix == NULL || matrix_size <= 0) {
    fprintf(stderr, "allocation failed\n");
    free(matrix_data);
    free(input);
    free(matrix);
    return 1;
  }
  if (!read_exact(matrix_data, matrix_count * sizeof(*matrix_data)) ||
      !read_exact(input, sample_count * sizeof(*input))) {
    fprintf(stderr, "truncated input\n");
    free(matrix_data);
    free(input);
    free(matrix);
    return 1;
  }

  mapping_matrix_init(matrix, (int)rows, (int)cols, 0, matrix_data,
      (opus_int32)(matrix_count * sizeof(*matrix_data)));

  if (mode == MODE_CHANNEL_OUT_FLOAT) {
    float *output = (float *)calloc((size_t)rows * (size_t)frame_size, sizeof(*output));
    if (output == NULL) {
      fprintf(stderr, "output allocation failed\n");
      free(matrix_data);
      free(input);
      free(matrix);
      return 1;
    }
    for (uint32_t col = 0; col < cols; col++) {
      mapping_matrix_multiply_channel_out_float(matrix, input + col, (int)col,
          (int)cols, output, (int)rows, (int)frame_size);
    }
    if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) ||
        !write_u32((uint32_t)((size_t)rows * (size_t)frame_size)) ||
        !write_exact(output, (size_t)rows * (size_t)frame_size * sizeof(*output))) {
      fprintf(stderr, "write failed\n");
      free(output);
      free(matrix_data);
      free(input);
      free(matrix);
      return 1;
    }
    free(output);
  } else {
    opus_int16 *output = (opus_int16 *)calloc((size_t)rows * (size_t)frame_size, sizeof(*output));
    if (output == NULL) {
      fprintf(stderr, "output allocation failed\n");
      free(matrix_data);
      free(input);
      free(matrix);
      return 1;
    }
    for (uint32_t col = 0; col < cols; col++) {
      mapping_matrix_multiply_channel_out_short(matrix, input + col, (int)col,
          (int)cols, output, (int)rows, (int)frame_size);
    }
    if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) ||
        !write_u32((uint32_t)((size_t)rows * (size_t)frame_size)) ||
        !write_exact(output, (size_t)rows * (size_t)frame_size * sizeof(*output))) {
      fprintf(stderr, "write failed\n");
      free(output);
      free(matrix_data);
      free(input);
      free(matrix);
      return 1;
    }
    free(output);
  }

  free(matrix_data);
  free(input);
  free(matrix);
  return 0;
}
