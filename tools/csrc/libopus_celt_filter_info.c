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
#include "celt.h"

#define GCFI_MAGIC "GCFI"
#define GCFO_MAGIC "GCFO"

enum {
  MODE_DEEMPHASIS = 0,
  MODE_COMB_FILTER = 1,
  MODE_COMB_FILTER_INPUT = 2
};

void deemphasis(celt_sig *in[], opus_res *pcm, int N, int C, int downsample, const opus_val16 *coef, celt_sig *mem, int accum);
void comb_filter(opus_val32 *y, opus_val32 *x, int T0, int T1, int N, opus_val16 g0, opus_val16 g1, int tapset0, int tapset1, const celt_coef *window, int overlap, int arch);

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

static int read_float(float *out) {
  union {
    uint32_t u;
    float f;
  } bits;
  if (!read_u32(&bits.u)) return 0;
  *out = bits.f;
  return 1;
}

static int write_float(float v) {
  union {
    float f;
    uint32_t u;
  } bits;
  bits.f = v;
  return write_u32(bits.u);
}

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

static int run_deemphasis(void) {
  uint32_t channels = 0;
  uint32_t n = 0;
  uint32_t downsample = 0;
  uint32_t accum = 0;
  opus_val16 coef[4];
  celt_sig mem[2];
  celt_sig *input = NULL;
  celt_sig *planes[2] = {NULL, NULL};
  opus_res *pcm = NULL;
  uint32_t nd;
  uint32_t count;
  uint32_t i;

  if (!read_u32(&channels) || !read_u32(&n) || !read_u32(&downsample) || !read_u32(&accum)) {
    fprintf(stderr, "failed to read deemphasis header\n");
    return 0;
  }
  if (channels == 0 || channels > 2 || n == 0 || downsample == 0 || downsample > n || accum > 1) {
    fprintf(stderr, "invalid deemphasis dimensions\n");
    return 0;
  }
  for (i = 0; i < 4; i++) {
    float v;
    if (!read_float(&v)) return 0;
    coef[i] = (opus_val16)v;
  }
  for (i = 0; i < channels; i++) {
    float v;
    if (!read_float(&v)) return 0;
    mem[i] = (celt_sig)v;
  }

  count = channels * n;
  input = (celt_sig *)malloc((size_t)count * sizeof(celt_sig));
  nd = n / downsample;
  pcm = (opus_res *)calloc((size_t)(channels * nd), sizeof(opus_res));
  if (input == NULL || pcm == NULL) {
    free(input);
    free(pcm);
    return 0;
  }
  for (i = 0; i < count; i++) {
    float v;
    if (!read_float(&v)) {
      free(input);
      free(pcm);
      return 0;
    }
    input[i] = (celt_sig)v;
  }
  /* In accum mode the pcm buffer holds the SILK lowband output that CELT's
     deemphasis adds onto (the hybrid SILK+CELT combine in opus_decoder.c).
     Seed it with the supplied interleaved values so we exercise the real
     ADD_RES(y[j*C], SIG2RES(tmp)) accumulation rather than 0 + celt. */
  if (accum) {
    for (i = 0; i < channels * nd; i++) {
      float v;
      if (!read_float(&v)) {
        free(input);
        free(pcm);
        return 0;
      }
      pcm[i] = (opus_res)v;
    }
  }
  planes[0] = input;
  if (channels == 2) planes[1] = input + n;

  deemphasis(planes, pcm, (int)n, (int)channels, (int)downsample, coef, mem, (int)accum);

  if (!write_u32(channels * nd)) {
    free(input);
    free(pcm);
    return 0;
  }
  for (i = 0; i < channels; i++) {
    if (!write_float((float)mem[i])) {
      free(input);
      free(pcm);
      return 0;
    }
  }
  for (i = 0; i < channels * nd; i++) {
    if (!write_float((float)pcm[i])) {
      free(input);
      free(pcm);
      return 0;
    }
  }

  free(input);
  free(pcm);
  return 1;
}

static int run_comb_filter(int separate_input) {
  uint32_t start = 0;
  uint32_t n = 0;
  uint32_t t0 = 0;
  uint32_t t1 = 0;
  uint32_t tapset0 = 0;
  uint32_t tapset1 = 0;
  uint32_t overlap = 0;
  uint32_t total = 0;
  float g0f = 0;
  float g1f = 0;
  opus_val32 *buf = NULL;
  opus_val32 *y = NULL;
  celt_coef *window = NULL;
  uint32_t i;

  if (!read_u32(&start) || !read_u32(&n) || !read_u32(&t0) || !read_u32(&t1) ||
      !read_u32(&tapset0) || !read_u32(&tapset1) || !read_u32(&overlap) ||
      !read_float(&g0f) || !read_float(&g1f)) {
    fprintf(stderr, "failed to read comb filter header\n");
    return 0;
  }
  if (start < 2 || n == 0 || tapset0 > 2 || tapset1 > 2 || overlap > n || overlap > 240) {
    fprintf(stderr, "invalid comb filter dimensions\n");
    return 0;
  }
  if (n > UINT32_MAX - start - 2) {
    fprintf(stderr, "comb filter buffer overflow\n");
    return 0;
  }
  total = start + n + 2;
  window = (celt_coef *)malloc((size_t)overlap * sizeof(celt_coef));
  buf = (opus_val32 *)malloc((size_t)total * sizeof(opus_val32));
  y = separate_input ? (opus_val32 *)malloc((size_t)n * sizeof(opus_val32)) : NULL;
  if ((overlap > 0 && window == NULL) || buf == NULL || (separate_input && y == NULL)) {
    free(window);
    free(buf);
    free(y);
    return 0;
  }
  for (i = 0; i < overlap; i++) {
    float v;
    if (!read_float(&v)) {
      free(window);
      free(buf);
      free(y);
      return 0;
    }
    window[i] = (celt_coef)v;
  }
  for (i = 0; i < total; i++) {
    float v;
    if (!read_float(&v)) {
      free(window);
      free(buf);
      free(y);
      return 0;
    }
    buf[i] = (opus_val32)v;
  }

  comb_filter(separate_input ? y : buf + start, buf + start, (int)t0, (int)t1, (int)n,
      (opus_val16)g0f, (opus_val16)g1f, (int)tapset0, (int)tapset1,
      overlap > 0 ? window : NULL, (int)overlap, 0);

  if (!write_u32(n)) {
    free(window);
    free(buf);
    free(y);
    return 0;
  }
  for (i = 0; i < n; i++) {
    if (!write_float((float)(separate_input ? y[i] : buf[start + i]))) {
      free(window);
      free(buf);
      free(y);
      return 0;
    }
  }

  free(window);
  free(buf);
  free(y);
  return 1;
}

int main(void) {
  unsigned char magic[4];
  uint32_t version = 0;
  uint32_t mode = 0;
  int ok = 0;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, GCFI_MAGIC, sizeof(magic)) != 0) {
    fprintf(stderr, "invalid input magic\n");
    return 1;
  }
  if (!read_u32(&version) || version != 1 || !read_u32(&mode)) {
    fprintf(stderr, "invalid input header\n");
    return 1;
  }
  if (!write_exact(GCFO_MAGIC, 4) || !write_u32(1) || !write_u32(mode)) {
    fprintf(stderr, "failed to write output header\n");
    return 1;
  }

  if (mode == MODE_DEEMPHASIS) {
    ok = run_deemphasis();
  } else if (mode == MODE_COMB_FILTER) {
    ok = run_comb_filter(0);
  } else if (mode == MODE_COMB_FILTER_INPUT) {
    ok = run_comb_filter(1);
  } else {
    fprintf(stderr, "unsupported mode\n");
    return 1;
  }
  if (!ok) return 1;
  return 0;
}
