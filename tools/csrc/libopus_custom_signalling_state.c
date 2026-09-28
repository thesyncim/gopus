/* Stateful custom signalling oracle for end-band commits around errors. */
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

static void write_decode(OpusCustomDecoder *decoder,
                         const unsigned char *packet, int packet_size,
                         float *output, int output_capacity) {
  int samples = opus_custom_decode_float(decoder, packet, packet_size,
                                         output, output_capacity);
  opus_uint32 range = 0;
  int status = opus_custom_decoder_ctl(decoder, OPUS_GET_FINAL_RANGE(&range));
  if (status != OPUS_OK) exit(4);
  write_i32(samples);
  write_u32(range);
  if (samples > 0) {
    for (int i = 0; i < samples; i++) write_f32(output[i]);
  }
}

static void run_scenario(OpusCustomMode *mode, int scenario,
                         const unsigned char *base, int base_size) {
  int error = OPUS_OK;
  OpusCustomDecoder *decoder = opus_custom_decoder_create(mode, 1, &error);
  if (!decoder || error != OPUS_OK) exit(5);
  float output[960];
  unsigned char packet[1500];
  memcpy(packet, base, (size_t)base_size);
  write_decode(decoder, packet, base_size, output, 960);

  if (scenario == 0 || scenario == 2) {
    packet[0] = 0xd8; /* Valid TOC with end band 19, LM=3. */
    write_decode(decoder, packet, base_size, output, scenario == 0 ? 959 : 0);
  } else if (scenario == 1) {
    packet[0] = 0xdb; /* Same committed end band, malformed code-3 framing. */
    write_decode(decoder, packet, 1, output, 960);
  } else {
    packet[0] = 0x7f; /* fromOpus rejects before committing end band. */
    write_decode(decoder, packet, base_size, output, 0);
  }
  for (int loss = 0; loss < 6; loss++)
    write_decode(decoder, NULL, 0, output, 960);
  opus_custom_decoder_destroy(decoder);
}

int main(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 2;
#endif
  char magic[4];
  if (fread(magic, 1, 4, stdin) != 4 || memcmp(magic, "GCSL", 4)) return 2;
  uint32_t fs = read_u32();
  uint32_t frame_size = read_u32();
  uint32_t max_bytes = read_u32();
  uint32_t sample_count = read_u32();
  if (frame_size != 960 || max_bytes < 2 || max_bytes > 1500 ||
      sample_count != frame_size) return 2;
  float input[960], output[960];
  unsigned char packet[1500];
  for (uint32_t i = 0; i < sample_count; i++) input[i] = read_f32();

  int error = OPUS_OK;
  OpusCustomMode *mode = opus_custom_mode_create((opus_int32)fs,
                                                  (int)frame_size, &error);
  if (!mode || error != OPUS_OK) return 4;
  OpusCustomEncoder *encoder = opus_custom_encoder_create(mode, 1, &error);
  if (!encoder || error != OPUS_OK) return 4;
  int packet_size = opus_custom_encode_float(encoder, input, (int)frame_size,
                                              packet, (int)max_bytes);
  if (packet_size < 2 || packet_size > (int)max_bytes) return 5;
  write_i32(packet_size);
  if (fwrite(packet, 1, (size_t)packet_size, stdout) != (size_t)packet_size)
    return 3;
  for (int scenario = 0; scenario < 4; scenario++)
    run_scenario(mode, scenario, packet, packet_size);
  opus_custom_encoder_destroy(encoder);
  opus_custom_mode_destroy(mode);
  return 0;
}
