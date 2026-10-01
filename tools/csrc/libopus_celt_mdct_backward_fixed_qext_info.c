#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "celt/arch.h"
#include "celt/mdct.h"

static const opus_int16 eband5ms[] = {0};
static const unsigned char band_allocation[] = {0};
#include "celt/static_modes_fixed.h"

#if !defined(FIXED_POINT) || !defined(ENABLE_QEXT)
#error "QEXT inverse MDCT oracle requires FIXED_POINT + ENABLE_QEXT"
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
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 1;
#endif
  unsigned char magic[4];
  uint32_t version, mode, shift, stride, i, input_count, initial_count, output_count;
  const CELTMode *m;
  kiss_fft_scalar *in, *out;
  if (fread(magic, 1, 4, stdin) != 4 || magic[0] != 'G' || magic[1] != 'Q' ||
      magic[2] != 'B' || magic[3] != 'I' || !read_u32(&version) || version != 1 ||
      !read_u32(&mode) || !read_u32(&shift) || !read_u32(&stride) ||
      !read_u32(&input_count) || !read_u32(&initial_count)) return 1;
  if (mode == 0) m = &mode48000_960_120;
  else if (mode == 1) m = &mode96000_1920_240;
  else return 1;
  if (shift > 3 || stride == 0 || stride > 8) return 1;
  {
    uint32_t n = (uint32_t)m->mdct.n >> shift;
    uint32_t n2 = n >> 1;
    uint32_t expected = stride * (n2 - 1) + 1;
    if (input_count != expected) return 1;
    output_count = n / 2 + (uint32_t)m->overlap / 2;
  }
  if (initial_count != output_count) return 1;
  in = (kiss_fft_scalar *)calloc(input_count, sizeof(*in));
  out = (kiss_fft_scalar *)calloc(output_count, sizeof(*out));
  if (in == NULL || out == NULL) return 1;
  for (i = 0; i < input_count; i++) {
    uint32_t v;
    if (!read_u32(&v)) return 1;
    in[i] = (kiss_fft_scalar)(int32_t)v;
  }
  for (i = 0; i < output_count; i++) {
    uint32_t v;
    if (!read_u32(&v)) return 1;
    out[i] = (kiss_fft_scalar)(int32_t)v;
  }
  clt_mdct_backward_c(&m->mdct, in, out, m->window, m->overlap,
      (int)shift, (int)stride, 0);
  if (!write_exact("GQBO", 4) || !write_u32(1) || !write_u32(output_count)) return 1;
  for (i = 0; i < output_count; i++) {
    if (!write_u32((uint32_t)(int32_t)out[i])) return 1;
  }
  free(out);
  free(in);
  return 0;
}
