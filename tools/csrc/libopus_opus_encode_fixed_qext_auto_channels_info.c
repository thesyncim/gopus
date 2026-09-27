/* Public fixed-QEXT 96 kHz auto-channel sequence oracle. */

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#if !defined(FIXED_POINT) || !defined(ENABLE_RES24) || !defined(ENABLE_QEXT)
#error "auto-channel oracle requires FIXED_POINT ENABLE_RES24 ENABLE_QEXT"
#endif

#include "opus.h"
#include "opus_defines.h"
#include "opus_private.h"
#include "celt.h"
#include "entenc.h"

#define MAX_FRAMES 32
#define MAX_CELT_TRACE_SAMPLES 3840
static int capture_fixed_celt_frame(CELTEncoder *st, const opus_res *pcm,
    int frame_size, unsigned char *compressed, int nbCompressedBytes, ec_enc *enc);

/* Capture the exact RES24 input and controls that opus_encode_native passes
 * into celt_encode_with_ec. This helper compiles the pinned public encoder so
 * its private per-frame bitrate, LSB depth and selected channel count are
 * available without guessing from the packet. */
#define celt_encode_with_ec capture_fixed_celt_frame
#include "src/opus_encoder.c"
#undef celt_encode_with_ec

typedef struct {
  uint32_t calls;
  uint32_t frame_size;
  uint32_t sample_count;
  uint32_t stream_channels;
  int32_t bitrate;
  int32_t lsb_depth;
  uint32_t max_bytes;
  int32_t pcm[MAX_CELT_TRACE_SAMPLES];
} CELTFrameTrace;

static OpusEncoder *trace_encoder;
static uint32_t trace_frame;
static CELTFrameTrace celt_trace[MAX_FRAMES];

static int capture_fixed_celt_frame(CELTEncoder *st, const opus_res *pcm,
    int frame_size, unsigned char *compressed, int nbCompressedBytes, ec_enc *enc) {
  if (trace_encoder != NULL && enc != NULL && trace_frame < MAX_FRAMES) {
    CELTFrameTrace *trace = &celt_trace[trace_frame];
    opus_int32 celt_lsb_depth = 0;
    uint32_t samples = (uint32_t)(frame_size * trace_encoder->channels);
    if (celt_encoder_ctl(st, OPUS_GET_LSB_DEPTH(&celt_lsb_depth)) != OPUS_OK) return -1;
    trace->calls++;
    trace->frame_size = (uint32_t)frame_size;
    trace->sample_count = samples;
    trace->stream_channels = (uint32_t)trace_encoder->stream_channels;
    trace->bitrate = trace_encoder->bitrate_bps;
    trace->lsb_depth = celt_lsb_depth;
    trace->max_bytes = (uint32_t)nbCompressedBytes;
    if (samples <= MAX_CELT_TRACE_SAMPLES) {
      uint32_t i;
      for (i = 0; i < samples; i++) trace->pcm[i] = (int32_t)pcm[i];
    }
  }
  return celt_encode_with_ec(st, pcm, frame_size, compressed, nbCompressedBytes, enc);
}

#define INPUT_MAGIC "GQAI"
#define OUTPUT_MAGIC "GQAO"
#define MAX_PACKET_BYTES 4000
#define CHANNELS 2

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

static int read_exact(void *dst, size_t n) {
  return fread(dst, 1, n, stdin) == n;
}

static int write_exact(const void *src, size_t n) {
  size_t off = 0;
  const unsigned char *p = (const unsigned char *)src;
  while (off < n) {
    size_t written = fwrite(p + off, 1, n - off, stdout);
    if (written == 0) return 0;
    off += written;
  }
  return 1;
}

static int read_u32(uint32_t *value) {
  unsigned char b[4];
  if (!read_exact(b, sizeof(b))) return 0;
  *value = (uint32_t)b[0] | ((uint32_t)b[1] << 8) |
           ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int write_u32(uint32_t value) {
  unsigned char b[4];
  b[0] = (unsigned char)value;
  b[1] = (unsigned char)(value >> 8);
  b[2] = (unsigned char)(value >> 16);
  b[3] = (unsigned char)(value >> 24);
  return write_exact(b, sizeof(b));
}

static int write_pad(size_t bytes) {
  unsigned char zeros[3] = {0, 0, 0};
  size_t padding = (4 - (bytes & 3)) & 3;
  return padding == 0 || write_exact(zeros, padding);
}

int main(void) {
  if (!set_binary_stdio()) {
    fprintf(stderr, "set_binary_stdio failed\n");
    return 1;
  }

  char magic[4];
  uint32_t version, frame_size, frame_count, max_packet_bytes, complexity;
  uint32_t lsb_depth, enable_qext;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0 ||
      !read_u32(&version) || version != 1 || !read_u32(&frame_size) ||
      !read_u32(&frame_count) || !read_u32(&max_packet_bytes) ||
      !read_u32(&complexity) || !read_u32(&lsb_depth) || !read_u32(&enable_qext)) {
    fprintf(stderr, "invalid auto-channel header\n");
    return 1;
  }
  if ((frame_size != 240 && frame_size != 480 && frame_size != 960 && frame_size != 1920) ||
      frame_count == 0 || frame_count > MAX_FRAMES || max_packet_bytes == 0 ||
      max_packet_bytes > MAX_PACKET_BYTES || complexity > 10 ||
      lsb_depth < 8 || lsb_depth > 24 || enable_qext > 1) {
    fprintf(stderr, "invalid auto-channel dimensions or controls\n");
    return 1;
  }

  size_t per_frame = (size_t)frame_size * CHANNELS;
  opus_int16 *pcm = (opus_int16 *)malloc(per_frame * sizeof(*pcm));
  unsigned char *packet = (unsigned char *)malloc(max_packet_bytes);
  unsigned char **packets = (unsigned char **)calloc(frame_count, sizeof(*packets));
  uint32_t *packet_lengths = (uint32_t *)calloc(frame_count, sizeof(*packet_lengths));
  int32_t *statuses = (int32_t *)calloc(frame_count, sizeof(*statuses));
  opus_uint32 *ranges = (opus_uint32 *)calloc(frame_count, sizeof(*ranges));
  if (pcm == NULL || packet == NULL || packets == NULL || packet_lengths == NULL ||
      statuses == NULL || ranges == NULL) {
    fprintf(stderr, "auto-channel allocation failed\n");
    free(pcm); free(packet); free(packets); free(packet_lengths); free(statuses); free(ranges);
    return 1;
  }

  int error = OPUS_OK;
  OpusEncoder *enc = opus_encoder_create(96000, CHANNELS, OPUS_APPLICATION_AUDIO, &error);
  if (enc == NULL || error != OPUS_OK) {
    fprintf(stderr, "opus_encoder_create failed: %d\n", error);
    goto fail;
  }
  if (opus_encoder_ctl(enc, OPUS_SET_FORCE_MODE(MODE_CELT_ONLY)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_BANDWIDTH(OPUS_BANDWIDTH_FULLBAND)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_MAX_BANDWIDTH(OPUS_BANDWIDTH_FULLBAND)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_COMPLEXITY((opus_int32)complexity)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_LSB_DEPTH((opus_int32)lsb_depth)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_VBR(1)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_VBR_CONSTRAINT(0)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_QEXT((opus_int32)enable_qext)) != OPUS_OK) {
    fprintf(stderr, "auto-channel initial control failed\n");
    goto fail;
  }

  for (uint32_t f = 0; f < frame_count; f++) {
    uint32_t bitrate;
    if (!read_u32(&bitrate) || bitrate == 0 || bitrate > 1500000) {
      fprintf(stderr, "invalid bitrate at frame %u\n", f);
      goto fail;
    }
    for (size_t i = 0; i < per_frame; i++) {
      unsigned char b[2];
      if (!read_exact(b, sizeof(b))) {
        fprintf(stderr, "truncated PCM at frame %u sample %zu\n", f, i);
        goto fail;
      }
      pcm[i] = (opus_int16)((uint16_t)b[0] | ((uint16_t)b[1] << 8));
    }
    /* libopus clears the forced mode after each call. */
    if (opus_encoder_ctl(enc, OPUS_SET_BITRATE((opus_int32)bitrate)) != OPUS_OK ||
        opus_encoder_ctl(enc, OPUS_SET_FORCE_MODE(MODE_CELT_ONLY)) != OPUS_OK) {
      fprintf(stderr, "per-frame control failed at frame %u\n", f);
      goto fail;
    }
    trace_encoder = enc;
    trace_frame = f;
    int n = opus_encode(enc, pcm, (int)frame_size, packet, (int)max_packet_bytes);
    trace_encoder = NULL;
    statuses[f] = n < 0 ? n : OPUS_OK;
    if (n > 0) {
      packets[f] = (unsigned char *)malloc((size_t)n);
      if (packets[f] == NULL) {
        fprintf(stderr, "packet allocation failed at frame %u\n", f);
        goto fail;
      }
      memcpy(packets[f], packet, (size_t)n);
      packet_lengths[f] = (uint32_t)n;
      if (opus_encoder_ctl(enc, OPUS_GET_FINAL_RANGE(&ranges[f])) != OPUS_OK) {
        fprintf(stderr, "final range failed at frame %u\n", f);
        goto fail;
      }
    }
  }

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(2) || !write_u32(frame_count)) {
    fprintf(stderr, "write header failed\n");
    goto fail;
  }
  for (uint32_t f = 0; f < frame_count; f++) {
    CELTFrameTrace *trace = &celt_trace[f];
    if (!write_u32((uint32_t)statuses[f]) || !write_u32(packet_lengths[f]) ||
        !write_u32(ranges[f]) || !write_exact(packets[f], packet_lengths[f]) ||
        !write_pad(packet_lengths[f]) || !write_u32(trace->calls) ||
        !write_u32(trace->frame_size) || !write_u32(trace->sample_count) ||
        !write_u32(trace->stream_channels) || !write_u32((uint32_t)trace->bitrate) ||
        !write_u32((uint32_t)trace->lsb_depth) || !write_u32(trace->max_bytes)) {
      fprintf(stderr, "write record failed at frame %u\n", f);
      goto fail;
    }
    for (uint32_t j = 0; j < trace->sample_count; j++) {
      if (!write_u32((uint32_t)trace->pcm[j])) {
        fprintf(stderr, "write CELT trace failed at frame %u sample %u\n", f, j);
        goto fail;
      }
    }
  }

  for (uint32_t f = 0; f < frame_count; f++) free(packets[f]);
  opus_encoder_destroy(enc);
  free(pcm); free(packet); free(packets); free(packet_lengths); free(statuses); free(ranges);
  return 0;

fail:
  if (enc != NULL) opus_encoder_destroy(enc);
  for (uint32_t f = 0; f < frame_count; f++) free(packets[f]);
  free(pcm); free(packet); free(packets); free(packet_lengths); free(statuses); free(ranges);
  return 1;
}
