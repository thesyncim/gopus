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
#include "opus_private.h"

#define INPUT_MAGIC "GDQI"
#define OUTPUT_MAGIC "GDQO"

#ifndef ENABLE_DEEP_PLC
#error "ENABLE_DEEP_PLC is required for decoder DRED quality comparison"
#endif

static int read_exact(void *dst, size_t n) {
  return fread(dst, 1, n, stdin) == n;
}

static int write_exact(const void *src, size_t n) {
  return fwrite(src, 1, n, stdout) == n;
}

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact(b, sizeof(b))) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) | ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int write_u32(uint32_t v) {
  unsigned char b[4];
  b[0] = (unsigned char)(v & 0xFF);
  b[1] = (unsigned char)((v >> 8) & 0xFF);
  b[2] = (unsigned char)((v >> 16) & 0xFF);
  b[3] = (unsigned char)((v >> 24) & 0xFF);
  return write_exact(b, sizeof(b));
}

static int write_i32(int32_t v) {
  return write_u32((uint32_t)v);
}

static int write_f32(float v) {
  union {
    float f;
    uint32_t u;
  } bits;
  bits.f = v;
  return write_u32(bits.u);
}

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

static int append_pcm(float *dst, uint32_t *offset, const float *src, int samples) {
  int i;
  for (i = 0; i < samples; i++) {
    dst[*offset + (uint32_t)i] = src[i];
  }
  *offset += (uint32_t)samples;
  return 1;
}

int main(void) {
  unsigned char magic[4];
  uint32_t version = 0;
  uint32_t sample_rate = 0;
  uint32_t channels = 0;
  uint32_t frame_size = 0;
  uint32_t packet_count = 0;
  uint32_t use_dred = 0;
  uint32_t decoder_model_blob_len = 0;
  uint32_t dred_model_blob_len = 0;
  unsigned char *decoder_model_blob = NULL;
  unsigned char *dred_model_blob = NULL;
  OpusDecoder *dec = NULL;
  OpusDREDDecoder *dred_dec = NULL;
  OpusDRED *dred = NULL;
  float *pcm = NULL;
  float *loss_pcm = NULL;
  uint32_t loss_pcm_samples = 0;
  uint32_t loss_frames = 0;
  uint32_t dred_frames = 0;
  uint32_t fallback_frames = 0;
  int expected = 0;
  int have_expected = 0;
  uint32_t frame = 0;
  int err = OPUS_OK;

  if (!set_binary_stdio()) {
    fprintf(stderr, "failed to set binary stdio mode\n");
    return 1;
  }
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) {
    fprintf(stderr, "invalid input magic\n");
    return 1;
  }
  if (!read_u32(&version) || version != 1 ||
      !read_u32(&sample_rate) ||
      !read_u32(&channels) ||
      !read_u32(&frame_size) ||
      !read_u32(&packet_count) ||
      !read_u32(&use_dred) ||
      !read_u32(&decoder_model_blob_len) ||
      !read_u32(&dred_model_blob_len)) {
    fprintf(stderr, "failed to read helper header\n");
    return 1;
  }
  if (sample_rate == 0 || channels == 0 || frame_size == 0 || packet_count == 0) {
    fprintf(stderr, "invalid sequence parameters\n");
    return 1;
  }

  if (decoder_model_blob_len > 0) {
    decoder_model_blob = (unsigned char *)malloc(decoder_model_blob_len);
    if (decoder_model_blob == NULL || !read_exact(decoder_model_blob, decoder_model_blob_len)) {
      fprintf(stderr, "failed to read decoder model blob\n");
      free(decoder_model_blob);
      return 1;
    }
  }
  if (dred_model_blob_len > 0) {
    dred_model_blob = (unsigned char *)malloc(dred_model_blob_len);
    if (dred_model_blob == NULL || !read_exact(dred_model_blob, dred_model_blob_len)) {
      fprintf(stderr, "failed to read DRED model blob\n");
      free(dred_model_blob);
      free(decoder_model_blob);
      return 1;
    }
  }

  dec = opus_decoder_create((opus_int32)sample_rate, (int)channels, &err);
  if (dec == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_decoder_create failed: %d\n", err);
    goto cleanup_fail;
  }
  err = opus_decoder_ctl(dec, OPUS_SET_COMPLEXITY(10));
  if (err != OPUS_OK) {
    fprintf(stderr, "OPUS_SET_COMPLEXITY failed: %d\n", err);
    goto cleanup_fail;
  }
#ifdef USE_WEIGHTS_FILE
  if (decoder_model_blob != NULL && decoder_model_blob_len > 0) {
    err = opus_decoder_ctl(dec, OPUS_SET_DNN_BLOB(decoder_model_blob, (opus_int32)decoder_model_blob_len));
    if (err != OPUS_OK) {
      fprintf(stderr, "OPUS_SET_DNN_BLOB failed: %d\n", err);
      goto cleanup_fail;
    }
  }
#endif

  dred_dec = opus_dred_decoder_create(&err);
  if (dred_dec == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_dred_decoder_create failed: %d\n", err);
    goto cleanup_fail;
  }
#ifdef USE_WEIGHTS_FILE
  if (dred_model_blob != NULL && dred_model_blob_len > 0) {
    err = opus_dred_decoder_ctl(dred_dec, OPUS_SET_DNN_BLOB(dred_model_blob, (opus_int32)dred_model_blob_len));
    if (err != OPUS_OK) {
      fprintf(stderr, "opus_dred_decoder_ctl(OPUS_SET_DNN_BLOB) failed: %d\n", err);
      goto cleanup_fail;
    }
  }
#endif
  dred = opus_dred_alloc(&err);
  if (dred == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_dred_alloc failed: %d\n", err);
    goto cleanup_fail;
  }

  pcm = (float *)calloc((size_t)frame_size * channels, sizeof(float));
  loss_pcm = (float *)calloc((size_t)packet_count * frame_size * channels, sizeof(float));
  if (pcm == NULL || loss_pcm == NULL) {
    fprintf(stderr, "pcm buffer alloc failed\n");
    goto cleanup_fail;
  }

  for (frame = 0; frame < packet_count; frame++) {
    uint32_t delivered = 0;
    uint32_t packet_len = 0;
    unsigned char *packet = NULL;
    int ret = 0;

    if (!read_u32(&delivered) || !read_u32(&packet_len)) {
      fprintf(stderr, "failed to read packet header\n");
      goto cleanup_fail;
    }
    if (packet_len > 0) {
      packet = (unsigned char *)malloc(packet_len);
      if (packet == NULL || !read_exact(packet, packet_len)) {
        fprintf(stderr, "failed to read packet bytes\n");
        free(packet);
        goto cleanup_fail;
      }
    }
    if (!delivered) {
      free(packet);
      continue;
    }
    if (packet == NULL || packet_len == 0) {
      fprintf(stderr, "delivered packet is empty\n");
      free(packet);
      goto cleanup_fail;
    }

    if (have_expected) {
      int missing = (int)frame - expected;
      if (missing > 0) {
        int available = 0;
        if (use_dred) {
          int dred_end = 0;
          available = opus_dred_parse(dred_dec, dred, packet, (opus_int32)packet_len,
              (opus_int32)(missing * (int)frame_size), (opus_int32)sample_rate, &dred_end, 0);
          if (available < 0) {
            available = 0;
          }
        }
        {
          int lost_ago;
          for (lost_ago = missing; lost_ago >= 1; lost_ago--) {
            int used_dred = 0;
            if (use_dred && available >= lost_ago * (int)frame_size) {
              ret = opus_decoder_dred_decode_float(dec, dred, lost_ago * (int)frame_size, pcm, (int)frame_size);
              if (ret >= 0) {
                used_dred = 1;
              }
            }
            if (!used_dred) {
              ret = opus_decode_float(dec, NULL, 0, pcm, (int)frame_size, 0);
            }
            if (ret < 0) {
              fprintf(stderr, "loss decode failed: %d\n", ret);
              free(packet);
              goto cleanup_fail;
            }
            if (!append_pcm(loss_pcm, &loss_pcm_samples, pcm, ret * (int)channels)) {
              free(packet);
              goto cleanup_fail;
            }
            loss_frames++;
            if (used_dred) {
              dred_frames++;
            } else {
              fallback_frames++;
            }
          }
        }
      }
    }

    ret = opus_decode_float(dec, packet, (opus_int32)packet_len, pcm, (int)frame_size, 0);
    if (ret < 0) {
      fprintf(stderr, "primary decode failed: %d\n", ret);
      free(packet);
      goto cleanup_fail;
    }
    expected = (int)frame + 1;
    have_expected = 1;
    free(packet);
  }

  if (!write_exact(OUTPUT_MAGIC, 4) ||
      !write_u32(1) ||
      !write_i32((int32_t)loss_frames) ||
      !write_i32((int32_t)dred_frames) ||
      !write_i32((int32_t)fallback_frames) ||
      !write_i32((int32_t)channels) ||
      !write_i32((int32_t)sample_rate) ||
      !write_i32((int32_t)frame_size) ||
      !write_i32((int32_t)loss_pcm_samples)) {
    fprintf(stderr, "failed to write output header\n");
    goto cleanup_fail;
  }
  {
    uint32_t i;
    for (i = 0; i < loss_pcm_samples; i++) {
      if (!write_f32(loss_pcm[i])) {
        fprintf(stderr, "failed to write output pcm\n");
        goto cleanup_fail;
      }
    }
  }

  free(loss_pcm);
  free(pcm);
  opus_dred_free(dred);
  opus_dred_decoder_destroy(dred_dec);
  opus_decoder_destroy(dec);
  free(dred_model_blob);
  free(decoder_model_blob);
  return 0;

cleanup_fail:
  free(loss_pcm);
  free(pcm);
  if (dred != NULL) opus_dred_free(dred);
  if (dred_dec != NULL) opus_dred_decoder_destroy(dred_dec);
  if (dec != NULL) opus_decoder_destroy(dec);
  free(dred_model_blob);
  free(decoder_model_blob);
  return 1;
}
