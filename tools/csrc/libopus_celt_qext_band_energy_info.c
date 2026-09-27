/* Live QEXT-mode band-energy oracle for compute_band_energies + amp2Log2. */

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
#include "celt/bands.h"
#include "celt/modes.h"
#include "celt/pitch.h"
#include "celt/quant_bands.h"

enum {
  DISPATCH_SCALAR = 0,
  DISPATCH_NEON = 1,
  DISPATCH_SSE = 2,
  DISPATCH_UNKNOWN = 255,
  FLAG_MAY_HAVE_NEON = 1u << 0,
  FLAG_PRESUME_NEON = 1u << 1,
  FLAG_HAVE_RTCD = 1u << 2
};

static int read_exact(void *dst, size_t n) { return fread(dst, 1, n, stdin) == n; }
static int write_exact(const void *src, size_t n) { return fwrite(src, 1, n, stdout) == n; }

static uint32_t read_u32(void) {
  unsigned char b[4];
  if (!read_exact(b, sizeof(b))) exit(1);
  return (uint32_t)b[0] | ((uint32_t)b[1] << 8) |
         ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
}

static int write_u32(uint32_t v) {
  unsigned char b[4] = {(unsigned char)v, (unsigned char)(v >> 8),
                        (unsigned char)(v >> 16), (unsigned char)(v >> 24)};
  return write_exact(b, sizeof(b));
}

static int read_float(float *out) {
  uint32_t bits = read_u32();
  memcpy(out, &bits, sizeof(bits));
  return 1;
}

static int write_float(float v) {
  uint32_t bits;
  memcpy(&bits, &v, sizeof(bits));
  return write_u32(bits);
}

static uint32_t dispatch_flags(void) {
  uint32_t flags = 0;
#if defined(OPUS_ARM_MAY_HAVE_NEON_INTR)
  flags |= FLAG_MAY_HAVE_NEON;
#endif
#if defined(OPUS_ARM_PRESUME_NEON_INTR) || defined(OPUS_ARM_PRESUME_NEON)
  flags |= FLAG_PRESUME_NEON;
#endif
#if defined(OPUS_HAVE_RTCD)
  flags |= FLAG_HAVE_RTCD;
#endif
  return flags;
}

static uint32_t inner_product_dispatch(int arch) {
#if defined(OPUS_ARM_PRESUME_NEON_INTR) || defined(OPUS_ARM_PRESUME_NEON)
  (void)arch;
  return DISPATCH_NEON;
#elif defined(OPUS_HAVE_RTCD) && defined(OPUS_ARM_MAY_HAVE_NEON_INTR)
  {
    opus_val32 (*impl)(const opus_val16 *, const opus_val16 *, int) =
        CELT_INNER_PROD_IMPL[arch & OPUS_ARCHMASK];
    if (impl == celt_inner_prod_neon) return DISPATCH_NEON;
    if (impl == celt_inner_prod_c) return DISPATCH_SCALAR;
    return DISPATCH_UNKNOWN;
  }
#elif defined(OPUS_X86_PRESUME_SSE) && !defined(FIXED_POINT)
  (void)arch;
  return DISPATCH_SSE;
#elif defined(OPUS_HAVE_RTCD) && defined(OPUS_X86_MAY_HAVE_SSE) && !defined(FIXED_POINT)
  {
    opus_val32 (*impl)(const opus_val16 *, const opus_val16 *, int) =
        CELT_INNER_PROD_IMPL[arch & OPUS_ARCHMASK];
    if (impl == celt_inner_prod_sse) return DISPATCH_SSE;
    if (impl == celt_inner_prod_c) return DISPATCH_SCALAR;
    return DISPATCH_UNKNOWN;
  }
#else
  (void)arch;
  return DISPATCH_SCALAR;
#endif
}

int main(void) {
  char magic[4];
  uint32_t version, count;
  int arch, error = OPUS_OK;
  OpusCustomMode *base;
  CELTMode qext;

#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 1;
#endif

  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, "GQBE", 4) != 0)
    return 1;
  version = read_u32();
  count = read_u32();
  if (version != 1 || count < 1 || count > 32) return 1;

  base = opus_custom_mode_create(48000, 960, &error);
  if (base == NULL || error != OPUS_OK) return 1;
  compute_qext_mode(&qext, (const CELTMode *)base);
  if (qext.nbEBands != NB_QEXT_BANDS || qext.effEBands != 2) return 1;

  arch = opus_select_arch();
  if (inner_product_dispatch(arch) == DISPATCH_UNKNOWN) return 1;
  if (!write_exact("GQBO", 4) || !write_u32(1) ||
      !write_u32(inner_product_dispatch(arch)) || !write_u32((uint32_t)arch) ||
      !write_u32(dispatch_flags()) || !write_u32(count)) return 1;

  for (uint32_t case_index = 0; case_index < count; case_index++) {
    uint32_t frame_size = read_u32();
    uint32_t channels = read_u32();
    int lm = 0;
    uint32_t n = (uint32_t)qext.shortMdctSize;
    if ((channels != 1 && channels != 2) || frame_size < 120 || frame_size > 960)
      return 1;
    while (n < frame_size && lm < 4) { n <<= 1; lm++; }
    if (n != frame_size) return 1;

    celt_sig *coeffs = (celt_sig *)malloc((size_t)frame_size * channels * sizeof(*coeffs));
    celt_ener *band_e = (celt_ener *)calloc((size_t)channels * NB_QEXT_BANDS, sizeof(*band_e));
    celt_glog *band_log_e = (celt_glog *)calloc((size_t)channels * NB_QEXT_BANDS, sizeof(*band_log_e));
    if (coeffs == NULL || band_e == NULL || band_log_e == NULL) {
      free(coeffs); free(band_e); free(band_log_e);
      return 1;
    }
    for (uint32_t i = 0; i < frame_size * channels; i++) {
      if (!read_float(&coeffs[i])) {
        free(coeffs); free(band_e); free(band_log_e);
        return 1;
      }
    }

    compute_band_energies(&qext, coeffs, band_e, qext.effEBands,
                          (int)channels, lm, arch);
    amp2Log2(&qext, qext.effEBands, qext.effEBands, band_e, band_log_e,
             (int)channels);

    if (!write_u32(frame_size) || !write_u32(channels) ||
        !write_u32((uint32_t)qext.shortMdctSize) || !write_u32((uint32_t)qext.effEBands)) {
      free(coeffs); free(band_e); free(band_log_e);
      return 1;
    }
    for (uint32_t c = 0; c < channels; c++) {
      for (uint32_t band = 0; band < (uint32_t)qext.effEBands; band++) {
        if (!write_float(band_e[c * NB_QEXT_BANDS + band])) {
          free(coeffs); free(band_e); free(band_log_e);
          return 1;
        }
      }
    }
    for (uint32_t c = 0; c < channels; c++) {
      for (uint32_t band = 0; band < (uint32_t)qext.effEBands; band++) {
        if (!write_float(band_log_e[c * NB_QEXT_BANDS + band])) {
          free(coeffs); free(band_e); free(band_log_e);
          return 1;
        }
      }
    }
    free(coeffs); free(band_e); free(band_log_e);
  }
  return fflush(stdout) == 0 ? 0 : 1;
}
