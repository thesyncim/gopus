#include <math.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "opus.h"
#include "dred_decoder.h"

#define GODI_MAGIC "GODI"
#define GODO_MAGIC "GODO"

static int read_exact(void *dst, size_t n) {
  return fread(dst, 1, n, stdin) == n;
}

static int write_exact(const void *src, size_t n) {
  return fwrite(src, 1, n, stdout) == n;
}

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact(b, sizeof(b))) {
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
  return write_exact(b, sizeof(b));
}

static int write_i32(int32_t v) {
  return write_u32((uint32_t)v);
}

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) {
    return 0;
  }
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) {
    return 0;
  }
#endif
  return 1;
}

int main(void) {
  unsigned char magic[4];
  uint32_t version = 0;
  uint32_t sample_rate = 0;
  uint32_t max_dred_samples = 0;
  uint32_t frame_size = 0;
  uint32_t decode_offset = 0;
  uint32_t blend = 0;
  uint32_t packet_len = 0;
  unsigned char *packet = NULL;
  OpusDREDDecoder *dred_dec = NULL;
  OpusDRED *dred = NULL;
  int err = OPUS_OK;
  int dred_end = 0;
  int parse_ret = 0;
  int process_ret = OPUS_BAD_ARG;
  int F10;
  int init_frames;
  int features_per_frame;
  int needed_feature_frames;
  int feature_offset_base;
  int max_feature_index;
  int recoverable_feature_frames = 0;
  int missing_positive_frames = 0;
  int i;

  if (!set_binary_stdio()) {
    fprintf(stderr, "failed to set binary stdio mode\n");
    return 1;
  }

  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, GODI_MAGIC, sizeof(magic)) != 0) {
    fprintf(stderr, "invalid input magic\n");
    return 1;
  }
  if (!read_u32(&version) || version != 1 || !read_u32(&sample_rate) || !read_u32(&max_dred_samples) ||
      !read_u32(&frame_size) || !read_u32(&decode_offset) || !read_u32(&blend) || !read_u32(&packet_len)) {
    fprintf(stderr, "failed to read helper header\n");
    return 1;
  }

  if (packet_len > 0) {
    packet = (unsigned char *)malloc(packet_len);
    if (packet == NULL || !read_exact(packet, packet_len)) {
      fprintf(stderr, "failed to read packet payload\n");
      free(packet);
      return 1;
    }
  }

  dred_dec = opus_dred_decoder_create(&err);
  if (dred_dec == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_dred_decoder_create failed: %d\n", err);
    free(packet);
    return 1;
  }

  dred = opus_dred_alloc(&err);
  if (dred == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_dred_alloc failed: %d\n", err);
    opus_dred_decoder_destroy(dred_dec);
    free(packet);
    return 1;
  }

  memset(dred, 0, sizeof(*dred));
  parse_ret = opus_dred_parse(dred_dec, dred, packet, (opus_int32)packet_len, (opus_int32)max_dred_samples,
                              (opus_int32)sample_rate, &dred_end, 1);
  if (parse_ret >= 0 && dred->process_stage == 1) {
    process_ret = opus_dred_process(dred_dec, dred, dred);
  }

  F10 = (int)sample_rate / 100;
  init_frames = blend == 0 ? 2 : 0;
  features_per_frame = F10 > 0 ? (int)frame_size / F10 : 0;
  if (features_per_frame < 1) {
    features_per_frame = 1;
  }
  needed_feature_frames = init_frames + features_per_frame;
  feature_offset_base = init_frames - 2 +
      (int)floor(((float)(int32_t)decode_offset + dred->dred_offset * F10 / 4.0f) / F10);
  max_feature_index = 4 * dred->nb_latents - 1;

  for (i = 0; i < needed_feature_frames; i++) {
    int feature_offset = feature_offset_base - i;
    if (feature_offset < 0) {
      continue;
    }
    if (feature_offset <= max_feature_index) {
      recoverable_feature_frames++;
    } else {
      missing_positive_frames++;
    }
  }

  if (!write_exact(GODO_MAGIC, 4) || !write_u32(1) || !write_i32(parse_ret) || !write_i32(dred_end) ||
      !write_i32(process_ret) || !write_i32(dred->process_stage) || !write_i32(dred->nb_latents) ||
      !write_i32(dred->dred_offset) || !write_i32(features_per_frame) || !write_i32(needed_feature_frames) ||
      !write_i32(feature_offset_base) || !write_i32(max_feature_index) || !write_i32(recoverable_feature_frames) ||
      !write_i32(missing_positive_frames)) {
    fprintf(stderr, "failed to write helper header\n");
    opus_dred_free(dred);
    opus_dred_decoder_destroy(dred_dec);
    free(packet);
    return 1;
  }

  for (i = 0; i < needed_feature_frames; i++) {
    if (!write_i32(feature_offset_base - i)) {
      fprintf(stderr, "failed to write feature offsets\n");
      opus_dred_free(dred);
      opus_dred_decoder_destroy(dred_dec);
      free(packet);
      return 1;
    }
  }

  opus_dred_free(dred);
  opus_dred_decoder_destroy(dred_dec);
  free(packet);
  return 0;
}
