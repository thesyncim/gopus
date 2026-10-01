#include <stdint.h>
#include <stdio.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#ifdef HAVE_CONFIG_H
#include "config.h"
#endif

#include "freq.h"

#define INPUT_MAGIC "GBCI"
#define OUTPUT_MAGIC "GBCO"

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

static int read_u32(uint32_t *v) {
  unsigned char b[4];
  if (!read_exact(b, sizeof(b))) return 0;
  *v = (uint32_t)b[0] | ((uint32_t)b[1] << 8) |
      ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int write_u32(uint32_t v) {
  unsigned char b[4];
  b[0] = (unsigned char)v;
  b[1] = (unsigned char)(v >> 8);
  b[2] = (unsigned char)(v >> 16);
  b[3] = (unsigned char)(v >> 24);
  return write_exact(b, sizeof(b));
}

static int read_bits_array(float *dst, int count) {
  int i;
  for (i = 0; i < count; i++) {
    uint32_t bits;
    if (!read_u32(&bits)) return 0;
    memcpy(&dst[i], &bits, sizeof(bits));
  }
  return 1;
}

static int write_bits_array(const float *src, int count) {
  int i;
  for (i = 0; i < count; i++) {
    uint32_t bits;
    memcpy(&bits, &src[i], sizeof(bits));
    if (!write_u32(bits)) return 0;
  }
  return 1;
}

int main(void) {
  char magic[4];
  uint32_t version;
  float frame[FRAME_SIZE];
  float ceps[2 * NB_BANDS];

  if (!set_binary_stdio()) {
    fprintf(stderr, "failed to set binary stdio mode\n");
    return 1;
  }
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) {
    fprintf(stderr, "invalid input magic\n");
    return 1;
  }
  if (!read_u32(&version) || version != 1) {
    fprintf(stderr, "unsupported input version\n");
    return 1;
  }
  if (!read_bits_array(frame, FRAME_SIZE)) {
    fprintf(stderr, "failed to read frame data\n");
    return 1;
  }

  burg_cepstral_analysis(ceps, frame);

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(version)) {
    fprintf(stderr, "failed to write header\n");
    return 1;
  }
  if (!write_bits_array(ceps, 2 * NB_BANDS)) {
    fprintf(stderr, "failed to write burg cepstrum\n");
    return 1;
  }
  return 0;
}
