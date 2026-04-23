#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "dred_encoder.h"
#include "dred_encoder.c"

#define INPUT_MAGIC "GDCI"
#define OUTPUT_MAGIC "GDCO"

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
    if (!write_u32(bits)) return 0;
  }
  return 1;
}

int main(void) {
  char magic[4];
  uint32_t version;
  uint32_t sample_rate;
  uint32_t channels;
  uint32_t frame_samples;
  float *input = NULL;
  float *output = NULL;
  uint32_t output_samples;
  DREDEnc enc;

  if (!set_binary_stdio()) {
    fprintf(stderr, "failed to set binary stdio mode\n");
    return 1;
  }
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) {
    fprintf(stderr, "invalid input magic\n");
    return 1;
  }
  if (!read_u32(&version) || version != 1 || !read_u32(&sample_rate) || !read_u32(&channels) ||
      !read_u32(&frame_samples)) {
    fprintf(stderr, "failed to read header\n");
    return 1;
  }
  if (!(channels == 1 || channels == 2)) {
    fprintf(stderr, "invalid channels\n");
    return 1;
  }
  output_samples = frame_samples * 16000u / sample_rate;
  if (output_samples == 0 || output_samples * sample_rate != frame_samples * 16000u) {
    fprintf(stderr, "unsupported frame/sample-rate combination\n");
    return 1;
  }

  input = (float *)calloc((size_t)channels * frame_samples, sizeof(float));
  output = (float *)calloc(output_samples, sizeof(float));
  if (input == NULL || output == NULL) {
    fprintf(stderr, "allocation failure\n");
    free(input);
    free(output);
    return 1;
  }

  dred_encoder_init(&enc, (opus_int32)sample_rate, (int)channels);
  if (!read_bits_array(enc.resample_mem, RESAMPLING_ORDER + 1) ||
      !read_bits_array(input, (int)(channels * frame_samples))) {
    fprintf(stderr, "failed to read payload\n");
    free(input);
    free(output);
    return 1;
  }

  dred_convert_to_16k(&enc, input, (int)frame_samples, output, (int)output_samples);

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(output_samples) ||
      !write_bits_array(output, (int)output_samples) ||
      !write_bits_array(enc.resample_mem, RESAMPLING_ORDER + 1)) {
    fprintf(stderr, "failed to write output\n");
    free(input);
    free(output);
    return 1;
  }

  free(input);
  free(output);
  return 0;
}
