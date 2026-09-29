/* Exact linked-archive oracle for the floating-point SILK k2a step-up kernel. */

#include <stdint.h>
#include <stdio.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "SigProc_FLP.h"

#define INPUT_MAGIC "GSKI"
#define OUTPUT_MAGIC "GSKO"
#define MAX_CASES 4096u

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

static int read_float(silk_float *out) {
  uint32_t bits;
  if (!read_u32(&bits)) return 0;
  memcpy(out, &bits, sizeof(bits));
  return 1;
}

static int write_float(silk_float value) {
  uint32_t bits;
  memcpy(&bits, &value, sizeof(bits));
  return write_u32(bits);
}

static int valid_order(uint32_t order) {
  return order == 12 || order == 14 || order == 16 ||
      order == 20 || order == 24;
}

static int run_case(void) {
  uint32_t raw_order;
  opus_int32 order;
  silk_float rc[MAX_SHAPE_LPC_ORDER] = {0};
  silk_float a[MAX_SHAPE_LPC_ORDER] = {0};
  int i;

  if (!read_u32(&raw_order) || !valid_order(raw_order)) return 0;
  order = (opus_int32)raw_order;
  for (i = 0; i < order; i++) {
    if (!read_float(&rc[i])) return 0;
    if (!(rc[i] > -1.0f && rc[i] < 1.0f)) return 0;
  }

  silk_k2a_FLP(a, rc, order);

  if (!write_u32(raw_order)) return 0;
  for (i = 0; i < order; i++) {
    if (!write_float(a[i])) return 0;
  }
  return 1;
}

int main(void) {
  char magic[4];
  uint32_t version;
  uint32_t count;
  uint32_t i;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, sizeof(magic)) ||
      memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) return 1;
  if (!read_u32(&version) || version != 1 ||
      !read_u32(&count) || count == 0 || count > MAX_CASES) return 1;
  if (!write_exact(OUTPUT_MAGIC, sizeof(magic)) ||
      !write_u32(1) || !write_u32(count)) return 1;
  for (i = 0; i < count; i++) {
    if (!run_case()) return 1;
  }
  return 0;
}
