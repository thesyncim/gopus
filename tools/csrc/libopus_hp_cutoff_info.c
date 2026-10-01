/* Execute the selected opus_encoder.c hp_cutoff with exact float I/O. */
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif
#include "opus_encoder.c"

static int read_u32(uint32_t *value) {
  unsigned char b[4];
  if (fread(b, 1, sizeof(b), stdin) != sizeof(b)) return 0;
  *value = (uint32_t)b[0] | (uint32_t)b[1] << 8 |
           (uint32_t)b[2] << 16 | (uint32_t)b[3] << 24;
  return 1;
}

static int write_u32(uint32_t value) {
  unsigned char b[4] = {(unsigned char)value, (unsigned char)(value >> 8),
                        (unsigned char)(value >> 16), (unsigned char)(value >> 24)};
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
      memcmp(magic, "GHPI", sizeof(magic)) != 0 ||
      !read_u32(&version) || version != 1 ||
      !read_u32(&count) || count == 0 || count > 128) return 2;
  if (fwrite("GHPO", 1, 4, stdout) != 4 ||
      !write_u32(1) || !write_u32(count)) return 3;

  for (uint32_t case_index = 0; case_index < count; case_index++) {
    uint32_t sample_rate, channels, frame_size, frames;
    opus_val32 mem[4];
    opus_res pcm[1920], output[1920];
    if (!read_u32(&sample_rate) || !read_u32(&channels) ||
        !read_u32(&frame_size) || !read_u32(&frames) ||
        sample_rate < 8000 || sample_rate > 48000 ||
        channels < 1 || channels > 2 || frame_size == 0 ||
        frame_size > 960 || frames == 0 || frames > 32) return 4;
    for (int i = 0; i < 4; i++) if (!read_float(&mem[i])) return 5;
    if (!write_u32(frames)) return 6;
    for (uint32_t frame = 0; frame < frames; frame++) {
      uint32_t cutoff;
      if (!read_u32(&cutoff) || cutoff < 1 || cutoff > 500) return 7;
      for (uint32_t i = 0; i < frame_size * channels; i++) {
        if (!read_float(&pcm[i])) return 8;
      }
      hp_cutoff(pcm, (opus_int32)cutoff, output, mem, (int)frame_size,
                (int)channels, (opus_int32)sample_rate, opus_select_arch());
      if (!write_u32(frame_size * channels)) return 9;
      for (uint32_t i = 0; i < frame_size * channels; i++) {
        if (!write_float(output[i])) return 10;
      }
      for (int i = 0; i < 4; i++) if (!write_float(mem[i])) return 11;
    }
  }
  return ferror(stdin) || ferror(stdout) ? 12 : 0;
}
