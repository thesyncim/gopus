/* Read dynamically generated FFT tables from the selected libopus archive. */
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
#include "kiss_fft.h"
#include "cpu_support.h"

static uint32_t read_u32(void) {
  unsigned char b[4];
  if (fread(b, 1, 4, stdin) != 4) exit(1);
  return (uint32_t)b[0] | ((uint32_t)b[1] << 8) |
         ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
}
static void write_u32(uint32_t v) {
  unsigned char b[4] = {(unsigned char)v, (unsigned char)(v >> 8),
                      (unsigned char)(v >> 16), (unsigned char)(v >> 24)};
  if (fwrite(b, 1, 4, stdout) != 4) exit(1);
}
static void write_float(float f) {
  uint32_t bits;
  memcpy(&bits, &f, sizeof(bits));
  write_u32(bits);
}
int main(void) {
#ifdef _WIN32
  _setmode(_fileno(stdin), _O_BINARY);
  _setmode(_fileno(stdout), _O_BINARY);
#endif
  char magic[4];
  if (fread(magic, 1, 4, stdin) != 4 || memcmp(magic, "GFTI", 4) || read_u32() != 1) return 1;
  uint32_t nfft = read_u32();
  if (nfft < 1 || nfft > 512) return 1;
  int arch = opus_select_arch();
  kiss_fft_state *state = opus_fft_alloc_twiddles((int)nfft, NULL, NULL, NULL, arch);
  if (state == NULL || state->nfft != (int)nfft) return 1;
  if (fwrite("GFTO", 1, 4, stdout) != 4) return 1;
  write_u32(1);
  write_u32(nfft);
  for (uint32_t i = 0; i < nfft; i++) {
    write_float(state->twiddles[i].r);
    write_float(state->twiddles[i].i);
  }
  opus_fft_free(state, arch);
  return ferror(stdout) ? 1 : 0;
}
