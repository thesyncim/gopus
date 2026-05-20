#include <stdint.h>
#include <stdio.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "silk/main.h"

#define INPUT_MAGIC "GSSI"
#define OUTPUT_MAGIC "GSSO"
#define MAX_STEREO_SAMPLES 320

enum {
  MODE_STEREO_QUANT_PRED = 0,
  MODE_STEREO_FIND_PREDICTOR = 1,
  MODE_STEREO_LR_TO_MS = 2
};

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

static int read_exact(void *dst, size_t size) {
  return fread(dst, 1, size, stdin) == size;
}

static int write_exact(const void *src, size_t size) {
  return fwrite(src, 1, size, stdout) == size;
}

static int read_u32(uint32_t *out) {
  return read_exact(out, sizeof(*out));
}

static int write_u32(uint32_t value) {
  return write_exact(&value, sizeof(value));
}

static int read_i32(int32_t *out) {
  uint32_t raw;
  if (!read_u32(&raw)) return 0;
  *out = (int32_t)raw;
  return 1;
}

static int write_i32(int32_t value) {
  return write_exact(&value, sizeof(value));
}

static int write_stereo_record(int32_t first, int32_t second, const int32_t extra[6]) {
  int i;
  if (!write_i32(first) || !write_i32(second)) return 0;
  for (i = 0; i < 6; i++) {
    if (!write_i32(extra[i])) return 0;
  }
  return 1;
}

static int write_lr_to_ms_record(
    const stereo_enc_state *state,
    const opus_int8 ix[2][3],
    opus_int8 mid_only,
    const opus_int32 rates[2],
    const opus_int16 mid[MAX_STEREO_SAMPLES + 2],
    const opus_int16 side[MAX_STEREO_SAMPLES + 2],
    int frame_length
) {
  int i;
  int n;
  if (!write_i32((int32_t)mid_only)) return 0;
  if (!write_i32((int32_t)rates[0]) || !write_i32((int32_t)rates[1])) return 0;
  for (n = 0; n < 2; n++) {
    for (i = 0; i < 3; i++) {
      if (!write_i32((int32_t)ix[n][i])) return 0;
    }
  }
  if (!write_i32((int32_t)state->pred_prev_Q13[0]) || !write_i32((int32_t)state->pred_prev_Q13[1])) return 0;
  if (!write_i32((int32_t)state->sMid[0]) || !write_i32((int32_t)state->sMid[1])) return 0;
  if (!write_i32((int32_t)state->sSide[0]) || !write_i32((int32_t)state->sSide[1])) return 0;
  for (i = 0; i < 4; i++) {
    if (!write_i32((int32_t)state->mid_side_amp_Q0[i])) return 0;
  }
  if (!write_i32((int32_t)state->smth_width_Q14) ||
      !write_i32((int32_t)state->width_prev_Q14) ||
      !write_i32((int32_t)state->silent_side_len)) {
    return 0;
  }
  for (i = 1; i <= frame_length; i++) {
    if (!write_i32((int32_t)mid[i])) return 0;
  }
  for (i = 1; i <= frame_length; i++) {
    if (!write_i32((int32_t)side[i])) return 0;
  }
  return 1;
}

static int eval_quant_pred(void) {
  int32_t raw;
  opus_int32 pred_Q13[2];
  opus_int8 ix[2][3] = {{0}};
  int32_t extra[6];
  if (!read_i32(&raw)) return 0;
  pred_Q13[0] = (opus_int32)raw;
  if (!read_i32(&raw)) return 0;
  pred_Q13[1] = (opus_int32)raw;
  silk_stereo_quant_pred(pred_Q13, ix);
  extra[0] = ix[0][0];
  extra[1] = ix[0][1];
  extra[2] = ix[0][2];
  extra[3] = ix[1][0];
  extra[4] = ix[1][1];
  extra[5] = ix[1][2];
  return write_stereo_record(pred_Q13[0], pred_Q13[1], extra);
}

static int eval_find_predictor(void) {
  int i;
  int32_t raw;
  opus_int length;
  opus_int smooth_coef_Q16;
  opus_int32 ratio_Q14;
  opus_int32 mid_res_amp_Q0[2];
  opus_int16 x[MAX_STEREO_SAMPLES];
  opus_int16 y[MAX_STEREO_SAMPLES];
  int32_t extra[6] = {0};
  opus_int32 pred_Q13;

  if (!read_i32(&raw)) return 0;
  length = (opus_int)raw;
  if (length <= 0 || length > MAX_STEREO_SAMPLES) return 0;
  if (!read_i32(&raw)) return 0;
  mid_res_amp_Q0[0] = (opus_int32)raw;
  if (!read_i32(&raw)) return 0;
  mid_res_amp_Q0[1] = (opus_int32)raw;
  if (!read_i32(&raw)) return 0;
  smooth_coef_Q16 = (opus_int)raw;
  for (i = 0; i < length; i++) {
    if (!read_i32(&raw)) return 0;
    x[i] = (opus_int16)raw;
  }
  for (i = 0; i < length; i++) {
    if (!read_i32(&raw)) return 0;
    y[i] = (opus_int16)raw;
  }

  pred_Q13 = silk_stereo_find_predictor(&ratio_Q14, x, y, mid_res_amp_Q0, length, smooth_coef_Q16);
  extra[0] = mid_res_amp_Q0[0];
  extra[1] = mid_res_amp_Q0[1];
  return write_stereo_record(pred_Q13, ratio_Q14, extra);
}

static int eval_lr_to_ms(void) {
  int i;
  int32_t raw;
  opus_int frame_length;
  opus_int fs_kHz;
  opus_int total_rate_bps;
  opus_int prev_speech_act_Q8;
  opus_int toMono;
  stereo_enc_state state;
  opus_int16 x1[MAX_STEREO_SAMPLES + 2] = {0};
  opus_int16 x2[MAX_STEREO_SAMPLES + 2] = {0};
  opus_int8 ix[2][3] = {{0}};
  opus_int8 mid_only = 0;
  opus_int32 rates[2] = {0, 0};

  memset(&state, 0, sizeof(state));
  if (!read_i32(&raw)) return 0;
  frame_length = (opus_int)raw;
  if (frame_length <= 0 || frame_length > MAX_STEREO_SAMPLES) return 0;
  if (!read_i32(&raw)) return 0;
  fs_kHz = (opus_int)raw;
  if (!read_i32(&raw)) return 0;
  total_rate_bps = (opus_int)raw;
  if (!read_i32(&raw)) return 0;
  prev_speech_act_Q8 = (opus_int)raw;
  if (!read_i32(&raw)) return 0;
  toMono = (opus_int)(raw != 0);

  if (!read_i32(&raw)) return 0; state.pred_prev_Q13[0] = (opus_int16)raw;
  if (!read_i32(&raw)) return 0; state.pred_prev_Q13[1] = (opus_int16)raw;
  if (!read_i32(&raw)) return 0; state.sMid[0] = (opus_int16)raw;
  if (!read_i32(&raw)) return 0; state.sMid[1] = (opus_int16)raw;
  if (!read_i32(&raw)) return 0; state.sSide[0] = (opus_int16)raw;
  if (!read_i32(&raw)) return 0; state.sSide[1] = (opus_int16)raw;
  for (i = 0; i < 4; i++) {
    if (!read_i32(&raw)) return 0;
    state.mid_side_amp_Q0[i] = (opus_int32)raw;
  }
  if (!read_i32(&raw)) return 0; state.smth_width_Q14 = (opus_int16)raw;
  if (!read_i32(&raw)) return 0; state.width_prev_Q14 = (opus_int16)raw;
  if (!read_i32(&raw)) return 0; state.silent_side_len = (opus_int16)raw;

  for (i = 0; i < frame_length; i++) {
    if (!read_i32(&raw)) return 0;
    x1[i + 2] = (opus_int16)raw;
  }
  for (i = 0; i < frame_length; i++) {
    if (!read_i32(&raw)) return 0;
    x2[i + 2] = (opus_int16)raw;
  }

  silk_stereo_LR_to_MS(&state, &x1[2], &x2[2], ix, &mid_only, rates, total_rate_bps,
      prev_speech_act_Q8, toMono, fs_kHz, frame_length);
  return write_lr_to_ms_record(&state, ix, mid_only, rates, x1, x2, frame_length);
}

static int eval_record(uint32_t mode) {
  switch (mode) {
    case MODE_STEREO_QUANT_PRED: return eval_quant_pred();
    case MODE_STEREO_FIND_PREDICTOR: return eval_find_predictor();
    case MODE_STEREO_LR_TO_MS: return eval_lr_to_ms();
  }
  return 0;
}

int main(void) {
  char magic[4];
  uint32_t version;
  uint32_t mode;
  uint32_t count;
  uint32_t i;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) return 1;
  if (!read_u32(&version) || version != 1 || !read_u32(&mode) || !read_u32(&count)) return 1;
  if (mode > MODE_STEREO_LR_TO_MS) return 1;

  if (!write_exact(OUTPUT_MAGIC, sizeof(magic)) || !write_u32(1) || !write_u32(count)) return 1;
  for (i = 0; i < count; i++) {
    if (!eval_record(mode)) return 1;
  }
  return 0;
}
