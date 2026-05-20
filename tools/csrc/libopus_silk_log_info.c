#include <stdint.h>
#include <stdio.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "silk/SigProc_FIX.h"

#define INPUT_MAGIC "GSLI"
#define OUTPUT_MAGIC "GSLO"

enum {
  MODE_LIN2LOG = 0,
  MODE_LOG2LIN = 1
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

static int32_t eval_sample(uint32_t mode, int32_t x) {
  switch (mode) {
    case MODE_LIN2LOG:
      return silk_lin2log(x);
    case MODE_LOG2LIN:
      return silk_log2lin(x);
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
  if (mode > MODE_LOG2LIN) return 1;

  if (!write_exact(OUTPUT_MAGIC, sizeof(magic)) || !write_u32(1) || !write_u32(count)) return 1;
  for (i = 0; i < count; i++) {
    int32_t x;
    int32_t y;
    if (!read_exact(&x, sizeof(x))) return 1;
    y = eval_sample(mode, x);
    if (!write_exact(&y, sizeof(y))) return 1;
  }
  return 0;
}
