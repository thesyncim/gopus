/* Native 96 kHz QEXT full-packet decode oracle.
 *
 * Decodes a sequence of Opus packets through a single QEXT-enabled OpusDecoder
 * created at Fs=96000. With ENABLE_QEXT the libopus decoder runs the native
 * 96 kHz CELT mode (mode96000_1920_240: 1920-sample frames, 3840-MDCT, 8 short
 * blocks) plus the >20 kHz extension-band decode chain, producing real native
 * 96 kHz PCM (not a 2:1 resample of 48 kHz). gopus mirrors this with
 * celt.HD96kMode + the qext extension decode chain.
 *
 * Protocol (little-endian):
 *   in : "GQDI" magic, u32 version(=1),
 *        u32 sampleFormat (0=float32, 1=int16, 2=int24),
 *        u32 channels (1|2), u32 maxFrameSize (per-channel samples at 96 kHz),
 *        u32 packetCount,
 *        then for each packet: u32 packetLen, packetLen bytes
 *   out: "GQDO" magic, u32 version(=1),
 *        u32 totalSamples (interleaved element count across all packets),
 *        totalSamples elements of sampleFormat,
 *        u32 packetCount, packetCount * u32 finalRange
 */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "opus.h"

#define GQDI_MAGIC "GQDI"
#define GQDO_MAGIC "GQDO"

enum {
  SAMPLE_FORMAT_FLOAT32 = 0,
  SAMPLE_FORMAT_INT16 = 1,
  SAMPLE_FORMAT_INT24 = 2
};

static int read_exact(void *dst, size_t n) {
  return fread(dst, 1, n, stdin) == n;
}

static int write_exact(const void *src, size_t n) {
  return fwrite(src, 1, n, stdout) == n;
}

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact(b, 4)) return 0;
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
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

static int append_items(void **out, size_t *out_len, size_t *out_cap, const void *src, size_t n, size_t item_size) {
  size_t need;
  size_t new_cap;
  void *resized;

  if (n == 0) return 1;
  if (n > SIZE_MAX - *out_len) return 0;
  need = *out_len + n;
  if (need > SIZE_MAX / item_size) return 0;

  if (need > *out_cap) {
    new_cap = *out_cap ? *out_cap : 1024;
    while (new_cap < need) {
      if (new_cap > SIZE_MAX / 2) {
        new_cap = need;
        break;
      }
      new_cap *= 2;
    }
    if (new_cap > SIZE_MAX / item_size) return 0;
    resized = realloc(*out, new_cap * item_size);
    if (resized == NULL) return 0;
    *out = resized;
    *out_cap = new_cap;
  }

  memcpy((unsigned char *)(*out) + (*out_len * item_size), src, n * item_size);
  *out_len = need;
  return 1;
}

int main(void) {
  unsigned char magic[4];
  uint32_t version = 0;
  uint32_t sample_format = SAMPLE_FORMAT_FLOAT32;
  uint32_t channels = 0;
  uint32_t frame_size = 0;
  uint32_t packet_count = 0;
  size_t frame_samples = 0;
  size_t item_size = sizeof(float);
  void *frame = NULL;
  void *decoded = NULL;
  opus_uint32 *ranges = NULL;
  size_t decoded_len = 0;
  size_t decoded_cap = 0;
  OpusDecoder *dec = NULL;
  int err = OPUS_OK;
  uint32_t i;

  if (!set_binary_stdio()) {
    fprintf(stderr, "failed to set binary stdio mode\n");
    return 1;
  }

  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, GQDI_MAGIC, sizeof(magic)) != 0) {
    fprintf(stderr, "invalid input magic\n");
    return 1;
  }
  if (!read_u32(&version) || version != 1) {
    fprintf(stderr, "unsupported input version\n");
    return 1;
  }
  if (!read_u32(&sample_format) || !read_u32(&channels) || !read_u32(&frame_size) || !read_u32(&packet_count)) {
    fprintf(stderr, "failed to read header\n");
    return 1;
  }
  if (sample_format != SAMPLE_FORMAT_FLOAT32 && sample_format != SAMPLE_FORMAT_INT16 && sample_format != SAMPLE_FORMAT_INT24) {
    fprintf(stderr, "unsupported sample format\n");
    return 1;
  }
  if (channels == 0 || channels > 2 || frame_size == 0) {
    fprintf(stderr, "invalid decoder dimensions\n");
    return 1;
  }

  item_size = sample_format == SAMPLE_FORMAT_INT16 ? sizeof(opus_int16) :
              sample_format == SAMPLE_FORMAT_INT24 ? sizeof(opus_int32) :
              sizeof(float);
  if (channels > SIZE_MAX / frame_size) {
    fprintf(stderr, "frame buffer overflow\n");
    return 1;
  }
  frame_samples = (size_t)channels * (size_t)frame_size;
  if (frame_samples > SIZE_MAX / item_size) {
    fprintf(stderr, "frame buffer overflow\n");
    return 1;
  }
  frame = malloc(frame_samples * item_size);
  if (frame == NULL) {
    fprintf(stderr, "failed to allocate frame buffer\n");
    return 1;
  }

  /* Native 96 kHz: with ENABLE_QEXT this runs the 96 kHz CELT mode. */
  dec = opus_decoder_create(96000, (int)channels, &err);
  if (dec == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_decoder_create(96000) failed: %d\n", err);
    free(frame);
    return 1;
  }

  if (packet_count > 0) {
    ranges = (opus_uint32 *)calloc(packet_count, sizeof(*ranges));
    if (ranges == NULL) {
      fprintf(stderr, "failed to allocate final range buffer\n");
      opus_decoder_destroy(dec);
      free(frame);
      return 1;
    }
  }

  for (i = 0; i < packet_count; i++) {
    uint32_t packet_len = 0;
    unsigned char *packet = NULL;
    int decoded_samples = 0;
    opus_uint32 final_range = 0;

    if (!read_u32(&packet_len)) {
      fprintf(stderr, "failed to read packet length\n");
      opus_decoder_destroy(dec);
      free(frame);
      free(decoded);
      free(ranges);
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
        free(ranges);
        return 1;
      }
    }

    if (sample_format == SAMPLE_FORMAT_INT16) {
      decoded_samples = opus_decode(dec, packet, (opus_int32)packet_len, (opus_int16 *)frame, (int)frame_size, 0);
    } else if (sample_format == SAMPLE_FORMAT_INT24) {
      decoded_samples = opus_decode24(dec, packet, (opus_int32)packet_len, (opus_int32 *)frame, (int)frame_size, 0);
    } else {
      decoded_samples = opus_decode_float(dec, packet, (opus_int32)packet_len, (float *)frame, (int)frame_size, 0);
    }
    free(packet);

    if (decoded_samples < 0) {
      fprintf(stderr, "opus_decode failed: %d\n", decoded_samples);
      opus_decoder_destroy(dec);
      free(frame);
      free(decoded);
      free(ranges);
      return 1;
    }
    if (opus_decoder_ctl(dec, OPUS_GET_FINAL_RANGE(&final_range)) != OPUS_OK) {
      fprintf(stderr, "OPUS_GET_FINAL_RANGE failed\n");
      opus_decoder_destroy(dec);
      free(frame);
      free(decoded);
      free(ranges);
      return 1;
    }
    ranges[i] = final_range;

    if (!append_items(&decoded, &decoded_len, &decoded_cap, frame, (size_t)decoded_samples * (size_t)channels, item_size)) {
      fprintf(stderr, "failed to append decoded samples\n");
      opus_decoder_destroy(dec);
      free(frame);
      free(decoded);
      free(ranges);
      return 1;
    }
  }

  opus_decoder_destroy(dec);

  if (!write_exact(GQDO_MAGIC, 4) || decoded_len > UINT32_MAX ||
      !write_u32(1) || !write_u32((uint32_t)decoded_len)) {
    fprintf(stderr, "failed to write output header\n");
    free(frame);
    free(decoded);
    free(ranges);
    return 1;
  }
  if (decoded_len > 0 && !write_exact(decoded, decoded_len * item_size)) {
    fprintf(stderr, "failed to write output samples\n");
    free(frame);
    free(decoded);
    free(ranges);
    return 1;
  }
  if (!write_u32(packet_count) || (packet_count > 0 && !write_exact(ranges, packet_count * sizeof(*ranges)))) {
    fprintf(stderr, "failed to write final ranges\n");
    free(frame);
    free(decoded);
    free(ranges);
    return 1;
  }

  free(frame);
  free(decoded);
  free(ranges);
  return 0;
}
