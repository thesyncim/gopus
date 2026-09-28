#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#ifdef HAVE_CONFIG_H
#include "config.h"
#endif

#include "opus.h"
#include "opus_multistream.h"
#include "cpu_support.h"

#define DNN_BUILD_RTCD                         (1u << 0)
#define DNN_BUILD_ARM_MAY_DOTPROD              (1u << 1)
#define DNN_BUILD_ARM_MAY_NEON_INTR            (1u << 2)
#define DNN_BUILD_ARM_PRESUME_AARCH64_NEON_INTR (1u << 3)
#define DNN_BUILD_ARM_PRESUME_DOTPROD           (1u << 4)
#define DNN_BUILD_ARM_PRESUME_NEON_INTR         (1u << 5)
#define DNN_BUILD_X86_MAY_AVX2                  (1u << 6)
#define DNN_BUILD_X86_PRESUME_AVX2              (1u << 7)
#define DNN_BUILD_X86_MAY_SSE4_1                (1u << 8)
#define DNN_BUILD_X86_PRESUME_SSE4_1            (1u << 9)

static uint32_t dnn_build_flags(void) {
  uint32_t flags = 0;
#ifdef OPUS_HAVE_RTCD
  flags |= DNN_BUILD_RTCD;
#endif
#ifdef OPUS_ARM_MAY_HAVE_DOTPROD
  flags |= DNN_BUILD_ARM_MAY_DOTPROD;
#endif
#ifdef OPUS_ARM_MAY_HAVE_NEON_INTR
  flags |= DNN_BUILD_ARM_MAY_NEON_INTR;
#endif
#ifdef OPUS_ARM_PRESUME_AARCH64_NEON_INTR
  flags |= DNN_BUILD_ARM_PRESUME_AARCH64_NEON_INTR;
#endif
#ifdef OPUS_ARM_PRESUME_DOTPROD
  flags |= DNN_BUILD_ARM_PRESUME_DOTPROD;
#endif
#ifdef OPUS_ARM_PRESUME_NEON_INTR
  flags |= DNN_BUILD_ARM_PRESUME_NEON_INTR;
#endif
#ifdef OPUS_X86_MAY_HAVE_AVX2
  flags |= DNN_BUILD_X86_MAY_AVX2;
#endif
#ifdef OPUS_X86_PRESUME_AVX2
  flags |= DNN_BUILD_X86_PRESUME_AVX2;
#endif
#ifdef OPUS_X86_MAY_HAVE_SSE4_1
  flags |= DNN_BUILD_X86_MAY_SSE4_1;
#endif
#ifdef OPUS_X86_PRESUME_SSE4_1
  flags |= DNN_BUILD_X86_PRESUME_SSE4_1;
#endif
  return flags;
}

static int read_exact(void *dst, size_t size) {
  return fread(dst, 1, size, stdin) == size;
}

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact(b, sizeof(b))) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) |
      ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int write_exact(const void *src, size_t size) {
  return fwrite(src, 1, size, stdout) == size;
}

static int write_u32(uint32_t value) {
  unsigned char b[4] = {
      (unsigned char)value,
      (unsigned char)(value >> 8),
      (unsigned char)(value >> 16),
      (unsigned char)(value >> 24),
  };
  return write_exact(b, sizeof(b));
}

int main(void) {
  unsigned char magic[4];
  uint32_t version, sample_rate, channels, streams, coupled, frame_size, step_count;
  unsigned char *mapping = NULL;
  float *pcm = NULL;
  OpusMSDecoder *decoder = NULL;
  int err = OPUS_OK;
  uint32_t step;

#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 1;
#endif

  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, "GMSC", 4) != 0 ||
      !read_u32(&version) || version != 1 ||
      !read_u32(&sample_rate) || !read_u32(&channels) ||
      !read_u32(&streams) || !read_u32(&coupled) ||
      !read_u32(&frame_size) || !read_u32(&step_count)) return 2;
  if (channels == 0 || channels > 2 || streams == 0 || streams > channels ||
      coupled > streams || frame_size == 0 || step_count == 0 || step_count > 64) return 3;

  mapping = (unsigned char *)malloc(channels);
  pcm = (float *)malloc((size_t)frame_size * channels * sizeof(float));
  if (mapping == NULL || pcm == NULL || !read_exact(mapping, channels)) return 4;

  decoder = opus_multistream_decoder_create((opus_int32)sample_rate, (int)channels,
      (int)streams, (int)coupled, mapping, &err);
  if (decoder == NULL || err != OPUS_OK) return 5;

  if (!write_exact("GMSR", 4) || !write_u32(2) ||
      !write_u32((uint32_t)opus_select_arch()) ||
      !write_u32(dnn_build_flags())) return 6;
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
    if (!write_u32(features) || !write_u32(step_count)) return 7;
  }

  for (step = 0; step < step_count; step++) {
    uint32_t complexity, reset, packet_len, final_range = 0;
    unsigned char *packet = NULL;
    int ret;
    uint32_t i;
    if (!read_u32(&complexity) || !read_u32(&reset) || !read_u32(&packet_len) ||
        complexity > 10 || reset > 1 || packet_len > 1275) return 8;
    if (packet_len != 0) {
      packet = (unsigned char *)malloc(packet_len);
      if (packet == NULL || !read_exact(packet, packet_len)) return 9;
    }
    if (reset && opus_multistream_decoder_ctl(decoder, OPUS_RESET_STATE) != OPUS_OK) return 10;
    if (opus_multistream_decoder_ctl(decoder, OPUS_SET_COMPLEXITY((int)complexity)) != OPUS_OK) return 11;
    ret = opus_multistream_decode_float(decoder, packet, (opus_int32)packet_len,
        pcm, (int)frame_size, 0);
    free(packet);
    if (ret < 0 || (uint32_t)ret > frame_size ||
        opus_multistream_decoder_ctl(decoder, OPUS_GET_FINAL_RANGE(&final_range)) != OPUS_OK) return 12;
    if (!write_u32((uint32_t)ret) || !write_u32(final_range)) return 13;
    for (i = 0; i < (uint32_t)ret * channels; i++) {
      uint32_t bits;
      memcpy(&bits, &pcm[i], sizeof(bits));
      if (!write_u32(bits)) return 14;
    }
  }

  opus_multistream_decoder_destroy(decoder);
  free(mapping);
  free(pcm);
  return 0;
}
