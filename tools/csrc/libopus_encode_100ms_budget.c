/* Live libopus oracle for single-stream encoder byte-budget edge cases.
 * G100 input: version, Fs, channels, bitrate, API format, operation count;
 * each operation supplies caller frame size, expert duration, and byte budget.
 * G100 output: version, operation count; each result has status, final range,
 * packet length, and packet bytes.
 */

#include "config.h"

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "opus.h"

#define MAX_FRAME_SAMPLES (11520 * 2)
#define MAX_PACKET_BYTES 4000

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

static int write_i32(int32_t value) { return write_u32((uint32_t)value); }

int main(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1)
    return 2;
#endif

  char magic[4];
  uint32_t version, sample_rate, channels, bitrate, api, count;
  if (fread(magic, 1, sizeof(magic), stdin) != sizeof(magic) ||
      memcmp(magic, "G100", sizeof(magic)) != 0 ||
      !read_u32(&version) || !read_u32(&sample_rate) ||
      !read_u32(&channels) || !read_u32(&bitrate) || !read_u32(&api) ||
      !read_u32(&count))
    return 2;
  if (version != 1 || (channels != 1 && channels != 2) || api > 2 ||
      count == 0 || count > 32)
    return 2;

  int status = OPUS_OK;
  OpusEncoder *enc = opus_encoder_create((opus_int32)sample_rate,
                                         (int)channels,
                                         OPUS_APPLICATION_AUDIO, &status);
  if (enc == NULL || status != OPUS_OK) return 4;
#ifdef ENABLE_QEXT
  if (sample_rate == 96000 &&
      opus_encoder_ctl(enc, OPUS_SET_QEXT(1)) != OPUS_OK)
    return 4;
#endif
  if (opus_encoder_ctl(enc, OPUS_SET_BITRATE((opus_int32)bitrate)) != OPUS_OK)
    return 4;

  static float pcm_float[MAX_FRAME_SAMPLES];
  static opus_int16 pcm_int16[MAX_FRAME_SAMPLES];
  static opus_int32 pcm_int24[MAX_FRAME_SAMPLES];
  unsigned char packet[MAX_PACKET_BYTES];

  if (fwrite("G100", 1, 4, stdout) != 4 || !write_u32(1) ||
      !write_u32(count))
    return 3;

  for (uint32_t i = 0; i < count; i++) {
    uint32_t frame_size, expert_duration, budget;
    uint32_t max_frame_size = sample_rate == 96000 ? 11520 : 5760;
    if (!read_u32(&frame_size) || !read_u32(&expert_duration) ||
        !read_u32(&budget) || frame_size == 0 || frame_size > max_frame_size ||
        budget == 0 || budget > MAX_PACKET_BYTES)
      return 2;
    if (opus_encoder_ctl(enc,
                         OPUS_SET_EXPERT_FRAME_DURATION((opus_int32)expert_duration)) !=
        OPUS_OK)
      return 4;

    int n;
    switch (api) {
      case 0:
        n = opus_encode_float(enc, pcm_float, (int)frame_size, packet,
                              (opus_int32)budget);
        break;
      case 1:
        n = opus_encode(enc, pcm_int16, (int)frame_size, packet,
                        (opus_int32)budget);
        break;
      default:
        n = opus_encode24(enc, pcm_int24, (int)frame_size, packet,
                          (opus_int32)budget);
        break;
    }

    opus_uint32 range = 0;
    if (opus_encoder_ctl(enc, OPUS_GET_FINAL_RANGE(&range)) != OPUS_OK) return 4;
    uint32_t packet_len = n > 0 ? (uint32_t)n : 0;
    if (!write_i32(n) || !write_u32(range) || !write_u32(packet_len) ||
        (packet_len != 0 &&
         fwrite(packet, 1, packet_len, stdout) != packet_len))
      return 3;
  }

  opus_encoder_destroy(enc);
  return 0;
}
