/* Stateful public encode API error and recovery oracle. */

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "opus.h"

#define INPUT_MAGIC "GEAI"
#define OUTPUT_MAGIC "GEAO"
#define MAX_FRAME 11520
#define MAX_PACKET 4000

enum { API_FLOAT32 = 0, API_INT16 = 1, API_INT24 = 2 };

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

static int read_exact(void *dst, size_t n) {
  return fread(dst, 1, n, stdin) == n;
}

static int write_exact(const void *src, size_t n) {
  const unsigned char *p = (const unsigned char *)src;
  size_t off = 0;
  while (off < n) {
    size_t written = fwrite(p + off, 1, n - off, stdout);
    if (written == 0) return 0;
    off += written;
  }
  return 1;
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

static int write_i32(int32_t v) { return write_u32((uint32_t)v); }

static opus_int32 encode_frame(OpusEncoder *enc, uint32_t api,
                               const float *pcm_float,
                               const opus_int16 *pcm_int16,
                               const opus_int32 *pcm_int24,
                               int frame_size, unsigned char *packet,
                               int max_data_bytes) {
  switch (api) {
    case API_FLOAT32:
      return opus_encode_float(enc, pcm_float, frame_size, packet, max_data_bytes);
    case API_INT16:
      return opus_encode(enc, pcm_int16, frame_size, packet, max_data_bytes);
    case API_INT24:
      return opus_encode24(enc, pcm_int24, frame_size, packet, max_data_bytes);
    default:
      return OPUS_BAD_ARG;
  }
}

int main(void) {
  if (!set_binary_stdio()) return 1;
  char magic[4];
  uint32_t version, sample_rate, channels, application, api, frame_size;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, 4) != 0 ||
      !read_u32(&version) || version != 1 || !read_u32(&sample_rate) ||
      !read_u32(&channels) || !read_u32(&application) || !read_u32(&api) ||
      !read_u32(&frame_size)) {
    fprintf(stderr, "invalid encode API oracle request\n");
    return 1;
  }
  if (channels < 1 || channels > 2 || frame_size == 0 || frame_size > MAX_FRAME || api > API_INT24) {
    fprintf(stderr, "unsupported encode API oracle configuration\n");
    return 1;
  }

  static float pcm_float[MAX_FRAME * 2];
  static opus_int16 pcm_int16[MAX_FRAME * 2];
  static opus_int32 pcm_int24[MAX_FRAME * 2];
  const int samples = (int)frame_size * (int)channels;
  for (int i = 0; i < samples; i++) {
    int value = ((i * 7919 + 1237) % 20001) - 10000;
    pcm_int16[i] = (opus_int16)value;
    pcm_float[i] = (float)value / 32768.0f;
    pcm_int24[i] = (opus_int32)value * 256;
  }

  int error = OPUS_OK;
  OpusEncoder *enc = opus_encoder_create((opus_int32)sample_rate, (int)channels,
                                         (int)application, &error);
  if (enc == NULL || error != OPUS_OK) {
    fprintf(stderr, "opus_encoder_create failed: %d\n", error);
    return 1;
  }
  if (opus_encoder_ctl(enc, OPUS_SET_BITRATE(64000)) != OPUS_OK) {
    fprintf(stderr, "OPUS_SET_BITRATE failed\n");
    opus_encoder_destroy(enc);
    return 1;
  }

  const int durations[] = {
    OPUS_FRAMESIZE_ARG,
    OPUS_FRAMESIZE_120_MS,
    OPUS_FRAMESIZE_ARG,
    OPUS_FRAMESIZE_ARG,
    OPUS_FRAMESIZE_ARG,
    OPUS_FRAMESIZE_120_MS,
    OPUS_FRAMESIZE_ARG,
  };
  const int budgets[] = { MAX_PACKET, MAX_PACKET, MAX_PACKET, 0, MAX_PACKET, 0, MAX_PACKET };
  const uint32_t count = (uint32_t)(sizeof(durations) / sizeof(durations[0]));
  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(count)) {
    opus_encoder_destroy(enc);
    return 1;
  }

  static unsigned char packet[MAX_PACKET];
  for (uint32_t i = 0; i < count; i++) {
    int32_t ret;
    if (opus_encoder_ctl(enc, OPUS_SET_EXPERT_FRAME_DURATION(durations[i])) != OPUS_OK) {
      ret = OPUS_BAD_ARG;
    } else {
      ret = encode_frame(enc, api, pcm_float, pcm_int16, pcm_int24,
                         (int)frame_size, packet, budgets[i]);
    }
    opus_uint32 final_range = 0;
    if (opus_encoder_ctl(enc, OPUS_GET_FINAL_RANGE(&final_range)) != OPUS_OK) {
      opus_encoder_destroy(enc);
      return 1;
    }
    uint32_t packet_size = ret > 0 ? (uint32_t)ret : 0;
    if (!write_i32(ret) || !write_u32(final_range) || !write_u32(packet_size) ||
        (packet_size > 0 && !write_exact(packet, packet_size))) {
      opus_encoder_destroy(enc);
      return 1;
    }
  }
  opus_encoder_destroy(enc);
  return 0;
}
