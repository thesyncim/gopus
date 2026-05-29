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

#include "pitchdnn.h"
#include "nnet.h"
#include "cpu_support.h"
#include "os_support.h"

#undef HAVE_CONFIG_H
#ifdef USE_WEIGHTS_FILE
#undef USE_WEIGHTS_FILE
#endif
#include "pitchdnn_data.c"

#define INPUT_MAGIC "GPSI"
#define OUTPUT_MAGIC "GPSO"
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
  PitchDNNState st;
  int arch, i;

  /* Stage buffers mirroring compute_pitchdnn(). */
  float if1_out[DENSE_IF_UPSAMPLER_1_OUT_SIZE];
  float downsampler_in[NB_XCORR_FEATURES + DENSE_IF_UPSAMPLER_2_OUT_SIZE];
  float downsampler_out[DENSE_DOWNSAMPLER_OUT_SIZE];
  float conv1_tmp1[(NB_XCORR_FEATURES + 2)*8] = {0};
  float conv1_tmp2[(NB_XCORR_FEATURES + 2)*8] = {0};
  float output[DENSE_FINAL_UPSAMPLER_OUT_SIZE];
  float pitch;
  int pos = 0;
  float maxval = -1, sum = 0, count = 0;
  PitchDNN *model;

  if (!set_binary_stdio()) { fprintf(stderr, "stdio\n"); return 1; }
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) { fprintf(stderr, "magic\n"); return 1; }
  if (!read_exact(&version, sizeof(version)) || version != 1) { fprintf(stderr, "version\n"); return 1; }
  if (!read_bits_array(if_features, PITCH_IF_FEATURES)) { fprintf(stderr, "if\n"); return 1; }
  if (!read_bits_array(xcorr_features, NB_XCORR_FEATURES)) { fprintf(stderr, "xcorr\n"); return 1; }

  pitchdnn_init(&st);
  if (!read_bits_array(st.gru_state, GRU_1_STATE_SIZE)) { fprintf(stderr, "gru\n"); return 1; }
  if (!read_bits_array(st.xcorr_mem1, (NB_XCORR_FEATURES + 2) * 2)) { fprintf(stderr, "m1\n"); return 1; }
  if (!read_bits_array(st.xcorr_mem2, (NB_XCORR_FEATURES + 2) * 2 * 8)) { fprintf(stderr, "m2\n"); return 1; }
  if (!read_bits_array(st.xcorr_mem3, (NB_XCORR_FEATURES + 2) * 2 * 8)) { fprintf(stderr, "m3\n"); return 1; }
  if (init_pitchdnn(&st.model, pitchdnn_arrays)) { fprintf(stderr, "init\n"); return 1; }
  arch = opus_select_arch();
  model = &st.model;

  compute_generic_dense(&model->dense_if_upsampler_1, if1_out, if_features, ACTIVATION_TANH, arch);
  compute_generic_dense(&model->dense_if_upsampler_2, &downsampler_in[NB_XCORR_FEATURES], if1_out, ACTIVATION_TANH, arch);
  OPUS_COPY(&conv1_tmp1[1], xcorr_features, NB_XCORR_FEATURES);
  compute_conv2d(&model->conv2d_1, &conv1_tmp2[1], st.xcorr_mem1, conv1_tmp1, NB_XCORR_FEATURES, NB_XCORR_FEATURES+2, ACTIVATION_TANH, arch);
  compute_conv2d(&model->conv2d_2, downsampler_in, st.xcorr_mem2, conv1_tmp2, NB_XCORR_FEATURES, NB_XCORR_FEATURES, ACTIVATION_TANH, arch);
  compute_generic_dense(&model->dense_downsampler, downsampler_out, downsampler_in, ACTIVATION_TANH, arch);
  compute_generic_gru(&model->gru_1_input, &model->gru_1_recurrent, st.gru_state, downsampler_out, arch);
  compute_generic_dense(&model->dense_final_upsampler, output, st.gru_state, ACTIVATION_LINEAR, arch);
  for (i = 0; i < 180; i++) {
    if (output[i] > maxval) { pos = i; maxval = output[i]; }
  }
  {
    int s = (0 > pos-2 ? 0 : pos-2);
    int e = (179 < pos+2 ? 179 : pos+2);
    float expwin[5];
    int k = 0;
    for (i = s; i <= e; i++) {
      float p = exp(output[i]);
      if (k < 5) expwin[k++] = p;
      sum += p*i;
      count += p;
    }
    for (; k < 5; k++) expwin[k] = 0;
    pitch = (1.f/60.f)*(sum/count) - 1.5f;
    /* stash exp window after the output for diagnostics */
    if (!write_exact(OUTPUT_MAGIC, 4) || !write_exact(&version, sizeof(version))) { fprintf(stderr, "hdr\n"); return 1; }
    if (!write_bits_array(if1_out, DENSE_IF_UPSAMPLER_1_OUT_SIZE) ||
        !write_bits_array(&downsampler_in[NB_XCORR_FEATURES], DENSE_IF_UPSAMPLER_2_OUT_SIZE) ||
        !write_bits_array(&conv1_tmp2[1], NB_XCORR_FEATURES) ||
        !write_bits_array(downsampler_in, NB_XCORR_FEATURES) ||
        !write_bits_array(downsampler_out, DENSE_DOWNSAMPLER_OUT_SIZE) ||
        !write_bits_array(st.gru_state, GRU_1_STATE_SIZE) ||
        !write_bits_array(output, DENSE_FINAL_UPSAMPLER_OUT_SIZE) ||
        !write_bits_array(expwin, 5) ||
        !write_bits_array(&pitch, 1)) {
      fprintf(stderr, "out\n");
      return 1;
    }
    return 0;
  }
}
