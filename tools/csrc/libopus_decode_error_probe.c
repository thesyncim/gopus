/* libopus_decode_error_probe.c
 *
 * Oracle helper that probes the exact error code libopus returns when
 * opus_decode / opus_decode_float / opus_decode24 is called with
 * a given (packet, packet_len, frame_size, decode_fec, sample_format) tuple.
 *
 * Wire format
 * -----------
 * Input (stdin):
 *   magic[4]       "GDEI"
 *   version        u32 = 1 or 2
 *   channels       u32 (1 or 2)
 *   sample_rate    u32 (8000/12000/16000/24000/48000)
 *   count          u32  -- number of probe cases
 *   For each case:
 *     sample_format  u32  (0=float32, 1=int16, 2=int24)
 *     frame_size     u32  (pcm buffer size in samples/channel, 0 → use full auto)
 *     decode_fec     u32  (0 or 1)
 *     packet_len     u32  (0 means NULL packet)
 *     packet[packet_len] bytes
 *
 * Output (stdout):
 *   magic[4]       "GDEO"
 *   version        u32 = 1 or 2 (echoes input version)
 *   count          u32
 *   For each case:
 *     error_code   i32   (negative libopus error, or positive sample count on success)
 *     -- version 2 only, additionally emits the decoded PCM on success:
 *     pcm_bytes    u32   (number of raw PCM bytes that follow; 0 when error_code<=0)
 *     pcm[pcm_bytes] raw little-endian samples (float32 / int16 / int32)
 *
 * Each probe case is decoded through a FRESH decoder so packet results are
 * independent and reproducible (no cross-case state leak). This makes the
 * helper a per-packet differential oracle for fuzzing.
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
#include "opus_defines.h"

#define INPUT_MAGIC  "GDEI"
#define OUTPUT_MAGIC "GDEO"

enum {
  SAMPLE_FORMAT_FLOAT32 = 0,
  SAMPLE_FORMAT_INT16   = 1,
  SAMPLE_FORMAT_INT24   = 2
};

static int read_exact(const void *dst, size_t n) {
  return fread((void *)dst, 1, n, stdin) == n;
}

static int write_exact(const void *src, size_t n) {
  return fwrite(src, 1, n, stdout) == n;
}

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

static int write_i32(int32_t v) {
  return write_u32((uint32_t)v);
}

int main(void) {
  unsigned char magic[4];
  uint32_t version, channels, sample_rate, count, i;
  OpusDecoder *dec = NULL;
  int err;

#ifdef _WIN32
  if (_setmode(_fileno(stdin),  _O_BINARY) == -1) return 1;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 1;
#endif

  /* --- read header --- */
  if (!read_exact(magic, 4) || memcmp(magic, INPUT_MAGIC, 4) != 0) {
    fprintf(stderr, "bad input magic\n"); return 1;
  }
  if (!read_u32(&version) || (version != 1 && version != 2)) {
    fprintf(stderr, "bad version\n"); return 1;
  }
  if (!read_u32(&channels) || channels < 1 || channels > 2) {
    fprintf(stderr, "bad channels\n"); return 1;
  }
  if (!read_u32(&sample_rate)) {
    fprintf(stderr, "bad sample_rate\n"); return 1;
  }
  if (!read_u32(&count)) {
    fprintf(stderr, "bad count\n"); return 1;
  }

  dec = opus_decoder_create((opus_int32)sample_rate, (int)channels, &err);
  if (!dec || err != OPUS_OK) {
    fprintf(stderr, "opus_decoder_create failed: %d\n", err); return 1;
  }

  /* --- write output header (echo input version) --- */
  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(version) || !write_u32(count)) {
    fprintf(stderr, "write header failed\n");
    opus_decoder_destroy(dec);
    return 1;
  }

  for (i = 0; i < count; i++) {
    uint32_t sample_format, frame_size_u32, decode_fec_u32, packet_len;
    int32_t result;
    unsigned char *packet = NULL;
    int frame_size;

    if (!read_u32(&sample_format) || !read_u32(&frame_size_u32) ||
        !read_u32(&decode_fec_u32) || !read_u32(&packet_len)) {
      fprintf(stderr, "read case %u header failed\n", i);
      opus_decoder_destroy(dec);
      return 1;
    }
    frame_size = (int)frame_size_u32;

    if (packet_len > 0) {
      packet = (unsigned char *)malloc(packet_len);
      if (!packet || !read_exact(packet, packet_len)) {
        fprintf(stderr, "read packet %u failed\n", i);
        free(packet);
        opus_decoder_destroy(dec);
        return 1;
      }
    }

    /* Each case gets a fresh decoder so state doesn't leak across cases */
    opus_decoder_destroy(dec);
    dec = opus_decoder_create((opus_int32)sample_rate, (int)channels, &err);
    if (!dec || err != OPUS_OK) {
      fprintf(stderr, "opus_decoder_create case %u failed\n", i);
      free(packet);
      return 1;
    }

    {
      /* Allocate a generously-sized PCM buffer */
      int buf_samples = frame_size > 0 ? frame_size : 5760;
      int total = buf_samples * (int)channels;
      size_t item_size = sample_format == SAMPLE_FORMAT_INT16 ? sizeof(opus_int16) :
                         sample_format == SAMPLE_FORMAT_INT24 ? sizeof(opus_int32) :
                         sizeof(float);
      void *pcm_buf = calloc((size_t)total, 4); /* 4 bytes covers float, int16, int32 */
      if (!pcm_buf) {
        fprintf(stderr, "alloc pcm_buf case %u\n", i);
        free(packet);
        opus_decoder_destroy(dec);
        return 1;
      }

      if (sample_format == SAMPLE_FORMAT_INT16) {
        result = (int32_t)opus_decode(dec,
                                      packet_len > 0 ? packet : NULL,
                                      (opus_int32)packet_len,
                                      (opus_int16 *)pcm_buf,
                                      buf_samples,
                                      (int)decode_fec_u32);
      } else if (sample_format == SAMPLE_FORMAT_INT24) {
        result = (int32_t)opus_decode24(dec,
                                        packet_len > 0 ? packet : NULL,
                                        (opus_int32)packet_len,
                                        (opus_int32 *)pcm_buf,
                                        buf_samples,
                                        (int)decode_fec_u32);
      } else {
        result = (int32_t)opus_decode_float(dec,
                                            packet_len > 0 ? packet : NULL,
                                            (opus_int32)packet_len,
                                            (float *)pcm_buf,
                                            buf_samples,
                                            (int)decode_fec_u32);
      }

      free(packet);

      if (!write_i32(result)) {
        fprintf(stderr, "write result case %u failed\n", i);
        free(pcm_buf);
        opus_decoder_destroy(dec);
        return 1;
      }
      if (version == 2) {
        /* Emit decoded PCM bytes on success; nothing on error. */
        uint32_t pcm_bytes = 0;
        if (result > 0) {
          pcm_bytes = (uint32_t)((size_t)result * (size_t)channels * item_size);
        }
        if (!write_u32(pcm_bytes) ||
            (pcm_bytes > 0 && !write_exact(pcm_buf, pcm_bytes))) {
          fprintf(stderr, "write pcm case %u failed\n", i);
          free(pcm_buf);
          opus_decoder_destroy(dec);
          return 1;
        }
      }
      free(pcm_buf);
    }
  }

  opus_decoder_destroy(dec);
  fflush(stdout);
  return 0;
}
