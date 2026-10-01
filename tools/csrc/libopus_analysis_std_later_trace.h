#ifndef GOPUS_LIBOPUS_ANALYSIS_STD_LATER_TRACE_H
#define GOPUS_LIBOPUS_ANALYSIS_STD_LATER_TRACE_H

#include <stdint.h>

void gopus_analysis_std_later_run_begin(void);
void gopus_analysis_std_later_chunk_begin(int32_t count, int32_t chunk);
void gopus_analysis_std_later_mean_begin(int32_t count, float alpha,
                                         const float *cmean, const float *bfcc);
void gopus_analysis_std_later_mean_finish(const float *cmean);
void gopus_analysis_std_later_before_std(const float *std, const float *features,
                                         const float *mem);
void gopus_analysis_std_later_finish(const float *std);
int gopus_analysis_std_later_trace_write(void);

#endif
