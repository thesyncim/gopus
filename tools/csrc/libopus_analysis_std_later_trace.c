#include "libopus_analysis_std_later_trace.h"

#include <stdio.h>
#include <string.h>

#ifndef GOPUS_ANALYSIS_STD_LATER_SOURCE_SHA256
#error "GOPUS_ANALYSIS_STD_LATER_SOURCE_SHA256 must identify instrumented analysis.c"
#endif

#define GOPUS_ANALYSIS_STD_LATER_MAX_ROWS 64
#define GOPUS_ANALYSIS_STD_LATER_MAX_FRAME 35

typedef struct {
  int32_t frame;
  int32_t chunk;
  int32_t count;
  uint32_t flags;
  float alpha;
  float cmean_old[4];
  float bfcc[4];
  float cmean_new[4];
  float feature[11];
  float std_old[9];
  float std_new[9];
  float mem[32];
} gopus_analysis_std_later_row;

static gopus_analysis_std_later_row rows[GOPUS_ANALYSIS_STD_LATER_MAX_ROWS];
static uint32_t row_count;
static uint32_t overflow;
static int32_t current_frame = -1;
static int32_t current_chunk = -1;
static int32_t active_row = -1;

static int put_bytes(const void *data, size_t size) {
  return fwrite(data, 1, size, stdout) == size;
}

static int put_u32(uint32_t value) {
  return put_bytes(&value, sizeof(value));
}

static int put_float(float value) {
  uint32_t bits;
  memcpy(&bits, &value, sizeof(bits));
  return put_u32(bits);
}

void gopus_analysis_std_later_run_begin(void) {
  current_frame++;
  current_chunk = -1;
  active_row = -1;
}

void gopus_analysis_std_later_chunk_begin(int32_t count, int32_t chunk) {
  (void)count;
  current_chunk = chunk;
  active_row = -1;
}

void gopus_analysis_std_later_mean_begin(int32_t count, float alpha,
                                         const float *cmean, const float *bfcc) {
  gopus_analysis_std_later_row *row;
  if (current_frame < 0 || current_frame > GOPUS_ANALYSIS_STD_LATER_MAX_FRAME || count < 5) return;
  if (current_chunk < 0 || row_count >= GOPUS_ANALYSIS_STD_LATER_MAX_ROWS) {
    overflow |= 1u;
    return;
  }
  active_row = (int32_t)row_count++;
  row = &rows[active_row];
  memset(row, 0, sizeof(*row));
  row->frame = current_frame;
  row->chunk = current_chunk;
  row->count = count;
  row->flags = 1u;
  row->alpha = alpha;
  memcpy(row->cmean_old, cmean, sizeof(row->cmean_old));
  memcpy(row->bfcc, bfcc, sizeof(row->bfcc));
}

void gopus_analysis_std_later_mean_finish(const float *cmean) {
  gopus_analysis_std_later_row *row;
  if (active_row < 0) return;
  row = &rows[active_row];
  memcpy(row->cmean_new, cmean, sizeof(row->cmean_new));
  row->flags |= 2u;
}

void gopus_analysis_std_later_before_std(const float *std, const float *features,
                                         const float *mem) {
  gopus_analysis_std_later_row *row;
  if (active_row < 0) return;
  row = &rows[active_row];
  memcpy(row->std_old, std, sizeof(row->std_old));
  memcpy(row->feature, features, sizeof(row->feature));
  memcpy(row->mem, mem, sizeof(row->mem));
  row->flags |= 4u;
}

void gopus_analysis_std_later_finish(const float *std) {
  gopus_analysis_std_later_row *row;
  if (active_row < 0) return;
  row = &rows[active_row];
  memcpy(row->std_new, std, sizeof(row->std_new));
  row->flags |= 8u;
  if (row->flags != 15u) overflow |= 2u;
  active_row = -1;
}

int gopus_analysis_std_later_trace_write(void) {
  static const char magic[4] = {'G', 'M', 'S', 'R'};
  static const char source_hash[] = GOPUS_ANALYSIS_STD_LATER_SOURCE_SHA256;
  uint32_t i, j;
  if (sizeof(source_hash) - 1 != 64 || !put_bytes(magic, sizeof(magic)) ||
      !put_u32(1u) || !put_u32(row_count) || !put_u32(overflow) ||
      !put_u32((uint32_t)(sizeof(source_hash) - 1)) ||
      !put_bytes(source_hash, sizeof(source_hash) - 1)) return 0;
  for (i = 0; i < row_count; i++) {
    const gopus_analysis_std_later_row *row = &rows[i];
    if (!put_u32((uint32_t)row->frame) || !put_u32((uint32_t)row->chunk) ||
        !put_u32((uint32_t)row->count) || !put_u32(row->flags) ||
        !put_float(row->alpha)) return 0;
    for (j = 0; j < 4; j++) if (!put_float(row->cmean_old[j])) return 0;
    for (j = 0; j < 4; j++) if (!put_float(row->bfcc[j])) return 0;
    for (j = 0; j < 4; j++) if (!put_float(row->cmean_new[j])) return 0;
    for (j = 0; j < 11; j++) if (!put_float(row->feature[j])) return 0;
    for (j = 0; j < 9; j++) if (!put_float(row->std_old[j])) return 0;
    for (j = 0; j < 9; j++) if (!put_float(row->std_new[j])) return 0;
    for (j = 0; j < 32; j++) if (!put_float(row->mem[j])) return 0;
  }
  return fflush(stdout) == 0;
}
