#include <limits.h>
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

#if !defined(ENABLE_DRED) || !defined(ENABLE_DEEP_PLC) || !defined(ENABLE_QEXT)
#error "this helper requires the selected libopus build to enable DRED, deep PLC, and QEXT"
#endif
#ifdef FIXED_POINT
#error "this helper requires the floating-point libopus build"
#endif

#include "opus_multistream.h"
#include "opus_projection.h"
#include "opus_private.h"

#define INPUT_MAGIC "GMQI"
#define OUTPUT_MAGIC "GMQO"
#define HELPER_VERSION 3
#define MAX_CHANNELS 38
#define MAX_FRAME_COUNT 256
#define MAX_PACKET_BYTES 12000
#define ENCODER_CTL(encoder, projection, ...) \
  ((projection) ? opus_projection_encoder_ctl((OpusProjectionEncoder *)(encoder), __VA_ARGS__) : \
                 opus_multistream_encoder_ctl((OpusMSEncoder *)(encoder), __VA_ARGS__))

enum {
  ENCODER_KIND_SURROUND = 0,
  ENCODER_KIND_PROJECTION = 1
};

static int read_exact(void *dst, size_t size) {
  return fread(dst, 1, size, stdin) == size;
}

static int write_exact(const void *src, size_t size) {
  return fwrite(src, 1, size, stdout) == size;
}

static int read_u32(uint32_t *out) {
  unsigned char bytes[4];
  if (!read_exact(bytes, sizeof(bytes))) return 0;
  *out = (uint32_t)bytes[0] |
         ((uint32_t)bytes[1] << 8) |
         ((uint32_t)bytes[2] << 16) |
         ((uint32_t)bytes[3] << 24);
  return 1;
}

static int write_u32(uint32_t value) {
  unsigned char bytes[4];
  bytes[0] = (unsigned char)value;
  bytes[1] = (unsigned char)(value >> 8);
  bytes[2] = (unsigned char)(value >> 16);
  bytes[3] = (unsigned char)(value >> 24);
  return write_exact(bytes, sizeof(bytes));
}

static int read_i16(opus_int16 *out) {
  unsigned char bytes[2];
  if (!read_exact(bytes, sizeof(bytes))) return 0;
  *out = (opus_int16)((uint16_t)bytes[0] | ((uint16_t)bytes[1] << 8));
  return 1;
}

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

static void destroy_encoder(void *encoder, int projection) {
  if (projection) opus_projection_encoder_destroy((OpusProjectionEncoder *)encoder);
  else opus_multistream_encoder_destroy((OpusMSEncoder *)encoder);
}

static int get_child(void *encoder, int projection, int stream, OpusEncoder **child) {
  return ENCODER_CTL(encoder, projection, OPUS_MULTISTREAM_GET_ENCODER_STATE(stream, child));
}

int main(void) {
  unsigned char magic[4];
  uint32_t version = 0;
  uint32_t kind = 0;
  uint32_t sample_rate = 0;
  uint32_t channels = 0;
  uint32_t application = 0;
  uint32_t bitrate_u32 = 0;
  uint32_t vbr = 0;
  uint32_t vbr_constraint = 0;
  uint32_t complexity = 0;
  uint32_t bandwidth_u32 = 0;
  uint32_t signal = 0;
  uint32_t packet_loss = 0;
  uint32_t dred_duration = 0;
  uint32_t qext = 0;
  uint32_t frame_size = 0;
  uint32_t frame_count = 0;
  uint32_t packet_capacity = 0;
  int32_t bitrate = 0;
  int32_t bandwidth = 0;
  int err = OPUS_OK;
  int streams = 0;
  int coupled_streams = 0;
  unsigned char mapping[MAX_CHANNELS];
  OpusMSEncoder *multistream_encoder = NULL;
  OpusProjectionEncoder *projection_encoder = NULL;
  void *encoder = NULL;
  int projection = 0;
  opus_int16 *pcm = NULL;
  unsigned char *packet = NULL;
  size_t frame_samples;

  if (!set_binary_stdio()) {
    fprintf(stderr, "failed to set binary stdio mode\n");
    return 1;
  }
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0 ||
      !read_u32(&version) || version != HELPER_VERSION ||
      !read_u32(&kind) || !read_u32(&sample_rate) || !read_u32(&channels) ||
      !read_u32(&application) || !read_u32(&bitrate_u32) || !read_u32(&vbr) ||
      !read_u32(&vbr_constraint) || !read_u32(&complexity) || !read_u32(&bandwidth_u32) ||
      !read_u32(&signal) || !read_u32(&packet_loss) || !read_u32(&dred_duration) ||
      !read_u32(&qext) || !read_u32(&frame_size) || !read_u32(&frame_count) ||
      !read_u32(&packet_capacity)) {
    fprintf(stderr, "invalid combined DRED-QEXT input header\n");
    return 1;
  }
  bitrate = (int32_t)bitrate_u32;
  bandwidth = (int32_t)bandwidth_u32;
  projection = kind == ENCODER_KIND_PROJECTION;

  if ((kind != ENCODER_KIND_SURROUND && kind != ENCODER_KIND_PROJECTION) ||
      sample_rate != 48000 || channels == 0 || channels > MAX_CHANNELS ||
      application != OPUS_APPLICATION_AUDIO || bitrate <= 0 || vbr > 1 ||
      vbr_constraint > 1 || complexity > 10 || signal > OPUS_SIGNAL_MUSIC ||
      packet_loss > 100 || dred_duration == 0 || dred_duration > 104 || qext != 1 ||
      frame_size != 960 || frame_count == 0 || frame_count > MAX_FRAME_COUNT ||
      packet_capacity == 0 || packet_capacity > MAX_PACKET_BYTES) {
    fprintf(stderr, "unsupported combined DRED-QEXT encoder case\n");
    return 1;
  }

  if (projection) {
    projection_encoder = opus_projection_ambisonics_encoder_create(
        (opus_int32)sample_rate, (int)channels, 3, &streams, &coupled_streams,
        (int)application, &err);
    encoder = projection_encoder;
  } else {
    multistream_encoder = opus_multistream_surround_encoder_create(
        (opus_int32)sample_rate, (int)channels, 1, &streams, &coupled_streams,
        mapping, (int)application, &err);
    encoder = multistream_encoder;
  }
  if (encoder == NULL || err != OPUS_OK) {
    fprintf(stderr, "encoder create failed: %d\n", err);
    return 1;
  }
  if (projection) {
    for (uint32_t ch = 0; ch < channels; ch++) mapping[ch] = (unsigned char)ch;
  }

  if (ENCODER_CTL(encoder, projection, OPUS_SET_BITRATE(bitrate)) != OPUS_OK ||
      ENCODER_CTL(encoder, projection, OPUS_SET_VBR((int)vbr)) != OPUS_OK ||
      ENCODER_CTL(encoder, projection, OPUS_SET_VBR_CONSTRAINT((int)vbr_constraint)) != OPUS_OK ||
      ENCODER_CTL(encoder, projection, OPUS_SET_COMPLEXITY((int)complexity)) != OPUS_OK ||
      ENCODER_CTL(encoder, projection, OPUS_SET_BANDWIDTH(bandwidth)) != OPUS_OK ||
      ENCODER_CTL(encoder, projection, OPUS_SET_MAX_BANDWIDTH(OPUS_BANDWIDTH_FULLBAND)) != OPUS_OK ||
      ENCODER_CTL(encoder, projection, OPUS_SET_SIGNAL((int)signal)) != OPUS_OK ||
      ENCODER_CTL(encoder, projection, OPUS_SET_PACKET_LOSS_PERC((int)packet_loss)) != OPUS_OK ||
      ENCODER_CTL(encoder, projection, OPUS_SET_FORCE_MODE(MODE_CELT_ONLY)) != OPUS_OK ||
      ENCODER_CTL(encoder, projection, OPUS_SET_QEXT((int)qext)) != OPUS_OK) {
    fprintf(stderr, "multistream encoder controls failed\n");
    destroy_encoder(encoder, projection);
    return 1;
  }

  for (int stream = 0; stream < streams; stream++) {
    OpusEncoder *child = NULL;
    opus_int32 got_dred = 0;
    opus_int32 got_qext = 0;
    if (get_child(encoder, projection, stream, &child) != OPUS_OK || child == NULL ||
        opus_encoder_ctl(child, OPUS_SET_DRED_DURATION((int)dred_duration)) != OPUS_OK ||
        opus_encoder_ctl(child, OPUS_GET_DRED_DURATION(&got_dred)) != OPUS_OK ||
        opus_encoder_ctl(child, OPUS_GET_QEXT(&got_qext)) != OPUS_OK ||
        got_dred != (opus_int32)dred_duration || got_qext != (opus_int32)qext) {
      fprintf(stderr, "stream %d combined feature controls did not stick\n", stream);
      destroy_encoder(encoder, projection);
      return 1;
    }
  }

  frame_samples = (size_t)channels * (size_t)frame_size;
  if (frame_samples > SIZE_MAX / sizeof(*pcm)) {
    fprintf(stderr, "frame sample count overflow\n");
    destroy_encoder(encoder, projection);
    return 1;
  }
  pcm = (opus_int16 *)malloc(frame_samples * sizeof(*pcm));
  packet = (unsigned char *)malloc(packet_capacity);
  if (pcm == NULL || packet == NULL) {
    fprintf(stderr, "allocation failed\n");
    free(pcm);
    free(packet);
    destroy_encoder(encoder, projection);
    return 1;
  }

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(HELPER_VERSION) ||
      !write_u32((uint32_t)streams) || !write_u32((uint32_t)coupled_streams) ||
      !write_u32(channels) || !write_exact(mapping, channels) ||
      !write_u32((uint32_t)dred_duration) ||
      !write_u32(qext) || !write_u32(frame_count)) {
    fprintf(stderr, "failed to write combined DRED-QEXT output header\n");
    free(pcm);
    free(packet);
    destroy_encoder(encoder, projection);
    return 1;
  }

  for (uint32_t frame = 0; frame < frame_count; frame++) {
    uint32_t reset_before = 0;
    if (!read_u32(&reset_before) || reset_before > 1) {
      fprintf(stderr, "invalid reset flag at frame %u\n", frame);
      free(pcm);
      free(packet);
      destroy_encoder(encoder, projection);
      return 1;
    }
    if (reset_before) {
      if (ENCODER_CTL(encoder, projection, OPUS_RESET_STATE) != OPUS_OK) {
        fprintf(stderr, "OPUS_RESET_STATE failed at frame %u\n", frame);
        free(pcm);
        free(packet);
        destroy_encoder(encoder, projection);
        return 1;
      }
      for (int stream = 0; stream < streams; stream++) {
        OpusEncoder *child = NULL;
        if (get_child(encoder, projection, stream, &child) != OPUS_OK || child == NULL ||
            opus_encoder_ctl(child, OPUS_SET_DRED_DURATION((int)dred_duration)) != OPUS_OK) {
          fprintf(stderr, "reapply DRED duration failed at frame %u stream %d\n", frame, stream);
          free(pcm);
          free(packet);
          destroy_encoder(encoder, projection);
          return 1;
        }
      }
    }
    for (size_t sample = 0; sample < frame_samples; sample++) {
      if (!read_i16(&pcm[sample])) {
        fprintf(stderr, "failed to read PCM frame %u sample %zu\n", frame, sample);
        free(pcm);
        free(packet);
        destroy_encoder(encoder, projection);
        return 1;
      }
    }

    int nbytes;
    if (projection) {
      nbytes = opus_projection_encode(projection_encoder, pcm, (int)frame_size,
                                      packet, (opus_int32)packet_capacity);
    } else {
      nbytes = opus_multistream_encode(multistream_encoder, pcm, (int)frame_size,
                                       packet, (opus_int32)packet_capacity);
    }
    if (nbytes < 0) {
      fprintf(stderr, "encode failed at frame %u: %d\n", frame, nbytes);
      free(pcm);
      free(packet);
      destroy_encoder(encoder, projection);
      return 1;
    }
    opus_uint32 final_range = 0;
    OpusEncoder *first_child = NULL;
    opus_int32 frame_dred_duration = 0;
    opus_int32 frame_qext = 0;
    if (ENCODER_CTL(encoder, projection, OPUS_GET_FINAL_RANGE(&final_range)) != OPUS_OK ||
        get_child(encoder, projection, 0, &first_child) != OPUS_OK || first_child == NULL ||
        opus_encoder_ctl(first_child, OPUS_GET_DRED_DURATION(&frame_dred_duration)) != OPUS_OK ||
        opus_encoder_ctl(first_child, OPUS_GET_QEXT(&frame_qext)) != OPUS_OK ||
        !write_u32(final_range) || !write_u32((uint32_t)frame_dred_duration) ||
        !write_u32((uint32_t)frame_qext)) {
      fprintf(stderr, "failed to write frame %u result\n", frame);
      free(pcm);
      free(packet);
      destroy_encoder(encoder, projection);
      return 1;
    }
    for (int stream = 0; stream < streams; stream++) {
      OpusEncoder *child = NULL;
      opus_uint32 stream_range = 0;
      if (get_child(encoder, projection, stream, &child) != OPUS_OK || child == NULL ||
          opus_encoder_ctl(child, OPUS_GET_FINAL_RANGE(&stream_range)) != OPUS_OK ||
          !write_u32(stream_range)) {
        fprintf(stderr, "failed to write frame %u stream %d final range\n", frame, stream);
        free(pcm);
        free(packet);
        destroy_encoder(encoder, projection);
        return 1;
      }
    }
    for (int stream = 0; stream < streams; stream++) {
      OpusEncoder *child = NULL;
      opus_int32 stream_bitrate = 0;
      if (get_child(encoder, projection, stream, &child) != OPUS_OK || child == NULL ||
          opus_encoder_ctl(child, OPUS_GET_BITRATE(&stream_bitrate)) != OPUS_OK ||
          stream_bitrate < 0 || !write_u32((uint32_t)stream_bitrate)) {
        fprintf(stderr, "failed to write frame %u stream %d bitrate\n", frame, stream);
        free(pcm);
        free(packet);
        destroy_encoder(encoder, projection);
        return 1;
      }
    }
    if (!write_u32((uint32_t)nbytes) ||
        (nbytes > 0 && !write_exact(packet, (size_t)nbytes))) {
      fprintf(stderr, "failed to write frame %u packet\n", frame);
      free(pcm);
      free(packet);
      destroy_encoder(encoder, projection);
      return 1;
    }
  }

  free(pcm);
  free(packet);
  destroy_encoder(encoder, projection);
  return 0;
}
