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

static int dred_helper_bitrate_for_frame_size(int frame_size) {
  int bitrate = 40000;
  if (frame_size > 0 && frame_size < 960) {
    bitrate = (40000 * 960) / frame_size;
  }
  if (bitrate > 320000) {
    bitrate = 320000;
  }
  return bitrate;
}

static int packet_mode_from_toc(const unsigned char *packet) {
  int config;
  if (packet == NULL) {
    return -1;
  }
  config = packet[0] >> 3;
  if (config < 12) {
    return MODE_SILK_ONLY;
  }
  if (config < 16) {
    return MODE_HYBRID;
  }
  return MODE_CELT_ONLY;
}

static int parse_force_mode_env(const char *value, int *force_mode, int *force_mode_enabled) {
  if (force_mode == NULL || force_mode_enabled == NULL) {
    return 0;
  }
  *force_mode = MODE_CELT_ONLY;
  *force_mode_enabled = 1;
  if (value == NULL || value[0] == '\0') {
    return 1;
  }
  if (strcmp(value, "auto") == 0) {
    *force_mode = 0;
    *force_mode_enabled = 0;
    return 1;
  }
  if (strcmp(value, "celt") == 0) {
    *force_mode = MODE_CELT_ONLY;
    *force_mode_enabled = 1;
    return 1;
  }
  if (strcmp(value, "hybrid") == 0) {
    *force_mode = MODE_HYBRID;
    *force_mode_enabled = 1;
    return 1;
  }
  if (strcmp(value, "silk") == 0) {
    *force_mode = MODE_SILK_ONLY;
    *force_mode_enabled = 1;
    return 1;
  }
  return 0;
}

static int parse_bandwidth_env(const char *value, int *bandwidth) {
  if (bandwidth == NULL) {
    return 0;
  }
  *bandwidth = OPUS_BANDWIDTH_FULLBAND;
  if (value == NULL || value[0] == '\0') {
    return 1;
  }
  if (strcmp(value, "wb") == 0 || strcmp(value, "wideband") == 0) {
    *bandwidth = OPUS_BANDWIDTH_WIDEBAND;
    return 1;
  }
  if (strcmp(value, "swb") == 0 || strcmp(value, "superwideband") == 0) {
    *bandwidth = OPUS_BANDWIDTH_SUPERWIDEBAND;
    return 1;
  }
  if (strcmp(value, "fb") == 0 || strcmp(value, "fullband") == 0) {
    *bandwidth = OPUS_BANDWIDTH_FULLBAND;
    return 1;
  }
  return 0;
}

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
  const int max_frames_to_try = 640;
  int frame_size = 960;
  int force_mode = MODE_CELT_ONLY;
  int force_mode_enabled = 1;
  int bandwidth = OPUS_BANDWIDTH_FULLBAND;
  int bitrate = 40000;
  const int max_packet = 1500;
  const int max_dred_samples = 960;
  float pcm[2880];
  unsigned char packet[1500];
  OpusEncoder *enc = NULL;
  OpusDREDDecoder *dred_dec = NULL;
  OpusDRED *dred = NULL;
  int err = OPUS_OK;
  int frame_idx;
  const char *frame_size_env = getenv("GOPUS_DRED_FRAME_SIZE");
  const char *force_mode_env = getenv("GOPUS_DRED_FORCE_MODE");
  const char *bandwidth_env = getenv("GOPUS_DRED_BANDWIDTH");

  if (frame_size_env != NULL && frame_size_env[0] != '\0') {
    char *end = NULL;
    long parsed = strtol(frame_size_env, &end, 10);
    if (end == NULL || *end != '\0' || (parsed != 120 && parsed != 240 && parsed != 480 && parsed != 960 && parsed != 1920 && parsed != 2880)) {
      fprintf(stderr, "invalid GOPUS_DRED_FRAME_SIZE=%s\n", frame_size_env);
      return 1;
    }
    frame_size = (int)parsed;
  }

  if (!parse_force_mode_env(force_mode_env, &force_mode, &force_mode_enabled)) {
    fprintf(stderr, "invalid GOPUS_DRED_FORCE_MODE=%s\n", force_mode_env);
    return 1;
  }

  if (!parse_bandwidth_env(bandwidth_env, &bandwidth)) {
    fprintf(stderr, "invalid GOPUS_DRED_BANDWIDTH=%s\n", bandwidth_env);
    return 1;
  }

  bitrate = dred_helper_bitrate_for_frame_size(frame_size);

  if (force_mode_enabled && force_mode == MODE_HYBRID && bandwidth <= OPUS_BANDWIDTH_WIDEBAND) {
    fprintf(stderr, "hybrid DRED packet helper requires swb/fb bandwidth, got %d\n", bandwidth);
    return 1;
  }

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

  opus_encoder_ctl(enc, OPUS_SET_BITRATE(bitrate));
  opus_encoder_ctl(enc, OPUS_SET_SIGNAL(OPUS_SIGNAL_MUSIC));
  opus_encoder_ctl(enc, OPUS_SET_BANDWIDTH(bandwidth));
  if (force_mode_enabled) {
    opus_encoder_ctl(enc, OPUS_SET_FORCE_MODE(force_mode));
  }
  opus_encoder_ctl(enc, OPUS_SET_PACKET_LOSS_PERC(20));
  opus_encoder_ctl(enc, OPUS_SET_DRED_DURATION(80));

  for (frame_idx = 0; frame_idx < max_frames_to_try; frame_idx++) {
    int dred_end = 0;
    int packet_mode;
    int packet_bandwidth;
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
    packet_mode = packet_mode_from_toc(packet);
    packet_bandwidth = opus_packet_get_bandwidth(packet);
    if ((force_mode_enabled && packet_mode != force_mode) || packet_bandwidth != bandwidth) {
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
