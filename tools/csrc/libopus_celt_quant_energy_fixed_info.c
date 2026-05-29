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
#include "celt/quant_bands.h"

/* Oracle helper for the libopus FIXED_POINT CELT energy-quantizer prediction
 * math kernels. Built against the --enable-fixed-point reference tree so
 * config.h defines FIXED_POINT and amp2Log2 resolves to its integer path. */

#define INPUT_MAGIC "GQEI"
#define OUTPUT_MAGIC "GQEO"

enum {
  MODE_AMP2LOG2 = 0
};

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

/* MODE_AMP2LOG2 wire format (after the GQEI header, version, mode and an unused
 * count word):
 *   u32 nbEBands, u32 effEnd, u32 end, u32 C
 *   C*nbEBands x i32 bandE   (celt_ener, Q12)
 * Output (after the GQEO header, version 1, and count = C*nbEBands):
 *   C*nbEBands x i32 bandLogE (celt_glog, Q24)
 * The helper builds a minimal CELTMode and calls the real libopus amp2Log2() so
 * the result is the genuine reference path. */
static int eval_amp2log2(void) {
  uint32_t nbEBands, effEnd, end, C;
  celt_ener *bandE = NULL;
  celt_glog *bandLogE = NULL;
  CELTMode mode;
  uint32_t i, total;
  int ok = 0;

  if (!read_u32(&nbEBands) || !read_u32(&effEnd) || !read_u32(&end) ||
      !read_u32(&C)) {
    return 0;
  }
  total = C * nbEBands;

  bandE = (celt_ener *)malloc(total * sizeof(*bandE));
  bandLogE = (celt_glog *)malloc(total * sizeof(*bandLogE));
  if (!bandE || !bandLogE) goto done;

  for (i = 0; i < total; i++) {
    uint32_t v;
    if (!read_u32(&v)) goto done;
    bandE[i] = (celt_ener)(int32_t)v;
  }
  memset(bandLogE, 0, total * sizeof(*bandLogE));

  memset(&mode, 0, sizeof(mode));
  mode.nbEBands = (int)nbEBands;

  amp2Log2(&mode, (int)effEnd, (int)end, bandE, bandLogE, (int)C);

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(total)) {
    goto done;
  }
  for (i = 0; i < total; i++) {
    if (!write_u32((uint32_t)(int32_t)bandLogE[i])) goto done;
  }
  ok = 1;

done:
  free(bandE);
  free(bandLogE);
  return ok;
}

int main(void) {
  char magic[4];
  uint32_t version;
  uint32_t mode;
  uint32_t count;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) return 1;
  if (!read_u32(&version) || version != 1 || !read_u32(&mode) || !read_u32(&count)) return 1;
  (void)count;

  switch (mode) {
    case MODE_AMP2LOG2:
      return eval_amp2log2() ? 0 : 1;
  }
  return 1;
}
