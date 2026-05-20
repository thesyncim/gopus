#include <stdint.h>
#include <stdio.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "opus.h"

#define INPUT_MAGIC "GAMI"
#define OUTPUT_MAGIC "GAMO"

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

static int write_i32(int32_t value) {
  return write_exact(&value, sizeof(value));
}

static int read_float(float *out) {
  uint32_t bits;
  if (!read_u32(&bits)) return 0;
  memcpy(out, &bits, sizeof(*out));
  return 1;
}

int main(void) {
  char magic[4];
  uint32_t version;
  uint32_t count;
  uint32_t i;

  if (!set_binary_stdio()) {
    fprintf(stderr, "failed to set binary stdio mode\n");
    return 1;
  }
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) {
    fprintf(stderr, "invalid input magic\n");
    return 1;
  }
  if (!read_u32(&version) || version != 1 || !read_u32(&count)) {
    fprintf(stderr, "failed to read header\n");
    return 1;
  }
  if (!write_exact(OUTPUT_MAGIC, sizeof(magic)) || !write_u32(1) || !write_u32(count)) {
    fprintf(stderr, "failed to write output header\n");
    return 1;
  }

  for (i = 0; i < count; i++) {
    uint32_t sample_rate;
    uint32_t channels;
    uint32_t frame_size;
    uint32_t bitrate;
    uint32_t max_data_bytes;
    uint32_t total_samples;
    uint32_t j;
    int err;
    int ret;
    OpusEncoder *enc;
    unsigned char packet[1500];
    float pcm[5760 * 2];

    if (!read_u32(&sample_rate) || !read_u32(&channels) || !read_u32(&frame_size) ||
        !read_u32(&bitrate) || !read_u32(&max_data_bytes)) {
      fprintf(stderr, "truncated case header\n");
      return 1;
    }
    if (channels < 1 || channels > 2 || frame_size > 5760 || max_data_bytes > sizeof(packet)) {
      fprintf(stderr, "invalid case shape\n");
      return 1;
    }
    total_samples = frame_size * channels;
    for (j = 0; j < total_samples; j++) {
      if (!read_float(&pcm[j])) {
        fprintf(stderr, "truncated pcm\n");
        return 1;
      }
    }

    enc = opus_encoder_create((opus_int32)sample_rate, (int)channels, OPUS_APPLICATION_AUDIO, &err);
    if (enc == NULL || err != OPUS_OK) {
      fprintf(stderr, "opus_encoder_create failed: %d\n", err);
      return 1;
    }
    if (opus_encoder_ctl(enc, OPUS_SET_BITRATE((opus_int32)bitrate)) != OPUS_OK ||
        opus_encoder_ctl(enc, OPUS_SET_VBR(1)) != OPUS_OK ||
        opus_encoder_ctl(enc, OPUS_SET_VBR_CONSTRAINT(0)) != OPUS_OK ||
        opus_encoder_ctl(enc, OPUS_SET_BANDWIDTH(OPUS_BANDWIDTH_FULLBAND)) != OPUS_OK ||
        opus_encoder_ctl(enc, OPUS_SET_SIGNAL(OPUS_AUTO)) != OPUS_OK ||
        opus_encoder_ctl(enc, OPUS_SET_COMPLEXITY(10)) != OPUS_OK ||
        opus_encoder_ctl(enc, OPUS_SET_PACKET_LOSS_PERC(0)) != OPUS_OK ||
        opus_encoder_ctl(enc, OPUS_SET_INBAND_FEC(0)) != OPUS_OK ||
        opus_encoder_ctl(enc, OPUS_SET_DTX(0)) != OPUS_OK) {
      fprintf(stderr, "opus_encoder_ctl setup failed\n");
      opus_encoder_destroy(enc);
      return 1;
    }

    ret = opus_encode_float(enc, pcm, (int)frame_size, packet, (opus_int32)max_data_bytes);
    opus_encoder_destroy(enc);
    if (ret < 0) {
      if (!write_i32((int32_t)ret) || !write_u32(0)) {
        fprintf(stderr, "failed to write encode error\n");
        return 1;
      }
      continue;
    }
    if (!write_i32((int32_t)ret) || !write_u32((uint32_t)packet[0])) {
      fprintf(stderr, "failed to write encode result\n");
      return 1;
    }
  }
  return 0;
}
