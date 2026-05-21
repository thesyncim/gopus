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

#define INPUT_MAGIC "GDAI"
#define OUTPUT_MAGIC "GDAO"

enum {
  MODE_SIGMOID = 0,
  MODE_TANH = 1,
  MODE_EXP = 2
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
  uint32_t count;
  float *in = NULL;
  float *out = NULL;
  uint32_t i;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) return 1;
  if (!read_u32(&version) || version != 1 || !read_u32(&mode) || !read_u32(&count)) return 1;
  if (mode > MODE_EXP) return 1;

  if (count != 0) {
    in = (float *)malloc(count * sizeof(*in));
    out = (float *)malloc(count * sizeof(*out));
    if (in == NULL || out == NULL) {
      free(in);
      free(out);
      return 1;
    }
  }
  for (i = 0; i < count; i++) {
    uint32_t bits;
    if (!read_u32(&bits)) {
      free(in);
      free(out);
      return 1;
    }
    memcpy(&in[i], &bits, sizeof(in[i]));
  }

  switch (mode) {
    case MODE_SIGMOID:
      vec_sigmoid(out, in, (int)count);
      break;
    case MODE_TANH:
      vec_tanh(out, in, (int)count);
      break;
    case MODE_EXP:
      softmax(out, in, (int)count);
      break;
  }

  if (!write_exact(OUTPUT_MAGIC, sizeof(magic)) || !write_u32(1) || !write_u32(count)) {
    free(in);
    free(out);
    return 1;
  }
  for (i = 0; i < count; i++) {
    uint32_t bits;
    memcpy(&bits, &out[i], sizeof(bits));
    if (!write_u32(bits)) {
      free(in);
      free(out);
      return 1;
    }
  }
  free(in);
  free(out);
  return 0;
}
