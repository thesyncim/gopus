#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "celt/arch.h"
#include "celt/_kiss_fft_guts.h"
#include "celt/mathops.h"
#include "celt/mdct.h"

/* The static mode table only uses these as metadata pointers. The MDCT oracle
 * accesses mdct/window and keeps the arrays local so it can include the pinned
 * static table without linking a second modes.c definition. */
static const opus_int16 eband5ms[] = {0};
static const unsigned char band_allocation[] = {0};
#include "celt/static_modes_fixed.h"

#if !defined(FIXED_POINT) || !defined(ENABLE_QEXT)
#error "QEXT MDCT oracle requires FIXED_POINT + ENABLE_QEXT"
#endif

static int read_exact(void *dst, size_t n) { return fread(dst, 1, n, stdin) == n; }
static int write_exact(const void *src, size_t n) { return fwrite(src, 1, n, stdout) == n; }

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact(b, sizeof(b))) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) |
      ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int write_u32(uint32_t value) {
  unsigned char b[4] = {
    (unsigned char)value, (unsigned char)(value >> 8),
    (unsigned char)(value >> 16), (unsigned char)(value >> 24)
  };
  return write_exact(b, sizeof(b));
}

int main(void) {
  unsigned char magic[4];
  uint32_t version, mode, shift, stride, n, overlap, i, count;
  int headroom = 0;
  const CELTMode *m;
  kiss_fft_scalar *in, *out, *fold;
  kiss_fft_cpx *pre, *pre_raw, *fft_raw;
  kiss_fft_scalar *post;
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 1;
#endif
  if (fread(magic, 1, 4, stdin) != 4 || magic[0] != 'G' || magic[1] != 'Q' ||
      magic[2] != 'M' || magic[3] != 'I' || !read_u32(&version) || version != 1 ||
      !read_u32(&mode) || !read_u32(&shift) || !read_u32(&stride)) return 1;
  if (mode == 0) m = &mode48000_960_120;
  else if (mode == 1) m = &mode96000_1920_240;
  else return 1;
  n = (uint32_t)m->mdct.n;
  overlap = (uint32_t)m->overlap;
  if (shift > 3 || stride == 0 || stride > 8) return 1;
  count = stride * ((n >> shift >> 1) - 1) + 1;
  fold = (kiss_fft_scalar *)calloc(n >> shift >> 1, sizeof(*fold));
  pre = (kiss_fft_cpx *)calloc(n >> shift >> 2, sizeof(*pre));
  pre_raw = (kiss_fft_cpx *)calloc(n >> shift >> 2, sizeof(*pre_raw));
  fft_raw = (kiss_fft_cpx *)calloc(n >> shift >> 2, sizeof(*fft_raw));
  post = (kiss_fft_scalar *)calloc(6 * (n >> shift >> 2), sizeof(*post));
  in = (kiss_fft_scalar *)calloc(n, sizeof(*in));
  out = (kiss_fft_scalar *)calloc(count, sizeof(*out));
  if (fold == NULL || pre == NULL || pre_raw == NULL || fft_raw == NULL ||
      post == NULL || in == NULL || out == NULL) return 1;
  for (i = 0; i < n; i++) {
    uint32_t v;
    if (!read_u32(&v)) return 1;
    in[i] = (kiss_fft_scalar)(int32_t)v;
  }
  {
    int N = (int)(n >> shift);
    int N2 = N >> 1;
    int N4 = N >> 2;
    int i;
    const kiss_fft_scalar *xp1 = in + (overlap >> 1);
    const kiss_fft_scalar *xp2 = in + N2 - 1 + (overlap >> 1);
    kiss_fft_scalar *yp = fold;
    const celt_coef *wp1 = m->window + (overlap >> 1);
    const celt_coef *wp2 = m->window + (overlap >> 1) - 1;
    for (i = 0; i < ((overlap + 3) >> 2); i++) {
      *yp++ = ADD32_ovflw(S_MUL(xp1[N2], *wp2), S_MUL(*xp2, *wp1));
      *yp++ = SUB32_ovflw(S_MUL(*xp1, *wp1), S_MUL(xp2[-N2], *wp2));
      xp1 += 2; xp2 -= 2; wp1 += 2; wp2 -= 2;
    }
    wp1 = m->window;
    wp2 = m->window + overlap - 1;
    for (; i < N4 - ((overlap + 3) >> 2); i++) {
      *yp++ = *xp2;
      *yp++ = *xp1;
      xp1 += 2; xp2 -= 2;
    }
    for (; i < N4; i++) {
      *yp++ = ADD32_ovflw(-S_MUL(xp1[-N2], *wp1), S_MUL(*xp2, *wp2));
      *yp++ = ADD32_ovflw(S_MUL(*xp1, *wp2), S_MUL(xp2[N2], *wp1));
      xp1 += 2; xp2 -= 2; wp1 += 2; wp2 -= 2;
    }
  }
  {
    int N = (int)(n >> shift);
    int N4 = N >> 2;
    int i;
    int Nfull = (int)n;
    const kiss_twiddle_scalar *trig = m->mdct.trig;
    const opus_int16 *bitrev = m->mdct.kfft[shift]->bitrev;
    for (i = 0; i < shift; i++) {
      Nfull >>= 1;
      trig += Nfull;
    }
    for (i = 0; i < N4; i++) {
      kiss_fft_scalar re = fold[2*i];
      kiss_fft_scalar im = fold[2*i+1];
      kiss_fft_scalar yr = SUB32_ovflw(S_MUL(re, trig[i]), S_MUL(im, trig[N4+i]));
      kiss_fft_scalar yi = ADD32_ovflw(S_MUL(im, trig[i]), S_MUL(re, trig[N4+i]));
      pre[bitrev[i]].r = yr;
      pre[bitrev[i]].i = yi;
    }
  }
  memcpy(pre_raw, pre, (n >> shift >> 2) * sizeof(*pre));
  {
    const kiss_fft_state *st = m->mdct.kfft[shift];
    opus_val32 maxval = 1;
    int scale_shift = st->scale_shift - 1;
    int i;
    for (i = 0; i < (int)(n >> shift >> 2); i++) {
      maxval = MAX32(maxval, MAX32(ABS32(pre[i].r), ABS32(pre[i].i)));
    }
    headroom = IMAX(0, IMIN(scale_shift, 28-celt_ilog2(maxval)));
    opus_fft_impl(st, pre, scale_shift-headroom);
  }
  memcpy(fft_raw, pre, (n >> shift >> 2) * sizeof(*pre));
  {
    int N = (int)(n >> shift);
    int N2 = N >> 1;
    int N4 = N >> 2;
    const kiss_twiddle_scalar *trig = m->mdct.trig;
    const kiss_fft_state *st = m->mdct.kfft[shift];
    opus_val32 scale = st->scale;
    int i;
    int Nfull = (int)n;
    for (i = 0; i < shift; i++) {
      Nfull >>= 1;
      trig += Nfull;
    }
    for (i = 0; i < N4; i++) {
      kiss_fft_scalar t0 = S_MUL2(trig[i], scale);
      kiss_fft_scalar t1 = S_MUL2(trig[N4+i], scale);
      kiss_fft_scalar a = S_MUL(fft_raw[i].i, t1);
      kiss_fft_scalar b = S_MUL(fft_raw[i].r, t0);
      kiss_fft_scalar c = S_MUL(fft_raw[i].r, t1);
      kiss_fft_scalar d = S_MUL(fft_raw[i].i, t0);
      post[6*i+0] = t0;
      post[6*i+1] = t1;
      post[6*i+2] = a;
      post[6*i+3] = b;
      post[6*i+4] = c;
      post[6*i+5] = d;
      (void)N2;
    }
  }
  clt_mdct_forward_c(&m->mdct, in, out, m->window, (int)overlap,
      (int)shift, (int)stride, 0);
  if (!write_exact("GQMO", 4) || !write_u32(1) ||
      !write_u32(n >> shift >> 1) || !write_u32(n >> shift >> 2) ||
      !write_u32(n >> shift >> 2) || !write_u32(6 * (n >> shift >> 2)) ||
      !write_u32((uint32_t)headroom) || !write_u32(count)) return 1;
  for (i = 0; i < (n >> shift >> 1); i++) {
    if (!write_u32((uint32_t)(int32_t)fold[i])) return 1;
  }
  for (i = 0; i < (n >> shift >> 2); i++) {
    if (!write_u32((uint32_t)(int32_t)pre_raw[i].r) ||
        !write_u32((uint32_t)(int32_t)pre_raw[i].i)) return 1;
  }
  for (i = 0; i < (n >> shift >> 2); i++) {
    if (!write_u32((uint32_t)(int32_t)fft_raw[i].r) ||
        !write_u32((uint32_t)(int32_t)fft_raw[i].i)) return 1;
  }
  for (i = 0; i < 6 * (n >> shift >> 2); i++) {
    if (!write_u32((uint32_t)(int32_t)post[i])) return 1;
  }
  for (i = 0; i < count; i++) {
    if (!write_u32((uint32_t)(int32_t)out[i])) return 1;
  }
  free(out);
  free(in);
  free(fold);
  free(pre);
  free(pre_raw);
  free(fft_raw);
  free(post);
  return 0;
}
