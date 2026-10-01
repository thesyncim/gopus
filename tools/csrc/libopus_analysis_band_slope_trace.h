#ifndef GOPUS_LIBOPUS_ANALYSIS_BAND_SLOPE_TRACE_H
#define GOPUS_LIBOPUS_ANALYSIS_BAND_SLOPE_TRACE_H

#include <stdint.h>

void gopus_analysis_band_slope_trace_capture(uint32_t frame,
                                             uint32_t analysis_count,
                                             uint32_t write_pos,
                                             uint32_t latest_index,
                                             uint32_t valid,
                                             const float *prev_band_tonality,
                                             float latest_slope);
int gopus_analysis_band_slope_trace_write(void);

#endif
