/*
 * libopus_dtx_emit_packets.c - DTX sequence oracle for gopus parity testing.
 *
 * Encodes a speech->silence->speech PCM stream with DTX enabled and emits
 * every Opus packet (including 1-byte TOC-only DTX packets) so the Go test
 * can compare TOC byte values, packet lengths, and the DTX cadence against
 * gopus.
 *
 * Reference: src/opus_encoder.c:2564-2572 (DTX decision + gen_toc),
 *            decide_dtx_mode(): src/opus_encoder.c:1114-1140
 *            silk/define.h: NB_SPEECH_FRAMES_BEFORE_DTX, MAX_CONSECUTIVE_DTX
 *
 * Wire format (little-endian):
 *   [4]  magic "GDTX"
 *   [4]  version = 1
 *   [4]  frame_size   (samples per channel per frame)
 *   [4]  channels
 *   [4]  total_frames (number of entries below)
 *   per frame:
 *     [4]  packet_len    (bytes; 1 = DTX TOC-only; 0 = not emitted/error)
 *     [N]  packet_data
 *
 * Environment variables:
 *   GOPUS_DTX_FRAME_SIZE   default 960
 *   GOPUS_DTX_CHANNELS     default 1  (1 or 2)
 *   GOPUS_DTX_BITRATE      default 24000
 *   GOPUS_DTX_BANDWIDTH    "nb","wb","swb","fb"  default "wb"
 *   GOPUS_DTX_MODE         "silk","hybrid"       default "silk"
 *   GOPUS_DTX_APPLICATION  "audio","voip"        default "audio"
 *   GOPUS_DTX_MAX_FRAMES   default 50
 *   GOPUS_DTX_PCM_STDIN    1 = read float32 LE from stdin; default 1
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
#include "src/opus_private.h"

#define GDTX_MAGIC "GDTX"
#define MAX_DTX_PACKETS 256
#define MAX_PACKET_BYTES 4000

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

static int write_exact(const void *src, size_t n) {
  const unsigned char *p = (const unsigned char *)src;
  size_t off = 0;
  while (off < n) {
    size_t wrote = fwrite(p + off, 1, n - off, stdout);
    if (wrote == 0) return 0;
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
  if (v == NULL || v[0] == '\0') return fallback;
  x = strtol(v, &end, 10);
  if (end == v || *end != '\0') return fallback;
  return (int)x;
}

static int read_pcm_stdin(float *pcm, int frame_size, int channels) {
  size_t n = (size_t)frame_size * (size_t)channels;
  size_t need = n * sizeof(float);
  size_t got = fread(pcm, 1, need, stdin);
  return got == need;
}

static int parse_bandwidth(const char *value) {
  if (value == NULL || value[0] == '\0') return OPUS_BANDWIDTH_WIDEBAND;
  if (strcmp(value, "nb") == 0) return OPUS_BANDWIDTH_NARROWBAND;
  if (strcmp(value, "mb") == 0) return OPUS_BANDWIDTH_MEDIUMBAND;
  if (strcmp(value, "wb") == 0) return OPUS_BANDWIDTH_WIDEBAND;
  if (strcmp(value, "swb") == 0) return OPUS_BANDWIDTH_SUPERWIDEBAND;
  if (strcmp(value, "fb") == 0) return OPUS_BANDWIDTH_FULLBAND;
  return OPUS_BANDWIDTH_WIDEBAND;
}

static int parse_mode(const char *value) {
  if (value == NULL || value[0] == '\0') return MODE_SILK_ONLY;
  if (strcmp(value, "silk") == 0) return MODE_SILK_ONLY;
  if (strcmp(value, "hybrid") == 0) return MODE_HYBRID;
  if (strcmp(value, "celt") == 0) return MODE_CELT_ONLY;
  return MODE_SILK_ONLY;
}

struct dtx_packet_buf {
  int len;
  unsigned char data[MAX_PACKET_BYTES];
};

int main(void) {
  const int sample_rate = 48000;
  int frame_size     = env_int("GOPUS_DTX_FRAME_SIZE", 960);
  int channels       = env_int("GOPUS_DTX_CHANNELS", 1);
  int bitrate        = env_int("GOPUS_DTX_BITRATE", 24000);
  int max_frames     = env_int("GOPUS_DTX_MAX_FRAMES", 50);
  int use_pcm_stdin  = env_int("GOPUS_DTX_PCM_STDIN", 1);
  int bandwidth      = parse_bandwidth(getenv("GOPUS_DTX_BANDWIDTH"));
  int force_mode     = parse_mode(getenv("GOPUS_DTX_MODE"));
  int application    = OPUS_APPLICATION_AUDIO;
  const char *app_env = getenv("GOPUS_DTX_APPLICATION");
  OpusEncoder *enc   = NULL;
  int err            = OPUS_OK;
  float *pcm         = NULL;
  unsigned char packet_buf[MAX_PACKET_BYTES];
  struct dtx_packet_buf packets[MAX_DTX_PACKETS];
  int frame_idx;
  int packet_count   = 0;

  if (!set_binary_stdio()) return 1;
  if (frame_size <= 0 || channels <= 0 || max_frames <= 0) {
    fprintf(stderr, "invalid frame_size/channels/max_frames\n");
    return 1;
  }
  if (channels != 1 && channels != 2) {
    fprintf(stderr, "unsupported channels=%d\n", channels);
    return 1;
  }
  if (max_frames > MAX_DTX_PACKETS) {
    fprintf(stderr, "max_frames %d exceeds limit %d\n", max_frames, MAX_DTX_PACKETS);
    return 1;
  }

  if (app_env != NULL && strcmp(app_env, "voip") == 0) {
    application = OPUS_APPLICATION_VOIP;
  }

  pcm = (float *)calloc((size_t)frame_size * (size_t)channels, sizeof(float));
  if (pcm == NULL) { fprintf(stderr, "OOM\n"); return 1; }

  enc = opus_encoder_create(sample_rate, channels, application, &err);
  if (enc == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_encoder_create failed: %d\n", err);
    free(pcm);
    return 1;
  }

  /* Set up encoder: DTX on, fixed bandwidth, force mode, CBR-ish for
   * deterministic packet cadence. VBR is fine too; the DTX decision
   * is independent of the bitrate mode. */
  if (opus_encoder_ctl(enc, OPUS_SET_DTX(1)) != OPUS_OK) {
    fprintf(stderr, "OPUS_SET_DTX failed\n");
    opus_encoder_destroy(enc); free(pcm); return 1;
  }
  if (opus_encoder_ctl(enc, OPUS_SET_BITRATE(bitrate)) != OPUS_OK) {
    fprintf(stderr, "OPUS_SET_BITRATE failed\n");
    opus_encoder_destroy(enc); free(pcm); return 1;
  }
  if (opus_encoder_ctl(enc, OPUS_SET_BANDWIDTH(bandwidth)) != OPUS_OK) {
    fprintf(stderr, "OPUS_SET_BANDWIDTH failed\n");
    opus_encoder_destroy(enc); free(pcm); return 1;
  }
  if (opus_encoder_ctl(enc, OPUS_SET_COMPLEXITY(10)) != OPUS_OK) {
    fprintf(stderr, "OPUS_SET_COMPLEXITY failed\n");
    opus_encoder_destroy(enc); free(pcm); return 1;
  }
  /* Force mode so gopus and libopus stay in sync over the whole sequence. */
  if (opus_encoder_ctl(enc, OPUS_SET_FORCE_MODE(force_mode)) != OPUS_OK) {
    fprintf(stderr, "OPUS_SET_FORCE_MODE(%d) failed\n", force_mode);
    opus_encoder_destroy(enc); free(pcm); return 1;
  }
  if (channels == 2) {
    if (opus_encoder_ctl(enc, OPUS_SET_FORCE_CHANNELS(2)) != OPUS_OK) {
      fprintf(stderr, "OPUS_SET_FORCE_CHANNELS failed\n");
      opus_encoder_destroy(enc); free(pcm); return 1;
    }
  }

  for (frame_idx = 0; frame_idx < max_frames; frame_idx++) {
    int packet_len;
    if (use_pcm_stdin) {
      if (!read_pcm_stdin(pcm, frame_size, channels)) {
        /* EOF: fewer frames than requested is fine */
        break;
      }
    } else {
      fprintf(stderr, "GOPUS_DTX_PCM_STDIN is required\n");
      opus_encoder_destroy(enc); free(pcm); return 1;
    }

    packet_len = opus_encode_float(enc, pcm, frame_size,
                                   packet_buf, (opus_int32)sizeof(packet_buf));
    if (packet_len < 0) {
      fprintf(stderr, "opus_encode_float failed frame %d: %d\n", frame_idx, packet_len);
      opus_encoder_destroy(enc); free(pcm); return 1;
    }
    /* packet_len == 0 means the encoder decided not to emit (shouldn't happen
     * with DTX since it always returns >=1). */
    if (packet_len == 0) {
      fprintf(stderr, "opus_encode_float returned 0 at frame %d\n", frame_idx);
      opus_encoder_destroy(enc); free(pcm); return 1;
    }
    if (packet_count >= MAX_DTX_PACKETS || packet_len > MAX_PACKET_BYTES) {
      fprintf(stderr, "packet buffer overflow at frame %d\n", frame_idx);
      opus_encoder_destroy(enc); free(pcm); return 1;
    }
    packets[packet_count].len = packet_len;
    memcpy(packets[packet_count].data, packet_buf, (size_t)packet_len);
    packet_count++;
  }

  /* Write output */
  if (!write_exact(GDTX_MAGIC, 4) ||
      !write_u32(1) ||
      !write_u32((uint32_t)frame_size) ||
      !write_u32((uint32_t)channels) ||
      !write_u32((uint32_t)packet_count)) {
    fprintf(stderr, "failed to write header\n");
    opus_encoder_destroy(enc); free(pcm); return 1;
  }
  for (frame_idx = 0; frame_idx < packet_count; frame_idx++) {
    if (!write_u32((uint32_t)packets[frame_idx].len) ||
        !write_exact(packets[frame_idx].data, (size_t)packets[frame_idx].len)) {
      fprintf(stderr, "failed to write packet %d\n", frame_idx);
      opus_encoder_destroy(enc); free(pcm); return 1;
    }
  }

  opus_encoder_destroy(enc);
  free(pcm);
  return 0;
}
