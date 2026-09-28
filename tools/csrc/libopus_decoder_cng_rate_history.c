/* Public decoder oracle for SILK CNG excitation retention across rate changes. */
#include "config.h"
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "opus.h"
#include "opus_private.h"

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

static void write_u32(uint32_t value) {
  unsigned char bytes[4] = {
      (unsigned char)value, (unsigned char)(value >> 8),
      (unsigned char)(value >> 16), (unsigned char)(value >> 24)};
  if (fwrite(bytes, 1, sizeof(bytes), stdout) != sizeof(bytes)) exit(2);
}

static void write_f32(float value) {
  uint32_t bits;
  memcpy(&bits, &value, sizeof(bits));
  write_u32(bits);
}

static void check_ctl(int status) {
  if (status != OPUS_OK) {
    fprintf(stderr, "CNG rate-history encoder control failed: %d\n", status);
    exit(3);
  }
}

static OpusEncoder *new_encoder(int bandwidth) {
  int error = OPUS_OK;
  OpusEncoder *encoder = opus_encoder_create(48000, 1, OPUS_APPLICATION_VOIP,
                                             &error);
  if (!encoder || error != OPUS_OK) exit(4);
  check_ctl(opus_encoder_ctl(encoder, OPUS_SET_FORCE_MODE(MODE_SILK_ONLY)));
  check_ctl(opus_encoder_ctl(encoder, OPUS_SET_BANDWIDTH(bandwidth)));
  check_ctl(opus_encoder_ctl(encoder, OPUS_SET_MAX_BANDWIDTH(bandwidth)));
  check_ctl(opus_encoder_ctl(encoder, OPUS_SET_BITRATE(16000)));
  check_ctl(opus_encoder_ctl(encoder, OPUS_SET_VBR(0)));
  check_ctl(opus_encoder_ctl(encoder, OPUS_SET_DTX(0)));
  return encoder;
}

int main(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 2;
#endif
  const float amplitudes[] = {0.00003f, 0.0003f, 0.003f, 0.03f};
  if (fwrite("GCRH", 1, 4, stdout) != 4) return 2;
  write_u32(8);
  for (int direction = 0; direction < 2; direction++) {
    int first_bandwidth = direction ? OPUS_BANDWIDTH_NARROWBAND :
                                      OPUS_BANDWIDTH_WIDEBAND;
    int second_bandwidth = direction ? OPUS_BANDWIDTH_WIDEBAND :
                                       OPUS_BANDWIDTH_NARROWBAND;
    for (int amplitude = 0; amplitude < 4; amplitude++) {
      OpusEncoder *encoder[2] = {
          new_encoder(first_bandwidth), new_encoder(second_bandwidth)};
      int error = OPUS_OK;
      OpusDecoder *decoder = opus_decoder_create(48000, 1, &error);
      if (!decoder || error != OPUS_OK) return 5;

      uint32_t random = 12345;
      write_u32((uint32_t)direction);
      write_u32((uint32_t)amplitude);
      write_u32(17);
      for (int step = 0; step < 17; step++) {
        float input[960], output[960];
        unsigned char packet[1000];
        int packet_size = 0;
        if (step < 7) {
          for (int sample = 0; sample < 960; sample++) {
            random = random * 1664525u + 1013904223u;
            input[sample] = (float)(int32_t)random *
                            (amplitudes[amplitude] / 2147483648.0f);
          }
          packet_size = opus_encode_float(encoder[step < 6 ? 0 : 1], input,
                                          960, packet, sizeof(packet));
          if (packet_size <= 0 || packet_size > (int)sizeof(packet)) return 6;
        }

        int samples = opus_decode_float(decoder,
                                        packet_size ? packet : NULL,
                                        packet_size, output, 960, 0);
        if (samples != 960) return 7;
        opus_uint32 final_range = 0;
        opus_int32 pitch = 0;
        check_ctl(opus_decoder_ctl(decoder, OPUS_GET_FINAL_RANGE(&final_range)));
        check_ctl(opus_decoder_ctl(decoder, OPUS_GET_PITCH(&pitch)));
        write_u32((uint32_t)packet_size);
        write_u32((uint32_t)samples);
        write_u32(final_range);
        write_u32((uint32_t)pitch);
        if (packet_size && fwrite(packet, 1, (size_t)packet_size, stdout) !=
                               (size_t)packet_size) return 8;
        for (int sample = 0; sample < samples; sample++) write_f32(output[sample]);
      }

      opus_decoder_destroy(decoder);
      opus_encoder_destroy(encoder[0]);
      opus_encoder_destroy(encoder[1]);
    }
  }
  return 0;
}
