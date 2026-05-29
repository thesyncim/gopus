#include <stdint.h>
#include <stdio.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "celt/_kiss_fft_guts.h"
#include "celt/arch.h"

/* Oracle helper for the libopus FIXED_POINT integer KISS-FFT butterflies.
 * Built against the --enable-fixed-point reference tree so config.h defines
 * FIXED_POINT (without ENABLE_QEXT). kf_bfly2 in celt/kiss_fft.c is static, so
 * its non-CUSTOM_MODES body is reproduced verbatim here to drive the same
 * integer arithmetic the library uses. The macros (S_MUL, ADD32_ovflw, ...) are
 * the reference macros pulled in via _kiss_fft_guts.h. */

#define INPUT_MAGIC "GKFI"
#define OUTPUT_MAGIC "GKFO"

enum {
  MODE_KF_BFLY2 = 0
};

/* Verbatim copy of the celt/kiss_fft.c kf_bfly2() non-CUSTOM_MODES path. */
static void ref_kf_bfly2(kiss_fft_cpx *Fout, int N) {
  kiss_fft_cpx *Fout2;
  int i;
  celt_coef tw;
  tw = QCONST32(0.7071067812f, COEF_SHIFT - 1);
  for (i = 0; i < N; i++) {
    kiss_fft_cpx t;
    Fout2 = Fout + 4;
    t = Fout2[0];
    C_SUB(Fout2[0], Fout[0], t);
    C_ADDTO(Fout[0], t);

    t.r = S_MUL(ADD32_ovflw(Fout2[1].r, Fout2[1].i), tw);
    t.i = S_MUL(SUB32_ovflw(Fout2[1].i, Fout2[1].r), tw);
    C_SUB(Fout2[1], Fout[1], t);
    C_ADDTO(Fout[1], t);

    t.r = Fout2[2].i;
    t.i = NEG32_ovflw(Fout2[2].r);
    C_SUB(Fout2[2], Fout[2], t);
    C_ADDTO(Fout[2], t);

    t.r = S_MUL(SUB32_ovflw(Fout2[3].i, Fout2[3].r), tw);
    t.i = S_MUL(NEG32_ovflw(ADD32_ovflw(Fout2[3].i, Fout2[3].r)), tw);
    C_SUB(Fout2[3], Fout[3], t);
    C_ADDTO(Fout[3], t);
    Fout += 8;
  }
}

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

int main(void) {
  char magic[4];
  uint32_t version;
  uint32_t mode;
  uint32_t count; /* number of groups N */
  uint32_t total; /* number of complex samples = 8*N */
  uint32_t i;
  kiss_fft_cpx *buf;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) return 1;
  if (!read_u32(&version) || version != 1 || !read_u32(&mode) || !read_u32(&count)) return 1;
  if (mode != MODE_KF_BFLY2) return 1;

  total = count * 8u;
  buf = (kiss_fft_cpx *)malloc(sizeof(kiss_fft_cpx) * (total ? total : 1));
  if (!buf) return 1;

  for (i = 0; i < total; i++) {
    uint32_t r, im;
    if (!read_u32(&r) || !read_u32(&im)) { free(buf); return 1; }
    buf[i].r = (kiss_fft_scalar)(int32_t)r;
    buf[i].i = (kiss_fft_scalar)(int32_t)im;
  }

  ref_kf_bfly2(buf, (int)count);

  if (!write_exact(OUTPUT_MAGIC, sizeof(magic)) || !write_u32(1) || !write_u32(total)) { free(buf); return 1; }
  for (i = 0; i < total; i++) {
    if (!write_u32((uint32_t)(int32_t)buf[i].r) || !write_u32((uint32_t)(int32_t)buf[i].i)) { free(buf); return 1; }
  }
  free(buf);
  return 0;
}
