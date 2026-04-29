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

#include "cpu_support.h"
#include "dred_rdovae_enc.h"
#include "nnet.h"

#undef HAVE_CONFIG_H
#ifdef USE_WEIGHTS_FILE
#undef USE_WEIGHTS_FILE
#endif
#include "dred_rdovae_enc_data.c"

#define INPUT_MAGIC "GROI"
#define OUTPUT_MAGIC "GROO"
#define INPUT_FEATURES 40

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

static int read_exact(void *dst, size_t size) {
  return fread(dst, 1, size, stdin) == size;
}

static int write_exact(const void *src, size_t size) {
  return fwrite(src, 1, size, stdout) == size;
}

static int read_u32(uint32_t *out) {
  return read_exact(out, sizeof(*out));
}

static int write_u32(uint32_t value) {
  return write_exact(&value, sizeof(value));
}

static int read_bits_array(float *dst, int count) {
  int i;
  for (i = 0; i < count; i++) {
    uint32_t bits;
    if (!read_u32(&bits)) return 0;
    memcpy(&dst[i], &bits, sizeof(bits));
  }
  return 1;
}

static int write_bits_array(const float *src, int count) {
  int i;
  for (i = 0; i < count; i++) {
    uint32_t bits;
    memcpy(&bits, &src[i], sizeof(bits));
    if (!write_exact(&bits, sizeof(bits))) return 0;
  }
  return 1;
}

int main(void) {
  char magic[4];
  uint32_t version;
  uint32_t frame_count;
  uint32_t i;
  RDOVAEEnc model;
  RDOVAEEncState enc_state;
  float input[INPUT_FEATURES];
  float latents[DRED_LATENT_DIM];
  float state[DRED_STATE_DIM];
  int arch;

  if (!set_binary_stdio()) {
    fprintf(stderr, "failed to set binary stdio mode\n");
    return 1;
  }
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) {
    fprintf(stderr, "invalid input magic\n");
    return 1;
  }
  if (!read_u32(&version) || version != 1 || !read_u32(&frame_count)) {
    fprintf(stderr, "failed to read header\n");
    return 1;
  }
  if (frame_count == 0) {
    fprintf(stderr, "frame count must be positive\n");
    return 1;
  }
  if (init_rdovaeenc(&model, rdovaeenc_arrays) != 0) {
    fprintf(stderr, "init_rdovaeenc failed\n");
    return 1;
  }
  memset(&enc_state, 0, sizeof(enc_state));
  arch = opus_select_arch();

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(version) || !write_u32(frame_count)) {
    fprintf(stderr, "failed to write output header\n");
    return 1;
  }

  for (i = 0; i < frame_count; i++) {
    if (!read_bits_array(input, INPUT_FEATURES)) {
      fprintf(stderr, "failed to read input frame\n");
      return 1;
    }
    dred_rdovae_encode_dframe(&enc_state, &model, latents, state, input, arch);
    if (!write_bits_array(latents, DRED_LATENT_DIM) || !write_bits_array(state, DRED_STATE_DIM)) {
      fprintf(stderr, "failed to write output frame\n");
      return 1;
    }
  }

  return 0;
}
