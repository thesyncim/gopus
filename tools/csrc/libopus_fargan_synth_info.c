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

#include "nnet.h"
#include "fargan.h"

#undef HAVE_CONFIG_H
#ifdef USE_WEIGHTS_FILE
#undef USE_WEIGHTS_FILE
#endif
#include "fargan_data.c"

#define INPUT_MAGIC "GFSI"
#define OUTPUT_MAGIC "GFSO"
#define NB_FEATURES 20

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
  int32_t cont_initialized;
  FARGANState st;
  float pcm[FARGAN_FRAME_SIZE];
  float features[NB_FEATURES];

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

  fargan_init(&st);
  if (!read_exact(&cont_initialized, sizeof(cont_initialized)) ||
      !read_exact(&st.last_period, sizeof(st.last_period)) ||
      !read_bits_array(&st.deemph_mem, 1) ||
      !read_bits_array(st.pitch_buf, PITCH_MAX_PERIOD) ||
      !read_bits_array(st.cond_conv1_state, COND_NET_FCONV1_STATE_SIZE) ||
      !read_bits_array(st.fwc0_mem, SIG_NET_FWC0_STATE_SIZE) ||
      !read_bits_array(st.gru1_state, SIG_NET_GRU1_STATE_SIZE) ||
      !read_bits_array(st.gru2_state, SIG_NET_GRU2_STATE_SIZE) ||
      !read_bits_array(st.gru3_state, SIG_NET_GRU3_STATE_SIZE) ||
      !read_bits_array(features, NB_FEATURES)) {
    fprintf(stderr, "failed to read fargan synth payload\n");
    return 1;
  }
  st.cont_initialized = cont_initialized;

  fargan_synthesize(&st, pcm, features);

  cont_initialized = st.cont_initialized;
  if (!write_exact(OUTPUT_MAGIC, 4) ||
      !write_exact(&version, sizeof(version)) ||
      !write_exact(&cont_initialized, sizeof(cont_initialized)) ||
      !write_exact(&st.last_period, sizeof(st.last_period))) {
    fprintf(stderr, "failed to write fargan synth header\n");
    return 1;
  }
  if (!write_bits_array(&st.deemph_mem, 1) ||
      !write_bits_array(pcm, FARGAN_FRAME_SIZE) ||
      !write_bits_array(st.pitch_buf, PITCH_MAX_PERIOD) ||
      !write_bits_array(st.cond_conv1_state, COND_NET_FCONV1_STATE_SIZE) ||
      !write_bits_array(st.fwc0_mem, SIG_NET_FWC0_STATE_SIZE) ||
      !write_bits_array(st.gru1_state, SIG_NET_GRU1_STATE_SIZE) ||
      !write_bits_array(st.gru2_state, SIG_NET_GRU2_STATE_SIZE) ||
      !write_bits_array(st.gru3_state, SIG_NET_GRU3_STATE_SIZE)) {
    fprintf(stderr, "failed to write fargan synth state\n");
    return 1;
  }
  return 0;
}
