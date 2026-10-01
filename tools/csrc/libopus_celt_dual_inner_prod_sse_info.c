#include <stdint.h>
#include <stdio.h>
#include <string.h>

#ifdef HAVE_CONFIG_H
#include "config.h"
#endif

#include "celt/pitch.h"

#define MAX_CASES 64
#define MAX_LENGTH 4096

static int read_u32(uint32_t *value) {
  unsigned char bytes[4];
  if (fread(bytes, sizeof(bytes), 1, stdin) != 1) return 0;
  *value = (uint32_t)bytes[0] | ((uint32_t)bytes[1] << 8) |
      ((uint32_t)bytes[2] << 16) | ((uint32_t)bytes[3] << 24);
  return 1;
}

static int write_u32(uint32_t value) {
  unsigned char bytes[4];
  bytes[0] = (unsigned char)value;
  bytes[1] = (unsigned char)(value >> 8);
  bytes[2] = (unsigned char)(value >> 16);
  bytes[3] = (unsigned char)(value >> 24);
  return fwrite(bytes, sizeof(bytes), 1, stdout) == 1;
}

static int read_f32(float *value) {
  uint32_t bits;
  if (!read_u32(&bits)) return 0;
  memcpy(value, &bits, sizeof(bits));
  return 1;
}

static int write_f32(float value) {
  uint32_t bits;
  memcpy(&bits, &value, sizeof(bits));
  return write_u32(bits);
}

int main(void) {
  unsigned char magic[4];
  uint32_t version;
  uint32_t count;
  static float x[MAX_LENGTH];
  static float y1[MAX_LENGTH];
  static float y2[MAX_LENGTH];

  if (fread(magic, sizeof(magic), 1, stdin) != 1 ||
      memcmp(magic, "GDPI", sizeof(magic)) != 0 ||
      !read_u32(&version) || version != 1 || !read_u32(&count) ||
      count == 0 || count > MAX_CASES) return 2;
  if (fwrite("GDPO", 4, 1, stdout) != 1 || !write_u32(1) || !write_u32(count)) return 3;

  for (uint32_t c = 0; c < count; c++) {
    uint32_t n;
    float xy1 = 0;
    float xy2 = 0;
    if (!read_u32(&n) || n == 0 || n > MAX_LENGTH) return 4;
    for (uint32_t i = 0; i < n; i++) {
      if (!read_f32(&x[i])) return 5;
    }
    for (uint32_t i = 0; i < n; i++) {
      if (!read_f32(&y1[i])) return 6;
    }
    for (uint32_t i = 0; i < n; i++) {
      if (!read_f32(&y2[i])) return 7;
    }
    dual_inner_prod_sse(x, y1, y2, (int)n, &xy1, &xy2);
    if (!write_f32(xy1) || !write_f32(xy2)) return 8;
  }

  if (fgetc(stdin) != EOF || ferror(stdin)) return 9;
  return fflush(stdout) == 0 ? 0 : 10;
}
