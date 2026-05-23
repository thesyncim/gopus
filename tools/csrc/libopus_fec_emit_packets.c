#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "opus.h"
#include "src/opus_private.h"

#define GOFC_MAGIC "GOFC"
#define MAX_FEC_PACKETS 32
#define MAX_PACKET_BYTES 4000

struct fec_packet_buf {
  int len;
  unsigned char data[MAX_PACKET_BYTES];
};

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

static int write_exact(const void *src, size_t n) {
  const unsigned char *p = (const unsigned char *)src;
  size_t off = 0;
  while (off < n) {
    size_t wrote = fwrite(p + off, 1, n - off, stdout);
    if (wrote == 0) {
      return 0;
    }
    off += wrote;
  }
  return 1;
}

static int write_u32(uint32_t v) {
  unsigned char b[4];
  b[0] = (unsigned char)(v & 0xff);
  b[1] = (unsigned char)((v >> 8) & 0xff);
  b[2] = (unsigned char)((v >> 16) & 0xff);
  b[3] = (unsigned char)((v >> 24) & 0xff);
  return write_exact(b, 4);
}

static int env_int(const char *name, int fallback) {
  const char *v = getenv(name);
  char *end = NULL;
  long x;
  if (v == NULL || v[0] == '\0') {
    return fallback;
  }
  x = strtol(v, &end, 10);
  if (end == v || *end != '\0') {
    return fallback;
  }
  return (int)x;
}

static int read_pcm_stdin(float *pcm, int frame_size, int channels) {
  size_t n = (size_t)frame_size * (size_t)channels;
  size_t need = n * sizeof(float);
  size_t got = fread(pcm, 1, need, stdin);
  return got == need;
}

static int parse_bandwidth_env(const char *value, int *bandwidth) {
  if (bandwidth == NULL) {
    return 0;
  }
  *bandwidth = OPUS_BANDWIDTH_WIDEBAND;
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

int main(void) {
  const int sample_rate = 48000;
  int frame_size = env_int("GOPUS_FEC_FRAME_SIZE", 960);
  int channels = env_int("GOPUS_FEC_CHANNELS", 1);
  int max_frames = env_int("GOPUS_FEC_MAX_FRAMES", 12);
  int bitrate = env_int("GOPUS_FEC_BITRATE", 24000);
  int bandwidth = OPUS_BANDWIDTH_WIDEBAND;
  int application = OPUS_APPLICATION_AUDIO;
  int signal_type = OPUS_SIGNAL_MUSIC;
  int use_pcm_stdin = env_int("GOPUS_FEC_PCM_STDIN", 0);
  int use_inband_fec = env_int("GOPUS_FEC_INBAND", 1);
  OpusEncoder *enc = NULL;
  int err = OPUS_OK;
  float *pcm = NULL;
  unsigned char packet[MAX_PACKET_BYTES];
  struct fec_packet_buf packets[MAX_FEC_PACKETS];
  int frame_idx;
  int packet_count = 0;

  if (!set_binary_stdio()) {
    return 1;
  }
  if (frame_size <= 0 || channels <= 0 || max_frames <= 0) {
    fprintf(stderr, "invalid frame_size/channels/max_frames\n");
    return 1;
  }
  if (channels != 1 && channels != 2) {
    fprintf(stderr, "unsupported channels=%d\n", channels);
    return 1;
  }
  {
    const char *bw = getenv("GOPUS_FEC_BANDWIDTH");
    if (!parse_bandwidth_env(bw, &bandwidth)) {
      fprintf(stderr, "invalid GOPUS_FEC_BANDWIDTH\n");
      return 1;
    }
  }
  {
    const char *app = getenv("GOPUS_FEC_APPLICATION");
    if (app != NULL && strcmp(app, "voip") == 0) {
      application = OPUS_APPLICATION_VOIP;
    }
  }
  {
    const char *sig = getenv("GOPUS_FEC_SIGNAL");
    if (sig != NULL && strcmp(sig, "voice") == 0) {
      signal_type = OPUS_SIGNAL_VOICE;
    }
  }
  pcm = (float *)calloc((size_t)frame_size * (size_t)channels, sizeof(float));
  if (pcm == NULL) {
    return 1;
  }

  enc = opus_encoder_create(sample_rate, channels, application, &err);
  if (enc == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_encoder_create failed: %d\n", err);
    free(pcm);
    return 1;
  }

  if (opus_encoder_ctl(enc, OPUS_SET_BITRATE(bitrate)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_SIGNAL(signal_type)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_BANDWIDTH(bandwidth)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_COMPLEXITY(10)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_VBR(1)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_VBR_CONSTRAINT(1)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_PACKET_LOSS_PERC(20)) != OPUS_OK) {
    fprintf(stderr, "opus_encoder_ctl setup failed\n");
    opus_encoder_destroy(enc);
    free(pcm);
    return 1;
  }
  if (use_inband_fec &&
      opus_encoder_ctl(enc, OPUS_SET_INBAND_FEC(1)) != OPUS_OK) {
    fprintf(stderr, "opus_encoder_ctl setup failed\n");
    opus_encoder_destroy(enc);
    free(pcm);
    return 1;
  }
  if (channels == 2) {
    if (opus_encoder_ctl(enc, OPUS_SET_FORCE_CHANNELS(2)) != OPUS_OK) {
      fprintf(stderr, "OPUS_SET_FORCE_CHANNELS failed\n");
      opus_encoder_destroy(enc);
      free(pcm);
      return 1;
    }
  }
  if (opus_encoder_ctl(enc, OPUS_SET_FORCE_MODE(MODE_SILK_ONLY)) != OPUS_OK) {
    fprintf(stderr, "OPUS_SET_FORCE_MODE failed\n");
    opus_encoder_destroy(enc);
    free(pcm);
    return 1;
  }

  for (frame_idx = 0; frame_idx < max_frames; frame_idx++) {
    int packet_len;
    if (use_pcm_stdin) {
      if (!read_pcm_stdin(pcm, frame_size, channels)) {
        break;
      }
    } else {
      fprintf(stderr, "GOPUS_FEC_PCM_STDIN required\n");
      opus_encoder_destroy(enc);
      free(pcm);
      return 1;
    }
    packet_len = opus_encode_float(enc, pcm, frame_size, packet, (opus_int32)sizeof(packet));
    if (packet_len < 0) {
      fprintf(stderr, "opus_encode_float failed: %d\n", packet_len);
      opus_encoder_destroy(enc);
      free(pcm);
      return 1;
    }
    if (packet_len == 0) {
      continue;
    }
    if (packet_count >= MAX_FEC_PACKETS || packet_len > MAX_PACKET_BYTES) {
      fprintf(stderr, "packet buffer overflow at frame %d\n", frame_idx);
      opus_encoder_destroy(enc);
      free(pcm);
      return 1;
    }
    packets[packet_count].len = packet_len;
    memcpy(packets[packet_count].data, packet, (size_t)packet_len);
    packet_count++;
  }

  if (!write_exact(GOFC_MAGIC, 4) || !write_u32(1) || !write_u32((uint32_t)frame_size) ||
      !write_u32((uint32_t)channels) || !write_u32((uint32_t)packet_count)) {
    fprintf(stderr, "failed to write header\n");
    opus_encoder_destroy(enc);
    free(pcm);
    return 1;
  }
  for (frame_idx = 0; frame_idx < packet_count; frame_idx++) {
    if (!write_u32((uint32_t)packets[frame_idx].len) ||
        !write_exact(packets[frame_idx].data, (size_t)packets[frame_idx].len)) {
      fprintf(stderr, "failed to write packet %d\n", frame_idx);
      opus_encoder_destroy(enc);
      free(pcm);
      return 1;
    }
  }

  opus_encoder_destroy(enc);
  free(pcm);
  return 0;
}
