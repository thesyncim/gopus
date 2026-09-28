/* Finite CBR/VBR Opus Custom signalling oracle. */
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

static uint32_t read_u32(void) {
  unsigned char b[4];
  if (fread(b, 1, sizeof(b), stdin) != sizeof(b)) exit(2);
  return (uint32_t)b[0] | (uint32_t)b[1] << 8 |
         (uint32_t)b[2] << 16 | (uint32_t)b[3] << 24;
}

static void write_u32(uint32_t value) {
  unsigned char b[4] = {
      (unsigned char)value, (unsigned char)(value >> 8),
      (unsigned char)(value >> 16), (unsigned char)(value >> 24)};
  if (fwrite(b, 1, sizeof(b), stdout) != sizeof(b)) exit(3);
}

static void write_i32(int32_t value) { write_u32((uint32_t)value); }

static float read_f32(void) {
  uint32_t bits = read_u32();
  float value;
  memcpy(&value, &bits, sizeof(value));
  return value;
}

static void check_status(int status) {
  if (status != OPUS_OK) {
    fprintf(stderr, "custom finite-rate control: %d\n", status);
    exit(4);
  }
}

int main(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 2;
#endif
  char magic[4];
  if (fread(magic, 1, sizeof(magic), stdin) != sizeof(magic) ||
      memcmp(magic, "GCSR", sizeof(magic))) return 2;
  uint32_t case_count = read_u32();
  if (case_count == 0 || case_count > 64) return 2;
  if (fwrite("GCSR", 1, sizeof(magic), stdout) != sizeof(magic)) return 3;
  write_u32(case_count);

  for (uint32_t i = 0; i < case_count; i++) {
    uint32_t fs = read_u32();
    uint32_t frame_size = read_u32();
    uint32_t channels = read_u32();
    uint32_t max_bytes = read_u32();
    uint32_t bitrate = read_u32();
    uint32_t vbr = read_u32();
    uint32_t cvbr = read_u32();
    uint32_t frame_count = read_u32();
    if (frame_size == 0 || frame_size > 2048 || channels < 1 || channels > 2 ||
        max_bytes < 2 || max_bytes > 1500 || bitrate <= 500 ||
        vbr > 1 || cvbr > 1 || frame_count == 0 || frame_count > 64) return 2;

    int error = OPUS_OK;
    OpusCustomMode *mode = opus_custom_mode_create((opus_int32)fs,
                                                    (int)frame_size, &error);
    if (!mode || error != OPUS_OK) return 4;
    OpusCustomEncoder *encoder = opus_custom_encoder_create(mode, (int)channels,
                                                            &error);
    if (!encoder || error != OPUS_OK) return 4;
    check_status(opus_custom_encoder_ctl(encoder,
                                         OPUS_SET_BITRATE((opus_int32)bitrate)));
    check_status(opus_custom_encoder_ctl(encoder, OPUS_SET_VBR((int)vbr)));
    check_status(opus_custom_encoder_ctl(encoder,
                                         OPUS_SET_VBR_CONSTRAINT((int)cvbr)));

    write_u32(frame_count);
    for (uint32_t frame = 0; frame < frame_count; frame++) {
      float input[4096];
      unsigned char packet[1500];
      for (uint32_t k = 0; k < frame_size * channels; k++) input[k] = read_f32();
      int packet_size = opus_custom_encode_float(encoder, input,
                                                 (int)frame_size, packet,
                                                 (int)max_bytes);
      if (packet_size < 0 || packet_size > (int)max_bytes) return 5;
      opus_uint32 final_range = 0;
      check_status(opus_custom_encoder_ctl(encoder,
                                           OPUS_GET_FINAL_RANGE(&final_range)));
      write_i32(packet_size);
      write_u32(final_range);
      if (fwrite(packet, 1, (size_t)packet_size, stdout) != (size_t)packet_size)
        return 3;
    }
    opus_custom_encoder_destroy(encoder);
    opus_custom_mode_destroy(mode);
  }
  return 0;
}
