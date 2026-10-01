/* Stateful OPUS_SET_FORCE_CHANNELS broadcast behavior and packet evidence. */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "opus_multistream.h"

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (fread(b, 1, 4, stdin) != 4) return 0;
  *out = (uint32_t)b[0] | (uint32_t)b[1] << 8 |
         (uint32_t)b[2] << 16 | (uint32_t)b[3] << 24;
  return 1;
}

static int read_i16(opus_int16 *out) {
  unsigned char b[2];
  if (fread(b, 1, 2, stdin) != 2) return 0;
  *out = (opus_int16)((uint16_t)b[0] | (uint16_t)b[1] << 8);
  return 1;
}

static int write_u32(uint32_t value) {
  unsigned char b[4] = {(unsigned char)value, (unsigned char)(value >> 8),
      (unsigned char)(value >> 16), (unsigned char)(value >> 24)};
  return fwrite(b, 1, 4, stdout) == 4;
}

static int write_i32(int32_t value) {
  return write_u32((uint32_t)value);
}

static int write_child_force_channels(OpusMSEncoder *encoder, int streams) {
  if (!write_u32((uint32_t)streams)) return 0;
  for (int i = 0; i < streams; i++) {
    OpusEncoder *child = NULL;
    opus_int32 value = 0;
    int ret = opus_multistream_encoder_ctl(
        encoder, OPUS_MULTISTREAM_GET_ENCODER_STATE(i, &child));
    if (ret == OPUS_OK)
      ret = opus_encoder_ctl(child, OPUS_GET_FORCE_CHANNELS(&value));
    if (!write_i32(ret) || !write_i32(value)) return 0;
  }
  return 1;
}

static int write_force_channels_state(OpusMSEncoder *encoder, int streams) {
  opus_int32 first = 0;
  int first_ret = opus_multistream_encoder_ctl(
      encoder, OPUS_GET_FORCE_CHANNELS(&first));
  if (!write_i32(first_ret) || !write_i32(first)) return 0;
  return write_child_force_channels(encoder, streams);
}

static int write_encoded_packet(OpusMSEncoder *encoder, int16_t *pcm,
                                int frame_size, int max_bytes,
                                unsigned char *packet) {
  int n = opus_multistream_encode(encoder, pcm, frame_size, packet, max_bytes);
  opus_uint32 final_range = 0;
  int range_ret = OPUS_INTERNAL_ERROR;
  if (n >= 0)
    range_ret = opus_multistream_encoder_ctl(
        encoder, OPUS_GET_FINAL_RANGE(&final_range));
  if (!write_i32(n) || !write_i32(range_ret) || !write_u32(final_range) ||
      !write_u32(n < 0 ? 0 : (uint32_t)n)) return 0;
  return n < 0 || fwrite(packet, 1, (size_t)n, stdout) == (size_t)n;
}

int main(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 1;
#endif
  char magic[4];
  uint32_t version, complexity, frame_size, frame_count;
  const int channels = 3;
  const int streams = 2;
  const int coupled = 1;
  const int max_bytes = 8000;
  opus_int16 *pcm = NULL;
  unsigned char *packet = NULL;
  OpusMSEncoder *encoder = NULL;
  unsigned char mapping[255];
  int error = OPUS_OK;
  int status = 1;

  if (fread(magic, 1, 4, stdin) != 4 || memcmp(magic, "GFCI", 4) != 0 ||
      !read_u32(&version) || version != 2 || !read_u32(&complexity) ||
      complexity > 10 || !read_u32(&frame_size) ||
      !read_u32(&frame_count) || frame_size == 0 || frame_size > 5760 ||
      frame_count != 3) return 2;

  mapping[0] = 0;
  mapping[1] = 1;
  mapping[2] = 2;
  encoder = opus_multistream_encoder_create(
      48000, channels, streams, coupled, mapping,
      OPUS_APPLICATION_AUDIO, &error);
  if (!encoder || error != OPUS_OK) {
    fprintf(stderr, "multistream encoder create failed: err=%d\n", error);
    goto done;
  }
  if (opus_multistream_encoder_ctl(encoder, OPUS_SET_BITRATE(256000)) != OPUS_OK) {
    fprintf(stderr, "multistream bitrate control failed\n");
    goto done;
  }
  if (opus_multistream_encoder_ctl(encoder, OPUS_SET_COMPLEXITY(complexity)) != OPUS_OK) {
    fprintf(stderr, "multistream complexity control failed\n");
    goto done;
  }

  pcm = (opus_int16 *)malloc((size_t)channels * frame_size * sizeof(*pcm));
  packet = (unsigned char *)malloc((size_t)max_bytes);
  if (!pcm || !packet) goto done;

  if (fwrite("GFCO", 1, 4, stdout) != 4 || !write_u32(2) ||
      !write_u32((uint32_t)streams) || !write_u32((uint32_t)coupled) ||
      !write_u32((uint32_t)channels) || fwrite(mapping, 1, channels, stdout) != channels)
    goto done;

  if (!write_force_channels_state(encoder, streams)) goto done;
  for (size_t i = 0; i < (size_t)channels * frame_size; i++) {
    if (!read_i16(&pcm[i])) goto done;
  }
  if (!write_encoded_packet(encoder, pcm, (int)frame_size, max_bytes, packet))
    goto done;

  int failed_set = opus_multistream_encoder_ctl(
      encoder, OPUS_SET_FORCE_CHANNELS(2));
  if (!write_i32(failed_set) || !write_force_channels_state(encoder, streams))
    goto done;
  for (size_t i = 0; i < (size_t)channels * frame_size; i++) {
    if (!read_i16(&pcm[i])) goto done;
  }
  if (!write_encoded_packet(encoder, pcm, (int)frame_size, max_bytes, packet))
    goto done;

  int reset_ret = opus_multistream_encoder_ctl(encoder, OPUS_RESET_STATE);
  if (!write_i32(reset_ret) || !write_force_channels_state(encoder, streams))
    goto done;

  int recovery_ret = opus_multistream_encoder_ctl(
      encoder, OPUS_SET_FORCE_CHANNELS(OPUS_AUTO));
  if (!write_i32(recovery_ret) || !write_force_channels_state(encoder, streams))
    goto done;
  for (size_t i = 0; i < (size_t)channels * frame_size; i++) {
    if (!read_i16(&pcm[i])) goto done;
  }
  if (!write_encoded_packet(encoder, pcm, (int)frame_size, max_bytes, packet))
    goto done;

  status = ferror(stdin) || ferror(stdout) ? 1 : 0;
done:
  free(pcm);
  free(packet);
  if (encoder) opus_multistream_encoder_destroy(encoder);
  return status;
}
