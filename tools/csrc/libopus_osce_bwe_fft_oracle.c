#include <stdint.h>
#include <math.h>
#include <stdio.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#ifdef HAVE_CONFIG_H
#include "config.h"
#endif

#include "freq.h"

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

int main(void) {
  float in[WINDOW_SIZE];
  float mag[FREQ_SIZE];
  float bands[32];
  float logs[32];
  kiss_fft_cpx out[FREQ_SIZE];
  static const int centers[32] = {
    0,5,10,15,20,25,30,35,40,45,50,55,60,65,70,75,
    80,85,90,95,100,105,110,115,120,125,130,135,140,145,150,160
  };
  static const float weights[32] = {
    0.333333333f, 0.200000000f, 0.200000000f, 0.200000000f,
    0.200000000f, 0.200000000f, 0.200000000f, 0.200000000f,
    0.200000000f, 0.200000000f, 0.200000000f, 0.200000000f,
    0.200000000f, 0.200000000f, 0.200000000f, 0.200000000f,
    0.200000000f, 0.200000000f, 0.200000000f, 0.200000000f,
    0.200000000f, 0.200000000f, 0.200000000f, 0.200000000f,
    0.200000000f, 0.200000000f, 0.200000000f, 0.200000000f,
    0.200000000f, 0.200000000f, 0.133333333f, 0.181818182f
  };
  static const char tag[8] = {'O','S','C','E','F','F','T','\0'};
  const int32_t header[5] = {2, FREQ_SIZE, FREQ_SIZE, 32, 32};

  if (!set_binary_stdio()) return 1;
  if (fread(in, sizeof(float), WINDOW_SIZE, stdin) != WINDOW_SIZE) return 2;
  forward_transform(out, in);

  for (int k = 0; k < FREQ_SIZE; k++) {
    mag[k] = WINDOW_SIZE * sqrt(out[k].r * out[k].r + out[k].i * out[k].i);
  }
  for (int b = 0; b < 32; b++) bands[b] = 0;
  for (int b = 0; b < 31; b++) {
    bands[b + 1] = 0;
    for (int i = centers[b]; i < centers[b + 1]; i++) {
      float frac = (float)(centers[b + 1] - i) / (centers[b + 1] - centers[b]);
      bands[b] += weights[b] * frac * mag[i];
      bands[b + 1] += weights[b + 1] * (1 - frac) * mag[i];
    }
  }
  bands[31] += weights[31] * mag[centers[31]];
  for (int b = 0; b < 32; b++) logs[b] = log(bands[b] + 1e-9);

  if (fwrite(tag, 1, sizeof(tag), stdout) != sizeof(tag)) return 3;
  if (fwrite(header, sizeof(header[0]), 5, stdout) != 5) return 4;
  if (fwrite(out, sizeof(out[0]), FREQ_SIZE, stdout) != FREQ_SIZE) return 5;
  if (fwrite(mag, sizeof(mag[0]), FREQ_SIZE, stdout) != FREQ_SIZE) return 6;
  if (fwrite(bands, sizeof(bands[0]), 32, stdout) != 32) return 7;
  if (fwrite(logs, sizeof(logs[0]), 32, stdout) != 32) return 8;
  return 0;
}
