#include <math.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "opus.h"
#include "dred_decoder.h"
#include "src/opus_private.h"

#define GODO_MAGIC "GODP"

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) {
    return 0;
  }
#endif
  return 1;
}

static int write_exact(const void *src, size_t n) {
  return fwrite(src, 1, n, stdout) == n;
}

static int write_u32(uint32_t v) {
  unsigned char b[4];
  b[0] = (unsigned char)(v & 0xFF);
  b[1] = (unsigned char)((v >> 8) & 0xFF);
  b[2] = (unsigned char)((v >> 16) & 0xFF);
  b[3] = (unsigned char)((v >> 24) & 0xFF);
  return write_exact(b, sizeof(b));
}

static float voiced_sample(int frame_idx, int sample_idx, int frame_size, int sample_rate) {
  int n = frame_idx * frame_size + sample_idx;
  float t = (float)n / (float)sample_rate;
  float env = 0.82f + 0.18f * sinf(2.f * 3.14159265358979323846f * 1.3f * t);
  float s = 0.0f;
  s += 0.28f * sinf(2.f * 3.14159265358979323846f * 110.f * t);
  s += 0.17f * sinf(2.f * 3.14159265358979323846f * 220.f * t + 0.11f);
  s += 0.09f * sinf(2.f * 3.14159265358979323846f * 330.f * t + 0.23f);
  s += 0.05f * sinf(2.f * 3.14159265358979323846f * 440.f * t + 0.37f);
  return env * s;
}

int main(void) {
  const int sample_rate = 48000;
  const int channels = 1;
  const int frame_size = 960;
  const int max_packet = 1500;
  const int max_dred_samples = 960;
  float pcm[960];
  unsigned char packet[1500];
  OpusEncoder *enc = NULL;
  OpusDREDDecoder *dred_dec = NULL;
  OpusDRED *dred = NULL;
  int err = OPUS_OK;
  int frame_idx;

  if (!set_binary_stdio()) {
    fprintf(stderr, "failed to set binary stdout mode\n");
    return 1;
  }

  enc = opus_encoder_create(sample_rate, channels, OPUS_APPLICATION_AUDIO, &err);
  if (enc == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_encoder_create failed: %d\n", err);
    return 1;
  }
  dred_dec = opus_dred_decoder_create(&err);
  if (dred_dec == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_dred_decoder_create failed: %d\n", err);
    opus_encoder_destroy(enc);
    return 1;
  }
  dred = opus_dred_alloc(&err);
  if (dred == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_dred_alloc failed: %d\n", err);
    opus_dred_decoder_destroy(dred_dec);
    opus_encoder_destroy(enc);
    return 1;
  }

  opus_encoder_ctl(enc, OPUS_SET_BITRATE(40000));
  opus_encoder_ctl(enc, OPUS_SET_SIGNAL(OPUS_SIGNAL_MUSIC));
  opus_encoder_ctl(enc, OPUS_SET_BANDWIDTH(OPUS_BANDWIDTH_FULLBAND));
  opus_encoder_ctl(enc, OPUS_SET_FORCE_MODE(MODE_CELT_ONLY));
  opus_encoder_ctl(enc, OPUS_SET_PACKET_LOSS_PERC(20));
  opus_encoder_ctl(enc, OPUS_SET_DRED_DURATION(80));

  for (frame_idx = 0; frame_idx < 160; frame_idx++) {
    int dred_end = 0;
    int ret;
    int packet_len;
    int i;
    memset(dred, 0, sizeof(*dred));
    for (i = 0; i < frame_size; i++) {
      pcm[i] = voiced_sample(frame_idx, i, frame_size, sample_rate);
    }
    packet_len = opus_encode_float(enc, pcm, frame_size, packet, max_packet);
    if (packet_len < 0) {
      fprintf(stderr, "opus_encode_float failed: %d\n", packet_len);
      opus_dred_free(dred);
      opus_dred_decoder_destroy(dred_dec);
      opus_encoder_destroy(enc);
      return 1;
    }
    if (packet_len == 0) {
      continue;
    }
    ret = opus_dred_parse(dred_dec, dred, packet, packet_len, max_dred_samples, sample_rate, &dred_end, 1);
    if (ret >= 0 && dred->process_stage == 1 && dred->nb_latents > 0) {
      if (!write_exact(GODO_MAGIC, 4) || !write_u32(1) || !write_u32((uint32_t)sample_rate) ||
          !write_u32((uint32_t)max_dred_samples) || !write_u32((uint32_t)packet_len) ||
          !write_exact(packet, (size_t)packet_len)) {
        fprintf(stderr, "failed to write packet output\n");
        opus_dred_free(dred);
        opus_dred_decoder_destroy(dred_dec);
        opus_encoder_destroy(enc);
        return 1;
      }
      opus_dred_free(dred);
      opus_dred_decoder_destroy(dred_dec);
      opus_encoder_destroy(enc);
      return 0;
    }
  }

  fprintf(stderr, "failed to emit a DRED-bearing packet\n");
  opus_dred_free(dred);
  opus_dred_decoder_destroy(dred_dec);
  opus_encoder_destroy(enc);
  return 1;
}
