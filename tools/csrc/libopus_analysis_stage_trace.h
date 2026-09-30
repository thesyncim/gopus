#ifndef GOPUS_LIBOPUS_ANALYSIS_STAGE_TRACE_H
#define GOPUS_LIBOPUS_ANALYSIS_STAGE_TRACE_H

#include <stdint.h>

/* These hooks observe the selected run_analysis call without changing it. */
void gopus_analysis_stage_run_begin(int analysis_frame_size, int frame_size,
                                    int c1, int c2, int channels, int fs,
                                    int lsb_depth);
void gopus_analysis_stage_tonality_begin(int fs, int len24, int offset24,
                                         int c1, int c2, int channels,
                                         int lsb_depth, int mem_fill);
/* values points to the first sample in the captured [start, start+count) span. */
void gopus_analysis_stage_capture_inmem(const float *values, int start,
                                        int count);
void gopus_analysis_stage_capture_fft_input(const void *values,
                                            uint32_t complex_count);
void gopus_analysis_stage_capture_fft_output(const void *values,
                                             uint32_t complex_count);
/* Captures the real analysis.c phase-loop operands and assigned angles. */
void gopus_analysis_stage_capture_phase(int bin, float x1r, float x1i,
                                        float x2r, float x2i, float angle,
                                        float angle2, float angle_state,
                                        float d_angle_state,
                                        float d2_angle_state);
void gopus_analysis_stage_capture_post_run(const float *downmix_state,
                                          float hp_energy_accum);

int gopus_analysis_stage_trace_write(void);

#endif
