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
#include "celt/entdec.h"
#include "celt/celt.h"
#include "celt/modes.h"
#include "celt/bands.h"
#include "opus_custom.h"

/* Oracle helper for the libopus FIXED_POINT band-shape decode (quant_all_bands,
 * decode side, QEXT off). It runs the real quant_all_bands(0, ...) over a fresh
 * range decoder initialized on a caller-supplied byte buffer, using the static
 * 48000/960 CELTMode, and dumps the resulting normalized celt_norm X[] (and
 * stereo Y[]) plus the collapse_masks[].
 *
 * Both the gopus integer port and this oracle start the range decoder at offset
 * 0 over identical bytes, so the comparison is decoupled from the surrounding
 * decoder state (energy/allocation/tf are passed in as plain inputs). */

#define INPUT_MAGIC "GQBI"
#define OUTPUT_MAGIC "GQBO"
#define GO_QEXT_BANDS 14

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

/* Wire format (after the GQBI magic and version 1):
 *   u32 channels, u32 LM, u32 start, u32 end, u32 shortBlocks, u32 spread,
 *   u32 dual_stereo, u32 intensity, i32 total_bits, i32 balance,
 *   u32 codedBands, u32 disable_inv, u32 seed,
 *   u32 nbEBands
 *   nbEBands x i32 pulses
 *   nbEBands x i32 tf_res
 *   u32 nbytes, nbytes x u8 coded (padded to 4 bytes)
 *   u32 ext_nbytes, ext_nbytes x u8 extension (padded to 4 bytes)
 *   i32 ext_total_bits
 *   (nbEBands+14) x i32 extra_pulses
 *   (nbEBands+14) x i32 extra_caps
 * Output (after the GQBO magic, version 2):
 *   u32 N (== shortMdctSize<<LM), u32 channels, u32 seed_out
 *   main then extension decoder state: rng, val, tell, tell_frac, error
 *   channels*N x i32 X
 *   channels*nbEBands x u8 collapse_masks (padded to 4 bytes)
 */
int main(void) {
  char magic[4];
  uint32_t version, channels, LM, start, end, shortBlocks, spread;
  uint32_t dual_stereo, intensity, codedBands, disable_inv, seed, nbEBands, nbytes;
  uint32_t ext_nbytes;
  int32_t total_bits, balance, ext_total_bits;
  uint32_t i, padded, N, total_x;
  int *pulses = NULL, *tf_res = NULL, *extra_pulses = NULL, *extra_caps = NULL;
  ec_dec ext_dec;
  unsigned char *coded = NULL, *ext_coded = NULL;
  celt_norm *X = NULL;
  unsigned char *collapse_masks = NULL;
  const CELTMode *mode = NULL;
  ec_dec dec;
  int ok = 0;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, 4) || memcmp(magic, INPUT_MAGIC, 4) != 0) return 1;
  if (!read_u32(&version) || version != 2) return 1;

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
  if (!read_u32(&codedBands) || !read_u32(&disable_inv) || !read_u32(&seed) ||
      !read_u32(&nbEBands)) {
    return 1;
  }

  pulses = (int *)malloc(nbEBands * sizeof(int));
  tf_res = (int *)malloc(nbEBands * sizeof(int));
  extra_pulses = (int *)calloc(nbEBands + GO_QEXT_BANDS, sizeof(int));
  extra_caps = (int *)calloc(nbEBands + GO_QEXT_BANDS, sizeof(int));
  if (!pulses || !tf_res || !extra_pulses || !extra_caps) goto done;
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

  if (!read_u32(&nbytes)) goto done;
  coded = (unsigned char *)malloc(nbytes ? nbytes : 1);
  if (!coded) goto done;
  if (nbytes && !read_exact(coded, nbytes)) goto done;
  padded = (nbytes + 3u) & ~3u;
  for (i = nbytes; i < padded; i++) {
    unsigned char pad;
    if (!read_exact(&pad, 1)) goto done;
  }

  if (!read_u32(&ext_nbytes)) goto done;
  ext_coded = (unsigned char *)malloc(ext_nbytes ? ext_nbytes : 1);
  if (!ext_coded) goto done;
  if (ext_nbytes && !read_exact(ext_coded, ext_nbytes)) goto done;
  padded = (ext_nbytes + 3u) & ~3u;
  for (i = ext_nbytes; i < padded; i++) {
    unsigned char pad;
    if (!read_exact(&pad, 1)) goto done;
  }
  {
    uint32_t v;
    if (!read_u32(&v)) goto done;
    ext_total_bits = (int32_t)v;
  }
  for (i = 0; i < nbEBands + GO_QEXT_BANDS; i++) {
    uint32_t v;
    if (!read_u32(&v)) goto done;
    extra_pulses[i] = (int)(int32_t)v;
  }
  for (i = 0; i < nbEBands + GO_QEXT_BANDS; i++) {
    uint32_t v;
    if (!read_u32(&v)) goto done;
    extra_caps[i] = (int)(int32_t)v;
  }

  mode = opus_custom_mode_create(48000, 960, NULL);
  if (!mode) goto done;

  N = (uint32_t)(mode->shortMdctSize << LM);
  total_x = channels * N;
  X = (celt_norm *)calloc(total_x ? total_x : 1, sizeof(celt_norm));
  collapse_masks = (unsigned char *)calloc(channels * nbEBands ? channels * nbEBands : 1, 1);
  if (!X || !collapse_masks) goto done;

  ec_dec_init(&dec, coded, nbytes);
  ec_dec_init(&ext_dec, ext_nbytes ? ext_coded : NULL, ext_nbytes);
  quant_all_bands(0, mode, (int)start, (int)end, X,
                  channels == 2 ? X + N : NULL, collapse_masks, NULL, pulses,
                  (int)shortBlocks, (int)spread, (int)dual_stereo, (int)intensity,
                  tf_res, total_bits, balance, &dec, (int)LM, (int)codedBands,
                  &seed, 0, 0, (int)disable_inv
#ifdef ENABLE_QEXT
                  , &ext_dec, extra_pulses, ext_total_bits, extra_caps
#endif
                  );

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(2) || !write_u32(N) ||
      !write_u32(channels) || !write_u32(seed) ||
      !write_u32(dec.rng) || !write_u32(dec.val) ||
      !write_u32((uint32_t)ec_tell(&dec)) || !write_u32((uint32_t)ec_tell_frac(&dec)) ||
      !write_u32((uint32_t)dec.error) ||
      !write_u32(ext_dec.rng) || !write_u32(ext_dec.val) ||
      !write_u32((uint32_t)ec_tell(&ext_dec)) || !write_u32((uint32_t)ec_tell_frac(&ext_dec)) ||
      !write_u32((uint32_t)ext_dec.error)) {
    goto done;
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
  free(extra_pulses);
  free(extra_caps);
  free(coded);
  free(ext_coded);
  free(X);
  free(collapse_masks);
  return ok ? 0 : 1;
}
