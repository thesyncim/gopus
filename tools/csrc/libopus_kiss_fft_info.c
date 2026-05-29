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

/* Oracle helper for the libopus FIXED_POINT integer KISS-FFT butterflies.
 * Built against the --enable-fixed-point reference tree so config.h defines
 * FIXED_POINT (without ENABLE_QEXT). The kf_bfly* kernels in celt/kiss_fft.c are
 * static, so their non-CUSTOM_MODES bodies are reproduced verbatim here to drive
 * the same integer arithmetic the library uses. The macros (S_MUL, C_MUL,
 * ADD32_ovflw, ...) are the reference macros pulled in via _kiss_fft_guts.h.
 *
 * The radix-3/4/5 kernels read st->twiddles. We rebuild the twiddle table for a
 * given nfft exactly as compute_twiddles() does (kf_cexp2 -> celt_cos_norm,
 * which is a non-static library function), export it to the Go side so both
 * implementations consume identical Q15 twiddles, and pass it to the kernels. */

#define INPUT_MAGIC "GKFI"
#define OUTPUT_MAGIC "GKFO"

enum {
  MODE_KF_BFLY2 = 0,
  MODE_KF_BFLY4 = 1,
  MODE_KF_BFLY3 = 2,
  MODE_KF_BFLY5 = 3,
  MODE_OPUS_FFT = 4,
  MODE_OPUS_IFFT = 5
};

/* Verbatim copy of the celt/kiss_fft.c kf_bfly2() non-CUSTOM_MODES path. */
static void ref_kf_bfly2(kiss_fft_cpx *Fout, int N) {
  kiss_fft_cpx *Fout2;
  int i;
  celt_coef tw;
  tw = QCONST32(0.7071067812f, COEF_SHIFT - 1);
  for (i = 0; i < N; i++) {
    kiss_fft_cpx t;
    Fout2 = Fout + 4;
    t = Fout2[0];
    C_SUB(Fout2[0], Fout[0], t);
    C_ADDTO(Fout[0], t);

    t.r = S_MUL(ADD32_ovflw(Fout2[1].r, Fout2[1].i), tw);
    t.i = S_MUL(SUB32_ovflw(Fout2[1].i, Fout2[1].r), tw);
    C_SUB(Fout2[1], Fout[1], t);
    C_ADDTO(Fout[1], t);

    t.r = Fout2[2].i;
    t.i = NEG32_ovflw(Fout2[2].r);
    C_SUB(Fout2[2], Fout[2], t);
    C_ADDTO(Fout[2], t);

    t.r = S_MUL(SUB32_ovflw(Fout2[3].i, Fout2[3].r), tw);
    t.i = S_MUL(NEG32_ovflw(ADD32_ovflw(Fout2[3].i, Fout2[3].r)), tw);
    C_SUB(Fout2[3], Fout[3], t);
    C_ADDTO(Fout[3], t);
    Fout += 8;
  }
}

/* Verbatim copy of the celt/kiss_fft.c kf_bfly4() body (both the m==1 degenerate
 * case and the general C_MUL path), with the twiddle table passed explicitly. */
static void ref_kf_bfly4(kiss_fft_cpx *Fout, const size_t fstride,
                         const kiss_twiddle_cpx *twiddles, int m, int N,
                         int mm) {
  int i;
  if (m == 1) {
    for (i = 0; i < N; i++) {
      kiss_fft_cpx scratch0, scratch1;
      C_SUB(scratch0, *Fout, Fout[2]);
      C_ADDTO(*Fout, Fout[2]);
      C_ADD(scratch1, Fout[1], Fout[3]);
      C_SUB(Fout[2], *Fout, scratch1);
      C_ADDTO(*Fout, scratch1);
      C_SUB(scratch1, Fout[1], Fout[3]);
      Fout[1].r = ADD32_ovflw(scratch0.r, scratch1.i);
      Fout[1].i = SUB32_ovflw(scratch0.i, scratch1.r);
      Fout[3].r = SUB32_ovflw(scratch0.r, scratch1.i);
      Fout[3].i = ADD32_ovflw(scratch0.i, scratch1.r);
      Fout += 4;
    }
  } else {
    int j;
    kiss_fft_cpx scratch[6];
    const kiss_twiddle_cpx *tw1, *tw2, *tw3;
    const int m2 = 2 * m;
    const int m3 = 3 * m;
    kiss_fft_cpx *Fout_beg = Fout;
    for (i = 0; i < N; i++) {
      Fout = Fout_beg + i * mm;
      tw3 = tw2 = tw1 = twiddles;
      for (j = 0; j < m; j++) {
        C_MUL(scratch[0], Fout[m], *tw1);
        C_MUL(scratch[1], Fout[m2], *tw2);
        C_MUL(scratch[2], Fout[m3], *tw3);
        C_SUB(scratch[5], *Fout, scratch[1]);
        C_ADDTO(*Fout, scratch[1]);
        C_ADD(scratch[3], scratch[0], scratch[2]);
        C_SUB(scratch[4], scratch[0], scratch[2]);
        C_SUB(Fout[m2], *Fout, scratch[3]);
        tw1 += fstride;
        tw2 += fstride * 2;
        tw3 += fstride * 3;
        C_ADDTO(*Fout, scratch[3]);
        Fout[m].r = ADD32_ovflw(scratch[5].r, scratch[4].i);
        Fout[m].i = SUB32_ovflw(scratch[5].i, scratch[4].r);
        Fout[m3].r = SUB32_ovflw(scratch[5].r, scratch[4].i);
        Fout[m3].i = ADD32_ovflw(scratch[5].i, scratch[4].r);
        ++Fout;
      }
    }
  }
}

/* Verbatim copy of the celt/kiss_fft.c kf_bfly3() FIXED_POINT body. */
static void ref_kf_bfly3(kiss_fft_cpx *Fout, const size_t fstride,
                         const kiss_twiddle_cpx *twiddles, int m, int N,
                         int mm) {
  int i;
  size_t k;
  const size_t m2 = 2 * m;
  const kiss_twiddle_cpx *tw1, *tw2;
  kiss_fft_cpx scratch[5];
  kiss_twiddle_cpx epi3;
  kiss_fft_cpx *Fout_beg = Fout;
  epi3.i = -QCONST32(0.86602540f, COEF_SHIFT - 1);
  for (i = 0; i < N; i++) {
    Fout = Fout_beg + i * mm;
    tw1 = tw2 = twiddles;
    k = m;
    do {
      C_MUL(scratch[1], Fout[m], *tw1);
      C_MUL(scratch[2], Fout[m2], *tw2);
      C_ADD(scratch[3], scratch[1], scratch[2]);
      C_SUB(scratch[0], scratch[1], scratch[2]);
      tw1 += fstride;
      tw2 += fstride * 2;
      Fout[m].r = SUB32_ovflw(Fout->r, HALF_OF(scratch[3].r));
      Fout[m].i = SUB32_ovflw(Fout->i, HALF_OF(scratch[3].i));
      C_MULBYSCALAR(scratch[0], epi3.i);
      C_ADDTO(*Fout, scratch[3]);
      Fout[m2].r = ADD32_ovflw(Fout[m].r, scratch[0].i);
      Fout[m2].i = SUB32_ovflw(Fout[m].i, scratch[0].r);
      Fout[m].r = SUB32_ovflw(Fout[m].r, scratch[0].i);
      Fout[m].i = ADD32_ovflw(Fout[m].i, scratch[0].r);
      ++Fout;
    } while (--k);
  }
}

/* Verbatim copy of the celt/kiss_fft.c kf_bfly5() FIXED_POINT body. */
static void ref_kf_bfly5(kiss_fft_cpx *Fout, const size_t fstride,
                         const kiss_twiddle_cpx *twiddles, int m, int N,
                         int mm) {
  kiss_fft_cpx *Fout0, *Fout1, *Fout2, *Fout3, *Fout4;
  int i, u;
  kiss_fft_cpx scratch[13];
  const kiss_twiddle_cpx *tw;
  kiss_twiddle_cpx ya, yb;
  kiss_fft_cpx *Fout_beg = Fout;
  ya.r = QCONST32(0.30901699f, COEF_SHIFT - 1);
  ya.i = -QCONST32(0.95105652f, COEF_SHIFT - 1);
  yb.r = -QCONST32(0.80901699f, COEF_SHIFT - 1);
  yb.i = -QCONST32(0.58778525f, COEF_SHIFT - 1);
  tw = twiddles;
  for (i = 0; i < N; i++) {
    Fout = Fout_beg + i * mm;
    Fout0 = Fout;
    Fout1 = Fout0 + m;
    Fout2 = Fout0 + 2 * m;
    Fout3 = Fout0 + 3 * m;
    Fout4 = Fout0 + 4 * m;
    for (u = 0; u < m; ++u) {
      scratch[0] = *Fout0;
      C_MUL(scratch[1], *Fout1, tw[u * fstride]);
      C_MUL(scratch[2], *Fout2, tw[2 * u * fstride]);
      C_MUL(scratch[3], *Fout3, tw[3 * u * fstride]);
      C_MUL(scratch[4], *Fout4, tw[4 * u * fstride]);
      C_ADD(scratch[7], scratch[1], scratch[4]);
      C_SUB(scratch[10], scratch[1], scratch[4]);
      C_ADD(scratch[8], scratch[2], scratch[3]);
      C_SUB(scratch[9], scratch[2], scratch[3]);
      Fout0->r = ADD32_ovflw(Fout0->r, ADD32_ovflw(scratch[7].r, scratch[8].r));
      Fout0->i = ADD32_ovflw(Fout0->i, ADD32_ovflw(scratch[7].i, scratch[8].i));
      scratch[5].r = ADD32_ovflw(scratch[0].r, ADD32_ovflw(S_MUL(scratch[7].r, ya.r), S_MUL(scratch[8].r, yb.r)));
      scratch[5].i = ADD32_ovflw(scratch[0].i, ADD32_ovflw(S_MUL(scratch[7].i, ya.r), S_MUL(scratch[8].i, yb.r)));
      scratch[6].r = ADD32_ovflw(S_MUL(scratch[10].i, ya.i), S_MUL(scratch[9].i, yb.i));
      scratch[6].i = NEG32_ovflw(ADD32_ovflw(S_MUL(scratch[10].r, ya.i), S_MUL(scratch[9].r, yb.i)));
      C_SUB(*Fout1, scratch[5], scratch[6]);
      C_ADD(*Fout4, scratch[5], scratch[6]);
      scratch[11].r = ADD32_ovflw(scratch[0].r, ADD32_ovflw(S_MUL(scratch[7].r, yb.r), S_MUL(scratch[8].r, ya.r)));
      scratch[11].i = ADD32_ovflw(scratch[0].i, ADD32_ovflw(S_MUL(scratch[7].i, yb.r), S_MUL(scratch[8].i, ya.r)));
      scratch[12].r = SUB32_ovflw(S_MUL(scratch[9].i, ya.i), S_MUL(scratch[10].i, yb.i));
      scratch[12].i = SUB32_ovflw(S_MUL(scratch[10].r, yb.i), S_MUL(scratch[9].r, ya.i));
      C_ADD(*Fout2, scratch[11], scratch[12]);
      C_SUB(*Fout3, scratch[11], scratch[12]);
      ++Fout0;
      ++Fout1;
      ++Fout2;
      ++Fout3;
      ++Fout4;
    }
  }
}

/* Verbatim copies of _celt_cos_pi_2()/celt_cos_norm() from celt/mathops.c. The
 * single-source dead-strip helper build does not link libopus, so we reproduce
 * the exact FIXED_POINT polynomial here (same fixed_generic.h macros) rather
 * than depend on the library symbol. */
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

/* Rebuild the FIXED_POINT (non-QEXT) twiddle table exactly as
 * compute_twiddles() does in celt/kiss_fft.c, expanding the kf_cexp2 macro with
 * our local celt_cos_norm copy. */
static void build_twiddles(kiss_twiddle_cpx *twiddles, int nfft) {
  int i;
  for (i = 0; i < nfft; ++i) {
    opus_val32 phase = -i;
    opus_val32 ph = DIV32(SHL32(phase, 17), nfft);
    twiddles[i].r = TRIG_UPSCALE * oracle_celt_cos_norm(ph);
    twiddles[i].i = TRIG_UPSCALE * oracle_celt_cos_norm(ph - 32768);
  }
}

/* Local copy of celt/kiss_fft.c kf_factor() (static in the library, behind
 * CUSTOM_MODES). Populates facbuf as p1,m1,p2,m2,... Returns 0 if n is not
 * factorable into radices 2..5. */
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

/* Build a standalone kiss_fft_state for nfft exactly as opus_fft_alloc(base=NULL)
 * does in the FIXED_POINT (non-QEXT) build. opus_fft_alloc itself lives behind
 * CUSTOM_MODES and is absent from the static-mode library, so we reconstruct the
 * state here and feed it to the linkable opus_fft_c/opus_ifft_c. Returns 0 on
 * failure. */
static int oracle_fft_alloc(kiss_fft_state *st, kiss_twiddle_cpx *twiddles,
                            opus_int16 *bitrev, int nfft) {
  st->nfft = nfft;
  st->scale_shift = celt_ilog2(nfft);
  if (nfft == 1 << st->scale_shift)
    st->scale = Q15ONE;
  else
    st->scale = (1073741824 + nfft / 2) / nfft >> (15 - st->scale_shift);
  st->shift = -1;
  build_twiddles(twiddles, nfft);
  st->twiddles = twiddles;
  if (!oracle_kf_factor(nfft, st->factors)) return 0;
  st->bitrev = bitrev;
  oracle_compute_bitrev_table(0, bitrev, 1, 1, st->factors);
  return 1;
}

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

static int write_u32(uint32_t value) { return write_exact(&value, sizeof(value)); }

/* kf_bfly2 path: header is {version=1, mode, count=N}, samples = 8*N. */
static int run_bfly2(uint32_t count) {
  uint32_t total = count * 8u;
  uint32_t i;
  kiss_fft_cpx *buf = (kiss_fft_cpx *)malloc(sizeof(kiss_fft_cpx) * (total ? total : 1));
  if (!buf) return 1;
  for (i = 0; i < total; i++) {
    uint32_t r, im;
    if (!read_u32(&r) || !read_u32(&im)) { free(buf); return 1; }
    buf[i].r = (kiss_fft_scalar)(int32_t)r;
    buf[i].i = (kiss_fft_scalar)(int32_t)im;
  }
  ref_kf_bfly2(buf, (int)count);
  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(total)) { free(buf); return 1; }
  for (i = 0; i < total; i++) {
    if (!write_u32((uint32_t)(int32_t)buf[i].r) || !write_u32((uint32_t)(int32_t)buf[i].i)) { free(buf); return 1; }
  }
  free(buf);
  return 0;
}

/* kf_bfly3/4/5 path: header is {version=1, mode, nfft}, then params
 * {fstride, m, N, mm, total} each as u32, then total complex samples.
 * Output: {magic, version=1, nfft, total}, then nfft twiddles (r,i as i32),
 * then total transformed samples. */
static int run_bfly_radix(uint32_t mode, uint32_t nfft) {
  uint32_t fstride, m, N, mm, total, i;
  kiss_twiddle_cpx *tw;
  kiss_fft_cpx *buf;

  if (!read_u32(&fstride) || !read_u32(&m) || !read_u32(&N) || !read_u32(&mm) || !read_u32(&total))
    return 1;

  tw = (kiss_twiddle_cpx *)malloc(sizeof(kiss_twiddle_cpx) * (nfft ? nfft : 1));
  buf = (kiss_fft_cpx *)malloc(sizeof(kiss_fft_cpx) * (total ? total : 1));
  if (!tw || !buf) { free(tw); free(buf); return 1; }

  build_twiddles(tw, (int)nfft);

  for (i = 0; i < total; i++) {
    uint32_t r, im;
    if (!read_u32(&r) || !read_u32(&im)) { free(tw); free(buf); return 1; }
    buf[i].r = (kiss_fft_scalar)(int32_t)r;
    buf[i].i = (kiss_fft_scalar)(int32_t)im;
  }

  switch (mode) {
    case MODE_KF_BFLY4: ref_kf_bfly4(buf, fstride, tw, (int)m, (int)N, (int)mm); break;
    case MODE_KF_BFLY3: ref_kf_bfly3(buf, fstride, tw, (int)m, (int)N, (int)mm); break;
    case MODE_KF_BFLY5: ref_kf_bfly5(buf, fstride, tw, (int)m, (int)N, (int)mm); break;
    default: free(tw); free(buf); return 1;
  }

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(nfft) || !write_u32(total)) {
    free(tw); free(buf); return 1;
  }
  for (i = 0; i < nfft; i++) {
    if (!write_u32((uint32_t)(int32_t)tw[i].r) || !write_u32((uint32_t)(int32_t)tw[i].i)) {
      free(tw); free(buf); return 1;
    }
  }
  for (i = 0; i < total; i++) {
    if (!write_u32((uint32_t)(int32_t)buf[i].r) || !write_u32((uint32_t)(int32_t)buf[i].i)) {
      free(tw); free(buf); return 1;
    }
  }
  free(tw);
  free(buf);
  return 0;
}

/* Full forward/inverse FFT path: header is {version=1, mode, nfft}, then total
 * (== nfft) complex input samples. Drives the real (non-static) library
 * opus_fft_c/opus_ifft_c against a standalone opus_fft_alloc state.
 *
 * Output: {magic, version=1, nfft, scale_shift, scale, shift}, then 2*MAXFACTORS
 * factors (i32), then nfft bitrev entries (i32), then nfft twiddles (r,i as
 * i32), then nfft transformed samples. Exporting the state lets the Go side
 * validate its constructor against the library's exact tables. */
static int run_opus_fft(uint32_t mode, uint32_t nfft) {
  uint32_t total, i;
  kiss_fft_state st;
  kiss_twiddle_cpx *twiddles;
  opus_int16 *bitrev;
  kiss_fft_cpx *fin;
  kiss_fft_cpx *fout;

  if (!read_u32(&total)) return 1;
  if (total != nfft) return 1;

  memset(&st, 0, sizeof(st));
  twiddles = (kiss_twiddle_cpx *)malloc(sizeof(kiss_twiddle_cpx) * (nfft ? nfft : 1));
  bitrev = (opus_int16 *)malloc(sizeof(opus_int16) * (nfft ? nfft : 1));
  fin = (kiss_fft_cpx *)malloc(sizeof(kiss_fft_cpx) * (total ? total : 1));
  fout = (kiss_fft_cpx *)malloc(sizeof(kiss_fft_cpx) * (total ? total : 1));
  if (!twiddles || !bitrev || !fin || !fout) { free(twiddles); free(bitrev); free(fin); free(fout); return 1; }

  if (!oracle_fft_alloc(&st, twiddles, bitrev, (int)nfft)) {
    free(twiddles); free(bitrev); free(fin); free(fout); return 1;
  }

  for (i = 0; i < total; i++) {
    uint32_t r, im;
    if (!read_u32(&r) || !read_u32(&im)) { free(twiddles); free(bitrev); free(fin); free(fout); return 1; }
    fin[i].r = (kiss_fft_scalar)(int32_t)r;
    fin[i].i = (kiss_fft_scalar)(int32_t)im;
  }

  if (mode == MODE_OPUS_FFT)
    opus_fft_c(&st, fin, fout);
  else
    opus_ifft_c(&st, fin, fout);

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(nfft) ||
      !write_u32((uint32_t)(int32_t)st.scale_shift) ||
      !write_u32((uint32_t)(int32_t)st.scale) ||
      !write_u32((uint32_t)(int32_t)st.shift)) {
    free(twiddles); free(bitrev); free(fin); free(fout); return 1;
  }
  for (i = 0; i < 2 * MAXFACTORS; i++) {
    if (!write_u32((uint32_t)(int32_t)st.factors[i])) {
      free(twiddles); free(bitrev); free(fin); free(fout); return 1;
    }
  }
  for (i = 0; i < nfft; i++) {
    if (!write_u32((uint32_t)(int32_t)st.bitrev[i])) {
      free(twiddles); free(bitrev); free(fin); free(fout); return 1;
    }
  }
  for (i = 0; i < nfft; i++) {
    if (!write_u32((uint32_t)(int32_t)st.twiddles[i].r) ||
        !write_u32((uint32_t)(int32_t)st.twiddles[i].i)) {
      free(twiddles); free(bitrev); free(fin); free(fout); return 1;
    }
  }
  for (i = 0; i < total; i++) {
    if (!write_u32((uint32_t)(int32_t)fout[i].r) || !write_u32((uint32_t)(int32_t)fout[i].i)) {
      free(twiddles); free(bitrev); free(fin); free(fout); return 1;
    }
  }
  free(twiddles);
  free(bitrev);
  free(fin);
  free(fout);
  return 0;
}

int main(void) {
  char magic[4];
  uint32_t version;
  uint32_t mode;
  uint32_t arg; /* count for bfly2; nfft for radix-3/4/5 */

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) return 1;
  if (!read_u32(&version) || version != 1 || !read_u32(&mode) || !read_u32(&arg)) return 1;

  switch (mode) {
    case MODE_KF_BFLY2: return run_bfly2(arg);
    case MODE_KF_BFLY4:
    case MODE_KF_BFLY3:
    case MODE_KF_BFLY5: return run_bfly_radix(mode, arg);
    case MODE_OPUS_FFT:
    case MODE_OPUS_IFFT: return run_opus_fft(mode, arg);
    default: return 1;
  }
}
