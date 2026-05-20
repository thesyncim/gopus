#include <stdint.h>
#include <stdio.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "silk/SigProc_FIX.h"

#define INPUT_MAGIC "GSFI"
#define OUTPUT_MAGIC "GSFO"

enum {
  MODE_RSHIFT_ROUND = 0,
  MODE_SAT16 = 1,
  MODE_SAT16_RSHIFT_ROUND10 = 2,
  MODE_SAT16_RSHIFT_ROUND15 = 3,
  MODE_LSHIFT_SAT32 = 4
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

static int32_t eval_sample(uint32_t mode, int32_t x, uint32_t shift) {
  switch (mode) {
    case MODE_RSHIFT_ROUND:
      return silk_RSHIFT_ROUND(x, (int)shift);
    case MODE_SAT16:
      return silk_SAT16(x);
    case MODE_SAT16_RSHIFT_ROUND10:
      return silk_SAT16(silk_RSHIFT_ROUND(x, 10));
    case MODE_SAT16_RSHIFT_ROUND15:
      return silk_SAT16(silk_RSHIFT_ROUND(x, 15));
    case MODE_LSHIFT_SAT32:
      return silk_LSHIFT_SAT32(x, (int)shift);
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
  if (mode > MODE_LSHIFT_SAT32) return 1;

  if (!write_exact(OUTPUT_MAGIC, sizeof(magic)) || !write_u32(1) || !write_u32(count)) return 1;
  for (i = 0; i < count; i++) {
    int32_t x;
    uint32_t shift;
    int32_t y;
    if (!read_exact(&x, sizeof(x)) || !read_u32(&shift)) return 1;
    y = eval_sample(mode, x, shift);
    if (!write_exact(&y, sizeof(y))) return 1;
  }
  return 0;
}
