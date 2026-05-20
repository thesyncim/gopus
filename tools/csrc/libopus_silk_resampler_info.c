#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#define CELT_C
#include "silk/SigProc_FIX.h"

#define INPUT_MAGIC "GSRI"
#define OUTPUT_MAGIC "GSRO"

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

static int valid_decoder_rate(uint32_t fs_in, uint32_t fs_out) {
  if (fs_in != 8000 && fs_in != 12000 && fs_in != 16000) return 0;
  if (fs_out != 8000 && fs_out != 12000 && fs_out != 16000 && fs_out != 24000 && fs_out != 48000) return 0;
  return 1;
}

static int eval_record(void) {
  uint32_t fs_in;
  uint32_t fs_out;
  uint32_t frame_samples;
  uint32_t frame_count;
  uint32_t frame;
  uint32_t out_samples;
  uint32_t total_out;
  opus_int16 *in = NULL;
  opus_int16 *out = NULL;
  silk_resampler_state_struct state;

  if (!read_u32(&fs_in) || !read_u32(&fs_out) || !read_u32(&frame_samples) || !read_u32(&frame_count)) return 0;
  if (!valid_decoder_rate(fs_in, fs_out)) return 0;
  if (frame_samples < fs_in / 1000 || frame_samples > fs_in * 60 / 1000 || frame_count == 0 || frame_count > 64) return 0;
  if (((uint64_t)frame_samples * fs_out) % fs_in != 0) return 0;
  out_samples = (uint32_t)(((uint64_t)frame_samples * fs_out) / fs_in);
  if (out_samples == 0 || out_samples > fs_out * 60 / 1000) return 0;
  if ((uint64_t)out_samples * frame_count > UINT32_MAX) return 0;
  total_out = out_samples * frame_count;

  in = (opus_int16 *)malloc(frame_samples * sizeof(*in));
  out = (opus_int16 *)malloc(out_samples * sizeof(*out));
  if (in == NULL || out == NULL) {
    free(in);
    free(out);
    return 0;
  }
  if (silk_resampler_init(&state, (opus_int32)fs_in, (opus_int32)fs_out, 0) != 0) {
    free(in);
    free(out);
    return 0;
  }
  if (!write_u32(total_out)) {
    free(in);
    free(out);
    return 0;
  }
  for (frame = 0; frame < frame_count; frame++) {
    if (!read_exact(in, frame_samples * sizeof(*in))) {
      free(in);
      free(out);
      return 0;
    }
    if (silk_resampler(&state, out, in, (opus_int32)frame_samples) != 0) {
      free(in);
      free(out);
      return 0;
    }
    if (!write_exact(out, out_samples * sizeof(*out))) {
      free(in);
      free(out);
      return 0;
    }
  }
  free(in);
  free(out);
  return 1;
}

int main(void) {
  char magic[4];
  uint32_t version;
  uint32_t count;
  uint32_t i;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) return 1;
  if (!read_u32(&version) || version != 1 || !read_u32(&count)) return 1;

  if (!write_exact(OUTPUT_MAGIC, sizeof(magic)) || !write_u32(1) || !write_u32(count)) return 1;
  for (i = 0; i < count; i++) {
    if (!eval_record()) return 1;
  }
  return 0;
}
