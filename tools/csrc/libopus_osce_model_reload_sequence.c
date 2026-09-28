/* Exact receive-sequence oracle for OSCE state across a DNN-model reload. */
#ifdef HAVE_CONFIG_H
#include "config.h"
#endif
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif
#include "opus.h"
#include "opus_multistream.h"
#include "cpu_support.h"

static int read32(uint32_t *v) {
  unsigned char b[4];
  if (fread(b, 1, 4, stdin) != 4) return 0;
  *v = (uint32_t)b[0] | (uint32_t)b[1]<<8 | (uint32_t)b[2]<<16 | (uint32_t)b[3]<<24;
  return 1;
}

static int write32(uint32_t v) {
  unsigned char b[4] = {(unsigned char)v, (unsigned char)(v>>8),
      (unsigned char)(v>>16), (unsigned char)(v>>24)};
  return fwrite(b, 1, 4, stdout) == 4;
}

static int set_model(OpusDecoder *dec, OpusMSDecoder *ms,
                     const unsigned char *blob, uint32_t blob_len) {
  if (ms) {
    OpusDecoder *child = NULL;
    int ret = opus_multistream_decoder_ctl(ms,
        OPUS_MULTISTREAM_GET_DECODER_STATE(0, &child));
    if (ret != OPUS_OK || child == NULL) return ret != OPUS_OK ? ret : OPUS_INTERNAL_ERROR;
    return opus_decoder_ctl(child, OPUS_SET_DNN_BLOB(blob, (opus_int32)blob_len));
  }
  return opus_decoder_ctl(dec, OPUS_SET_DNN_BLOB(blob, (opus_int32)blob_len));
}

static int set_osce_bwe(OpusDecoder *dec, OpusMSDecoder *ms, uint32_t enabled) {
  if (!enabled) return OPUS_OK;
  if (ms) {
    OpusDecoder *child = NULL;
    int ret = opus_multistream_decoder_ctl(ms,
        OPUS_MULTISTREAM_GET_DECODER_STATE(0, &child));
    if (ret != OPUS_OK || child == NULL) return ret != OPUS_OK ? ret : OPUS_INTERNAL_ERROR;
    return opus_decoder_ctl(child, OPUS_SET_OSCE_BWE(1));
  }
  return opus_decoder_ctl(dec, OPUS_SET_OSCE_BWE(1));
}

static int get_range(OpusDecoder *dec, OpusMSDecoder *ms, uint32_t *range) {
  if (ms) return opus_multistream_decoder_ctl(ms, OPUS_GET_FINAL_RANGE(range));
  return opus_decoder_ctl(dec, OPUS_GET_FINAL_RANGE(range));
}

int main(void) {
  unsigned char magic[4], mapping[1] = {0};
  uint32_t version, rate, channels, complexity, multistream, enable_bwe;
  uint32_t frame_size, count, reload_step, fec_step, blob_len, step;
  unsigned char *blob = NULL;
  OpusDecoder *dec = NULL;
  OpusMSDecoder *ms = NULL;
  float *pcm = NULL;
  int err = OPUS_OK;
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 1;
#endif
  if (fread(magic, 1, 4, stdin) != 4 || memcmp(magic, "GORL", 4) ||
      !read32(&version) || version != 2 || !read32(&rate) ||
      !read32(&channels) || !read32(&complexity) || !read32(&multistream) ||
      !read32(&enable_bwe) || !read32(&frame_size) || !read32(&count) ||
      !read32(&reload_step) || !read32(&fec_step) || !read32(&blob_len) ||
      channels != 1 || complexity > 10 || multistream > 1 || enable_bwe > 1 ||
      frame_size == 0 || frame_size > 2880 ||
      count < 2 || count > 64 ||
      (reload_step != UINT32_MAX && reload_step >= count) ||
      (fec_step != UINT32_MAX && fec_step >= count) || blob_len == 0 ||
      blob_len > 16 * 1024 * 1024) return 2;
  blob = (unsigned char *)malloc(blob_len);
  if (!blob || fread(blob, 1, blob_len, stdin) != blob_len) return 3;

  if (multistream) {
    ms = opus_multistream_decoder_create(rate, channels, 1, 0, mapping, &err);
    if (!ms || err != OPUS_OK ||
        opus_multistream_decoder_ctl(ms, OPUS_SET_COMPLEXITY(complexity)) != OPUS_OK ||
        set_osce_bwe(NULL, ms, enable_bwe) != OPUS_OK) return 4;
  } else {
    dec = opus_decoder_create(rate, channels, &err);
    if (!dec || err != OPUS_OK ||
        opus_decoder_ctl(dec, OPUS_SET_COMPLEXITY(complexity)) != OPUS_OK ||
        set_osce_bwe(dec, NULL, enable_bwe) != OPUS_OK) return 4;
  }
  if (set_model(dec, ms, blob, blob_len) != OPUS_OK) return 5;

  pcm = (float *)malloc(frame_size * channels * sizeof(*pcm));
  if (!pcm) return 6;
  {
    uint32_t features = 0;
#ifdef ENABLE_DRED
    features |= 1;
#endif
#ifdef ENABLE_OSCE
    features |= 2;
#endif
#ifdef ENABLE_QEXT
    features |= 4;
#endif
    if (fwrite("GORO", 1, 4, stdout) != 4 || !write32(1) ||
        !write32(features) || !write32(opus_select_arch()) || !write32(count)) return 7;
  }

  for (step = 0; step < count; step++) {
    uint32_t len, range, i;
    unsigned char packet[1275];
    int n;
    if (step == reload_step && set_model(dec, ms, blob, blob_len) != OPUS_OK) return 8;
    if (!read32(&len) || len == 0 || len > sizeof(packet) ||
        fread(packet, 1, len, stdin) != len) return 9;
    if (ms) {
      n = opus_multistream_decode_float(ms, packet, (opus_int32)len, pcm,
          (int)frame_size, step == fec_step);
    } else {
      n = opus_decode_float(dec, packet, (opus_int32)len, pcm,
          (int)frame_size, step == fec_step);
    }
    if (n < 0 || (uint32_t)n > frame_size || get_range(dec, ms, &range) != OPUS_OK) {
      fprintf(stderr, "decode step %u: n=%d\n", step, n);
      return 10;
    }
    if (!write32((uint32_t)n) || !write32(range)) return 11;
    for (i = 0; i < (uint32_t)n * channels; i++) {
      uint32_t bits;
      memcpy(&bits, &pcm[i], sizeof(bits));
      if (!write32(bits)) return 11;
    }
  }

  if (ms) opus_multistream_decoder_destroy(ms);
  if (dec) opus_decoder_destroy(dec);
  free(pcm);
  free(blob);
  return 0;
}
