#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#ifdef HAVE_CONFIG_H
#include "config.h"
#endif

#include "lpcnet.h"
#include "lpcnet_private.h"

#define INPUT_MAGIC "GLFI"
#define OUTPUT_MAGIC "GLFO"

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

static int write_complex_array(const kiss_fft_cpx *src, int count) {
  int i;
  for (i = 0; i < count; i++) {
    if (!write_bits_array(&src[i].r, 1) || !write_bits_array(&src[i].i, 1)) return 0;
  }
  return 1;
}

int main(void) {
  char magic[4];
  uint32_t version;
  uint32_t frame_count;
  float *all_features = NULL;
  LPCNetEncState st;
  float frame[FRAME_SIZE];
  uint32_t i;

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
  if (!read_exact(&frame_count, sizeof(frame_count))) {
    fprintf(stderr, "failed to read frame count\n");
    return 1;
  }

  if (frame_count == 0) {
    fprintf(stderr, "frame count must be positive\n");
    return 1;
  }
  all_features = (float *)calloc((size_t)frame_count * NB_TOTAL_FEATURES, sizeof(float));
  if (all_features == NULL) {
    fprintf(stderr, "failed to allocate output buffer\n");
    return 1;
  }

  lpcnet_encoder_init(&st);
  for (i = 0; i < frame_count; i++) {
    if (!read_bits_array(frame, FRAME_SIZE)) {
      fprintf(stderr, "failed to read frame data\n");
      free(all_features);
      return 1;
    }
    lpcnet_compute_single_frame_features_float(&st, frame, &all_features[i * NB_TOTAL_FEATURES], 0);
  }

  if (!write_exact(OUTPUT_MAGIC, 4) ||
      !write_exact(&version, sizeof(version)) ||
      !write_exact(&frame_count, sizeof(frame_count))) {
    fprintf(stderr, "failed to write header\n");
    free(all_features);
    return 1;
  }
  if (!write_bits_array(all_features, frame_count * NB_TOTAL_FEATURES) ||
      !write_bits_array(st.analysis_mem, OVERLAP_SIZE) ||
      !write_bits_array(&st.mem_preemph, 1) ||
      !write_complex_array(st.prev_if, PITCH_IF_MAX_FREQ) ||
      !write_bits_array(st.if_features, PITCH_IF_FEATURES) ||
      !write_bits_array(st.xcorr_features, PITCH_MAX_PERIOD - PITCH_MIN_PERIOD) ||
      !write_bits_array(&st.dnn_pitch, 1) ||
      !write_bits_array(st.pitch_mem, LPC_ORDER) ||
      !write_bits_array(&st.pitch_filt, 1) ||
      !write_bits_array(st.exc_buf, PITCH_BUF_SIZE) ||
      !write_bits_array(st.lp_buf, PITCH_BUF_SIZE) ||
      !write_bits_array(st.lp_mem, 4) ||
      !write_bits_array(st.lpc, LPC_ORDER) ||
      !write_bits_array(st.pitchdnn.gru_state, GRU_1_STATE_SIZE) ||
      !write_bits_array(st.pitchdnn.xcorr_mem1, (NB_XCORR_FEATURES + 2) * 2) ||
      !write_bits_array(st.pitchdnn.xcorr_mem2, (NB_XCORR_FEATURES + 2) * 2 * 8) ||
      !write_bits_array(st.pitchdnn.xcorr_mem3, (NB_XCORR_FEATURES + 2) * 2 * 8)) {
    fprintf(stderr, "failed to write output\n");
    free(all_features);
    return 1;
  }

  free(all_features);
  return 0;
}
