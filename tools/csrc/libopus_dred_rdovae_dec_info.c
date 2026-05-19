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
#include "dred_rdovae_dec.h"
#include "nnet.h"

#undef HAVE_CONFIG_H
#ifdef USE_WEIGHTS_FILE
#undef USE_WEIGHTS_FILE
#endif
#include "dred_rdovae_dec_data.c"

#define INPUT_MAGIC "GRDI"
#define OUTPUT_MAGIC "GRDO"

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
  uint32_t nb_latents;
  RDOVAEDec model;
  float state[DRED_STATE_DIM];
  float *latents = NULL;
  float *features = NULL;
  int latent_values;
  int feature_values;
  int arch;

  if (!set_binary_stdio()) {
    fprintf(stderr, "failed to set binary stdio mode\n");
    return 1;
  }
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) {
    fprintf(stderr, "invalid input magic\n");
    return 1;
  }
  if (!read_u32(&version) || version != 1 || !read_u32(&nb_latents)) {
    fprintf(stderr, "failed to read header\n");
    return 1;
  }
  if (nb_latents == 0 || nb_latents > 128) {
    fprintf(stderr, "invalid latent count\n");
    return 1;
  }
  latent_values = (int)nb_latents * (DRED_LATENT_DIM + 1);
  feature_values = (int)nb_latents * 4 * DRED_NUM_FEATURES;
  latents = (float *)malloc((size_t)latent_values * sizeof(*latents));
  features = (float *)malloc((size_t)feature_values * sizeof(*features));
  if (latents == NULL || features == NULL) {
    fprintf(stderr, "allocation failed\n");
    free(features);
    free(latents);
    return 1;
  }
  if (!read_bits_array(state, DRED_STATE_DIM) || !read_bits_array(latents, latent_values)) {
    fprintf(stderr, "failed to read state or latents\n");
    free(features);
    free(latents);
    return 1;
  }
  if (init_rdovaedec(&model, rdovaedec_arrays) != 0) {
    fprintf(stderr, "init_rdovaedec failed\n");
    free(features);
    free(latents);
    return 1;
  }
  arch = opus_select_arch();
  DRED_rdovae_decode_all(&model, features, state, latents, (int)nb_latents, arch);

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(version) || !write_u32(nb_latents) ||
      !write_bits_array(features, feature_values)) {
    fprintf(stderr, "failed to write output\n");
    free(features);
    free(latents);
    return 1;
  }

  free(features);
  free(latents);
  return 0;
}
