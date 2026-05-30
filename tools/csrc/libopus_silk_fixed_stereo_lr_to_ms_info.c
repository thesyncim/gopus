/* Oracle for the libopus FIXED_POINT silk_stereo_LR_to_MS encode-side kernel.
 *
 * Must be compiled and linked against a libopus configured with
 * --enable-fixed-point (defines FIXED_POINT). Reads a little-endian payload of
 * cases from stdin and writes the bit-exact mid/side output, quantization
 * indices, mid_only_flag, mid/side rates, and updated stereo_enc_state to
 * stdout. */

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"

#ifndef FIXED_POINT
#error "this oracle requires a FIXED_POINT libopus build (--enable-fixed-point)"
#endif

#include "SigProc_FIX.h"
#include "define.h"
#include "structs.h"

#define INPUT_MAGIC "GSLI"
#define OUTPUT_MAGIC "GSLO"

#define MAX_FRAME 320 /* 20 ms @ 16 kHz */

void silk_stereo_LR_to_MS(stereo_enc_state *state, opus_int16 x1[],
                          opus_int16 x2[], opus_int8 ix[2][3],
                          opus_int8 *mid_only_flag,
                          opus_int32 mid_side_rates_bps[],
                          opus_int32 total_rate_bps, opus_int prev_speech_act_Q8,
                          opus_int toMono, opus_int fs_kHz, opus_int frame_length);

#if defined(ENABLE_ASSERTIONS) || defined(ENABLE_HARDENING)
void celt_fatal(const char *str, const char *file, int line) {
  (void)str;
  (void)file;
  (void)line;
  abort();
}
#endif

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
  unsigned char b[4];
  if (!read_exact(b, sizeof(b))) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) | ((uint32_t)b[2] << 16) |
         ((uint32_t)b[3] << 24);
  return 1;
}

static int read_i32(int32_t *out) {
  uint32_t u;
  if (!read_u32(&u)) return 0;
  *out = (int32_t)u;
  return 1;
}

static int read_i16(int16_t *out) {
  unsigned char b[2];
  if (!read_exact(b, sizeof(b))) return 0;
  *out = (int16_t)((uint16_t)b[0] | ((uint16_t)b[1] << 8));
  return 1;
}

static int write_u32(uint32_t value) {
  unsigned char b[4];
  b[0] = (unsigned char)(value & 0xffu);
  b[1] = (unsigned char)((value >> 8) & 0xffu);
  b[2] = (unsigned char)((value >> 16) & 0xffu);
  b[3] = (unsigned char)((value >> 24) & 0xffu);
  return write_exact(b, sizeof(b));
}

static int write_i32(int32_t value) { return write_u32((uint32_t)value); }

static int write_i16(int16_t value) {
  unsigned char b[2];
  b[0] = (unsigned char)((uint16_t)value & 0xffu);
  b[1] = (unsigned char)(((uint16_t)value >> 8) & 0xffu);
  return write_exact(b, sizeof(b));
}

int main(void) {
  if (!set_binary_stdio()) return 1;

  char magic[4];
  if (!read_exact(magic, sizeof(magic)) ||
      memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) {
    return 1;
  }
  uint32_t version;
  if (!read_u32(&version) || version != 1) return 1;
  uint32_t count;
  if (!read_u32(&count)) return 1;

  if (!write_exact(OUTPUT_MAGIC, 4)) return 1;
  if (!write_u32(1)) return 1; /* version */
  if (!write_u32(count)) return 1;

  for (uint32_t c = 0; c < count; c++) {
    int32_t total_rate_bps, prev_speech_act_Q8, toMono, fs_kHz, frame_length;
    if (!read_i32(&total_rate_bps) || !read_i32(&prev_speech_act_Q8) ||
        !read_i32(&toMono) || !read_i32(&fs_kHz) || !read_i32(&frame_length)) {
      return 1;
    }
    if (frame_length < 0 || frame_length > MAX_FRAME) return 1;

    /* Initial stereo_enc_state. */
    stereo_enc_state state;
    memset(&state, 0, sizeof(state));
    for (int i = 0; i < 2; i++) {
      if (!read_i16(&state.pred_prev_Q13[i])) return 1;
    }
    for (int i = 0; i < 2; i++) {
      if (!read_i16(&state.sMid[i])) return 1;
    }
    for (int i = 0; i < 2; i++) {
      if (!read_i16(&state.sSide[i])) return 1;
    }
    for (int i = 0; i < 4; i++) {
      if (!read_i32(&state.mid_side_amp_Q0[i])) return 1;
    }
    if (!read_i16(&state.smth_width_Q14)) return 1;
    if (!read_i16(&state.width_prev_Q14)) return 1;
    if (!read_i16(&state.silent_side_len)) return 1;

    /* x1/x2 with two leading history samples each: layout matches libopus
     * mid = &x1[-2], so x1[-2..frame_length-1] is provided as frame_length+2
     * samples; we offset by 2. */
    static opus_int16 x1buf[MAX_FRAME + 2];
    static opus_int16 x2buf[MAX_FRAME + 2];
    for (int i = 0; i < frame_length + 2; i++) {
      if (!read_i16(&x1buf[i])) return 1;
    }
    for (int i = 0; i < frame_length + 2; i++) {
      if (!read_i16(&x2buf[i])) return 1;
    }

    opus_int16 *x1 = &x1buf[2];
    opus_int16 *x2 = &x2buf[2];

    opus_int8 ix[2][3];
    memset(ix, 0, sizeof(ix));
    opus_int8 mid_only_flag = 0;
    opus_int32 mid_side_rates_bps[2] = {0, 0};

    silk_stereo_LR_to_MS(&state, x1, x2, ix, &mid_only_flag, mid_side_rates_bps,
                         (opus_int32)total_rate_bps,
                         (opus_int)prev_speech_act_Q8, (opus_int)toMono,
                         (opus_int)fs_kHz, (opus_int)frame_length);

    /* Mid signal: mid = &x1[-2]; mid[0..frame_length+1]. We emit the full
     * frame_length+2 mid samples. */
    opus_int16 *mid = &x1[-2];
    for (int i = 0; i < frame_length + 2; i++) {
      if (!write_i16(mid[i])) return 1;
    }
    /* Side output: x2[n-1] for n in [0,frame_length); also include the two
     * history-buffer tail samples via state below. Emit x2[-1..frame_length-2]
     * i.e. x2buf[1..frame_length]. */
    for (int i = 0; i < frame_length; i++) {
      if (!write_i16(x2[i - 1])) return 1;
    }

    /* Quantization indices, mid_only_flag, rates. */
    for (int n = 0; n < 2; n++) {
      for (int k = 0; k < 3; k++) {
        if (!write_i32((int32_t)ix[n][k])) return 1;
      }
    }
    if (!write_i32((int32_t)mid_only_flag)) return 1;
    if (!write_i32((int32_t)mid_side_rates_bps[0])) return 1;
    if (!write_i32((int32_t)mid_side_rates_bps[1])) return 1;

    /* Updated state. */
    for (int i = 0; i < 2; i++) {
      if (!write_i16(state.pred_prev_Q13[i])) return 1;
    }
    for (int i = 0; i < 2; i++) {
      if (!write_i16(state.sMid[i])) return 1;
    }
    for (int i = 0; i < 2; i++) {
      if (!write_i16(state.sSide[i])) return 1;
    }
    for (int i = 0; i < 4; i++) {
      if (!write_i32(state.mid_side_amp_Q0[i])) return 1;
    }
    if (!write_i16(state.smth_width_Q14)) return 1;
    if (!write_i16(state.width_prev_Q14)) return 1;
    if (!write_i16(state.silent_side_len)) return 1;
  }

  return 0;
}
