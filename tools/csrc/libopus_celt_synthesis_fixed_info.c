#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "celt/_kiss_fft_guts.h"
#include "celt/arch.h"
#include "celt/entcode.h"
#include "celt/kiss_fft.h"
#include "celt/mathops.h"
#include "celt/mdct.h"
#include "celt/modes.h"
#include "celt/bands.h"

/* Oracle helper for the libopus FIXED_POINT celt/celt_decoder.c celt_synthesis.
 * celt_synthesis is static in libopus, so its body is reproduced verbatim here
 * (the non-QEXT path) and driven on a caller-supplied X / oldBandE / flags. The
 * cross-frame overlap history is exercised by carrying the full decode_mem
 * buffer in and out, with out_syn[c] = decode_mem[c] + DECODE_BUFFER_SIZE - N,
 * exactly as celt_decode_with_ec sets it up.
 *
 * The synthesis IMDCT runs against a reconstructed mdct_lookup (the same
 * runtime clt_mdct_init reconstruction the standalone MDCT oracle and the Go
 * NewMDCTLookup use) rather than the static mode->mdct. The static
 * mdct_twiddles table baked into the library differs from the runtime
 * clt_mdct_init computation by up to 1 ULP per entry; matching the runtime
 * reconstruction validates the celt_synthesis composition (denormalise +
 * per-block IMDCT orchestration + overlap-add + saturate + stereo) bit-exactly.
 * Wiring the static twiddle table into the Go MDCT is increment-4 (full decode)
 * work.
 *
 * Built against the --enable-fixed-point reference tree so config.h defines
 * FIXED_POINT (and not ENABLE_QEXT): celt_norm/celt_sig/celt_glog are int32,
 * celt_coef (the window) is int16 Q15, and SIG_SAT is 2^29-1. */

#define INPUT_MAGIC "GCYI"
#define OUTPUT_MAGIC "GCYO"

#define DECODE_BUFFER_SIZE 2048

/* Verbatim copies of _celt_cos_pi_2()/celt_cos_norm() from celt/mathops.c plus
 * kf_factor()/compute_bitrev_table()/opus_fft_alloc()/clt_mdct_init() (all
 * static behind CUSTOM_MODES, absent from the static library) used to
 * reconstruct the mdct_lookup exactly as the runtime path builds it. */
#define L1 32767
#define L2 -7651
#define L3 8277
#define L4 -626
static opus_val16 oracle_celt_cos_pi_2(opus_val16 x) {
  opus_val16 x2;
  x2 = MULT16_16_P15(x, x);
  return ADD16(1, MIN16(32766, ADD32(SUB16(L1, x2), MULT16_16_P15(x2, ADD32(L2, MULT16_16_P15(x2, ADD32(L3, MULT16_16_P15(L4, x2))))))));
}
#undef L1
#undef L2
#undef L3
#undef L4

static opus_val16 oracle_celt_cos_norm(opus_val32 x) {
  x = x & 0x0001ffff;
  if (x > SHL32(EXTEND32(1), 16)) x = SUB32(SHL32(EXTEND32(1), 17), x);
  if (x & 0x00007fff) {
    if (x < SHL32(EXTEND32(1), 15)) {
      return oracle_celt_cos_pi_2(EXTRACT16(x));
    } else {
      return NEG16(oracle_celt_cos_pi_2(EXTRACT16(65536 - x)));
    }
  } else {
    if (x & 0x0000ffff)
      return 0;
    else if (x & 0x0001ffff)
      return -32767;
    else
      return 32767;
  }
}

static void oracle_build_twiddles(kiss_twiddle_cpx *twiddles, int nfft) {
  int i;
  for (i = 0; i < nfft; ++i) {
    opus_val32 phase = -i;
    opus_val32 ph = DIV32(SHL32(phase, 17), nfft);
    twiddles[i].r = TRIG_UPSCALE * oracle_celt_cos_norm(ph);
    twiddles[i].i = TRIG_UPSCALE * oracle_celt_cos_norm(ph - 32768);
  }
}

static int oracle_kf_factor(int n, opus_int16 *facbuf) {
  int p = 4;
  int i;
  int stages = 0;
  int nbak = n;
  do {
    while (n % p) {
      switch (p) {
        case 4: p = 2; break;
        case 2: p = 3; break;
        default: p += 2; break;
      }
      if (p > 32000 || (opus_int32)p * (opus_int32)p > n) p = n;
    }
    n /= p;
    if (p > 5) return 0;
    facbuf[2 * stages] = p;
    if (p == 2 && stages > 1) {
      facbuf[2 * stages] = 4;
      facbuf[2] = 2;
    }
    stages++;
  } while (n > 1);
  n = nbak;
  for (i = 0; i < stages / 2; i++) {
    int tmp = facbuf[2 * i];
    facbuf[2 * i] = facbuf[2 * (stages - i - 1)];
    facbuf[2 * (stages - i - 1)] = tmp;
  }
  for (i = 0; i < stages; i++) {
    n /= facbuf[2 * i];
    facbuf[2 * i + 1] = n;
  }
  return 1;
}

static void oracle_compute_bitrev_table(int Fout, opus_int16 *f, const size_t fstride,
                                        int in_stride, opus_int16 *factors) {
  const int p = *factors++;
  const int m = *factors++;
  if (m == 1) {
    int j;
    for (j = 0; j < p; j++) {
      *f = Fout + j;
      f += fstride * in_stride;
    }
  } else {
    int j;
    for (j = 0; j < p; j++) {
      oracle_compute_bitrev_table(Fout, f, fstride * p, in_stride, factors);
      f += fstride * in_stride;
      Fout += m;
    }
  }
}

static int oracle_fft_alloc(kiss_fft_state *st, kiss_twiddle_cpx *twiddles,
                            opus_int16 *bitrev, int nfft,
                            const kiss_fft_state *base) {
  st->nfft = nfft;
  st->scale_shift = celt_ilog2(nfft);
  if (nfft == 1 << st->scale_shift)
    st->scale = Q15ONE;
  else
    st->scale = (1073741824 + nfft / 2) / nfft >> (15 - st->scale_shift);
  if (base != NULL) {
    st->twiddles = base->twiddles;
    st->shift = 0;
    while (st->shift < 32 && nfft << st->shift != base->nfft) st->shift++;
    if (st->shift >= 32) return 0;
  } else {
    oracle_build_twiddles(twiddles, nfft);
    st->twiddles = twiddles;
    st->shift = -1;
  }
  if (!oracle_kf_factor(nfft, st->factors)) return 0;
  st->bitrev = bitrev;
  oracle_compute_bitrev_table(0, bitrev, 1, 1, st->factors);
  return 1;
}

static int oracle_mdct_init(mdct_lookup *l, int N, int maxshift) {
  int i, shift;
  kiss_twiddle_scalar *trig;
  int N2 = N >> 1;
  kiss_fft_state *states;
  kiss_twiddle_cpx *base_twiddles;

  if (maxshift > 3) return 0;
  l->n = N;
  l->maxshift = maxshift;

  states = (kiss_fft_state *)calloc(maxshift + 1, sizeof(kiss_fft_state));
  base_twiddles = (kiss_twiddle_cpx *)malloc(sizeof(kiss_twiddle_cpx) * (N >> 2));
  if (!states || !base_twiddles) { free(states); free(base_twiddles); return 0; }

  for (i = 0; i <= maxshift; i++) {
    int nfft = N >> 2 >> i;
    opus_int16 *bitrev = (opus_int16 *)malloc(sizeof(opus_int16) * nfft);
    if (!bitrev) return 0;
    if (i == 0) {
      if (!oracle_fft_alloc(&states[i], base_twiddles, bitrev, nfft, NULL)) return 0;
    } else {
      if (!oracle_fft_alloc(&states[i], NULL, bitrev, nfft, &states[0])) return 0;
    }
    l->kfft[i] = &states[i];
  }

  l->trig = trig = (kiss_twiddle_scalar *)malloc(
      (N - (N2 >> maxshift)) * sizeof(kiss_twiddle_scalar));
  if (!trig) return 0;
  for (shift = 0; shift <= maxshift; shift++) {
    for (i = 0; i < N2; i++)
      trig[i] = TRIG_UPSCALE *
                oracle_celt_cos_norm(DIV32(ADD32(SHL32(EXTEND32(i), 17), N2 + 16384), N));
    trig += N2;
    N2 >>= 1;
    N >>= 1;
  }
  return 1;
}

#if defined(ENABLE_ASSERTIONS) || defined(ENABLE_HARDENING)
#ifdef __GNUC__
__attribute__((noreturn))
#endif
void celt_fatal(const char *str, const char *file, int line) {
  fprintf(stderr, "Fatal (internal) error in %s, line %d: %s\n", file, line, str);
  abort();
}
#endif

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

/* Verbatim non-QEXT body of celt_synthesis from celt/celt_decoder.c. */
static void celt_synthesis_local(const CELTMode *mode, celt_norm *X, celt_sig *out_syn[],
                                 celt_glog *oldBandE, int start, int effEnd, int C, int CC,
                                 int isTransient, int LM, int downsample, int silence, int arch) {
  int c, i;
  int M;
  int b;
  int B;
  int N, NB;
  int shift;
  int nbEBands;
  int overlap;
  celt_sig *freq;

  overlap = mode->overlap;
  nbEBands = mode->nbEBands;
  N = mode->shortMdctSize << LM;
  freq = (celt_sig *)malloc((size_t)(N ? N : 1) * sizeof(*freq));
  M = 1 << LM;

  if (isTransient) {
    B = M;
    NB = mode->shortMdctSize;
    shift = mode->maxLM;
  } else {
    B = 1;
    NB = mode->shortMdctSize << LM;
    shift = mode->maxLM - LM;
  }

  if (CC == 2 && C == 1) {
    celt_sig *freq2;
    denormalise_bands(mode, X, freq, oldBandE, start, effEnd, M, downsample, silence);
    freq2 = out_syn[1] + overlap / 2;
    OPUS_COPY(freq2, freq, N);
    for (b = 0; b < B; b++)
      clt_mdct_backward(&mode->mdct, &freq2[b], out_syn[0] + NB * b, mode->window, overlap, shift, B, arch);
    for (b = 0; b < B; b++)
      clt_mdct_backward(&mode->mdct, &freq[b], out_syn[1] + NB * b, mode->window, overlap, shift, B, arch);
  } else if (CC == 1 && C == 2) {
    celt_sig *freq2;
    freq2 = out_syn[0] + overlap / 2;
    denormalise_bands(mode, X, freq, oldBandE, start, effEnd, M, downsample, silence);
    denormalise_bands(mode, X + N, freq2, oldBandE + nbEBands, start, effEnd, M, downsample, silence);
    for (i = 0; i < N; i++)
      freq[i] = ADD32(HALF32(freq[i]), HALF32(freq2[i]));
    for (b = 0; b < B; b++)
      clt_mdct_backward(&mode->mdct, &freq[b], out_syn[0] + NB * b, mode->window, overlap, shift, B, arch);
  } else {
    c = 0;
    do {
      denormalise_bands(mode, X + c * N, freq, oldBandE + c * nbEBands, start, effEnd, M, downsample, silence);
      for (b = 0; b < B; b++)
        clt_mdct_backward(&mode->mdct, &freq[b], out_syn[c] + NB * b, mode->window, overlap, shift, B, arch);
    } while (++c < CC);
  }
  c = 0;
  do {
    for (i = 0; i < N; i++)
      out_syn[c][i] = SATURATE(out_syn[c][i], SIG_SAT);
  } while (++c < CC);
  free(freq);
}

/* Wire format (after the GCYI header and version word):
 *   u32 frame_size, u32 C, u32 CC, u32 isTransient, u32 LM, u32 downsample,
 *   u32 silence, u32 start, u32 effEnd
 *   (C*N) x i32 X            (celt_norm), channel-major
 *   (C*nbEBands) x i32 oldBandE (celt_glog), channel-major
 *   (CC*(DECODE_BUFFER_SIZE+overlap)) x i32 decode_mem (celt_sig), channel-major
 * Output (after the GCYO header, version 1):
 *   u32 nbEBands, u32 overlap
 *   (nbEBands+1) x i32 eBands (sign-extended opus_int16 mode->eBands)
 *   overlap x i32 window     (sign-extended celt_coef mode->window, Q15)
 *   u32 count = CC*(DECODE_BUFFER_SIZE+overlap)
 *   count x i32 decode_mem   (celt_sig) */
static int eval_synthesis(void) {
  uint32_t frame_size, C, CC, isTransient, LM, downsample, silence, start, effEnd;
  CELTMode *mode = NULL;
  celt_norm *X = NULL;
  celt_glog *oldBandE = NULL;
  celt_sig *decode_mem[2] = {NULL, NULL};
  celt_sig *out_syn[2] = {NULL, NULL};
  int err = 0;
  int nbEBands, overlap, N, chan_len;
  uint32_t i, c, xlen, elen, total;
  int ok = 0;

  if (!read_u32(&frame_size) || !read_u32(&C) || !read_u32(&CC) ||
      !read_u32(&isTransient) || !read_u32(&LM) || !read_u32(&downsample) ||
      !read_u32(&silence) || !read_u32(&start) || !read_u32(&effEnd)) {
    return 0;
  }

  {
    static CELTMode mode_copy;
    static mdct_lookup recon;
    const CELTMode *base = opus_custom_mode_create(48000, 960, &err);
    if (base == NULL || err != OPUS_OK) {
      fprintf(stderr, "failed to create CELT mode\n");
      return 0;
    }
    mode_copy = *base;
    if (!oracle_mdct_init(&recon, base->mdct.n, base->mdct.maxshift)) {
      fprintf(stderr, "failed to reconstruct mdct lookup\n");
      return 0;
    }
    mode_copy.mdct = recon;
    mode = &mode_copy;
  }
  nbEBands = mode->nbEBands;
  overlap = mode->overlap;
  N = mode->shortMdctSize << LM;
  chan_len = DECODE_BUFFER_SIZE + overlap;

  xlen = C * (uint32_t)N;
  elen = C * (uint32_t)nbEBands;
  X = (celt_norm *)malloc((xlen ? xlen : 1) * sizeof(*X));
  oldBandE = (celt_glog *)malloc((elen ? elen : 1) * sizeof(*oldBandE));
  if (!X || !oldBandE) goto done;
  for (i = 0; i < xlen; i++) {
    uint32_t v;
    if (!read_u32(&v)) goto done;
    X[i] = (celt_norm)(int32_t)v;
  }
  for (i = 0; i < elen; i++) {
    uint32_t v;
    if (!read_u32(&v)) goto done;
    oldBandE[i] = (celt_glog)(int32_t)v;
  }

  for (c = 0; c < CC; c++) {
    decode_mem[c] = (celt_sig *)malloc((size_t)chan_len * sizeof(celt_sig));
    if (!decode_mem[c]) goto done;
    for (i = 0; i < (uint32_t)chan_len; i++) {
      uint32_t v;
      if (!read_u32(&v)) goto done;
      decode_mem[c][i] = (celt_sig)(int32_t)v;
    }
    out_syn[c] = decode_mem[c] + DECODE_BUFFER_SIZE - N;
  }

  celt_synthesis_local(mode, X, out_syn, oldBandE, (int)start, (int)effEnd,
                       (int)C, (int)CC, (int)isTransient, (int)LM,
                       (int)downsample, (int)silence, 0);

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) ||
      !write_u32((uint32_t)nbEBands) || !write_u32((uint32_t)overlap)) {
    goto done;
  }
  for (i = 0; i < (uint32_t)(nbEBands + 1); i++) {
    if (!write_u32((uint32_t)(int32_t)mode->eBands[i])) goto done;
  }
  for (i = 0; i < (uint32_t)overlap; i++) {
    if (!write_u32((uint32_t)(int32_t)mode->window[i])) goto done;
  }
  total = CC * (uint32_t)chan_len;
  if (!write_u32(total)) goto done;
  for (c = 0; c < CC; c++) {
    for (i = 0; i < (uint32_t)chan_len; i++) {
      if (!write_u32((uint32_t)(int32_t)decode_mem[c][i])) goto done;
    }
  }
  ok = 1;

done:
  free(X);
  free(oldBandE);
  free(decode_mem[0]);
  free(decode_mem[1]);
  return ok;
}

int main(void) {
  char magic[4];
  uint32_t version;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) return 1;
  if (!read_u32(&version) || version != 1) return 1;

  return eval_synthesis() ? 0 : 1;
}
