#include <stdint.h>
#include <stdio.h>
#include <string.h>

#include "analysis.h"
#include "libopus_analysis_band_slope_trace.h"

#ifndef GOPUS_ANALYSIS_BAND_SLOPE_TRACE_FRAME
#error "GOPUS_ANALYSIS_BAND_SLOPE_TRACE_FRAME must select one analysis frame"
#endif

#ifndef GOPUS_ANALYSIS_BAND_SLOPE_DRIVER_SHA256
#error "GOPUS_ANALYSIS_BAND_SLOPE_DRIVER_SHA256 must bind the copied GANI driver"
#endif

#define GOPUS_ANALYSIS_BAND_SLOPE_SOURCE_SHA256_LENGTH 64u

static struct {
  uint32_t calls;
  uint32_t stored;
  uint32_t overflow;
  uint32_t frame;
  uint32_t analysis_count;
  uint32_t write_pos;
  uint32_t latest_index;
  uint32_t valid;
  float prev_band_tonality[NB_TBANDS];
  float latest_slope;
} gopus_band_slope_trace;

static int gopus_band_slope_write_exact(const void *data, size_t size) {
  return fwrite(data, 1, size, stdout) == size;
}

static int gopus_band_slope_write_u32(uint32_t value) {
  unsigned char bytes[4];
  bytes[0] = (unsigned char)value;
  bytes[1] = (unsigned char)(value >> 8);
  bytes[2] = (unsigned char)(value >> 16);
  bytes[3] = (unsigned char)(value >> 24);
  return gopus_band_slope_write_exact(bytes, sizeof(bytes));
}

static int gopus_band_slope_write_f32(float value) {
  uint32_t bits;
  memcpy(&bits, &value, sizeof(bits));
  return gopus_band_slope_write_u32(bits);
}

void gopus_analysis_band_slope_trace_capture(uint32_t frame,
                                             uint32_t analysis_count,
                                             uint32_t write_pos,
                                             uint32_t latest_index,
                                             uint32_t valid,
                                             const float *prev_band_tonality,
                                             float latest_slope) {
  uint32_t i;
  gopus_band_slope_trace.calls++;
  if (frame != GOPUS_ANALYSIS_BAND_SLOPE_TRACE_FRAME ||
      gopus_band_slope_trace.stored != 0 || prev_band_tonality == NULL) {
    gopus_band_slope_trace.overflow = 1;
    return;
  }
  gopus_band_slope_trace.frame = frame;
  gopus_band_slope_trace.analysis_count = analysis_count;
  gopus_band_slope_trace.write_pos = write_pos;
  gopus_band_slope_trace.latest_index = latest_index;
  gopus_band_slope_trace.valid = valid;
  for (i = 0; i < NB_TBANDS; i++)
    gopus_band_slope_trace.prev_band_tonality[i] = prev_band_tonality[i];
  gopus_band_slope_trace.latest_slope = latest_slope;
  gopus_band_slope_trace.stored++;
}

int gopus_analysis_band_slope_trace_write(void) {
  uint32_t i;
  static const char driver_hash[] = GOPUS_ANALYSIS_BAND_SLOPE_DRIVER_SHA256;
  size_t driver_hash_length = sizeof(driver_hash) - 1;

  if (driver_hash_length != GOPUS_ANALYSIS_BAND_SLOPE_SOURCE_SHA256_LENGTH ||
      gopus_band_slope_trace.calls != 1 || gopus_band_slope_trace.stored != 1 ||
      gopus_band_slope_trace.overflow != 0)
    gopus_band_slope_trace.overflow = 1;

  if (!gopus_band_slope_write_exact("GABS", 4) ||
      !gopus_band_slope_write_u32(2) ||
      !gopus_band_slope_write_u32(gopus_band_slope_trace.frame) ||
      !gopus_band_slope_write_u32(gopus_band_slope_trace.analysis_count) ||
      !gopus_band_slope_write_u32(gopus_band_slope_trace.write_pos) ||
      !gopus_band_slope_write_u32(gopus_band_slope_trace.latest_index) ||
      !gopus_band_slope_write_u32(gopus_band_slope_trace.valid) ||
      !gopus_band_slope_write_u32(gopus_band_slope_trace.calls) ||
      !gopus_band_slope_write_u32(gopus_band_slope_trace.stored) ||
      !gopus_band_slope_write_u32(gopus_band_slope_trace.overflow) ||
      !gopus_band_slope_write_u32(NB_TBANDS) ||
      !gopus_band_slope_write_u32((uint32_t)driver_hash_length) ||
      !gopus_band_slope_write_exact(driver_hash, driver_hash_length))
    return 0;

  for (i = 0; i < NB_TBANDS; i++) {
    if (!gopus_band_slope_write_u32(i) ||
        !gopus_band_slope_write_f32(gopus_band_slope_trace.prev_band_tonality[i]))
      return 0;
  }
  if (!gopus_band_slope_write_f32(gopus_band_slope_trace.latest_slope)) return 0;
  return fflush(stdout) == 0;
}
