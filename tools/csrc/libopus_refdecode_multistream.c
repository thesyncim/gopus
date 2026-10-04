#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "opus_multistream.h"
#include "opus_projection.h"

#define GMSI_MAGIC "GMSI"
#define GMSO_MAGIC "GMSO"

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
         sample_rate == 48000 || sample_rate == 96000;
}

static unsigned char *read_blob(uint32_t n) {
  if (n == 0) return NULL;
  unsigned char *data = (unsigned char *)malloc(n);
  if (data == NULL || !read_exact(data, n)) {
    free(data);
    return NULL;
  }
  return data;
}

/* Version 7 decodes independent records with a fresh decoder per packet. */
static int run_fresh_decode_batch(void) {
  uint32_t count = 0;
  if (!read_u32(&count)) {
    fprintf(stderr, "failed to read fresh-decode case count\n");
    return 1;
  }
  if (count > 256) {
    fprintf(stderr, "fresh-decode batch has too many cases: %u\n", count);
    return 1;
  }
  if (!write_exact(GMSO_MAGIC, 4) || !write_u32(7) || !write_u32(count)) {
    fprintf(stderr, "failed to write fresh-decode output header\n");
    return 1;
  }

  for (uint32_t i = 0; i < count; i++) {
    uint32_t sample_rate, raw_gain, sample_format, family, channels, streams, coupled;
    uint32_t frame_size, mapping_len, demix_len, packet_len;
    unsigned char *mapping = NULL, *demixing = NULL, *packet = NULL;
    void *frame = NULL;
    OpusMSDecoder *ms = NULL;
    OpusProjectionDecoder *projection = NULL;
    int result = 0;
    opus_uint32 final_range = 0;
    size_t sample_count, item_size, pcm_bytes = 0;

    if (!read_u32(&sample_rate) || !read_u32(&raw_gain) || !read_u32(&sample_format) ||
        !read_u32(&family) || !read_u32(&channels) || !read_u32(&streams) || !read_u32(&coupled) ||
        !read_u32(&frame_size) || !read_u32(&mapping_len) || !read_u32(&demix_len) || !read_u32(&packet_len)) {
      fprintf(stderr, "failed to read fresh-decode case %u header\n", i);
      goto case_fail;
    }
    if (!valid_sample_rate(sample_rate) || channels == 0 || channels > 255 || streams == 0 || streams > 255 ||
        coupled > 255 || frame_size == 0 || frame_size > (sample_rate * 120U / 1000U) ||
        sample_format > SAMPLE_FORMAT_INT24 || mapping_len > 255 || demix_len > 1048576 || packet_len > 1048576 ||
        (family != 3 && mapping_len < channels) ||
        (family == 3 && demix_len != (uint64_t)channels * (streams + coupled) * sizeof(opus_int16)) ||
        (size_t)channels > SIZE_MAX / (size_t)frame_size ||
        (size_t)channels * (size_t)frame_size > SIZE_MAX / sizeof(uint32_t)) {
      fprintf(stderr, "invalid fresh-decode case %u dimensions\n", i);
      goto case_fail;
    }

    mapping = read_blob(mapping_len);
    demixing = read_blob(demix_len);
    packet = read_blob(packet_len);
    if ((mapping_len && mapping == NULL) || (demix_len && demixing == NULL) || (packet_len && packet == NULL)) {
      fprintf(stderr, "failed to read fresh-decode case %u payload\n", i);
      goto case_fail;
    }

    sample_count = (size_t)channels * (size_t)frame_size;
    item_size = sample_format == SAMPLE_FORMAT_INT16 ? sizeof(opus_int16) : sizeof(uint32_t);
    frame = calloc(sample_count, sizeof(uint32_t));
    if (frame == NULL) {
      fprintf(stderr, "failed to allocate fresh-decode case %u PCM\n", i);
      goto case_fail;
    }

    if (family == 3) {
      int err = OPUS_OK;
      projection = opus_projection_decoder_create((opus_int32)sample_rate, (int)channels, (int)streams,
          (int)coupled, demixing, (opus_int32)demix_len, &err);
      if (projection == NULL || err != OPUS_OK) {
        fprintf(stderr, "fresh projection decoder create case %u failed: %d\n", i, err);
        goto case_fail;
      }
      if ((int32_t)raw_gain != 0 &&
          opus_projection_decoder_ctl(projection, OPUS_SET_GAIN((int32_t)raw_gain)) != OPUS_OK) {
        fprintf(stderr, "fresh projection decoder gain case %u failed\n", i);
        goto case_fail;
      }
      if (sample_format == SAMPLE_FORMAT_INT16) {
        result = opus_projection_decode(projection, packet, (opus_int32)packet_len, (opus_int16 *)frame,
            (int)frame_size, 0);
      } else if (sample_format == SAMPLE_FORMAT_INT24) {
        result = opus_projection_decode24(projection, packet, (opus_int32)packet_len, (opus_int32 *)frame,
            (int)frame_size, 0);
      } else {
        result = opus_projection_decode_float(projection, packet, (opus_int32)packet_len, (float *)frame,
            (int)frame_size, 0);
      }
    } else {
      int err = OPUS_OK;
      ms = opus_multistream_decoder_create((opus_int32)sample_rate, (int)channels, (int)streams,
          (int)coupled, mapping, &err);
      if (ms == NULL || err != OPUS_OK) {
        fprintf(stderr, "fresh multistream decoder create case %u failed: %d\n", i, err);
        goto case_fail;
      }
      if ((int32_t)raw_gain != 0 && opus_multistream_decoder_ctl(ms, OPUS_SET_GAIN((int32_t)raw_gain)) != OPUS_OK) {
        fprintf(stderr, "fresh multistream decoder gain case %u failed\n", i);
        goto case_fail;
      }
      if (sample_format == SAMPLE_FORMAT_INT16) {
        result = opus_multistream_decode(ms, packet, (opus_int32)packet_len, (opus_int16 *)frame,
            (int)frame_size, 0);
      } else if (sample_format == SAMPLE_FORMAT_INT24) {
        result = opus_multistream_decode24(ms, packet, (opus_int32)packet_len, (opus_int32 *)frame,
            (int)frame_size, 0);
      } else {
        result = opus_multistream_decode_float(ms, packet, (opus_int32)packet_len, (float *)frame,
            (int)frame_size, 0);
      }
    }

    if (ms != NULL) {
      if (opus_multistream_decoder_ctl(ms, OPUS_GET_FINAL_RANGE(&final_range)) != OPUS_OK) {
        fprintf(stderr, "fresh multistream final range case %u failed\n", i);
        goto case_fail;
      }
    } else if (opus_projection_decoder_ctl(projection, OPUS_GET_FINAL_RANGE(&final_range)) != OPUS_OK) {
      fprintf(stderr, "fresh projection final range case %u failed\n", i);
      goto case_fail;
    }

    if (result > 0) {
      if ((size_t)result > SIZE_MAX / (size_t)channels / item_size) {
        fprintf(stderr, "fresh-decode case %u PCM output overflows size_t\n", i);
        goto case_fail;
      }
      pcm_bytes = (size_t)result * (size_t)channels * item_size;
      if (pcm_bytes > UINT32_MAX) {
        fprintf(stderr, "fresh-decode case %u PCM output is too large\n", i);
        goto case_fail;
      }
    }
    if (!write_u32((uint32_t)result) || !write_u32(final_range) || !write_u32((uint32_t)pcm_bytes) ||
        (pcm_bytes && !write_exact(frame, pcm_bytes))) {
      fprintf(stderr, "failed to write fresh-decode case %u output\n", i);
      goto case_fail;
    }

    if (ms != NULL) opus_multistream_decoder_destroy(ms);
    if (projection != NULL) opus_projection_decoder_destroy(projection);
    free(frame);
    free(mapping);
    free(demixing);
    free(packet);
    continue;

case_fail:
    if (ms != NULL) opus_multistream_decoder_destroy(ms);
    if (projection != NULL) opus_projection_decoder_destroy(projection);
    free(frame);
    free(mapping);
    free(demixing);
    free(packet);
    return 1;
  }
  return 0;
}

static int append_items(void **out, size_t *out_len, size_t *out_cap, const void *src, size_t n, size_t item_size) {
  if (n == 0) {
    return 1;
  }

  if (n > SIZE_MAX - *out_len) {
    return 0;
  }
  size_t need = *out_len + n;
  if (need > SIZE_MAX / item_size) {
    return 0;
  }
  if (need > *out_cap) {
    size_t new_cap = *out_cap ? *out_cap : 1024;
    while (new_cap < need) {
      if (new_cap > SIZE_MAX / 2) {
        new_cap = need;
        break;
      }
      new_cap *= 2;
    }
    if (new_cap > SIZE_MAX / item_size) {
      return 0;
    }
    void *resized = realloc(*out, new_cap * item_size);
    if (resized == NULL) {
      return 0;
    }
    *out = resized;
    *out_cap = new_cap;
  }

  memcpy((unsigned char *)(*out) + *out_len * item_size, src, n * item_size);
  *out_len = need;
  return 1;
}

int main(void) {
  unsigned char magic[4];
  uint32_t version = 0;
  uint32_t sample_rate = 48000;
  int32_t decode_gain = 0;
  uint32_t phase_inversion_disabled = 0;
  uint32_t sample_format = SAMPLE_FORMAT_FLOAT32;
  uint32_t family = 0;
  uint32_t channels = 0;
  uint32_t streams = 0;
  uint32_t coupled = 0;
  uint32_t frame_size = 0;
  uint32_t packet_count = 0;
  uint32_t mapping_len = 0;
  uint32_t demix_len = 0;

  unsigned char *mapping = NULL;
  unsigned char *demixing = NULL;
  void *frame = NULL;
  void *decoded = NULL;
  size_t decoded_len = 0;
  size_t decoded_cap = 0;
  size_t item_size = sizeof(float);

  if (!set_binary_stdio()) {
    fprintf(stderr, "failed to set binary stdio mode\n");
    return 1;
  }

  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, GMSI_MAGIC, sizeof(magic)) != 0) {
    fprintf(stderr, "invalid input magic\n");
    return 1;
  }

  if (!read_u32(&version)) {
    fprintf(stderr, "failed to read header\n");
    return 1;
  }

  if (version == 7) return run_fresh_decode_batch();

  if (version >= 2 && version <= 6) {
    if (!read_u32(&sample_rate)) {
      fprintf(stderr, "failed to read sample rate\n");
      return 1;
    }
    if (version >= 3) {
      uint32_t raw_gain = 0;
      if (!read_u32(&raw_gain)) {
        fprintf(stderr, "failed to read decode gain\n");
        return 1;
      }
      decode_gain = (int32_t)raw_gain;
    }
    if (version >= 4 && !read_u32(&sample_format)) {
      fprintf(stderr, "failed to read sample format\n");
      return 1;
    }
  } else if (version != 1) {
    fprintf(stderr, "unsupported input version: %u\n", version);
    return 1;
  }

  if (!read_u32(&family) || !read_u32(&channels) || !read_u32(&streams) || !read_u32(&coupled) ||
      !read_u32(&frame_size) || !read_u32(&packet_count) || !read_u32(&mapping_len) || !read_u32(&demix_len)) {
    fprintf(stderr, "failed to read header\n");
    return 1;
  }

  if (version >= 5 && !read_u32(&phase_inversion_disabled)) {
    fprintf(stderr, "failed to read phase inversion control\n");
    return 1;
  }

  if (!valid_sample_rate(sample_rate) || channels == 0 || streams == 0 || frame_size == 0 ||
      phase_inversion_disabled > 1 ||
      (sample_format != SAMPLE_FORMAT_FLOAT32 && sample_format != SAMPLE_FORMAT_INT16 && sample_format != SAMPLE_FORMAT_INT24)) {
    fprintf(stderr, "invalid decoder dimensions\n");
    return 1;
  }

  if (mapping_len > 0) {
    mapping = (unsigned char *)malloc(mapping_len);
    if (mapping == NULL || !read_exact(mapping, mapping_len)) {
      fprintf(stderr, "failed to read mapping\n");
      free(mapping);
      return 1;
    }
  }

  if (demix_len > 0) {
    demixing = (unsigned char *)malloc(demix_len);
    if (demixing == NULL || !read_exact(demixing, demix_len)) {
      fprintf(stderr, "failed to read demixing matrix\n");
      free(mapping);
      free(demixing);
      return 1;
    }
  }

  item_size = sample_format == SAMPLE_FORMAT_INT16 ? sizeof(opus_int16) :
              sample_format == SAMPLE_FORMAT_INT24 ? sizeof(opus_int32) :
              sizeof(float);
  if (channels > SIZE_MAX / frame_size || (size_t)channels * (size_t)frame_size > SIZE_MAX / sizeof(float)) {
    fprintf(stderr, "frame buffer overflow\n");
    free(mapping);
    free(demixing);
    return 1;
  }

  /* Version 6 carries a format before each packet and returns a byte count. */
  frame = malloc((size_t)channels * (size_t)frame_size * sizeof(float));
  if (frame == NULL) {
    fprintf(stderr, "failed to allocate frame buffer\n");
    free(mapping);
    free(demixing);
    return 1;
  }

  if (family == 3) {
    int err = OPUS_OK;
    OpusProjectionDecoder *dec = opus_projection_decoder_create(
        (opus_int32)sample_rate, (int)channels, (int)streams, (int)coupled, demixing, (opus_int32)demix_len, &err);
    if (dec == NULL || err != OPUS_OK) {
      fprintf(stderr, "opus_projection_decoder_create failed: %d\n", err);
      free(mapping);
      free(demixing);
      free(frame);
      return 1;
    }
    if (decode_gain != 0) {
      err = opus_projection_decoder_ctl(dec, OPUS_SET_GAIN(decode_gain));
      if (err != OPUS_OK) {
        fprintf(stderr, "opus_projection_decoder_ctl(OPUS_SET_GAIN) failed: %d\n", err);
        opus_projection_decoder_destroy(dec);
        free(mapping);
        free(demixing);
        free(frame);
        return 1;
      }
    }
    for (uint32_t i = 0; i < packet_count; i++) {
      uint32_t packet_len = 0;
      unsigned char *packet = NULL;
      int decoded_samples = 0;

      if (version == 6) {
        if (!read_u32(&sample_format) || sample_format > SAMPLE_FORMAT_INT24) {
          fprintf(stderr, "invalid packet sample format\n");
          free(mapping);
          free(demixing);
          free(frame);
          free(decoded);
          opus_projection_decoder_destroy(dec);
          return 1;
        }
        item_size = sample_format == SAMPLE_FORMAT_INT16 ? sizeof(opus_int16) : sizeof(float);
      }
      if (!read_u32(&packet_len)) {
        fprintf(stderr, "failed to read packet length\n");
        opus_projection_decoder_destroy(dec);
        free(mapping);
        free(demixing);
        free(frame);
        free(decoded);
        return 1;
      }

      if (packet_len > 0) {
        packet = (unsigned char *)malloc(packet_len);
        if (packet == NULL || !read_exact(packet, packet_len)) {
          fprintf(stderr, "failed to read packet payload\n");
          free(packet);
          opus_projection_decoder_destroy(dec);
          free(mapping);
          free(demixing);
          free(frame);
          free(decoded);
          return 1;
        }
      }

      if (sample_format == SAMPLE_FORMAT_INT16) {
        decoded_samples = opus_projection_decode(dec, packet, (opus_int32)packet_len, (opus_int16 *)frame, (int)frame_size, 0);
      } else if (sample_format == SAMPLE_FORMAT_INT24) {
        decoded_samples = opus_projection_decode24(dec, packet, (opus_int32)packet_len, (opus_int32 *)frame, (int)frame_size, 0);
      } else {
        decoded_samples = opus_projection_decode_float(dec, packet, (opus_int32)packet_len, (float *)frame, (int)frame_size, 0);
      }
      free(packet);

      if (decoded_samples < 0) {
        fprintf(stderr, "opus_projection_decode_float failed: %d\n", decoded_samples);
        opus_projection_decoder_destroy(dec);
        free(mapping);
        free(demixing);
        free(frame);
        free(decoded);
        return 1;
      }

      if (!append_items(&decoded, &decoded_len, &decoded_cap, frame,
          (size_t)decoded_samples * (size_t)channels * (version == 6 ? item_size : 1),
          version == 6 ? 1 : item_size)) {
        fprintf(stderr, "failed to append decoded samples\n");
        opus_projection_decoder_destroy(dec);
        free(mapping);
        free(demixing);
        free(frame);
        free(decoded);
        return 1;
      }
    }

    opus_projection_decoder_destroy(dec);
  } else {
    int err = OPUS_OK;
    OpusMSDecoder *dec =
        opus_multistream_decoder_create((opus_int32)sample_rate, (int)channels, (int)streams, (int)coupled, mapping, &err);
    if (dec == NULL || err != OPUS_OK) {
      fprintf(stderr, "opus_multistream_decoder_create failed: %d\n", err);
      free(mapping);
      free(demixing);
      free(frame);
      return 1;
    }
    if (decode_gain != 0) {
      err = opus_multistream_decoder_ctl(dec, OPUS_SET_GAIN(decode_gain));
      if (err != OPUS_OK) {
        fprintf(stderr, "opus_multistream_decoder_ctl(OPUS_SET_GAIN) failed: %d\n", err);
        opus_multistream_decoder_destroy(dec);
        free(mapping);
        free(demixing);
        free(frame);
        return 1;
      }
    }
    if (version >= 5) {
      err = opus_multistream_decoder_ctl(dec, OPUS_SET_PHASE_INVERSION_DISABLED((int)phase_inversion_disabled));
      if (err != OPUS_OK) {
        fprintf(stderr, "opus_multistream_decoder_ctl(OPUS_SET_PHASE_INVERSION_DISABLED) failed: %d\n", err);
        opus_multistream_decoder_destroy(dec);
        free(mapping);
        free(demixing);
        free(frame);
        return 1;
      }
    }

    for (uint32_t i = 0; i < packet_count; i++) {
      uint32_t packet_len = 0;
      unsigned char *packet = NULL;
      int decoded_samples = 0;

      if (version == 6) {
        if (!read_u32(&sample_format) || sample_format > SAMPLE_FORMAT_INT24) {
          fprintf(stderr, "invalid packet sample format\n");
          free(mapping);
          free(demixing);
          free(frame);
          free(decoded);
          opus_multistream_decoder_destroy(dec);
          return 1;
        }
        item_size = sample_format == SAMPLE_FORMAT_INT16 ? sizeof(opus_int16) : sizeof(float);
      }
      if (!read_u32(&packet_len)) {
        fprintf(stderr, "failed to read packet length\n");
        opus_multistream_decoder_destroy(dec);
        free(mapping);
        free(demixing);
        free(frame);
        free(decoded);
        return 1;
      }

      if (packet_len > 0) {
        packet = (unsigned char *)malloc(packet_len);
        if (packet == NULL || !read_exact(packet, packet_len)) {
          fprintf(stderr, "failed to read packet payload\n");
          free(packet);
          opus_multistream_decoder_destroy(dec);
          free(mapping);
          free(demixing);
          free(frame);
          free(decoded);
          return 1;
        }
      }

      if (sample_format == SAMPLE_FORMAT_INT16) {
        decoded_samples = opus_multistream_decode(dec, packet, (opus_int32)packet_len, (opus_int16 *)frame, (int)frame_size, 0);
      } else if (sample_format == SAMPLE_FORMAT_INT24) {
        decoded_samples = opus_multistream_decode24(dec, packet, (opus_int32)packet_len, (opus_int32 *)frame, (int)frame_size, 0);
      } else {
        decoded_samples = opus_multistream_decode_float(dec, packet, (opus_int32)packet_len, (float *)frame, (int)frame_size, 0);
      }
      free(packet);

      if (decoded_samples < 0) {
        fprintf(stderr, "opus_multistream_decode_float failed: %d\n", decoded_samples);
        opus_multistream_decoder_destroy(dec);
        free(mapping);
        free(demixing);
        free(frame);
        free(decoded);
        return 1;
      }

      if (!append_items(&decoded, &decoded_len, &decoded_cap, frame,
          (size_t)decoded_samples * (size_t)channels * (version == 6 ? item_size : 1),
          version == 6 ? 1 : item_size)) {
        fprintf(stderr, "failed to append decoded samples\n");
        opus_multistream_decoder_destroy(dec);
        free(mapping);
        free(demixing);
        free(frame);
        free(decoded);
        return 1;
      }
    }

    opus_multistream_decoder_destroy(dec);
  }

  if (decoded_len > UINT32_MAX) {
    fprintf(stderr, "decoded output too large\n");
    free(mapping);
    free(demixing);
    free(frame);
    free(decoded);
    return 1;
  }

  if (!write_exact(GMSO_MAGIC, 4) || !write_u32(1) || !write_u32((uint32_t)decoded_len) ||
      (decoded_len > 0 && !write_exact(decoded, decoded_len * (version == 6 ? 1 : item_size)))) {
    fprintf(stderr, "failed to write output\n");
    free(mapping);
    free(demixing);
    free(frame);
    free(decoded);
    return 1;
  }

  free(mapping);
  free(demixing);
  free(frame);
  free(decoded);
  return 0;
}
