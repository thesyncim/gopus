/* Fixed-point CELT entropy-coupled PVQ wrapper oracle (alg_quant / alg_unquant).
 *
 * Built against the --enable-fixed-point libopus reference (config.h defines
 * FIXED_POINT). QEXT is off in that reference config, so the ARG_QEXT wrapper
 * arguments collapse away and we exercise the canonical non-QEXT alg_quant /
 * alg_unquant path: exp_rotation -> op_pvq_search -> encode_pulses (range
 * coder) -> normalise_residual, and the inverse on decode.
 *
 * The NEON integer override for op_pvq_search is undefined so the pure-C
 * scalar kernel is exercised (libopus asserts the NEON path is bit-exact).
 *
 * MODE_QUANT runs alg_quant with a real ec_enc and returns the collapse mask,
 * the number of finalised range-coder bytes, those bytes, and the resynthesised
 * celt_norm X (resynth=1).
 *
 * MODE_UNQUANT runs alg_unquant with a real ec_dec over caller-supplied bytes
 * and returns the collapse mask and the decoded celt_norm X.
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

/* Force the canonical scalar kernel regardless of the reference config. */
#undef OPUS_ARM_MAY_HAVE_NEON_INTR
#undef OPUS_ARM_PRESUME_NEON_INTR
#undef OPUS_ARM_MAY_HAVE_NEON
#undef OPUS_ARM_PRESUME_NEON
#undef OPUS_HAVE_RTCD

#include "arch.h"
#include "entenc.h"
#include "entdec.h"
#include "vq.h"

#define GAQI_MAGIC "GAQI"
#define GAQO_MAGIC "GAQO"

enum {
  MODE_QUANT = 0,
  MODE_UNQUANT = 1
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

static int write_i32(int32_t v) { return write_u32((uint32_t)v); }

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

/* Compact the finalised range-coder buffer into a contiguous packet, matching
 * the layout libopus packets use (range bytes at the front grown from offset 0,
 * raw/end bytes appended after, with the shared partial byte merged in). This
 * is the same layout the Go range encoder's Done() produces, so the byte
 * streams can be compared directly. */
static uint32_t compact_packet(const ec_enc *enc, unsigned char *dst) {
  uint32_t len;
  uint32_t partial;
  if (enc->error) {
    memcpy(dst, enc->buf, enc->storage);
    return enc->storage;
  }
  partial = (enc->nend_bits & 7) != 0 && enc->end_offs < enc->storage ? 1U : 0U;
  len = enc->offs + partial + enc->end_offs;
  if (enc->offs > 0) memcpy(dst, enc->buf, enc->offs);
  if (partial) dst[enc->offs] = enc->buf[enc->storage - enc->end_offs - 1];
  if (enc->end_offs > 0) {
    memcpy(dst + enc->offs + partial, enc->buf + enc->storage - enc->end_offs, enc->end_offs);
  }
  return len;
}

/* MODE_QUANT input:  N, K, spread, B, gain, bufBytes, then N celt_norm X.
 * MODE_QUANT output: collapse_mask(u32), nbytes(u32), nbytes bytes,
 *                    then N celt_norm X (resynthesised).
 */
static int run_quant(void) {
  uint32_t n, k, spread, b, buf_bytes;
  int32_t gain;
  int32_t i;
  celt_norm *X = NULL;
  unsigned char *buf = NULL;
  unsigned char *packet = NULL;
  ec_enc enc;
  unsigned collapse_mask;
  uint32_t nbytes;

  if (!read_u32(&n) || !read_u32(&k) || !read_u32(&spread) || !read_u32(&b)) return 0;
  if (!read_i32(&gain) || !read_u32(&buf_bytes)) return 0;
  if (n == 0 || buf_bytes == 0) return 0;

  X = (celt_norm *)malloc((size_t)n * sizeof(celt_norm));
  buf = (unsigned char *)calloc((size_t)buf_bytes, 1);
  packet = (unsigned char *)calloc((size_t)buf_bytes, 1);
  if (X == NULL || buf == NULL || packet == NULL) { free(X); free(buf); free(packet); return 0; }

  for (i = 0; i < (int32_t)n; i++) {
    int32_t v;
    if (!read_i32(&v)) { free(X); free(buf); free(packet); return 0; }
    X[i] = (celt_norm)v;
  }

  ec_enc_init(&enc, buf, buf_bytes);
  collapse_mask = alg_quant(X, (int)n, (int)k, (int)spread, (int)b, &enc,
                            (opus_val32)gain, 1, 0);
  ec_enc_done(&enc);
  nbytes = compact_packet(&enc, packet);

  if (!write_u32(MODE_QUANT) || !write_u32((uint32_t)collapse_mask) ||
      !write_u32(nbytes) || (nbytes > 0 && !write_exact(packet, nbytes))) {
    free(X); free(buf); free(packet); return 0;
  }
  for (i = 0; i < (int32_t)n; i++) {
    if (!write_i32((int32_t)X[i])) { free(X); free(buf); free(packet); return 0; }
  }
  free(X); free(buf); free(packet);
  return 1;
}

/* MODE_UNQUANT input:  N, K, spread, B, gain, nbytes, then nbytes bytes.
 * MODE_UNQUANT output: collapse_mask(u32), then N celt_norm X.
 */
static int run_unquant(void) {
  uint32_t n, k, spread, b, nbytes;
  int32_t gain;
  int32_t i;
  celt_norm *X = NULL;
  unsigned char *buf = NULL;
  ec_dec dec;
  unsigned collapse_mask;

  if (!read_u32(&n) || !read_u32(&k) || !read_u32(&spread) || !read_u32(&b)) return 0;
  if (!read_i32(&gain) || !read_u32(&nbytes)) return 0;
  if (n == 0 || nbytes == 0) return 0;

  X = (celt_norm *)malloc((size_t)n * sizeof(celt_norm));
  buf = (unsigned char *)malloc((size_t)nbytes);
  if (X == NULL || buf == NULL) { free(X); free(buf); return 0; }
  if (!read_exact(buf, nbytes)) { free(X); free(buf); return 0; }

  ec_dec_init(&dec, buf, nbytes);
  collapse_mask = alg_unquant(X, (int)n, (int)k, (int)spread, (int)b, &dec,
                              (opus_val32)gain);

  if (!write_u32(MODE_UNQUANT) || !write_u32((uint32_t)collapse_mask)) {
    free(X); free(buf); return 0;
  }
  for (i = 0; i < (int32_t)n; i++) {
    if (!write_i32((int32_t)X[i])) { free(X); free(buf); return 0; }
  }
  free(X); free(buf);
  return 1;
}

int main(void) {
  char magic[4];
  uint32_t version;
  uint32_t mode;
  int ok;
  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, 4) || memcmp(magic, GAQI_MAGIC, 4) != 0) {
    fprintf(stderr, "bad input magic\n");
    return 1;
  }
  if (!read_u32(&version) || version != 1) {
    fprintf(stderr, "bad input version\n");
    return 1;
  }
  if (!write_exact(GAQO_MAGIC, 4)) return 1;
  if (!write_u32(1)) return 1; /* protocol version */
  if (!read_u32(&mode)) {
    fprintf(stderr, "failed to read mode\n");
    return 1;
  }
  switch (mode) {
    case MODE_QUANT:   ok = run_quant(); break;
    case MODE_UNQUANT: ok = run_unquant(); break;
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
