/* libopus_fec_encode_packets.c — in-band FEC (LBRR) encode oracle for gopus
 * parity testing.
 *
 * Mirrors libopus_cbr_encode_packets.c but enables OPUS_SET_INBAND_FEC and
 * OPUS_SET_PACKET_LOSS_PERC so the SILK LBRR (low-bitrate redundancy) path is
 * exercised. Encoding uses opus_encode_float() exactly like the CBR oracle.
 *
 * Wire format (all little-endian):
 *   IN:  "GFEC" u32(version=1) u32(application) u32(bandwidth) u32(channels)
 *              u32(bitrate) u32(frame_size) u32(complexity) u32(num_frames)
 *              u32(vbr) u32(force_channels) u32(packet_loss_perc)
 *              u32(force_mode)
 *              [num_frames × frame_size × channels × float32]
 *   OUT: "GFEO" u32(version=1) u32(num_packets)
 *              [num_packets × u32(packet_len) u8(packet_len × bytes)]
 *
 * application values match libopus_cbr_encode_packets.c.
 * bandwidth values match opus_defines.h.
 * force_mode: 0 = auto, otherwise an OPUS_SET_FORCE_MODE() argument
 *             (1000 = SILK, 1001 = Hybrid, 1002 = CELT).
 *
 * Reference: libopus src/opus_encoder.c opus_encode_float(),
 *            silk/enc_API.c LBRR handling.
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
#include "opus_private.h"

#define INPUT_MAGIC  "GFEC"
#define OUTPUT_MAGIC "GFEO"
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
  uint32_t vbr, force_channels, packet_loss, force_mode;
  if (!read_u32(&app_code)   || !read_u32(&bandwidth)  || !read_u32(&channels) ||
      !read_u32(&bitrate)    || !read_u32(&frame_size) || !read_u32(&complexity) ||
      !read_u32(&num_frames) || !read_u32(&vbr)        || !read_u32(&force_channels) ||
      !read_u32(&packet_loss)|| !read_u32(&force_mode)) {
    fprintf(stderr, "truncated header\n");
    return 1;
  }

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

  int err = OPUS_OK;
  OpusEncoder *enc = opus_encoder_create(48000, (int)channels, application, &err);
  if (enc == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_encoder_create failed: %d\n", err);
    return 1;
  }

  opus_encoder_ctl(enc, OPUS_SET_VBR((opus_int32)(vbr ? 1 : 0)));
  opus_encoder_ctl(enc, OPUS_SET_BITRATE((opus_int32)bitrate));
  opus_encoder_ctl(enc, OPUS_SET_BANDWIDTH((opus_int32)bandwidth));
  opus_encoder_ctl(enc, OPUS_SET_MAX_BANDWIDTH((opus_int32)bandwidth));
  opus_encoder_ctl(enc, OPUS_SET_COMPLEXITY((opus_int32)complexity));
  opus_encoder_ctl(enc, OPUS_SET_INBAND_FEC(1));
  opus_encoder_ctl(enc, OPUS_SET_PACKET_LOSS_PERC((opus_int32)packet_loss));
  if (force_channels == 1 || force_channels == 2) {
    opus_encoder_ctl(enc, OPUS_SET_FORCE_CHANNELS((opus_int32)force_channels));
  }
  if (force_mode != 0) {
    opus_encoder_ctl(enc, OPUS_SET_FORCE_MODE((opus_int32)force_mode));
  }

  size_t samples_per_frame = (size_t)frame_size * (size_t)channels;
  float *pcm = (float *)malloc(samples_per_frame * sizeof(float));
  unsigned char *pkt_buf = (unsigned char *)malloc(MAX_PACKET_BYTES);
  unsigned char **packets = (unsigned char **)malloc(num_frames * sizeof(unsigned char *));
  int *packet_lens = (int *)malloc(num_frames * sizeof(int));
  if (!pcm || !pkt_buf || !packets || !packet_lens) {
    fprintf(stderr, "malloc failed\n");
    return 1;
  }
  uint32_t actual_packets = 0;

  for (uint32_t f = 0; f < num_frames; f++) {
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
    packets[actual_packets] = (unsigned char *)malloc((size_t)(n > 0 ? n : 1));
    if (packets[actual_packets] == NULL) {
      fprintf(stderr, "packet copy malloc failed at frame %u\n", f);
      goto cleanup_fail;
    }
    memcpy(packets[actual_packets], pkt_buf, (size_t)(n > 0 ? n : 0));
    packet_lens[actual_packets] = n;
    actual_packets++;
  }

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(actual_packets)) {
    fprintf(stderr, "write output header failed\n");
    goto cleanup_fail;
  }
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
