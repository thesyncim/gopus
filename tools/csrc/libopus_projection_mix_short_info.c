/* Direct selected-C mapping_matrix_multiply_channel_in_short oracle. */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "arch.h"
#include "float_cast.h"
#include "opus_private.h"
#include "mapping_matrix.h"

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (fread(b, 1, 4, stdin) != 4) return 0;
  *out = (uint32_t)b[0] | (uint32_t)b[1] << 8 |
      (uint32_t)b[2] << 16 | (uint32_t)b[3] << 24;
  return 1;
}

static int read_i16(opus_int16 *out) {
  unsigned char b[2];
  if (fread(b, 1, 2, stdin) != 2) return 0;
  *out = (opus_int16)((uint16_t)b[0] | (uint16_t)b[1] << 8);
  return 1;
}

static int write_u32(uint32_t value) {
  unsigned char b[4] = {(unsigned char)value, (unsigned char)(value >> 8),
      (unsigned char)(value >> 16), (unsigned char)(value >> 24)};
  return fwrite(b, 1, 4, stdout) == 4;
}

int main(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 1;
#endif
  char magic[4];
  uint32_t version, rows, cols, frame_size;
  if (fread(magic, 1, 4, stdin) != 4 || memcmp(magic, "GMSI", 4) ||
      !read_u32(&version) || version != 1 || !read_u32(&rows) ||
      !read_u32(&cols) || !read_u32(&frame_size) ||
      rows < 1 || rows > 16 || cols < 1 || cols > 16 ||
      frame_size < 1 || frame_size > 5760 || sizeof(opus_res) != 4) return 2;
  size_t n_matrix = (size_t)rows * cols;
  size_t n_input = (size_t)cols * frame_size;
  size_t n_output = (size_t)rows * frame_size;
  opus_int16 *coefficients = malloc(n_matrix * sizeof(*coefficients));
  opus_int16 *input = malloc(n_input * sizeof(*input));
  opus_res *output = calloc(n_output, sizeof(*output));
  opus_int32 matrix_bytes = mapping_matrix_get_size((int)rows, (int)cols);
  MappingMatrix *matrix = matrix_bytes > 0 ? malloc((size_t)matrix_bytes) : NULL;
  if (!coefficients || !input || !output || !matrix) return 3;
  for (size_t i = 0; i < n_matrix; i++)
    if (!read_i16(&coefficients[i])) return 4;
  for (size_t i = 0; i < n_input; i++)
    if (!read_i16(&input[i])) return 5;
  mapping_matrix_init(matrix, (int)rows, (int)cols, 0, coefficients,
      (opus_int32)(n_matrix * sizeof(*coefficients)));
  for (uint32_t row = 0; row < rows; row++) {
    mapping_matrix_multiply_channel_in_short(matrix, input, (int)cols,
        output + row, (int)row, (int)rows, (int)frame_size);
  }
  if (fwrite("GMSO", 1, 4, stdout) != 4 || !write_u32(1) ||
      !write_u32((uint32_t)n_output)) return 6;
  for (size_t i = 0; i < n_output; i++) {
    uint32_t bits;
    memcpy(&bits, &output[i], 4);
    if (!write_u32(bits)) return 7;
  }
  free(coefficients);
  free(input);
  free(output);
  free(matrix);
  return ferror(stdin) || ferror(stdout) ? 8 : 0;
}
