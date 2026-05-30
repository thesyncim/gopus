#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "opus_multistream.h"

#define GMEI_MAGIC "GMEI"
#define GMEO_MAGIC "GMEO"

enum {
  SAMPLE_FORMAT_FLOAT32 = 0,
  SAMPLE_FORMAT_INT16 = 1
};

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

static int valid_sample_rate(uint32_t sample_rate) {
  return sample_rate == 8000 || sample_rate == 12000 || sample_rate == 16000 || sample_rate == 24000 ||
         sample_rate == 48000;
}

/*
 * Input layout (little-endian):
 *   magic "GMEI"
 *   u32 version (1)
 *   u32 sample_rate
 *   u32 channels
 *   u32 mapping_family
 *   u32 application
 *   i32 bitrate              (OPUS_AUTO/OPUS_BITRATE_MAX accepted; otherwise bits/s)
 *   u32 vbr                  (0 = CBR, 1 = VBR)
 *   u32 vbr_constraint
 *   u32 complexity
 *   i32 bandwidth            (OPUS_AUTO or a fixed bandwidth)
 *   u32 frame_size
 *   u32 frame_count
 *   u32 max_packet_bytes
 *   u32 sample_format        (0 float32, 1 int16)
 *   PCM samples: frame_count * frame_size * channels in the requested format
 *
 * Output layout (little-endian):
 *   magic "GMEO"
 *   u32 version (1)
 *   u32 streams
 *   u32 coupled_streams
 *   u32 channels
 *   raw mapping[channels]
 *   u32 packet_count
 *   for each packet: u32 len, raw bytes[len]
 */
int main(void) {
  unsigned char magic[4];
  uint32_t version = 0;
  uint32_t sample_rate = 48000;
  uint32_t channels = 0;
  uint32_t mapping_family = 0;
  uint32_t application = 0;
  int32_t bitrate = 0;
  uint32_t vbr = 0;
  uint32_t vbr_constraint = 0;
  uint32_t complexity = 0;
  int32_t bandwidth = -1000;
  uint32_t frame_size = 0;
  uint32_t frame_count = 0;
  uint32_t max_packet_bytes = 0;
  uint32_t sample_format = SAMPLE_FORMAT_FLOAT32;

  int streams = 0;
  int coupled_streams = 0;
  unsigned char mapping[256];
  void *pcm = NULL;
  unsigned char *packet = NULL;
  size_t item_size = sizeof(float);
  int err = OPUS_OK;
  OpusMSEncoder *enc = NULL;

  if (!set_binary_stdio()) {
    fprintf(stderr, "failed to set binary stdio mode\n");
    return 1;
  }

  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, GMEI_MAGIC, sizeof(magic)) != 0) {
    fprintf(stderr, "invalid input magic\n");
    return 1;
  }

  uint32_t b_bitrate = 0, b_bandwidth = 0;
  if (!read_u32(&version) || version != 1) {
    fprintf(stderr, "unsupported input version\n");
    return 1;
  }
  if (!read_u32(&sample_rate) || !read_u32(&channels) || !read_u32(&mapping_family) ||
      !read_u32(&application) || !read_u32(&b_bitrate) || !read_u32(&vbr) ||
      !read_u32(&vbr_constraint) || !read_u32(&complexity) || !read_u32(&b_bandwidth) ||
      !read_u32(&frame_size) || !read_u32(&frame_count) || !read_u32(&max_packet_bytes) ||
      !read_u32(&sample_format)) {
    fprintf(stderr, "failed to read header\n");
    return 1;
  }
  bitrate = (int32_t)b_bitrate;
  bandwidth = (int32_t)b_bandwidth;

  if (!valid_sample_rate(sample_rate) || channels == 0 || channels > 255 || frame_size == 0 ||
      max_packet_bytes == 0 ||
      (sample_format != SAMPLE_FORMAT_FLOAT32 && sample_format != SAMPLE_FORMAT_INT16)) {
    fprintf(stderr, "invalid encoder dimensions\n");
    return 1;
  }

  enc = opus_multistream_surround_encoder_create((opus_int32)sample_rate, (int)channels,
                                                 (int)mapping_family, &streams, &coupled_streams,
                                                 mapping, (int)application, &err);
  if (enc == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_multistream_surround_encoder_create failed: %d\n", err);
    return 1;
  }

  if (opus_multistream_encoder_ctl(enc, OPUS_SET_BITRATE(bitrate)) != OPUS_OK ||
      opus_multistream_encoder_ctl(enc, OPUS_SET_VBR((int)vbr)) != OPUS_OK ||
      opus_multistream_encoder_ctl(enc, OPUS_SET_VBR_CONSTRAINT((int)vbr_constraint)) != OPUS_OK ||
      opus_multistream_encoder_ctl(enc, OPUS_SET_COMPLEXITY((int)complexity)) != OPUS_OK ||
      opus_multistream_encoder_ctl(enc, OPUS_SET_BANDWIDTH(bandwidth)) != OPUS_OK) {
    fprintf(stderr, "encoder ctl failed\n");
    opus_multistream_encoder_destroy(enc);
    return 1;
  }

  item_size = sample_format == SAMPLE_FORMAT_INT16 ? sizeof(opus_int16) : sizeof(float);
  if (channels > SIZE_MAX / frame_size ||
      (size_t)channels * (size_t)frame_size > SIZE_MAX / item_size) {
    fprintf(stderr, "frame buffer overflow\n");
    opus_multistream_encoder_destroy(enc);
    return 1;
  }

  size_t frame_samples = (size_t)channels * (size_t)frame_size;
  pcm = malloc(frame_samples * item_size);
  packet = (unsigned char *)malloc(max_packet_bytes);
  if (pcm == NULL || packet == NULL) {
    fprintf(stderr, "allocation failed\n");
    free(pcm);
    free(packet);
    opus_multistream_encoder_destroy(enc);
    return 1;
  }

  if (!write_exact(GMEO_MAGIC, 4) || !write_u32(1) || !write_u32((uint32_t)streams) ||
      !write_u32((uint32_t)coupled_streams) || !write_u32(channels)) {
    fprintf(stderr, "failed to write output header\n");
    free(pcm);
    free(packet);
    opus_multistream_encoder_destroy(enc);
    return 1;
  }
  if (!write_exact(mapping, channels)) {
    fprintf(stderr, "failed to write mapping\n");
    free(pcm);
    free(packet);
    opus_multistream_encoder_destroy(enc);
    return 1;
  }
  if (!write_u32(frame_count)) {
    fprintf(stderr, "failed to write packet count\n");
    free(pcm);
    free(packet);
    opus_multistream_encoder_destroy(enc);
    return 1;
  }

  for (uint32_t i = 0; i < frame_count; i++) {
    if (!read_exact(pcm, frame_samples * item_size)) {
      fprintf(stderr, "failed to read pcm frame %u\n", i);
      free(pcm);
      free(packet);
      opus_multistream_encoder_destroy(enc);
      return 1;
    }

    int nbytes;
    if (sample_format == SAMPLE_FORMAT_INT16) {
      nbytes = opus_multistream_encode(enc, (const opus_int16 *)pcm, (int)frame_size, packet,
                                       (opus_int32)max_packet_bytes);
    } else {
      nbytes = opus_multistream_encode_float(enc, (const float *)pcm, (int)frame_size, packet,
                                             (opus_int32)max_packet_bytes);
    }
    if (nbytes < 0) {
      fprintf(stderr, "opus_multistream_encode failed: %d\n", nbytes);
      free(pcm);
      free(packet);
      opus_multistream_encoder_destroy(enc);
      return 1;
    }

    if (!write_u32((uint32_t)nbytes) || (nbytes > 0 && !write_exact(packet, (size_t)nbytes))) {
      fprintf(stderr, "failed to write packet %u\n", i);
      free(pcm);
      free(packet);
      opus_multistream_encoder_destroy(enc);
      return 1;
    }
  }

  free(pcm);
  free(packet);
  opus_multistream_encoder_destroy(enc);
  return 0;
}
