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
#include "opus_multistream.h"

#define INPUT_MAGIC "GM96"
#define OUTPUT_MAGIC "GMSO"
#define FRAME_CAPACITY 11520
#define CHANNEL_CAPACITY 2
#define SAMPLE_CAPACITY (FRAME_CAPACITY * CHANNEL_CAPACITY)
#define PACKET_CAPACITY 3825

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

static int read_i16(opus_int16 *out) {
  unsigned char b[2];
  uint16_t bits;
  if (!read_exact(b, sizeof(b))) return 0;
  bits = (uint16_t)b[0] | ((uint16_t)b[1] << 8);
  memcpy(out, &bits, sizeof(bits));
  return 1;
}

static int write_u32(uint32_t x) {
  unsigned char b[4] = {(unsigned char)x, (unsigned char)(x >> 8),
      (unsigned char)(x >> 16), (unsigned char)(x >> 24)};
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
  uint32_t version, qext, frame_size, frame_count, packet_capacity;
  uint32_t bitrate_u32, complexity, channels, streams, coupled, format, i;
  float pcm_f32[SAMPLE_CAPACITY];
  opus_int16 pcm_i16[SAMPLE_CAPACITY];
  unsigned char packet[PACKET_CAPACITY];
  unsigned char mapping[CHANNEL_CAPACITY] = {0, 1};
  OpusMSEncoder *enc = NULL;
  OpusEncoder *children[CHANNEL_CAPACITY] = {NULL, NULL};
  opus_int32 sample_rate = 0;
  int err = OPUS_OK;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0 ||
      !read_u32(&version) || version != 3 || !read_u32(&qext) || qext > 1 ||
      !read_u32(&frame_size) || (frame_size != 240 && frame_size != 480 &&
          frame_size != 960 && frame_size != 1920 && frame_size != 3840) ||
      !read_u32(&frame_count) || frame_count == 0 || frame_count > 16 ||
      !read_u32(&packet_capacity) || packet_capacity < 3 || packet_capacity > PACKET_CAPACITY ||
      !read_u32(&bitrate_u32) || !read_u32(&complexity) || complexity > 10 ||
      !read_u32(&channels) || channels < 1 || channels > CHANNEL_CAPACITY ||
      !read_u32(&streams) || streams < 1 || streams > channels ||
      !read_u32(&coupled) || coupled > streams || coupled * 2 + (streams - coupled) != channels ||
      !read_u32(&format) || format > 1) return 2;

  enc = opus_multistream_encoder_create(96000, (int)channels, (int)streams,
      (int)coupled, mapping,
      OPUS_APPLICATION_AUDIO, &err);
  if (!enc || err != OPUS_OK) return 3;
  if (opus_multistream_encoder_ctl(enc, OPUS_SET_BITRATE((opus_int32)bitrate_u32)) != OPUS_OK ||
      opus_multistream_encoder_ctl(enc, OPUS_SET_COMPLEXITY((opus_int32)complexity)) != OPUS_OK ||
      opus_multistream_encoder_ctl(enc, OPUS_SET_VBR(1)) != OPUS_OK ||
      opus_multistream_encoder_ctl(enc, OPUS_SET_VBR_CONSTRAINT(1)) != OPUS_OK ||
      opus_multistream_encoder_ctl(enc, OPUS_SET_QEXT((opus_int32)qext)) != OPUS_OK) {
    opus_multistream_encoder_destroy(enc);
    return 4;
  }
  for (uint32_t stream = 0; stream < streams; stream++) {
    opus_int32 child_qext = 0;
    if (opus_multistream_encoder_ctl(enc,
            OPUS_MULTISTREAM_GET_ENCODER_STATE((int)stream, &children[stream])) != OPUS_OK ||
        children[stream] == NULL ||
        opus_encoder_ctl(children[stream], OPUS_GET_QEXT(&child_qext)) != OPUS_OK ||
        child_qext != (opus_int32)qext) {
      opus_multistream_encoder_destroy(enc);
      return 4;
    }
    if (stream == 0 &&
        opus_encoder_ctl(children[stream], OPUS_GET_SAMPLE_RATE(&sample_rate)) != OPUS_OK) {
      opus_multistream_encoder_destroy(enc);
      return 4;
    }
  }

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) ||
      !write_u32((uint32_t)sample_rate) || !write_u32(channels) ||
      !write_u32(streams) || !write_u32(coupled) ||
      !write_u32(qext) || !write_u32(frame_count)) {
    opus_multistream_encoder_destroy(enc);
    return 5;
  }

  for (i = 0; i < frame_count; i++) {
    uint32_t frame_budget;
    if (!read_u32(&frame_budget) || frame_budget < 2*streams-1 || frame_budget > packet_capacity) {
      opus_multistream_encoder_destroy(enc);
      return 6;
    }
    size_t input_samples = (size_t)frame_size * channels;
    if (format == 0) {
      for (size_t j = 0; j < input_samples; j++) {
        uint32_t bits;
        if (!read_u32(&bits)) {
          opus_multistream_encoder_destroy(enc);
          return 6;
        }
        memcpy(&pcm_f32[j], &bits, sizeof(bits));
      }
    } else {
      for (size_t j = 0; j < input_samples; j++) {
        if (!read_i16(&pcm_i16[j])) {
          opus_multistream_encoder_destroy(enc);
          return 6;
        }
      }
    }

    int n = format == 0 ?
        opus_multistream_encode_float(enc, pcm_f32, (int)frame_size, packet,
            (opus_int32)frame_budget) :
        opus_multistream_encode(enc, pcm_i16, (int)frame_size, packet,
            (opus_int32)frame_budget);
    opus_uint32 range = 0;
    int packet_samples;
    if (n <= 0) {
      opus_multistream_encoder_destroy(enc);
      return 7;
    }
    for (uint32_t stream = 0; stream < streams; stream++) {
      opus_uint32 stream_range = 0;
      if (opus_encoder_ctl(children[stream], OPUS_GET_FINAL_RANGE(&stream_range)) != OPUS_OK) {
        opus_multistream_encoder_destroy(enc);
        return 7;
      }
      range ^= stream_range;
    }
    packet_samples = opus_packet_get_nb_samples(packet, n, 96000);
    if (packet_samples != (int)frame_size || !write_u32((uint32_t)n) ||
        !write_u32((uint32_t)packet_samples) || !write_u32(range) ||
        !write_u32((uint32_t)n) || !write_exact(packet, (size_t)n)) {
      opus_multistream_encoder_destroy(enc);
      return 8;
    }
  }

  opus_multistream_encoder_destroy(enc);
  return 0;
}
