#include <stdint.h>
#include <stdio.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#define INPUT_MAGIC "GSGI"
#define OUTPUT_MAGIC "GSGO"
#define MAX_CASES 4096u

static int read_exact(void *dst, size_t size) {
  return fread(dst, 1, size, stdin) == size;
}

static int write_exact(const void *src, size_t size) {
  return fwrite(src, 1, size, stdout) == size;
}

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact(b, sizeof(b))) return 0;
  *out = (uint32_t)b[0] |
         ((uint32_t)b[1] << 8) |
         ((uint32_t)b[2] << 16) |
         ((uint32_t)b[3] << 24);
  return 1;
}

static int write_u32(uint32_t value) {
  unsigned char b[4];
  b[0] = (unsigned char)(value & 0xffu);
  b[1] = (unsigned char)((value >> 8) & 0xffu);
  b[2] = (unsigned char)((value >> 16) & 0xffu);
  b[3] = (unsigned char)((value >> 24) & 0xffu);
  return write_exact(b, sizeof(b));
}

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

/* Compile the source-shaped statements from silk/float/noise_shape_analysis_FLP.c. */
static float silk_gain_tweak_source_expression(float gain, float gain_mult, float gain_add) {
  gain *= gain_mult;
  gain += gain_add;
  return gain;
}

int main(void) {
  char magic[4];
  uint32_t version;
  uint32_t mode;
  uint32_t count;
  uint32_t i;
  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, 4) != 0 ||
      !read_u32(&version) || version != 1 || !read_u32(&mode) || mode != 0 ||
      !read_u32(&count) || count == 0 || count > MAX_CASES) return 1;
  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(count)) return 1;
  for (i = 0; i < count; i++) {
    uint32_t raw_gain, raw_mult, raw_add, raw_result;
    float gain, gain_mult, gain_add, result;
    if (!read_u32(&raw_gain) || !read_u32(&raw_mult) || !read_u32(&raw_add)) return 1;
    memcpy(&gain, &raw_gain, sizeof(gain));
    memcpy(&gain_mult, &raw_mult, sizeof(gain_mult));
    memcpy(&gain_add, &raw_add, sizeof(gain_add));
    result = silk_gain_tweak_source_expression(gain, gain_mult, gain_add);
    memcpy(&raw_result, &result, sizeof(raw_result));
    if (!write_u32(raw_result)) return 1;
  }
  return 0;
}
