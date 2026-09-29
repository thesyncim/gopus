#include <stdint.h>
#include <stdio.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "opus.h"
#include "opus_multistream.h"

#define MFSI_MAGIC "MFSI"
#define MFSO_MAGIC "MFSO"
#define MAX_FRAME_SAMPLES 12000
#define MAX_PACKET_BYTES 4000
#define MAX_RATES 6
#define MAX_LEGAL_SIZES 9
#define MAX_INVALID_SIZES 20

enum {
  SAMPLE_FORMAT_FLOAT32 = 0,
  SAMPLE_FORMAT_INT16 = 1
};

static int read_exact(void *dst, size_t n) {
  return fread(dst, 1, n, stdin) == n;
}

static int write_exact(const void *src, size_t n) {
  return fwrite(src, 1, n, stdout) == n;
}

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact(b, sizeof(b))) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) | ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int read_i32(int32_t *out) {
  uint32_t bits;
  if (!read_u32(&bits)) return 0;
  memcpy(out, &bits, sizeof(bits));
  return 1;
}

static int write_u32(uint32_t value) {
  unsigned char b[4];
  b[0] = (unsigned char)(value & 0xff);
  b[1] = (unsigned char)((value >> 8) & 0xff);
  b[2] = (unsigned char)((value >> 16) & 0xff);
  b[3] = (unsigned char)((value >> 24) & 0xff);
  return write_exact(b, sizeof(b));
}

static int write_i32(int32_t value) {
  uint32_t bits;
  memcpy(&bits, &value, sizeof(bits));
  return write_u32(bits);
}

static void fill_pcm(float *pcm_float, opus_int16 *pcm_short, int frame_size, int seed) {
  int i;
  for (i = 0; i < frame_size; i++) {
    int value = (i * 97 + seed * 31) % 2001 - 1000;
    pcm_float[i] = (float)value * 0.0001f;
    pcm_short[i] = (opus_int16)(value * 16);
  }
}

static OpusMSEncoder *create_encoder(uint32_t sample_rate, int restricted_silk) {
  static const unsigned char mapping[1] = {0};
  int error = OPUS_OK;
  int application = restricted_silk ? OPUS_APPLICATION_RESTRICTED_SILK : OPUS_APPLICATION_AUDIO;
  OpusMSEncoder *enc = opus_multistream_encoder_create((opus_int32)sample_rate, 1, 1, 0,
                                                       mapping, application, &error);
  if (enc == NULL || error != OPUS_OK) {
    if (enc != NULL) opus_multistream_encoder_destroy(enc);
    return NULL;
  }
  if (opus_multistream_encoder_ctl(enc, OPUS_SET_BITRATE(64000)) != OPUS_OK) {
    opus_multistream_encoder_destroy(enc);
    return NULL;
  }
  return enc;
}

static int encode_frame(OpusMSEncoder *enc, uint32_t sample_format,
                        const float *pcm_float, const opus_int16 *pcm_short,
                        int frame_size, unsigned char *packet, int max_bytes) {
  if (sample_format == SAMPLE_FORMAT_INT16) {
    return opus_multistream_encode(enc, pcm_short, frame_size, packet, max_bytes);
  }
  return opus_multistream_encode_float(enc, pcm_float, frame_size, packet, max_bytes);
}

static int get_final_range(OpusMSEncoder *enc, opus_uint32 *range) {
  return opus_multistream_encoder_ctl(enc, OPUS_GET_FINAL_RANGE(range)) == OPUS_OK;
}

static int write_legal_record(uint32_t sample_rate, uint32_t sample_format,
                             int restricted_silk, int frame_size,
                             float *pcm_float, opus_int16 *pcm_short,
                             unsigned char *packet) {
  OpusMSEncoder *enc = create_encoder(sample_rate, restricted_silk);
  opus_uint32 range = 0;
  int packet_size;
  if (enc == NULL) return 0;
  fill_pcm(pcm_float, pcm_short, frame_size, 9);
  packet_size = encode_frame(enc, sample_format, pcm_float, pcm_short, frame_size, packet,
                             MAX_PACKET_BYTES);
  if (!write_i32(packet_size)) {
    opus_multistream_encoder_destroy(enc);
    return 0;
  }
  if (packet_size < 0 || !get_final_range(enc, &range) || !write_u32(range)) {
    opus_multistream_encoder_destroy(enc);
    return 0;
  }
  opus_multistream_encoder_destroy(enc);
  return 1;
}

static int write_invalid_record(uint32_t sample_rate, uint32_t sample_format,
                                int restricted_silk, int bad_frame_size,
                                float *pcm_float, opus_int16 *pcm_short,
                                unsigned char *bad_packet,
                                unsigned char *probe_packet,
                                unsigned char *control_packet) {
  int prime_frame_size = (int)sample_rate / 50;
  OpusMSEncoder *probe = create_encoder(sample_rate, restricted_silk);
  OpusMSEncoder *control = create_encoder(sample_rate, restricted_silk);
  opus_uint32 range_before = 0, range_after = 0;
  opus_uint32 probe_recovery_range = 0, control_recovery_range = 0;
  int bad_result, probe_prime, control_prime, probe_recovery, control_recovery;
  int same_recovery;

  if (probe == NULL || control == NULL) goto fail;
  fill_pcm(pcm_float, pcm_short, prime_frame_size, 3);
  probe_prime = encode_frame(probe, sample_format, pcm_float, pcm_short, prime_frame_size,
                             probe_packet, MAX_PACKET_BYTES);
  control_prime = encode_frame(control, sample_format, pcm_float, pcm_short, prime_frame_size,
                               control_packet, MAX_PACKET_BYTES);
  if (probe_prime < 0 || control_prime < 0 ||
      !get_final_range(probe, &range_before) || !get_final_range(control, &range_after) ||
      range_before != range_after || probe_prime != control_prime ||
      memcmp(probe_packet, control_packet, (size_t)probe_prime) != 0) goto fail;

  fill_pcm(pcm_float, pcm_short, MAX_FRAME_SAMPLES, 11);
  bad_result = encode_frame(probe, sample_format, pcm_float, pcm_short, bad_frame_size,
                            bad_packet, 1);
  if (!get_final_range(probe, &range_after)) goto fail;

  fill_pcm(pcm_float, pcm_short, prime_frame_size, 29);
  probe_recovery = encode_frame(probe, sample_format, pcm_float, pcm_short, prime_frame_size,
                                probe_packet, MAX_PACKET_BYTES);
  control_recovery = encode_frame(control, sample_format, pcm_float, pcm_short, prime_frame_size,
                                  control_packet, MAX_PACKET_BYTES);
  if (probe_recovery < 0 || control_recovery < 0 ||
      !get_final_range(probe, &probe_recovery_range) ||
      !get_final_range(control, &control_recovery_range)) goto fail;

  same_recovery = probe_recovery == control_recovery &&
                  probe_recovery_range == control_recovery_range &&
                  memcmp(probe_packet, control_packet, (size_t)probe_recovery) == 0;
  if (!write_i32(bad_result) || !write_u32(range_before) || !write_u32(range_after) ||
      !write_i32(probe_recovery) || !write_u32(probe_recovery_range) ||
      !write_i32(control_recovery) || !write_u32(control_recovery_range) ||
      !write_u32((uint32_t)same_recovery) || !write_u32((uint32_t)control_recovery) ||
      !write_exact(control_packet, (size_t)control_recovery)) goto fail;

  opus_multistream_encoder_destroy(probe);
  opus_multistream_encoder_destroy(control);
  return 1;

fail:
  if (probe != NULL) opus_multistream_encoder_destroy(probe);
  if (control != NULL) opus_multistream_encoder_destroy(control);
  return 0;
}

int main(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 1;
#endif
  unsigned char magic[4];
  uint32_t version = 0, sample_format = 0, mode_count = 0;
  float pcm_float[MAX_FRAME_SAMPLES];
  opus_int16 pcm_short[MAX_FRAME_SAMPLES];
  unsigned char bad_packet[MAX_PACKET_BYTES];
  unsigned char probe_packet[MAX_PACKET_BYTES];
  unsigned char control_packet[MAX_PACKET_BYTES];
  uint32_t mode;

  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, MFSI_MAGIC, sizeof(magic)) != 0 ||
      !read_u32(&version) || version != 1 || !read_u32(&sample_format) ||
      sample_format > SAMPLE_FORMAT_INT16 || !read_u32(&mode_count) || mode_count > 2) return 1;
  if (!write_exact(MFSO_MAGIC, 4) || !write_u32(version) || !write_u32(mode_count)) return 1;

  for (mode = 0; mode < mode_count; mode++) {
    uint32_t restricted_silk = 0, rate_count = 0, rate_index;
    if (!read_u32(&restricted_silk) || restricted_silk > 1 || !read_u32(&rate_count) ||
        rate_count == 0 || rate_count > MAX_RATES || !write_u32(restricted_silk) ||
        !write_u32(rate_count)) return 1;
    for (rate_index = 0; rate_index < rate_count; rate_index++) {
      uint32_t sample_rate = 0, legal_count = 0, invalid_count = 0, i;
      int32_t legal[MAX_LEGAL_SIZES];
      int32_t invalid[MAX_INVALID_SIZES];
      if (!read_u32(&sample_rate) || !read_u32(&legal_count) || legal_count > MAX_LEGAL_SIZES) return 1;
      for (i = 0; i < legal_count; i++) {
        if (!read_i32(&legal[i]) || legal[i] <= 0 || legal[i] > MAX_FRAME_SAMPLES) return 1;
      }
      if (!read_u32(&invalid_count) || invalid_count > MAX_INVALID_SIZES) return 1;
      for (i = 0; i < invalid_count; i++) {
        if (!read_i32(&invalid[i]) || invalid[i] > MAX_FRAME_SAMPLES) return 1;
      }
      if (!write_u32(sample_rate) || !write_u32(legal_count)) return 1;
      for (i = 0; i < legal_count; i++) {
        if (!write_legal_record(sample_rate, sample_format, (int)restricted_silk, legal[i],
                                pcm_float, pcm_short, control_packet)) return 1;
      }
      if (!write_u32(invalid_count)) return 1;
      for (i = 0; i < invalid_count; i++) {
        if (!write_invalid_record(sample_rate, sample_format, (int)restricted_silk, invalid[i],
                                  pcm_float, pcm_short, bad_packet, probe_packet,
                                  control_packet)) return 1;
      }
    }
  }
  return 0;
}
