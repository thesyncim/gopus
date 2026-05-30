/* Native 96 kHz QEXT full-packet encode oracle.
 *
 * Encodes a sequence of native 96 kHz float PCM frames through a single
 * QEXT-enabled OpusEncoder created at Fs=96000. With ENABLE_QEXT the libopus
 * encoder runs the native 96 kHz CELT mode (mode96000_1920_240: 1920-sample
 * frames, 3840-MDCT, 8 short blocks) plus the >20 kHz extension-band encode
 * chain, producing a real native 96 kHz QEXT Opus packet (not a 2:1 resample
 * of a 48 kHz encode). gopus mirrors this with celt.HD96kMode + the qext
 * extension encode chain.
 *
 * The encoder is configured CELT-only / fullband / CBR with QEXT enabled, to
 * match the gopus native HD96k encode routing under test.
 *
 * Protocol (little-endian):
 *   in : "GQEI" magic, u32 version(=1),
 *        u32 channels (1|2), u32 frameSize (per-channel samples at 96 kHz),
 *        i32 bitrate, i32 complexity, u32 vbr (0=CBR,1=VBR),
 *        u32 maxPacketBytes, u32 frameCount,
 *        then frameCount*frameSize*channels float32 PCM samples
 *   out: "GQEO" magic, u32 version(=1), u32 frameCount,
 *        then for each frame: u32 packetLen, packetLen bytes (4-byte padded),
 *        then u32 frameCount, frameCount*u32 finalRange
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

#define GQEI_MAGIC "GQEI"
#define GQEO_MAGIC "GQEO"

static int read_exact(void *dst, size_t n) { return fread(dst, 1, n, stdin) == n; }
static int write_exact(const void *src, size_t n) { return fwrite(src, 1, n, stdout) == n; }

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact(b, 4)) return 0;
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

static int read_f32(float *out) {
  uint32_t bits;
  if (!read_u32(&bits)) return 0;
  memcpy(out, &bits, sizeof(bits));
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
  uint32_t channels = 0;
  uint32_t frame_size = 0;
  int32_t bitrate = 0;
  int32_t complexity = 0;
  uint32_t vbr = 0;
  uint32_t max_packet = 0;
  uint32_t frame_count = 0;
  uint32_t f;
  size_t n_floats;
  OpusEncoder *enc = NULL;
  int err = OPUS_OK;
  float *pcm = NULL;
  unsigned char *packet = NULL;
  uint32_t *ranges = NULL;

  if (!set_binary_stdio()) { fprintf(stderr, "stdio mode\n"); return 1; }
  if (!read_exact(magic, 4) || memcmp(magic, GQEI_MAGIC, 4) != 0) { fprintf(stderr, "bad magic\n"); return 1; }
  if (!read_u32(&version) || version != 1) { fprintf(stderr, "bad version\n"); return 1; }
  if (!read_u32(&channels) || !read_u32(&frame_size) ||
      !read_u32((uint32_t *)&bitrate) || !read_u32((uint32_t *)&complexity) ||
      !read_u32(&vbr) || !read_u32(&max_packet) || !read_u32(&frame_count)) {
    fprintf(stderr, "bad header\n");
    return 1;
  }
  if (channels < 1 || channels > 2 || frame_size == 0 || max_packet == 0) {
    fprintf(stderr, "bad dims\n");
    return 1;
  }

  enc = opus_encoder_create(96000, (int)channels, OPUS_APPLICATION_RESTRICTED_LOWDELAY, &err);
  if (enc == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_encoder_create(96000) failed: %d\n", err);
    return 1;
  }
  opus_encoder_ctl(enc, OPUS_SET_QEXT(1));
  opus_encoder_ctl(enc, OPUS_SET_FORCE_MODE(MODE_CELT_ONLY));
  opus_encoder_ctl(enc, OPUS_SET_BANDWIDTH(OPUS_BANDWIDTH_FULLBAND));
  opus_encoder_ctl(enc, OPUS_SET_BITRATE((opus_int32)bitrate));
  opus_encoder_ctl(enc, OPUS_SET_VBR((int)vbr));
  opus_encoder_ctl(enc, OPUS_SET_VBR_CONSTRAINT(0));
  opus_encoder_ctl(enc, OPUS_SET_COMPLEXITY((int)complexity));
  opus_encoder_ctl(enc, OPUS_SET_LSB_DEPTH(24));

  n_floats = (size_t)frame_size * (size_t)channels;
  pcm = (float *)malloc(sizeof(float) * (n_floats ? n_floats : 1));
  packet = (unsigned char *)malloc(max_packet);
  if (frame_count > 0) ranges = (uint32_t *)calloc(frame_count, sizeof(*ranges));
  if (pcm == NULL || packet == NULL || (frame_count > 0 && ranges == NULL)) {
    fprintf(stderr, "alloc\n");
    opus_encoder_destroy(enc); free(pcm); free(packet); free(ranges);
    return 1;
  }

  if (!write_exact(GQEO_MAGIC, 4) || !write_u32(1) || !write_u32(frame_count)) {
    opus_encoder_destroy(enc); free(pcm); free(packet); free(ranges);
    return 1;
  }

  for (f = 0; f < frame_count; f++) {
    size_t i;
    int ret;
    opus_uint32 fr = 0;
    uint32_t pad;
    for (i = 0; i < n_floats; i++) {
      float v;
      if (!read_f32(&v)) {
        fprintf(stderr, "pcm read\n");
        opus_encoder_destroy(enc); free(pcm); free(packet); free(ranges);
        return 1;
      }
      pcm[i] = v;
    }
    ret = opus_encode_float(enc, pcm, (int)frame_size, packet, (opus_int32)max_packet);
    if (ret < 0) {
      fprintf(stderr, "opus_encode_float failed: %d\n", ret);
      opus_encoder_destroy(enc); free(pcm); free(packet); free(ranges);
      return 1;
    }
    opus_encoder_ctl(enc, OPUS_GET_FINAL_RANGE(&fr));
    ranges[f] = fr;
    if (!write_u32((uint32_t)ret) || !write_exact(packet, (size_t)ret)) {
      opus_encoder_destroy(enc); free(pcm); free(packet); free(ranges);
      return 1;
    }
    pad = (uint32_t)((4 - (ret % 4)) % 4);
    if (pad > 0) {
      unsigned char zero[4] = {0, 0, 0, 0};
      if (!write_exact(zero, pad)) {
        opus_encoder_destroy(enc); free(pcm); free(packet); free(ranges);
        return 1;
      }
    }
  }

  if (!write_u32(frame_count) || (frame_count > 0 && !write_exact(ranges, frame_count * sizeof(*ranges)))) {
    opus_encoder_destroy(enc); free(pcm); free(packet); free(ranges);
    return 1;
  }

  opus_encoder_destroy(enc);
  free(pcm); free(packet); free(ranges);
  return 0;
}
