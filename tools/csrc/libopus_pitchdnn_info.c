#include <stdint.h>
#include <stdio.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#ifdef HAVE_CONFIG_H
#include "config.h"
#endif

#include "pitchdnn.h"
#include "nnet.h"

#undef HAVE_CONFIG_H
#ifdef USE_WEIGHTS_FILE
#undef USE_WEIGHTS_FILE
#endif
#include "pitchdnn_data.c"

#define INPUT_MAGIC "GPDI"
#define OUTPUT_MAGIC "GPDO"
#define PITCH_IF_MAX_FREQ 30
#define PITCH_IF_FEATURES (3*PITCH_IF_MAX_FREQ - 2)

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

int main(void) {
  char magic[4];
  uint32_t version;
  float if_features[PITCH_IF_FEATURES];
  float xcorr_features[NB_XCORR_FEATURES];
  float pitch;
  PitchDNNState st;

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
  if (!read_bits_array(if_features, PITCH_IF_FEATURES)) {
    fprintf(stderr, "failed to read if_features\n");
    return 1;
  }
  if (!read_bits_array(xcorr_features, NB_XCORR_FEATURES)) {
    fprintf(stderr, "failed to read xcorr_features\n");
    return 1;
  }

  pitchdnn_init(&st);
  if (!read_bits_array(st.gru_state, GRU_1_STATE_SIZE)) {
    fprintf(stderr, "failed to read gru_state\n");
    return 1;
  }
  if (!read_bits_array(st.xcorr_mem1, (NB_XCORR_FEATURES + 2) * 2)) {
    fprintf(stderr, "failed to read xcorr_mem1\n");
    return 1;
  }
  if (!read_bits_array(st.xcorr_mem2, (NB_XCORR_FEATURES + 2) * 2 * 8)) {
    fprintf(stderr, "failed to read xcorr_mem2\n");
    return 1;
  }
  if (!read_bits_array(st.xcorr_mem3, (NB_XCORR_FEATURES + 2) * 2 * 8)) {
    fprintf(stderr, "failed to read xcorr_mem3\n");
    return 1;
  }

  if (init_pitchdnn(&st.model, pitchdnn_arrays)) {
    fprintf(stderr, "failed to init pitchdnn model\n");
    return 1;
  }
  pitch = compute_pitchdnn(&st, if_features, xcorr_features, 0);

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_exact(&version, sizeof(version))) {
    fprintf(stderr, "failed to write header\n");
    return 1;
  }
  if (!write_bits_array(&pitch, 1) ||
      !write_bits_array(st.gru_state, GRU_1_STATE_SIZE) ||
      !write_bits_array(st.xcorr_mem1, (NB_XCORR_FEATURES + 2) * 2) ||
      !write_bits_array(st.xcorr_mem2, (NB_XCORR_FEATURES + 2) * 2 * 8) ||
      !write_bits_array(st.xcorr_mem3, (NB_XCORR_FEATURES + 2) * 2 * 8)) {
    fprintf(stderr, "failed to write output\n");
    return 1;
  }
  return 0;
}
