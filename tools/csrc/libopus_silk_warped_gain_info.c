/* Direct oracle for the pinned SILK warped_gain() static helper. */

#include <math.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

/* Compile warped_gain() from the selected pinned translation unit. Rename its
 * public encoder entry point so the linked archive supplies encode dependencies. */
#define silk_noise_shape_analysis_FLP gopus_oracle_unused_noise_shape_analysis_FLP
#include "noise_shape_analysis_FLP.c"
#undef silk_noise_shape_analysis_FLP

#define INPUT_MAGIC "GSWI"
#define OUTPUT_MAGIC "GSWO"
#define MAX_CASES 4096u
#define MAX_ORDER 24u

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

static int read_float(float *out) {
  uint32_t bits;
  if (!read_u32(&bits)) return 0;
  memcpy(out, &bits, sizeof(bits));
  return 1;
}

static int write_float(float value) {
  uint32_t bits;
  memcpy(&bits, &value, sizeof(bits));
  return write_u32(bits);
}

static int run_case(void) {
  uint32_t raw_order;
  opus_int order;
  silk_float lambda;
  silk_float coefs[MAX_ORDER];
  silk_float result;
  int i;

  if (!read_u32(&raw_order) || raw_order == 0 || raw_order > MAX_ORDER) return 0;
  order = (opus_int)raw_order;
  if (!read_float(&lambda) || !isfinite(lambda) || lambda < 0.0f || lambda > 0.51f) return 0;
  for (i = 0; i < order; i++) {
    if (!read_float(&coefs[i]) || !isfinite(coefs[i])) return 0;
  }

  result = warped_gain(coefs, lambda, order);
  return write_u32(raw_order) && write_float(result);
}

int main(void) {
  char magic[4];
  uint32_t version;
  uint32_t count;
  uint32_t i;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, sizeof(magic)) ||
      memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0 ||
      !read_u32(&version) || version != 1 ||
      !read_u32(&count) || count == 0 || count > MAX_CASES) return 1;
  if (!write_exact(OUTPUT_MAGIC, sizeof(magic)) ||
      !write_u32(1) || !write_u32(count)) return 1;
  for (i = 0; i < count; i++) {
    if (!run_case()) return 1;
  }
  return 0;
}
