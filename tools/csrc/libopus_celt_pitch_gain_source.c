#include <stdint.h>
#include <stdio.h>
#include <string.h>

/* Compile the pinned float implementation in celt/pitch.c in this translation
 * unit so this helper calls its actual static compute_pitch_gain function. */
#include "pitch.c"

#define MAX_CASES 64u

static int read_exact(void *dst, size_t size) {
  return fread(dst, 1, size, stdin) == size;
}

static int write_exact(const void *src, size_t size) {
  return fwrite(src, 1, size, stdout) == size;
}

static int read_u32(uint32_t *value) {
  unsigned char bytes[4];
  if (!read_exact(bytes, sizeof(bytes))) return 0;
  *value = (uint32_t)bytes[0] | ((uint32_t)bytes[1] << 8) |
           ((uint32_t)bytes[2] << 16) | ((uint32_t)bytes[3] << 24);
  return 1;
}

static int write_u32(uint32_t value) {
  unsigned char bytes[4];
  bytes[0] = (unsigned char)value;
  bytes[1] = (unsigned char)(value >> 8);
  bytes[2] = (unsigned char)(value >> 16);
  bytes[3] = (unsigned char)(value >> 24);
  return write_exact(bytes, sizeof(bytes));
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
  uint32_t version;
  uint32_t count;
  uint32_t i;

  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, "GCPG", 4) != 0 ||
      !read_u32(&version) || version != 1 || !read_u32(&count) ||
      count == 0 || count > MAX_CASES) {
    return 2;
  }

  if (!write_exact("GPGO", 4) || !write_u32(1) || !write_u32(count)) return 3;
  for (i = 0; i < count; ++i) {
    opus_val32 xy;
    opus_val32 xx;
    opus_val32 yy;
    opus_val16 gain;

    if (!read_float((float *)&xy) || !read_float((float *)&xx) ||
        !read_float((float *)&yy)) {
      return 4;
    }
    gain = compute_pitch_gain(xy, xx, yy);
    if (!write_float((float)gain)) return 5;
  }

  if (fgetc(stdin) != EOF) return 6;
  return 0;
}
