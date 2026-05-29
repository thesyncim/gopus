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

#include "freq.h"
#include "kiss_fft.h"
#include "arch.h"
#include "mathops.h"

#define INPUT_MAGIC "GLAI"
#define OUTPUT_MAGIC "GLAO"

/* Per-stage parity oracle for the LPCNet analysis frontend.
 *
 * Input: one raw pre-window analysis buffer x[WINDOW_SIZE] (analysis_mem
 * followed by the current frame, exactly as frame_analysis() builds it before
 * apply_window()).
 *
 * Output, in order:
 *   - windowed x[WINDOW_SIZE]          (apply_window)
 *   - X[FREQ_SIZE] complex             (forward_transform)
 *   - Ex[NB_BANDS]                     (lpcn_compute_band_energy)
 *   - Ly[NB_BANDS]                     (log10 + follow/logMax shaping)
 *   - features[NB_BANDS] (dct, with [0]-=4)
 *   - lpc[LPC_ORDER]                   (lpc_from_cepstrum)
 *
 * This isolates which sub-stage first diverges from the arm64 reference.
 */

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

#define celt_log10(x) (0.3010299957f*celt_log2(x))

int main(void) {
  char magic[4];
  uint32_t version;
  float x[WINDOW_SIZE];
  kiss_fft_cpx X[FREQ_SIZE];
  float Ex[NB_BANDS];
  float Ly[NB_BANDS];
  float features[NB_BANDS];
  float lpc[LPC_ORDER];
  float follow, logMax;
  int i;

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
  if (!read_bits_array(x, WINDOW_SIZE)) {
    fprintf(stderr, "failed to read analysis buffer\n");
    return 1;
  }

  apply_window(x);
  forward_transform(X, x);
  lpcn_compute_band_energy(Ex, X);

  logMax = -2;
  follow = -2;
  for (i = 0; i < NB_BANDS; i++) {
    Ly[i] = celt_log10(1e-2f + Ex[i]);
    Ly[i] = MAX16(logMax - 8, MAX16(follow - 2.5f, Ly[i]));
    logMax = MAX16(logMax, Ly[i]);
    follow = MAX16(follow - 2.5f, Ly[i]);
  }
  dct(features, Ly);
  features[0] -= 4;
  lpc_from_cepstrum(lpc, features);

  if (!write_exact(OUTPUT_MAGIC, 4) ||
      !write_exact(&version, sizeof(version))) {
    fprintf(stderr, "failed to write header\n");
    return 1;
  }
  if (!write_bits_array(x, WINDOW_SIZE) ||
      !write_complex_array(X, FREQ_SIZE) ||
      !write_bits_array(Ex, NB_BANDS) ||
      !write_bits_array(Ly, NB_BANDS) ||
      !write_bits_array(features, NB_BANDS) ||
      !write_bits_array(lpc, LPC_ORDER)) {
    fprintf(stderr, "failed to write output\n");
    return 1;
  }
  return 0;
}
