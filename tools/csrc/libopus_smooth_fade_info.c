#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "celt/arch.h"
#include "celt/modes.h"

#define INPUT_MAGIC "GSFI"
#define OUTPUT_MAGIC "GSFO"

CELTMode *opus_custom_mode_create(opus_int32 Fs, int frame_size, int *error);

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
  unsigned char b[4];
  if (!read_exact(b, 4)) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) | ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int write_u32(uint32_t v) {
  unsigned char b[4];
  b[0] = (unsigned char)(v & 0xFF);
  b[1] = (unsigned char)((v >> 8) & 0xFF);
  b[2] = (unsigned char)((v >> 16) & 0xFF);
  b[3] = (unsigned char)((v >> 24) & 0xFF);
  return write_exact(b, 4);
}

static int read_float(float *out) {
  union {
    uint32_t u;
    float f;
  } bits;
  if (!read_u32(&bits.u)) return 0;
  *out = bits.f;
  return 1;
}

static int write_float(float v) {
  union {
    float f;
    uint32_t u;
  } bits;
  bits.f = v;
  return write_u32(bits.u);
}

static int read_float_array(float *dst, uint32_t n) {
  uint32_t i;
  for (i = 0; i < n; i++) {
    if (!read_float(&dst[i])) return 0;
  }
  return 1;
}

static int write_float_array(const float *src, uint32_t n) {
  uint32_t i;
  for (i = 0; i < n; i++) {
    if (!write_float(src[i])) return 0;
  }
  return 1;
}

static void libopus_smooth_fade(const opus_res *in1, const opus_res *in2,
      opus_res *out, int overlap, int channels,
      const celt_coef *window, opus_int32 Fs)
{
   int i, c;
   int inc = 48000/Fs;
   for (c=0;c<channels;c++)
   {
      for (i=0;i<overlap;i++)
      {
         opus_val16 w = COEF2VAL16(window[i*inc]);
         w = MULT16_16_Q15(w, w);
         out[i*channels+c] = SHR32(MAC16_16(MULT16_16(w,in2[i*channels+c]),
                                   Q15ONE-w, in1[i*channels+c]), 15);
      }
   }
}

int main(void) {
  char magic[4];
  uint32_t version;
  uint32_t sample_rate;
  uint32_t channels;
  uint32_t overlap;
  uint32_t count;
  int err = 0;
  CELTMode *mode = NULL;
  opus_res *in1 = NULL;
  opus_res *in2 = NULL;
  opus_res *out = NULL;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, 4) || memcmp(magic, INPUT_MAGIC, 4) != 0) return 1;
  if (!read_u32(&version) || version != 1 ||
      !read_u32(&sample_rate) ||
      !read_u32(&channels) ||
      !read_u32(&overlap)) {
    return 1;
  }
  if ((sample_rate != 8000 && sample_rate != 12000 && sample_rate != 16000 &&
       sample_rate != 24000 && sample_rate != 48000) ||
      (channels != 1 && channels != 2) ||
      overlap == 0 || overlap > 120) {
    return 1;
  }
  if (overlap > UINT32_MAX / channels) return 1;
  count = overlap * channels;
  in1 = (opus_res *)malloc((size_t)count * sizeof(*in1));
  in2 = (opus_res *)malloc((size_t)count * sizeof(*in2));
  out = (opus_res *)calloc((size_t)count, sizeof(*out));
  if (in1 == NULL || in2 == NULL || out == NULL) goto fail;
  if (!read_float_array(in1, count) || !read_float_array(in2, count)) goto fail;

  mode = opus_custom_mode_create(48000, 960, &err);
  if (mode == NULL) goto fail;
  libopus_smooth_fade(in1, in2, out, (int)overlap, (int)channels, mode->window, (opus_int32)sample_rate);

  if (!write_exact(OUTPUT_MAGIC, 4) ||
      !write_u32(1) ||
      !write_u32(count) ||
      !write_float_array(out, count)) {
    goto fail;
  }

  free(in1);
  free(in2);
  free(out);
  return 0;

fail:
  free(in1);
  free(in2);
  free(out);
  return 1;
}
