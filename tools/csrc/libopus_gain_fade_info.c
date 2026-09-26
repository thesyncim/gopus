#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

/* Include the pinned implementation so this probe calls its static gain_fade. */
#include "opus_encoder.c"
#include "modes.h"

#define INPUT_MAGIC "GGFI"
#define OUTPUT_MAGIC "GGFO"

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

static int read_f32(float *out) {
  uint32_t bits;
  if (!read_u32(&bits)) return 0;
  memcpy(out, &bits, sizeof(bits));
  return 1;
}

static int write_f32(float value) {
  uint32_t bits;
  memcpy(&bits, &value, sizeof(bits));
  return write_u32(bits);
}

int main(void) {
  char magic[4];
  uint32_t version;
  uint32_t count;
  uint32_t case_idx;
  int mode_error = OPUS_OK;
  CELTMode *mode;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) return 1;
  if (!read_u32(&version) || version != 1 || !read_u32(&count)) return 1;

  mode = opus_custom_mode_create(48000, 960, &mode_error);
  if (mode == NULL || mode_error != OPUS_OK) return 1;

  if (!write_exact(OUTPUT_MAGIC, sizeof(magic)) || !write_u32(1) || !write_u32(count)) return 1;
  for (case_idx = 0; case_idx < count; case_idx++) {
    uint32_t sample_rate;
    uint32_t channels;
    uint32_t frame_size;
    uint32_t sample_count;
    uint32_t i;
    float g1;
    float g2;
    opus_res *samples;

    if (!read_u32(&sample_rate) || !read_u32(&channels) || !read_u32(&frame_size) ||
        !read_f32(&g1) || !read_f32(&g2) || !read_u32(&sample_count)) return 1;
    if ((sample_rate != 24000 && sample_rate != 48000) ||
        (channels != 1 && channels != 2)) return 1;
    if (frame_size < (uint32_t)(mode->overlap / (48000 / (int)sample_rate))) return 1;
    if (sample_count != frame_size * channels) return 1;

    samples = (opus_res *)malloc(sizeof(*samples) * sample_count);
    if (samples == NULL) return 1;
    for (i = 0; i < sample_count; i++) {
      float value;
      if (!read_f32(&value)) {
        free(samples);
        return 1;
      }
      samples[i] = (opus_res)value;
    }

    gain_fade(samples, samples, (opus_val16)g1, (opus_val16)g2,
        mode->overlap, (int)frame_size, (int)channels, mode->window, (opus_int32)sample_rate);

    if (!write_u32(sample_count)) {
      free(samples);
      return 1;
    }
    for (i = 0; i < sample_count; i++) {
      if (!write_f32((float)samples[i])) {
        free(samples);
        return 1;
      }
    }
    free(samples);
  }

  return fflush(stdout) == 0 ? 0 : 1;
}
