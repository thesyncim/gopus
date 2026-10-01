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

#include "opus.h"

#define INPUT_MAGIC "GPI1"
#define OUTPUT_MAGIC "GPO1"

static int read_exact(void *dst, size_t n) {
  return fread(dst, 1, n, stdin) == n;
}

static int write_exact(const void *src, size_t n) {
  return fwrite(src, 1, n, stdout) == n;
}

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact(b, sizeof(b))) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) |
      ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int write_u32(uint32_t value) {
  unsigned char b[4] = {
      (unsigned char)value,
      (unsigned char)(value >> 8),
      (unsigned char)(value >> 16),
      (unsigned char)(value >> 24),
  };
  return write_exact(b, sizeof(b));
}

int main(void) {
  unsigned char magic[4];
  uint32_t version, sample_rate, channels, step_count;
  OpusDecoder *decoder = NULL;
  float *pcm = NULL;
  int err = OPUS_OK;

#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 1;
#endif
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0 ||
      !read_u32(&version) || version != 1 ||
      !read_u32(&sample_rate) || !read_u32(&channels) || !read_u32(&step_count) ||
      sample_rate == 0 || channels == 0 || channels > 2 || step_count == 0 || step_count > 64) {
    fprintf(stderr, "invalid pitch probe header\n");
    return 2;
  }

  decoder = opus_decoder_create((opus_int32)sample_rate, (int)channels, &err);
  if (decoder == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_decoder_create failed: %d\n", err);
    return 3;
  }
  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(step_count)) goto fail;

  for (uint32_t step = 0; step < step_count; step++) {
    uint32_t frame_size, decode_fec, packet_len;
    unsigned char packet[1275];
    opus_int32 pitch = -1;
    int decoded, ctl;
    if (!read_u32(&frame_size) || !read_u32(&decode_fec) || !read_u32(&packet_len) ||
        frame_size == 0 || frame_size > 5760 || decode_fec > 1 || packet_len > sizeof(packet) ||
        !read_exact(packet, packet_len)) {
      fprintf(stderr, "invalid pitch probe step %u\n", step);
      goto fail;
    }
    free(pcm);
    pcm = (float *)malloc((size_t)frame_size * channels * sizeof(*pcm));
    if (pcm == NULL) {
      fprintf(stderr, "pitch probe PCM allocation failed\n");
      goto fail;
    }
    decoded = opus_decode_float(decoder,
        packet_len == 0 ? NULL : packet, (opus_int32)packet_len,
        pcm, (int)frame_size, (int)decode_fec);
    ctl = opus_decoder_ctl(decoder, OPUS_GET_PITCH(&pitch));
    if (!write_u32((uint32_t)(int32_t)decoded) ||
        !write_u32((uint32_t)(int32_t)ctl) ||
        !write_u32((uint32_t)pitch)) goto fail;
  }

  free(pcm);
  opus_decoder_destroy(decoder);
  return 0;

fail:
  free(pcm);
  opus_decoder_destroy(decoder);
  return 4;
}
