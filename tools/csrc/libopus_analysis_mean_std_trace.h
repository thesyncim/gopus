#ifndef GOPUS_LIBOPUS_ANALYSIS_MEAN_STD_TRACE_H
#define GOPUS_LIBOPUS_ANALYSIS_MEAN_STD_TRACE_H

#include <stdint.h>

void gopus_analysis_mean_std_run_begin(void);
void gopus_analysis_mean_std_chunk_begin(int32_t count, int32_t chunk);
void gopus_analysis_mean_std_begin(int32_t count, float alpha,
                                   const float *cmean, const float *bfcc);
void gopus_analysis_mean_std_before_std(const float *std, const float *features,
                                        const float *mem);
void gopus_analysis_mean_std_finish(const float *cmean, const float *std);
int gopus_analysis_mean_std_trace_write(void);

#endif
