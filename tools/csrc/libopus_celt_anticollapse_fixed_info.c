/* Fixed-point CELT anti-collapse / renormalise_vector kernel oracle.
 *
 * Built against the --enable-fixed-point libopus reference (config.h defines
 * FIXED_POINT, QEXT off). Exposes the two pure-integer kernels of the
 * anti-collapse path:
 *
 *   renormalise_vector(X, N, gain, arch)  (celt/vq.c)
 *   anti_collapse(m, X, collapse_masks, LM, C, size, start, end,
 *                 logE, prev1logE, prev2logE, pulses, seed, encode, arch)
 *                                          (celt/bands.c)
 *
 * Both are pure integer with no entropy coder. anti_collapse reads only the
 * mode's nbEBands and eBands, so a minimal CELTMode is synthesised here.
 */
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

#include "arch.h"
#include "modes.h"
#include "vq.h"
#include "bands.h"

#define GACI_MAGIC "GACI"
#define GACO_MAGIC "GACO"

enum {
  MODE_RENORMALISE = 0,
  MODE_ANTI_COLLAPSE = 1
};

static int read_exact(void *dst, size_t n) {
  return fread(dst, 1, n, stdin) == n;
}

static int write_exact(const void *src, size_t n) {
  return fwrite(src, 1, n, stdout) == n;
}

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact(b, 4)) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) | ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int write_u32(uint32_t v) {
  unsigned char b[4];
  b[0] = (unsigned char)(v & 0xFF);
  b[1] = (unsigned char)((v >> 8) & 0xFF);
  b[2] = (unsigned char)((v >> 16) & 0xFF);
  b[3] = (unsigned char)((v >> 24) & 0xFF);
  return write_exact(b, 4);
}

static int write_i32(int32_t v) {
  return write_u32((uint32_t)v);
}

static int read_i32(int32_t *out) {
  uint32_t v;
  if (!read_u32(&v)) return 0;
  *out = (int32_t)v;
  return 1;
}

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

static int run_renormalise(void) {
  uint32_t n;
  int32_t gain;
  int32_t i;
  celt_norm *X = NULL;
  if (!read_u32(&n) || n == 0) return 0;
  if (!read_i32(&gain)) return 0;
  X = (celt_norm *)malloc((size_t)n * sizeof(celt_norm));
  if (X == NULL) return 0;
  for (i = 0; i < (int32_t)n; i++) {
    int32_t v;
    if (!read_i32(&v)) { free(X); return 0; }
    X[i] = (celt_norm)v;
  }
  renormalise_vector(X, (int)n, (opus_val32)gain, 0);
  if (!write_u32(MODE_RENORMALISE)) { free(X); return 0; }
  for (i = 0; i < (int32_t)n; i++) {
    if (!write_i32((int32_t)X[i])) { free(X); return 0; }
  }
  free(X);
  return 1;
}

static int run_anti_collapse(void) {
  uint32_t lm_u, c_u, size_u, start_u, end_u, nbEBands_u, seed_u, encode_u;
  int LM, C, size, start, end, nbEBands, encode;
  opus_uint32 seed;
  int32_t i;
  CELTMode mode;
  opus_int16 *eBands = NULL;
  celt_norm *X = NULL;
  unsigned char *collapse_masks = NULL;
  celt_glog *logE = NULL, *prev1logE = NULL, *prev2logE = NULL;
  int *pulses = NULL;
  uint32_t total_X, total_masks, total_logE;

  if (!read_u32(&lm_u) || !read_u32(&c_u) || !read_u32(&size_u) ||
      !read_u32(&start_u) || !read_u32(&end_u) || !read_u32(&nbEBands_u) ||
      !read_u32(&seed_u) || !read_u32(&encode_u))
    return 0;
  LM = (int)lm_u; C = (int)c_u; size = (int)size_u; start = (int)start_u;
  end = (int)end_u; nbEBands = (int)nbEBands_u; seed = (opus_uint32)seed_u;
  encode = (int)encode_u;

  /* eBands has nbEBands+1 entries. */
  eBands = (opus_int16 *)malloc(((size_t)nbEBands + 1) * sizeof(opus_int16));
  if (eBands == NULL) return 0;
  for (i = 0; i < nbEBands + 1; i++) {
    int32_t v;
    if (!read_i32(&v)) { free(eBands); return 0; }
    eBands[i] = (opus_int16)v;
  }

  total_X = (uint32_t)C * (uint32_t)size;
  total_masks = (uint32_t)nbEBands * (uint32_t)C;
  /* logE is read at [c*nbEBands+i] for c<C, but the !encode && C==1 branch
     reads prev{1,2}logE[nbEBands+i]; libopus always allocates the energy logs
     for both channels (2*nbEBands), so we mirror that here. */
  total_logE = 2u * (uint32_t)nbEBands;

  X = (celt_norm *)malloc((size_t)total_X * sizeof(celt_norm));
  collapse_masks = (unsigned char *)malloc((size_t)total_masks);
  logE = (celt_glog *)malloc((size_t)total_logE * sizeof(celt_glog));
  prev1logE = (celt_glog *)malloc((size_t)total_logE * sizeof(celt_glog));
  prev2logE = (celt_glog *)malloc((size_t)total_logE * sizeof(celt_glog));
  pulses = (int *)malloc((size_t)nbEBands * sizeof(int));
  if (!X || !collapse_masks || !logE || !prev1logE || !prev2logE || !pulses)
    goto fail;

  for (i = 0; i < (int32_t)total_X; i++) {
    int32_t v;
    if (!read_i32(&v)) goto fail;
    X[i] = (celt_norm)v;
  }
  for (i = 0; i < (int32_t)total_masks; i++) {
    int32_t v;
    if (!read_i32(&v)) goto fail;
    collapse_masks[i] = (unsigned char)v;
  }
  for (i = 0; i < (int32_t)total_logE; i++) {
    int32_t v;
    if (!read_i32(&v)) goto fail;
    logE[i] = (celt_glog)v;
  }
  for (i = 0; i < (int32_t)total_logE; i++) {
    int32_t v;
    if (!read_i32(&v)) goto fail;
    prev1logE[i] = (celt_glog)v;
  }
  for (i = 0; i < (int32_t)total_logE; i++) {
    int32_t v;
    if (!read_i32(&v)) goto fail;
    prev2logE[i] = (celt_glog)v;
  }
  for (i = 0; i < nbEBands; i++) {
    int32_t v;
    if (!read_i32(&v)) goto fail;
    pulses[i] = (int)v;
  }

  memset(&mode, 0, sizeof(mode));
  mode.nbEBands = nbEBands;
  mode.eBands = eBands;

  anti_collapse(&mode, X, collapse_masks, LM, C, size, start, end,
                logE, prev1logE, prev2logE, pulses, seed, encode, 0);

  if (!write_u32(MODE_ANTI_COLLAPSE)) goto fail;
  for (i = 0; i < (int32_t)total_X; i++) {
    if (!write_i32((int32_t)X[i])) goto fail;
  }
  free(eBands); free(X); free(collapse_masks);
  free(logE); free(prev1logE); free(prev2logE); free(pulses);
  return 1;

fail:
  free(eBands); free(X); free(collapse_masks);
  free(logE); free(prev1logE); free(prev2logE); free(pulses);
  return 0;
}

int main(void) {
  char magic[4];
  uint32_t version;
  uint32_t mode;
  int ok;
  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, 4) || memcmp(magic, GACI_MAGIC, 4) != 0) {
    fprintf(stderr, "bad input magic\n");
    return 1;
  }
  if (!read_u32(&version) || version != 1) {
    fprintf(stderr, "bad input version\n");
    return 1;
  }
  if (!write_exact(GACO_MAGIC, 4)) return 1;
  if (!write_u32(1)) return 1; /* protocol version */
  if (!read_u32(&mode)) {
    fprintf(stderr, "failed to read mode\n");
    return 1;
  }
  switch (mode) {
    case MODE_RENORMALISE: ok = run_renormalise(); break;
    case MODE_ANTI_COLLAPSE: ok = run_anti_collapse(); break;
    default:
      fprintf(stderr, "unknown mode %u\n", mode);
      return 1;
  }
  if (!ok) {
    fprintf(stderr, "mode %u failed\n", mode);
    return 1;
  }
  fflush(stdout);
  return 0;
}
