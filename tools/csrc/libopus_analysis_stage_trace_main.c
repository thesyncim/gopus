/* Append GAST to the unchanged GANO output produced by the GANI oracle. */

#define main gopus_analysis_info_main
#include "libopus_analysis_info.c"
#undef main

int gopus_analysis_stage_trace_write(void);

int main(void) {
  int status = gopus_analysis_info_main();
  if (status != 0) return status;
  return gopus_analysis_stage_trace_write() ? 0 : 5;
}
