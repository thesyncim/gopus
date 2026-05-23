#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "celt/celt.h"

#define INPUT_MAGIC "GCPI"
#define OUTPUT_MAGIC "GCPO"

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

static int read_f32(float *out) {
  uint32_t bits;
  if (!read_u32(&bits)) return 0;
  memcpy(out, &bits, sizeof(bits));
  return 1;
}

static int write_f32(float value) {
  uint32_t bits;
  memcpy(&bits, &value, sizeof(bits));
  return write_u32(bits);
}

int main(void) {
  char magic[4];
  uint32_t version;
  uint32_t count;
  uint32_t case_idx;
  static const opus_val16 coef[4] = {0.8500061035f, 0.0f, 1.0f, 1.0f};

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) return 1;
  if (!read_u32(&version) || version != 1 || !read_u32(&count)) return 1;

  if (!write_exact(OUTPUT_MAGIC, sizeof(magic)) || !write_u32(1) || !write_u32(count)) return 1;
  for (case_idx = 0; case_idx < count; case_idx++) {
    uint32_t channels;
    uint32_t n;
    uint32_t i;
    uint32_t ch;
    opus_res *pcm;
    celt_sig *out;
    celt_sig mem[2];

    if (!read_u32(&channels) || !read_u32(&n)) return 1;
    if (channels < 1 || channels > 2) return 1;
    mem[0] = 0;
    mem[1] = 0;
    for (ch = 0; ch < channels; ch++) {
      float v;
      if (!read_f32(&v)) return 1;
      mem[ch] = (celt_sig)v;
    }

    pcm = NULL;
    out = NULL;
    if (n > 0) {
      pcm = (opus_res *)malloc(sizeof(*pcm) * n * channels);
      out = (celt_sig *)calloc(n * channels, sizeof(*out));
      if (pcm == NULL || out == NULL) {
        free(pcm);
        free(out);
        return 1;
      }
    }
    for (i = 0; i < n * channels; i++) {
      float v;
      if (!read_f32(&v)) {
        free(pcm);
        free(out);
        return 1;
      }
      pcm[i] = (opus_res)v;
    }

    for (ch = 0; ch < channels; ch++) {
      celt_preemphasis(pcm + ch, out + ch * n, (int)n, (int)channels, 1, coef, &mem[ch], 0);
    }
    for (i = 0; i < n; i++) {
      for (ch = 0; ch < channels; ch++) {
        if (!write_f32((float)out[ch * n + i])) {
          free(pcm);
          free(out);
          return 1;
        }
      }
    }
    for (ch = 0; ch < channels; ch++) {
      if (!write_f32((float)mem[ch])) {
        free(pcm);
        free(out);
        return 1;
      }
    }

    free(pcm);
    free(out);
  }
  return 0;
}
