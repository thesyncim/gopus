#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

#ifdef HAVE_CONFIG_H
#include "config.h"
#endif

#include "opus.h"

#define INPUT_MAGIC "GQPI"
#define OUTPUT_MAGIC "GQPO"

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
  unsigned char b[4];
  b[0] = (unsigned char)(value & 0xFF);
  b[1] = (unsigned char)((value >> 8) & 0xFF);
  b[2] = (unsigned char)((value >> 16) & 0xFF);
  b[3] = (unsigned char)((value >> 24) & 0xFF);
  return write_exact(b, sizeof(b));
}

static int write_float_array(const float *values, size_t n) {
  size_t i;
  for (i = 0; i < n; i++) {
    union {
      float f;
      uint32_t u;
    } bits;
    bits.f = values[i];
    if (!write_u32(bits.u)) return 0;
  }
  return 1;
}

int main(void) {
  unsigned char magic[4];
  uint32_t version, sample_rate, channels, good_frame_size, loss_frame_size, packet_len;
  unsigned char *packet = NULL;
  float *pcm = NULL;
  OpusDecoder *decoder = NULL;
  int error = OPUS_OK;
  int good_samples, loss_samples;

  if (!read_exact(magic, sizeof(magic)) ||
      magic[0] != INPUT_MAGIC[0] || magic[1] != INPUT_MAGIC[1] ||
      magic[2] != INPUT_MAGIC[2] || magic[3] != INPUT_MAGIC[3] ||
      !read_u32(&version) || version != 1 ||
      !read_u32(&sample_rate) || !read_u32(&channels) ||
      !read_u32(&good_frame_size) || !read_u32(&loss_frame_size) ||
      !read_u32(&packet_len)) {
    fprintf(stderr, "invalid QEXT CELT PLC request\n");
    return 1;
  }
  if ((sample_rate != 48000 && sample_rate != 96000) || channels < 1 || channels > 2 ||
      good_frame_size == 0 || good_frame_size > 1920 || loss_frame_size == 0 ||
      loss_frame_size > good_frame_size || packet_len < 2 || packet_len > 65535) {
    fprintf(stderr, "invalid QEXT CELT PLC geometry\n");
    return 1;
  }

  packet = (unsigned char *)malloc(packet_len);
  pcm = (float *)malloc((size_t)good_frame_size * channels * sizeof(*pcm));
  if (packet == NULL || pcm == NULL || !read_exact(packet, packet_len)) {
    fprintf(stderr, "failed to read QEXT CELT PLC packet\n");
    free(packet);
    free(pcm);
    return 1;
  }
  decoder = opus_decoder_create((opus_int32)sample_rate, (int)channels, &error);
  if (decoder == NULL || error != OPUS_OK) {
    fprintf(stderr, "opus_decoder_create failed: %d\n", error);
    free(packet);
    free(pcm);
    if (decoder != NULL) opus_decoder_destroy(decoder);
    return 1;
  }

  good_samples = opus_decode_float(decoder, packet, (opus_int32)packet_len,
                                   pcm, (int)good_frame_size, 0);
  if (good_samples != (int)good_frame_size) {
    fprintf(stderr, "selected C good decode returned %d\n", good_samples);
    opus_decoder_destroy(decoder);
    free(packet);
    free(pcm);
    return 1;
  }
  loss_samples = opus_decode_float(decoder, NULL, 0, pcm, (int)loss_frame_size, 0);
  if (loss_samples != (int)loss_frame_size) {
    fprintf(stderr, "selected C PLC decode returned %d\n", loss_samples);
    opus_decoder_destroy(decoder);
    free(packet);
    free(pcm);
    return 1;
  }

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) ||
      !write_u32((uint32_t)good_samples) ||
      !write_u32((uint32_t)loss_samples) ||
      !write_float_array(pcm, (size_t)loss_samples * channels)) {
    fprintf(stderr, "failed to write QEXT CELT PLC result\n");
    opus_decoder_destroy(decoder);
    free(packet);
    free(pcm);
    return 1;
  }

  opus_decoder_destroy(decoder);
  free(packet);
  free(pcm);
  return 0;
}
