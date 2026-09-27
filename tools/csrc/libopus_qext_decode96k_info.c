/* Selected ENABLE_QEXT full-packet decode oracle.
 *
 * Decodes a sequence of Opus packets through one QEXT-enabled OpusDecoder at
 * the requested API sample rate. At 96 kHz libopus selects its native
 * 96 kHz CELT geometry; API rates through 48 kHz use the 48 kHz CELT geometry
 * with the configured integer downsample factor.
 *
 * Protocol (little-endian):
 *   in : "GQDI" magic, u32 version(=1|2|3|4|5|6|7|8),
 *        u32 sampleFormat (0=float32, 1=int16, 2=int24; version 3 uses 2),
 *        u32 channels (1|2), u32 maxFrameSize (per-channel samples at the API rate),
 *        u32 packetCount, [version 2/3/4: i32 output gain in Q8 dB],
 *        [version 4/5: u32 API sampleRate (8000|12000|16000|24000|48000|96000)],
 *        [version 5/6/7: u32 phaseInversionDisabled (0|1)],
 *        [version 7: u32 ignoreExtensions (0|1)],
 *        then for each packet: [version 3: u32 sampleFormat (1|2)],
 *        [version 8: u32 decodeFEC (0|1)],
 *        u32 packetLen, packetLen bytes
 *   out: "GQDO" magic, matching version,
 *        u32 totalSamples (interleaved element count across all packets),
 *        totalSamples elements of sampleFormat (version 3: int32; int16 frames
 *        are sign-extended),
 *        u32 packetCount, packetCount * u32 finalRange,
 *        [version 6/8: packetCount * i32 decodeStatus]
 */
#include "config.h"
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

static int read_i32(int32_t *out) {
  uint32_t v;
  if (!read_u32(&v)) return 0;
  *out = (int32_t)v;
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

static int supported_sample_rate(uint32_t sample_rate) {
  return sample_rate == 8000 || sample_rate == 12000 || sample_rate == 16000 ||
         sample_rate == 24000 || sample_rate == 48000 || sample_rate == 96000;
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
  int32_t gain_q8 = 0;
  uint32_t sample_format = SAMPLE_FORMAT_FLOAT32;
  uint32_t channels = 0;
  uint32_t sample_rate = 96000;
  uint32_t phase_inversion_disabled = 0;
  uint32_t ignore_extensions = 0;
  uint32_t frame_size = 0;
  uint32_t packet_count = 0;
  size_t frame_samples = 0;
  size_t item_size = sizeof(float);
  void *frame = NULL;
  void *decoded = NULL;
  opus_uint32 *ranges = NULL;
  opus_int32 *statuses = NULL;
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
  if (!read_u32(&version) || (version != 1 && version != 2 && version != 3 && version != 4 && version != 5 && version != 6 && version != 7 && version != 8)) {
    fprintf(stderr, "unsupported input version\n");
    return 1;
  }
  if (!read_u32(&sample_format) || !read_u32(&channels) || !read_u32(&frame_size) || !read_u32(&packet_count)) {
    fprintf(stderr, "failed to read header\n");
    return 1;
  }
  if (version >= 2 && !read_i32(&gain_q8)) {
    fprintf(stderr, "failed to read gain\n");
    return 1;
  }
  if (version >= 4 && !read_u32(&sample_rate)) {
    fprintf(stderr, "failed to read sample rate\n");
    return 1;
  }
  if (version >= 5 && !read_u32(&phase_inversion_disabled)) {
    fprintf(stderr, "failed to read phase-inversion control\n");
    return 1;
  }
  if (version >= 7 && !read_u32(&ignore_extensions)) {
    fprintf(stderr, "failed to read extension-ignore control\n");
    return 1;
  }
  if (sample_format != SAMPLE_FORMAT_FLOAT32 && sample_format != SAMPLE_FORMAT_INT16 && sample_format != SAMPLE_FORMAT_INT24) {
    fprintf(stderr, "unsupported sample format\n");
    return 1;
  }
  if (version == 3 && sample_format != SAMPLE_FORMAT_INT24) {
    fprintf(stderr, "mixed-format output must use int32 samples\n");
    return 1;
  }
  if (version >= 4 && !supported_sample_rate(sample_rate)) {
    fprintf(stderr, "unsupported QEXT API sample rate\n");
    return 1;
  }
  if (version >= 5 && phase_inversion_disabled > 1) {
    fprintf(stderr, "invalid phase-inversion control\n");
    return 1;
  }
  if (version >= 7 && ignore_extensions > 1) {
    fprintf(stderr, "invalid extension-ignore control\n");
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

  /* The selected native QEXT mode is chosen by the API sample rate. */
  dec = opus_decoder_create((int)sample_rate, (int)channels, &err);
  if (dec == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_decoder_create(%u) failed: %d\n", sample_rate, err);
    free(frame);
    return 1;
  }
  if (version >= 2 && opus_decoder_ctl(dec, OPUS_SET_GAIN(gain_q8)) != OPUS_OK) {
    fprintf(stderr, "OPUS_SET_GAIN failed\n");
    opus_decoder_destroy(dec);
    free(frame);
    return 1;
  }
  if (version >= 5 && opus_decoder_ctl(dec, OPUS_SET_PHASE_INVERSION_DISABLED((int)phase_inversion_disabled)) != OPUS_OK) {
    fprintf(stderr, "OPUS_SET_PHASE_INVERSION_DISABLED failed\n");
    opus_decoder_destroy(dec);
    free(frame);
    return 1;
  }
  if (version >= 7 && opus_decoder_ctl(dec, OPUS_SET_IGNORE_EXTENSIONS((int)ignore_extensions)) != OPUS_OK) {
    fprintf(stderr, "OPUS_SET_IGNORE_EXTENSIONS failed\n");
    opus_decoder_destroy(dec);
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
    if (version == 6 || version == 8) {
      statuses = (opus_int32 *)calloc(packet_count, sizeof(*statuses));
      if (statuses == NULL) {
        fprintf(stderr, "failed to allocate decode status buffer\n");
        opus_decoder_destroy(dec);
        free(frame);
        free(ranges);
        return 1;
      }
    }
  }

  for (i = 0; i < packet_count; i++) {
    uint32_t packet_len = 0;
    uint32_t packet_format = sample_format;
    uint32_t decode_fec = 0;
    unsigned char *packet = NULL;
    int decoded_samples = 0;
    opus_uint32 final_range = 0;

    if (version == 3 && !read_u32(&packet_format)) {
      fprintf(stderr, "failed to read packet sample format\n");
      opus_decoder_destroy(dec);
      free(frame);
      free(decoded);
      free(ranges);
      free(statuses);
      return 1;
    }
    if (version == 3 && packet_format != SAMPLE_FORMAT_INT16 && packet_format != SAMPLE_FORMAT_INT24) {
      fprintf(stderr, "unsupported mixed packet sample format\n");
      opus_decoder_destroy(dec);
      free(frame);
      free(decoded);
      free(ranges);
      free(statuses);
      return 1;
    }
    if (version == 8 && !read_u32(&decode_fec)) {
      fprintf(stderr, "failed to read decode_fec flag\n");
      opus_decoder_destroy(dec);
      free(frame);
      free(decoded);
      free(ranges);
      free(statuses);
      return 1;
    }
    if (version == 8 && decode_fec > 1) {
      fprintf(stderr, "invalid decode_fec flag\n");
      opus_decoder_destroy(dec);
      free(frame);
      free(decoded);
      free(ranges);
      free(statuses);
      return 1;
    }
    if (!read_u32(&packet_len)) {
      fprintf(stderr, "failed to read packet length\n");
      opus_decoder_destroy(dec);
      free(frame);
      free(decoded);
      free(ranges);
      free(statuses);
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
        free(statuses);
        return 1;
      }
    }

    if (packet_format == SAMPLE_FORMAT_INT16) {
      decoded_samples = opus_decode(dec, packet, (opus_int32)packet_len, (opus_int16 *)frame, (int)frame_size, (int)decode_fec);
    } else if (packet_format == SAMPLE_FORMAT_INT24) {
      decoded_samples = opus_decode24(dec, packet, (opus_int32)packet_len, (opus_int32 *)frame, (int)frame_size, (int)decode_fec);
    } else {
      decoded_samples = opus_decode_float(dec, packet, (opus_int32)packet_len, (float *)frame, (int)frame_size, (int)decode_fec);
    }
    free(packet);

    if (version == 6 || version == 8) statuses[i] = decoded_samples;
    if (decoded_samples < 0 && version != 6 && version != 8) {
      fprintf(stderr, "opus_decode failed: %d\n", decoded_samples);
      opus_decoder_destroy(dec);
      free(frame);
      free(decoded);
      free(ranges);
      free(statuses);
      return 1;
    }
    if (opus_decoder_ctl(dec, OPUS_GET_FINAL_RANGE(&final_range)) != OPUS_OK) {
      fprintf(stderr, "OPUS_GET_FINAL_RANGE failed\n");
      opus_decoder_destroy(dec);
      free(frame);
      free(decoded);
      free(ranges);
      free(statuses);
      return 1;
    }
    ranges[i] = final_range;

    if (decoded_samples < 0) decoded_samples = 0;

    if (version == 3 && packet_format == SAMPLE_FORMAT_INT16) {
      opus_int16 *narrow = (opus_int16 *)frame;
      opus_int32 *wide = (opus_int32 *)frame;
      size_t sample_count = (size_t)decoded_samples * (size_t)channels;
      size_t j = sample_count;
      while (j > 0) {
        j--;
        wide[j] = narrow[j];
      }
    }

    if (!append_items(&decoded, &decoded_len, &decoded_cap, frame, (size_t)decoded_samples * (size_t)channels, item_size)) {
      fprintf(stderr, "failed to append decoded samples\n");
      opus_decoder_destroy(dec);
      free(frame);
      free(decoded);
      free(ranges);
      free(statuses);
      return 1;
    }
  }

  opus_decoder_destroy(dec);

  if (!write_exact(GQDO_MAGIC, 4) || decoded_len > UINT32_MAX ||
      !write_u32(version) || !write_u32((uint32_t)decoded_len)) {
    fprintf(stderr, "failed to write output header\n");
    free(frame);
    free(decoded);
    free(ranges);
    free(statuses);
    return 1;
  }
  if (decoded_len > 0 && !write_exact(decoded, decoded_len * item_size)) {
    fprintf(stderr, "failed to write output samples\n");
    free(frame);
    free(decoded);
    free(ranges);
    free(statuses);
    return 1;
  }
  if (!write_u32(packet_count) || (packet_count > 0 && !write_exact(ranges, packet_count * sizeof(*ranges)))) {
    fprintf(stderr, "failed to write final ranges\n");
    free(frame);
    free(decoded);
    free(ranges);
    free(statuses);
    return 1;
  }
  if (version == 6 || version == 8) {
    for (i = 0; i < packet_count; i++) {
      if (!write_u32((uint32_t)statuses[i])) {
        fprintf(stderr, "failed to write decode statuses\n");
        free(frame);
        free(decoded);
        free(ranges);
        free(statuses);
        return 1;
      }
    }
  }

  free(frame);
  free(decoded);
  free(ranges);
  free(statuses);
  return 0;
}
