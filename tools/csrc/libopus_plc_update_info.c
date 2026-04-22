#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <math.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#ifdef HAVE_CONFIG_H
#include "config.h"
#endif

#include "lpcnet_private.h"

#define INPUT_MAGIC "GPUI"
#define OUTPUT_MAGIC "GPUO"

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

static int read_bits_array(float *dst, int count) {
  int i;
  for (i = 0; i < count; i++) {
    uint32_t bits;
    if (!read_exact(&bits, sizeof(bits))) return 0;
    memcpy(&dst[i], &bits, sizeof(bits));
  }
  return 1;
}

static int write_bits_array(const float *src, int count) {
  int i;
  for (i = 0; i < count; i++) {
    uint32_t bits;
    memcpy(&bits, &src[i], sizeof(bits));
    if (!write_exact(&bits, sizeof(bits))) return 0;
  }
  return 1;
}

static opus_int16 float_to_pcm16(float x) {
  float scaled = x * 32768.f;
  if (scaled < -32767.f) scaled = -32767.f;
  if (scaled > 32767.f) scaled = 32767.f;
  return (opus_int16)floorf(.5f + scaled);
}

int main(void) {
  char magic[4];
  uint32_t version;
  int32_t blend;
  int32_t loss_count;
  int32_t analysis_gap;
  int32_t analysis_pos;
  int32_t predict_pos;
  float framef[FRAME_SIZE];
  opus_int16 frame16[FRAME_SIZE];
  LPCNetPLCState st;

  if (!set_binary_stdio()) {
    fprintf(stderr, "failed to set binary stdio mode\n");
    return 1;
  }
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) {
    fprintf(stderr, "invalid input magic\n");
    return 1;
  }
  if (!read_exact(&version, sizeof(version)) || version != 1) {
    fprintf(stderr, "unsupported input version\n");
    return 1;
  }
  if (!read_exact(&blend, sizeof(blend)) ||
      !read_exact(&loss_count, sizeof(loss_count)) ||
      !read_exact(&analysis_gap, sizeof(analysis_gap)) ||
      !read_exact(&analysis_pos, sizeof(analysis_pos)) ||
      !read_exact(&predict_pos, sizeof(predict_pos))) {
    fprintf(stderr, "failed to read update header\n");
    return 1;
  }

  memset(&st, 0, sizeof(st));
  st.loaded = 1;
  st.blend = blend;
  st.loss_count = loss_count;
  st.analysis_gap = analysis_gap;
  st.analysis_pos = analysis_pos;
  st.predict_pos = predict_pos;
  if (!read_bits_array(st.pcm, PLC_BUF_SIZE) || !read_bits_array(framef, FRAME_SIZE)) {
    fprintf(stderr, "failed to read update payload\n");
    return 1;
  }
  for (int i = 0; i < FRAME_SIZE; i++) {
    frame16[i] = float_to_pcm16(framef[i]);
  }

  lpcnet_plc_update(&st, frame16);

  if (!write_exact(OUTPUT_MAGIC, 4) ||
      !write_exact(&version, sizeof(version)) ||
      !write_exact(&st.blend, sizeof(st.blend)) ||
      !write_exact(&st.loss_count, sizeof(st.loss_count)) ||
      !write_exact(&st.analysis_gap, sizeof(st.analysis_gap)) ||
      !write_exact(&st.analysis_pos, sizeof(st.analysis_pos)) ||
      !write_exact(&st.predict_pos, sizeof(st.predict_pos))) {
    fprintf(stderr, "failed to write update header\n");
    return 1;
  }
  if (!write_bits_array(st.pcm, PLC_BUF_SIZE)) {
    fprintf(stderr, "failed to write update payload\n");
    return 1;
  }
  return 0;
}
