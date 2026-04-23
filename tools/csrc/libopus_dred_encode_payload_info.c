#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "dred_encoder.h"

#define INPUT_MAGIC "GDPI"
#define OUTPUT_MAGIC "GDPO"

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

int main(void) {
  char magic[4];
  uint32_t version;
  uint32_t q0, dQ, qmax, max_chunks, max_bytes;
  uint32_t latents_fill, dred_offset, latent_offset, last_extra_dred_offset;
  unsigned char activity_mem[DRED_MAX_FRAMES * 4];
  unsigned char *output = NULL;
  uint32_t payload_bytes;
  DREDEnc enc;

  if (!set_binary_stdio()) {
    fprintf(stderr, "failed to set binary stdio mode\n");
    return 1;
  }
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) {
    fprintf(stderr, "invalid input magic\n");
    return 1;
  }
  if (!read_u32(&version) || version != 1 || !read_u32(&q0) || !read_u32(&dQ) ||
      !read_u32(&qmax) || !read_u32(&max_chunks) || !read_u32(&max_bytes) ||
      !read_u32(&latents_fill) || !read_u32(&dred_offset) || !read_u32(&latent_offset) ||
      !read_u32(&last_extra_dred_offset)) {
    fprintf(stderr, "failed to read header\n");
    return 1;
  }

  memset(&enc, 0, sizeof(enc));
  enc.latents_buffer_fill = (int)latents_fill;
  enc.dred_offset = (int)dred_offset;
  enc.latent_offset = (int)latent_offset;
  enc.last_extra_dred_offset = (int)last_extra_dred_offset;

  if (!read_bits_array(enc.state_buffer, DRED_MAX_FRAMES * DRED_STATE_DIM) ||
      !read_bits_array(enc.latents_buffer, DRED_MAX_FRAMES * DRED_LATENT_DIM) ||
      !read_exact(activity_mem, sizeof(activity_mem))) {
    fprintf(stderr, "failed to read payload\n");
    return 1;
  }

  output = (unsigned char *)calloc(max_bytes > 0 ? max_bytes : 1, sizeof(unsigned char));
  if (output == NULL) {
    fprintf(stderr, "allocation failure\n");
    return 1;
  }

  payload_bytes = (uint32_t)dred_encode_silk_frame(&enc, output, (int)max_chunks, (int)max_bytes, (int)q0, (int)dQ, (int)qmax, activity_mem, 0);

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32((uint32_t)enc.last_extra_dred_offset) ||
      !write_u32(payload_bytes) || !write_exact(output, payload_bytes)) {
    fprintf(stderr, "failed to write output\n");
    free(output);
    return 1;
  }

  free(output);
  return 0;
}
