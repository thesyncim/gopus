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

/* Oracle helper for the libopus FIXED_POINT integer CELT math kernels.
 * Built against the --enable-fixed-point reference tree so config.h defines
 * FIXED_POINT and the celt_* functions resolve to their integer paths. */

#define INPUT_MAGIC "GFMI"
#define OUTPUT_MAGIC "GFMO"

/* celt_rcp_norm16 is a public symbol in the FIXED_POINT reference build but has
 * no prototype in mathops.h; declare it here so the oracle can call it. */
opus_val16 celt_rcp_norm16(opus_val16 x);

enum {
  MODE_CELT_SQRT = 0,
  MODE_CELT_SQRT32 = 1,
  MODE_CELT_RSQRT_NORM32 = 2,
  MODE_COMPUTE_BAND_ENERGIES = 3,
  MODE_NORMALISE_BANDS = 4,
  MODE_CELT_RCP = 5,
  MODE_CELT_RCP_NORM16 = 6,
  MODE_CELT_RCP_NORM32 = 7,
  MODE_CELT_COS_NORM = 8,
  MODE_CELT_COS_NORM32 = 9,
  MODE_FRAC_DIV32_Q29 = 10,
  MODE_FRAC_DIV32 = 11,
  MODE_MAX = MODE_FRAC_DIV32
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

/* MODE_COMPUTE_BAND_ENERGIES wire format (after the GFMI header, version, mode
 * and an unused count word):
 *   u32 nbEBands, u32 shortMdctSize, u32 end, u32 C, u32 LM
 *   (nbEBands+1) x i32 eBands (sign-extended opus_int16 values)
 *   end x i32 logN          (sign-extended opus_int16 values)
 *   C*N x i32 X             (N = shortMdctSize<<LM)
 * Output (after the GFMO header, version 1, and count = C*nbEBands):
 *   C*nbEBands x i32 bandE
 * The helper builds a minimal CELTMode and calls the real libopus
 * compute_band_energies() so the result is the genuine reference path. */
static int eval_compute_band_energies(void) {
  uint32_t nbEBands, shortMdctSize, end, C, LM;
  opus_int16 *eBands = NULL, *logN = NULL;
  celt_sig *X = NULL;
  celt_ener *bandE = NULL;
  CELTMode mode;
  uint32_t i, N, total;
  int ok = 0;

  if (!read_u32(&nbEBands) || !read_u32(&shortMdctSize) || !read_u32(&end) ||
      !read_u32(&C) || !read_u32(&LM)) {
    return 0;
  }
  N = shortMdctSize << LM;
  total = C * N;

  eBands = (opus_int16 *)malloc((nbEBands + 1) * sizeof(*eBands));
  logN = (opus_int16 *)malloc(end * sizeof(*logN));
  X = (celt_sig *)malloc(total * sizeof(*X));
  bandE = (celt_ener *)malloc(C * nbEBands * sizeof(*bandE));
  if (!eBands || !logN || !X || !bandE) goto done;

  for (i = 0; i < nbEBands + 1; i++) {
    uint32_t v;
    if (!read_u32(&v)) goto done;
    eBands[i] = (opus_int16)(int32_t)v;
  }
  for (i = 0; i < end; i++) {
    uint32_t v;
    if (!read_u32(&v)) goto done;
    logN[i] = (opus_int16)(int32_t)v;
  }
  for (i = 0; i < total; i++) {
    uint32_t v;
    if (!read_u32(&v)) goto done;
    X[i] = (celt_sig)(int32_t)v;
  }

  memset(&mode, 0, sizeof(mode));
  mode.nbEBands = (int)nbEBands;
  mode.shortMdctSize = (int)shortMdctSize;
  mode.eBands = eBands;
  mode.logN = logN;

  compute_band_energies(&mode, X, bandE, (int)end, (int)C, (int)LM, 0);

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(C * nbEBands)) {
    goto done;
  }
  for (i = 0; i < C * nbEBands; i++) {
    if (!write_u32((uint32_t)(int32_t)bandE[i])) goto done;
  }
  ok = 1;

done:
  free(eBands);
  free(logN);
  free(X);
  free(bandE);
  return ok;
}

/* MODE_NORMALISE_BANDS wire format (after the GFMI header, version, mode and an
 * unused count word):
 *   u32 nbEBands, u32 shortMdctSize, u32 end, u32 C, u32 M
 *   (nbEBands+1) x i32 eBands (sign-extended opus_int16 values)
 *   C*nbEBands x i32 bandE
 *   C*N x i32 freq          (N = M*shortMdctSize)
 * Output (after the GFMO header, version 1, and count = C*N):
 *   C*N x i32 X
 * The helper builds a minimal CELTMode and calls the real libopus
 * normalise_bands() so the result is the genuine reference path. */
static int eval_normalise_bands(void) {
  uint32_t nbEBands, shortMdctSize, end, C, M;
  opus_int16 *eBands = NULL;
  celt_sig *freq = NULL;
  celt_norm *X = NULL;
  celt_ener *bandE = NULL;
  CELTMode mode;
  uint32_t i, N, total;
  int ok = 0;

  if (!read_u32(&nbEBands) || !read_u32(&shortMdctSize) || !read_u32(&end) ||
      !read_u32(&C) || !read_u32(&M)) {
    return 0;
  }
  N = M * shortMdctSize;
  total = C * N;

  eBands = (opus_int16 *)malloc((nbEBands + 1) * sizeof(*eBands));
  bandE = (celt_ener *)malloc(C * nbEBands * sizeof(*bandE));
  freq = (celt_sig *)malloc(total * sizeof(*freq));
  X = (celt_norm *)malloc(total * sizeof(*X));
  if (!eBands || !bandE || !freq || !X) goto done;

  for (i = 0; i < nbEBands + 1; i++) {
    uint32_t v;
    if (!read_u32(&v)) goto done;
    eBands[i] = (opus_int16)(int32_t)v;
  }
  for (i = 0; i < C * nbEBands; i++) {
    uint32_t v;
    if (!read_u32(&v)) goto done;
    bandE[i] = (celt_ener)(int32_t)v;
  }
  for (i = 0; i < total; i++) {
    uint32_t v;
    if (!read_u32(&v)) goto done;
    freq[i] = (celt_sig)(int32_t)v;
  }

  memset(&mode, 0, sizeof(mode));
  mode.nbEBands = (int)nbEBands;
  mode.shortMdctSize = (int)shortMdctSize;
  mode.eBands = eBands;

  normalise_bands(&mode, freq, X, bandE, (int)end, (int)C, (int)M);

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(total)) {
    goto done;
  }
  for (i = 0; i < total; i++) {
    if (!write_u32((uint32_t)(int32_t)X[i])) goto done;
  }
  ok = 1;

done:
  free(eBands);
  free(bandE);
  free(freq);
  free(X);
  return ok;
}

static int eval_record(uint32_t mode) {
  uint32_t a, b;

  switch (mode) {
    case MODE_CELT_SQRT:
      if (!read_u32(&a)) return 0;
      return write_u32((uint32_t)(int32_t)celt_sqrt((opus_val32)(int32_t)a));
    case MODE_CELT_SQRT32:
      if (!read_u32(&a)) return 0;
      return write_u32((uint32_t)(int32_t)celt_sqrt32((opus_val32)(int32_t)a));
    case MODE_CELT_RSQRT_NORM32:
      if (!read_u32(&a)) return 0;
      return write_u32((uint32_t)(int32_t)celt_rsqrt_norm32((opus_val32)(int32_t)a));
    case MODE_CELT_RCP:
      if (!read_u32(&a)) return 0;
      return write_u32((uint32_t)(int32_t)celt_rcp((opus_val32)(int32_t)a));
    case MODE_CELT_RCP_NORM16:
      if (!read_u32(&a)) return 0;
      return write_u32((uint32_t)(int32_t)celt_rcp_norm16((opus_val16)(int32_t)a));
    case MODE_CELT_RCP_NORM32:
      if (!read_u32(&a)) return 0;
      return write_u32((uint32_t)(int32_t)celt_rcp_norm32((opus_val32)(int32_t)a));
    case MODE_CELT_COS_NORM:
      if (!read_u32(&a)) return 0;
      return write_u32((uint32_t)(int32_t)celt_cos_norm((opus_val32)(int32_t)a));
    case MODE_CELT_COS_NORM32:
      if (!read_u32(&a)) return 0;
      return write_u32((uint32_t)(int32_t)celt_cos_norm32((opus_val32)(int32_t)a));
    case MODE_FRAC_DIV32_Q29:
      if (!read_u32(&a) || !read_u32(&b)) return 0;
      return write_u32((uint32_t)(int32_t)frac_div32_q29((opus_val32)(int32_t)a, (opus_val32)(int32_t)b));
    case MODE_FRAC_DIV32:
      if (!read_u32(&a) || !read_u32(&b)) return 0;
      return write_u32((uint32_t)(int32_t)frac_div32((opus_val32)(int32_t)a, (opus_val32)(int32_t)b));
  }
  return 0;
}

int main(void) {
  char magic[4];
  uint32_t version;
  uint32_t mode;
  uint32_t count;
  uint32_t i;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) return 1;
  if (!read_u32(&version) || version != 1 || !read_u32(&mode) || !read_u32(&count)) return 1;
  if (mode > MODE_MAX) return 1;

  if (mode == MODE_COMPUTE_BAND_ENERGIES) {
    return eval_compute_band_energies() ? 0 : 1;
  }
  if (mode == MODE_NORMALISE_BANDS) {
    return eval_normalise_bands() ? 0 : 1;
  }

  if (!write_exact(OUTPUT_MAGIC, sizeof(magic)) || !write_u32(1) || !write_u32(count)) return 1;
  for (i = 0; i < count; i++) {
    if (!eval_record(mode)) return 1;
  }
  return 0;
}
