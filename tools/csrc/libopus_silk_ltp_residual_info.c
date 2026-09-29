/* Bounded direct-call oracle for silk_LTP_analysis_filter_FLP. */

#include "config.h"
#include "main_FLP.h"

#include <math.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

#define INPUT_MAGIC "GSLT"
#define OUTPUT_MAGIC "GSLU"
#define PROTOCOL_VERSION 1u
#define MAX_CASES 8u
#define MAX_INPUT_FLOATS 4096u
#define MAX_SUBFRAME_LENGTH 384u
#define MAX_PRE_LENGTH 16u
#define MAX_OUTPUT_FLOATS 2048u

static int read_exact(void *dst, size_t n) {
  return fread(dst, 1, n, stdin) == n;
}

static int write_exact(const void *src, size_t n) {
  return fwrite(src, 1, n, stdout) == n;
}

static int read_u32(uint32_t *value) {
  unsigned char b[4];
  if (!read_exact(b, sizeof(b))) return 0;
  *value = (uint32_t)b[0] | ((uint32_t)b[1] << 8) |
      ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int read_i32(int32_t *value) {
  uint32_t bits;
  if (!read_u32(&bits)) return 0;
  memcpy(value, &bits, sizeof(bits));
  return 1;
}

static int read_f32(float *value) {
  uint32_t bits;
  if (!read_u32(&bits)) return 0;
  memcpy(value, &bits, sizeof(bits));
  return 1;
}

static int write_u32(uint32_t value) {
  const unsigned char b[4] = {
    (unsigned char)value,
    (unsigned char)(value >> 8),
    (unsigned char)(value >> 16),
    (unsigned char)(value >> 24)
  };
  return write_exact(b, sizeof(b));
}

static int write_i32(int32_t value) {
  uint32_t bits;
  memcpy(&bits, &value, sizeof(bits));
  return write_u32(bits);
}

static int write_f32(float value) {
  uint32_t bits;
  memcpy(&bits, &value, sizeof(bits));
  return write_u32(bits);
}

static int finite_f32(float value) {
  return isfinite(value) != 0;
}

static int validate_geometry(uint32_t frame_start, uint32_t subfr_length,
    uint32_t nb_subfr, uint32_t pre_length, uint32_t input_count,
    const opus_int pitch_lags[MAX_NB_SUBFR]) {
  uint64_t output_count;
  uint64_t x_start;
  uint32_t k, i, j;

  if (subfr_length == 0 || subfr_length > MAX_SUBFRAME_LENGTH ||
      nb_subfr == 0 || nb_subfr > MAX_NB_SUBFR ||
      pre_length > MAX_PRE_LENGTH || input_count == 0 ||
      input_count > MAX_INPUT_FLOATS || frame_start < pre_length ||
      frame_start > input_count) {
    return 0;
  }
  output_count = (uint64_t)nb_subfr *
      ((uint64_t)subfr_length + (uint64_t)pre_length);
  if (output_count == 0 || output_count > MAX_OUTPUT_FLOATS) return 0;
  x_start = (uint64_t)frame_start - (uint64_t)pre_length;
  for (k = 0; k < nb_subfr; k++) {
    if (pitch_lags[k] < 0 || pitch_lags[k] > MAX_INPUT_FLOATS) return 0;
    for (i = 0; i < subfr_length + pre_length; i++) {
      uint64_t x_index = x_start + (uint64_t)k * subfr_length + i;
      if (x_index >= input_count) return 0;
      for (j = 0; j < LTP_ORDER; j++) {
        int64_t lag_index = (int64_t)x_index - pitch_lags[k] + LTP_ORDER / 2 - j;
        if (lag_index < 0 || (uint64_t)lag_index >= input_count) return 0;
      }
    }
  }
  return 1;
}

static int write_case(const uint32_t metadata[6], uint32_t nb_subfr,
    const opus_int pitch_lags[MAX_NB_SUBFR],
    const silk_float inv_gains[MAX_NB_SUBFR],
    const silk_float taps[MAX_NB_SUBFR * LTP_ORDER],
    const silk_float *pitch_buffer, const silk_float *residual) {
  uint32_t k, i, j;
  uint32_t frame_start = metadata[0];
  uint32_t subfr_length = metadata[1];
  uint32_t pre_length = metadata[3];
  uint32_t output_count = metadata[5];
  uint32_t x_start = frame_start - pre_length;
  uint32_t output_base = 0;

  for (i = 0; i < 6; i++) {
    if (!write_u32(metadata[i])) return 0;
  }
  for (k = 0; k < nb_subfr; k++) {
    if (!write_i32((int32_t)pitch_lags[k]) || !write_f32(inv_gains[k])) return 0;
    for (j = 0; j < LTP_ORDER; j++) {
      if (!write_f32(taps[k * LTP_ORDER + j])) return 0;
    }
  }
  for (k = 0; k < nb_subfr; k++) {
    uint32_t output_length = subfr_length + pre_length;
    uint32_t x_base = x_start + k * subfr_length;
    for (i = 0; i < output_length; i++, output_base++) {
      uint32_t x_index = x_base + i;
      if (!write_f32(pitch_buffer[x_index])) return 0;
      for (j = 0; j < LTP_ORDER; j++) {
        int64_t lag_index = (int64_t)x_index - pitch_lags[k] + LTP_ORDER / 2 - j;
        if (!write_f32(pitch_buffer[lag_index])) return 0;
      }
      if (!write_f32(residual[output_base])) return 0;
    }
  }
  return output_base == output_count;
}

int main(void) {
  unsigned char magic[4];
  uint32_t version, count, case_index;
  if (sizeof(float) != 4 || !read_exact(magic, sizeof(magic)) ||
      memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0 ||
      !read_u32(&version) || version != PROTOCOL_VERSION ||
      !read_u32(&count) || count == 0 || count > MAX_CASES) {
    return 2;
  }
  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(PROTOCOL_VERSION) ||
      !write_u32(count)) {
    return 3;
  }

  for (case_index = 0; case_index < count; case_index++) {
    uint32_t metadata[6];
    uint32_t k, j, i;
    uint32_t nb_subfr;
    uint32_t input_count;
    uint64_t output_count;
    silk_float pitch_buffer[MAX_INPUT_FLOATS];
    silk_float taps[MAX_NB_SUBFR * LTP_ORDER] = {0};
    silk_float inv_gains[MAX_NB_SUBFR] = {0};
    silk_float residual[MAX_OUTPUT_FLOATS];
    opus_int pitch_lags[MAX_NB_SUBFR] = {0};
    if (!read_u32(&metadata[0]) || !read_u32(&metadata[1]) ||
        !read_u32(&metadata[2]) || !read_u32(&metadata[3]) ||
        !read_u32(&metadata[4])) {
      return 4;
    }
    nb_subfr = metadata[2];
    input_count = metadata[4];
    if (nb_subfr == 0 || nb_subfr > MAX_NB_SUBFR || input_count == 0 ||
        input_count > MAX_INPUT_FLOATS) {
      return 5;
    }
    for (i = 0; i < input_count; i++) {
      if (!read_f32(&pitch_buffer[i]) || !finite_f32(pitch_buffer[i])) return 6;
    }
    for (k = 0; k < nb_subfr; k++) {
      int32_t lag;
      if (!read_i32(&lag) || lag < 0 || !read_f32(&inv_gains[k]) ||
          !finite_f32(inv_gains[k]) || inv_gains[k] <= 0.0f) {
        return 7;
      }
      pitch_lags[k] = lag;
      for (j = 0; j < LTP_ORDER; j++) {
        if (!read_f32(&taps[k * LTP_ORDER + j]) ||
            !finite_f32(taps[k * LTP_ORDER + j])) {
          return 8;
        }
      }
    }
    if (!validate_geometry(metadata[0], metadata[1], nb_subfr,
        metadata[3], input_count, pitch_lags)) {
      return 9;
    }
    output_count = (uint64_t)nb_subfr *
        ((uint64_t)metadata[1] + (uint64_t)metadata[3]);
    metadata[5] = (uint32_t)output_count;

    silk_LTP_analysis_filter_FLP(
        residual,
        pitch_buffer + metadata[0] - metadata[3],
        taps,
        pitch_lags,
        inv_gains,
        (opus_int)metadata[1],
        (opus_int)nb_subfr,
        (opus_int)metadata[3]);

    if (!write_case(metadata, nb_subfr, pitch_lags, inv_gains,
        taps, pitch_buffer, residual)) {
      return 10;
    }
  }
  if (fgetc(stdin) != EOF || ferror(stdin)) return 12;
  return fflush(stdout) == 0 ? 0 : 11;
}
