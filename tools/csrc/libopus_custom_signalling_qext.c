/* Selected-libopus QEXT packet and custom decoder oracle. */
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
  if (fread(b, 1, 4, stdin) != 4) exit(2);
  return (uint32_t)b[0] | (uint32_t)b[1] << 8 |
         (uint32_t)b[2] << 16 | (uint32_t)b[3] << 24;
}

static void write_u32(uint32_t v) {
  unsigned char b[4] = {
      (unsigned char)v, (unsigned char)(v >> 8),
      (unsigned char)(v >> 16), (unsigned char)(v >> 24)};
  if (fwrite(b, 1, 4, stdout) != 4) exit(3);
}

static void write_i32(int32_t v) { write_u32((uint32_t)v); }

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
    fprintf(stderr, "custom QEXT signalling oracle: %d\n", status);
    exit(4);
  }
}

int main(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 2;
#endif
  char magic[4];
  if (fread(magic, 1, 4, stdin) != 4 || memcmp(magic, "GCQX", 4)) return 2;
  uint32_t fs = read_u32();
  uint32_t frame_size = read_u32();
  uint32_t channels = read_u32();
  uint32_t max_bytes = read_u32();
  uint32_t sample_count = read_u32();
  if (frame_size == 0 || frame_size > 2048 || channels < 1 || channels > 2 ||
      max_bytes < 2 || max_bytes > 4096 ||
      sample_count != frame_size * channels) return 2;

  float input[4096], output[4096];
  unsigned char packet[4096];
  for (uint32_t i = 0; i < sample_count; i++) input[i] = read_f32();

  int error = OPUS_OK;
  OpusCustomMode *mode = opus_custom_mode_create((opus_int32)fs,
                                                  (int)frame_size, &error);
  if (!mode || error != OPUS_OK) return 4;
  OpusCustomEncoder *encoder = opus_custom_encoder_create(mode, (int)channels,
                                                          &error);
  if (!encoder || error != OPUS_OK) return 4;
  OpusCustomDecoder *decoder = opus_custom_decoder_create(mode, (int)channels,
                                                          &error);
  if (!decoder || error != OPUS_OK) return 4;
  check_status(opus_custom_encoder_ctl(encoder, OPUS_SET_QEXT(1)));

  int packet_size = opus_custom_encode_float(encoder, input, (int)frame_size,
                                              packet, (int)max_bytes);
  if (packet_size < 0 || packet_size > (int)max_bytes) return 5;
  opus_uint32 enc_range = 0;
  check_status(opus_custom_encoder_ctl(encoder,
                                      OPUS_GET_FINAL_RANGE(&enc_range)));
  int samples = opus_custom_decode_float(decoder, packet, packet_size, output,
                                          (int)frame_size);
  if (samples < 0) return 6;
  opus_uint32 dec_range = 0;
  check_status(opus_custom_decoder_ctl(decoder,
                                       OPUS_GET_FINAL_RANGE(&dec_range)));

  if (fwrite("GCQX", 1, 4, stdout) != 4) return 3;
  write_i32(packet_size);
  write_u32(enc_range);
  write_i32(samples);
  write_u32(dec_range);
  if (fwrite(packet, 1, (size_t)packet_size, stdout) != (size_t)packet_size)
    return 3;
  for (int i = 0; i < samples * (int)channels; i++) write_f32(output[i]);

  opus_custom_decoder_destroy(decoder);
  opus_custom_encoder_destroy(encoder);
  opus_custom_mode_destroy(mode);
  return 0;
}
