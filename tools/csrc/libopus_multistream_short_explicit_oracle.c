/* Actual opus_multistream_encode entry for an explicit, ordinary channel map. */
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

int main(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 1;
#endif
  char magic[4];
  uint32_t version, sample_rate, channels, streams, coupled, application;
  uint32_t bitrate, vbr, constraint, complexity, bandwidth, frame_size, frame_count, max_bytes;
  unsigned char mapping[255];
  opus_int16 *pcm = NULL;
  unsigned char *packet = NULL;
  OpusMSEncoder *encoder = NULL;
  int error = OPUS_OK;
  int status = 1;

  if (fread(magic, 1, 4, stdin) != 4 || memcmp(magic, "GMSI", 4) != 0 ||
      !read_u32(&version) || version != 1 || !read_u32(&sample_rate) ||
      !read_u32(&channels) || !read_u32(&streams) || !read_u32(&coupled) ||
      !read_u32(&application) || !read_u32(&bitrate) || !read_u32(&vbr) ||
      !read_u32(&constraint) || !read_u32(&complexity) || !read_u32(&bandwidth) ||
      !read_u32(&frame_size) || !read_u32(&frame_count) || !read_u32(&max_bytes) ||
      channels == 0 || channels > 255 || streams == 0 || coupled > streams ||
      frame_size == 0 || frame_size > 5760 || frame_count == 0 || frame_count > 1000 ||
      max_bytes == 0 || max_bytes > 65536) return 2;
  if (fread(mapping, 1, channels, stdin) != channels) return 2;

  encoder = opus_multistream_encoder_create((opus_int32)sample_rate, (int)channels,
      (int)streams, (int)coupled, mapping, (int)application, &error);
  if (!encoder || error != OPUS_OK) {
    fprintf(stderr, "opus_multistream_encoder_create: %d\n", error);
    goto done;
  }
  if (opus_multistream_encoder_ctl(encoder, OPUS_SET_BITRATE((opus_int32)bitrate)) != OPUS_OK ||
      opus_multistream_encoder_ctl(encoder, OPUS_SET_VBR((int)vbr)) != OPUS_OK ||
      opus_multistream_encoder_ctl(encoder, OPUS_SET_VBR_CONSTRAINT((int)constraint)) != OPUS_OK ||
      opus_multistream_encoder_ctl(encoder, OPUS_SET_COMPLEXITY((int)complexity)) != OPUS_OK ||
      opus_multistream_encoder_ctl(encoder, OPUS_SET_BANDWIDTH((opus_int32)bandwidth)) != OPUS_OK) {
    fprintf(stderr, "multistream control failed\n");
    goto done;
  }
  size_t samples = (size_t)channels * frame_size;
  pcm = malloc(samples * sizeof(*pcm));
  packet = malloc(max_bytes);
  if (!pcm || !packet) goto done;
  if (fwrite("GMSO", 1, 4, stdout) != 4 || !write_u32(1) ||
      !write_u32(frame_count)) goto done;

  for (uint32_t frame = 0; frame < frame_count; frame++) {
    for (size_t i = 0; i < samples; i++) {
      if (!read_i16(&pcm[i])) goto done;
    }
    int n = opus_multistream_encode(encoder, pcm, (int)frame_size, packet, (opus_int32)max_bytes);
    opus_uint32 final_range = 0;
    if (n < 0 || opus_multistream_encoder_ctl(encoder, OPUS_GET_FINAL_RANGE(&final_range)) != OPUS_OK) {
      fprintf(stderr, "opus_multistream_encode frame %u failed: %d\n", frame, n);
      goto done;
    }
    if (!write_u32(final_range) || !write_u32((uint32_t)n) ||
        fwrite(packet, 1, (size_t)n, stdout) != (size_t)n) goto done;
  }
  status = ferror(stdin) || ferror(stdout) ? 1 : 0;
done:
  free(pcm);
  free(packet);
  if (encoder) opus_multistream_encoder_destroy(encoder);
  return status;
}
