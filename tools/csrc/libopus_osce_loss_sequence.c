/* Selected-feature public root/multistream OSCE receive/loss/recovery oracle. */
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

int main(void) {
  unsigned char magic[4], mapping[2] = {0, 1};
  uint32_t version, rate, channels, complexity, multistream, size, count, step;
  OpusDecoder *dec = NULL;
  OpusMSDecoder *ms = NULL;
  float *pcm;
  int err;
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 1;
#endif
  if (fread(magic, 1, 4, stdin) != 4 || memcmp(magic, "GOLI", 4) ||
      !read32(&version) || version != 1 || !read32(&rate) ||
      !read32(&channels) || !read32(&complexity) || !read32(&multistream) ||
      !read32(&size) || !read32(&count) || channels < 1 || channels > 2 ||
      complexity > 10 || multistream > 1 || size == 0 || size > 11520 ||
      count == 0 || count > 64) return 2;
  if (multistream) {
    ms = opus_multistream_decoder_create(rate, channels, 1, channels-1, mapping, &err);
    if (!ms || err != OPUS_OK ||
        opus_multistream_decoder_ctl(ms, OPUS_SET_COMPLEXITY(complexity)) != OPUS_OK) return 3;
  } else {
    dec = opus_decoder_create(rate, channels, &err);
    if (!dec || err != OPUS_OK ||
        opus_decoder_ctl(dec, OPUS_SET_COMPLEXITY(complexity)) != OPUS_OK) return 3;
  }
  pcm = malloc(size * channels * sizeof(*pcm));
  if (!pcm) return 4;
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
    if (fwrite("GOLO", 1, 4, stdout) != 4 || !write32(1) ||
        !write32(features) || !write32(opus_select_arch()) || !write32(count)) return 5;
  }
  for (step = 0; step < count; step++) {
    uint32_t len, range, i;
    unsigned char packet[1275];
    int n;
    if (!read32(&len) || len > sizeof(packet) || fread(packet, 1, len, stdin) != len) return 6;
    if (ms) {
      n = opus_multistream_decode_float(ms, len ? packet : NULL, len, pcm, size, 0);
      err = opus_multistream_decoder_ctl(ms, OPUS_GET_FINAL_RANGE(&range));
    } else {
      n = opus_decode_float(dec, len ? packet : NULL, len, pcm, size, 0);
      err = opus_decoder_ctl(dec, OPUS_GET_FINAL_RANGE(&range));
    }
    if (n < 0 || (uint32_t)n > size || err != OPUS_OK) {
      fprintf(stderr, "decode step %u: n=%d ctl=%d\n", step, n, err);
      return 7;
    }
    if (!write32(n) || !write32(range)) return 8;
    for (i = 0; i < (uint32_t)n * channels; i++) {
      uint32_t bits;
      memcpy(&bits, &pcm[i], sizeof(bits));
      if (!write32(bits)) return 8;
    }
  }
  if (ms) opus_multistream_decoder_destroy(ms);
  if (dec) opus_decoder_destroy(dec);
  free(pcm);
  return 0;
}
