/* Selected libopus public API oracle for native 96 kHz mode/budget sequences. */

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

#ifndef ENABLE_QEXT
#error ENABLE_QEXT is required
#endif

#include "opus.h"

#define INPUT_MAGIC "G96M"
#define OUTPUT_MAGIC "G96O"
#define MAX_FRAME_SIZE 11520
#define MAX_PACKET_SIZE 3825
#define MAX_FRAMES 8

static int read_exact(void *dst, size_t size) {
  return fread(dst, 1, size, stdin) == size;
}

static int write_exact(const void *src, size_t size) {
  return fwrite(src, 1, size, stdout) == size;
}

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact(b, sizeof(b))) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) |
      ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int write_u32(uint32_t value) {
  unsigned char b[4] = {(unsigned char)value, (unsigned char)(value >> 8),
      (unsigned char)(value >> 16), (unsigned char)(value >> 24)};
  return write_exact(b, sizeof(b));
}

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

int main(void) {
  unsigned char magic[4];
  uint32_t version, frame_size, frame_count, qext, complexity, lsb_depth;
  float pcm[MAX_FRAME_SIZE];
  unsigned char packet[MAX_PACKET_SIZE];
  OpusEncoder *enc = NULL;
  int err = OPUS_OK;

  if (!set_binary_stdio() || !read_exact(magic, sizeof(magic)) ||
      memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0 || !read_u32(&version) ||
      version != 1 || !read_u32(&frame_size) || frame_size == 0 || frame_size > MAX_FRAME_SIZE ||
      !read_u32(&frame_count) || frame_count == 0 || frame_count > MAX_FRAMES ||
      !read_u32(&qext) || qext > 1 || !read_u32(&complexity) || complexity > 10 ||
      !read_u32(&lsb_depth) || lsb_depth < 8 || lsb_depth > 24) return 2;

  enc = opus_encoder_create(96000, 1, OPUS_APPLICATION_AUDIO, &err);
  if (enc == NULL || err != OPUS_OK) return 3;
  if (opus_encoder_ctl(enc, OPUS_SET_BITRATE(256000)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_COMPLEXITY((opus_int32)complexity)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_VBR(1)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_VBR_CONSTRAINT(1)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_LSB_DEPTH((opus_int32)lsb_depth)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_QEXT((opus_int32)qext)) != OPUS_OK) {
    opus_encoder_destroy(enc);
    return 4;
  }

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(frame_count)) {
    opus_encoder_destroy(enc);
    return 5;
  }
  for (uint32_t frame = 0; frame < frame_count; frame++) {
    uint32_t budget, bitrate;
    if (!read_u32(&budget) || budget < 1 || budget > MAX_PACKET_SIZE ||
        !read_u32(&bitrate) || bitrate == 0 || bitrate > 1500000) {
      opus_encoder_destroy(enc);
      return 6;
    }
    for (uint32_t i = 0; i < frame_size; i++) {
      uint32_t bits;
      if (!read_u32(&bits)) {
        opus_encoder_destroy(enc);
        return 6;
      }
      memcpy(&pcm[i], &bits, sizeof(bits));
    }
    if (opus_encoder_ctl(enc, OPUS_SET_BITRATE((opus_int32)bitrate)) != OPUS_OK) {
      opus_encoder_destroy(enc);
      return 7;
    }
    int n = opus_encode_float(enc, pcm, (int)frame_size, packet, (opus_int32)budget);
    opus_uint32 range = 0;
    if (n <= 0 || n > (int)budget ||
        opus_encoder_ctl(enc, OPUS_GET_FINAL_RANGE(&range)) != OPUS_OK ||
        !write_u32((uint32_t)n) || !write_u32(range) || !write_exact(packet, (size_t)n)) {
      opus_encoder_destroy(enc);
      return 8;
    }
  }
  opus_encoder_destroy(enc);
  return fflush(stdout) == 0 ? 0 : 9;
}
