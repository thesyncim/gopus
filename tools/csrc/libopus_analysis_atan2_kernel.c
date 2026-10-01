/*
 * Bounded oracle for the pinned analysis.c fast_atan2f implementation.
 * The implementation itself comes from celt/mathops.h; this helper only
 * supplies a binary float32 interface for same-operand comparisons.
 */

#include <stdint.h>
#include <stdio.h>
#include <string.h>

#ifdef HAVE_CONFIG_H
#include "config.h"
#endif

#define ANALYSIS_C
#include "mathops.h"

#ifndef GOPUS_ANALYSIS_MATHOPS_SHA256
#error "GOPUS_ANALYSIS_MATHOPS_SHA256 must bind the selected mathops.h"
#endif

#define ANALYSIS_ATAN2_MAX_CASES 4096u

static int read_u32(uint32_t *value) {
  unsigned char bytes[4];
  if (fread(bytes, 1, sizeof(bytes), stdin) != sizeof(bytes)) return 0;
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
  return fwrite(bytes, 1, sizeof(bytes), stdout) == sizeof(bytes);
}

int main(void) {
  unsigned char magic[4];
  uint32_t version, count, i;
  if (fread(magic, 1, sizeof(magic), stdin) != sizeof(magic) ||
      memcmp(magic, "GATI", sizeof(magic)) != 0 ||
      !read_u32(&version) || !read_u32(&count) || version != 1u ||
      count == 0u || count > ANALYSIS_ATAN2_MAX_CASES)
    return 2;

  if (fwrite("GATO", 1, 4, stdout) != 4 || !write_u32(1u) ||
      !write_u32(count) ||
      fwrite(GOPUS_ANALYSIS_MATHOPS_SHA256, 1, 64, stdout) != 64)
    return 3;

  for (i = 0; i < count; i++) {
    uint32_t y_bits, x_bits, result_bits;
    float y, x, result;
    if (!read_u32(&y_bits) || !read_u32(&x_bits)) return 4;
    memcpy(&y, &y_bits, sizeof(y));
    memcpy(&x, &x_bits, sizeof(x));
    result = fast_atan2f(y, x);
    memcpy(&result_bits, &result, sizeof(result_bits));
    if (!write_u32(result_bits)) return 5;
  }
  return fflush(stdout) == 0 ? 0 : 6;
}
