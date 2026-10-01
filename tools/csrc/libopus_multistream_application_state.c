/* Stateful OPUS_SET_APPLICATION behavior after low-budget multistream encodes. */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "opus.h"
#include "opus_multistream.h"

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (fread(b, 1, 4, stdin) != 4) return 0;
  *out = (uint32_t)b[0] | (uint32_t)b[1] << 8 |
         (uint32_t)b[2] << 16 | (uint32_t)b[3] << 24;
  return 1;
}

static int write_u32(uint32_t value) {
  unsigned char b[4] = {(unsigned char)value, (unsigned char)(value >> 8),
      (unsigned char)(value >> 16), (unsigned char)(value >> 24)};
  return fwrite(b, 1, 4, stdout) == 4;
}

static int write_i32(int32_t value) { return write_u32((uint32_t)value); }

static int write_encoded_packet(OpusMSEncoder *encoder, const float *pcm,
                                int frame_size, int capacity,
                                unsigned char *packet) {
  int n = opus_multistream_encode_float(encoder, pcm, frame_size, packet, capacity);
  opus_uint32 final_range = 0;
  int range_status = opus_multistream_encoder_ctl(
      encoder, OPUS_GET_FINAL_RANGE(&final_range));
  uint32_t packet_len = n < 0 ? 0 : (uint32_t)n;
  if (!write_i32(n) || !write_i32(range_status) || !write_u32(final_range) ||
      !write_u32(packet_len)) return 0;
  return packet_len == 0 || fwrite(packet, 1, packet_len, stdout) == packet_len;
}

int main(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 1;
#endif
  char magic[4];
  uint32_t version, channels, streams, coupled, frame_size, low_capacity;
  uint32_t full_capacity, bitrate, mapping_len;
  unsigned char mapping[255];
  float *pcm = NULL;
  unsigned char *low_packet = NULL;
  unsigned char *full_packet = NULL;
  OpusMSEncoder *encoder = NULL;
  int create_status = OPUS_INTERNAL_ERROR;
  int bitrate_status = OPUS_INTERNAL_ERROR;
  int set_application_status = OPUS_INTERNAL_ERROR;
  int get_application_status = OPUS_INTERNAL_ERROR;
  opus_int32 application = -1;
  int status = 1;

  if (fread(magic, 1, 4, stdin) != 4 || memcmp(magic, "GMCI", 4) != 0 ||
      !read_u32(&version) || version != 1 || !read_u32(&channels) ||
      !read_u32(&streams) || !read_u32(&coupled) || !read_u32(&frame_size) ||
      !read_u32(&low_capacity) || !read_u32(&full_capacity) ||
      !read_u32(&bitrate) || !read_u32(&mapping_len)) return 2;
  if (channels < 1 || channels > 255 || streams < 1 || streams > 255 ||
      coupled > streams || streams + coupled > 255 || frame_size < 1 ||
      frame_size > 5760 || low_capacity < 1 || low_capacity > 4000 ||
      full_capacity < 1 || full_capacity > 4000 || mapping_len != channels ||
      bitrate > INT32_MAX) return 2;
  if (fread(mapping, 1, mapping_len, stdin) != mapping_len) return 2;

  pcm = (float *)malloc((size_t)channels * frame_size * sizeof(*pcm));
  low_packet = (unsigned char *)malloc(low_capacity);
  full_packet = (unsigned char *)malloc(full_capacity);
  if (!pcm || !low_packet || !full_packet) goto done;
  for (size_t i = 0; i < (size_t)channels * frame_size; i++) {
    uint32_t bits;
    if (!read_u32(&bits)) goto done;
    memcpy(&pcm[i], &bits, sizeof(bits));
  }

  encoder = opus_multistream_encoder_create(48000, channels, streams, coupled,
      mapping, OPUS_APPLICATION_AUDIO, &create_status);
  if (!encoder) goto write_result;
  bitrate_status = opus_multistream_encoder_ctl(encoder, OPUS_SET_BITRATE((opus_int32)bitrate));

write_result:
  if (fwrite("GMCO", 1, 4, stdout) != 4 || !write_u32(1) ||
      !write_i32(create_status) || !write_i32(bitrate_status)) goto done;
  if (!encoder) {
    status = 0;
    goto done;
  }
  if (!write_encoded_packet(encoder, pcm, frame_size, low_capacity, low_packet)) goto done;

  set_application_status = opus_multistream_encoder_ctl(
      encoder, OPUS_SET_APPLICATION(OPUS_APPLICATION_VOIP));
  get_application_status = opus_multistream_encoder_ctl(
      encoder, OPUS_GET_APPLICATION(&application));
  if (!write_i32(set_application_status) || !write_i32(get_application_status) ||
      !write_i32(application) || !write_u32(streams)) goto done;
  for (uint32_t i = 0; i < streams; i++) {
    OpusEncoder *child = NULL;
    opus_int32 child_application = -1;
    int child_status = opus_multistream_encoder_ctl(
        encoder, OPUS_MULTISTREAM_GET_ENCODER_STATE(i, &child));
    if (child_status == OPUS_OK)
      child_status = opus_encoder_ctl(child, OPUS_GET_APPLICATION(&child_application));
    if (!write_i32(child_status) || !write_i32(child_application)) goto done;
  }
  if (!write_encoded_packet(encoder, pcm, frame_size, full_capacity, full_packet)) goto done;
  status = 0;

done:
  if (encoder) opus_multistream_encoder_destroy(encoder);
  free(pcm);
  free(low_packet);
  free(full_packet);
  return status;
}
