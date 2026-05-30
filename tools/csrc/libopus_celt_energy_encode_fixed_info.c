#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "celt/entenc.h"
#include "celt/entcode.h"
#include "celt/mathops.h"
#include "celt/modes.h"
#include "celt/quant_bands.h"

/* Oracle helper for the libopus FIXED_POINT CELT energy-quantizer encoders.
 * Built against the --enable-fixed-point reference tree so config.h defines
 * FIXED_POINT. It drives the real quant_coarse_energy, quant_fine_energy and
 * quant_energy_finalise through a genuine ec_enc and dumps the coded bytes plus
 * the resulting oldEBands and error arrays. */

#define INPUT_MAGIC "GEEI"
#define OUTPUT_MAGIC "GEEO"

enum { MODE_QUANT_ENERGY = 0 };

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
static int read_u32(uint32_t *out) { return read_exact(out, sizeof(*out)); }
static int write_u32(uint32_t value) { return write_exact(&value, sizeof(value)); }

/* MODE_QUANT_ENERGY wire format (after the GEEI header, version, mode, unused
 * count word):
 *   u32 nbEBands, u32 start, u32 end, u32 effEnd, u32 C, u32 LM
 *   u32 budget        (bits passed to quant_coarse_energy)
 *   u32 nbAvailableBytes
 *   u32 force_intra, u32 two_pass, u32 loss_rate, u32 lfe
 *   i32 delayedIntra  (Q? celt_glog accumulator)
 *   u32 bufSize       (range-coder buffer size in bytes)
 *   u32 finaliseBits  (bits_left passed to quant_energy_finalise)
 *   C*nbEBands x i32 bandLogE   (eBands target, celt_glog Q24)
 *   C*nbEBands x i32 oldEBands  (predictor, celt_glog Q24)
 *   nbEBands x i32 fine_quant
 *   nbEBands x i32 extra_quant  (for quant_fine_energy)
 *   nbEBands x i32 fine_priority
 * Output (after the GEEO header, version 1, count = unused 0):
 *   u32 nbytes ; nbytes bytes of coded range-coder output
 *   u32 total = C*nbEBands ; total x i32 oldEBands ; total x i32 error
 *   i32 delayedIntra (updated) */
static int eval_quant_energy(void) {
  uint32_t nbEBands, start, end, effEnd, C, LM;
  uint32_t budget, nbAvailableBytes;
  uint32_t force_intra, two_pass, loss_rate, lfe;
  uint32_t bufSize, finaliseBits;
  int32_t delayedIntraRaw;
  celt_glog *eBands = NULL, *oldEBands = NULL, *error = NULL;
  int *fine_quant = NULL, *extra_quant = NULL, *fine_priority = NULL;
  unsigned char *buf = NULL;
  CELTMode mode;
  ec_enc enc;
  opus_val32 delayedIntra;
  uint32_t i, total;
  int ok = 0;

  if (!read_u32(&nbEBands) || !read_u32(&start) || !read_u32(&end) ||
      !read_u32(&effEnd) || !read_u32(&C) || !read_u32(&LM))
    return 0;
  if (!read_u32(&budget) || !read_u32(&nbAvailableBytes))
    return 0;
  if (!read_u32(&force_intra) || !read_u32(&two_pass) || !read_u32(&loss_rate) ||
      !read_u32(&lfe))
    return 0;
  if (!read_u32((uint32_t *)&delayedIntraRaw))
    return 0;
  if (!read_u32(&bufSize) || !read_u32(&finaliseBits))
    return 0;

  total = C * nbEBands;
  eBands = (celt_glog *)malloc(total * sizeof(*eBands));
  oldEBands = (celt_glog *)malloc(total * sizeof(*oldEBands));
  error = (celt_glog *)malloc(total * sizeof(*error));
  fine_quant = (int *)malloc(nbEBands * sizeof(*fine_quant));
  extra_quant = (int *)malloc(nbEBands * sizeof(*extra_quant));
  fine_priority = (int *)malloc(nbEBands * sizeof(*fine_priority));
  buf = (unsigned char *)malloc(bufSize ? bufSize : 1);
  if (!eBands || !oldEBands || !error || !fine_quant || !extra_quant ||
      !fine_priority || !buf)
    goto done;

  for (i = 0; i < total; i++) {
    uint32_t v;
    if (!read_u32(&v)) goto done;
    eBands[i] = (celt_glog)(int32_t)v;
  }
  for (i = 0; i < total; i++) {
    uint32_t v;
    if (!read_u32(&v)) goto done;
    oldEBands[i] = (celt_glog)(int32_t)v;
  }
  for (i = 0; i < nbEBands; i++) {
    uint32_t v;
    if (!read_u32(&v)) goto done;
    fine_quant[i] = (int)(int32_t)v;
  }
  for (i = 0; i < nbEBands; i++) {
    uint32_t v;
    if (!read_u32(&v)) goto done;
    extra_quant[i] = (int)(int32_t)v;
  }
  for (i = 0; i < nbEBands; i++) {
    uint32_t v;
    if (!read_u32(&v)) goto done;
    fine_priority[i] = (int)(int32_t)v;
  }
  memset(error, 0, total * sizeof(*error));

  memset(&mode, 0, sizeof(mode));
  mode.nbEBands = (int)nbEBands;

  delayedIntra = (opus_val32)delayedIntraRaw;

  ec_enc_init(&enc, buf, bufSize);

  quant_coarse_energy(&mode, (int)start, (int)end, (int)effEnd, eBands,
                      oldEBands, (opus_uint32)budget, error, &enc, (int)C,
                      (int)LM, (int)nbAvailableBytes, (int)force_intra,
                      &delayedIntra, (int)two_pass, (int)loss_rate, (int)lfe);

  /* prev_quant is NULL in the real CELT encoder (celt_encoder.c); extra_quant
   * carries the per-band fine-bit counts. */
  quant_fine_energy(&mode, (int)start, (int)end, oldEBands, error, NULL,
                    extra_quant, &enc, (int)C);

  quant_energy_finalise(&mode, (int)start, (int)end, oldEBands, error,
                        fine_quant, fine_priority, (int)finaliseBits, &enc,
                        (int)C);

  ec_enc_done(&enc);

  {
    /* Dump the full range-coder buffer: ec_enc_done() has merged the front
     * range bytes and the back raw bits into the single bufSize buffer, so a
     * byte-exact comparison must cover the whole storage. */
    if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(0))
      goto done;
    if (!write_u32(bufSize)) goto done;
    if (!write_exact(buf, bufSize)) goto done;
    if (!write_u32(total)) goto done;
    for (i = 0; i < total; i++)
      if (!write_u32((uint32_t)(int32_t)oldEBands[i])) goto done;
    for (i = 0; i < total; i++)
      if (!write_u32((uint32_t)(int32_t)error[i])) goto done;
    if (!write_u32((uint32_t)(int32_t)delayedIntra)) goto done;
  }
  ok = 1;

done:
  free(eBands);
  free(oldEBands);
  free(error);
  free(fine_quant);
  free(extra_quant);
  free(fine_priority);
  free(buf);
  return ok;
}

int main(void) {
  char magic[4];
  uint32_t version, mode, count;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, sizeof(magic)) ||
      memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0)
    return 1;
  if (!read_u32(&version) || version != 1 || !read_u32(&mode) ||
      !read_u32(&count))
    return 1;
  (void)count;

  switch (mode) {
    case MODE_QUANT_ENERGY:
      return eval_quant_energy() ? 0 : 1;
  }
  return 1;
}
