#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "celt/arch.h"
#include "celt/mdct.h"

static const opus_int16 eband5ms[] = {0};
static const unsigned char band_allocation[] = {0};
#include "celt/static_modes_fixed.h"

/* Include the pinned implementation so this probe calls its static gain_fade. */
#include "opus_encoder.c"

#if !defined(FIXED_POINT) || !defined(ENABLE_QEXT) || !defined(ENABLE_RES24)
#error "fixed-QEXT gain fade oracle requires FIXED_POINT, ENABLE_QEXT, and ENABLE_RES24"
#endif

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
  unsigned char b[4];
  if (!read_exact(b, sizeof(b))) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) |
      ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int write_u32(uint32_t value) {
  unsigned char b[4] = {
    (unsigned char)value, (unsigned char)(value >> 8),
    (unsigned char)(value >> 16), (unsigned char)(value >> 24)
  };
  return write_exact(b, sizeof(b));
}

int main(void) {
  unsigned char magic[4];
  uint32_t version, case_count;
  if (!set_binary_stdio() || !read_exact(magic, sizeof(magic)) ||
      memcmp(magic, "GGQI", sizeof(magic)) != 0 ||
      !read_u32(&version) || version != 1 ||
      !read_u32(&case_count) || case_count == 0 || case_count > 64) return 1;

  if (!write_exact("GGQO", 4) || !write_u32(1) || !write_u32(case_count)) return 1;
  for (uint32_t case_idx = 0; case_idx < case_count; case_idx++) {
    uint32_t sample_rate, channels, frame_size, raw_g1, raw_g2, sample_count;
    if (!read_u32(&sample_rate) || !read_u32(&channels) ||
        !read_u32(&frame_size) || !read_u32(&raw_g1) || !read_u32(&raw_g2) ||
        !read_u32(&sample_count)) return 1;
    if ((sample_rate != 8000 && sample_rate != 12000 && sample_rate != 16000 &&
         sample_rate != 24000 && sample_rate != 48000 && sample_rate != 96000) ||
        (channels != 1 && channels != 2) || raw_g1 > 32767 || raw_g2 > 32767 ||
        frame_size == 0 || frame_size > 3840 ||
        sample_count != frame_size * channels) return 1;

    const CELTMode *mode = sample_rate == 96000 ?
        &mode96000_1920_240 : &mode48000_960_120;
    int inc = 48000 / (int)sample_rate;
    if (inc < 1) inc = 1;
    int overlap = mode->overlap / inc;
    if (frame_size < (uint32_t)overlap) return 1;

    opus_res *samples = (opus_res *)malloc((size_t)sample_count * sizeof(*samples));
    if (samples == NULL) return 1;
    for (uint32_t i = 0; i < sample_count; i++) {
      uint32_t bits;
      if (!read_u32(&bits)) {
        free(samples);
        return 1;
      }
      memcpy(&samples[i], &bits, sizeof(bits));
    }

    gain_fade(samples, samples, (opus_val16)raw_g1, (opus_val16)raw_g2,
        mode->overlap, (int)frame_size, (int)channels, mode->window,
        (opus_int32)sample_rate);

    if (!write_u32(sample_count)) {
      free(samples);
      return 1;
    }
    for (uint32_t i = 0; i < sample_count; i++) {
      uint32_t bits;
      memcpy(&bits, &samples[i], sizeof(bits));
      if (!write_u32(bits)) {
        free(samples);
        return 1;
      }
    }
    free(samples);
  }
  return fflush(stdout) == 0 ? 0 : 1;
}
