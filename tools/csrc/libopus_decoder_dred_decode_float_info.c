#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "opus.h"

#define INPUT_MAGIC "GDDI"
#define OUTPUT_MAGIC "GDDO"

static int read_exact(void *dst, size_t n) {
  return fread(dst, 1, n, stdin) == n;
}

static int write_exact(const void *src, size_t n) {
  return fwrite(src, 1, n, stdout) == n;
}

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact(b, sizeof(b))) return 0;
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

static int write_f32(float v) {
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

int main(void) {
  unsigned char magic[4];
  uint32_t version = 0;
  uint32_t sample_rate = 0;
  uint32_t max_dred_samples = 0;
  int32_t warmup_dred_offset = -1;
  int32_t dred_offset = 0;
  uint32_t frame_size = 0;
  uint32_t packet_len = 0;
  unsigned char *packet = NULL;
  OpusDecoder *dec = NULL;
  OpusDREDDecoder *dred_dec = NULL;
  OpusDRED *dred = NULL;
  float *seed_pcm = NULL;
  float *out_pcm = NULL;
  int err = OPUS_OK;
  int parse_ret = OPUS_OK;
  int dred_end = 0;
  int packet_samples = 0;
  int channels = 0;
  int warmup_ret = 0;
  int ret = 0;
  int i;

  if (!set_binary_stdio()) {
    fprintf(stderr, "failed to set binary stdio mode\n");
    return 1;
  }
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) {
    fprintf(stderr, "invalid input magic\n");
    return 1;
  }
  if (!read_u32(&version) || version != 2 ||
      !read_u32(&sample_rate) || !read_u32(&max_dred_samples) ||
      !read_exact(&warmup_dred_offset, sizeof(warmup_dred_offset)) ||
      !read_exact(&dred_offset, sizeof(dred_offset)) ||
      !read_u32(&frame_size) || !read_u32(&packet_len)) {
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

  channels = opus_packet_get_nb_channels(packet);
  if (channels <= 0) {
    fprintf(stderr, "failed to get packet channels\n");
    free(packet);
    return 1;
  }

  dec = opus_decoder_create((opus_int32)sample_rate, channels, &err);
  if (dec == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_decoder_create failed: %d\n", err);
    free(packet);
    return 1;
  }
  dred_dec = opus_dred_decoder_create(&err);
  if (dred_dec == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_dred_decoder_create failed: %d\n", err);
    opus_decoder_destroy(dec);
    free(packet);
    return 1;
  }
  dred = opus_dred_alloc(&err);
  if (dred == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_dred_alloc failed: %d\n", err);
    opus_dred_decoder_destroy(dred_dec);
    opus_decoder_destroy(dec);
    free(packet);
    return 1;
  }

  parse_ret = opus_dred_parse(dred_dec, dred, packet, (opus_int32)packet_len, (opus_int32)max_dred_samples, (opus_int32)sample_rate, &dred_end, 0);
  if (parse_ret >= 0) {
    packet_samples = opus_decoder_get_nb_samples(dec, packet, (opus_int32)packet_len);
    if (packet_samples > 0) {
      seed_pcm = (float *)calloc((size_t)packet_samples * channels, sizeof(float));
      if (seed_pcm == NULL) {
        fprintf(stderr, "seed buffer alloc failed\n");
        opus_dred_free(dred);
        opus_dred_decoder_destroy(dred_dec);
        opus_decoder_destroy(dec);
        free(packet);
        return 1;
      }
      err = opus_decode_float(dec, packet, (opus_int32)packet_len, seed_pcm, packet_samples, 0);
      if (err < 0) {
        parse_ret = err;
      }
    }
  }

  if (parse_ret >= 0) {
    out_pcm = (float *)calloc((size_t)frame_size * channels, sizeof(float));
    if (out_pcm == NULL) {
      fprintf(stderr, "output buffer alloc failed\n");
      free(seed_pcm);
      opus_dred_free(dred);
      opus_dred_decoder_destroy(dred_dec);
      opus_decoder_destroy(dec);
      free(packet);
        return 1;
    }
    if (warmup_dred_offset >= 0) {
      warmup_ret = opus_decoder_dred_decode_float(dec, dred, warmup_dred_offset, out_pcm, (opus_int32)frame_size);
      if (warmup_ret < 0) {
        ret = warmup_ret;
      } else {
        ret = opus_decoder_dred_decode_float(dec, dred, dred_offset, out_pcm, (opus_int32)frame_size);
      }
    } else {
      warmup_ret = 0;
      ret = opus_decoder_dred_decode_float(dec, dred, dred_offset, out_pcm, (opus_int32)frame_size);
    }
  } else {
    warmup_ret = parse_ret;
    ret = parse_ret;
  }

  if (!write_exact(OUTPUT_MAGIC, 4) ||
      !write_u32(2) ||
      !write_i32(parse_ret) ||
      !write_i32(dred_end) ||
      !write_i32(warmup_ret) ||
      !write_i32(ret) ||
      !write_i32(channels)) {
    fprintf(stderr, "failed to write helper header\n");
    free(out_pcm);
    free(seed_pcm);
    opus_dred_free(dred);
    opus_dred_decoder_destroy(dred_dec);
    opus_decoder_destroy(dec);
    free(packet);
    return 1;
  }

  if (ret > 0) {
    for (i = 0; i < ret * channels; i++) {
      if (!write_f32(out_pcm[i])) {
        fprintf(stderr, "failed to write pcm\n");
        free(out_pcm);
        free(seed_pcm);
        opus_dred_free(dred);
        opus_dred_decoder_destroy(dred_dec);
        opus_decoder_destroy(dec);
        free(packet);
        return 1;
      }
    }
  }

  free(out_pcm);
  free(seed_pcm);
  opus_dred_free(dred);
  opus_dred_decoder_destroy(dred_dec);
  opus_decoder_destroy(dec);
  free(packet);
  return 0;
}
