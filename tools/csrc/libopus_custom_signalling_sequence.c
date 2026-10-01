/* Stateful custom signalling oracle for coded/output channel changes and PLC. */
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
  unsigned char bytes[4];
  if (fread(bytes, 1, sizeof(bytes), stdin) != sizeof(bytes)) exit(2);
  return (uint32_t)bytes[0] | (uint32_t)bytes[1] << 8 |
         (uint32_t)bytes[2] << 16 | (uint32_t)bytes[3] << 24;
}

static void write_u32(uint32_t value) {
  unsigned char bytes[4] = {
      (unsigned char)value, (unsigned char)(value >> 8),
      (unsigned char)(value >> 16), (unsigned char)(value >> 24)};
  if (fwrite(bytes, 1, sizeof(bytes), stdout) != sizeof(bytes)) exit(3);
}

static void write_i32(int32_t value) { write_u32((uint32_t)value); }

static float read_f32(void) {
  uint32_t bits = read_u32();
  float value;
  memcpy(&value, &bits, sizeof(value));
  return value;
}

static void write_f32(float value) {
  uint32_t bits;
  memcpy(&bits, &value, sizeof(bits));
  write_u32(bits);
}

static void check_status(int status) {
  if (status != OPUS_OK) {
    fprintf(stderr, "custom signalling sequence control: %d\n", status);
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
      memcmp(magic, "GCSS", sizeof(magic))) return 2;
  uint32_t case_count = read_u32();
  if (case_count > 64) return 2;
  if (fwrite("GCSS", 1, 4, stdout) != 4) return 3;
  write_u32(case_count);

  for (uint32_t i = 0; i < case_count; i++) {
    uint32_t fs = read_u32();
    uint32_t frame_size = read_u32();
    uint32_t output_channels = read_u32();
    uint32_t record_count = read_u32();
    if (frame_size == 0 || frame_size > 2048 || output_channels < 1 ||
        output_channels > 2 || record_count == 0 || record_count > 64) return 2;

    int error = OPUS_OK;
    OpusCustomMode *mode = opus_custom_mode_create((opus_int32)fs,
                                                    (int)frame_size, &error);
    if (!mode || error != OPUS_OK) return 4;
    OpusCustomEncoder *encoders[2] = {NULL, NULL};
    for (int channels = 1; channels <= 2; channels++) {
      encoders[channels - 1] = opus_custom_encoder_create(mode, channels, &error);
      if (!encoders[channels - 1] || error != OPUS_OK) return 4;
      /* Go uses the opus_custom_encoder_create constructor defaults here. */
    }
    OpusCustomDecoder *decoder = opus_custom_decoder_create(mode,
                                                            (int)output_channels,
                                                            &error);
    if (!decoder || error != OPUS_OK) return 4;
    write_u32(record_count);

    for (uint32_t j = 0; j < record_count; j++) {
      uint32_t op = read_u32();
      uint32_t coded_channels = read_u32();
      uint32_t max_bytes = read_u32();
      if (op > 1 || max_bytes == 0 || max_bytes > 1500 ||
          (op == 0 && (coded_channels < 1 || coded_channels > 2))) return 2;
      float input[4096], output[4096];
      unsigned char packet[1500];
      if (op == 0) {
        for (uint32_t k = 0; k < frame_size * coded_channels; k++)
          input[k] = read_f32();
      }

      int packet_size = 0;
      opus_uint32 enc_range = 0;
      if (op == 0) {
        packet_size = opus_custom_encode_float(encoders[coded_channels - 1],
                                               input, (int)frame_size, packet,
                                               (int)max_bytes);
        if (packet_size < 0 || packet_size > (int)max_bytes) return 5;
        check_status(opus_custom_encoder_ctl(encoders[coded_channels - 1],
                                             OPUS_GET_FINAL_RANGE(&enc_range)));
      }
      write_i32(packet_size);
      write_u32(enc_range);
      if (packet_size > 0) fwrite(packet, 1, (size_t)packet_size, stdout);

      int samples = opus_custom_decode_float(decoder,
                                             op == 1 ? NULL : packet,
                                             packet_size, output,
                                             (int)frame_size);
      if (samples < 0) return 6;
      opus_uint32 dec_range = 0;
      check_status(opus_custom_decoder_ctl(decoder, OPUS_GET_FINAL_RANGE(&dec_range)));
      write_i32(samples);
      write_u32(dec_range);
      for (int k = 0; k < samples * (int)output_channels; k++)
        write_f32(output[k]);
    }
    opus_custom_decoder_destroy(decoder);
    opus_custom_encoder_destroy(encoders[0]);
    opus_custom_encoder_destroy(encoders[1]);
    opus_custom_mode_destroy(mode);
  }
  return 0;
}
