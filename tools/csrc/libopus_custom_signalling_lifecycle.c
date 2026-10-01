/* Selected-libopus oracle for custom end-band retention across raw mode/reset. */
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
#include "celt.h"

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

static void write_decode(OpusCustomDecoder *decoder,
                         const unsigned char *packet, int packet_size,
                         float *output) {
  int samples = opus_custom_decode_float(decoder, packet, packet_size,
                                         output, 960);
  opus_uint32 range = 0;
  int status = opus_custom_decoder_ctl(decoder, OPUS_GET_FINAL_RANGE(&range));
  if (status != OPUS_OK || samples < 0) exit(4);
  write_i32(samples);
  write_u32(range);
  for (int i = 0; i < samples; i++) write_f32(output[i]);
}

static OpusCustomDecoder *new_decoder(OpusCustomMode *mode) {
  int error = OPUS_OK;
  OpusCustomDecoder *decoder = opus_custom_decoder_create(mode, 1, &error);
  if (!decoder || error != OPUS_OK) exit(5);
  return decoder;
}

int main(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 2;
#endif
  char magic[4];
  if (fread(magic, 1, 4, stdin) != 4 || memcmp(magic, "GCLF", 4)) return 2;
  uint32_t sample_count = read_u32();
  if (sample_count != 960) return 2;
  float input[960], output[960];
  unsigned char packet[1500], low_band_packet[1500];
  for (uint32_t i = 0; i < sample_count; i++) input[i] = read_f32();

  int error = OPUS_OK;
  OpusCustomMode *mode = opus_custom_mode_create(48000, 960, &error);
  if (!mode || error != OPUS_OK) return 3;
  OpusCustomEncoder *encoder = opus_custom_encoder_create(mode, 1, &error);
  if (!encoder || error != OPUS_OK) return 3;
  int packet_size = opus_custom_encode_float(encoder, input, 960, packet, 200);
  if (packet_size < 2 || packet_size > 200) return 4;
  memcpy(low_band_packet, packet, (size_t)packet_size);
  low_band_packet[0] = 0xd8; /* LM=3 and end band 19 after TOC conversion. */
  write_i32(packet_size);
  if (fwrite(packet, 1, (size_t)packet_size, stdout) != (size_t)packet_size)
    return 5;

  OpusCustomDecoder *raw_decoder = new_decoder(mode);
  write_decode(raw_decoder, low_band_packet, packet_size, output);
  if (opus_custom_decoder_ctl(raw_decoder, CELT_SET_SIGNALLING(0)) != OPUS_OK)
    return 6;
  write_decode(raw_decoder, packet + 1, packet_size - 1, output);
  opus_custom_decoder_destroy(raw_decoder);

  OpusCustomDecoder *reset_decoder = new_decoder(mode);
  write_decode(reset_decoder, low_band_packet, packet_size, output);
  if (opus_custom_decoder_ctl(reset_decoder, OPUS_RESET_STATE) != OPUS_OK)
    return 7;
  write_decode(reset_decoder, NULL, 0, output);
  opus_custom_decoder_destroy(reset_decoder);
  opus_custom_encoder_destroy(encoder);
  opus_custom_mode_destroy(mode);
  return 0;
}
