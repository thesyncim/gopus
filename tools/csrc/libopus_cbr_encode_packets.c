/* libopus_cbr_encode_packets.c — CBR encode oracle for gopus parity testing.
 *
 * Reads float32 LE PCM frames from stdin and encodes them with libopus
 * configured for CBR (OPUS_SET_VBR(0)), emitting each packet in the standard
 * gopus oracle wire format.
 *
 * Wire format (all little-endian):
 *   IN:  "GCBR" u32(version=1) u32(application) u32(bandwidth) u32(channels)
 *              u32(bitrate) u32(frame_size) u32(complexity) u32(num_frames)
 *              [num_frames × frame_size × channels × float32]
 *   OUT: "GCBO" u32(version=1) u32(num_packets)
 *              [num_packets × u32(packet_len) u8(packet_len × bytes)]
 *
 * application values:
 *   0 = OPUS_APPLICATION_AUDIO
 *   1 = OPUS_APPLICATION_VOIP
 *   2 = OPUS_APPLICATION_RESTRICTED_SILK
 *   3 = OPUS_APPLICATION_RESTRICTED_CELT
 *
 * bandwidth values (match opus_defines.h):
 *   1101 = OPUS_BANDWIDTH_NARROWBAND
 *   1102 = OPUS_BANDWIDTH_MEDIUMBAND
 *   1103 = OPUS_BANDWIDTH_WIDEBAND
 *   1104 = OPUS_BANDWIDTH_SUPERWIDEBAND
 *   1105 = OPUS_BANDWIDTH_FULLBAND
 *
 * Reference: libopus src/opus_encoder.c opus_encode_float()
 *            src/opus_demo.c -cbr flag: opus_encoder_ctl(enc, OPUS_SET_VBR(0))
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

#define INPUT_MAGIC  "GCBR"
#define OUTPUT_MAGIC "GCBO"
#define MAX_PACKET_BYTES 4000

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin),  _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

static int read_exact(void *dst, size_t n) {
  size_t got = fread(dst, 1, n, stdin);
  return got == n;
}

static int write_exact(const void *src, size_t n) {
  size_t off = 0;
  const unsigned char *p = (const unsigned char *)src;
  while (off < n) {
    size_t w = fwrite(p + off, 1, n - off, stdout);
    if (w == 0) return 0;
    off += w;
  }
  return 1;
}

static int read_u32(uint32_t *v) {
  unsigned char b[4];
  if (!read_exact(b, 4)) return 0;
  *v = (uint32_t)b[0] | ((uint32_t)b[1] << 8) |
       ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
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

int main(void) {
  if (!set_binary_stdio()) {
    fprintf(stderr, "set_binary_stdio failed\n");
    return 1;
  }

  /* Read and validate input header magic */
  char magic[4];
  if (!read_exact(magic, 4) || memcmp(magic, INPUT_MAGIC, 4) != 0) {
    fprintf(stderr, "bad input magic\n");
    return 1;
  }
  uint32_t version;
  if (!read_u32(&version) || version != 1) {
    fprintf(stderr, "bad input version %u\n", version);
    return 1;
  }

  uint32_t app_code, bandwidth, channels, bitrate, frame_size, complexity, num_frames;
  if (!read_u32(&app_code)   || !read_u32(&bandwidth)  || !read_u32(&channels) ||
      !read_u32(&bitrate)    || !read_u32(&frame_size)  || !read_u32(&complexity) ||
      !read_u32(&num_frames)) {
    fprintf(stderr, "truncated header\n");
    return 1;
  }

  /* Map application code to libopus constant */
  int application;
  switch (app_code) {
    case 0: application = OPUS_APPLICATION_AUDIO;             break;
    case 1: application = OPUS_APPLICATION_VOIP;              break;
    case 2: application = OPUS_APPLICATION_RESTRICTED_SILK;   break;
    case 3: application = OPUS_APPLICATION_RESTRICTED_CELT;   break;
    default:
      fprintf(stderr, "unknown application code %u\n", app_code);
      return 1;
  }

  /* Validate bandwidth is a valid Opus bandwidth constant */
  if (bandwidth != OPUS_BANDWIDTH_NARROWBAND    &&
      bandwidth != OPUS_BANDWIDTH_MEDIUMBAND    &&
      bandwidth != OPUS_BANDWIDTH_WIDEBAND      &&
      bandwidth != OPUS_BANDWIDTH_SUPERWIDEBAND &&
      bandwidth != OPUS_BANDWIDTH_FULLBAND) {
    fprintf(stderr, "unknown bandwidth %u\n", bandwidth);
    return 1;
  }

  if (channels < 1 || channels > 2) {
    fprintf(stderr, "unsupported channels %u\n", channels);
    return 1;
  }
  if (frame_size == 0 || num_frames == 0) {
    fprintf(stderr, "invalid frame_size=%u num_frames=%u\n", frame_size, num_frames);
    return 1;
  }
  if (complexity > 10) {
    fprintf(stderr, "invalid complexity %u\n", complexity);
    return 1;
  }

  /* Create encoder — always at 48 kHz, matching gopus internal rate */
  int err = OPUS_OK;
  OpusEncoder *enc = opus_encoder_create(48000, (int)channels, application, &err);
  if (enc == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_encoder_create failed: %d\n", err);
    return 1;
  }

  /* libopus CBR setup: VBR=0, CVBR=0 — mirrors opus_demo -cbr
   * Reference: src/opus_demo.c lines that handle "-cbr":
   *   opus_encoder_ctl(enc, OPUS_SET_VBR(0)) */
  if (opus_encoder_ctl(enc, OPUS_SET_VBR(0)) != OPUS_OK) {
    fprintf(stderr, "OPUS_SET_VBR(0) failed\n");
    opus_encoder_destroy(enc);
    return 1;
  }
  if (opus_encoder_ctl(enc, OPUS_SET_BITRATE((opus_int32)bitrate)) != OPUS_OK) {
    fprintf(stderr, "OPUS_SET_BITRATE failed\n");
    opus_encoder_destroy(enc);
    return 1;
  }
  if (opus_encoder_ctl(enc, OPUS_SET_BANDWIDTH((opus_int32)bandwidth)) != OPUS_OK) {
    fprintf(stderr, "OPUS_SET_BANDWIDTH failed\n");
    opus_encoder_destroy(enc);
    return 1;
  }
  if (opus_encoder_ctl(enc, OPUS_SET_COMPLEXITY((opus_int32)complexity)) != OPUS_OK) {
    fprintf(stderr, "OPUS_SET_COMPLEXITY failed\n");
    opus_encoder_destroy(enc);
    return 1;
  }

  /* Allocate PCM buffer and packet buffer */
  size_t samples_per_frame = (size_t)frame_size * (size_t)channels;
  float *pcm = (float *)malloc(samples_per_frame * sizeof(float));
  if (pcm == NULL) {
    fprintf(stderr, "pcm malloc failed\n");
    opus_encoder_destroy(enc);
    return 1;
  }
  unsigned char *pkt_buf = (unsigned char *)malloc(MAX_PACKET_BYTES);
  if (pkt_buf == NULL) {
    fprintf(stderr, "packet buf malloc failed\n");
    free(pcm);
    opus_encoder_destroy(enc);
    return 1;
  }

  /* Collect all encoded packets before writing output so we know the count */
  unsigned char **packets = (unsigned char **)malloc(num_frames * sizeof(unsigned char *));
  int *packet_lens = (int *)malloc(num_frames * sizeof(int));
  if (packets == NULL || packet_lens == NULL) {
    fprintf(stderr, "packet array malloc failed\n");
    free(pkt_buf);
    free(pcm);
    opus_encoder_destroy(enc);
    return 1;
  }
  uint32_t actual_packets = 0;

  for (uint32_t f = 0; f < num_frames; f++) {
    /* Read PCM: float32 LE samples interleaved by channel */
    for (size_t s = 0; s < samples_per_frame; s++) {
      unsigned char raw[4];
      uint32_t bits;
      if (!read_exact(raw, 4)) {
        fprintf(stderr, "truncated PCM at frame %u sample %zu\n", f, s);
        goto cleanup_fail;
      }
      bits = (uint32_t)raw[0] | ((uint32_t)raw[1] << 8) |
             ((uint32_t)raw[2] << 16) | ((uint32_t)raw[3] << 24);
      memcpy(&pcm[s], &bits, sizeof(float));
    }

    int n = opus_encode_float(enc, pcm, (int)frame_size, pkt_buf, MAX_PACKET_BYTES);
    if (n < 0) {
      fprintf(stderr, "opus_encode_float frame %u failed: %d\n", f, n);
      goto cleanup_fail;
    }
    if (n == 0) {
      /* DTX silence — skip (CBR should not produce these, but guard it) */
      continue;
    }
    packets[actual_packets] = (unsigned char *)malloc((size_t)n);
    if (packets[actual_packets] == NULL) {
      fprintf(stderr, "packet copy malloc failed at frame %u\n", f);
      goto cleanup_fail;
    }
    memcpy(packets[actual_packets], pkt_buf, (size_t)n);
    packet_lens[actual_packets] = n;
    actual_packets++;
  }

  /* Write output header */
  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(actual_packets)) {
    fprintf(stderr, "write output header failed\n");
    goto cleanup_fail;
  }
  /* Write each packet */
  for (uint32_t i = 0; i < actual_packets; i++) {
    if (!write_u32((uint32_t)packet_lens[i]) ||
        !write_exact(packets[i], (size_t)packet_lens[i])) {
      fprintf(stderr, "write packet %u failed\n", i);
      goto cleanup_fail;
    }
  }

  for (uint32_t i = 0; i < actual_packets; i++) free(packets[i]);
  free(packets);
  free(packet_lens);
  free(pkt_buf);
  free(pcm);
  opus_encoder_destroy(enc);
  return 0;

cleanup_fail:
  for (uint32_t i = 0; i < actual_packets; i++) free(packets[i]);
  free(packets);
  free(packet_lens);
  free(pkt_buf);
  free(pcm);
  opus_encoder_destroy(enc);
  return 1;
}
