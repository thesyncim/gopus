/* libopus_vbr_cvbr_encode_info.c
 *
 * Oracle helper: encode a PCM stream with libopus 1.5.1 in either VBR or CVBR
 * mode and emit per-packet (length, final_range, bytes) tuples.
 *
 * Wire format (little-endian throughout):
 *
 * Input  magic "GVCI" + u32(version=1) + u32(mode) + u32(application)
 *              + u32(sample_rate) + u32(channels) + u32(frame_size)
 *              + u32(bitrate) + u32(bandwidth) + u32(signal) + u32(n_frames)
 *              then n_frames * frame_size * channels float32 samples.
 *
 *   mode: 0 = VBR (OPUS_SET_VBR(1), OPUS_SET_VBR_CONSTRAINT(0))
 *         1 = CVBR (OPUS_SET_VBR(1), OPUS_SET_VBR_CONSTRAINT(1))
 *
 *   application: 2048=OPUS_APPLICATION_VOIP, 2049=OPUS_APPLICATION_AUDIO,
 *                2050=OPUS_APPLICATION_RESTRICTED_LOWDELAY
 *
 *   bandwidth: 1101=NB, 1102=MB, 1103=WB, 1104=SWB, 1105=FB, -1000=auto
 *
 *   signal: -1000=OPUS_AUTO, 3001=OPUS_SIGNAL_VOICE, 3002=OPUS_SIGNAL_MUSIC
 *
 * Output magic "GVCO" + u32(version=1) + u32(n_frames)
 *              then n_frames records: u32(packet_len) + u32(final_range)
 *              followed by packet_len bytes of packet data.
 *
 * Reference: libopus src/opus_encoder.c opus_encode_float() with
 *   OPUS_SET_VBR / OPUS_SET_VBR_CONSTRAINT controls.
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

#define INPUT_MAGIC  "GVCI"
#define OUTPUT_MAGIC "GVCO"
#define MAX_PACKET_BYTES 4000
#define MAX_FRAME_SAMPLES (5760 * 2)  /* 120ms stereo */

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin),  _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

static int read_exact(void *dst, size_t n) {
  return fread(dst, 1, n, stdin) == n;
}

static int write_exact(const void *src, size_t n) {
  const unsigned char *p = (const unsigned char *)src;
  size_t off = 0;
  while (off < n) {
    size_t w = fwrite(p + off, 1, n - off, stdout);
    if (w == 0) return 0;
    off += w;
  }
  return 1;
}

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact(b, 4)) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) |
         ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int write_u32(uint32_t v) {
  unsigned char b[4];
  b[0] = (unsigned char)(v & 0xff);
  b[1] = (unsigned char)((v >> 8)  & 0xff);
  b[2] = (unsigned char)((v >> 16) & 0xff);
  b[3] = (unsigned char)((v >> 24) & 0xff);
  return write_exact(b, 4);
}

int main(void) {
  char magic[4];
  uint32_t version;
  uint32_t mode;       /* 0=VBR, 1=CVBR */
  uint32_t application;
  uint32_t sample_rate;
  uint32_t channels;
  uint32_t frame_size;
  uint32_t bitrate;
  uint32_t bandwidth;
  uint32_t signal;
  uint32_t n_frames;
  uint32_t i;
  int err;
  OpusEncoder *enc = NULL;
  float     *pcm    = NULL;
  unsigned char *packet = NULL;
  uint32_t final_range = 0;

  if (!set_binary_stdio()) {
    fprintf(stderr, "set binary stdio failed\n");
    return 1;
  }

  /* --- read header --- */
  if (!read_exact(magic, 4) || memcmp(magic, INPUT_MAGIC, 4) != 0) {
    fprintf(stderr, "bad input magic\n");
    return 1;
  }
  if (!read_u32(&version) || version != 1) {
    fprintf(stderr, "unsupported version\n");
    return 1;
  }
  if (!read_u32(&mode)        || mode > 1     ||
      !read_u32(&application) ||
      !read_u32(&sample_rate) || sample_rate == 0 ||
      !read_u32(&channels)    || channels < 1 || channels > 2 ||
      !read_u32(&frame_size)  || frame_size == 0 ||
      !read_u32(&bitrate)     ||
      !read_u32(&bandwidth)   ||
      !read_u32(&signal)      ||
      !read_u32(&n_frames)) {
    fprintf(stderr, "truncated header\n");
    return 1;
  }

  /* --- allocate buffers --- */
  pcm = (float *)malloc(sizeof(float) * frame_size * channels);
  packet = (unsigned char *)malloc(MAX_PACKET_BYTES);
  if (pcm == NULL || packet == NULL) {
    fprintf(stderr, "malloc failed\n");
    free(pcm);
    free(packet);
    return 1;
  }

  /* --- create encoder --- */
  enc = opus_encoder_create((opus_int32)sample_rate, (int)channels,
                            (int)application, &err);
  if (enc == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_encoder_create failed: %d\n", err);
    free(pcm);
    free(packet);
    return 1;
  }

  /* Set VBR / CVBR.
   * libopus src/opus_encoder.c opus_encode_native():
   *   use_vbr         = OPUS_GET_VBR
   *   constrained_vbr = OPUS_GET_VBR_CONSTRAINT
   */
  if (opus_encoder_ctl(enc, OPUS_SET_VBR(1)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_VBR_CONSTRAINT((int)mode)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_BITRATE((opus_int32)bitrate)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_BANDWIDTH((int)bandwidth)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_SIGNAL((int)signal)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_COMPLEXITY(10)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_INBAND_FEC(0)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_DTX(0)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_PACKET_LOSS_PERC(0)) != OPUS_OK) {
    fprintf(stderr, "opus_encoder_ctl setup failed\n");
    opus_encoder_destroy(enc);
    free(pcm);
    free(packet);
    return 1;
  }
  if (channels == 2) {
    if (opus_encoder_ctl(enc, OPUS_SET_FORCE_CHANNELS(2)) != OPUS_OK) {
      fprintf(stderr, "OPUS_SET_FORCE_CHANNELS failed\n");
      opus_encoder_destroy(enc);
      free(pcm);
      free(packet);
      return 1;
    }
  }

  /* --- write output header --- */
  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(n_frames)) {
    fprintf(stderr, "write output header failed\n");
    opus_encoder_destroy(enc);
    free(pcm);
    free(packet);
    return 1;
  }

  /* --- encode frames --- */
  for (i = 0; i < n_frames; i++) {
    uint32_t n_samples = frame_size * channels;
    uint32_t j;
    int ret;

    for (j = 0; j < n_samples; j++) {
      uint32_t bits;
      if (!read_u32(&bits)) {
        fprintf(stderr, "truncated PCM at frame %u sample %u\n", i, j);
        opus_encoder_destroy(enc);
        free(pcm);
        free(packet);
        return 1;
      }
      memcpy(&pcm[j], &bits, 4);
    }

    ret = opus_encode_float(enc, pcm, (int)frame_size, packet, MAX_PACKET_BYTES);
    if (ret < 0) {
      fprintf(stderr, "opus_encode_float frame %u: %d (%s)\n",
              i, ret, opus_strerror(ret));
      opus_encoder_destroy(enc);
      free(pcm);
      free(packet);
      return 1;
    }

    opus_encoder_ctl(enc, OPUS_GET_FINAL_RANGE(&final_range));

    if (!write_u32((uint32_t)ret) ||
        !write_u32(final_range)   ||
        !write_exact(packet, (size_t)ret)) {
      fprintf(stderr, "write frame %u output failed\n", i);
      opus_encoder_destroy(enc);
      free(pcm);
      free(packet);
      return 1;
    }
  }

  opus_encoder_destroy(enc);
  free(pcm);
  free(packet);
  return 0;
}
