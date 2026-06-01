/* libopus_encode_diff_info.c — comprehensive FLOAT opus_encode_float oracle for
 * the encode-side differential fuzz harness.
 *
 * Built against the default (float) reference tree so opus_encode_float() runs
 * the same float SILK/CELT/Hybrid + float Opus API wrapper (dc_reject,
 * resampler, stereo analysis) that the default gopus build mirrors. This is the
 * byte-exact oracle for the public float Encoder: unlike the FIXED_POINT
 * opus_encode oracle, there is no float-vs-integer wrapper boundary, so the
 * produced full Opus packets must be byte-identical to gopus on the same arch.
 *
 * One encoder is created per case and driven STATEFULLY across all frames (no
 * reset), so cross-frame state (VBR reservoir, energy histories, mode hysteresis,
 * DTX/FEC cadence) is exercised exactly as a real stream.
 *
 * Wire format (all little-endian):
 *   IN:  "GEDI" u32(version=1)
 *              u32(sample_rate) u32(channels) u32(application)
 *              u32(force_mode) u32(bandwidth) u32(max_bandwidth)
 *              u32(bitrate) u32(complexity) u32(signal)
 *              u32(vbr) u32(vbr_constraint) u32(force_channels)
 *              u32(inband_fec) u32(packet_loss) u32(dtx)
 *              u32(lsb_depth) u32(prediction_disabled) u32(phase_inv_disabled)
 *              u32(frame_size) u32(num_frames) u32(nsamples)
 *              [float32 ... ] (interleaved, nsamples = frame_size*channels*num_frames)
 *   OUT: "GEDO" u32(version=1) u32(num_records)
 *              [num_records × u32(ret) u32(final_range) u32(packet_len)
 *                             bytes(packet_len) pad-to-4]
 *
 * ret is the opus_encode_float return: >0 packet length, ==1 DTX/CELT-silence
 * TOC-only packet, ==0 DTX no-output (no bytes follow), <0 error code. The Go
 * side asserts gopus produces an identical ret/packet/final_range per frame.
 *
 * force_mode: 0=auto, 1000=SILK_ONLY, 1001=HYBRID, 1002=CELT_ONLY
 *   (OPUS_SET_FORCE_MODE is cleared by opus_encode each call; we reassert it).
 * bandwidth/max_bandwidth: OPUS_BANDWIDTH_* (1101..1105); 0 leaves unset.
 * signal: OPUS_SIGNAL_VOICE(3001)/MUSIC(3002), or 0xFFFFFC18 (OPUS_AUTO=-1000).
 *
 * Reference: libopus src/opus_encoder.c opus_encode_float().
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

#define INPUT_MAGIC  "GEDI"
#define OUTPUT_MAGIC "GEDO"
#define MAX_PACKET_BYTES 4000

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

  uint32_t sample_rate, channels, application;
  uint32_t force_mode, bandwidth, max_bandwidth;
  uint32_t bitrate, complexity, signal;
  uint32_t vbr, vbr_constraint, force_channels;
  uint32_t inband_fec, packet_loss, dtx;
  uint32_t lsb_depth, prediction_disabled, phase_inv_disabled;
  uint32_t frame_size, num_frames, nsamples;
  if (!read_u32(&sample_rate) || !read_u32(&channels) || !read_u32(&application) ||
      !read_u32(&force_mode)  || !read_u32(&bandwidth) || !read_u32(&max_bandwidth) ||
      !read_u32(&bitrate)     || !read_u32(&complexity) || !read_u32(&signal) ||
      !read_u32(&vbr)         || !read_u32(&vbr_constraint) || !read_u32(&force_channels) ||
      !read_u32(&inband_fec)  || !read_u32(&packet_loss) || !read_u32(&dtx) ||
      !read_u32(&lsb_depth)   || !read_u32(&prediction_disabled) || !read_u32(&phase_inv_disabled) ||
      !read_u32(&frame_size)  || !read_u32(&num_frames) || !read_u32(&nsamples)) {
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

  float *pcm = (float *)malloc((size_t)nsamples * sizeof(float));
  if (pcm == NULL && nsamples != 0) {
    fprintf(stderr, "pcm malloc failed\n");
    return 1;
  }
  for (uint32_t i = 0; i < nsamples; i++) {
    uint32_t bits;
    if (!read_u32(&bits)) {
      fprintf(stderr, "truncated PCM at %u\n", i);
      free(pcm);
      return 1;
    }
    memcpy(&pcm[i], &bits, 4);
  }

  int err = OPUS_OK;
  OpusEncoder *enc = opus_encoder_create((opus_int32)sample_rate, (int)channels,
                                         (int)application, &err);
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
  CTL(OPUS_SET_INBAND_FEC((opus_int32)inband_fec));
  CTL(OPUS_SET_PACKET_LOSS_PERC((opus_int32)packet_loss));
  CTL(OPUS_SET_DTX((opus_int32)(dtx ? 1 : 0)));
  CTL(OPUS_SET_PREDICTION_DISABLED((opus_int32)(prediction_disabled ? 1 : 0)));
  CTL(OPUS_SET_PHASE_INVERSION_DISABLED((opus_int32)(phase_inv_disabled ? 1 : 0)));
  if (lsb_depth != 0) {
    CTL(OPUS_SET_LSB_DEPTH((opus_int32)lsb_depth));
  }
  CTL(OPUS_SET_SIGNAL((opus_int32)signal));
  if (bandwidth != 0) {
    CTL(OPUS_SET_BANDWIDTH((opus_int32)bandwidth));
  }
  if (max_bandwidth != 0) {
    CTL(OPUS_SET_MAX_BANDWIDTH((opus_int32)max_bandwidth));
  }
  if (force_channels != 0) {
    CTL(OPUS_SET_FORCE_CHANNELS((opus_int32)force_channels));
  }
  if (force_mode != 0) {
    CTL(OPUS_SET_FORCE_MODE((opus_int32)force_mode));
  }

  unsigned char *pkt_buf = (unsigned char *)malloc(MAX_PACKET_BYTES);
  if (pkt_buf == NULL) {
    fprintf(stderr, "alloc failed\n");
    opus_encoder_destroy(enc); free(pcm);
    return 1;
  }

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(num_frames)) {
    fprintf(stderr, "write header failed\n");
    free(pkt_buf); opus_encoder_destroy(enc); free(pcm);
    return 1;
  }

  size_t per = (size_t)frame_size * channels;
  for (uint32_t f = 0; f < num_frames; f++) {
    /* FORCE_MODE is cleared after each opus_encode call; reassert it. */
    if (force_mode != 0) {
      if (opus_encoder_ctl(enc, OPUS_SET_FORCE_MODE((opus_int32)force_mode)) != OPUS_OK) {
        fprintf(stderr, "reassert force_mode failed at frame %u\n", f);
        free(pkt_buf); opus_encoder_destroy(enc); free(pcm);
        return 1;
      }
    }
    int n = opus_encode_float(enc, pcm + (size_t)f * per, (int)frame_size,
                              pkt_buf, MAX_PACKET_BYTES);
    uint32_t final_range = 0;
    opus_encoder_ctl(enc, OPUS_GET_FINAL_RANGE(&final_range));

    uint32_t plen = (n > 0) ? (uint32_t)n : 0;
    if (!write_u32((uint32_t)(int32_t)n) || !write_u32(final_range) || !write_u32(plen)) {
      fprintf(stderr, "write record %u failed\n", f);
      free(pkt_buf); opus_encoder_destroy(enc); free(pcm);
      return 1;
    }
    if (plen > 0) {
      if (!write_exact(pkt_buf, (size_t)plen) || !write_pad((size_t)plen)) {
        fprintf(stderr, "write packet %u failed\n", f);
        free(pkt_buf); opus_encoder_destroy(enc); free(pcm);
        return 1;
      }
    }
  }

  free(pkt_buf);
  opus_encoder_destroy(enc);
  free(pcm);
  return 0;
}
