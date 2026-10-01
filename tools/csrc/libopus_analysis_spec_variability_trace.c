/* Bounded capture of the LogE matrix consumed by analysisSpecVariability. */
#include <stdint.h>
#include <stdio.h>
#include <string.h>

#ifndef GOPUS_ANALYSIS_SOURCE_SHA256
#error "GOPUS_ANALYSIS_SOURCE_SHA256 must identify selected src/analysis.c"
#endif
#ifndef GOPUS_GANI_DRIVER_SHA256
#error "GOPUS_GANI_DRIVER_SHA256 must identify the pinned GANI driver"
#endif

enum { SPEC_ROWS = 8, SPEC_COLS = 18, SPEC_VALUES = SPEC_ROWS * SPEC_COLS };

static float captured_loge[SPEC_VALUES];
static uint32_t capture_calls;
static uint32_t overflow;

void gopus_analysis_spec_variability_capture(const float *loge) {
  if (capture_calls++ != 0 || loge == NULL) {
    overflow = 1;
    return;
  }
  memcpy(captured_loge, loge, sizeof(captured_loge));
}

static int write_exact(const void *data, size_t size) {
  return fwrite(data, 1, size, stdout) == size;
}

static int write_u32(uint32_t value) {
  const unsigned char bytes[4] = {
      (unsigned char)value,
      (unsigned char)(value >> 8),
      (unsigned char)(value >> 16),
      (unsigned char)(value >> 24),
  };
  return write_exact(bytes, sizeof(bytes));
}

static int write_float32s(const float *values, size_t count) {
  size_t i;
  for (i = 0; i < count; i++) {
    uint32_t bits;
    memcpy(&bits, &values[i], sizeof(bits));
    if (!write_u32(bits)) return 0;
  }
  return 1;
}

int gopus_analysis_spec_variability_trace_write(void) {
  static const char magic[4] = {'G', 'V', 'A', 'R'};
  if (!write_exact(magic, sizeof(magic)) || !write_u32(1) || !write_u32(0) ||
      !write_u32(capture_calls) || !write_u32(overflow) ||
      !write_u32(SPEC_ROWS) || !write_u32(SPEC_COLS) ||
      !write_exact(GOPUS_ANALYSIS_SOURCE_SHA256, 64) ||
      !write_exact(GOPUS_GANI_DRIVER_SHA256, 64) ||
      !write_float32s(captured_loge, SPEC_VALUES)) {
    return 0;
  }
  return 1;
}
