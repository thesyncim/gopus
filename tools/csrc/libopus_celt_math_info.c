#include <stdint.h>
#include <stdio.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "celt/mathops.h"

#define INPUT_MAGIC "GCMI"
#define OUTPUT_MAGIC "GCMO"

enum {
  MODE_LOG2 = 0,
  MODE_EXP2 = 1
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

static float eval_sample(uint32_t mode, float x) {
  switch (mode) {
    case MODE_LOG2:
      return celt_log2(x);
    case MODE_EXP2:
      return celt_exp2(x);
    default:
      return 0;
  }
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
  if (mode > MODE_EXP2) return 1;

  if (!write_exact(OUTPUT_MAGIC, sizeof(magic)) || !write_u32(1) || !write_u32(count)) return 1;
  for (i = 0; i < count; i++) {
    uint32_t bits;
    float x;
    float y;
    uint32_t out_bits;
    if (!read_u32(&bits)) return 1;
    memcpy(&x, &bits, sizeof(x));
    y = eval_sample(mode, x);
    memcpy(&out_bits, &y, sizeof(out_bits));
    if (!write_u32(out_bits)) return 1;
  }
  return 0;
}
