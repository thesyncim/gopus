/* Live float CELT band-energy oracle for compute_band_energies + amp2Log2. */

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "config.h"
#include "opus_custom.h"
#include "celt/bands.h"
#include "celt/modes.h"
#include "celt/pitch.h"
#include "celt/quant_bands.h"

#define INPUT_MAGIC "GBEI"
#define OUTPUT_MAGIC "GBEO"

enum {
  BAND_ENERGY_DISPATCH_SCALAR = 0,
  BAND_ENERGY_DISPATCH_NEON = 1,
  BAND_ENERGY_DISPATCH_SSE = 2,
  BAND_ENERGY_DISPATCH_UNKNOWN = 255,
  BAND_ENERGY_FLAG_MAY_HAVE_NEON = 1u << 0,
  BAND_ENERGY_FLAG_PRESUME_NEON = 1u << 1,
  BAND_ENERGY_FLAG_HAVE_RTCD = 1u << 2
};

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

static int read_float(float *out) {
  uint32_t bits;
  if (!read_u32(&bits)) return 0;
  memcpy(out, &bits, sizeof(*out));
  return 1;
}

static int write_float(float value) {
  uint32_t bits;
  memcpy(&bits, &value, sizeof(bits));
  return write_u32(bits);
}

static uint32_t dispatch_flags(void) {
  uint32_t flags = 0;
#if defined(OPUS_ARM_MAY_HAVE_NEON_INTR)
  flags |= BAND_ENERGY_FLAG_MAY_HAVE_NEON;
#endif
#if defined(OPUS_ARM_PRESUME_NEON_INTR) || defined(OPUS_ARM_PRESUME_NEON)
  flags |= BAND_ENERGY_FLAG_PRESUME_NEON;
#endif
#if defined(OPUS_HAVE_RTCD)
  flags |= BAND_ENERGY_FLAG_HAVE_RTCD;
#endif
  return flags;
}

static uint32_t inner_product_dispatch(int arch) {
#if defined(OPUS_ARM_PRESUME_NEON_INTR) || defined(OPUS_ARM_PRESUME_NEON)
  (void)arch;
  return BAND_ENERGY_DISPATCH_NEON;
#elif defined(OPUS_HAVE_RTCD) && defined(OPUS_ARM_MAY_HAVE_NEON_INTR)
  {
    opus_val32 (*impl)(const opus_val16 *, const opus_val16 *, int) =
        CELT_INNER_PROD_IMPL[arch & OPUS_ARCHMASK];
    if (impl == celt_inner_prod_neon) return BAND_ENERGY_DISPATCH_NEON;
    if (impl == celt_inner_prod_c) return BAND_ENERGY_DISPATCH_SCALAR;
    return BAND_ENERGY_DISPATCH_UNKNOWN;
  }
#elif defined(OPUS_X86_PRESUME_SSE) && !defined(FIXED_POINT)
  (void)arch;
  return BAND_ENERGY_DISPATCH_SSE;
#elif defined(OPUS_HAVE_RTCD) && defined(OPUS_X86_MAY_HAVE_SSE) && !defined(FIXED_POINT)
  {
    opus_val32 (*impl)(const opus_val16 *, const opus_val16 *, int) =
        CELT_INNER_PROD_IMPL[arch & OPUS_ARCHMASK];
    if (impl == celt_inner_prod_sse) return BAND_ENERGY_DISPATCH_SSE;
    if (impl == celt_inner_prod_c) return BAND_ENERGY_DISPATCH_SCALAR;
    return BAND_ENERGY_DISPATCH_UNKNOWN;
  }
#elif defined(OVERRIDE_CELT_INNER_PROD)
  (void)arch;
  return BAND_ENERGY_DISPATCH_UNKNOWN;
#else
  (void)arch;
  return BAND_ENERGY_DISPATCH_SCALAR;
#endif
}

static int run(void) {
  char input_magic[4];
  uint32_t version, count, i;
  int arch = opus_select_arch();

  if (!read_exact(input_magic, sizeof(input_magic)) ||
      memcmp(input_magic, INPUT_MAGIC, sizeof(input_magic)) != 0 ||
      !read_u32(&version) || version != 1 || !read_u32(&count)) {
    return 0;
  }

  if (inner_product_dispatch(arch) == BAND_ENERGY_DISPATCH_UNKNOWN) return 0;

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) ||
      !write_u32(inner_product_dispatch(arch)) || !write_u32((uint32_t)arch) ||
      !write_u32(dispatch_flags()) || !write_u32(count)) {
    return 0;
  }

  for (i = 0; i < count; i++) {
    uint32_t frame_size, channels, n, total, active_bands, bands;
    int error = OPUS_OK;
    int lm = 0;
    OpusCustomMode *public_mode;
    const CELTMode *mode;
    celt_sig *coeffs = NULL;
    celt_ener *band_e = NULL;
    celt_glog *band_log_e = NULL;
    uint32_t j;
    int ok = 0;

    if (!read_u32(&frame_size) || !read_u32(&channels) ||
        (frame_size != 120 && frame_size != 240 && frame_size != 480 && frame_size != 960) ||
        (channels != 1 && channels != 2)) {
      return 0;
    }

    public_mode = opus_custom_mode_create(48000, 960, &error);
    if (public_mode == NULL || error != OPUS_OK) return 0;
    mode = (const CELTMode *)public_mode;
    if (mode->shortMdctSize <= 0 || mode->nbEBands <= 0 || mode->effEBands <= 0) return 0;
    n = (uint32_t)mode->shortMdctSize;
    while ((n << lm) < frame_size && lm < 4) lm++;
    if ((n << lm) != frame_size) return 0;
    total = frame_size * channels;
    active_bands = (uint32_t)mode->effEBands;
    bands = (uint32_t)mode->nbEBands;

    coeffs = (celt_sig *)malloc((size_t)total * sizeof(*coeffs));
    band_e = (celt_ener *)calloc((size_t)channels * bands, sizeof(*band_e));
    band_log_e = (celt_glog *)calloc((size_t)channels * bands, sizeof(*band_log_e));
    if (coeffs == NULL || band_e == NULL || band_log_e == NULL) goto done;

    for (j = 0; j < total; j++) {
      if (!read_float(&coeffs[j])) goto done;
    }

    compute_band_energies(mode, coeffs, band_e, (int)active_bands,
                          (int)channels, lm, arch);
    amp2Log2(mode, (int)active_bands, (int)bands, band_e, band_log_e,
             (int)channels);

    if (!write_u32(frame_size) || !write_u32(channels) ||
        !write_u32(active_bands) || !write_u32(bands)) {
      goto done;
    }
    for (j = 0; j < channels * active_bands; j++) {
      uint32_t channel = j / active_bands;
      uint32_t band = j % active_bands;
      uint32_t start = ((const CELTMode *)mode)->eBands[band] << lm;
      uint32_t width = (((const CELTMode *)mode)->eBands[band + 1] -
                        ((const CELTMode *)mode)->eBands[band]) << lm;
      if (!write_float(celt_inner_prod(&coeffs[channel * frame_size + start],
                                       &coeffs[channel * frame_size + start],
                                       (int)width, arch))) {
        goto done;
      }
    }
    for (j = 0; j < channels * bands; j++) {
      if (!write_float(band_e[j])) goto done;
    }
    for (j = 0; j < channels * bands; j++) {
      if (!write_float(band_log_e[j])) goto done;
    }
    ok = 1;

done:
    free(coeffs);
    free(band_e);
    free(band_log_e);
    if (!ok) return 0;
  }

  return fflush(stdout) == 0;
}

int main(void) {
  return run() ? 0 : 1;
}
