#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "opus_projection.h"

/* Oracle: given (channels, mapping_family=3, application),
 * create a projection encoder and return the demixing matrix
 * via OPUS_PROJECTION_GET_DEMIXING_MATRIX.
 *
 * Input:  "GPDI" version(u32=1) sample_rate(u32) channels(u32) application(u32)
 * Output: "GPDO" version(u32=1) streams(u32) coupled(u32)
 *         demix_size(u32) demix_bytes[demix_size]
 *         demix_gain(u32)
 */

#define INPUT_MAGIC  "GPDI"
#define OUTPUT_MAGIC "GPDO"

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin),  _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

static int read_exact(void *dst, size_t n) {
  return fread(dst, 1, n, stdin) == n;
}

static int write_exact(const void *src, size_t n) {
  return fwrite(src, 1, n, stdout) == n;
}

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact(b, 4)) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1]<<8) | ((uint32_t)b[2]<<16) | ((uint32_t)b[3]<<24);
  return 1;
}

static int write_u32(uint32_t v) {
  unsigned char b[4];
  b[0] = (unsigned char)(v & 0xFF);
  b[1] = (unsigned char)((v>>8)  & 0xFF);
  b[2] = (unsigned char)((v>>16) & 0xFF);
  b[3] = (unsigned char)((v>>24) & 0xFF);
  return write_exact(b, 4);
}

int main(void) {
  unsigned char magic[4];
  uint32_t version, sample_rate, channels, application;
  int streams = 0, coupled = 0, err;
  opus_int32 demix_size = 0, demix_gain = 0;
  unsigned char *demix_buf = NULL;
  OpusProjectionEncoder *enc = NULL;

  if (!set_binary_stdio()) {
    fprintf(stderr, "binary stdio failed\n");
    return 1;
  }

  if (!read_exact(magic, 4) || memcmp(magic, INPUT_MAGIC, 4) != 0) {
    fprintf(stderr, "bad input magic\n");
    return 1;
  }
  if (!read_u32(&version) || version != 1) {
    fprintf(stderr, "bad version\n");
    return 1;
  }
  if (!read_u32(&sample_rate) || !read_u32(&channels) || !read_u32(&application)) {
    fprintf(stderr, "truncated header\n");
    return 1;
  }

  enc = opus_projection_ambisonics_encoder_create(
    (opus_int32)sample_rate, (int)channels, 3,
    &streams, &coupled, (int)application, &err);
  if (enc == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_projection_ambisonics_encoder_create failed: %d\n", err);
    return 1;
  }

  if (opus_projection_encoder_ctl(enc, OPUS_PROJECTION_GET_DEMIXING_MATRIX_SIZE(&demix_size)) != OPUS_OK) {
    fprintf(stderr, "GET_DEMIXING_MATRIX_SIZE failed\n");
    opus_projection_encoder_destroy(enc);
    return 1;
  }

  if (opus_projection_encoder_ctl(enc, OPUS_PROJECTION_GET_DEMIXING_MATRIX_GAIN(&demix_gain)) != OPUS_OK) {
    fprintf(stderr, "GET_DEMIXING_MATRIX_GAIN failed\n");
    opus_projection_encoder_destroy(enc);
    return 1;
  }

  demix_buf = (unsigned char *)malloc((size_t)demix_size);
  if (demix_buf == NULL) {
    fprintf(stderr, "alloc failed\n");
    opus_projection_encoder_destroy(enc);
    return 1;
  }

  if (opus_projection_encoder_ctl(enc, OPUS_PROJECTION_GET_DEMIXING_MATRIX(demix_buf, demix_size)) != OPUS_OK) {
    fprintf(stderr, "GET_DEMIXING_MATRIX failed\n");
    free(demix_buf);
    opus_projection_encoder_destroy(enc);
    return 1;
  }

  opus_projection_encoder_destroy(enc);

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) ||
      !write_u32((uint32_t)streams) || !write_u32((uint32_t)coupled) ||
      !write_u32((uint32_t)demix_size) || !write_exact(demix_buf, (size_t)demix_size) ||
      !write_u32((uint32_t)(int32_t)demix_gain)) {
    fprintf(stderr, "write failed\n");
    free(demix_buf);
    return 1;
  }

  free(demix_buf);
  return 0;
}
