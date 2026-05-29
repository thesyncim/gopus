/*
 * libopus_osce_lace_forward.c
 *
 * Drives the libopus OSCE LACE / NoLACE postfilter forward pass for a single
 * 20 ms 16 kHz input frame and emits the resulting 16 kHz float32 PCM to
 * stdout, prefixed with a small ASCII header describing the payload shape.
 *
 * USAGE:
 *   libopus_osce_lace_forward [NUM_SAMPLES_16K]
 *
 * The mode is selected by the MODE environment variable:
 *   MODE=lace    (default) -- runs lace_process_20ms_frame
 *   MODE=nolace             -- runs nolace_process_20ms_frame
 *
 * NUM_SAMPLES_16K defaults to 320 (a single 20 ms frame). Other lengths are
 * rejected because the LACE / NoLACE postfilters are defined only for the
 * 20 ms restricted-control framing libopus exposes.
 *
 * Test signal: 1 kHz sinusoid at amplitude 0.5 (full-scale int16 = 16383),
 * mirroring the gopus side of the parity test so the helper is fully
 * deterministic and does not depend on any fixture file.
 *
 * Features / numbits / periods are all zeroed except for `periods`, which is
 * set to a small non-zero value (60) so the AdaComb stages exercise their
 * pitch-lag path rather than degenerating to a no-op. This matches the
 * input shape the gopus runtime accepts in
 * `internal/osce/lace/runtime.go::Process`.
 *
 * The static `lace_process_20ms_frame` / `nolace_process_20ms_frame` symbols
 * are not exported by libopus.a; this helper #include's `osce.c` directly to
 * pull them into the helper TU. To avoid duplicate-symbol link errors with
 * libopus.a's copies of the non-static `osce_load_models` / `osce_reset` /
 * `osce_bwe` / `osce_bwe_reset` / `osce_enhance_frame`, those names are
 * redefined to helper-local aliases before the include.
 *
 * Output format on stdout (binary):
 *   8-byte ASCII tag "OSCELAC\0"
 *   int32 version        (== 1)
 *   int32 mode_id        (0 = LACE, 1 = NoLACE)
 *   int32 num_out_samples (== NUM_SAMPLES_16K, == 320)
 *   float32[num_out_samples] x_out (16 kHz, float in [-1, 1])
 *
 * When TRACE=1 and MODE=lace, stdout instead carries:
 *   8-byte ASCII tag "OSCELTR\0"
 *   int32 version
 *   int32 mode_id
 *   int32 sample_rate
 *   int32 frame_samples
 *   int32 subframes
 *   int32 stage_count
 * followed by stage_count records:
 *   int32 stage_id
 *   int32 subframe (-1 for full-frame records)
 *   int32 channels
 *   int32 samples_per_channel
 *   int32 values_len
 *   float32[values_len] values
 */

#include <math.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifndef M_PI
#define M_PI 3.14159265358979323846
#endif

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#ifdef HAVE_CONFIG_H
#include "config.h"
#endif

#ifndef ENABLE_OSCE
#error "libopus_osce_lace_forward.c requires libopus built with --enable-osce"
#endif

/* Rename the non-static osce.c symbols so libopus.a's copies don't collide
 * with the helper's local versions when we #include osce.c below. The header
 * declarations follow the same rename so prototypes match. */
#define osce_reset           _helper_osce_reset
#define osce_bwe_reset       _helper_osce_bwe_reset
#define osce_load_models     _helper_osce_load_models
#define osce_bwe             _helper_osce_bwe
#define osce_enhance_frame   _helper_osce_enhance_frame

#include "osce.c"

#undef osce_reset
#undef osce_bwe_reset
#undef osce_load_models
#undef osce_bwe
#undef osce_enhance_frame

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) {
    return 0;
  }
#endif
  return 1;
}

static void fill_sinusoid_float(float *out, int num_samples, double freq_hz, double amp) {
  for (int i = 0; i < num_samples; i++) {
    double v = amp * sin(2.0 * M_PI * freq_hz * (double)i / 16000.0);
    /* gopus side quantises through int16 -> float to match exact bit pattern;
     * do the same here so input samples match bit-for-bit. */
    long q = lrint(v * 32767.0);
    if (q > 32767) q = 32767;
    if (q < -32768) q = -32768;
    out[i] = (float)q / 32768.0f;
  }
}

enum {
  TRACE_STAGE_INPUT = 1,
  TRACE_STAGE_FEATURES = 2,
  TRACE_STAGE_NUMBITS = 3,
  TRACE_STAGE_PERIODS = 4,
  TRACE_STAGE_PREEMPH = 5,
  TRACE_STAGE_FEATURE_NET_CONV1 = 6,
  TRACE_STAGE_FEATURE_NET_CONV2_INPUT = 7,
  TRACE_STAGE_FEATURE_NET_CONV2_LINEAR = 8,
  TRACE_STAGE_FEATURE_NET_CONV2 = 9,
  TRACE_STAGE_FEATURE_NET_TCONV = 10,
  TRACE_STAGE_FEATURE_NET_LATENT = 11,
  TRACE_STAGE_POST_CF1 = 12,
  TRACE_STAGE_POST_CF2 = 13,
  TRACE_STAGE_POST_AF1 = 14,
  TRACE_STAGE_DEEMPH = 15,
  TRACE_STAGE_CF1_KERNEL_RAW = 16,
  TRACE_STAGE_CF1_GAINS_RAW = 17,
  TRACE_STAGE_CF1_KERNEL_SCALED = 18,
  TRACE_STAGE_CF1_GAINS_SCALED = 19,
  /* NoLACE-only stage checkpoints (mode_id == 1). */
  TRACE_STAGE_NL_PREEMPH = 30,
  TRACE_STAGE_NL_LATENT = 31,
  TRACE_STAGE_NL_POST_CF1 = 32,
  TRACE_STAGE_NL_POST_CF2 = 33,
  TRACE_STAGE_NL_POST_AF1 = 34,
  TRACE_STAGE_NL_POST_TDSHAPE1 = 35,
  TRACE_STAGE_NL_POST_AF2 = 36,
  TRACE_STAGE_NL_POST_TDSHAPE2 = 37,
  TRACE_STAGE_NL_POST_AF3 = 38,
  TRACE_STAGE_NL_POST_TDSHAPE3 = 39,
  TRACE_STAGE_NL_POST_AF4 = 40,
  TRACE_STAGE_NL_DEEMPH = 41
};

static int write_i32(int32_t v) {
  return fwrite(&v, sizeof(v), 1, stdout) == 1;
}

static int write_trace_header(int mode_id, int stage_count) {
  static const char tag[8] = {'O','S','C','E','L','T','R','\0'};
  if (fwrite(tag, 1, sizeof(tag), stdout) != sizeof(tag)) return 0;
  return write_i32(1)
      && write_i32((int32_t)mode_id)
      && write_i32(16000)
      && write_i32(320)
      && write_i32(4)
      && write_i32((int32_t)stage_count);
}

static int write_trace_record(
    int stage_id,
    int subframe,
    int channels,
    int samples_per_channel,
    const float *values,
    int values_len
) {
  if (!write_i32((int32_t)stage_id)) return 0;
  if (!write_i32((int32_t)subframe)) return 0;
  if (!write_i32((int32_t)channels)) return 0;
  if (!write_i32((int32_t)samples_per_channel)) return 0;
  if (!write_i32((int32_t)values_len)) return 0;
  return fwrite(values, sizeof(float), (size_t)values_len, stdout) == (size_t)values_len;
}

static int trace_lace_feature_net(
    LACE *hLACE,
    LACEState *state,
    float *output,
    const float *features,
    const float *numbits,
    const int *periods,
    int arch
) {
  float input_buffer[IMAX(4 * IMAX(LACE_COND_DIM, LACE_HIDDEN_FEATURE_DIM), LACE_NUM_FEATURES + LACE_PITCH_EMBEDDING_DIM + 2*LACE_NUMBITS_EMBEDDING_DIM)];
  float output_buffer[4 * IMAX(LACE_COND_DIM, LACE_HIDDEN_FEATURE_DIM)];
  float conv2_input[LACE_FNET_CONV2_STATE_SIZE + LACE_FNET_CONV2_IN_SIZE];
  float numbits_embedded[2 * LACE_NUMBITS_EMBEDDING_DIM];
  int i_subframe;

  compute_lace_numbits_embedding(numbits_embedded, numbits[0], LACE_NUMBITS_EMBEDDING_DIM,
      log(LACE_NUMBITS_RANGE_LOW), log(LACE_NUMBITS_RANGE_HIGH), 1);
  compute_lace_numbits_embedding(numbits_embedded + LACE_NUMBITS_EMBEDDING_DIM, numbits[1], LACE_NUMBITS_EMBEDDING_DIM,
      log(LACE_NUMBITS_RANGE_LOW), log(LACE_NUMBITS_RANGE_HIGH), 1);

  for (i_subframe = 0; i_subframe < 4; i_subframe ++) {
    OPUS_COPY(input_buffer, features + i_subframe * LACE_NUM_FEATURES, LACE_NUM_FEATURES);
    OPUS_COPY(input_buffer + LACE_NUM_FEATURES, hLACE->layers.lace_pitch_embedding.float_weights + periods[i_subframe] * LACE_PITCH_EMBEDDING_DIM, LACE_PITCH_EMBEDDING_DIM);
    OPUS_COPY(input_buffer + LACE_NUM_FEATURES + LACE_PITCH_EMBEDDING_DIM, numbits_embedded, 2 * LACE_NUMBITS_EMBEDDING_DIM);

    compute_generic_conv1d(
        &hLACE->layers.lace_fnet_conv1,
        output_buffer + i_subframe * LACE_HIDDEN_FEATURE_DIM,
        NULL,
        input_buffer,
        LACE_NUM_FEATURES + LACE_PITCH_EMBEDDING_DIM + 2 * LACE_NUMBITS_EMBEDDING_DIM,
        ACTIVATION_TANH,
        arch);
  }
  if (!write_trace_record(TRACE_STAGE_FEATURE_NET_CONV1, -1, 1, 4 * LACE_HIDDEN_FEATURE_DIM, output_buffer, 4 * LACE_HIDDEN_FEATURE_DIM)) return 0;

  OPUS_COPY(conv2_input, state->feature_net_conv2_state, LACE_FNET_CONV2_STATE_SIZE);
  OPUS_COPY(conv2_input + LACE_FNET_CONV2_STATE_SIZE, output_buffer, 4 * LACE_HIDDEN_FEATURE_DIM);
  if (!write_trace_record(TRACE_STAGE_FEATURE_NET_CONV2_INPUT, -1, 1, LACE_FNET_CONV2_STATE_SIZE + LACE_FNET_CONV2_IN_SIZE, conv2_input, LACE_FNET_CONV2_STATE_SIZE + LACE_FNET_CONV2_IN_SIZE)) return 0;

  compute_linear(&hLACE->layers.lace_fnet_conv2, output_buffer, conv2_input, arch);
  if (!write_trace_record(TRACE_STAGE_FEATURE_NET_CONV2_LINEAR, -1, 1, LACE_COND_DIM, output_buffer, LACE_COND_DIM)) return 0;
  compute_activation(output_buffer, output_buffer, LACE_COND_DIM, ACTIVATION_TANH, arch);
  OPUS_COPY(state->feature_net_conv2_state, conv2_input + LACE_FNET_CONV2_IN_SIZE, LACE_FNET_CONV2_STATE_SIZE);
  if (!write_trace_record(TRACE_STAGE_FEATURE_NET_CONV2, -1, 1, LACE_COND_DIM, output_buffer, LACE_COND_DIM)) return 0;

  OPUS_COPY(input_buffer, output_buffer, 4 * LACE_COND_DIM);
  compute_generic_dense(
      &hLACE->layers.lace_fnet_tconv,
      output_buffer,
      input_buffer,
      ACTIVATION_TANH,
      arch
  );
  if (!write_trace_record(TRACE_STAGE_FEATURE_NET_TCONV, -1, 1, 4 * LACE_COND_DIM, output_buffer, 4 * LACE_COND_DIM)) return 0;

  OPUS_COPY(input_buffer, output_buffer, 4 * LACE_COND_DIM);
  for (i_subframe = 0; i_subframe < 4; i_subframe++) {
    compute_generic_gru(
        &hLACE->layers.lace_fnet_gru_input,
        &hLACE->layers.lace_fnet_gru_recurrent,
        state->feature_net_gru_state,
        input_buffer + i_subframe * LACE_COND_DIM,
        arch
    );
    OPUS_COPY(output + i_subframe * LACE_COND_DIM, state->feature_net_gru_state, LACE_COND_DIM);
  }
  return write_trace_record(TRACE_STAGE_FEATURE_NET_LATENT, -1, 1, 4 * LACE_COND_DIM, output, 4 * LACE_COND_DIM);
}

static int trace_lace_adacomb_params(
    int subframe,
    const float *features,
    const LinearLayer *kernel_layer,
    const LinearLayer *gain_layer,
    const LinearLayer *global_gain_layer,
    int kernel_size,
    float filter_gain_a,
    float filter_gain_b,
    float log_gain_limit,
    int arch
) {
  float kernel_buffer[ADACOMB_MAX_KERNEL_SIZE];
  float gains[2];
  float norm;
  float scale;
  int i;

  OPUS_CLEAR(kernel_buffer, ADACOMB_MAX_KERNEL_SIZE);
  OPUS_CLEAR(gains, 2);
  compute_generic_dense(kernel_layer, kernel_buffer, features, ACTIVATION_LINEAR, arch);
  compute_generic_dense(gain_layer, &gains[0], features, ACTIVATION_RELU, arch);
  compute_generic_dense(global_gain_layer, &gains[1], features, ACTIVATION_TANH, arch);
  if (!write_trace_record(TRACE_STAGE_CF1_KERNEL_RAW, subframe, 1, kernel_size, kernel_buffer, kernel_size)) return 0;
  if (!write_trace_record(TRACE_STAGE_CF1_GAINS_RAW, subframe, 1, 2, gains, 2)) return 0;

  gains[0] = exp(log_gain_limit - gains[0]);
  gains[1] = exp(filter_gain_a * gains[1] + filter_gain_b);
  norm = 0;
  for (i = 0; i < kernel_size; i++) {
    norm += kernel_buffer[i] * kernel_buffer[i];
  }
  scale = (1.f / (1e-6f + sqrt(norm))) * gains[0];
  for (i = 0; i < kernel_size; i++) {
    kernel_buffer[i] *= scale;
  }
  if (!write_trace_record(TRACE_STAGE_CF1_KERNEL_SCALED, subframe, 1, kernel_size, kernel_buffer, kernel_size)) return 0;
  return write_trace_record(TRACE_STAGE_CF1_GAINS_SCALED, subframe, 1, 2, gains, 2);
}

static int trace_lace_process_20ms_frame(
    LACE* hLACE,
    LACEState *state,
    float *x_out,
    const float *x_in,
    const float *features,
    const float *numbits,
    const int *periods,
    int arch
) {
  float feature_buffer[4 * LACE_COND_DIM];
  float output_buffer[4 * LACE_FRAME_SIZE];
  float periods_f[4];
  int i_subframe, i_sample;

  for (i_subframe = 0; i_subframe < 4; i_subframe++) {
    periods_f[i_subframe] = (float)periods[i_subframe];
  }

  if (!write_trace_header(0, 31)) return 0;
  if (!write_trace_record(TRACE_STAGE_INPUT, -1, 1, 4 * LACE_FRAME_SIZE, x_in, 4 * LACE_FRAME_SIZE)) return 0;
  if (!write_trace_record(TRACE_STAGE_FEATURES, -1, 1, 4 * LACE_NUM_FEATURES, features, 4 * LACE_NUM_FEATURES)) return 0;
  if (!write_trace_record(TRACE_STAGE_NUMBITS, -1, 1, 2, numbits, 2)) return 0;
  if (!write_trace_record(TRACE_STAGE_PERIODS, -1, 1, 4, periods_f, 4)) return 0;

  for (i_sample = 0; i_sample < 4 * LACE_FRAME_SIZE; i_sample++) {
    output_buffer[i_sample] = x_in[i_sample] - LACE_PREEMPH * state->preemph_mem;
    state->preemph_mem = x_in[i_sample];
  }
  if (!write_trace_record(TRACE_STAGE_PREEMPH, -1, 1, 4 * LACE_FRAME_SIZE, output_buffer, 4 * LACE_FRAME_SIZE)) return 0;

  if (!trace_lace_feature_net(hLACE, state, feature_buffer, features, numbits, periods, arch)) return 0;

  for (i_subframe = 0; i_subframe < 4; i_subframe++) {
    if (!trace_lace_adacomb_params(
        i_subframe,
        feature_buffer + i_subframe * LACE_COND_DIM,
        &hLACE->layers.lace_cf1_kernel,
        &hLACE->layers.lace_cf1_gain,
        &hLACE->layers.lace_cf1_global_gain,
        LACE_CF1_KERNEL_SIZE,
        LACE_CF1_FILTER_GAIN_A,
        LACE_CF1_FILTER_GAIN_B,
        LACE_CF1_LOG_GAIN_LIMIT,
        arch)) return 0;
    adacomb_process_frame(
        &state->cf1_state,
        output_buffer + i_subframe * LACE_FRAME_SIZE,
        output_buffer + i_subframe * LACE_FRAME_SIZE,
        feature_buffer + i_subframe * LACE_COND_DIM,
        &hLACE->layers.lace_cf1_kernel,
        &hLACE->layers.lace_cf1_gain,
        &hLACE->layers.lace_cf1_global_gain,
        periods[i_subframe],
        LACE_COND_DIM,
        LACE_FRAME_SIZE,
        LACE_OVERLAP_SIZE,
        LACE_CF1_KERNEL_SIZE,
        LACE_CF1_LEFT_PADDING,
        LACE_CF1_FILTER_GAIN_A,
        LACE_CF1_FILTER_GAIN_B,
        LACE_CF1_LOG_GAIN_LIMIT,
        hLACE->window,
        arch);
  }
  if (!write_trace_record(TRACE_STAGE_POST_CF1, -1, 1, 4 * LACE_FRAME_SIZE, output_buffer, 4 * LACE_FRAME_SIZE)) return 0;

  for (i_subframe = 0; i_subframe < 4; i_subframe++) {
    adacomb_process_frame(
        &state->cf2_state,
        output_buffer + i_subframe * LACE_FRAME_SIZE,
        output_buffer + i_subframe * LACE_FRAME_SIZE,
        feature_buffer + i_subframe * LACE_COND_DIM,
        &hLACE->layers.lace_cf2_kernel,
        &hLACE->layers.lace_cf2_gain,
        &hLACE->layers.lace_cf2_global_gain,
        periods[i_subframe],
        LACE_COND_DIM,
        LACE_FRAME_SIZE,
        LACE_OVERLAP_SIZE,
        LACE_CF2_KERNEL_SIZE,
        LACE_CF2_LEFT_PADDING,
        LACE_CF2_FILTER_GAIN_A,
        LACE_CF2_FILTER_GAIN_B,
        LACE_CF2_LOG_GAIN_LIMIT,
        hLACE->window,
        arch);
  }
  if (!write_trace_record(TRACE_STAGE_POST_CF2, -1, 1, 4 * LACE_FRAME_SIZE, output_buffer, 4 * LACE_FRAME_SIZE)) return 0;

  for (i_subframe = 0; i_subframe < 4; i_subframe++) {
    adaconv_process_frame(
        &state->af1_state,
        output_buffer + i_subframe * LACE_FRAME_SIZE,
        output_buffer + i_subframe * LACE_FRAME_SIZE,
        feature_buffer + i_subframe * LACE_COND_DIM,
        &hLACE->layers.lace_af1_kernel,
        &hLACE->layers.lace_af1_gain,
        LACE_COND_DIM,
        LACE_FRAME_SIZE,
        LACE_OVERLAP_SIZE,
        LACE_AF1_IN_CHANNELS,
        LACE_AF1_OUT_CHANNELS,
        LACE_AF1_KERNEL_SIZE,
        LACE_AF1_LEFT_PADDING,
        LACE_AF1_FILTER_GAIN_A,
        LACE_AF1_FILTER_GAIN_B,
        LACE_AF1_SHAPE_GAIN,
        hLACE->window,
        arch);
  }
  if (!write_trace_record(TRACE_STAGE_POST_AF1, -1, 1, 4 * LACE_FRAME_SIZE, output_buffer, 4 * LACE_FRAME_SIZE)) return 0;

  for (i_sample = 0; i_sample < 4 * LACE_FRAME_SIZE; i_sample++) {
    x_out[i_sample] = output_buffer[i_sample] + LACE_PREEMPH * state->deemph_mem;
    state->deemph_mem = x_out[i_sample];
  }
  return write_trace_record(TRACE_STAGE_DEEMPH, -1, 1, 4 * LACE_FRAME_SIZE, x_out, 4 * LACE_FRAME_SIZE);
}

/* trace_nolace_process_20ms_frame mirrors dnn/osce.c:nolace_process_20ms_frame
 * verbatim, inserting write_trace_record() checkpoints after each major stage so
 * the gopus NoLACE runtime can be compared stage-by-stage. The arithmetic path
 * is identical to the production routine. */
static int trace_nolace_process_20ms_frame(
    NoLACE* hNoLACE,
    NoLACEState *state,
    float *x_out,
    const float *x_in,
    const float *features,
    const float *numbits,
    const int *periods,
    int arch
) {
  float feature_buffer[4 * NOLACE_COND_DIM];
  float feature_transform_buffer[4 * NOLACE_COND_DIM];
  float x_buffer1[8 * NOLACE_FRAME_SIZE];
  float x_buffer2[8 * NOLACE_FRAME_SIZE];
  float periods_f[4];
  int i_subframe, i_sample;
  NOLACELayers *layers = &hNoLACE->layers;

  for (i_subframe = 0; i_subframe < 4; i_subframe++) {
    periods_f[i_subframe] = (float)periods[i_subframe];
  }

  if (!write_trace_header(1, 16)) return 0;
  if (!write_trace_record(TRACE_STAGE_INPUT, -1, 1, 4 * NOLACE_FRAME_SIZE, x_in, 4 * NOLACE_FRAME_SIZE)) return 0;
  if (!write_trace_record(TRACE_STAGE_FEATURES, -1, 1, 4 * NOLACE_NUM_FEATURES, features, 4 * NOLACE_NUM_FEATURES)) return 0;
  if (!write_trace_record(TRACE_STAGE_NUMBITS, -1, 1, 2, numbits, 2)) return 0;
  if (!write_trace_record(TRACE_STAGE_PERIODS, -1, 1, 4, periods_f, 4)) return 0;

  for (i_sample = 0; i_sample < 4 * NOLACE_FRAME_SIZE; i_sample++) {
    x_buffer1[i_sample] = x_in[i_sample] - NOLACE_PREEMPH * state->preemph_mem;
    state->preemph_mem = x_in[i_sample];
  }
  if (!write_trace_record(TRACE_STAGE_NL_PREEMPH, -1, 1, 4 * NOLACE_FRAME_SIZE, x_buffer1, 4 * NOLACE_FRAME_SIZE)) return 0;

  nolace_feature_net(hNoLACE, state, feature_buffer, features, numbits, periods, arch);
  if (!write_trace_record(TRACE_STAGE_NL_LATENT, -1, 1, 4 * NOLACE_COND_DIM, feature_buffer, 4 * NOLACE_COND_DIM)) return 0;

  for (i_subframe = 0; i_subframe < 4; i_subframe++) {
    adacomb_process_frame(&state->cf1_state,
        x_buffer1 + i_subframe * NOLACE_FRAME_SIZE, x_buffer1 + i_subframe * NOLACE_FRAME_SIZE,
        feature_buffer + i_subframe * NOLACE_COND_DIM,
        &layers->nolace_cf1_kernel, &layers->nolace_cf1_gain, &layers->nolace_cf1_global_gain,
        periods[i_subframe], NOLACE_COND_DIM, NOLACE_FRAME_SIZE, NOLACE_OVERLAP_SIZE,
        NOLACE_CF1_KERNEL_SIZE, NOLACE_CF1_LEFT_PADDING, NOLACE_CF1_FILTER_GAIN_A,
        NOLACE_CF1_FILTER_GAIN_B, NOLACE_CF1_LOG_GAIN_LIMIT, hNoLACE->window, arch);
    compute_generic_conv1d(&layers->nolace_post_cf1,
        feature_transform_buffer + i_subframe * NOLACE_COND_DIM, state->post_cf1_state,
        feature_buffer + i_subframe * NOLACE_COND_DIM, NOLACE_COND_DIM, ACTIVATION_TANH, arch);
  }
  OPUS_COPY(feature_buffer, feature_transform_buffer, 4 * NOLACE_COND_DIM);
  if (!write_trace_record(TRACE_STAGE_NL_POST_CF1, -1, 1, 4 * NOLACE_FRAME_SIZE, x_buffer1, 4 * NOLACE_FRAME_SIZE)) return 0;

  for (i_subframe = 0; i_subframe < 4; i_subframe++) {
    adacomb_process_frame(&state->cf2_state,
        x_buffer1 + i_subframe * NOLACE_FRAME_SIZE, x_buffer1 + i_subframe * NOLACE_FRAME_SIZE,
        feature_buffer + i_subframe * NOLACE_COND_DIM,
        &layers->nolace_cf2_kernel, &layers->nolace_cf2_gain, &layers->nolace_cf2_global_gain,
        periods[i_subframe], NOLACE_COND_DIM, NOLACE_FRAME_SIZE, NOLACE_OVERLAP_SIZE,
        NOLACE_CF2_KERNEL_SIZE, NOLACE_CF2_LEFT_PADDING, NOLACE_CF2_FILTER_GAIN_A,
        NOLACE_CF2_FILTER_GAIN_B, NOLACE_CF2_LOG_GAIN_LIMIT, hNoLACE->window, arch);
    compute_generic_conv1d(&layers->nolace_post_cf2,
        feature_transform_buffer + i_subframe * NOLACE_COND_DIM, state->post_cf2_state,
        feature_buffer + i_subframe * NOLACE_COND_DIM, NOLACE_COND_DIM, ACTIVATION_TANH, arch);
  }
  OPUS_COPY(feature_buffer, feature_transform_buffer, 4 * NOLACE_COND_DIM);
  if (!write_trace_record(TRACE_STAGE_NL_POST_CF2, -1, 1, 4 * NOLACE_FRAME_SIZE, x_buffer1, 4 * NOLACE_FRAME_SIZE)) return 0;

  for (i_subframe = 0; i_subframe < 4; i_subframe++) {
    adaconv_process_frame(&state->af1_state,
        x_buffer2 + i_subframe * NOLACE_FRAME_SIZE * NOLACE_AF1_OUT_CHANNELS,
        x_buffer1 + i_subframe * NOLACE_FRAME_SIZE,
        feature_buffer + i_subframe * NOLACE_COND_DIM,
        &layers->nolace_af1_kernel, &layers->nolace_af1_gain,
        NOLACE_COND_DIM, NOLACE_FRAME_SIZE, NOLACE_OVERLAP_SIZE,
        NOLACE_AF1_IN_CHANNELS, NOLACE_AF1_OUT_CHANNELS, NOLACE_AF1_KERNEL_SIZE,
        NOLACE_AF1_LEFT_PADDING, NOLACE_AF1_FILTER_GAIN_A, NOLACE_AF1_FILTER_GAIN_B,
        NOLACE_AF1_SHAPE_GAIN, hNoLACE->window, arch);
    compute_generic_conv1d(&layers->nolace_post_af1,
        feature_transform_buffer + i_subframe * NOLACE_COND_DIM, state->post_af1_state,
        feature_buffer + i_subframe * NOLACE_COND_DIM, NOLACE_COND_DIM, ACTIVATION_TANH, arch);
  }
  OPUS_COPY(feature_buffer, feature_transform_buffer, 4 * NOLACE_COND_DIM);
  if (!write_trace_record(TRACE_STAGE_NL_POST_AF1, -1, 1, 4 * NOLACE_FRAME_SIZE * NOLACE_AF1_OUT_CHANNELS, x_buffer2, 4 * NOLACE_FRAME_SIZE * NOLACE_AF1_OUT_CHANNELS)) return 0;

  for (i_subframe = 0; i_subframe < 4; i_subframe++) {
    adashape_process_frame(&state->tdshape1_state,
        x_buffer2 + i_subframe * NOLACE_AF1_OUT_CHANNELS * NOLACE_FRAME_SIZE + NOLACE_FRAME_SIZE,
        x_buffer2 + i_subframe * NOLACE_AF1_OUT_CHANNELS * NOLACE_FRAME_SIZE + NOLACE_FRAME_SIZE,
        feature_buffer + i_subframe * NOLACE_COND_DIM,
        &layers->nolace_tdshape1_alpha1_f, &layers->nolace_tdshape1_alpha1_t, &layers->nolace_tdshape1_alpha2,
        NOLACE_TDSHAPE1_FEATURE_DIM, NOLACE_TDSHAPE1_FRAME_SIZE, NOLACE_TDSHAPE1_AVG_POOL_K, 1, arch);
  }
  if (!write_trace_record(TRACE_STAGE_NL_POST_TDSHAPE1, -1, 1, 4 * NOLACE_FRAME_SIZE * NOLACE_AF1_OUT_CHANNELS, x_buffer2, 4 * NOLACE_FRAME_SIZE * NOLACE_AF1_OUT_CHANNELS)) return 0;

  for (i_subframe = 0; i_subframe < 4; i_subframe++) {
    adaconv_process_frame(&state->af2_state,
        x_buffer1 + i_subframe * NOLACE_FRAME_SIZE * NOLACE_AF2_OUT_CHANNELS,
        x_buffer2 + i_subframe * NOLACE_FRAME_SIZE * NOLACE_AF2_IN_CHANNELS,
        feature_buffer + i_subframe * NOLACE_COND_DIM,
        &layers->nolace_af2_kernel, &layers->nolace_af2_gain,
        NOLACE_COND_DIM, NOLACE_FRAME_SIZE, NOLACE_OVERLAP_SIZE,
        NOLACE_AF2_IN_CHANNELS, NOLACE_AF2_OUT_CHANNELS, NOLACE_AF2_KERNEL_SIZE,
        NOLACE_AF2_LEFT_PADDING, NOLACE_AF2_FILTER_GAIN_A, NOLACE_AF2_FILTER_GAIN_B,
        NOLACE_AF2_SHAPE_GAIN, hNoLACE->window, arch);
    compute_generic_conv1d(&layers->nolace_post_af2,
        feature_transform_buffer + i_subframe * NOLACE_COND_DIM, state->post_af2_state,
        feature_buffer + i_subframe * NOLACE_COND_DIM, NOLACE_COND_DIM, ACTIVATION_TANH, arch);
  }
  OPUS_COPY(feature_buffer, feature_transform_buffer, 4 * NOLACE_COND_DIM);
  if (!write_trace_record(TRACE_STAGE_NL_POST_AF2, -1, 1, 4 * NOLACE_FRAME_SIZE * NOLACE_AF2_OUT_CHANNELS, x_buffer1, 4 * NOLACE_FRAME_SIZE * NOLACE_AF2_OUT_CHANNELS)) return 0;

  for (i_subframe = 0; i_subframe < 4; i_subframe++) {
    adashape_process_frame(&state->tdshape2_state,
        x_buffer1 + i_subframe * NOLACE_AF2_OUT_CHANNELS * NOLACE_FRAME_SIZE + NOLACE_FRAME_SIZE,
        x_buffer1 + i_subframe * NOLACE_AF2_OUT_CHANNELS * NOLACE_FRAME_SIZE + NOLACE_FRAME_SIZE,
        feature_buffer + i_subframe * NOLACE_COND_DIM,
        &layers->nolace_tdshape2_alpha1_f, &layers->nolace_tdshape2_alpha1_t, &layers->nolace_tdshape2_alpha2,
        NOLACE_TDSHAPE2_FEATURE_DIM, NOLACE_TDSHAPE2_FRAME_SIZE, NOLACE_TDSHAPE2_AVG_POOL_K, 1, arch);
  }
  if (!write_trace_record(TRACE_STAGE_NL_POST_TDSHAPE2, -1, 1, 4 * NOLACE_FRAME_SIZE * NOLACE_AF2_OUT_CHANNELS, x_buffer1, 4 * NOLACE_FRAME_SIZE * NOLACE_AF2_OUT_CHANNELS)) return 0;

  for (i_subframe = 0; i_subframe < 4; i_subframe++) {
    adaconv_process_frame(&state->af3_state,
        x_buffer2 + i_subframe * NOLACE_FRAME_SIZE * NOLACE_AF3_OUT_CHANNELS,
        x_buffer1 + i_subframe * NOLACE_FRAME_SIZE * NOLACE_AF3_IN_CHANNELS,
        feature_buffer + i_subframe * NOLACE_COND_DIM,
        &layers->nolace_af3_kernel, &layers->nolace_af3_gain,
        NOLACE_COND_DIM, NOLACE_FRAME_SIZE, NOLACE_OVERLAP_SIZE,
        NOLACE_AF3_IN_CHANNELS, NOLACE_AF3_OUT_CHANNELS, NOLACE_AF3_KERNEL_SIZE,
        NOLACE_AF3_LEFT_PADDING, NOLACE_AF3_FILTER_GAIN_A, NOLACE_AF3_FILTER_GAIN_B,
        NOLACE_AF3_SHAPE_GAIN, hNoLACE->window, arch);
    compute_generic_conv1d(&layers->nolace_post_af3,
        feature_transform_buffer + i_subframe * NOLACE_COND_DIM, state->post_af3_state,
        feature_buffer + i_subframe * NOLACE_COND_DIM, NOLACE_COND_DIM, ACTIVATION_TANH, arch);
  }
  OPUS_COPY(feature_buffer, feature_transform_buffer, 4 * NOLACE_COND_DIM);
  if (!write_trace_record(TRACE_STAGE_NL_POST_AF3, -1, 1, 4 * NOLACE_FRAME_SIZE * NOLACE_AF3_OUT_CHANNELS, x_buffer2, 4 * NOLACE_FRAME_SIZE * NOLACE_AF3_OUT_CHANNELS)) return 0;

  for (i_subframe = 0; i_subframe < 4; i_subframe++) {
    adashape_process_frame(&state->tdshape3_state,
        x_buffer2 + i_subframe * NOLACE_AF3_OUT_CHANNELS * NOLACE_FRAME_SIZE + NOLACE_FRAME_SIZE,
        x_buffer2 + i_subframe * NOLACE_AF3_OUT_CHANNELS * NOLACE_FRAME_SIZE + NOLACE_FRAME_SIZE,
        feature_buffer + i_subframe * NOLACE_COND_DIM,
        &layers->nolace_tdshape3_alpha1_f, &layers->nolace_tdshape3_alpha1_t, &layers->nolace_tdshape3_alpha2,
        NOLACE_TDSHAPE3_FEATURE_DIM, NOLACE_TDSHAPE3_FRAME_SIZE, NOLACE_TDSHAPE3_AVG_POOL_K, 1, arch);
  }
  if (!write_trace_record(TRACE_STAGE_NL_POST_TDSHAPE3, -1, 1, 4 * NOLACE_FRAME_SIZE * NOLACE_AF3_OUT_CHANNELS, x_buffer2, 4 * NOLACE_FRAME_SIZE * NOLACE_AF3_OUT_CHANNELS)) return 0;

  for (i_subframe = 0; i_subframe < 4; i_subframe++) {
    adaconv_process_frame(&state->af4_state,
        x_buffer1 + i_subframe * NOLACE_FRAME_SIZE * NOLACE_AF4_OUT_CHANNELS,
        x_buffer2 + i_subframe * NOLACE_FRAME_SIZE * NOLACE_AF4_IN_CHANNELS,
        feature_buffer + i_subframe * NOLACE_COND_DIM,
        &layers->nolace_af4_kernel, &layers->nolace_af4_gain,
        NOLACE_COND_DIM, NOLACE_FRAME_SIZE, NOLACE_OVERLAP_SIZE,
        NOLACE_AF4_IN_CHANNELS, NOLACE_AF4_OUT_CHANNELS, NOLACE_AF4_KERNEL_SIZE,
        NOLACE_AF4_LEFT_PADDING, NOLACE_AF4_FILTER_GAIN_A, NOLACE_AF4_FILTER_GAIN_B,
        NOLACE_AF4_SHAPE_GAIN, hNoLACE->window, arch);
  }
  if (!write_trace_record(TRACE_STAGE_NL_POST_AF4, -1, 1, 4 * NOLACE_FRAME_SIZE * NOLACE_AF4_OUT_CHANNELS, x_buffer1, 4 * NOLACE_FRAME_SIZE * NOLACE_AF4_OUT_CHANNELS)) return 0;

  for (i_sample = 0; i_sample < 4 * NOLACE_FRAME_SIZE; i_sample++) {
    x_out[i_sample] = x_buffer1[i_sample] + NOLACE_PREEMPH * state->deemph_mem;
    state->deemph_mem = x_out[i_sample];
  }
  return write_trace_record(TRACE_STAGE_NL_DEEMPH, -1, 1, 4 * NOLACE_FRAME_SIZE, x_out, 4 * NOLACE_FRAME_SIZE);
}

int main(int argc, char *argv[]) {
  int num_samples = 320;
  if (argc >= 2) {
    num_samples = atoi(argv[1]);
  }
  if (num_samples != 320) {
    fprintf(stderr, "NUM_SAMPLES_16K must be 320 (got %d)\n", num_samples);
    return 2;
  }

  const char *mode_env = getenv("MODE");
  if (mode_env == NULL || mode_env[0] == '\0') {
    mode_env = "lace";
  }
  int mode_id;
  if (strcmp(mode_env, "lace") == 0) {
    mode_id = 0;
  } else if (strcmp(mode_env, "nolace") == 0) {
    mode_id = 1;
  } else {
    fprintf(stderr, "MODE must be 'lace' or 'nolace' (got '%s')\n", mode_env);
    return 2;
  }

  if (!set_binary_stdio()) {
    fprintf(stderr, "failed to set binary stdout mode\n");
    return 1;
  }

  /* Generate the same 1 kHz sinusoid the gopus side uses. */
  static float x_in[320];
  fill_sinusoid_float(x_in, num_samples, 1000.0, 0.5);

  /* Features (4 subframes * 93), numbits (2), periods (4). The parity probe
   * uses zero features + zero numbits + a small non-zero period so the
   * AdaComb stages exercise their pitch-lag path on both sides. */
  static float features[4 * 93];
  static float numbits[2];
  static int   periods[4];
  for (int i = 0; i < 4 * 93; i++) features[i] = 0.0f;
  numbits[0] = 0.0f;
  numbits[1] = 0.0f;
  for (int i = 0; i < 4; i++) periods[i] = 60;

  /* Load default-shipped OSCE models from the libopus built-in tables. */
  OSCEModel *model = (OSCEModel *)calloc(1, sizeof(OSCEModel));
  if (model == NULL) {
    fprintf(stderr, "calloc OSCEModel failed\n");
    return 1;
  }
  if (_helper_osce_load_models(model, NULL, 0) != 0) {
    fprintf(stderr, "_helper_osce_load_models failed (built-in tables unavailable?)\n");
    free(model);
    return 1;
  }
  model->loaded = 1;

  static float x_out[320];
  memset(x_out, 0, sizeof(x_out));

  if (mode_id == 0) {
    /* LACE */
    LACEState state;
    memset(&state, 0, sizeof(state));
    /* lace_process_20ms_frame expects an initialised AdaComb / AdaConv state.
     * reset_lace_state zeroes the AdaComb state and pre/de-emphasis memories
     * (mirroring what the decoder's osce_reset() would do at frame boundary). */
    reset_lace_state(&state);
    const char *trace_env = getenv("TRACE");
    if (trace_env != NULL && strcmp(trace_env, "1") == 0) {
      int ok = trace_lace_process_20ms_frame(&model->lace, &state, x_out, x_in,
                                             features, numbits, periods, 0 /*arch=GENERIC*/);
      free(model);
      return ok ? 0 : 1;
    }
    lace_process_20ms_frame(&model->lace, &state, x_out, x_in,
                            features, numbits, periods, 0 /*arch=GENERIC*/);
  } else {
    /* NoLACE */
    NoLACEState state;
    memset(&state, 0, sizeof(state));
    reset_nolace_state(&state);
    const char *trace_env = getenv("TRACE");
    if (trace_env != NULL && strcmp(trace_env, "1") == 0) {
      int ok = trace_nolace_process_20ms_frame(&model->nolace, &state, x_out, x_in,
                                               features, numbits, periods, 0 /*arch=GENERIC*/);
      free(model);
      return ok ? 0 : 1;
    }
    nolace_process_20ms_frame(&model->nolace, &state, x_out, x_in,
                              features, numbits, periods, 0 /*arch=GENERIC*/);
  }

  /* Emit header + binary payload. */
  static const char tag[8] = {'O','S','C','E','L','A','C','\0'};
  if (fwrite(tag, 1, sizeof(tag), stdout) != sizeof(tag)) goto write_err;
  int32_t hdr[3];
  hdr[0] = 1;
  hdr[1] = mode_id;
  hdr[2] = num_samples;
  if (fwrite(hdr, sizeof(int32_t), 3, stdout) != 3) goto write_err;
  if (fwrite(x_out, sizeof(float), (size_t)num_samples, stdout) != (size_t)num_samples) goto write_err;

  free(model);
  return 0;
write_err:
  fprintf(stderr, "stdout write failed\n");
  free(model);
  return 1;
}
