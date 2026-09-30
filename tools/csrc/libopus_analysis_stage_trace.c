/*
 * Bounded, source-bound GAST v1 capture for the selected float run_analysis
 * call. The trace stores the real downmix/resampler output, windowed FFT input,
 * FFT output, and post-call resampler state.
 */

#include <stdint.h>
#include <stdio.h>
#include <string.h>

#include "libopus_analysis_stage_trace.h"

#ifdef HAVE_CONFIG_H
#include "config.h"
#endif

#ifdef FIXED_POINT
#error "GAST stage capture requires the float libopus build"
#endif

#ifndef GOPUS_ANALYSIS_STAGE_TRACE_FRAME
#error "GOPUS_ANALYSIS_STAGE_TRACE_FRAME must select one analysis frame"
#endif

#ifndef GOPUS_ANALYSIS_STAGE_SOURCE_SHA256
#error "GOPUS_ANALYSIS_STAGE_SOURCE_SHA256 must bind the copied analysis.c source"
#endif

#define GAST_RING_START 240u
#define GAST_RING_COUNT 480u
#define GAST_FFT_COMPLEX_COUNT 480u
#define GAST_FFT_WORD_COUNT (2u * GAST_FFT_COMPLEX_COUNT)
#define GAST_SOURCE_SHA256_LENGTH 64u

typedef struct {
  uint32_t analysis_frame_size;
  uint32_t frame_size;
  uint32_t run_fs;
  uint32_t run_c1;
  uint32_t run_c2;
  uint32_t run_channels;
  uint32_t run_lsb_depth;
  uint32_t tonality_fs;
  uint32_t tonality_len24;
  uint32_t tonality_offset24;
  uint32_t tonality_c1;
  uint32_t tonality_c2;
  uint32_t tonality_channels;
  uint32_t tonality_lsb_depth;
  uint32_t mem_fill;
  uint32_t inmem_start;
  uint32_t inmem_count;
  uint32_t fft_input_complex_count;
  uint32_t fft_output_complex_count;
  float inmem[GAST_RING_COUNT];
  float fft_input[GAST_FFT_WORD_COUNT];
  float fft_output[GAST_FFT_WORD_COUNT];
  float downmix_state[3];
  float hp_energy_accum;
} gast_record;

static struct {
  uint32_t run_calls;
  uint32_t selected_tonality_calls;
  uint32_t overflow;
  uint32_t stage_mask;
  gast_record record;
} gast;

static int gast_selected_run_active;

static void gast_mark_overflow(void) {
  gast.overflow = 1;
}

void gopus_analysis_stage_run_begin(int analysis_frame_size, int frame_size,
                                    int c1, int c2, int channels, int fs,
                                    int lsb_depth) {
  uint32_t frame = gast.run_calls;
  gast.run_calls++;
  gast_selected_run_active = frame == GOPUS_ANALYSIS_STAGE_TRACE_FRAME;
  if (!gast_selected_run_active) return;

  memset(&gast.record, 0, sizeof(gast.record));
  gast.stage_mask = 0;
  gast.record.analysis_frame_size = (uint32_t)analysis_frame_size;
  gast.record.frame_size = (uint32_t)frame_size;
  gast.record.run_fs = (uint32_t)fs;
  gast.record.run_c1 = (uint32_t)(int32_t)c1;
  gast.record.run_c2 = (uint32_t)(int32_t)c2;
  gast.record.run_channels = (uint32_t)channels;
  gast.record.run_lsb_depth = (uint32_t)lsb_depth;
}

void gopus_analysis_stage_tonality_begin(int fs, int len24, int offset24,
                                         int c1, int c2, int channels,
                                         int lsb_depth, int mem_fill) {
  if (!gast_selected_run_active) return;
  gast.selected_tonality_calls++;
  if (gast.selected_tonality_calls != 1) gast_mark_overflow();
  gast.record.tonality_fs = (uint32_t)fs;
  gast.record.tonality_len24 = (uint32_t)len24;
  gast.record.tonality_offset24 = (uint32_t)offset24;
  gast.record.tonality_c1 = (uint32_t)(int32_t)c1;
  gast.record.tonality_c2 = (uint32_t)(int32_t)c2;
  gast.record.tonality_channels = (uint32_t)channels;
  gast.record.tonality_lsb_depth = (uint32_t)lsb_depth;
  gast.record.mem_fill = (uint32_t)mem_fill;
}

void gopus_analysis_stage_capture_inmem(const float *values, int start,
                                        int count) {
  if (!gast_selected_run_active) return;
  if (values == NULL || start < 0 || count != (int)GAST_RING_COUNT ||
      start != (int)GAST_RING_START || (gast.stage_mask & 1u) != 0) {
    gast_mark_overflow();
    return;
  }
  gast.record.inmem_start = (uint32_t)start;
  gast.record.inmem_count = (uint32_t)count;
  memcpy(gast.record.inmem, values, sizeof(gast.record.inmem));
  gast.stage_mask |= 1u;
}

static void gast_capture_fft(const void *values, uint32_t complex_count,
                             float *destination, uint32_t stage_bit) {
  if (!gast_selected_run_active) return;
  if (values == NULL || complex_count != GAST_FFT_COMPLEX_COUNT ||
      (gast.stage_mask & stage_bit) != 0) {
    gast_mark_overflow();
    return;
  }
  memcpy(destination, values, GAST_FFT_WORD_COUNT * sizeof(float));
  if (stage_bit == 2u) gast.record.fft_input_complex_count = complex_count;
  if (stage_bit == 4u) gast.record.fft_output_complex_count = complex_count;
  gast.stage_mask |= stage_bit;
}

void gopus_analysis_stage_capture_fft_input(const void *values,
                                            uint32_t complex_count) {
  gast_capture_fft(values, complex_count, gast.record.fft_input, 2u);
}

void gopus_analysis_stage_capture_fft_output(const void *values,
                                             uint32_t complex_count) {
  gast_capture_fft(values, complex_count, gast.record.fft_output, 4u);
}

void gopus_analysis_stage_capture_post_run(const float *downmix_state,
                                          float hp_energy_accum) {
  int i;
  if (!gast_selected_run_active) return;
  if (downmix_state == NULL || (gast.stage_mask & 8u) != 0) {
    gast_mark_overflow();
    return;
  }
  for (i = 0; i < 3; i++) gast.record.downmix_state[i] = downmix_state[i];
  gast.record.hp_energy_accum = hp_energy_accum;
  gast.stage_mask |= 8u;
  gast_selected_run_active = 0;
}

static int gast_write_exact(const void *data, size_t size) {
  return fwrite(data, 1, size, stdout) == size;
}

static int gast_write_u32(uint32_t value) {
  unsigned char bytes[4];
  bytes[0] = (unsigned char)value;
  bytes[1] = (unsigned char)(value >> 8);
  bytes[2] = (unsigned char)(value >> 16);
  bytes[3] = (unsigned char)(value >> 24);
  return gast_write_exact(bytes, sizeof(bytes));
}

static int gast_write_f32(float value) {
  uint32_t bits;
  memcpy(&bits, &value, sizeof(bits));
  return gast_write_u32(bits);
}

static int gast_write_f32_array(const float *values, uint32_t count) {
  uint32_t i;
  for (i = 0; i < count; i++)
    if (!gast_write_f32(values[i])) return 0;
  return 1;
}

int gopus_analysis_stage_trace_write(void) {
  const gast_record *r = &gast.record;
  static const char source_hash[] = GOPUS_ANALYSIS_STAGE_SOURCE_SHA256;
  size_t source_hash_length = sizeof(source_hash) - 1;

  if (source_hash_length != GAST_SOURCE_SHA256_LENGTH ||
      gast.run_calls <= GOPUS_ANALYSIS_STAGE_TRACE_FRAME ||
      gast.selected_tonality_calls != 1 || gast.stage_mask != 15u)
    gast_mark_overflow();

  if (!gast_write_exact("GAST", 4) || !gast_write_u32(1) ||
      !gast_write_u32(GOPUS_ANALYSIS_STAGE_TRACE_FRAME) ||
      !gast_write_u32(gast.run_calls) ||
      !gast_write_u32(gast.selected_tonality_calls) ||
      !gast_write_u32(gast.overflow) || !gast_write_u32(gast.stage_mask) ||
      !gast_write_u32((uint32_t)source_hash_length) ||
      !gast_write_exact(source_hash, source_hash_length) ||
      !gast_write_u32(r->analysis_frame_size) || !gast_write_u32(r->frame_size) ||
      !gast_write_u32(r->run_fs) || !gast_write_u32(r->run_c1) ||
      !gast_write_u32(r->run_c2) || !gast_write_u32(r->run_channels) ||
      !gast_write_u32(r->run_lsb_depth) || !gast_write_u32(r->tonality_fs) ||
      !gast_write_u32(r->tonality_len24) || !gast_write_u32(r->tonality_offset24) ||
      !gast_write_u32(r->tonality_c1) || !gast_write_u32(r->tonality_c2) ||
      !gast_write_u32(r->tonality_channels) || !gast_write_u32(r->tonality_lsb_depth) ||
      !gast_write_u32(r->mem_fill) || !gast_write_u32(r->inmem_start) ||
      !gast_write_u32(r->inmem_count) ||
      !gast_write_u32(r->fft_input_complex_count) ||
      !gast_write_u32(r->fft_output_complex_count) ||
      !gast_write_u32(3) || !gast_write_u32(1) ||
      !gast_write_f32_array(r->inmem, GAST_RING_COUNT) ||
      !gast_write_f32_array(r->fft_input, GAST_FFT_WORD_COUNT) ||
      !gast_write_f32_array(r->fft_output, GAST_FFT_WORD_COUNT) ||
      !gast_write_f32_array(r->downmix_state, 3) ||
      !gast_write_f32(r->hp_energy_accum))
    return 0;
  return fflush(stdout) == 0;
}
