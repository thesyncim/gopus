#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "celt/arch.h"
#include "celt/kiss_fft.h"

static const opus_int16 eband5ms[] = {0};
static const unsigned char band_allocation[] = {0};
#include "celt/static_modes_fixed.h"

#if !defined(FIXED_POINT) || !defined(ENABLE_QEXT)
#error "QEXT FFT oracle requires FIXED_POINT + ENABLE_QEXT"
#endif

static int read_exact(void *dst, size_t n) { return fread(dst, 1, n, stdin) == n; }
static int write_exact(const void *src, size_t n) { return fwrite(src, 1, n, stdout) == n; }

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact(b, sizeof(b))) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) |
      ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int write_u32(uint32_t value) {
  unsigned char b[4] = {
    (unsigned char)value, (unsigned char)(value >> 8),
    (unsigned char)(value >> 16), (unsigned char)(value >> 24)
  };
  return write_exact(b, sizeof(b));
}

int main(void) {
  unsigned char magic[4];
  uint32_t version, mode, i, n;
  const CELTMode *m;
  kiss_fft_cpx *in, *out;
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 1;
#endif
  if (fread(magic, 1, 4, stdin) != 4 || magic[0] != 'G' || magic[1] != 'Q' ||
      magic[2] != 'F' || magic[3] != 'I' || !read_u32(&version) || version != 1 ||
      !read_u32(&mode)) return 1;
  if (mode == 0) m = &mode48000_960_120;
  else if (mode == 1) m = &mode96000_1920_240;
  else return 1;
  n = (uint32_t)m->mdct.kfft[0]->nfft;
  in = (kiss_fft_cpx *)calloc(n, sizeof(*in));
  out = (kiss_fft_cpx *)calloc(n, sizeof(*out));
  if (in == NULL || out == NULL) return 1;
  for (i = 0; i < n; i++) {
    uint32_t r, im;
    if (!read_u32(&r) || !read_u32(&im)) return 1;
    in[i].r = (kiss_fft_scalar)(int32_t)r;
    in[i].i = (kiss_fft_scalar)(int32_t)im;
  }
  opus_fft_c(m->mdct.kfft[0], in, out);
  if (!write_exact("GQFO", 4) || !write_u32(1) || !write_u32(n)) return 1;
  for (i = 0; i < n; i++) {
    if (!write_u32((uint32_t)(int32_t)out[i].r) ||
        !write_u32((uint32_t)(int32_t)out[i].i)) return 1;
  }
  free(out);
  free(in);
  return 0;
}
