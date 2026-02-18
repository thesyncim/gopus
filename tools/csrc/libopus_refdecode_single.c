#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "opus.h"

#define GOSI_MAGIC "GOSI"
#define GOSO_MAGIC "GOSO"

static int read_exact(void *dst, size_t n) {
  return fread(dst, 1, n, stdin) == n;
}

static int write_exact(const void *src, size_t n) {
  return fwrite(src, 1, n, stdout) == n;
}

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact(b, 4)) {
    return 0;
  }
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

static int append_floats(float **out, size_t *out_len, size_t *out_cap, const float *src, size_t n) {
  if (n == 0) {
    return 1;
  }

  if (n > SIZE_MAX - *out_len) {
    return 0;
  }
  size_t need = *out_len + n;
  if (need > *out_cap) {
    size_t new_cap = *out_cap ? *out_cap : 1024;
    while (new_cap < need) {
      if (new_cap > SIZE_MAX / 2) {
        new_cap = need;
        break;
      }
      new_cap *= 2;
    }
    float *resized = (float *)realloc(*out, new_cap * sizeof(float));
    if (resized == NULL) {
      return 0;
    }
    *out = resized;
    *out_cap = new_cap;
  }

  memcpy(*out + *out_len, src, n * sizeof(float));
  *out_len = need;
  return 1;
}

int main(void) {
  unsigned char magic[4];
  uint32_t version = 0;
  uint32_t channels = 0;
  uint32_t frame_size = 0;
  uint32_t packet_count = 0;

  float *frame = NULL;
  float *decoded = NULL;
  size_t decoded_len = 0;
  size_t decoded_cap = 0;

  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, GOSI_MAGIC, sizeof(magic)) != 0) {
    fprintf(stderr, "invalid input magic\n");
    return 1;
  }
  if (!read_u32(&version) || version != 1 || !read_u32(&channels) || !read_u32(&frame_size) || !read_u32(&packet_count)) {
    fprintf(stderr, "failed to read header\n");
    return 1;
  }
  if (channels == 0 || channels > 2 || frame_size == 0) {
    fprintf(stderr, "invalid decoder dimensions\n");
    return 1;
  }
  if (channels > SIZE_MAX / frame_size || (size_t)channels * (size_t)frame_size > SIZE_MAX / sizeof(float)) {
    fprintf(stderr, "frame buffer overflow\n");
    return 1;
  }

  frame = (float *)malloc((size_t)channels * (size_t)frame_size * sizeof(float));
  if (frame == NULL) {
    fprintf(stderr, "failed to allocate frame buffer\n");
    return 1;
  }

  int err = OPUS_OK;
  OpusDecoder *dec = opus_decoder_create(48000, (int)channels, &err);
  if (dec == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_decoder_create failed: %d\n", err);
    free(frame);
    return 1;
  }

  for (uint32_t i = 0; i < packet_count; i++) {
    uint32_t packet_len = 0;
    unsigned char *packet = NULL;
    int decoded_samples = 0;

    if (!read_u32(&packet_len)) {
      fprintf(stderr, "failed to read packet length\n");
      opus_decoder_destroy(dec);
      free(frame);
      free(decoded);
      return 1;
    }
    if (packet_len > 0) {
      packet = (unsigned char *)malloc(packet_len);
      if (packet == NULL || !read_exact(packet, packet_len)) {
        fprintf(stderr, "failed to read packet payload\n");
        free(packet);
        opus_decoder_destroy(dec);
        free(frame);
        free(decoded);
        return 1;
      }
    }

    decoded_samples = opus_decode_float(dec, packet, (opus_int32)packet_len, frame, (int)frame_size, 0);
    free(packet);

    if (decoded_samples < 0) {
      fprintf(stderr, "opus_decode_float failed: %d\n", decoded_samples);
      opus_decoder_destroy(dec);
      free(frame);
      free(decoded);
      return 1;
    }
    if (!append_floats(&decoded, &decoded_len, &decoded_cap, frame, (size_t)decoded_samples * (size_t)channels)) {
      fprintf(stderr, "failed to append decoded samples\n");
      opus_decoder_destroy(dec);
      free(frame);
      free(decoded);
      return 1;
    }
  }

  opus_decoder_destroy(dec);

  if (!write_exact(GOSO_MAGIC, 4) || decoded_len > UINT32_MAX || !write_u32((uint32_t)decoded_len) ||
      !write_exact(decoded, decoded_len * sizeof(float))) {
    fprintf(stderr, "failed to write output\n");
    free(frame);
    free(decoded);
    return 1;
  }

  free(frame);
  free(decoded);
  return 0;
}
