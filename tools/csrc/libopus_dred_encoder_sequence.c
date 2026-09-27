#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "opus.h"
#include "opus_defines.h"

#define INPUT_SAMPLES_PER_FRAME 960
#define MAX_PACKET_BYTES 4000
#define OUTPUT_MAGIC "GDES"

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

static int write_exact(const void *src, size_t size) {
  return fwrite(src, 1, size, stdout) == size;
}

static int write_u32(uint32_t value) {
  unsigned char bytes[4];
  bytes[0] = (unsigned char)value;
  bytes[1] = (unsigned char)(value >> 8);
  bytes[2] = (unsigned char)(value >> 16);
  bytes[3] = (unsigned char)(value >> 24);
  return write_exact(bytes, sizeof(bytes));
}

static int read_float32(float *sample) {
  unsigned char bytes[4];
  uint32_t bits;
  if (fread(bytes, 1, sizeof(bytes), stdin) != sizeof(bytes)) return 0;
  bits = (uint32_t)bytes[0] |
         ((uint32_t)bytes[1] << 8) |
         ((uint32_t)bytes[2] << 16) |
         ((uint32_t)bytes[3] << 24);
  memcpy(sample, &bits, sizeof(bits));
  return 1;
}

int main(int argc, char **argv) {
  const opus_int32 sample_rate = 48000;
  const int frame_size = INPUT_SAMPLES_PER_FRAME;
  const int bitrate = 48000;
  const int complexity = 10;
  const int packet_loss = 60;
  const int dred_duration = 80;
  int channels;
  int frames;
  int packet_capacity;
  int err = OPUS_OK;
  int frame;
  OpusEncoder *encoder;
  float pcm[INPUT_SAMPLES_PER_FRAME * 2];
  unsigned char packet[MAX_PACKET_BYTES];

  if (argc != 4) {
    fprintf(stderr, "usage: %s <channels> <frames> <packet-capacity>\n", argv[0]);
    return 1;
  }
  channels = atoi(argv[1]);
  frames = atoi(argv[2]);
  packet_capacity = atoi(argv[3]);
  if ((channels != 1 && channels != 2) || frames <= 0 ||
      packet_capacity <= 0 || packet_capacity > MAX_PACKET_BYTES) {
    fprintf(stderr, "channels must be 1 or 2, frames and packet capacity must be positive, and packet capacity must not exceed %d\n", MAX_PACKET_BYTES);
    return 1;
  }
  if (!set_binary_stdio()) {
    fprintf(stderr, "failed to set binary stdio\n");
    return 1;
  }

  encoder = opus_encoder_create(sample_rate, channels,
                                OPUS_APPLICATION_RESTRICTED_LOWDELAY, &err);
  if (encoder == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_encoder_create failed: %d\n", err);
    return 1;
  }
  if (opus_encoder_ctl(encoder, OPUS_SET_BITRATE(bitrate)) != OPUS_OK ||
      opus_encoder_ctl(encoder, OPUS_SET_COMPLEXITY(complexity)) != OPUS_OK ||
      opus_encoder_ctl(encoder, OPUS_SET_BANDWIDTH(OPUS_BANDWIDTH_FULLBAND)) != OPUS_OK ||
      opus_encoder_ctl(encoder, OPUS_SET_MAX_BANDWIDTH(OPUS_BANDWIDTH_FULLBAND)) != OPUS_OK ||
      opus_encoder_ctl(encoder, OPUS_SET_SIGNAL(OPUS_SIGNAL_VOICE)) != OPUS_OK ||
      opus_encoder_ctl(encoder, OPUS_SET_PACKET_LOSS_PERC(packet_loss)) != OPUS_OK ||
      opus_encoder_ctl(encoder, OPUS_SET_FORCE_CHANNELS(OPUS_AUTO)) != OPUS_OK ||
      opus_encoder_ctl(encoder, OPUS_SET_LSB_DEPTH(24)) != OPUS_OK ||
      opus_encoder_ctl(encoder, OPUS_SET_VBR(1)) != OPUS_OK ||
      opus_encoder_ctl(encoder, OPUS_SET_VBR_CONSTRAINT(1)) != OPUS_OK ||
      opus_encoder_ctl(encoder, OPUS_SET_DRED_DURATION(dred_duration)) != OPUS_OK) {
    fprintf(stderr, "failed to apply encoder controls\n");
    opus_encoder_destroy(encoder);
    return 1;
  }

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) ||
      !write_u32((uint32_t)sample_rate) || !write_u32((uint32_t)channels) ||
      !write_u32((uint32_t)frame_size) || !write_u32((uint32_t)frames)) {
    fprintf(stderr, "failed to write sequence header\n");
    opus_encoder_destroy(encoder);
    return 1;
  }

  for (frame = 0; frame < frames; frame++) {
    int sample;
    int packet_len;
    opus_uint32 final_range = 0;
    for (sample = 0; sample < frame_size * channels; sample++) {
      if (!read_float32(&pcm[sample])) {
        fprintf(stderr, "truncated PCM input at frame %d sample %d\n", frame, sample);
        opus_encoder_destroy(encoder);
        return 1;
      }
    }
    packet_len = opus_encode_float(encoder, pcm, frame_size, packet, packet_capacity);
    if (packet_len < 0) {
      fprintf(stderr, "opus_encode_float failed at frame %d: %d\n", frame, packet_len);
      opus_encoder_destroy(encoder);
      return 1;
    }
    if (opus_encoder_ctl(encoder, OPUS_GET_FINAL_RANGE(&final_range)) != OPUS_OK ||
        !write_u32((uint32_t)frame) || !write_u32((uint32_t)packet_len) ||
        !write_u32(final_range) || !write_exact(packet, (size_t)packet_len)) {
      fprintf(stderr, "failed to write encoded frame %d\n", frame);
      opus_encoder_destroy(encoder);
      return 1;
    }
  }
  if (fgetc(stdin) != EOF) {
    fprintf(stderr, "unexpected trailing PCM input\n");
    opus_encoder_destroy(encoder);
    return 1;
  }
  opus_encoder_destroy(encoder);
  return fflush(stdout) == 0 ? 0 : 1;
}
