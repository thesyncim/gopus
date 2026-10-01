/* Execute the selected opus_encoder.c stereo-width estimator and state. */
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif
#include "opus_encoder.c"

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


static int read_float(float *value) {
  uint32_t bits;
  if (!read_u32(&bits)) return 0;
  memcpy(value, &bits, sizeof(bits));
  return 1;
}

static int write_float(float value) {
  uint32_t bits;
  memcpy(&bits, &value, sizeof(bits));
  return write_u32(bits);
}

int main(void) {
  unsigned char magic[4];
  uint32_t version, count;
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 1;
#endif
  if (fread(magic, 1, sizeof(magic), stdin) != sizeof(magic) ||
      memcmp(magic, "GSWI", sizeof(magic)) != 0 ||
      !read_u32(&version) || version != 1 ||
      !read_u32(&count) || count == 0 || count > 64) return 2;
  if (fwrite("GSWO", 1, 4, stdout) != 4 ||
      !write_u32(1) || !write_u32(count)) return 3;

  for (uint32_t case_index = 0; case_index < count; case_index++) {
    uint32_t sample_rate, frame_size, frames;
    StereoWidthState mem;
    opus_res pcm[11520];
    if (!read_u32(&sample_rate) || !read_u32(&frame_size) ||
        !read_u32(&frames) || sample_rate == 0 ||
        frame_size < 4 || frame_size > 5760 ||
        frames == 0 || frames > 32) return 4;
    if (!read_float(&mem.XX) || !read_float(&mem.XY) || !read_float(&mem.YY) ||
        !read_float(&mem.smoothed_width) || !read_float(&mem.max_follower)) return 5;
    if (!write_u32(frames)) return 6;

    for (uint32_t frame = 0; frame < frames; frame++) {
      for (uint32_t i = 0; i < 2 * frame_size; i++) {
        if (!read_float(&pcm[i])) return 7;
      }
      float width = compute_stereo_width(pcm, (int)frame_size,
                                         (opus_int32)sample_rate, &mem);
      if (!write_float(width) || !write_float(mem.XX) ||
          !write_float(mem.XY) || !write_float(mem.YY) ||
          !write_float(mem.smoothed_width) || !write_float(mem.max_follower)) return 8;
    }
  }
  return ferror(stdin) || ferror(stdout) ? 9 : 0;
}
