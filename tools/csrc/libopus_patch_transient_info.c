/* Execute the selected celt_encoder.c transient patch with mode-strided history. */
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif
#include "celt_encoder.c"

static int read_u32(uint32_t *v) {
  unsigned char b[4];
  if (fread(b, 1, 4, stdin) != 4) return 0;
  *v = (uint32_t)b[0] | (uint32_t)b[1] << 8 |
       (uint32_t)b[2] << 16 | (uint32_t)b[3] << 24;
  return 1;
}
static int write_u32(uint32_t v) {
  unsigned char b[4] = {(unsigned char)v, (unsigned char)(v >> 8),
      (unsigned char)(v >> 16), (unsigned char)(v >> 24)};
  return fwrite(b, 1, 4, stdout) == 4;
}
static int read_float(float *v) {
  uint32_t bits;
  if (!read_u32(&bits)) return 0;
  memcpy(v, &bits, 4);
  return 1;
}
int main(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 1;
#endif
  unsigned char magic[4];
  uint32_t version, count;
  if (fread(magic, 1, 4, stdin) != 4 || memcmp(magic, "GPTI", 4) ||
      !read_u32(&version) || version != 1 || !read_u32(&count) || count > 128) return 2;
  if (fwrite("GPTO", 1, 4, stdout) != 4 || !write_u32(1) || !write_u32(count)) return 3;
  for (uint32_t test = 0; test < count; test++) {
    uint32_t stride, start, end, channels;
    float current[42], history[42];
    if (!read_u32(&stride) || !read_u32(&start) || !read_u32(&end) ||
        !read_u32(&channels) || stride > 21 || end > stride || start >= end ||
        channels < 1 || channels > 2 || end <= 3) return 4;
    for (uint32_t i = 0; i < stride * channels; i++) if (!read_float(&current[i])) return 5;
    for (uint32_t i = 0; i < stride * channels; i++) if (!read_float(&history[i])) return 6;
    if (!write_u32((uint32_t)patch_transient_decision(current, history,
        (int)stride, (int)start, (int)end, (int)channels))) return 7;
  }
  return ferror(stdin) || ferror(stdout) ? 8 : 0;
}
