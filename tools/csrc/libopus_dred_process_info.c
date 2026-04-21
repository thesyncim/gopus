#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "opus.h"
#include "dred_decoder.h"

#define GODI_MAGIC "GODI"
#define GODO_MAGIC "GODO"

static int read_exact(void *dst, size_t n) {
  return fread(dst, 1, n, stdin) == n;
}

static int write_exact(const void *src, size_t n) {
  return fwrite(src, 1, n, stdout) == n;
}

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact(b, sizeof(b))) {
    return 0;
  }
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

static uint32_t hash_f32_array(const float *data, int n) {
  uint32_t h = 2166136261u;
  int i;
  for (i = 0; i < n; i++) {
    union {
      float f;
      uint32_t u;
    } bits;
    bits.f = data[i];
    h ^= bits.u;
    h *= 16777619u;
  }
  return h;
}

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) {
    return 0;
  }
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) {
    return 0;
  }
#endif
  return 1;
}

int main(void) {
  unsigned char magic[4];
  uint32_t version = 0;
  uint32_t sample_rate = 0;
  uint32_t max_dred_samples = 0;
  uint32_t packet_len = 0;
  unsigned char *packet = NULL;
  OpusDREDDecoder *dred_dec = NULL;
  OpusDRED *dred = NULL;
  OpusDRED *clone = NULL;
  int err = OPUS_OK;
  int dred_end = 0;
  int parse_ret = 0;
  int process_ret = OPUS_BAD_ARG;
  int second_process_ret = OPUS_BAD_ARG;
  int clone_process_ret = OPUS_BAD_ARG;
  int second_process_stage = -1;
  int clone_process_stage = -1;
  uint32_t second_state_hash = 0;
  uint32_t second_latent_hash = 0;
  uint32_t second_feature_hash = 0;
  uint32_t clone_state_hash = 0;
  uint32_t clone_latent_hash = 0;
  uint32_t clone_feature_hash = 0;
  int i;
  int latent_values;
  int feature_values;

  if (!set_binary_stdio()) {
    fprintf(stderr, "failed to set binary stdio mode\n");
    return 1;
  }

  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, GODI_MAGIC, sizeof(magic)) != 0) {
    fprintf(stderr, "invalid input magic\n");
    return 1;
  }
  if (!read_u32(&version) || version != 1 || !read_u32(&sample_rate) || !read_u32(&max_dred_samples) ||
      !read_u32(&packet_len)) {
    fprintf(stderr, "failed to read helper header\n");
    return 1;
  }

  if (packet_len > 0) {
    packet = (unsigned char *)malloc(packet_len);
    if (packet == NULL || !read_exact(packet, packet_len)) {
      fprintf(stderr, "failed to read packet payload\n");
      free(packet);
      return 1;
    }
  }

  dred_dec = opus_dred_decoder_create(&err);
  if (dred_dec == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_dred_decoder_create failed: %d\n", err);
    free(packet);
    return 1;
  }

  dred = opus_dred_alloc(&err);
  if (dred == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_dred_alloc failed: %d\n", err);
    opus_dred_decoder_destroy(dred_dec);
    free(packet);
    return 1;
  }
  clone = opus_dred_alloc(&err);
  if (clone == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_dred_alloc(clone) failed: %d\n", err);
    opus_dred_free(dred);
    opus_dred_decoder_destroy(dred_dec);
    free(packet);
    return 1;
  }

  memset(dred, 0, sizeof(*dred));
  memset(clone, 0, sizeof(*clone));
  parse_ret = opus_dred_parse(dred_dec, dred, packet, (opus_int32)packet_len, (opus_int32)max_dred_samples,
                              (opus_int32)sample_rate, &dred_end, 1);
  if (parse_ret >= 0 && dred->process_stage == 1) {
    process_ret = opus_dred_process(dred_dec, dred, dred);
  }
  if (process_ret == OPUS_OK && dred->process_stage == 2) {
    second_process_ret = opus_dred_process(dred_dec, dred, dred);
    second_process_stage = dred->process_stage;
    latent_values = dred->nb_latents * (DRED_LATENT_DIM + 1);
    feature_values = dred->nb_latents * 4 * DRED_NUM_FEATURES;
    second_state_hash = hash_f32_array(dred->state, DRED_STATE_DIM);
    second_latent_hash = hash_f32_array(dred->latents, latent_values);
    second_feature_hash = hash_f32_array(dred->fec_features, feature_values);

    clone_process_ret = opus_dred_process(dred_dec, dred, clone);
    clone_process_stage = clone->process_stage;
    clone_state_hash = hash_f32_array(clone->state, DRED_STATE_DIM);
    clone_latent_hash = hash_f32_array(clone->latents, latent_values);
    clone_feature_hash = hash_f32_array(clone->fec_features, feature_values);
  }

  if (!write_exact(GODO_MAGIC, 4) || !write_u32(1) || !write_i32(parse_ret) || !write_i32(dred_end) ||
      !write_i32(process_ret) || !write_i32(dred->process_stage) || !write_i32(dred->nb_latents) ||
      !write_i32(dred->dred_offset) || !write_i32(second_process_ret) || !write_i32(second_process_stage) ||
      !write_i32(clone_process_ret) || !write_i32(clone_process_stage) || !write_u32(second_state_hash) ||
      !write_u32(second_latent_hash) || !write_u32(second_feature_hash) || !write_u32(clone_state_hash) ||
      !write_u32(clone_latent_hash) || !write_u32(clone_feature_hash)) {
    fprintf(stderr, "failed to write helper header\n");
    opus_dred_free(clone);
    opus_dred_free(dred);
    opus_dred_decoder_destroy(dred_dec);
    free(packet);
    return 1;
  }

  for (i = 0; i < DRED_STATE_DIM; i++) {
    if (!write_f32(dred->state[i])) {
      fprintf(stderr, "failed to write state\n");
      opus_dred_free(clone);
      opus_dred_free(dred);
      opus_dred_decoder_destroy(dred_dec);
      free(packet);
      return 1;
    }
  }

  latent_values = dred->nb_latents * (DRED_LATENT_DIM + 1);
  for (i = 0; i < latent_values; i++) {
    if (!write_f32(dred->latents[i])) {
      fprintf(stderr, "failed to write latents\n");
      opus_dred_free(clone);
      opus_dred_free(dred);
      opus_dred_decoder_destroy(dred_dec);
      free(packet);
      return 1;
    }
  }

  feature_values = dred->nb_latents * 4 * DRED_NUM_FEATURES;
  for (i = 0; i < feature_values; i++) {
    if (!write_f32(dred->fec_features[i])) {
      fprintf(stderr, "failed to write features\n");
      opus_dred_free(clone);
      opus_dred_free(dred);
      opus_dred_decoder_destroy(dred_dec);
      free(packet);
      return 1;
    }
  }

  opus_dred_free(clone);
  opus_dred_free(dred);
  opus_dred_decoder_destroy(dred_dec);
  free(packet);
  return 0;
}
