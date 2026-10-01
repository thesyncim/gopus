#ifndef GOPUS_ANALYSIS_BAND_SLOPE_DRIVER_SOURCE
#error "GOPUS_ANALYSIS_BAND_SLOPE_DRIVER_SOURCE must name the source-copied GANI driver"
#endif

#define main gopus_analysis_info_main
#include GOPUS_ANALYSIS_BAND_SLOPE_DRIVER_SOURCE
#undef main

int gopus_analysis_band_slope_trace_write(void);

int main(void) {
  int status = gopus_analysis_info_main();
  if (status != 0) return status;
  return gopus_analysis_band_slope_trace_write() ? 0 : 5;
}
