/* Selected libopus 1.6.1 tone_lpc oracle. The pinned encoder translation unit
 * supplies the actual static tone_lpc implementation and float macros. */
#include <stdint.h>
#include <stdio.h>
#include <string.h>

#include "celt_encoder.c"

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

static uint32_t float_bits(float value) {
  uint32_t bits;
  memcpy(&bits, &value, sizeof(bits));
  return bits;
}

int main(void) {
  unsigned char magic[4];
  uint32_t version, count;
  if (fread(magic, 1, sizeof(magic), stdin) != sizeof(magic) ||
      memcmp(magic, "GTLC", sizeof(magic)) != 0 ||
      !read_u32(&version) || version != 2 || !read_u32(&count) || count > 256)
    return 2;
  if (fwrite("GTLC", 1, 4, stdout) != 4 ||
      !write_u32(2) || !write_u32(count))
    return 3;

  for (uint32_t case_index = 0; case_index < count; case_index++) {
    uint32_t length, delay, bits;
    float x[4096];
    if (!read_u32(&length) || !read_u32(&delay) || length > 4096 ||
        delay == 0 || delay > 256 || length <= 2 * delay)
      return 4;
    for (uint32_t i = 0; i < length; i++) {
      if (!read_u32(&bits)) return 5;
      memcpy(&x[i], &bits, sizeof(bits));
    }

    /* The compiled tone_lpc loop and solve supply the selected-C reference.
     * A copied correlation loop could compile to a different reduction. */
    opus_val32 lpc[2] = {0, 0};
    int fail = tone_lpc(x, (int)length, (int)delay, lpc);
    opus_val32 toneishness = 0;
    opus_val16 freq = 0;
    if (length > 64)
      freq = tone_detect(x, 1, (int)length, &toneishness, 48000);
    if (!write_u32((uint32_t)fail) ||
        !write_u32(float_bits(lpc[0])) || !write_u32(float_bits(lpc[1])) ||
        !write_u32(float_bits(freq)) || !write_u32(float_bits(toneishness)))
      return 6;
  }
  return ferror(stdin) || ferror(stdout) ? 7 : 0;
}
