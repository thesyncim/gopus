#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "celt/arch.h"
#include "celt/entenc.h"
#include "celt/modes.h"
#include "celt/bands.h"
#include "opus_custom.h"

/* Oracle helper for the libopus FIXED_POINT band-shape encode (quant_all_bands,
 * encode side, QEXT off). It runs the real quant_all_bands(1, ...) over a fresh
 * range encoder, using the static 48000/960 CELTMode, on a caller-supplied
 * normalized celt_norm X[] (and stereo Y[]) plus band energies and allocation,
 * and dumps the coded bytes, the post-encode X[]/Y[], the collapse_masks[] and
 * the threaded LCG seed.
 *
 * Both the gopus integer port and this oracle start a fresh range encoder over
 * an identical-sized buffer, so the comparison is decoupled from the
 * surrounding encoder state. */

#define INPUT_MAGIC "GQEI"
#define OUTPUT_MAGIC "GQEO"

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

static int read_u32(uint32_t *out) { return read_exact(out, sizeof(*out)); }
static int write_u32(uint32_t v) { return write_exact(&v, sizeof(v)); }

/* Wire format (after the GQEI magic and version 1):
 *   u32 channels, u32 LM, u32 start, u32 end, u32 shortBlocks, u32 spread,
 *   u32 dual_stereo, u32 intensity, i32 total_bits, i32 balance,
 *   u32 codedBands, u32 complexity, u32 disable_inv, u32 seed,
 *   u32 nbEBands, u32 nbytes
 *   nbEBands x i32 pulses
 *   nbEBands x i32 tf_res
 *   channels*nbEBands x i32 bandE  (celt_ener == opus_val32)
 *   channels*N x i32 X             (N == shortMdctSize<<LM)
 * Output (after the GQEO magic, version 1):
 *   u32 N, u32 channels, u32 seed_out, u32 nbytes_used
 *   nbytes (storage) x u8 coded    (padded to 4 bytes)
 *   channels*N x i32 X (post-encode)
 *   channels*nbEBands x u8 collapse_masks (padded to 4 bytes)
 */
int main(void) {
  char magic[4];
  uint32_t version, channels, LM, start, end, shortBlocks, spread;
  uint32_t dual_stereo, intensity, codedBands, complexity, disable_inv, seed;
  uint32_t nbEBands, nbytes;
  int32_t total_bits, balance;
  uint32_t i, padded, N, total_x;
  int *pulses = NULL, *tf_res = NULL;
  celt_ener *bandE = NULL;
  unsigned char *coded = NULL;
  celt_norm *X = NULL;
  unsigned char *collapse_masks = NULL;
  const CELTMode *mode = NULL;
  ec_enc enc;
  int ok = 0;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, 4) || memcmp(magic, INPUT_MAGIC, 4) != 0) return 1;
  if (!read_u32(&version) || version != 1) return 1;

  if (!read_u32(&channels) || !read_u32(&LM) || !read_u32(&start) ||
      !read_u32(&end) || !read_u32(&shortBlocks) || !read_u32(&spread) ||
      !read_u32(&dual_stereo) || !read_u32(&intensity)) {
    return 1;
  }
  {
    uint32_t v;
    if (!read_u32(&v)) return 1;
    total_bits = (int32_t)v;
    if (!read_u32(&v)) return 1;
    balance = (int32_t)v;
  }
  if (!read_u32(&codedBands) || !read_u32(&complexity) || !read_u32(&disable_inv) ||
      !read_u32(&seed) || !read_u32(&nbEBands) || !read_u32(&nbytes)) {
    return 1;
  }

  pulses = (int *)malloc(nbEBands * sizeof(int));
  tf_res = (int *)malloc(nbEBands * sizeof(int));
  bandE = (celt_ener *)malloc(channels * nbEBands * sizeof(celt_ener));
  if (!pulses || !tf_res || !bandE) goto done;
  for (i = 0; i < nbEBands; i++) {
    uint32_t v;
    if (!read_u32(&v)) goto done;
    pulses[i] = (int)(int32_t)v;
  }
  for (i = 0; i < nbEBands; i++) {
    uint32_t v;
    if (!read_u32(&v)) goto done;
    tf_res[i] = (int)(int32_t)v;
  }
  for (i = 0; i < channels * nbEBands; i++) {
    uint32_t v;
    if (!read_u32(&v)) goto done;
    bandE[i] = (celt_ener)(int32_t)v;
  }

  mode = opus_custom_mode_create(48000, 960, NULL);
  if (!mode) goto done;

  N = (uint32_t)(mode->shortMdctSize << LM);
  total_x = channels * N;
  X = (celt_norm *)calloc(total_x ? total_x : 1, sizeof(celt_norm));
  if (!X) goto done;
  for (i = 0; i < total_x; i++) {
    uint32_t v;
    if (!read_u32(&v)) goto done;
    X[i] = (celt_norm)(int32_t)v;
  }

  coded = (unsigned char *)calloc(nbytes ? nbytes : 1, 1);
  collapse_masks = (unsigned char *)calloc(channels * nbEBands ? channels * nbEBands : 1, 1);
  if (!coded || !collapse_masks) goto done;

  ec_enc_init(&enc, coded, nbytes);
  quant_all_bands(1, mode, (int)start, (int)end, X,
                  channels == 2 ? X + N : NULL, collapse_masks, bandE, pulses,
                  (int)shortBlocks, (int)spread, (int)dual_stereo, (int)intensity,
                  tf_res, total_bits, balance, &enc, (int)LM, (int)codedBands,
                  &seed, (int)complexity, 0, (int)disable_inv);
  ec_enc_done(&enc);

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(N) ||
      !write_u32(channels) || !write_u32(seed) ||
      !write_u32((uint32_t)ec_range_bytes(&enc))) {
    goto done;
  }
  /* Emit the full storage buffer so raw bits written from the end are included. */
  if (!write_exact(coded, nbytes)) goto done;
  padded = (nbytes + 3u) & ~3u;
  for (i = nbytes; i < padded; i++) {
    unsigned char pad = 0;
    if (!write_exact(&pad, 1)) goto done;
  }
  for (i = 0; i < total_x; i++) {
    if (!write_u32((uint32_t)(int32_t)X[i])) goto done;
  }
  {
    uint32_t mcount = channels * nbEBands;
    if (!write_exact(collapse_masks, mcount)) goto done;
    padded = (mcount + 3u) & ~3u;
    for (i = mcount; i < padded; i++) {
      unsigned char pad = 0;
      if (!write_exact(&pad, 1)) goto done;
    }
  }
  ok = 1;

done:
  free(pulses);
  free(tf_res);
  free(bandE);
  free(coded);
  free(X);
  free(collapse_masks);
  return ok ? 0 : 1;
}
