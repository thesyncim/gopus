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
#include "plc_data.h"
#include "cpu_support.h"

#undef HAVE_CONFIG_H
#ifdef USE_WEIGHTS_FILE
#undef USE_WEIGHTS_FILE
#endif
#include "plc_data.c"

#define INPUT_MAGIC "GPLI"
#define OUTPUT_MAGIC "GPLO"
#define NB_FEATURES 20
#define NB_BANDS 18
#define PLC_INPUT_SIZE (2*NB_BANDS + NB_FEATURES + 1)

typedef struct {
  float gru1_state[PLC_GRU1_STATE_SIZE];
  float gru2_state[PLC_GRU2_STATE_SIZE];
} PLCNetState;

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

static void compute_generic_dense_info(const LinearLayer *layer, float *output, const float *input, int activation, int arch) {
  compute_generic_dense(layer, output, input, activation, arch);
}

static void compute_generic_gru_info(const LinearLayer *input_weights, const LinearLayer *recurrent_weights, float *state, const float *in, int arch) {
  compute_generic_gru(input_weights, recurrent_weights, state, in, arch);
}

static void compute_plc_pred_info(PLCModel *model, PLCNetState *net, float *out, const float *in, int arch) {
  float tmp[PLC_DENSE_IN_OUT_SIZE];
  compute_generic_dense_info(&model->plc_dense_in, tmp, in, ACTIVATION_TANH, arch);
  compute_generic_gru_info(&model->plc_gru1_input, &model->plc_gru1_recurrent, net->gru1_state, tmp, arch);
  compute_generic_gru_info(&model->plc_gru2_input, &model->plc_gru2_recurrent, net->gru2_state, net->gru1_state, arch);
  compute_generic_dense_info(&model->plc_dense_out, out, net->gru2_state, ACTIVATION_LINEAR, arch);
}

int main(void) {
  char magic[4];
  uint32_t version;
  float input[PLC_INPUT_SIZE];
  float output[NB_FEATURES];
  PLCModel model;
  PLCNetState net;
  int arch;

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
  if (!read_bits_array(input, PLC_INPUT_SIZE)) {
    fprintf(stderr, "failed to read input vector\n");
    return 1;
  }
  if (!read_bits_array(net.gru1_state, PLC_GRU1_STATE_SIZE)) {
    fprintf(stderr, "failed to read gru1 state\n");
    return 1;
  }
  if (!read_bits_array(net.gru2_state, PLC_GRU2_STATE_SIZE)) {
    fprintf(stderr, "failed to read gru2 state\n");
    return 1;
  }
  if (init_plcmodel(&model, plcmodel_arrays)) {
    fprintf(stderr, "failed to init plc model\n");
    return 1;
  }

  arch = opus_select_arch();
  compute_plc_pred_info(&model, &net, output, input, arch);

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_exact(&version, sizeof(version))) {
    fprintf(stderr, "failed to write header\n");
    return 1;
  }
  if (!write_bits_array(output, NB_FEATURES) ||
      !write_bits_array(net.gru1_state, PLC_GRU1_STATE_SIZE) ||
      !write_bits_array(net.gru2_state, PLC_GRU2_STATE_SIZE)) {
    fprintf(stderr, "failed to write output\n");
    return 1;
  }
  return 0;
}
