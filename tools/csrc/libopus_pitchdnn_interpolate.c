#include <stdint.h>
#include <stdio.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#ifdef HAVE_CONFIG_H
#include "config.h"
#endif

#define INPUT_MAGIC "GPWI"
#define OUTPUT_MAGIC "GPWO"

static int read_exact(void *dst, size_t size) {
  return fread(dst, 1, size, stdin) == size;
}

static int write_exact(const void *src, size_t size) {
  return fwrite(src, 1, size, stdout) == size;
}

static int read_u32(uint32_t *value) {
  unsigned char bytes[4];
  if (!read_exact(bytes, sizeof(bytes))) return 0;
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
  return write_exact(bytes, sizeof(bytes));
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

/* dnn/pitchdnn.c:compute_pitchdnn uses this exact return expression. */
static float interpolate(float sum, float count) {
  return (1.f/60.f)*(sum/count) - 1.5;
}

int main(void) {
  char magic[4];
  uint32_t version;
  uint32_t count;
  uint32_t i;
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 1;
#endif
  if (!read_exact(magic, sizeof(magic)) ||
      magic[0] != 'G' || magic[1] != 'P' || magic[2] != 'W' || magic[3] != 'I' ||
      !read_u32(&version) || version != 1 ||
      !read_u32(&count) || count == 0 || count > 64) return 1;
  if (!write_exact(OUTPUT_MAGIC, 4) ||
      !write_u32(version) || !write_u32(count)) return 1;
  for (i = 0; i < count; i++) {
    float sum;
    float weight;
    float value;
    if (!read_f32(&sum) || !read_f32(&weight)) return 1;
    value = interpolate(sum, weight);
    if (!write_f32(value)) return 1;
  }
  return 0;
}
