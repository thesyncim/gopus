/*
 * libopus_osce_bwe_forward.c
 *
 * Drives the libopus OSCE BWE forward pass for a single 10/20 ms 16 kHz
 * sinusoidal input and emits the resulting 48 kHz float32 PCM (plus the
 * 114-float-per-10ms feature vectors that libopus computes from the same
 * input) to stdout, prefixed with a small ASCII header describing the
 * payload shape.
 *
 * USAGE:
 *   libopus_osce_bwe_forward NUM_SAMPLES_16K
 *
 * Where NUM_SAMPLES_16K is 160 (10 ms) or 320 (20 ms). A 1 kHz sinusoid is
 * generated internally (matching the gopus side test signal) so the helper
 * is deterministic and does not depend on a fixture file.
 *
 * The helper links against the OSCE-enabled libopus build
 * (`--enable-osce --enable-osce-bwe`), which exposes the otherwise-quarantined
 * `bbwenet_process_frames` / `osce_bwe_calculate_features` symbols. Because
 * `bbwenet_process_frames` is `static` inside `dnn/osce.c`, the helper
 * includes osce.c directly (via #include) so the symbol is in the helper's
 * translation unit. The supporting layers (BBWENETLayers) and feature
 * extraction (`osce_bwe_calculate_features`) come from libopus.a.
 *
 * Output format on stdout (binary):
 *   8-byte ASCII tag "OSCEBWE\0"
 *   int32 num_frames (1 for 160, 2 for 320)
 *   int32 num_subframes (== 2 * num_frames)
 *   int32 num_out_samples (== 3 * NUM_SAMPLES_16K)
 *   float32[num_frames * 114] features
 *   float32[num_out_samples] x_out (48 kHz)
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

/* Pull in osce_structs/osce_features for the BBWENET configuration and the
 * public feature-extractor symbol. */
#include "nnet.h"
#include "osce_config.h"
#include "osce_structs.h"
#include "osce_features.h"
#include "bbwenet_data.h"

/* bbwenet_process_frames is static in osce.c. Build the helper with the
 * matching libopus source TU directly so we can call it. We must not
 * recursively include the helpers that are already shipped in libopus.a
 * (init_bbwenet, reset_bbwenet_state, osce_bwe, ...) -- the linker would
 * complain about duplicate symbols. The path used here matches the same
 * source tree used to build libopus.a; the helper takes only the static
 * `bbwenet_process_frames` (and its inline-able dependencies in nndsp.c
 * which the libopus build re-emits as extern symbols). To avoid the duplicate
 * symbol problem we compile osce.c with extra #defines that suppress the
 * non-static API surface; if any conflict remains we fall back to bundling
 * a thin reimplementation that wraps the static helpers via `osce_bwe`.
 *
 * In practice, calling libopus's public `osce_bwe(...)` already produces the
 * same x_out (the output is just routed through an extra int16 quantisation
 * for the BBWENET output delay buffer), so we use the public symbol instead
 * of fishing the static helper out of osce.c. The trade-off is that the
 * helper output is int16-quantised PCM, not raw float, so the gopus side
 * comparison must also quantise to int16 (or we lose <= 1 LSB of precision).
 */

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) {
    return 0;
  }
#endif
  return 1;
}

#ifndef ENABLE_OSCE_BWE
#error "libopus_osce_bwe_forward.c requires libopus built with --enable-osce --enable-osce-bwe"
#endif

extern int osce_load_models(OSCEModel *model, const void *data, int len);
extern void osce_bwe(
    OSCEModel *model,
    silk_OSCE_BWE_struct *psOSCEBWE,
    int16_t xq48[],
    int16_t xq16[],
    int32_t xq16_len,
    int arch);

int main(int argc, char *argv[]) {
  if (argc < 2) {
    fprintf(stderr, "usage: %s NUM_SAMPLES_16K\n", argv[0]);
    return 2;
  }
  int num_samples = atoi(argv[1]);
  if (num_samples != 160 && num_samples != 320) {
    fprintf(stderr, "NUM_SAMPLES_16K must be 160 or 320 (got %d)\n", num_samples);
    return 2;
  }
  int num_frames = num_samples / 160;
  int num_subframes = 2 * num_frames;
  int num_out = 3 * num_samples;

  if (!set_binary_stdio()) {
    fprintf(stderr, "failed to set binary stdout mode\n");
    return 1;
  }

  /* Generate the same 1 kHz sinusoid that the gopus side uses. */
  static int16_t xq16[320];
  for (int i = 0; i < num_samples; i++) {
    double v = 0.5 * sin(2.0 * M_PI * 1000.0 * (double)i / 16000.0);
    long q = lrint(v * 32767.0);
    if (q > 32767) q = 32767;
    if (q < -32768) q = -32768;
    xq16[i] = (int16_t)q;
  }

  /* Load default-shipped OSCE models from the libopus built-in tables. */
  OSCEModel *model = (OSCEModel *)calloc(1, sizeof(OSCEModel));
  if (model == NULL) {
    fprintf(stderr, "calloc OSCEModel failed\n");
    return 1;
  }
  if (osce_load_models(model, NULL, 0) != 0) {
    fprintf(stderr, "osce_load_models failed (built-in tables unavailable?)\n");
    free(model);
    return 1;
  }

  /* Compute features through libopus directly; this also serves as the
   * reference feature vector the gopus pure-Go feature extractor must match. */
  silk_OSCE_BWE_struct featState;
  memset(&featState, 0, sizeof(featState));
  /* libopus python-style init: 1e-9 priming of last_spec real bins. */
  for (int k = 0; k <= OSCE_BWE_MAX_INSTAFREQ_BIN; k++) {
    featState.features.last_spec[2*k] = 1e-9f;
  }
  static float features[2 * OSCE_BWE_FEATURE_DIM];
  /* osce_bwe_calculate_features mutates psFeatures (signal_history /
   * last_spec) but we only need a snapshot of the resulting features. */
  osce_bwe_calculate_features(&featState.features, features, xq16, num_samples);

  /* Now run the full forward pass through libopus's public entry point.
   * osce_bwe re-initialises its own feature state from a zeroed struct,
   * mirroring `osce_bwe_reset(...)` -- which means we must zero psOSCEBWE
   * before calling it so the call is independent from the features snapshot
   * above. */
  silk_OSCE_BWE_struct bweState;
  memset(&bweState, 0, sizeof(bweState));
  /* Same python-style init libopus performs in osce_bwe_reset. */
  for (int k = 0; k <= OSCE_BWE_MAX_INSTAFREQ_BIN; k++) {
    bweState.features.last_spec[2*k] = 1e-9f;
  }

  static int16_t xq48[3 * 320];
  osce_bwe(model, &bweState, xq48, xq16, num_samples, 0 /*arch=GENERIC*/);

  /* Emit header + binary payload. */
  static const char tag[8] = {'O','S','C','E','B','W','E','\0'};
  if (fwrite(tag, 1, sizeof(tag), stdout) != sizeof(tag)) goto write_err;
  int32_t hdr[3];
  hdr[0] = num_frames;
  hdr[1] = num_subframes;
  hdr[2] = num_out;
  if (fwrite(hdr, sizeof(int32_t), 3, stdout) != 3) goto write_err;
  if (fwrite(features, sizeof(float), num_frames * OSCE_BWE_FEATURE_DIM, stdout)
      != (size_t)(num_frames * OSCE_BWE_FEATURE_DIM)) goto write_err;
  /* Convert int16 PCM to float in [-1, 1] for comparison with the
   * gopus runtime, which works in normalised float space. */
  static float xq48f[3 * 320];
  for (int i = 0; i < num_out; i++) {
    xq48f[i] = (float)xq48[i] * (1.0f / 32768.0f);
  }
  if (fwrite(xq48f, sizeof(float), num_out, stdout) != (size_t)num_out) goto write_err;

  free(model);
  return 0;
write_err:
  fprintf(stderr, "stdout write failed\n");
  free(model);
  return 1;
}
