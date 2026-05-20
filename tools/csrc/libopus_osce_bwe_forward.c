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

/* Pull in osce_structs/osce_features for the BBWENET configuration and the
 * public feature-extractor symbol. */
#include "nnet.h"
#include "osce_config.h"
#include "osce_structs.h"
#include "osce_features.h"
#include "bbwenet_data.h"

/* Pull in the pinned OSCE implementation under helper-local public names so
 * this test helper can call static BBWENet routines without colliding with
 * libopus.a's exported OSCE symbols. */
#define osce_reset gopus_helper_osce_reset
#define osce_bwe_reset gopus_helper_osce_bwe_reset
#define osce_load_models gopus_helper_osce_load_models
#define osce_bwe gopus_helper_osce_bwe
#define osce_enhance_frame gopus_helper_osce_enhance_frame
#include "osce.c"
#undef osce_reset
#undef osce_bwe_reset
#undef osce_load_models
#undef osce_bwe
#undef osce_enhance_frame

/* bbwenet_process_frames is static in osce.c. The helper-local symbol renames
 * above let this file expose both reference paths: public `osce_bwe` wrapper
 * modes, which return delayed/int16 PCM, and raw BBWENet modes, which return
 * the float output before libopus applies the public wrapper delay.
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

/* osce_bwe_cross_fade_10ms is exported from libopus dnn/osce_features.c when
 * the build is configured with --enable-osce-bwe. The signature matches the
 * upstream header. */
extern void osce_bwe_cross_fade_10ms(
    int16_t *x_fadein,
    int16_t *x_fadeout,
    int length);

static void fill_sinusoid(int16_t *out, int num_samples, double freq_hz, double amp) {
  for (int i = 0; i < num_samples; i++) {
    double v = amp * sin(2.0 * M_PI * freq_hz * (double)i / 16000.0);
    long q = lrint(v * 32767.0);
    if (q > 32767) q = 32767;
    if (q < -32768) q = -32768;
    out[i] = (int16_t)q;
  }
}

static void scale_s16_to_float(float *out, const int16_t *in, int num_samples) {
  for (int i = 0; i < num_samples; i++) {
    out[i] = ((float)in[i]) * (1.0f / 32768.0f);
  }
}

static void bbwenet_raw_forward(
    OSCEModel *model,
    BBWENetState *state,
    float *x_out,
    const int16_t *xq16,
    const float *features,
    int num_samples) {
  float in_buffer[320];
  scale_s16_to_float(in_buffer, xq16, num_samples);
  bbwenet_process_frames(
      &model->bbwenet,
      state,
      x_out,
      in_buffer,
      features,
      num_samples / 160,
      0 /*arch=GENERIC*/);
}

int main(int argc, char *argv[]) {
  if (argc < 2) {
    fprintf(stderr, "usage: %s NUM_SAMPLES_16K [MODE]\n"
                    "  MODE: forward (default), consecutive, crossfade, raw, raw-consecutive\n", argv[0]);
    return 2;
  }
  int num_samples = atoi(argv[1]);
  if (num_samples != 160 && num_samples != 320) {
    fprintf(stderr, "NUM_SAMPLES_16K must be 160 or 320 (got %d)\n", num_samples);
    return 2;
  }
  const char *mode = (argc >= 3) ? argv[2] : "forward";
  int num_frames = num_samples / 160;
  int num_subframes = 2 * num_frames;
  int num_out = 3 * num_samples;

  if (!set_binary_stdio()) {
    fprintf(stderr, "failed to set binary stdout mode\n");
    return 1;
  }

  /* Generate the same 1 kHz sinusoid that the gopus side uses. */
  static int16_t xq16[320];
  fill_sinusoid(xq16, num_samples, 1000.0, 0.5);

  /* Load default-shipped OSCE models from the libopus built-in tables. */
  OSCEModel *model = (OSCEModel *)calloc(1, sizeof(OSCEModel));
  if (model == NULL) {
    fprintf(stderr, "calloc OSCEModel failed\n");
    return 1;
  }
  if (gopus_helper_osce_load_models(model, NULL, 0) != 0) {
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
  static int16_t xq48_second[3 * 320];
  static float xq48_raw_emit[3 * 320];
  int emit_raw = 0;

  if (strcmp(mode, "consecutive") == 0) {
    /* Two consecutive BWE calls to capture per-frame state continuity (the
     * scenario the PLC path triggers: a good SILK WB frame followed by a
     * concealed SILK WB frame both invoke osce_bwe in succession on the same
     * BWE state). We emit the SECOND frame's features and output so the
     * gopus side can compare its second-frame output against the libopus
     * second-frame output.
     *
     * Both frames use the same sinusoid input -- this isolates the
     * state-continuity issue from input-variability noise. */
    gopus_helper_osce_bwe(model, &bweState, xq48, xq16, num_samples, 0 /*arch=GENERIC*/);
    /* Snapshot features for the second frame using a separate feature state
     * that has consumed the first frame already. */
    osce_bwe_calculate_features(&featState.features, features, xq16, num_samples);
    /* Second frame -- bweState carries over signal_history, last_spec, etc. */
    gopus_helper_osce_bwe(model, &bweState, xq48_second, xq16, num_samples, 0 /*arch=GENERIC*/);
    /* Use the second-frame output as the canonical xq48 emitted below. */
    memcpy(xq48, xq48_second, sizeof(xq48));
  } else if (strcmp(mode, "crossfade") == 0) {
    /* Direct osce_bwe_cross_fade_10ms test. We synthesise two distinguishable
     * fade-in/fade-out signals at 48 kHz (since the function operates in the
     * 48 kHz output domain) and verify the gopus port produces the same
     * crossfade result. The features and the BWE pass are not used here; we
     * leave them populated with the forward-mode output for diagnostic
     * compatibility with the header parser, but mark numOut = 480 (10 ms
     * @ 48 kHz) which is the natural cross-fade window length. */
    gopus_helper_osce_bwe(model, &bweState, xq48, xq16, num_samples, 0 /*arch=GENERIC*/);
    num_out = 480;
    /* Generate two distinct ramps and emit the crossfaded result. */
    int16_t fadein[480];
    int16_t fadeout[480];
    for (int i = 0; i < 480; i++) {
      /* Triangle ramp for fadein (rising), sawtooth for fadeout (falling).
       * Scaled to ~24kFS to leave headroom. */
      long fi = (long)((i * 24000L) / 480L) - 12000L; /* -12000..+12000 */
      long fo = (long)(12000L - ((i * 24000L) / 480L)); /* +12000..-12000 */
      if (fi > 32767) fi = 32767; else if (fi < -32768) fi = -32768;
      if (fo > 32767) fo = 32767; else if (fo < -32768) fo = -32768;
      fadein[i] = (int16_t)fi;
      fadeout[i] = (int16_t)fo;
    }
    osce_bwe_cross_fade_10ms(fadein, fadeout, 480);
    memcpy(xq48, fadein, sizeof(fadein));
  } else if (strcmp(mode, "raw") == 0) {
    bbwenet_raw_forward(model, &bweState.state.bbwenet, xq48_raw_emit, xq16, features, num_samples);
    num_out = 3 * num_samples;
    emit_raw = 1;
  } else if (strcmp(mode, "raw-consecutive") == 0) {
    static float xq48_raw[3 * 320];
    bbwenet_raw_forward(model, &bweState.state.bbwenet, xq48_raw, xq16, features, num_samples);
    osce_bwe_calculate_features(&featState.features, features, xq16, num_samples);
    bbwenet_raw_forward(model, &bweState.state.bbwenet, xq48_raw_emit, xq16, features, num_samples);
    num_out = 3 * num_samples;
    emit_raw = 1;
  } else {
    /* Default "forward" mode: single BWE pass on the sinusoid. */
    gopus_helper_osce_bwe(model, &bweState, xq48, xq16, num_samples, 0 /*arch=GENERIC*/);
  }

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
  static float xq48f[3 * 320];
  if (emit_raw) {
    memcpy(xq48f, xq48_raw_emit, (size_t)num_out * sizeof(float));
  } else {
    /* Convert int16 PCM to float in [-1, 1] for comparison with the
     * gopus runtime, which works in normalised float space. */
    for (int i = 0; i < num_out; i++) {
      xq48f[i] = (float)xq48[i] * (1.0f / 32768.0f);
    }
  }
  if (fwrite(xq48f, sizeof(float), num_out, stdout) != (size_t)num_out) goto write_err;

  free(model);
  return 0;
write_err:
  fprintf(stderr, "stdout write failed\n");
  free(model);
  return 1;
}
