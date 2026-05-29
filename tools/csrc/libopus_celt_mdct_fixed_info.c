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
#include "celt/kiss_fft.h"
#include "celt/mathops.h"
#include "celt/mdct.h"

/* Oracle helper for the libopus FIXED_POINT (non-QEXT) integer MDCT. Built
 * against the --enable-fixed-point reference tree so config.h defines
 * FIXED_POINT without ENABLE_QEXT. clt_mdct_forward_c/clt_mdct_backward_c are
 * non-static and present in the static-mode library, so we link and drive them
 * directly. clt_mdct_init lives behind CUSTOM_MODES (absent from the static
 * library), so we reconstruct the mdct_lookup here: the kfft[] sub-FFT states
 * (kfft[0] standalone, kfft[i>0] sharing kfft[0]'s twiddles, exactly as
 * opus_fft_alloc/opus_fft_alloc_twiddles do) plus the concatenated per-shift
 * trig table the FIXED_POINT (non-QEXT) clt_mdct_init builds. */

#define INPUT_MAGIC "GCMI"
#define OUTPUT_MAGIC "GCMO"

enum { MODE_FORWARD = 0, MODE_BACKWARD = 1 };

/* Verbatim copies of _celt_cos_pi_2()/celt_cos_norm() from celt/mathops.c, used
 * to build both the FFT twiddles and the MDCT trig table without depending on
 * the (linkable) library symbol so the table construction is self-contained. */
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

static void build_twiddles(kiss_twiddle_cpx *twiddles, int nfft) {
  int i;
  for (i = 0; i < nfft; ++i) {
    opus_val32 phase = -i;
    opus_val32 ph = DIV32(SHL32(phase, 17), nfft);
    twiddles[i].r = TRIG_UPSCALE * oracle_celt_cos_norm(ph);
    twiddles[i].i = TRIG_UPSCALE * oracle_celt_cos_norm(ph - 32768);
  }
}

/* Local copy of celt/kiss_fft.c kf_factor() (static, CUSTOM_MODES). */
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

/* Local copy of celt/kiss_fft.c compute_bitrev_table() (static, CUSTOM_MODES). */
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

/* Reconstruct a kiss_fft_state for nfft. base==NULL builds a standalone state
 * (its own twiddles, shift==-1) like opus_fft_alloc; otherwise it shares base's
 * twiddles with a per-size shift like opus_fft_alloc_twiddles. */
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
    build_twiddles(twiddles, nfft);
    st->twiddles = twiddles;
    st->shift = -1;
  }
  if (!oracle_kf_factor(nfft, st->factors)) return 0;
  st->bitrev = bitrev;
  oracle_compute_bitrev_table(0, bitrev, 1, 1, st->factors);
  return 1;
}

/* Reconstruct the FIXED_POINT (non-QEXT) mdct_lookup for (N, maxshift) exactly
 * as clt_mdct_init does. Allocates the kfft states and the trig table. */
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
  /* kfft[0] owns its twiddles (size N>>2); the rest share them. */
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

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

static int read_exact(void *dst, size_t size) { return fread(dst, 1, size, stdin) == size; }
static int write_exact(const void *src, size_t size) { return fwrite(src, 1, size, stdout) == size; }
static int read_u32(uint32_t *out) { return read_exact(out, sizeof(*out)); }
static int write_u32(uint32_t value) { return write_exact(&value, sizeof(value)); }

/* MDCT path. Header is {version=1, mode, N}. Then params {maxshift, shift,
 * overlap, stride}. The forward path reads N input reals and an overlap-length
 * int16 window; output is stride*(N2-1)+1 reals (the post-rotated spectrum).
 * The backward path reads N2 input reals (frequency samples, contiguous) and a
 * window; output is N reals (the windowed/mirrored time samples). For parity we
 * keep stride==1 and size the output to N so the full buffer round-trips. */
static int run_mdct(uint32_t mode, uint32_t Nu) {
  uint32_t maxshift, shift, overlap, stride, i;
  int N = (int)Nu, N2, N4, n, n2;
  mdct_lookup l;
  kiss_fft_scalar *in, *out;
  celt_coef *window;
  uint32_t in_count, out_count;

  if (!read_u32(&maxshift) || !read_u32(&shift) || !read_u32(&overlap) || !read_u32(&stride))
    return 1;

  memset(&l, 0, sizeof(l));
  if (!oracle_mdct_init(&l, N, (int)maxshift)) return 1;

  /* n/n2 are the per-shift lengths the kernels operate on. */
  n = N;
  for (i = 0; i < shift; i++) n >>= 1;
  n2 = n >> 1;
  N2 = N >> 1;
  N4 = N >> 2;
  (void)N4;

  if (mode == MODE_FORWARD) {
    in_count = (uint32_t)N;                       /* full-length input */
    out_count = (uint32_t)(stride * (n2 - 1) + 1); /* post-rotated spectrum */
  } else {
    in_count = (uint32_t)(stride * (n2 - 1) + 1); /* frequency samples */
    out_count = (uint32_t)n;                      /* time-domain output (this shift) */
  }

  in = (kiss_fft_scalar *)calloc(in_count ? in_count : 1, sizeof(kiss_fft_scalar));
  out = (kiss_fft_scalar *)calloc(out_count ? out_count : 1, sizeof(kiss_fft_scalar));
  window = (celt_coef *)calloc(overlap ? overlap : 1, sizeof(celt_coef));
  if (!in || !out || !window) { free(in); free(out); free(window); return 1; }

  for (i = 0; i < in_count; i++) {
    uint32_t v;
    if (!read_u32(&v)) { free(in); free(out); free(window); return 1; }
    in[i] = (kiss_fft_scalar)(int32_t)v;
  }
  for (i = 0; i < overlap; i++) {
    uint32_t v;
    if (!read_u32(&v)) { free(in); free(out); free(window); return 1; }
    window[i] = (celt_coef)(int16_t)(uint16_t)v;
  }

  if (mode == MODE_FORWARD)
    clt_mdct_forward_c(&l, in, out, window, (int)overlap, (int)shift, (int)stride, 0);
  else
    clt_mdct_backward_c(&l, in, out, window, (int)overlap, (int)shift, (int)stride, 0);

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(out_count)) {
    free(in); free(out); free(window); return 1;
  }
  for (i = 0; i < out_count; i++) {
    if (!write_u32((uint32_t)(int32_t)out[i])) { free(in); free(out); free(window); return 1; }
  }
  free(in);
  free(out);
  free(window);
  return 0;
}

int main(void) {
  char magic[4];
  uint32_t version, mode, N;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) return 1;
  if (!read_u32(&version) || version != 1 || !read_u32(&mode) || !read_u32(&N)) return 1;

  switch (mode) {
    case MODE_FORWARD:
    case MODE_BACKWARD: return run_mdct(mode, N);
    default: return 1;
  }
}
