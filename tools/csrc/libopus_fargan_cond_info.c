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
#include "fargan_data.h"

#undef HAVE_CONFIG_H
#ifdef USE_WEIGHTS_FILE
#undef USE_WEIGHTS_FILE
#endif
#include "fargan_data.c"

#define INPUT_MAGIC "GFCI"
#define OUTPUT_MAGIC "GFCO"
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

static void compute_generic_dense_c(const LinearLayer *layer, float *output, const float *input, int activation) {
  compute_linear_c(layer, output, input);
  compute_activation_c(output, output, layer->nb_outputs, activation);
}

static void compute_generic_conv1d_c(const LinearLayer *layer, float *output, float *mem, const float *input, int input_size, int activation) {
  float tmp[COND_NET_FCONV1_STATE_SIZE + COND_NET_FCONV1_IN_SIZE];
  if (layer->nb_inputs != input_size) {
    memcpy(tmp, mem, (size_t)(layer->nb_inputs - input_size) * sizeof(float));
  }
  memcpy(&tmp[layer->nb_inputs - input_size], input, (size_t)input_size * sizeof(float));
  compute_linear_c(layer, output, tmp);
  compute_activation_c(output, output, layer->nb_outputs, activation);
  if (layer->nb_inputs != input_size) {
    memcpy(mem, &tmp[input_size], (size_t)(layer->nb_inputs - input_size) * sizeof(float));
  }
}

static int clamp_pembed_index(int period) {
  int idx = period - 32;
  if (idx < 0) return 0;
  if (idx > 223) return 223;
  return idx;
}

static void compute_fargan_cond_info(FARGAN *model, float *cond, float *cond_state, const float *features, int period) {
  int i;
  int slot = clamp_pembed_index(period);
  float dense_in[NB_FEATURES + COND_NET_PEMBED_OUT_SIZE];
  float conv1_in[COND_NET_FCONV1_IN_SIZE];
  float fdense2_in[COND_NET_FCONV1_OUT_SIZE];

  memcpy(dense_in, features, NB_FEATURES * sizeof(float));
  for (i = 0; i < COND_NET_PEMBED_OUT_SIZE; i++) {
    dense_in[NB_FEATURES + i] = model->cond_net_pembed.float_weights[slot * COND_NET_PEMBED_OUT_SIZE + i];
  }

  compute_generic_dense_c(&model->cond_net_fdense1, conv1_in, dense_in, ACTIVATION_TANH);
  compute_generic_conv1d_c(&model->cond_net_fconv1, fdense2_in, cond_state, conv1_in, COND_NET_FCONV1_IN_SIZE, ACTIVATION_TANH);
  compute_generic_dense_c(&model->cond_net_fdense2, cond, fdense2_in, ACTIVATION_TANH);
}

int main(void) {
  char magic[4];
  uint32_t version;
  int32_t period;
  float features[NB_FEATURES];
  float cond_state[COND_NET_FCONV1_STATE_SIZE];
  float cond[COND_NET_FDENSE2_OUT_SIZE];
  FARGAN model;

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
  if (!read_exact(&period, sizeof(period))) {
    fprintf(stderr, "failed to read period\n");
    return 1;
  }
  if (!read_bits_array(features, NB_FEATURES)) {
    fprintf(stderr, "failed to read features\n");
    return 1;
  }
  if (!read_bits_array(cond_state, COND_NET_FCONV1_STATE_SIZE)) {
    fprintf(stderr, "failed to read cond state\n");
    return 1;
  }
  if (init_fargan(&model, fargan_arrays)) {
    fprintf(stderr, "failed to init fargan model\n");
    return 1;
  }

  compute_fargan_cond_info(&model, cond, cond_state, features, (int)period);

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_exact(&version, sizeof(version))) {
    fprintf(stderr, "failed to write header\n");
    return 1;
  }
  if (!write_bits_array(cond, COND_NET_FDENSE2_OUT_SIZE) ||
      !write_bits_array(cond_state, COND_NET_FCONV1_STATE_SIZE)) {
    fprintf(stderr, "failed to write output\n");
    return 1;
  }
  return 0;
}
