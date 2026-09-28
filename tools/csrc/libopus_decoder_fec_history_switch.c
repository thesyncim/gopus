/* Exact PLC-history oracle across complexity changes through DecodeWithFEC. */
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
#include "cpu_support.h"

static int read32(uint32_t *v) {
  unsigned char b[4];
  if (fread(b, 1, 4, stdin) != 4) return 0;
  *v = (uint32_t)b[0] | (uint32_t)b[1] << 8 |
      (uint32_t)b[2] << 16 | (uint32_t)b[3] << 24;
  return 1;
}

static int write32(uint32_t v) {
  unsigned char b[4] = {(unsigned char)v, (unsigned char)(v >> 8),
      (unsigned char)(v >> 16), (unsigned char)(v >> 24)};
  return fwrite(b, 1, 4, stdout) == 4;
}

int main(void) {
  unsigned char magic[4];
  uint32_t version, rate, frame_size, packet_len, model_len;
  uint32_t complexity_step, complexity, step;
  unsigned char *model = NULL;
  unsigned char *packet = NULL;
  OpusDecoder *dec = NULL;
  float *pcm = NULL;
  int err = OPUS_OK;
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 1;
#endif
  if (fread(magic, 1, 4, stdin) != 4 || memcmp(magic, "GHFS", 4) ||
      !read32(&version) || version != 1 || !read32(&rate) ||
      !read32(&frame_size) || !read32(&packet_len) || !read32(&model_len) ||
      !read32(&complexity_step) || !read32(&complexity) || rate < 8000 ||
      rate > 48000 || frame_size == 0 || frame_size > 2880 ||
      packet_len == 0 || packet_len > 1275 || model_len == 0 ||
      model_len > 16 * 1024 * 1024 || complexity_step >= 4 || complexity > 10) return 2;
  model = (unsigned char *)malloc(model_len);
  packet = (unsigned char *)malloc(packet_len);
  pcm = (float *)malloc(frame_size * sizeof(*pcm));
  if (!model || !packet || !pcm ||
      fread(model, 1, model_len, stdin) != model_len ||
      fread(packet, 1, packet_len, stdin) != packet_len) return 3;
  dec = opus_decoder_create((opus_int32)rate, 1, &err);
  if (!dec || err != OPUS_OK ||
      opus_decoder_ctl(dec, OPUS_SET_COMPLEXITY(0)) != OPUS_OK ||
      opus_decoder_ctl(dec, OPUS_SET_DNN_BLOB(model, (opus_int32)model_len)) != OPUS_OK) return 4;

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
    if (fwrite("GHFO", 1, 4, stdout) != 4 || !write32(2) ||
        !write32(features) || !write32(opus_select_arch())) return 5;
#ifdef OPUS_HAVE_RTCD
    if (!write32(1)) return 5;
#else
    if (!write32(0)) return 5;
#endif
#ifdef OPUS_ARM_PRESUME_NEON_INTR
    if (!write32(1)) return 5;
#else
    if (!write32(0)) return 5;
#endif
    if (!write32(4)) return 5;
  }
  for (step = 0; step < 4; step++) {
    uint32_t range = 0, i;
    const int loss = step == 1 || step == 3;
    int n;
    if (step == complexity_step &&
        opus_decoder_ctl(dec, OPUS_SET_COMPLEXITY(complexity)) != OPUS_OK) return 6;
    n = opus_decode_float(dec, loss ? NULL : packet, loss ? 0 : (opus_int32)packet_len,
        pcm, (int)frame_size, loss ? 1 : 0);
    if (n < 0 || (uint32_t)n > frame_size ||
        opus_decoder_ctl(dec, OPUS_GET_FINAL_RANGE(&range)) != OPUS_OK ||
        !write32((uint32_t)n) || !write32(range)) return 7;
    for (i = 0; i < (uint32_t)n; i++) {
      uint32_t bits;
      memcpy(&bits, &pcm[i], sizeof(bits));
      if (!write32(bits)) return 8;
    }
  }
  opus_decoder_destroy(dec);
  free(pcm);
  free(packet);
  free(model);
  return 0;
}
