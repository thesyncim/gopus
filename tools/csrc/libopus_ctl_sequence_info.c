/* libopus_ctl_sequence_info.c - generic encoder/decoder CTL-sequence oracle.
 *
 * Drives a single OpusEncoder or OpusDecoder through a seeded program of CTL
 * SET/GET requests interleaved with encodes/decodes and OPUS_RESET_STATE, then
 * reports each step's outcome. This is the behavioral oracle for the public
 * gopus typed CTL setters/getters: the Go fuzz harness applies the SAME program
 * through gopus and asserts SET return-code parity, GET value+return parity, and
 * post-encode lookahead / final-range / last-packet-duration parity.
 *
 * The program is a flat opcode list; every value is in the libopus argument
 * domain (e.g. OPUS_BANDWIDTH_* codes, OPUS_AUTO=-1000, OPUS_BITRATE_MAX=-1).
 *
 * Wire format (all little-endian):
 *   IN:  "GCTI" u32(version=1)
 *              u32(is_decoder)  -- 0=encoder, 1=decoder
 *              u32(sample_rate) u32(channels) u32(application)
 *              u32(frame_size)  -- per-channel samples per OP_PROCESS frame
 *              u32(feed_len)    -- decoder OP_PROCESS packet length (0 if none)
 *              bytes(feed_len)  -- decoder OP_PROCESS packet (pad to 4)
 *              u32(num_ops)
 *              [num_ops x u32(op) i32(request) i32(arg)]
 *   OUT: "GCTO" u32(version=1) u32(num_results)
 *              [num_results x i32(ret) i32(value) u32(have_value)]
 *
 * The decoder OP_PROCESS packet is supplied by the caller (not self-encoded) so
 * gopus and libopus decode byte-identical input and decode-derived GETs
 * (final-range, pitch, last-packet-duration) are comparable.
 *
 * Opcodes (op):
 *   0 OP_SET     opus_*_ctl(request, arg); result.ret = return code.
 *   1 OP_GET     opus_*_ctl(request, &value); result.ret = return code,
 *                result.value = value, result.have_value = 1.
 *   2 OP_PROCESS one encode (encoder) or one decode of a canned packet
 *                (decoder); result.ret = process return code (>=0 on success).
 *   3 OP_RESET   opus_*_ctl(OPUS_RESET_STATE); result.ret = return code.
 *
 * For OP_GET of unsigned 32-bit values (OPUS_GET_FINAL_RANGE) the value is
 * carried verbatim in the i32 slot (reinterpreted by the Go side as uint32).
 *
 * The encoder is fed a fixed 20 ms sine frame per OP_PROCESS; the decoder is
 * fed the packets produced by encoding that same sine with a sibling encoder
 * so OP_PROCESS exercises real last_packet_duration / pitch / bandwidth state.
 *
 * Reference: libopus src/opus_encoder.c opus_encoder_ctl,
 *            src/opus_decoder.c opus_decoder_ctl.
 */

#include <math.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "opus.h"

#define INPUT_MAGIC  "GCTI"
#define OUTPUT_MAGIC "GCTO"

#define OP_SET     0u
#define OP_GET     1u
#define OP_PROCESS 2u
#define OP_RESET   3u

#define MAX_FRAME 5760
#define MAX_PACKET 4000

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

static int read_i32(int32_t *v) {
  uint32_t u;
  if (!read_u32(&u)) return 0;
  *v = (int32_t)u;
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

static int write_i32(int32_t v) { return write_u32((uint32_t)v); }

/* Apply one SET request with a single opus_int32 argument. Both encoder and
 * decoder CTL SETs that the harness exercises take exactly one opus_int32, so a
 * single dispatch on the request code suffices. */
static int enc_set(OpusEncoder *e, int32_t request, int32_t arg) {
  return opus_encoder_ctl(e, request, (opus_int32)arg);
}
static int dec_set(OpusDecoder *d, int32_t request, int32_t arg) {
  return opus_decoder_ctl(d, request, (opus_int32)arg);
}

/* Apply one GET request returning a single opus_int32/opus_uint32. */
static int enc_get(OpusEncoder *e, int32_t request, int32_t *out) {
  opus_int32 v = 0;
  int ret;
  if (request == OPUS_GET_FINAL_RANGE_REQUEST) {
    opus_uint32 uv = 0;
    ret = opus_encoder_ctl(e, request, &uv);
    *out = (int32_t)uv;
    return ret;
  }
  ret = opus_encoder_ctl(e, request, &v);
  *out = (int32_t)v;
  return ret;
}
static int dec_get(OpusDecoder *d, int32_t request, int32_t *out) {
  opus_int32 v = 0;
  int ret;
  if (request == OPUS_GET_FINAL_RANGE_REQUEST) {
    opus_uint32 uv = 0;
    ret = opus_decoder_ctl(d, request, &uv);
    *out = (int32_t)uv;
    return ret;
  }
  ret = opus_decoder_ctl(d, request, &v);
  *out = (int32_t)v;
  return ret;
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

  uint32_t is_decoder, sample_rate, channels, application, frame_size_in, feed_len;
  if (!read_u32(&is_decoder) || !read_u32(&sample_rate) ||
      !read_u32(&channels) || !read_u32(&application) ||
      !read_u32(&frame_size_in) || !read_u32(&feed_len)) {
    fprintf(stderr, "truncated header\n");
    return 1;
  }
  if (channels < 1 || channels > 2) {
    fprintf(stderr, "bad channels %u\n", channels);
    return 1;
  }
  static unsigned char feed_pkt[MAX_PACKET];
  if (feed_len > MAX_PACKET) {
    fprintf(stderr, "feed packet too large %u\n", feed_len);
    return 1;
  }
  if (feed_len > 0 && !read_exact(feed_pkt, feed_len)) {
    fprintf(stderr, "truncated feed packet\n");
    return 1;
  }
  if (feed_len % 4 != 0) {
    unsigned char pad[4];
    if (!read_exact(pad, 4 - (feed_len % 4))) {
      fprintf(stderr, "truncated feed pad\n");
      return 1;
    }
  }
  uint32_t num_ops;
  if (!read_u32(&num_ops)) {
    fprintf(stderr, "truncated num_ops\n");
    return 1;
  }

  uint32_t *ops = NULL;
  int32_t *reqs = NULL;
  int32_t *args = NULL;
  if (num_ops > 0) {
    ops = (uint32_t *)malloc(sizeof(uint32_t) * num_ops);
    reqs = (int32_t *)malloc(sizeof(int32_t) * num_ops);
    args = (int32_t *)malloc(sizeof(int32_t) * num_ops);
    if (!ops || !reqs || !args) {
      fprintf(stderr, "oom\n");
      return 1;
    }
    for (uint32_t i = 0; i < num_ops; i++) {
      if (!read_u32(&ops[i]) || !read_i32(&reqs[i]) || !read_i32(&args[i])) {
        fprintf(stderr, "truncated op %u\n", i);
        return 1;
      }
    }
  }

  /* Build the OP_PROCESS sine frame. frame_size is the per-channel sample
   * count the gopus encoder uses (its default 960-sample frame), matched here
   * so the encode input is identical. */
  int frame = (int)frame_size_in;
  if (frame <= 0 || frame > MAX_FRAME) frame = 960;
  static float pcm[MAX_FRAME * 2];
  for (int i = 0; i < frame; i++) {
    float s = (float)(0.5 * sin(2.0 * M_PI * 440.0 * i / (double)sample_rate));
    if (channels == 2) {
      pcm[2 * i] = s;
      pcm[2 * i + 1] = s;
    } else {
      pcm[i] = s;
    }
  }

  int err = 0;
  OpusEncoder *enc = NULL;
  OpusDecoder *dec = NULL;

  if (is_decoder) {
    dec = opus_decoder_create((opus_int32)sample_rate, (int)channels, &err);
    if (!dec || err != OPUS_OK) {
      fprintf(stderr, "decoder_create failed %d\n", err);
      return 1;
    }
  } else {
    enc = opus_encoder_create((opus_int32)sample_rate, (int)channels,
                              (int)application, &err);
    if (!enc || err != OPUS_OK) {
      fprintf(stderr, "encoder_create failed %d\n", err);
      return 1;
    }
  }

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(num_ops)) {
    fprintf(stderr, "write header failed\n");
    return 1;
  }

  static unsigned char pkt[MAX_PACKET];
  static float decoded[MAX_FRAME * 2];

  for (uint32_t i = 0; i < num_ops; i++) {
    int32_t ret = 0, value = 0;
    uint32_t have_value = 0;
    switch (ops[i]) {
      case OP_SET:
        ret = is_decoder ? dec_set(dec, reqs[i], args[i])
                         : enc_set(enc, reqs[i], args[i]);
        break;
      case OP_GET:
        ret = is_decoder ? dec_get(dec, reqs[i], &value)
                         : enc_get(enc, reqs[i], &value);
        have_value = 1;
        break;
      case OP_PROCESS:
        if (is_decoder) {
          ret = opus_decode_float(dec, feed_pkt, feed_len, decoded, MAX_FRAME, 0);
        } else {
          ret = opus_encode_float(enc, pcm, frame, pkt, MAX_PACKET);
        }
        break;
      case OP_RESET:
        ret = is_decoder ? opus_decoder_ctl(dec, OPUS_RESET_STATE)
                         : opus_encoder_ctl(enc, OPUS_RESET_STATE);
        break;
      default:
        ret = OPUS_UNIMPLEMENTED;
        break;
    }
    if (!write_i32(ret) || !write_i32(value) || !write_u32(have_value)) {
      fprintf(stderr, "write result %u failed\n", i);
      return 1;
    }
  }

  if (enc) opus_encoder_destroy(enc);
  if (dec) opus_decoder_destroy(dec);
  free(ops);
  free(reqs);
  free(args);
  return 0;
}
