/* Direct selected-libopus QEXT quant_all_bands encoder oracle.
   Both sides receive the same float32-normalised extension bins and coder
   controls. The returned packet includes range-coded and raw end bits. */
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

#include "opus.h"
#include "celt.h"
#include "modes.h"
#include "bands.h"
#include "entenc.h"
#include "cpu_support.h"

static int read_exact(void *dst, size_t n) { return fread(dst, 1, n, stdin) == n; }
static int write_exact(const void *src, size_t n) { return fwrite(src, 1, n, stdout) == n; }

static uint32_t read_u32(void) {
  unsigned char b[4];
  if (!read_exact(b, 4)) exit(1);
  return (uint32_t)b[0] | ((uint32_t)b[1] << 8) | ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
}

static int write_u32(uint32_t v) {
  unsigned char b[4] = {(unsigned char)v, (unsigned char)(v >> 8),
                        (unsigned char)(v >> 16), (unsigned char)(v >> 24)};
  return write_exact(b, 4);
}

int main(void) {
#ifdef _WIN32
  _setmode(_fileno(stdin), _O_BINARY);
  _setmode(_fileno(stdout), _O_BINARY);
#endif
  char magic[4];
  if (!read_exact(magic, 4) || memcmp(magic, "GQCI", 4) != 0 || read_u32() != 1) return 1;
  int LM = (int)read_u32();
  int storage = (int)read_u32();
  int balance = (int)read_u32();
  opus_uint32 seed = read_u32();
  int count = (int)read_u32();
  if (LM < 0 || LM > 2 || storage < 1 || storage > 256 || count < 1 || count > 80) return 1;

  int err = OPUS_OK;
  CELTMode *mode = opus_custom_mode_create(48000, 960, &err);
  if (mode == NULL || err != OPUS_OK) return 1;
  CELTMode qext;
  compute_qext_mode(&qext, mode);
  const int M = 1 << LM;
  const int N = qext.shortMdctSize * M;
  const int start_bin = qext.eBands[0] * M;
  const int stop_bin = qext.eBands[2] * M;
  if (count != stop_bin - start_bin || N > 480 || qext.nbEBands != NB_QEXT_BANDS) return 1;

  celt_norm X[480] = {0};
  for (int i = start_bin; i < stop_bin; i++) {
    uint32_t bits = read_u32();
    memcpy(&X[i], &bits, sizeof(bits));
  }
  celt_ener bandE[NB_QEXT_BANDS] = {0};
  for (int i = 0; i < 2; i++) {
    uint32_t bits = read_u32();
    memcpy(&bandE[i], &bits, sizeof(bits));
  }

  int pulses[NB_QEXT_BANDS] = {0};
  int zeros[NB_QEXT_BANDS] = {0};
  int tf_res[NB_QEXT_BANDS] = {0};
  unsigned char collapse[NB_QEXT_BANDS] = {0};
  unsigned char packet[256] = {0};
  ec_enc enc, dummy;
  ec_enc_init(&enc, packet, storage);
  ec_enc_init(&dummy, NULL, 0);
  quant_all_bands(1, &qext, 0, 2, X, NULL, collapse, bandE, pulses,
                  0, SPREAD_NORMAL, 0, 0, tf_res, storage * (8 << BITRES),
                  balance, &enc, LM, 2, &seed, 10, opus_select_arch(), 0,
                  &dummy, zeros, 0, NULL);
  opus_uint32 final_range = enc.rng;
  opus_uint32 tell_frac = ec_tell_frac(&enc);
  ec_enc_done(&enc);

  if (!write_exact("GQCO", 4) || !write_u32(1) || !write_u32((uint32_t)storage) ||
      !write_exact(packet, (size_t)storage) || !write_u32(final_range) ||
      !write_u32(tell_frac) || !write_u32(seed) ||
      !write_u32(collapse[0]) || !write_u32(collapse[1])) return 1;
  return 0;
}
