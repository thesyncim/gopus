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

enum {
  MODE_LONG = 0,
  MODE_TRANSIENT = 1,
  MODE_FORWARD = 2,
  MODE_FORWARD_TRANSIENT = 3
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

/* Long-block inverse MDCT: shift = maxLM - LM. For the 96 kHz 20 ms frame
 * (frame_size = 1920 = shortMdctSize * nbShortMdcts), LM = maxLM so shift = 0. */
static int run_long(const CELTMode *mode, uint32_t frame_size, uint32_t overlap) {
  celt_sig *freq = NULL;
  celt_sig *out = NULL;
  uint32_t needed;
  int ok;

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
    clt_mdct_backward(&mode->mdct, freq, out, mode->window, (int)overlap, 0, 1, 0);
    ok = write_u32(needed) && write_float_array(out, needed);
  }

  free(freq);
  free(out);
  return ok;
}

/* Transient inverse MDCT: nbShortMdcts blocks at shift = maxLM, stride
 * short_blocks, overlap-added into a shared buffer. */
static int run_transient(const CELTMode *mode, uint32_t frame_size, uint32_t overlap, uint32_t short_blocks) {
  celt_sig *freq = NULL;
  celt_sig *out = NULL;
  uint32_t short_size;
  uint32_t needed;
  uint32_t b;
  int ok;

  if (short_blocks == 0 || frame_size % short_blocks != 0) {
    fprintf(stderr, "invalid transient dimensions\n");
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

/* Long-block forward MDCT: shift = 0, stride 1. */
static int run_forward(const CELTMode *mode, uint32_t frame_size, uint32_t overlap) {
  celt_sig *in = NULL;
  celt_sig *out = NULL;
  uint32_t needed;
  int ok;

  needed = frame_size + overlap;
  in = (celt_sig *)malloc((size_t)needed * sizeof(celt_sig));
  out = (celt_sig *)calloc((size_t)frame_size, sizeof(celt_sig));
  if (in == NULL || out == NULL) {
    free(in);
    free(out);
    return 0;
  }

  ok = read_float_array(in, needed);
  if (ok) {
    clt_mdct_forward(&mode->mdct, in, out, mode->window, (int)overlap, 0, 1, 0);
    ok = write_u32(frame_size) && write_float_array(out, frame_size);
  }

  free(in);
  free(out);
  return ok;
}

/* Transient forward MDCT: nbShortMdcts blocks at shift = maxLM, stride
 * short_blocks, output interleaved as out[b + i*short_blocks]. */
static int run_forward_transient(const CELTMode *mode, uint32_t frame_size, uint32_t overlap, uint32_t short_blocks) {
  celt_sig *in = NULL;
  celt_sig *out = NULL;
  uint32_t short_size;
  uint32_t b;
  int ok;

  if (short_blocks == 0 || frame_size % short_blocks != 0) {
    fprintf(stderr, "invalid forward transient dimensions\n");
    return 0;
  }
  short_size = frame_size / short_blocks;

  in = (celt_sig *)malloc(((size_t)frame_size + overlap) * sizeof(celt_sig));
  out = (celt_sig *)calloc((size_t)frame_size, sizeof(celt_sig));
  if (in == NULL || out == NULL) {
    free(in);
    free(out);
    return 0;
  }

  ok = read_float_array(in, frame_size + overlap);
  if (ok) {
    for (b = 0; b < short_blocks; b++) {
      clt_mdct_forward(&mode->mdct, in + short_size * b, out + b,
          mode->window, (int)overlap, mode->maxLM, (int)short_blocks, 0);
    }
    ok = write_u32(frame_size) && write_float_array(out, frame_size);
  }

  free(in);
  free(out);
  return ok;
}

int main(void) {
  unsigned char magic[4];
  uint32_t version = 0;
  uint32_t op = 0;
  uint32_t frame_size = 0;
  uint32_t overlap = 0;
  uint32_t short_blocks = 0;
  int err = 0;
  CELTMode *mode = NULL;
  int ok = 0;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, GQXI_MAGIC, sizeof(magic)) != 0) {
    fprintf(stderr, "invalid input magic\n");
    return 1;
  }
  if (!read_u32(&version) || version != 1 || !read_u32(&op) ||
      !read_u32(&frame_size) || !read_u32(&overlap) || !read_u32(&short_blocks)) {
    fprintf(stderr, "invalid input header\n");
    return 1;
  }

  /* Native 96 kHz mode: opus_custom_mode_create(96000, 1920). Selected from the
   * QEXT-enabled static_mode_list (mode96000_1920_240). */
  mode = (CELTMode *)opus_custom_mode_create(96000, 1920, &err);
  if (mode == NULL || err != OPUS_OK) {
    fprintf(stderr, "failed to create 96 kHz CELT mode\n");
    return 1;
  }
  if (overlap != (uint32_t)mode->overlap) {
    fprintf(stderr, "overlap mismatch (got %u want %d)\n", overlap, mode->overlap);
    return 1;
  }

  if (!write_exact(GQXO_MAGIC, 4) || !write_u32(1) || !write_u32(op)) {
    fprintf(stderr, "failed to write output header\n");
    return 1;
  }

  if (op == MODE_LONG) {
    ok = run_long(mode, frame_size, overlap);
  } else if (op == MODE_TRANSIENT) {
    ok = run_transient(mode, frame_size, overlap, short_blocks);
  } else if (op == MODE_FORWARD) {
    ok = run_forward(mode, frame_size, overlap);
  } else if (op == MODE_FORWARD_TRANSIENT) {
    ok = run_forward_transient(mode, frame_size, overlap, short_blocks);
  } else {
    fprintf(stderr, "unknown op\n");
    return 1;
  }
  if (!ok) return 1;
  return 0;
}
