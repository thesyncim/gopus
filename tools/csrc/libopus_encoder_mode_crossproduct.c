/* libopus_encoder_mode_crossproduct.c
 * Oracle helper for encoder_auto_mode_crossproduct_parity_test.go.
 *
 * Protocol (little-endian binary):
 *
 * Input:
 *   4 bytes  magic  "GCPI"
 *   4 bytes  version = 1
 *   4 bytes  count   (number of cases)
 *   Per case:
 *     4 bytes  sample_rate   (Hz, e.g. 48000)
 *     4 bytes  channels      (1 or 2)
 *     4 bytes  frame_size    (samples per frame, e.g. 960)
 *     4 bytes  bitrate       (bps)
 *     4 bytes  application   (OPUS_APPLICATION_*)
 *     4 bytes  signal        (OPUS_SIGNAL_VOICE / OPUS_SIGNAL_MUSIC / OPUS_AUTO)
 *     4 bytes  num_frames    (number of frames to encode)
 *     4 bytes  max_data_bytes (per-frame byte budget)
 *     num_frames * frame_size * channels * 4 bytes   float32 PCM (interleaved)
 *
 * Output:
 *   4 bytes  magic  "GCPO"
 *   4 bytes  version = 1
 *   4 bytes  count
 *   Per case:
 *     4 bytes  num_frames
 *     Per frame:
 *       4 bytes  ret      (bytes encoded, or negative error code)
 *       4 bytes  toc_byte (first byte of encoded packet, 0 if error)
 *
 * Encodes each case with a fresh encoder, stateful (not reset between frames).
 * The chosen application controls the encoder; signal sets OPUS_SET_SIGNAL.
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

#define INPUT_MAGIC  "GCPI"
#define OUTPUT_MAGIC "GCPO"

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

static int read_exact(void *dst, size_t n) {
  return fread(dst, 1, n, stdin) == n;
}

static int write_exact(const void *src, size_t n) {
  return fwrite(src, 1, n, stdout) == n;
}

static int read_u32(uint32_t *out) { return read_exact(out, 4); }
static int write_u32(uint32_t v)   { return write_exact(&v, 4); }
static int write_i32(int32_t v)    { return write_exact(&v, 4); }

static int read_float(float *out) {
  uint32_t bits;
  if (!read_u32(&bits)) return 0;
  memcpy(out, &bits, sizeof(*out));
  return 1;
}

int main(void) {
  char magic[4];
  uint32_t version, count, i;

  if (!set_binary_stdio()) {
    fprintf(stderr, "failed to set binary stdio\n");
    return 1;
  }

  if (!read_exact(magic, 4) || memcmp(magic, INPUT_MAGIC, 4) != 0) {
    fprintf(stderr, "invalid input magic\n");
    return 1;
  }
  if (!read_u32(&version) || version != 1 || !read_u32(&count)) {
    fprintf(stderr, "failed to read input header\n");
    return 1;
  }

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(count)) {
    fprintf(stderr, "failed to write output header\n");
    return 1;
  }

  for (i = 0; i < count; i++) {
    uint32_t sample_rate, channels, frame_size, bitrate;
    uint32_t application, signal, num_frames, max_data_bytes_u;
    uint32_t f, s;
    uint32_t total_samples;
    int err;
    int max_db;
    OpusEncoder *enc;
    float *pcm_buf = NULL;
    unsigned char *out_buf = NULL;

    if (!read_u32(&sample_rate) || !read_u32(&channels) ||
        !read_u32(&frame_size) || !read_u32(&bitrate) ||
        !read_u32(&application) || !read_u32(&signal) ||
        !read_u32(&num_frames) || !read_u32(&max_data_bytes_u)) {
      fprintf(stderr, "case %u: truncated header\n", i);
      return 1;
    }

    max_db = (int)max_data_bytes_u;
    total_samples = frame_size * channels * num_frames;

    pcm_buf = (float *)malloc(total_samples * sizeof(float));
    if (!pcm_buf) { fprintf(stderr, "OOM\n"); return 1; }
    out_buf = (unsigned char *)malloc(max_db > 0 ? max_db : 1);
    if (!out_buf) { free(pcm_buf); fprintf(stderr, "OOM\n"); return 1; }

    for (s = 0; s < total_samples; s++) {
      if (!read_float(&pcm_buf[s])) {
        fprintf(stderr, "case %u: truncated PCM\n", i);
        free(pcm_buf); free(out_buf);
        return 1;
      }
    }

    enc = opus_encoder_create((opus_int32)sample_rate, (int)channels,
                              (int)application, &err);
    if (!enc || err != OPUS_OK) {
      fprintf(stderr, "case %u: opus_encoder_create failed: %d\n", i, err);
      free(pcm_buf); free(out_buf);
      return 1;
    }

    /* Configure encoder to match the Go side defaults. */
    opus_encoder_ctl(enc, OPUS_SET_BITRATE((opus_int32)bitrate));
    opus_encoder_ctl(enc, OPUS_SET_VBR(1));
    opus_encoder_ctl(enc, OPUS_SET_VBR_CONSTRAINT(0));
    opus_encoder_ctl(enc, OPUS_SET_BANDWIDTH(OPUS_BANDWIDTH_FULLBAND));
    opus_encoder_ctl(enc, OPUS_SET_SIGNAL((int)signal));
    opus_encoder_ctl(enc, OPUS_SET_COMPLEXITY(10));
    opus_encoder_ctl(enc, OPUS_SET_PACKET_LOSS_PERC(0));
    opus_encoder_ctl(enc, OPUS_SET_INBAND_FEC(0));
    opus_encoder_ctl(enc, OPUS_SET_DTX(0));

    /* Write num_frames for this case. */
    if (!write_u32(num_frames)) {
      opus_encoder_destroy(enc);
      free(pcm_buf); free(out_buf);
      fprintf(stderr, "case %u: write error\n", i);
      return 1;
    }

    for (f = 0; f < num_frames; f++) {
      int ret;
      uint32_t toc_byte = 0;
      const float *frame_pcm = pcm_buf + (size_t)f * frame_size * channels;

      ret = opus_encode_float(enc, frame_pcm, (int)frame_size, out_buf, max_db);
      if (ret > 0) {
        toc_byte = (uint32_t)out_buf[0];
      }
      if (!write_i32((int32_t)ret) || !write_u32(toc_byte)) {
        opus_encoder_destroy(enc);
        free(pcm_buf); free(out_buf);
        fprintf(stderr, "case %u frame %u: write error\n", i, f);
        return 1;
      }
    }

    opus_encoder_destroy(enc);
    free(pcm_buf);
    free(out_buf);
  }

  return 0;
}
