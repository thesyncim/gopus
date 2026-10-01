#include "libopus_analysis_mean_std_trace.h"

#include <stdio.h>
#include <string.h>

#ifndef GOPUS_ANALYSIS_MEAN_STD_SOURCE_SHA256
#error "GOPUS_ANALYSIS_MEAN_STD_SOURCE_SHA256 must identify instrumented analysis.c"
#endif

#define GOPUS_ANALYSIS_MEAN_STD_MAX_ROWS 6

typedef struct {
  int32_t frame;
  int32_t chunk;
  int32_t count;
  uint32_t flags;
  float alpha;
  float cmean_old[4];
  float bfcc[4];
  float mem0[4];
  float mem8[4];
  float mem16[4];
  float mem24[4];
  float feature[9];
  float cmean_new[4];
  float std_old[9];
  float std_new[9];
} gopus_analysis_mean_std_row;

static gopus_analysis_mean_std_row rows[GOPUS_ANALYSIS_MEAN_STD_MAX_ROWS];
static uint32_t row_count;
static uint32_t overflow;
static int32_t current_frame = -1;
static int32_t current_chunk = -1;
static int32_t current_count = -1;
static int32_t active_row = -1;

static uint32_t float_bits(float value) {
  uint32_t bits;
  memcpy(&bits, &value, sizeof(bits));
  return bits;
}

static int put_bytes(const void *data, size_t size) {
  return fwrite(data, 1, size, stdout) == size;
}

static int put_u32(uint32_t value) {
  return put_bytes(&value, sizeof(value));
}

static int put_float(float value) {
  return put_u32(float_bits(value));
}

static int32_t selected_count(int32_t count) {
  return count >= 0 && count <= 5;
}

void gopus_analysis_mean_std_run_begin(void) {
  current_frame++;
  current_chunk = -1;
  current_count = -1;
  active_row = -1;
}

void gopus_analysis_mean_std_chunk_begin(int32_t count, int32_t chunk) {
  current_count = count;
  current_chunk = chunk;
  active_row = -1;
}

void gopus_analysis_mean_std_begin(int32_t count, float alpha,
                                   const float *cmean, const float *bfcc) {
  gopus_analysis_mean_std_row *row;
  int i;
  if (!selected_count(count)) return;
  if (count != current_count || current_frame < 0 || current_chunk < 0 ||
      row_count >= GOPUS_ANALYSIS_MEAN_STD_MAX_ROWS) {
    overflow |= 1u;
    return;
  }
  for (i = 0; i < (int)row_count; i++) {
    if (rows[i].count == count) {
      overflow |= 2u;
      return;
    }
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

void gopus_analysis_mean_std_before_std(const float *std, const float *features,
                                        const float *mem) {
  gopus_analysis_mean_std_row *row;
  if (active_row < 0) return;
  row = &rows[active_row];
  if (row->count != 5 || (row->flags & 2u) != 0) {
    overflow |= 4u;
    return;
  }
  memcpy(row->std_old, std, sizeof(row->std_old));
  memcpy(row->feature, features, sizeof(row->feature));
  memcpy(row->mem0, mem, sizeof(row->mem0));
  memcpy(row->mem8, mem + 8, sizeof(row->mem8));
  memcpy(row->mem16, mem + 16, sizeof(row->mem16));
  memcpy(row->mem24, mem + 24, sizeof(row->mem24));
  row->flags |= 2u;
}

void gopus_analysis_mean_std_finish(const float *cmean, const float *std) {
  gopus_analysis_mean_std_row *row;
  if (active_row < 0) return;
  row = &rows[active_row];
  if ((row->flags & 1u) == 0 ||
      (row->count == 5 && (row->flags & 2u) == 0)) {
    overflow |= 8u;
    active_row = -1;
    return;
  }
  memcpy(row->cmean_new, cmean, sizeof(row->cmean_new));
  if (row->count == 5) memcpy(row->std_new, std, sizeof(row->std_new));
  row->flags |= 4u;
  active_row = -1;
}

int gopus_analysis_mean_std_trace_write(void) {
  static const char magic[4] = {'G', 'M', 'S', 'D'};
  static const char source_hash[] = GOPUS_ANALYSIS_MEAN_STD_SOURCE_SHA256;
  uint32_t i, j;
  if (sizeof(source_hash) - 1 != 64 ||
      !put_bytes(magic, sizeof(magic)) || !put_u32(2u) ||
      !put_u32(row_count) || !put_u32(overflow) ||
      !put_u32((uint32_t)(sizeof(source_hash) - 1)) ||
      !put_bytes(source_hash, sizeof(source_hash) - 1)) return 0;
  for (i = 0; i < row_count; i++) {
    const gopus_analysis_mean_std_row *row = &rows[i];
    if (!put_u32((uint32_t)row->frame) || !put_u32((uint32_t)row->chunk) ||
        !put_u32((uint32_t)row->count) || !put_u32(row->flags) ||
        !put_float(row->alpha)) return 0;
    for (j = 0; j < 4; j++) if (!put_float(row->cmean_old[j])) return 0;
    for (j = 0; j < 4; j++) if (!put_float(row->bfcc[j])) return 0;
    for (j = 0; j < 4; j++) if (!put_float(row->mem0[j])) return 0;
    for (j = 0; j < 4; j++) if (!put_float(row->mem8[j])) return 0;
    for (j = 0; j < 4; j++) if (!put_float(row->mem16[j])) return 0;
    for (j = 0; j < 4; j++) if (!put_float(row->mem24[j])) return 0;
    for (j = 0; j < 9; j++) if (!put_float(row->feature[j])) return 0;
    for (j = 0; j < 4; j++) if (!put_float(row->cmean_new[j])) return 0;
    for (j = 0; j < 9; j++) if (!put_float(row->std_old[j])) return 0;
    for (j = 0; j < 9; j++) if (!put_float(row->std_new[j])) return 0;
  }
  return fflush(stdout) == 0;
}
