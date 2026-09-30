#include <stdint.h>
#include <stdio.h>
#include <string.h>

#include "config.h"
#include "celt/celt_lpc.h"

#define MAX_LPC_ORDER 24u
#define MAX_CASES 64u

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

static int read_float(float *value) {
  uint32_t bits;
  if (!read_u32(&bits)) return 0;
  memcpy(value, &bits, sizeof(bits));
  return 1;
}

static int write_float(float value) {
  uint32_t bits;
  memcpy(&bits, &value, sizeof(bits));
  return write_u32(bits);
}

int main(void) {
  unsigned char magic[4];
  uint32_t version;
  uint32_t count;
  uint32_t case_index;

  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, "GCLK", 4) != 0 ||
      !read_u32(&version) || version != 1 || !read_u32(&count) ||
      count == 0 || count > MAX_CASES) {
    return 2;
  }

  if (!write_exact("GCKO", 4) || !write_u32(1) || !write_u32(count)) return 3;
  for (case_index = 0; case_index < count; ++case_index) {
    uint32_t order;
    uint32_t i;
    opus_val32 ac[MAX_LPC_ORDER + 1];
    opus_val16 lpc[MAX_LPC_ORDER];

    if (!read_u32(&order) || order == 0 || order > MAX_LPC_ORDER) return 4;
    for (i = 0; i <= order; ++i) {
      if (!read_float((float *)&ac[i])) return 5;
    }
    _celt_lpc(lpc, ac, (int)order);
    if (!write_u32(order)) return 6;
    for (i = 0; i < order; ++i) {
      if (!write_float((float)lpc[i])) return 7;
    }
  }

  if (fgetc(stdin) != EOF) return 8;
  return 0;
}
