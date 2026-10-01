/* Exercise the pinned opus_encoder.c stereo_fade() with its mode window. */
#include <stdint.h>
#include <stdio.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "opus_encoder.c"
#include "modes.h"

static int read_u32(uint32_t *v) {
  unsigned char b[4];
  if (fread(b, 1, sizeof(b), stdin) != sizeof(b)) return 0;
  *v = (uint32_t)b[0] | (uint32_t)b[1] << 8 |
       (uint32_t)b[2] << 16 | (uint32_t)b[3] << 24;
  return 1;
}

static int write_u32(uint32_t v) {
  unsigned char b[4] = {(unsigned char)v, (unsigned char)(v >> 8),
                        (unsigned char)(v >> 16), (unsigned char)(v >> 24)};
  return fwrite(b, 1, sizeof(b), stdout) == sizeof(b);
}

int main(void) {
  unsigned char magic[4];
  uint32_t version, count;
  int mode_error = OPUS_OK;
  CELTMode *mode;

#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 1;
#endif
  if (fread(magic, 1, sizeof(magic), stdin) != sizeof(magic) ||
      memcmp(magic, "GTFI", sizeof(magic)) != 0 ||
      !read_u32(&version) || version != 1 ||
      !read_u32(&count) || count == 0 || count > 128) return 2;

  mode = opus_custom_mode_create(48000, 960, &mode_error);
  if (mode == NULL || mode_error != OPUS_OK) return 3;
  if (fwrite("GTFO", 1, 4, stdout) != 4 ||
      !write_u32(1) || !write_u32(count)) return 4;

  for (uint32_t case_index = 0; case_index < count; case_index++) {
    uint32_t sample_rate, frame_size, width_prev, width_next, sample_count;
    opus_res samples[3840];
    if (!read_u32(&sample_rate) || !read_u32(&frame_size) ||
        !read_u32(&width_prev) || !read_u32(&width_next) ||
        !read_u32(&sample_count) ||
        (sample_rate != 24000 && sample_rate != 48000) ||
        frame_size < (uint32_t)(mode->overlap / (48000 / (int)sample_rate)) ||
        frame_size > 1920 || sample_count != 2 * frame_size ||
        width_prev > 16384 || width_next > 16384) return 5;
    for (uint32_t i = 0; i < sample_count; i++) {
      uint32_t bits;
      if (!read_u32(&bits)) return 6;
      memcpy(&samples[i], &bits, sizeof(bits));
    }

    opus_val16 g1 = (opus_val16)width_prev * (1.f / 16384);
    opus_val16 g2 = (opus_val16)width_next * (1.f / 16384);
    stereo_fade(samples, samples, g1, g2, mode->overlap,
                (int)frame_size, 2, mode->window, (opus_int32)sample_rate);

    if (!write_u32(sample_count)) return 7;
    for (uint32_t i = 0; i < sample_count; i++) {
      uint32_t bits;
      memcpy(&bits, &samples[i], sizeof(bits));
      if (!write_u32(bits)) return 8;
    }
  }
  return ferror(stdin) || ferror(stdout) ? 9 : 0;
}
