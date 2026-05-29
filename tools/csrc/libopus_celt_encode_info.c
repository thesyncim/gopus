#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "opus_defines.h"
#include "celt/celt.h"
#include "celt/entenc.h"

#define INPUT_MAGIC "GCEI"
#define OUTPUT_MAGIC "GCEO"
#define STREAM_INPUT_MAGIC "GCSI"
#define STREAM_OUTPUT_MAGIC "GCSO"

/* celt_encode_with_ec is the internal CELT codec entry that gopus' standalone
 * CELT encoder mirrors (with the top-level dc_reject/delay-compensation/
 * lsb-depth stages disabled). We drive it through a standard 48000 mode
 * obtained via celt_encoder_init, exactly as src/opus_encoder.c does.
 * Reference: libopus celt/celt_encoder.c celt_encode_with_ec(). */
int celt_encode_with_ec(CELTEncoder *st, const opus_res *pcm, int frame_size,
                        unsigned char *compressed, int nbCompressedBytes, ec_enc *enc);

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

static int read_exact(void *dst, size_t size) {
  return fread(dst, 1, size, stdin) == size;
}

static int write_exact(const void *src, size_t size) {
  return fwrite(src, 1, size, stdout) == size;
}

static int read_u32(uint32_t *out) {
  return read_exact(out, sizeof(*out));
}

static int write_u32(uint32_t value) {
  return write_exact(&value, sizeof(value));
}

static int read_f32(float *out) {
  uint32_t bits;
  if (!read_u32(&bits)) return 0;
  memcpy(out, &bits, sizeof(bits));
  return 1;
}

static void configure_encoder(CELTEncoder *st, int complexity, int32_t bitrate) {
  /* Match gopus standalone CELT defaults. */
  opus_custom_encoder_ctl(st, OPUS_SET_COMPLEXITY(complexity));
  opus_custom_encoder_ctl(st, OPUS_SET_VBR(0));
  opus_custom_encoder_ctl(st, OPUS_SET_VBR_CONSTRAINT(0));
  opus_custom_encoder_ctl(st, OPUS_SET_BITRATE((opus_int32)bitrate));
  opus_custom_encoder_ctl(st, OPUS_SET_LSB_DEPTH(24));
  opus_custom_encoder_ctl(st, CELT_SET_SIGNALLING(0));
}

/* run_stream: one persistent encoder, multiple frames, no per-frame reset.
 * Exercises inter-frame CELT state continuity. */
static int run_stream(void) {
  uint32_t n_frames;
  uint32_t channels;
  uint32_t frame_size;
  uint32_t target_bytes;
  int32_t bitrate;
  int32_t complexity;
  uint32_t f;
  uint32_t n_floats;
  int size;
  CELTEncoder *st;
  unsigned char *packet;
  opus_res *pcm;

  if (!read_u32(&n_frames)) return 1;
  if (!read_u32(&channels) || !read_u32(&frame_size) || !read_u32(&target_bytes) ||
      !read_u32((uint32_t *)&bitrate) || !read_u32((uint32_t *)&complexity)) {
    return 1;
  }
  if (channels < 1 || channels > 2) return 1;

  if (!write_exact(STREAM_OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(n_frames)) return 1;

  size = celt_encoder_get_size((int)channels);
  st = (CELTEncoder *)malloc((size_t)size);
  if (st == NULL) return 1;
  if (celt_encoder_init(st, 48000, (int)channels, opus_select_arch()) != OPUS_OK) {
    free(st);
    return 1;
  }
  configure_encoder(st, (int)complexity, bitrate);

  n_floats = frame_size * channels;
  pcm = (opus_res *)malloc(sizeof(opus_res) * (n_floats ? n_floats : 1));
  packet = (unsigned char *)calloc(target_bytes ? target_bytes : 1, 1);
  if (pcm == NULL || packet == NULL) { free(pcm); free(packet); free(st); return 1; }

  for (f = 0; f < n_frames; f++) {
    uint32_t i;
    int ret;
    for (i = 0; i < n_floats; i++) {
      float v;
      if (!read_f32(&v)) { free(pcm); free(packet); free(st); return 1; }
      pcm[i] = (opus_res)v;
    }
    ret = celt_encode_with_ec(st, pcm, (int)frame_size, packet, (int)target_bytes, NULL);
    if (ret < 0) { free(pcm); free(packet); free(st); return 1; }
    if (!write_u32((uint32_t)ret) || !write_exact(packet, (size_t)ret)) {
      free(pcm); free(packet); free(st);
      return 1;
    }
  }
  free(pcm);
  free(packet);
  free(st);
  return 0;
}

int main(void) {
  char magic[4];
  uint32_t version;
  uint32_t count;
  uint32_t case_idx;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, sizeof(magic))) return 1;
  if (memcmp(magic, STREAM_INPUT_MAGIC, sizeof(magic)) == 0) {
    if (!read_u32(&version) || version != 1) return 1;
    return run_stream();
  }
  if (memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) return 1;
  if (!read_u32(&version) || version != 1 || !read_u32(&count)) return 1;

  if (!write_exact(OUTPUT_MAGIC, sizeof(magic)) || !write_u32(1) || !write_u32(count)) return 1;

  for (case_idx = 0; case_idx < count; case_idx++) {
    uint32_t channels;
    uint32_t frame_size;
    uint32_t target_bytes;
    int32_t bitrate;
    int32_t complexity;
    uint32_t i;
    uint32_t n_floats;
    opus_res *pcm = NULL;
    unsigned char *packet = NULL;
    CELTEncoder *st = NULL;
    int size;
    int ret;

    if (!read_u32(&channels) || !read_u32(&frame_size) || !read_u32(&target_bytes) ||
        !read_u32((uint32_t *)&bitrate) || !read_u32((uint32_t *)&complexity)) {
      return 1;
    }
    if (channels < 1 || channels > 2) return 1;

    n_floats = frame_size * channels;
    pcm = (opus_res *)malloc(sizeof(opus_res) * (n_floats ? n_floats : 1));
    packet = (unsigned char *)calloc(target_bytes ? target_bytes : 1, 1);
    if (pcm == NULL || packet == NULL) { free(pcm); free(packet); return 1; }

    for (i = 0; i < n_floats; i++) {
      float v;
      if (!read_f32(&v)) { free(pcm); free(packet); return 1; }
      pcm[i] = (opus_res)v;
    }

    size = celt_encoder_get_size((int)channels);
    st = (CELTEncoder *)malloc((size_t)size);
    if (st == NULL) { free(pcm); free(packet); return 1; }

    if (celt_encoder_init(st, 48000, (int)channels, opus_select_arch()) != OPUS_OK) {
      free(pcm); free(packet); free(st); return 1;
    }
    configure_encoder(st, (int)complexity, bitrate);

    ret = celt_encode_with_ec(st, pcm, (int)frame_size, packet, (int)target_bytes, NULL);
    if (ret < 0) {
      free(pcm); free(packet); free(st); return 1;
    }
    if (!write_u32((uint32_t)ret)) { free(pcm); free(packet); free(st); return 1; }
    if (!write_exact(packet, (size_t)ret)) { free(pcm); free(packet); free(st); return 1; }

    free(pcm);
    free(packet);
    free(st);
  }
  return 0;
}
