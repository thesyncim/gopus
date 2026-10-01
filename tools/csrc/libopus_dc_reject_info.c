/* Execute the selected float opus_encoder.c dc_reject with exact float I/O. */
#include <stdint.h>
#include <stdio.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "opus_encoder.c"

#ifdef FIXED_POINT
#error "float dc_reject oracle requires the float libopus build"
#endif

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
  memcpy(value, &bits, sizeof(*value));
  return 1;
}

static int write_float(float value) {
  uint32_t bits;
  memcpy(&bits, &value, sizeof(bits));
  return write_u32(bits);
}

static int valid_sample_rate(uint32_t fs) {
  return fs == 8000 || fs == 12000 || fs == 16000 || fs == 24000 || fs == 48000;
}

int main(void) {
  unsigned char magic[4];
  uint32_t version;
  uint32_t case_count;
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 1;
#endif
  if (fread(magic, 1, sizeof(magic), stdin) != sizeof(magic) ||
      memcmp(magic, "GDRI", sizeof(magic)) != 0 ||
      !read_u32(&version) || version != 1 || !read_u32(&case_count) ||
      case_count == 0 || case_count > 32) return 2;
  if (fwrite("GDRO", 1, 4, stdout) != 4 || !write_u32(1) ||
      !write_u32(case_count)) return 3;

  for (uint32_t case_index = 0; case_index < case_count; case_index++) {
    uint32_t fs, channels, frame_size, frames;
    uint32_t sample_count;
    opus_val32 hp_mem[4];
    opus_res pcm[1920], output[1920];
    if (!read_u32(&fs) || !read_u32(&channels) || !read_u32(&frame_size) ||
        !read_u32(&frames) || !valid_sample_rate(fs) ||
        (channels != 1 && channels != 2) || frame_size == 0 ||
        frame_size != fs / 50 || frames == 0 || frames > 32) return 4;
    sample_count = frame_size * channels;
    if (sample_count > sizeof(pcm) / sizeof(pcm[0])) return 5;
    for (int i = 0; i < 4; i++) {
      if (!read_float(&hp_mem[i])) return 6;
    }
    if (!write_u32(frames)) return 7;
    for (uint32_t frame = 0; frame < frames; frame++) {
      for (uint32_t i = 0; i < sample_count; i++) {
        if (!read_float(&pcm[i])) return 8;
      }
      /* opus_encoder.c:2008: non-VoIP input uses the literal 3 Hz cutoff. */
      dc_reject(pcm, 3, output, hp_mem, (int)frame_size, (int)channels, (opus_int32)fs);
      if (!write_u32(sample_count)) return 9;
      for (uint32_t i = 0; i < sample_count; i++) {
        if (!write_float(output[i])) return 10;
      }
      for (int i = 0; i < 4; i++) {
        if (!write_float(hp_mem[i])) return 11;
      }
    }
  }
  return ferror(stdin) || ferror(stdout) ? 12 : 0;
}
