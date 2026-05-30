/* Drives the full libopus QEXT extension-band content coding chain (the
   ENABLE_QEXT secondary range coder that fills the reserved ext payload) for
   the native 96 kHz CELT mode and dumps the coded extension bytes plus the
   decoded extension X coefficients and qext band energies.

   It mirrors the encode block in celt/celt_encoder.c (quant_coarse_energy +
   clt_compute_extra_allocation + quant_fine_energy + quant_all_bands, all into
   one ext_enc) and the matching decode block in celt/celt_decoder.c
   (unquant_coarse_energy + clt_compute_extra_allocation + unquant_fine_energy +
   quant_all_bands, all from one ext_dec). The oracle encodes a deterministic,
   seed-generated normalised X and band-energy vector to produce a valid ext
   bitstream, then decodes it and dumps what the decoder reconstructs. The Go
   port replays the *decode* path against the same coded bytes and the same
   main-coder tell state, and must reproduce the decoded X and energies.

   To make the ec_tell_frac() of the main coder match between C and Go without
   transferring a full main frame, both sides init a main range coder over
   main_storage bytes and consume exactly main_consumed raw bits before the
   extension allocation runs (clt_compute_extra_allocation reads the main
   coder's ec_tell_frac to size qext_bits).

   Protocol (little-endian):
     in : "GQXI" magic, u32 version,
          u32 channels (C), u32 LM, u32 qext_end,
          u32 intensity, u32 dual_stereo, u32 transient,
          u32 ext_storage, u32 main_storage, u32 main_consumed,
          u32 x_seed, u32 e_seed
     out: "GQXO" magic, u32 version(=1),
          u32 N,                                  N = shortMdctSize<<LM = 1920
          u32 qext_start_bin, u32 qext_stop_bin,  X band range = eBands[0..qext_end]<<LM
          u32 ext_len, u8 ext_bytes[ext_len],     ext_len = ext_storage
          u32 nx, f32 decoded_X[nx],              nx = C*(stop-start)
          u32 ne, f32 decoded_qext_oldBandE[ne]   ne = C*NB_QEXT_BANDS
*/
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <math.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#ifdef HAVE_CONFIG_H
#include "config.h"
#endif

#include "celt.h"
#include "entcode.h"
#include "entenc.h"
#include "entdec.h"
#include "mdct.h"
#include "modes.h"
#include "rate.h"
#include "bands.h"
#include "quant_bands.h"
#include "vq.h"

#define GQXI_MAGIC "GQXI"
#define GQXO_MAGIC "GQXO"

#if defined(ENABLE_ASSERTIONS) || defined(ENABLE_HARDENING)
#ifdef __GNUC__
__attribute__((noreturn))
#endif
void celt_fatal(const char *str, const char *file, int line) {
  fprintf(stderr, "Fatal (internal) error in %s, line %d: %s\n", file, line, str);
  abort();
}
#endif

/* Some libopus builds reference an SSE2 PVQ search symbol when linking the
   static lib; provide the scalar fallback like the existing VQ oracle. */
opus_val16 op_pvq_search_sse2(celt_norm *x, int *iy, int k, int n, int arch) {
  return op_pvq_search_c(x, iy, k, n, arch);
}

static int read_exact(void *dst, size_t n) { return fread(dst, 1, n, stdin) == n; }
static int write_exact(const void *src, size_t n) { return fwrite(src, 1, n, stdout) == n; }

static uint32_t read_u32(void) {
  unsigned char b[4];
  if (!read_exact(b, 4)) { fprintf(stderr, "short read u32\n"); exit(1); }
  return (uint32_t)b[0] | ((uint32_t)b[1] << 8) | ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
}

static int write_u32(uint32_t v) {
  unsigned char b[4];
  b[0] = (unsigned char)(v & 0xFF);
  b[1] = (unsigned char)((v >> 8) & 0xFF);
  b[2] = (unsigned char)((v >> 16) & 0xFF);
  b[3] = (unsigned char)((v >> 24) & 0xFF);
  return write_exact(b, 4);
}

static int write_f32(float f) {
  uint32_t u;
  memcpy(&u, &f, 4);
  return write_u32(u);
}

/* xorshift32 PRNG so the Go side could reproduce the inputs if needed; here
   only the C oracle generates them. */
static uint32_t prng_next(uint32_t *s) {
  uint32_t x = *s;
  x ^= x << 13;
  x ^= x >> 17;
  x ^= x << 5;
  *s = x;
  return x;
}

static float prng_unit(uint32_t *s) {
  /* uniform in [-1,1) */
  return (float)((double)prng_next(s) / 2147483648.0 - 1.0);
}

int main(void) {
#ifdef _WIN32
  _setmode(_fileno(stdin), _O_BINARY);
  _setmode(_fileno(stdout), _O_BINARY);
#endif
  char magic[4];
  if (!read_exact(magic, 4) || memcmp(magic, GQXI_MAGIC, 4) != 0) {
    fprintf(stderr, "bad input magic\n");
    return 1;
  }
  (void)read_u32(); /* version */

  int C = (int)read_u32();
  int LM = (int)read_u32();
  int qext_end = (int)read_u32();
  int qext_intensity = (int)read_u32();
  int qext_dual_stereo = (int)read_u32();
  int transient = (int)read_u32();
  int ext_storage = (int)read_u32();
  int main_storage = (int)read_u32();
  int main_consumed = (int)read_u32();
  uint32_t x_seed = read_u32();
  uint32_t e_seed = read_u32();

  int err = OPUS_OK;
  CELTMode *mode = opus_custom_mode_create(96000, 1920, &err);
  if (mode == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_custom_mode_create(96000,1920) failed err=%d\n", err);
    return 1;
  }

  CELTMode qext;
  compute_qext_mode(&qext, mode);

  const int nbEBands = mode->nbEBands;
  const int M = 1 << LM;
  const int N = mode->shortMdctSize << LM;
  const int shortBlocks = transient ? M : 0;
  const int q_start_bin = qext.eBands[0] * M;
  const int q_stop_bin = qext.eBands[qext_end] * M;

  /* Build a deterministic normalised X for the qext bands (both channels) and
     deterministic band energies. The encoder consumes these; the decoder
     reconstructs its own X which we dump. */
  celt_norm *X = (celt_norm *)calloc((size_t)C * N, sizeof(celt_norm));
  celt_ener *qext_bandE = (celt_ener *)calloc((size_t)2 * NB_QEXT_BANDS, sizeof(celt_ener));
  celt_glog *qext_bandLogE = (celt_glog *)calloc((size_t)2 * NB_QEXT_BANDS, sizeof(celt_glog));
  celt_glog *qext_oldBandE_enc = (celt_glog *)calloc((size_t)2 * NB_QEXT_BANDS, sizeof(celt_glog));
  celt_glog *qext_error = (celt_glog *)calloc((size_t)2 * NB_QEXT_BANDS, sizeof(celt_glog));

  {
    uint32_t xs = x_seed;
    for (int c = 0; c < C; c++) {
      for (int i = 0; i < qext_end; i++) {
        int bs = qext.eBands[i] * M;
        int be = qext.eBands[i + 1] * M;
        /* normalised per-band unit vector */
        double norm = 0.0;
        for (int j = bs; j < be; j++) {
          float v = prng_unit(&xs);
          X[c * N + j] = v;
          norm += (double)v * (double)v;
        }
        norm = sqrt(norm);
        if (norm < 1e-9) norm = 1e-9;
        for (int j = bs; j < be; j++) X[c * N + j] = (celt_norm)(X[c * N + j] / norm);
      }
    }
  }
  {
    uint32_t es = e_seed;
    for (int c = 0; c < C; c++) {
      for (int i = 0; i < NB_QEXT_BANDS; i++) {
        /* positive energies; band-energy units */
        float e = 0.5f + 4.0f * ((float)(prng_next(&es) & 0xFFFF) / 65536.0f);
        qext_bandE[c * NB_QEXT_BANDS + i] = (celt_ener)e;
      }
    }
  }
  amp2Log2(&qext, qext_end, qext_end, qext_bandE, qext_bandLogE, C);

  /* ---- main-coder tell synchronisation ---- */
  unsigned char *main_buf = (unsigned char *)calloc((size_t)main_storage, 1);
  ec_enc main_enc;
  ec_enc_init(&main_enc, main_buf, main_storage);
  {
    int left = main_consumed;
    while (left > 0) {
      int n = left > 16 ? 16 : left;
      ec_enc_bits(&main_enc, (1u << (n - 1)), n);
      left -= n;
    }
  }
  opus_uint32 main_tell_frac = ec_tell_frac(&main_enc);

  /* ---- encode the extension chain ---- */
  unsigned char *ext_buf = (unsigned char *)calloc((size_t)ext_storage, 1);
  ec_enc ext_enc;
  ec_enc_init(&ext_enc, ext_buf, ext_storage);

  /* qext_end flag (NB_QEXT_BANDS vs 2). */
  ec_enc_bit_logp(&ext_enc, qext_end == NB_QEXT_BANDS, 1);
  if (C == 2) {
    ec_enc_uint(&ext_enc, qext_intensity, qext_end + 1);
    if (qext_intensity != 0) ec_enc_bit_logp(&ext_enc, qext_dual_stereo, 1);
  } else {
    qext_intensity = 0;
    qext_dual_stereo = 0;
  }

  opus_val32 qext_delayedIntra = 0;
  quant_coarse_energy(&qext, 0, qext_end, qext_end, qext_bandLogE,
                      qext_oldBandE_enc, (opus_uint32)ext_storage * 8, qext_error, &ext_enc,
                      C, LM, ext_storage, 0, &qext_delayedIntra, 1, 0, 0);

  int *extra_quant = (int *)calloc((size_t)(nbEBands + NB_QEXT_BANDS), sizeof(int));
  int *extra_pulses = (int *)calloc((size_t)(nbEBands + NB_QEXT_BANDS), sizeof(int));

  opus_int32 qext_bits = ((opus_int32)ext_storage * 8 << BITRES) - (opus_int32)main_tell_frac - 1;
  clt_compute_extra_allocation(mode, &qext, 0, nbEBands, qext_end, NULL, qext_bandLogE,
                               qext_bits, extra_pulses, extra_quant, C, LM, &ext_enc, 1, 0.5f, 0.3f);

  /* The encoder runs quant_fine_energy(mode,...) on the MAIN bands here, but
     that writes into the *main* ext_enc fine-energy slots which the decoder
     also replays identically; for an isolated extension oracle we only need the
     qext fine energy. The main-band extra fine energy uses extra_quant[start..end]
     which is fully determined by the allocation we already coded. Encode it the
     same way the real encoder does so the bit position before quant_all_bands
     matches: it uses fine_quant=NULL placeholder path only for main bands. */

  /* Encode-side qext residual coding (mirrors celt_encoder.c lines 2677-2694). */
  {
    int *zeros = (int *)calloc((size_t)nbEBands, sizeof(int));
    unsigned char *qcm = (unsigned char *)calloc((size_t)(C * NB_QEXT_BANDS), 1);
    ec_enc dummy_enc;
    int ext_balance;
    ec_enc_init(&dummy_enc, NULL, 0);
    ext_balance = ext_storage * (8 << BITRES) - ec_tell_frac(&ext_enc);
    for (int i = 0; i < qext_end; i++)
      ext_balance -= extra_pulses[nbEBands + i] + C * (extra_quant[nbEBands + 1] << BITRES);
    quant_fine_energy(&qext, 0, qext_end, qext_oldBandE_enc, qext_error, NULL,
                      &extra_quant[nbEBands], &ext_enc, C);
    quant_all_bands(1, &qext, 0, qext_end, X, C == 2 ? X + N : NULL, qcm,
                    qext_bandE, &extra_pulses[nbEBands], shortBlocks, SPREAD_NORMAL,
                    qext_dual_stereo, qext_intensity, zeros, ext_storage * (8 << BITRES),
                    ext_balance, &ext_enc, LM, qext_end, &(opus_uint32){0}, 10, 0, 0,
                    &dummy_enc, zeros, 0, NULL);
    free(zeros);
    free(qcm);
  }
  ec_enc_done(&ext_enc);

  /* ---- decode the extension chain from the coded bytes ---- */
  /* Reproduce the main-coder tell with a decoder over the same byte budget. */
  ec_dec main_dec;
  ec_dec_init(&main_dec, main_buf, main_storage);
  {
    int left = main_consumed;
    while (left > 0) {
      int n = left > 16 ? 16 : left;
      (void)ec_dec_bits(&main_dec, n);
      left -= n;
    }
  }
  opus_uint32 main_tell_frac_dec = ec_tell_frac(&main_dec);
  if (main_tell_frac_dec != main_tell_frac) {
    fprintf(stderr, "main tell mismatch enc=%u dec=%u\n", main_tell_frac, main_tell_frac_dec);
    return 1;
  }

  celt_norm *Xd = (celt_norm *)calloc((size_t)C * N, sizeof(celt_norm));
  celt_glog *qext_oldBandE_dec = (celt_glog *)calloc((size_t)2 * NB_QEXT_BANDS, sizeof(celt_glog));
  int *dq = (int *)calloc((size_t)(nbEBands + NB_QEXT_BANDS), sizeof(int));
  int *dp = (int *)calloc((size_t)(nbEBands + NB_QEXT_BANDS), sizeof(int));

  ec_dec ext_dec;
  ec_dec_init(&ext_dec, ext_buf, ext_storage);

  int dec_qext_end = ec_dec_bit_logp(&ext_dec, 1) ? NB_QEXT_BANDS : 2;
  int dec_intensity = 0, dec_dual = 0;
  if (C == 2) {
    dec_intensity = ec_dec_uint(&ext_dec, dec_qext_end + 1);
    if (dec_intensity != 0) dec_dual = ec_dec_bit_logp(&ext_dec, 1);
  }
  int qext_intra = ec_tell(&ext_dec) + 3 <= ext_storage * 8 ? ec_dec_bit_logp(&ext_dec, 3) : 0;
  unquant_coarse_energy(&qext, 0, dec_qext_end, qext_oldBandE_dec, qext_intra, &ext_dec, C, LM);

  opus_int32 qext_bits_dec = ((opus_int32)ext_storage * 8 << BITRES) - (opus_int32)main_tell_frac - 1;
  clt_compute_extra_allocation(mode, &qext, 0, nbEBands, dec_qext_end, NULL, NULL,
                               qext_bits_dec, dp, dq, C, LM, &ext_dec, 0, 0, 0);

  {
    int *zeros = (int *)calloc((size_t)nbEBands, sizeof(int));
    unsigned char *qcm = (unsigned char *)calloc((size_t)(C * NB_QEXT_BANDS), 1);
    ec_dec dummy_dec;
    int ext_balance;
    ec_dec_init(&dummy_dec, NULL, 0);
    ext_balance = ext_storage * (8 << BITRES) - ec_tell_frac(&ext_dec);
    for (int i = 0; i < dec_qext_end; i++)
      ext_balance -= dp[nbEBands + i] + C * (dq[nbEBands + 1] << BITRES);
    unquant_fine_energy(&qext, 0, dec_qext_end, qext_oldBandE_dec, NULL, &dq[nbEBands], &ext_dec, C);
    quant_all_bands(0, &qext, 0, dec_qext_end, Xd, C == 2 ? Xd + N : NULL, qcm,
                    NULL, &dp[nbEBands], shortBlocks, SPREAD_NORMAL,
                    dec_dual, dec_intensity, zeros, ext_storage * (8 << BITRES),
                    ext_balance, &ext_dec, LM, dec_qext_end, &(opus_uint32){0}, 0, 0, 0,
                    &dummy_dec, zeros, 0, NULL);
    free(zeros);
    free(qcm);
  }

  /* ---- dump ---- */
  if (!write_exact(GQXO_MAGIC, 4)) return 1;
  write_u32(1u);
  write_u32((uint32_t)N);
  write_u32((uint32_t)q_start_bin);
  write_u32((uint32_t)q_stop_bin);

  write_u32((uint32_t)ext_storage);
  if (ext_storage > 0 && !write_exact(ext_buf, (size_t)ext_storage)) return 1;

  int span = q_stop_bin - q_start_bin;
  write_u32((uint32_t)(C * span));
  for (int c = 0; c < C; c++)
    for (int j = 0; j < span; j++)
      write_f32((float)Xd[c * N + q_start_bin + j]);

  write_u32((uint32_t)(C * NB_QEXT_BANDS));
  for (int c = 0; c < C; c++)
    for (int i = 0; i < NB_QEXT_BANDS; i++)
      write_f32((float)qext_oldBandE_dec[c * NB_QEXT_BANDS + i]);

  /* debug: main tell + decoded qext extra pulses/quant */
  write_u32((uint32_t)main_tell_frac);
  write_u32((uint32_t)dec_qext_end);
  for (int i = 0; i < dec_qext_end; i++) write_u32((uint32_t)dp[nbEBands + i]);
  for (int i = 0; i < dec_qext_end; i++) write_u32((uint32_t)dq[nbEBands + i]);

  fflush(stdout);
  return 0;
}
