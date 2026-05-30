/* libopus_opus_encode_fixed_info.c — top-level opus_encode FIXED_POINT oracle.
 *
 * Built against the --enable-fixed-point reference tree (config.h defines
 * FIXED_POINT) so opus_encode() runs the integer API: int16 PCM is carried
 * unconverted through the resampler, SILK and CELT integer paths. Reads int16
 * LE PCM frames plus a forced-mode configuration and emits each produced full
 * Opus packet (TOC + payload) in the standard gopus oracle wire format.
 *
 * Wire format (all little-endian):
 *   IN:  "GOEI" u32(version=1)
 *              u32(sample_rate) u32(channels) u32(force_mode)
 *              u32(bandwidth) u32(bitrate) u32(complexity)
 *              u32(vbr) u32(vbr_constraint) u32(force_channels)
 *              u32(frame_size) u32(num_frames) u32(nsamples)
 *              [int16 ... ] [pad to 4]
 *   OUT: "GOEO" u32(version=1) u32(num_packets)
 *              [num_packets × u32(packet_len) bytes(packet_len) pad-to-4]
 *
 * force_mode values map to opus_private.h:
 *   1000 = MODE_SILK_ONLY, 1001 = MODE_HYBRID, 1002 = MODE_CELT_ONLY,
 *   0    = leave unset (auto).
 *
 * bandwidth values are the OPUS_BANDWIDTH_* constants (1101..1105); 0 leaves it
 * unset (auto).
 *
 * Reference: libopus src/opus_encoder.c opus_encode() (FIXED_POINT, int16 API).
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

#define INPUT_MAGIC  "GOEI"
#define OUTPUT_MAGIC "GOEO"
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

static int write_pad(size_t count) {
  unsigned char z[4] = {0, 0, 0, 0};
  size_t pad = (4 - (count % 4)) % 4;
  if (pad == 0) return 1;
  return write_exact(z, pad);
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

  uint32_t sample_rate, channels, force_mode, bandwidth, bitrate, complexity;
  uint32_t vbr, vbr_constraint, force_channels, frame_size, num_frames, nsamples;
  if (!read_u32(&sample_rate)    || !read_u32(&channels)       ||
      !read_u32(&force_mode)     || !read_u32(&bandwidth)      ||
      !read_u32(&bitrate)        || !read_u32(&complexity)     ||
      !read_u32(&vbr)            || !read_u32(&vbr_constraint) ||
      !read_u32(&force_channels) || !read_u32(&frame_size)     ||
      !read_u32(&num_frames)     || !read_u32(&nsamples)) {
    fprintf(stderr, "truncated header\n");
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
  if ((uint64_t)nsamples != (uint64_t)frame_size * channels * num_frames) {
    fprintf(stderr, "nsamples mismatch\n");
    return 1;
  }

  opus_int16 *pcm = (opus_int16 *)malloc((size_t)nsamples * sizeof(opus_int16));
  if (pcm == NULL && nsamples != 0) {
    fprintf(stderr, "pcm malloc failed\n");
    return 1;
  }
  for (uint32_t i = 0; i < nsamples; i++) {
    unsigned char b[2];
    if (!read_exact(b, 2)) {
      fprintf(stderr, "truncated PCM at %u\n", i);
      free(pcm);
      return 1;
    }
    pcm[i] = (opus_int16)((uint16_t)b[0] | ((uint16_t)b[1] << 8));
  }
  /* consume input padding to 4-byte boundary */
  {
    size_t pad = (4 - ((size_t)nsamples * 2) % 4) % 4;
    unsigned char tmp[4];
    if (pad > 0 && !read_exact(tmp, pad)) {
      fprintf(stderr, "truncated PCM pad\n");
      free(pcm);
      return 1;
    }
  }

  int err = OPUS_OK;
  OpusEncoder *enc = opus_encoder_create((opus_int32)sample_rate, (int)channels,
                                         OPUS_APPLICATION_AUDIO, &err);
  if (enc == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_encoder_create failed: %d\n", err);
    free(pcm);
    return 1;
  }

#define CTL(call) do { \
    if (opus_encoder_ctl(enc, call) != OPUS_OK) { \
      fprintf(stderr, "ctl failed: %s\n", #call); \
      opus_encoder_destroy(enc); free(pcm); return 1; \
    } \
  } while (0)

  CTL(OPUS_SET_BITRATE((opus_int32)bitrate));
  CTL(OPUS_SET_COMPLEXITY((opus_int32)complexity));
  CTL(OPUS_SET_VBR((opus_int32)(vbr ? 1 : 0)));
  CTL(OPUS_SET_VBR_CONSTRAINT((opus_int32)(vbr_constraint ? 1 : 0)));
  if (bandwidth != 0) {
    CTL(OPUS_SET_BANDWIDTH((opus_int32)bandwidth));
    CTL(OPUS_SET_MAX_BANDWIDTH((opus_int32)bandwidth));
  }
  if (force_channels != 0) {
    CTL(OPUS_SET_FORCE_CHANNELS((opus_int32)force_channels));
  }
  if (force_mode != 0) {
    CTL(OPUS_SET_FORCE_MODE((opus_int32)force_mode));
  }

  unsigned char *pkt_buf = (unsigned char *)malloc(MAX_PACKET_BYTES);
  unsigned char **packets = (unsigned char **)malloc((size_t)num_frames * sizeof(unsigned char *));
  int *packet_lens = (int *)malloc((size_t)num_frames * sizeof(int));
  if (pkt_buf == NULL || packets == NULL || packet_lens == NULL) {
    fprintf(stderr, "alloc failed\n");
    free(pkt_buf); free(packets); free(packet_lens);
    opus_encoder_destroy(enc); free(pcm);
    return 1;
  }

  uint32_t got = 0;
  size_t per = (size_t)frame_size * channels;
  for (uint32_t f = 0; f < num_frames; f++) {
    /* FORCE_MODE is cleared after each call in libopus; reassert it. */
    if (force_mode != 0) {
      if (opus_encoder_ctl(enc, OPUS_SET_FORCE_MODE((opus_int32)force_mode)) != OPUS_OK) {
        fprintf(stderr, "reassert force_mode failed at frame %u\n", f);
        goto fail;
      }
    }
    int n = opus_encode(enc, pcm + (size_t)f * per, (int)frame_size,
                        pkt_buf, MAX_PACKET_BYTES);
    if (n < 0) {
      fprintf(stderr, "opus_encode frame %u failed: %d\n", f, n);
      goto fail;
    }
    if (n == 0) {
      continue; /* DTX silence */
    }
    packets[got] = (unsigned char *)malloc((size_t)n);
    if (packets[got] == NULL) {
      fprintf(stderr, "packet copy malloc failed at %u\n", f);
      goto fail;
    }
    memcpy(packets[got], pkt_buf, (size_t)n);
    packet_lens[got] = n;
    got++;
  }

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(got)) {
    fprintf(stderr, "write header failed\n");
    goto fail;
  }
  for (uint32_t i = 0; i < got; i++) {
    if (!write_u32((uint32_t)packet_lens[i]) ||
        !write_exact(packets[i], (size_t)packet_lens[i]) ||
        !write_pad((size_t)packet_lens[i])) {
      fprintf(stderr, "write packet %u failed\n", i);
      goto fail;
    }
  }

  for (uint32_t i = 0; i < got; i++) free(packets[i]);
  free(packets); free(packet_lens); free(pkt_buf);
  opus_encoder_destroy(enc); free(pcm);
  return 0;

fail:
  for (uint32_t i = 0; i < got; i++) free(packets[i]);
  free(packets); free(packet_lens); free(pkt_buf);
  opus_encoder_destroy(enc); free(pcm);
  return 1;
}
