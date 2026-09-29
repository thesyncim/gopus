#include <stdint.h>
#include <stdio.h>
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

#define INPUT_MAGIC "GMI4"
#define OUTPUT_MAGIC "GMO4"
#define FRAME_CAPACITY 11520
#define CHANNEL_CAPACITY 2
#define PACKET_CAPACITY 4000

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

static int read_i32(opus_int32 *out) {
  uint32_t bits;
  if (!read_u32(&bits)) return 0;
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
  uint32_t version, frame_size, channels, streams, coupled, packet_capacity;
  uint32_t bitrate, complexity, i;
  unsigned char mapping[CHANNEL_CAPACITY];
  opus_int32 pcm[FRAME_CAPACITY * CHANNEL_CAPACITY];
  unsigned char packet[PACKET_CAPACITY];
  OpusMSEncoder *enc = NULL;
  OpusEncoder *children[CHANNEL_CAPACITY] = {NULL, NULL};
  int err = OPUS_OK;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0 ||
      !read_u32(&version) || version != 1 || !read_u32(&frame_size) ||
      frame_size < 1 || frame_size > FRAME_CAPACITY ||
      !read_u32(&channels) || channels < 1 || channels > CHANNEL_CAPACITY ||
      !read_u32(&streams) || streams < 1 || streams > channels ||
      !read_u32(&coupled) || coupled > streams ||
      coupled * 2 + (streams - coupled) != channels ||
      !read_u32(&packet_capacity) || packet_capacity < 2 * streams - 1 ||
      packet_capacity > PACKET_CAPACITY || !read_u32(&bitrate) ||
      !read_u32(&complexity) || complexity > 10 ||
      !read_exact(mapping, channels)) return 2;

  for (i = 0; i < frame_size * channels; i++) {
    if (!read_i32(&pcm[i])) return 2;
  }

  enc = opus_multistream_encoder_create(96000, (int)channels, (int)streams,
      (int)coupled, mapping, OPUS_APPLICATION_AUDIO, &err);
  if (!enc || err != OPUS_OK) return 3;
  if (opus_multistream_encoder_ctl(enc, OPUS_SET_BITRATE((opus_int32)bitrate)) != OPUS_OK ||
      opus_multistream_encoder_ctl(enc, OPUS_SET_COMPLEXITY((opus_int32)complexity)) != OPUS_OK) {
    opus_multistream_encoder_destroy(enc);
    return 4;
  }

  for (uint32_t stream = 0; stream < streams; stream++) {
    if (opus_multistream_encoder_ctl(enc,
            OPUS_MULTISTREAM_GET_ENCODER_STATE((int)stream, &children[stream])) != OPUS_OK ||
        children[stream] == NULL) {
      opus_multistream_encoder_destroy(enc);
      return 4;
    }
  }

  int n = opus_multistream_encode24(enc, pcm, (int)frame_size, packet,
      (opus_int32)packet_capacity);
  if (n <= 0) {
    opus_multistream_encoder_destroy(enc);
    return 5;
  }

  opus_uint32 range = 0;
  for (uint32_t stream = 0; stream < streams; stream++) {
    opus_uint32 stream_range = 0;
    if (opus_encoder_ctl(children[stream], OPUS_GET_FINAL_RANGE(&stream_range)) != OPUS_OK) {
      opus_multistream_encoder_destroy(enc);
      return 5;
    }
    range ^= stream_range;
  }

  int packet_samples = opus_packet_get_nb_samples(packet, n, 96000);
  int output_ok = packet_samples == (int)frame_size &&
      write_exact("GMO4", 4) && write_u32(1) &&
      write_u32((uint32_t)n) && write_u32((uint32_t)packet_samples) &&
      write_u32(range) && write_exact(packet, (size_t)n);
  opus_multistream_encoder_destroy(enc);
  return output_ok ? 0 : 6;
}
