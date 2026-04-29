#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <math.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#define INPUT_MAGIC "GCUI"
#define OUTPUT_MAGIC "GCUO"

#define DECODE_BUFFER_SIZE 2048
#define PLC_UPDATE_FRAMES 4
#define FRAME_SIZE 160
#define PLC_UPDATE_SAMPLES (PLC_UPDATE_FRAMES * FRAME_SIZE)
#define SINC_ORDER 48
#define PREEMPHASIS (0.85000610f)
#define UPDATE_OFFSET (DECODE_BUFFER_SIZE - SINC_ORDER - 1 - 3 * (PLC_UPDATE_SAMPLES - 1))

static const float sinc_filter[SINC_ORDER + 1] = {
    4.2931e-05f, -0.000190293f, -0.000816132f, -0.000637162f, 0.00141662f, 0.00354764f, 0.00184368f, -0.00428274f,
    -0.00856105f, -0.0034003f, 0.00930201f, 0.0159616f, 0.00489785f, -0.0169649f, -0.0259484f, -0.00596856f,
    0.0286551f, 0.0405872f, 0.00649994f, -0.0509284f, -0.0716655f, -0.00665212f, 0.134336f, 0.278927f,
    0.339995f, 0.278927f, 0.134336f, -0.00665212f, -0.0716655f, -0.0509284f, 0.00649994f, 0.0405872f,
    0.0286551f, -0.00596856f, -0.0259484f, -0.0169649f, 0.00489785f, 0.0159616f, 0.00930201f, -0.0034003f,
    -0.00856105f, -0.00428274f, 0.00184368f, 0.00354764f, 0.00141662f, -0.000637162f, -0.000816132f, -0.000190293f,
    4.2931e-05f
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

static int read_bits_array(float *dst, int count) {
  int i;
  for (i = 0; i < count; i++) {
    uint32_t bits;
    if (!read_exact(&bits, sizeof(bits))) return 0;
    memcpy(&dst[i], &bits, sizeof(bits));
  }
  return 1;
}

static int write_int16_array(const int16_t *src, int count) {
  int i;
  for (i = 0; i < count; i++) {
    if (!write_exact(&src[i], sizeof(src[i]))) return 0;
  }
  return 1;
}

static int write_f32(float v) {
  uint32_t bits;
  memcpy(&bits, &v, sizeof(bits));
  return write_exact(&bits, sizeof(bits));
}

static int16_t quantize_raw_pcm16_like(float x) {
  if (x < -32767.f) x = -32767.f;
  if (x > 32767.f) x = 32767.f;
  return (int16_t)lrintf(x);
}

int main(void) {
  char magic[4];
  uint32_t version;
  int32_t channels;
  float history[2 * DECODE_BUFFER_SIZE];
  float buf48k[DECODE_BUFFER_SIZE];
  float preemph_mem = 0;
  int16_t out[PLC_UPDATE_SAMPLES];
  int i;

  if (!set_binary_stdio()) {
    fprintf(stderr, "failed to set binary stdio mode\n");
    return 1;
  }
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) {
    fprintf(stderr, "invalid input magic\n");
    return 1;
  }
  if (!read_exact(&version, sizeof(version)) || version != 1) {
    fprintf(stderr, "unsupported input version\n");
    return 1;
  }
  if (!read_exact(&channels, sizeof(channels)) || (channels != 1 && channels != 2)) {
    fprintf(stderr, "invalid channels\n");
    return 1;
  }
  if (!read_bits_array(history, channels * DECODE_BUFFER_SIZE)) {
    fprintf(stderr, "failed to read decode history\n");
    return 1;
  }

  if (channels == 1) {
    for (i = 0; i < DECODE_BUFFER_SIZE; i++) {
      buf48k[i] = history[i];
    }
  } else {
    for (i = 0; i < DECODE_BUFFER_SIZE; i++) {
      buf48k[i] = .5f * (history[i] + history[DECODE_BUFFER_SIZE + i]);
    }
  }

  for (i = 1; i < DECODE_BUFFER_SIZE; i++) {
    buf48k[i] += PREEMPHASIS * buf48k[i - 1];
  }
  preemph_mem = (1.0f / 32768.0f) * buf48k[DECODE_BUFFER_SIZE - 1];
  for (i = 0; i < PLC_UPDATE_SAMPLES; i++) {
    int j;
    float sum = 0;
    int base = 3 * i + UPDATE_OFFSET;
    for (j = 0; j < SINC_ORDER + 1; j++) {
      sum += buf48k[base + j] * sinc_filter[j];
    }
    out[i] = quantize_raw_pcm16_like(sum);
  }

  if (!write_exact(OUTPUT_MAGIC, sizeof(magic)) ||
      !write_exact(&version, sizeof(version)) ||
      !write_f32(preemph_mem) ||
      !write_int16_array(out, PLC_UPDATE_SAMPLES)) {
    fprintf(stderr, "failed to write output\n");
    return 1;
  }
  return 0;
}
