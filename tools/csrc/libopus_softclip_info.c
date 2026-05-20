#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "opus.h"

#define INPUT_MAGIC "GSCI"
#define OUTPUT_MAGIC "GSCO"

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

int main(void) {
  char magic[4];
  uint32_t version;
  uint32_t frame_size;
  uint32_t channels;
  uint32_t total;
  float *pcm;
  float *mem;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) return 1;
  if (!read_u32(&version) || version != 1 || !read_u32(&frame_size) || !read_u32(&channels)) return 1;
  if (channels == 0 || channels > 8 || frame_size > 5760) return 1;
  total = frame_size * channels;
  if (frame_size != 0 && total / frame_size != channels) return 1;

  mem = (float *)calloc(channels, sizeof(*mem));
  pcm = (float *)calloc(total == 0 ? 1 : total, sizeof(*pcm));
  if (mem == NULL || pcm == NULL) {
    free(mem);
    free(pcm);
    return 1;
  }

  if (!read_exact(mem, channels * sizeof(*mem)) || !read_exact(pcm, total * sizeof(*pcm))) {
    free(mem);
    free(pcm);
    return 1;
  }

  opus_pcm_soft_clip(pcm, (int)frame_size, (int)channels, mem);

  if (!write_exact(OUTPUT_MAGIC, sizeof(magic)) ||
      !write_u32(1) ||
      !write_u32(frame_size) ||
      !write_u32(channels) ||
      !write_exact(mem, channels * sizeof(*mem)) ||
      !write_exact(pcm, total * sizeof(*pcm))) {
    free(mem);
    free(pcm);
    return 1;
  }

  free(mem);
  free(pcm);
  return 0;
}
