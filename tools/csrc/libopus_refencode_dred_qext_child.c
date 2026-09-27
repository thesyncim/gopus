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

#include "celt.h"
#include "opus.h"
#include "opus_private.h"

#define INPUT_MAGIC "GQCI"
#define OUTPUT_MAGIC "GQCO"
#define HELPER_VERSION 1
#define CELT_BANDS 21
#define MAX_FRAME_COUNT 16
#define MAX_PACKET_BYTES 12000

static int read_exact(void *dst, size_t size) {
  return fread(dst, 1, size, stdin) == size;
}

static int write_exact(const void *src, size_t size) {
  return fwrite(src, 1, size, stdout) == size;
}

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact(b, sizeof(b))) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) | ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int write_u32(uint32_t value) {
  unsigned char b[4];
  b[0] = (unsigned char)value;
  b[1] = (unsigned char)(value >> 8);
  b[2] = (unsigned char)(value >> 16);
  b[3] = (unsigned char)(value >> 24);
  return write_exact(b, sizeof(b));
}

static int read_i16s(opus_int16 *out, size_t count) {
  for (size_t i = 0; i < count; i++) {
    unsigned char b[2];
    if (!read_exact(b, sizeof(b))) return 0;
    out[i] = (opus_int16)((uint16_t)b[0] | ((uint16_t)b[1] << 8));
  }
  return 1;
}

static int read_f32s(float *out, size_t count) {
  for (size_t i = 0; i < count; i++) {
    uint32_t bits;
    if (!read_u32(&bits)) return 0;
    memcpy(&out[i], &bits, sizeof(bits));
  }
  return 1;
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
  uint32_t bitrate = 0;
  uint32_t dred_duration = 0;
  uint32_t complexity = 0;
  uint32_t bandwidth = 0;
  uint32_t signal = 0;
  uint32_t packet_loss = 0;
  uint32_t frame_size = 0;
  uint32_t frame_count = 0;
  uint32_t packet_capacity = 0;
  opus_int16 *pcm = NULL;
  unsigned char *packet = NULL;
  float mask[CELT_BANDS];
  OpusEncoder *encoder = NULL;
  int err = OPUS_OK;
  size_t samples;

  if (!set_binary_stdio()) {
    fprintf(stderr, "failed to set binary stdio mode\n");
    return 1;
  }
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0 ||
      !read_u32(&version) || version != HELPER_VERSION ||
      !read_u32(&sample_rate) || !read_u32(&bitrate) || !read_u32(&dred_duration) ||
      !read_u32(&complexity) || !read_u32(&bandwidth) || !read_u32(&signal) ||
      !read_u32(&packet_loss) || !read_u32(&frame_size) || !read_u32(&frame_count) ||
      !read_u32(&packet_capacity)) {
    fprintf(stderr, "invalid combined DRED-QEXT child input header\n");
    return 1;
  }
  if (sample_rate != 48000 || bitrate == 0 || dred_duration == 0 || dred_duration > 104 ||
      complexity > 10 || bandwidth != OPUS_BANDWIDTH_FULLBAND || signal > OPUS_SIGNAL_MUSIC ||
      packet_loss > 100 || frame_size != 960 || frame_count == 0 || frame_count > MAX_FRAME_COUNT ||
      packet_capacity == 0 || packet_capacity > MAX_PACKET_BYTES) {
    fprintf(stderr, "unsupported combined DRED-QEXT child case\n");
    return 1;
  }
  samples = frame_size;
  pcm = (opus_int16 *)malloc(samples * sizeof(*pcm));
  packet = (unsigned char *)malloc(packet_capacity);
  if (pcm == NULL || packet == NULL) {
    fprintf(stderr, "allocation failed\n");
    free(pcm);
    free(packet);
    return 1;
  }
  encoder = opus_encoder_create((opus_int32)sample_rate, 1, OPUS_APPLICATION_AUDIO, &err);
  if (encoder == NULL || err != OPUS_OK ||
      opus_encoder_ctl(encoder, OPUS_SET_BITRATE((opus_int32)bitrate)) != OPUS_OK ||
      opus_encoder_ctl(encoder, OPUS_SET_VBR(1)) != OPUS_OK ||
      opus_encoder_ctl(encoder, OPUS_SET_VBR_CONSTRAINT(1)) != OPUS_OK ||
      opus_encoder_ctl(encoder, OPUS_SET_COMPLEXITY((int)complexity)) != OPUS_OK ||
      opus_encoder_ctl(encoder, OPUS_SET_BANDWIDTH((int)bandwidth)) != OPUS_OK ||
      opus_encoder_ctl(encoder, OPUS_SET_MAX_BANDWIDTH(OPUS_BANDWIDTH_FULLBAND)) != OPUS_OK ||
      opus_encoder_ctl(encoder, OPUS_SET_SIGNAL((int)signal)) != OPUS_OK ||
      opus_encoder_ctl(encoder, OPUS_SET_PACKET_LOSS_PERC((int)packet_loss)) != OPUS_OK ||
      opus_encoder_ctl(encoder, OPUS_SET_FORCE_MODE(MODE_CELT_ONLY)) != OPUS_OK ||
      opus_encoder_ctl(encoder, OPUS_SET_QEXT(1)) != OPUS_OK ||
      opus_encoder_ctl(encoder, OPUS_SET_DRED_DURATION((int)dred_duration)) != OPUS_OK) {
    fprintf(stderr, "failed to create or configure child encoder: %d\n", err);
    free(pcm);
    free(packet);
    if (encoder != NULL) opus_encoder_destroy(encoder);
    return 1;
  }
  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(HELPER_VERSION) || !write_u32(frame_count)) {
    fprintf(stderr, "failed to write child response header\n");
    free(pcm);
    free(packet);
    opus_encoder_destroy(encoder);
    return 1;
  }
  for (uint32_t frame = 0; frame < frame_count; frame++) {
    if (!read_f32s(mask, CELT_BANDS) || !read_i16s(pcm, samples) ||
        opus_encoder_ctl(encoder, OPUS_SET_ENERGY_MASK(mask)) != OPUS_OK) {
      fprintf(stderr, "invalid child frame input at %u\n", frame);
      free(pcm);
      free(packet);
      opus_encoder_destroy(encoder);
      return 1;
    }
    int nbytes = opus_encode(encoder, pcm, (int)frame_size, packet, (opus_int32)packet_capacity);
    if (nbytes < 0) {
      fprintf(stderr, "child encode failed at frame %u: %d\n", frame, nbytes);
      free(pcm);
      free(packet);
      opus_encoder_destroy(encoder);
      return 1;
    }
    opus_uint32 final_range = 0;
    if (opus_encoder_ctl(encoder, OPUS_GET_FINAL_RANGE(&final_range)) != OPUS_OK ||
        !write_u32(final_range) || !write_u32((uint32_t)nbytes) || !write_exact(packet, (size_t)nbytes)) {
      fprintf(stderr, "failed to write child frame %u result\n", frame);
      free(pcm);
      free(packet);
      opus_encoder_destroy(encoder);
      return 1;
    }
  }
  free(pcm);
  free(packet);
  opus_encoder_destroy(encoder);
  return 0;
}
