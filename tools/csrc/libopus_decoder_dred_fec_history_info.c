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
#include "src/opus_private.h"
#include "silk/control.h"
#include "lpcnet_private.h"

#define INPUT_MAGIC "GDFI"
#define OUTPUT_MAGIC "GDFO"

#ifndef ENABLE_DEEP_PLC
#error "ENABLE_DEEP_PLC is required for DRED FEC history parity"
#endif

typedef struct {
  int celt_dec_offset;
  int silk_dec_offset;
  int channels;
  opus_int32 Fs;
  silk_DecControlStruct DecControl;
  int decode_gain;
  int complexity;
  int ignore_extensions;
  int arch;
  LPCNetPLCState lpcnet;
} GopusInternalOpusDecoder;

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

static int read_exact(void *dst, size_t n) {
  return fread(dst, 1, n, stdin) == n;
}

static int write_exact(const void *src, size_t n) {
  return fwrite(src, 1, n, stdout) == n;
}

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact(b, sizeof(b))) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) |
      ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int write_u32(uint32_t v) {
  unsigned char b[4];
  b[0] = (unsigned char)(v & 0xff);
  b[1] = (unsigned char)((v >> 8) & 0xff);
  b[2] = (unsigned char)((v >> 16) & 0xff);
  b[3] = (unsigned char)((v >> 24) & 0xff);
  return write_exact(b, sizeof(b));
}

static int write_i32(int32_t v) {
  return write_u32((uint32_t)v);
}

static int write_f32_array(const float *src, int count) {
  int i;
  for (i = 0; i < count; i++) {
    uint32_t bits;
    memcpy(&bits, &src[i], sizeof(bits));
    if (!write_u32(bits)) return 0;
  }
  return 1;
}

int main(void) {
  char magic[4];
  uint32_t version, seed_len, fec_len, frame_size, model_len;
  unsigned char *seed = NULL;
  unsigned char *fec = NULL;
  unsigned char *model = NULL;
  float *seed_pcm = NULL;
  float *fec_pcm = NULL;
  OpusDecoder *dec = NULL;
  int err = OPUS_OK;
  int ctl_result;
  int seed_ret, fec_ret;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0 ||
      !read_u32(&version) || version != 1 || !read_u32(&seed_len) ||
      !read_u32(&fec_len) || !read_u32(&frame_size) || !read_u32(&model_len)) {
    fprintf(stderr, "invalid DRED FEC history input\n");
    return 1;
  }
  if (seed_len == 0 || fec_len == 0 || frame_size == 0 || model_len == 0 ||
      seed_len > 4000 || fec_len > 4000 || frame_size > 5760) {
    fprintf(stderr, "invalid DRED FEC history lengths\n");
    return 1;
  }

  seed = (unsigned char *)malloc(seed_len);
  fec = (unsigned char *)malloc(fec_len);
  model = (unsigned char *)malloc(model_len);
  if (seed == NULL || fec == NULL || model == NULL ||
      !read_exact(seed, seed_len) || !read_exact(fec, fec_len) || !read_exact(model, model_len)) {
    fprintf(stderr, "truncated DRED FEC history input\n");
    goto fail;
  }

  dec = opus_decoder_create(48000, 1, &err);
  if (dec == NULL || err != OPUS_OK) {
    fprintf(stderr, "DRED FEC history decoder setup failed: %d\n", err);
    goto fail;
  }
  ctl_result = opus_decoder_ctl(dec, OPUS_SET_COMPLEXITY(0));
  if (ctl_result != OPUS_OK) {
    fprintf(stderr, "DRED FEC history complexity setup failed: %d\n", ctl_result);
    goto fail;
  }
#ifdef USE_WEIGHTS_FILE
  ctl_result = opus_decoder_ctl(dec, OPUS_SET_DNN_BLOB(model, (opus_int32)model_len));
  if (ctl_result != OPUS_OK) {
    fprintf(stderr, "DRED FEC history model setup failed: %d\n", ctl_result);
    goto fail;
  }
#else
  (void)model;
  (void)model_len;
#endif

  seed_pcm = (float *)calloc(5760, sizeof(float));
  fec_pcm = (float *)calloc(frame_size, sizeof(float));
  if (seed_pcm == NULL || fec_pcm == NULL) {
    fprintf(stderr, "DRED FEC history PCM allocation failed\n");
    goto fail;
  }

  seed_ret = opus_decode_float(dec, seed, (opus_int32)seed_len, seed_pcm, 5760, 0);
  if (seed_ret <= 0) {
    fprintf(stderr, "DRED FEC history prime decode failed: %d\n", seed_ret);
    goto fail;
  }
  fec_ret = opus_decode_float(dec, fec, (opus_int32)fec_len, fec_pcm, (int)frame_size, 1);
  if (fec_ret <= 0) {
    fprintf(stderr, "DRED FEC history FEC decode failed: %d\n", fec_ret);
    goto fail;
  }

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) ||
      !write_i32(seed_ret) || !write_i32(fec_ret) ||
      !write_f32_array(((GopusInternalOpusDecoder *)dec)->lpcnet.pcm, PLC_BUF_SIZE)) {
    fprintf(stderr, "DRED FEC history output failed\n");
    goto fail;
  }

  free(fec_pcm);
  free(seed_pcm);
  free(model);
  free(fec);
  free(seed);
  opus_decoder_destroy(dec);
  return 0;

fail:
  free(fec_pcm);
  free(seed_pcm);
  free(model);
  free(fec);
  free(seed);
  if (dec != NULL) opus_decoder_destroy(dec);
  return 1;
}
