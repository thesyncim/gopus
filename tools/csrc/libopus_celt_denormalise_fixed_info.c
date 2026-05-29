#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "celt/entcode.h"
#include "celt/mathops.h"
#include "celt/modes.h"
#include "celt/bands.h"

/* Oracle helper for the libopus FIXED_POINT celt/bands.c denormalise_bands.
 * Built against the --enable-fixed-point reference tree so config.h defines
 * FIXED_POINT and the kernel resolves to its integer path. */

#define INPUT_MAGIC "GDBI"
#define OUTPUT_MAGIC "GDBO"

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

static int read_u32(uint32_t *out) {
  return read_exact(out, sizeof(*out));
}

static int write_u32(uint32_t value) {
  return write_exact(&value, sizeof(value));
}

/* Wire format (after the GDBI header and version word):
 *   u32 nbEBands, u32 shortMdctSize, u32 start, u32 end,
 *   u32 M, u32 downsample, u32 silence
 *   (end+1) x i32 eBands     (sign-extended opus_int16 values)
 *   end     x i32 bandLogE   (celt_glog)
 *   (M*eBands[end]) x i32 X  (celt_norm)
 * Output (after the GDBO header, version 1, count = N = M*shortMdctSize):
 *   N x i32 freq             (celt_sig)
 */
static int eval_denormalise(void) {
  uint32_t nbEBands, shortMdctSize, start, end, M, downsample, silence;
  opus_int16 *eBands = NULL;
  celt_glog *bandLogE = NULL;
  celt_norm *X = NULL;
  celt_sig *freq = NULL;
  CELTMode mode;
  uint32_t i, N, xlen;
  int ok = 0;

  if (!read_u32(&nbEBands) || !read_u32(&shortMdctSize) || !read_u32(&start) ||
      !read_u32(&end) || !read_u32(&M) || !read_u32(&downsample) ||
      !read_u32(&silence)) {
    return 0;
  }
  N = M * shortMdctSize;

  eBands = (opus_int16 *)malloc((end + 1) * sizeof(*eBands));
  bandLogE = (celt_glog *)malloc((end ? end : 1) * sizeof(*bandLogE));
  if (!eBands || !bandLogE) goto done;

  for (i = 0; i < end + 1; i++) {
    uint32_t v;
    if (!read_u32(&v)) goto done;
    eBands[i] = (opus_int16)(int32_t)v;
  }
  for (i = 0; i < end; i++) {
    uint32_t v;
    if (!read_u32(&v)) goto done;
    bandLogE[i] = (celt_glog)(int32_t)v;
  }

  xlen = (uint32_t)(M * (uint32_t)eBands[end]);
  X = (celt_norm *)malloc((xlen ? xlen : 1) * sizeof(*X));
  freq = (celt_sig *)malloc((N ? N : 1) * sizeof(*freq));
  if (!X || !freq) goto done;

  for (i = 0; i < xlen; i++) {
    uint32_t v;
    if (!read_u32(&v)) goto done;
    X[i] = (celt_norm)(int32_t)v;
  }
  for (i = 0; i < N; i++) freq[i] = 0;

  memset(&mode, 0, sizeof(mode));
  mode.nbEBands = (int)nbEBands;
  mode.shortMdctSize = (int)shortMdctSize;
  mode.eBands = eBands;

  denormalise_bands(&mode, X, freq, bandLogE, (int)start, (int)end, (int)M,
                    (int)downsample, (int)silence);

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(N)) {
    goto done;
  }
  for (i = 0; i < N; i++) {
    if (!write_u32((uint32_t)(int32_t)freq[i])) goto done;
  }
  ok = 1;

done:
  free(eBands);
  free(bandLogE);
  free(X);
  free(freq);
  return ok;
}

int main(void) {
  char magic[4];
  uint32_t version;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) return 1;
  if (!read_u32(&version) || version != 1) return 1;

  return eval_denormalise() ? 0 : 1;
}
