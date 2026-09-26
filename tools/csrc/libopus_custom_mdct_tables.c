/* Actual mode-owned tables and transforms from the selected custom archive. */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif
#ifdef HAVE_CONFIG_H
#include "config.h"
#endif
#include "opus_custom.h"
#include "modes.h"
#include "mdct.h"
#include "cpu_support.h"

static uint32_t read_u32(void) {
  unsigned char b[4];
  if (fread(b, 1, 4, stdin) != 4) exit(1);
  return (uint32_t)b[0] | ((uint32_t)b[1] << 8) | ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
}
static void write_u32(uint32_t v) {
  unsigned char b[4] = {(unsigned char)v, (unsigned char)(v >> 8), (unsigned char)(v >> 16), (unsigned char)(v >> 24)};
  if (fwrite(b, 1, 4, stdout) != 4) exit(1);
}
static float read_float(void) {
  uint32_t b = read_u32(); float f; memcpy(&f, &b, 4); return f;
}
static void write_float(float f) {
  uint32_t b; memcpy(&b, &f, 4); write_u32(b);
}
int main(void) {
#ifdef _WIN32
  _setmode(_fileno(stdin), _O_BINARY); _setmode(_fileno(stdout), _O_BINARY);
#endif
  char magic[4];
  if (fread(magic, 1, 4, stdin) != 4 || memcmp(magic, "GMTI", 4) || read_u32() != 1) return 1;
  int fs = (int)read_u32(), frame = (int)read_u32(), error = 0;
  if (fs < 8000 || fs > 96000 || frame < 40 || frame > 1024) return 1;
  OpusCustomMode *mode = opus_custom_mode_create(fs, frame, &error);
  if (!mode || error) return 1;
  if (fwrite("GMTO", 1, 4, stdout) != 4) return 1;
  write_u32(1); write_u32((uint32_t)mode->overlap); write_u32((uint32_t)mode->maxLM);
  for (int i = 0; i < mode->overlap; i++) write_float(mode->window[i]);
  int trig_offset = 0;
  for (int shift = 0; shift <= mode->maxLM; shift++) {
    int block = frame >> shift;
    const kiss_fft_state *fft = mode->mdct.kfft[shift];
    if (fft->nfft != block / 2) return 1;
    write_u32((uint32_t)fft->nfft);
    for (int i = 0; i < fft->nfft; i++) {
      int wi = i << (fft->shift < 0 ? 0 : fft->shift);
      write_float(fft->twiddles[wi].r); write_float(fft->twiddles[wi].i);
    }
    for (int i = 0; i < block; i++) write_float(mode->mdct.trig[trig_offset + i]);
    trig_offset += block;
    if (block > 1024 || block + mode->overlap > 2048) return 1;
    float input[2048], coeffs[1024], freq[1024], output[2048] = {0};
    for (int i = 0; i < block + mode->overlap; i++) input[i] = read_float();
    for (int i = 0; i < mode->overlap; i++) output[i] = read_float();
    for (int i = 0; i < block; i++) freq[i] = read_float();
    clt_mdct_forward(&mode->mdct, input, coeffs, mode->window, mode->overlap, shift, 1, opus_select_arch());
    clt_mdct_backward(&mode->mdct, freq, output, mode->window, mode->overlap, shift, 1, opus_select_arch());
    for (int i = 0; i < block; i++) write_float(coeffs[i]);
    for (int i = 0; i < block + mode->overlap; i++) write_float(output[i]);
  }
  opus_custom_mode_destroy(mode);
  return ferror(stdout) ? 1 : 0;
}
