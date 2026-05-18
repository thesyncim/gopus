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
 *   int32 mode_id        (0 = LACE, 1 = NoLACE)
 *   int32 num_out_samples (== NUM_SAMPLES_16K, == 320)
 *   float32[num_out_samples] x_out (16 kHz, float in [-1, 1])
 */

#include <math.h>
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
    lace_process_20ms_frame(&model->lace, &state, x_out, x_in,
                            features, numbits, periods, 0 /*arch=GENERIC*/);
  } else {
    /* NoLACE */
    NoLACEState state;
    memset(&state, 0, sizeof(state));
    reset_nolace_state(&state);
    nolace_process_20ms_frame(&model->nolace, &state, x_out, x_in,
                              features, numbits, periods, 0 /*arch=GENERIC*/);
  }

  /* Emit header + binary payload. */
  static const char tag[8] = {'O','S','C','E','L','A','C','\0'};
  if (fwrite(tag, 1, sizeof(tag), stdout) != sizeof(tag)) goto write_err;
  int32_t hdr[2];
  hdr[0] = mode_id;
  hdr[1] = num_samples;
  if (fwrite(hdr, sizeof(int32_t), 2, stdout) != 2) goto write_err;
  if (fwrite(x_out, sizeof(float), (size_t)num_samples, stdout) != (size_t)num_samples) goto write_err;

  free(model);
  return 0;
write_err:
  fprintf(stderr, "stdout write failed\n");
  free(model);
  return 1;
}
