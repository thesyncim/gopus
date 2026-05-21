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

#include "celt.h"
#include "mdct.h"
#include "modes.h"

#define GCII_MAGIC "GCII"
#define GCIO_MAGIC "GCIO"

#if defined(ENABLE_ASSERTIONS) || defined(ENABLE_HARDENING)
#ifdef __GNUC__
__attribute__((noreturn))
#endif
void celt_fatal(const char *str, const char *file, int line) {
  fprintf(stderr, "Fatal (internal) error in %s, line %d: %s\n", file, line, str);
  abort();
}
#endif

enum {
  MODE_LONG = 0,
  MODE_TRANSIENT = 1,
  MODE_FFT = 2
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

static int frame_lm(uint32_t frame_size) {
  if (frame_size == 120) return 0;
  if (frame_size == 240) return 1;
  if (frame_size == 480) return 2;
  if (frame_size == 960) return 3;
  return -1;
}

static int read_float_array(celt_sig *dst, uint32_t n) {
  uint32_t i;
  for (i = 0; i < n; i++) {
    float v;
    if (!read_float(&v)) return 0;
    dst[i] = (celt_sig)v;
  }
  return 1;
}

static int write_float_array(const celt_sig *src, uint32_t n) {
  uint32_t i;
  for (i = 0; i < n; i++) {
    if (!write_float((float)src[i])) return 0;
  }
  return 1;
}

static int run_long(const CELTMode *mode, uint32_t frame_size, uint32_t overlap) {
  celt_sig *freq = NULL;
  celt_sig *out = NULL;
  uint32_t needed;
  int lm;
  int shift;
  int ok;

  lm = frame_lm(frame_size);
  if (lm < 0 || overlap != (uint32_t)mode->overlap) {
    fprintf(stderr, "invalid long IMDCT dimensions\n");
    return 0;
  }
  shift = mode->maxLM - lm;
  needed = frame_size + overlap;

  freq = (celt_sig *)malloc((size_t)frame_size * sizeof(celt_sig));
  out = (celt_sig *)calloc((size_t)needed, sizeof(celt_sig));
  if (freq == NULL || out == NULL) {
    free(freq);
    free(out);
    return 0;
  }

  ok = read_float_array(out, overlap) && read_float_array(freq, frame_size);
  if (ok) {
    clt_mdct_backward(&mode->mdct, freq, out, mode->window, (int)overlap, shift, 1, 0);
    ok = write_u32(needed) && write_float_array(out, needed);
  }

  free(freq);
  free(out);
  return ok;
}

static int run_transient(const CELTMode *mode, uint32_t frame_size, uint32_t overlap, uint32_t short_blocks) {
  celt_sig *freq = NULL;
  celt_sig *out = NULL;
  uint32_t short_size;
  uint32_t needed;
  uint32_t b;
  int lm;
  int ok;

  lm = frame_lm(frame_size);
  if (lm < 0 || short_blocks != (uint32_t)(1U << lm) || short_blocks == 0 ||
      overlap != (uint32_t)mode->overlap) {
    fprintf(stderr, "invalid transient IMDCT dimensions\n");
    return 0;
  }
  short_size = frame_size / short_blocks;
  needed = frame_size + overlap;

  freq = (celt_sig *)malloc((size_t)frame_size * sizeof(celt_sig));
  out = (celt_sig *)calloc((size_t)needed, sizeof(celt_sig));
  if (freq == NULL || out == NULL) {
    free(freq);
    free(out);
    return 0;
  }

  ok = read_float_array(out, overlap) && read_float_array(freq, frame_size);
  if (ok) {
    for (b = 0; b < short_blocks; b++) {
      clt_mdct_backward(&mode->mdct, freq + b, out + short_size * b,
          mode->window, (int)overlap, mode->maxLM, (int)short_blocks, 0);
    }
    ok = write_u32(needed) && write_float_array(out, needed);
  }

  free(freq);
  free(out);
  return ok;
}

static int fft_shift_for_nfft(uint32_t nfft) {
  if (nfft == 480) return 0;
  if (nfft == 240) return 1;
  if (nfft == 120) return 2;
  if (nfft == 60) return 3;
  return -1;
}

static int run_fft(const CELTMode *mode, uint32_t nfft) {
  kiss_fft_cpx *fin = NULL;
  kiss_fft_cpx *fout = NULL;
  const kiss_fft_state *st = NULL;
  int shift;
  uint32_t i;
  int ok = 1;

  shift = fft_shift_for_nfft(nfft);
  if (shift < 0) {
    fprintf(stderr, "invalid FFT size\n");
    return 0;
  }
  st = mode->mdct.kfft[shift];
  if (st == NULL || st->nfft != (int)nfft) {
    fprintf(stderr, "missing FFT state\n");
    return 0;
  }

  fin = (kiss_fft_cpx *)malloc((size_t)nfft * sizeof(kiss_fft_cpx));
  fout = (kiss_fft_cpx *)calloc((size_t)nfft, sizeof(kiss_fft_cpx));
  if (fin == NULL || fout == NULL) {
    free(fin);
    free(fout);
    return 0;
  }
  for (i = 0; i < nfft; i++) {
    float r;
    float im;
    if (!read_float(&r) || !read_float(&im)) {
      ok = 0;
      break;
    }
    fin[i].r = (kiss_fft_scalar)r;
    fin[i].i = (kiss_fft_scalar)im;
  }
  if (ok) {
    for (i = 0; i < nfft; i++) {
      fout[st->bitrev[i]] = fin[i];
    }
    opus_fft_impl(st, fout);
    ok = write_u32(nfft);
    for (i = 0; ok && i < nfft; i++) {
      ok = write_float((float)fout[i].r) && write_float((float)fout[i].i);
    }
  }

  free(fin);
  free(fout);
  return ok;
}

int main(void) {
  unsigned char magic[4];
  uint32_t version = 0;
  uint32_t mode_id = 0;
  uint32_t frame_size = 0;
  uint32_t overlap = 0;
  uint32_t short_blocks = 0;
  int err = 0;
  CELTMode *mode = NULL;
  int ok = 0;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, GCII_MAGIC, sizeof(magic)) != 0) {
    fprintf(stderr, "invalid input magic\n");
    return 1;
  }
  if (!read_u32(&version) || version != 1 || !read_u32(&mode_id) ||
      !read_u32(&frame_size) || !read_u32(&overlap) || !read_u32(&short_blocks)) {
    fprintf(stderr, "invalid input header\n");
    return 1;
  }

  mode = (CELTMode *)opus_custom_mode_create(48000, mode_id == MODE_FFT ? 960 : (int)frame_size, &err);
  if (mode == NULL || err != OPUS_OK) {
    fprintf(stderr, "failed to create CELT mode\n");
    return 1;
  }

  if (!write_exact(GCIO_MAGIC, 4) || !write_u32(1) || !write_u32(mode_id)) {
    fprintf(stderr, "failed to write output header\n");
    return 1;
  }

  if (mode_id == MODE_LONG) {
    ok = run_long(mode, frame_size, overlap);
  } else if (mode_id == MODE_TRANSIENT) {
    ok = run_transient(mode, frame_size, overlap, short_blocks);
  } else if (mode_id == MODE_FFT) {
    ok = run_fft(mode, frame_size);
  } else {
    fprintf(stderr, "unsupported mode\n");
    return 1;
  }
  if (!ok) return 1;
  return 0;
}
