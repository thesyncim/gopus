#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <math.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "celt/float_cast.h"
#include "celt/mathops.h"
#include "celt/cpu_support.h"
#include "silk/float/SigProc_FLP.h"

#define INPUT_MAGIC "GFQI"
#define OUTPUT_MAGIC "GFQO"

enum {
  MODE_FLOAT2INT16 = 0,
  MODE_OSCE_OUTPUT_SCALE = 1,
  MODE_FARGAN_SYNTH_INT = 2,
  MODE_CELT_RAW_32767_FLOAT2INT = 3,
  MODE_CELT_FLOAT2INT16_DISPATCH = 4,
  MODE_SILK_FLOAT2SHORT_ARRAY = 5,
  MODE_SILK_FLOAT2INT_SCALE = 6,
  MODE_SILK_SHORT2FLOAT_ARRAY = 7
};

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

static int16_t osce_output_scale(float x) {
  float tmp = 32768.f * x;
  if (tmp > 32767.f) tmp = 32767.f;
  if (tmp < -32767.f) tmp = -32767.f;
  return (int16_t)float2int(tmp);
}

static int16_t fargan_synth_int(float x) {
  float tmp = 32768.f * x;
  if (tmp > 32767.f) tmp = 32767.f;
  if (tmp < -32767.f) tmp = -32767.f;
  return (int16_t)floor(.5 + (double)tmp);
}

static int16_t celt_raw_32767_float2int(float x) {
  if (x > 32767.f) x = 32767.f;
  if (x < -32767.f) x = -32767.f;
  return (int16_t)float2int(x);
}

static int16_t silk_float2short_sample(float x) {
  opus_int16 y;
  silk_float2short_array(&y, &x, 1);
  return y;
}

static int16_t convert_sample(uint32_t mode, float x) {
  switch (mode) {
    case MODE_FLOAT2INT16:
      return FLOAT2INT16(x);
    case MODE_OSCE_OUTPUT_SCALE:
      return osce_output_scale(x);
    case MODE_FARGAN_SYNTH_INT:
      return fargan_synth_int(x);
    case MODE_CELT_RAW_32767_FLOAT2INT:
      return celt_raw_32767_float2int(x);
    case MODE_SILK_FLOAT2SHORT_ARRAY:
      return silk_float2short_sample(x);
    default:
      return 0;
  }
}

static int convert_dispatch(uint32_t count) {
  float *in = NULL;
  int16_t *out = NULL;
  uint32_t i;

  if (count == 0) return 1;
  in = (float *)malloc(count * sizeof(*in));
  out = (int16_t *)malloc(count * sizeof(*out));
  if (in == NULL || out == NULL) {
    free(in);
    free(out);
    return 0;
  }
  for (i = 0; i < count; i++) {
    uint32_t bits;
    if (!read_u32(&bits)) {
      free(in);
      free(out);
      return 0;
    }
    memcpy(&in[i], &bits, sizeof(in[i]));
  }
  celt_float2int16(in, out, (int)count, opus_select_arch());
  if (!write_exact(out, count * sizeof(*out))) {
    free(in);
    free(out);
    return 0;
  }
  free(in);
  free(out);
  return 1;
}

static int convert_scaled_float2int(uint32_t count) {
  uint32_t scale_bits;
  float scale;
  uint32_t i;

  if (!read_u32(&scale_bits)) return 0;
  memcpy(&scale, &scale_bits, sizeof(scale));

  for (i = 0; i < count; i++) {
    uint32_t bits;
    float x;
    int32_t y;
    if (!read_u32(&bits)) return 0;
    memcpy(&x, &bits, sizeof(x));
    y = silk_float2int(x * scale);
    if (!write_exact(&y, sizeof(y))) return 0;
  }
  return 1;
}

static int convert_short2float(uint32_t count) {
  int16_t *in = NULL;
  float *out = NULL;
  uint32_t i;

  if (count == 0) return 1;
  in = (int16_t *)malloc(count * sizeof(*in));
  out = (float *)malloc(count * sizeof(*out));
  if (in == NULL || out == NULL) {
    free(in);
    free(out);
    return 0;
  }
  for (i = 0; i < count; i++) {
    if (!read_exact(&in[i], sizeof(in[i]))) {
      free(in);
      free(out);
      return 0;
    }
  }
  silk_short2float_array(out, in, (opus_int32)count);
  for (i = 0; i < count; i++) {
    uint32_t bits;
    memcpy(&bits, &out[i], sizeof(bits));
    if (!write_u32(bits)) {
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
  uint32_t mode;
  uint32_t count;
  uint32_t i;

  if (!set_binary_stdio()) {
    fprintf(stderr, "failed to set binary stdio mode\n");
    return 1;
  }
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) {
    fprintf(stderr, "invalid input magic\n");
    return 1;
  }
  if (!read_u32(&version) || version != 1 || !read_u32(&mode) || !read_u32(&count)) {
    fprintf(stderr, "failed to read header\n");
    return 1;
  }
  if (mode > MODE_SILK_SHORT2FLOAT_ARRAY) {
    fprintf(stderr, "invalid mode\n");
    return 1;
  }

  if (!write_exact(OUTPUT_MAGIC, sizeof(magic)) || !write_u32(1) || !write_u32(count)) {
    fprintf(stderr, "failed to write output header\n");
    return 1;
  }
  if (mode == MODE_CELT_FLOAT2INT16_DISPATCH) {
    if (!convert_dispatch(count)) {
      fprintf(stderr, "failed to convert dispatch vector\n");
      return 1;
    }
    return 0;
  }
  if (mode == MODE_SILK_FLOAT2INT_SCALE) {
    if (!convert_scaled_float2int(count)) {
      fprintf(stderr, "failed to convert scaled float2int vector\n");
      return 1;
    }
    return 0;
  }
  if (mode == MODE_SILK_SHORT2FLOAT_ARRAY) {
    if (!convert_short2float(count)) {
      fprintf(stderr, "failed to convert short2float vector\n");
      return 1;
    }
    return 0;
  }

  for (i = 0; i < count; i++) {
    uint32_t bits;
    float x;
    int16_t y;
    if (!read_u32(&bits)) {
      fprintf(stderr, "truncated input\n");
      return 1;
    }
    memcpy(&x, &bits, sizeof(x));
    y = convert_sample(mode, x);
    if (!write_exact(&y, sizeof(y))) {
      fprintf(stderr, "failed to write output\n");
      return 1;
    }
  }
  return 0;
}
