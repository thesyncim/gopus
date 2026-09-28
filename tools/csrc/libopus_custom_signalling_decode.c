/* Stateless decoder oracle for custom header LM, end-band, and padding cases. */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "config.h"
#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif
#include "opus_custom.h"
#include "opus_defines.h"

static int read_exact(void *dst, size_t size) {
  return fread(dst, 1, size, stdin) == size;
}

static int write_exact(const void *src, size_t size) {
  return fwrite(src, 1, size, stdout) == size;
}

static uint32_t read_u32(void) {
  unsigned char bytes[4];
  if (!read_exact(bytes, sizeof(bytes))) exit(2);
  return (uint32_t)bytes[0] | (uint32_t)bytes[1] << 8 |
         (uint32_t)bytes[2] << 16 | (uint32_t)bytes[3] << 24;
}

static void write_u32(uint32_t value) {
  unsigned char bytes[4] = {
      (unsigned char)value, (unsigned char)(value >> 8),
      (unsigned char)(value >> 16), (unsigned char)(value >> 24)};
  if (!write_exact(bytes, sizeof(bytes))) exit(3);
}

static void write_i32(int32_t value) { write_u32((uint32_t)value); }

static void write_f32(float value) {
  uint32_t bits;
  memcpy(&bits, &value, sizeof(bits));
  write_u32(bits);
}

int main(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 2;
#endif
  char magic[4];
  uint32_t count;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, "GCSD", 4)) return 1;
  count = read_u32();
  if (count > 64 || !write_exact("GCSD", 4)) return 2;
  write_u32(count);

  for (uint32_t i = 0; i < count; i++) {
    uint32_t fs = read_u32();
    uint32_t mode_frame = read_u32();
    uint32_t channels = read_u32();
    uint32_t output_capacity = read_u32();
    uint32_t packet_len = read_u32();
    unsigned char packet[5000];
    float pcm[4096];
    if (mode_frame == 0 || mode_frame > 2048 || channels < 1 || channels > 2 ||
        output_capacity == 0 || output_capacity > 2048 || packet_len == 0 ||
        packet_len > sizeof(packet) || !read_exact(packet, packet_len)) return 3;

    int error = OPUS_OK;
    OpusCustomMode *mode = opus_custom_mode_create((opus_int32)fs,
                                                    (int)mode_frame, &error);
    if (!mode || error != OPUS_OK) return 4;
    OpusCustomDecoder *decoder = opus_custom_decoder_create(mode, (int)channels,
                                                             &error);
    if (!decoder || error != OPUS_OK) return 5;
    int samples = opus_custom_decode_float(decoder, packet, (int)packet_len,
                                            pcm, (int)output_capacity);
    opus_uint32 range = 0;
    int ctl = opus_custom_decoder_ctl(decoder, OPUS_GET_FINAL_RANGE(&range));
    if (samples < 0 || ctl != OPUS_OK) return 6;
    write_i32(samples);
    write_u32(range);
    if (samples > 0) {
      for (int sample = 0; sample < samples * (int)channels; sample++)
        write_f32(pcm[sample]);
    }
    opus_custom_decoder_destroy(decoder);
    opus_custom_mode_destroy(mode);
  }
  return 0;
}
