#define main gopus_analysis_info_main
#include "libopus_analysis_info.c"
#undef main

#include "libopus_analysis_std_later_trace.h"

int main(void) {
  int status = gopus_analysis_info_main();
  if (status != 0) return status;
  return gopus_analysis_std_later_trace_write() ? 0 : 5;
}
