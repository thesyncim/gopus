/*
 * Public Opus Custom signalling oracle.
 *
 * Each request produces a default opus_custom_* result and a raw CELT result
 * made with the private CELT_SET_SIGNALLING(0) control. The raw result exists
 * only to verify the explicit raw mode of the Go custom wrapper.
 */
#include <stdint.h>
#include <stdio.h>

#include "opus_custom.h"
#include "opus_defines.h"
#include "celt.h"

static int read_exact(void *dst, size_t size) {
  return fread(dst, 1, size, stdin) == size;
}

static int write_exact(const void *src, size_t size) {
  return fwrite(src, 1, size, stdout) == size;
}

static int read_u32(uint32_t *value) { return read_exact(value, sizeof(*value)); }
static int write_u32(uint32_t value) { return write_exact(&value, sizeof(value)); }
static int write_i32(int32_t value) { return write_exact(&value, sizeof(value)); }

int main(void) {
  char magic[4];
  uint32_t count;
  if (!read_exact(magic, sizeof(magic)) || magic[0] != 'G' ||
      magic[1] != 'C' || magic[2] != 'S' || magic[3] != 'G' ||
      !read_u32(&count)) return 1;
  if (!write_exact("GCSG", 4) || !write_u32(count)) return 2;

  for (uint32_t i = 0; i < count; i++) {
    uint32_t fs, frame_size, channels, decode_channels, max_bytes, sample_count;
    if (!read_u32(&fs) || !read_u32(&frame_size) || !read_u32(&channels) ||
        !read_u32(&decode_channels) || !read_u32(&max_bytes) ||
        !read_u32(&sample_count) || sample_count > 4096 ||
        max_bytes > 1500 || channels < 1 || channels > 2 ||
        decode_channels < 1 || decode_channels > 2) return 3;
    float pcm[4096];
    if (!read_exact(pcm, sizeof(float) * sample_count)) return 4;

    int error = OPUS_OK;
    OpusCustomMode *mode = opus_custom_mode_create((opus_int32)fs,
                                                   (int)frame_size, &error);
    if (!mode || error != OPUS_OK) return 5;

    for (int signalling = 1; signalling >= 0; signalling--) {
      OpusCustomEncoder *enc = opus_custom_encoder_create(mode, (int)channels,
                                                          &error);
      if (!enc || error != OPUS_OK) return 6;
      if (opus_custom_encoder_ctl(enc, OPUS_SET_VBR(0)) != OPUS_OK ||
          opus_custom_encoder_ctl(enc, OPUS_SET_VBR_CONSTRAINT(0)) != OPUS_OK ||
          opus_custom_encoder_ctl(enc, OPUS_SET_COMPLEXITY(9)) != OPUS_OK ||
          opus_custom_encoder_ctl(enc, OPUS_SET_LSB_DEPTH(16)) != OPUS_OK) return 7;
      if (!signalling &&
          opus_custom_encoder_ctl(enc, CELT_SET_SIGNALLING(0)) != OPUS_OK) return 8;

      unsigned char packet[1500];
      int size = opus_custom_encode_float(enc, pcm, (int)frame_size, packet,
                                          (int)max_bytes);
      opus_uint32 enc_range = 0;
      if (size < 0 ||
          opus_custom_encoder_ctl(enc, OPUS_GET_FINAL_RANGE(&enc_range)) != OPUS_OK) return 9;
      opus_custom_encoder_destroy(enc);

      int output_channels = signalling ? (int)decode_channels : (int)channels;
      OpusCustomDecoder *dec = opus_custom_decoder_create(mode, output_channels,
                                                            &error);
      if (!dec || error != OPUS_OK) return 10;
      if (!signalling &&
          opus_custom_decoder_ctl(dec, CELT_SET_SIGNALLING(0)) != OPUS_OK) return 11;
      float decoded[4096];
      int samples = opus_custom_decode_float(dec, packet, size, decoded,
                                             (int)frame_size);
      opus_uint32 dec_range = 0;
      if (samples < 0 ||
          opus_custom_decoder_ctl(dec, OPUS_GET_FINAL_RANGE(&dec_range)) != OPUS_OK) return 12;
      opus_custom_decoder_destroy(dec);

      if (!write_i32(signalling) || !write_i32(size) || !write_i32(samples) ||
          !write_u32(enc_range) || !write_u32(dec_range) ||
          !write_exact(packet, (size_t)size) ||
          !write_exact(decoded, sizeof(float) * (size_t)samples * output_channels)) return 13;
    }
    opus_custom_mode_destroy(mode);
  }
  return 0;
}
