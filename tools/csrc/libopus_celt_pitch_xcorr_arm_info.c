/* Selected libopus 1.6.1 ARM float pitch cross-correlation oracle. */
#include <stdint.h>
#include <stdio.h>
#include <string.h>

#include "config.h"
#include "pitch.h"
#include "cpu_support.h"

static int read_u32(uint32_t *value) {
  unsigned char bytes[4];
  if (fread(bytes, 1, sizeof(bytes), stdin) != sizeof(bytes)) return 0;
  *value = (uint32_t)bytes[0] | (uint32_t)bytes[1] << 8 |
           (uint32_t)bytes[2] << 16 | (uint32_t)bytes[3] << 24;
  return 1;
}

static int write_u32(uint32_t value) {
  unsigned char bytes[4] = {
      (unsigned char)value, (unsigned char)(value >> 8),
      (unsigned char)(value >> 16), (unsigned char)(value >> 24)};
  return fwrite(bytes, 1, sizeof(bytes), stdout) == sizeof(bytes);
}

int main(void) {
  unsigned char magic[4];
  uint32_t version, count;
  if (fread(magic, 1, sizeof(magic), stdin) != sizeof(magic) ||
      memcmp(magic, "GXCI", sizeof(magic)) != 0 ||
      !read_u32(&version) || version != 1 ||
      !read_u32(&count) || count > 256)
    return 2;

  uint32_t selected_neon = 0;
#if defined(OPUS_ARM_PRESUME_NEON_INTR) && !defined(FIXED_POINT)
  selected_neon = 1;
#endif
  if (fwrite("GXAO", 1, 4, stdout) != 4 ||
      !write_u32(1) || !write_u32(selected_neon) || !write_u32(count))
    return 3;

  for (uint32_t case_index = 0; case_index < count; case_index++) {
    uint32_t length, max_pitch, bits;
    float x[1024], y[2048], out[1024];
    if (!read_u32(&length) || !read_u32(&max_pitch) ||
        length == 0 || length > 1024 || max_pitch == 0 || max_pitch > 1024)
      return 4;
    for (uint32_t i = 0; i < length; i++) {
      if (!read_u32(&bits)) return 5;
      memcpy(&x[i], &bits, sizeof(bits));
    }
    for (uint32_t i = 0; i < length + max_pitch - 1; i++) {
      if (!read_u32(&bits)) return 6;
      memcpy(&y[i], &bits, sizeof(bits));
    }
    celt_pitch_xcorr(x, y, out, (int)length, (int)max_pitch,
                     opus_select_arch());
    if (!write_u32(max_pitch)) return 7;
    for (uint32_t i = 0; i < max_pitch; i++) {
      memcpy(&bits, &out[i], sizeof(bits));
      if (!write_u32(bits)) return 8;
    }
  }
  return ferror(stdin) || ferror(stdout) ? 9 : 0;
}
