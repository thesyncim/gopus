/* Oracle for the libopus silk_process_NLSFs kernel (silk/process_NLSFs.c).
 *
 * silk_process_NLSFs is shared float/fixed C; this oracle is compiled and
 * linked against a libopus configured with --enable-fixed-point (defines
 * FIXED_POINT) so the integer helpers it composes (Laroia weights, NLSF MSVQ
 * encode, NLSF2A) match the gopus_fixedpoint kernels. Reads a little-endian
 * payload of cases from stdin, populates the silk_encoder_state fields read by
 * silk_process_NLSFs, calls it, and writes the bit-exact outputs (the two
 * halves of PredCoef_Q12, the chosen NLSFIndices, and the quantized pNLSF_Q15)
 * to stdout. */

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"

#ifndef FIXED_POINT
#error "this oracle requires a FIXED_POINT libopus build (--enable-fixed-point)"
#endif

#include "main.h"

#define INPUT_MAGIC "PNLI"
#define OUTPUT_MAGIC "PNLO"

#if defined(ENABLE_ASSERTIONS) || defined(ENABLE_HARDENING)
void celt_fatal(const char *str, const char *file, int line) {
  (void)str;
  (void)file;
  (void)line;
  abort();
}
#endif

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
  unsigned char b[4];
  if (!read_exact(b, sizeof(b))) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) | ((uint32_t)b[2] << 16) |
         ((uint32_t)b[3] << 24);
  return 1;
}

static int read_i32(int32_t *out) {
  uint32_t v;
  if (!read_u32(&v)) return 0;
  *out = (int32_t)v;
  return 1;
}

static int write_u32(uint32_t value) {
  unsigned char b[4];
  b[0] = (unsigned char)(value & 0xffu);
  b[1] = (unsigned char)((value >> 8) & 0xffu);
  b[2] = (unsigned char)((value >> 16) & 0xffu);
  b[3] = (unsigned char)((value >> 24) & 0xffu);
  return write_exact(b, sizeof(b));
}

static int write_i32(int32_t value) { return write_u32((uint32_t)value); }

int main(void) {
  if (!set_binary_stdio()) return 1;

  char magic[4];
  if (!read_exact(magic, sizeof(magic)) ||
      memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) {
    return 1;
  }
  uint32_t version;
  if (!read_u32(&version) || version != 1) return 1;
  uint32_t count;
  if (!read_u32(&count)) return 1;

  if (!write_exact(OUTPUT_MAGIC, 4)) return 1;
  if (!write_u32(1)) return 1; /* version */
  if (!write_u32(count)) return 1;

  for (uint32_t c = 0; c < count; c++) {
    uint32_t order, nbSubfr, signalType, useInterp, nlsfMSVQSurvivors;
    int32_t speechActivityQ8, nlsfInterpCoefQ2;

    if (!read_u32(&order) || !read_u32(&nbSubfr) || !read_u32(&signalType) ||
        !read_u32(&useInterp) || !read_u32(&nlsfMSVQSurvivors)) {
      return 1;
    }
    if (!read_i32(&speechActivityQ8) || !read_i32(&nlsfInterpCoefQ2)) {
      return 1;
    }
    if ((order != 10 && order != 16) || nbSubfr < 1 || nbSubfr > MAX_NB_SUBFR) {
      return 1;
    }

    /* calloc to zero all fields process_NLSFs does not touch. */
    silk_encoder_state *psEncC =
        (silk_encoder_state *)calloc(1, sizeof(silk_encoder_state));
    if (!psEncC) return 1;

    psEncC->predictLPCOrder = (opus_int)order;
    psEncC->nb_subfr = (opus_int)nbSubfr;
    psEncC->speech_activity_Q8 = (opus_int)speechActivityQ8;
    psEncC->useInterpolatedNLSFs = (opus_int)useInterp;
    psEncC->NLSF_MSVQ_Survivors = (opus_int)nlsfMSVQSurvivors;
    psEncC->indices.signalType = (opus_int8)signalType;
    psEncC->indices.NLSFInterpCoef_Q2 = (opus_int8)nlsfInterpCoefQ2;
    psEncC->arch = 0;

    if (order == 16) {
      psEncC->psNLSF_CB = &silk_NLSF_CB_WB;
    } else {
      psEncC->psNLSF_CB = &silk_NLSF_CB_NB_MB;
    }

    opus_int16 pNLSF_Q15[MAX_LPC_ORDER];
    opus_int16 prev_NLSF_Q15[MAX_LPC_ORDER];
    memset(pNLSF_Q15, 0, sizeof(pNLSF_Q15));
    memset(prev_NLSF_Q15, 0, sizeof(prev_NLSF_Q15));
    for (uint32_t i = 0; i < order; i++) {
      int32_t v;
      if (!read_i32(&v)) return 1;
      pNLSF_Q15[i] = (opus_int16)v;
    }
    for (uint32_t i = 0; i < order; i++) {
      int32_t v;
      if (!read_i32(&v)) return 1;
      prev_NLSF_Q15[i] = (opus_int16)v;
    }

    opus_int16 PredCoef_Q12[2][MAX_LPC_ORDER];
    memset(PredCoef_Q12, 0, sizeof(PredCoef_Q12));

    silk_process_NLSFs(psEncC, PredCoef_Q12, pNLSF_Q15, prev_NLSF_Q15);

    for (uint32_t i = 0; i < order; i++) {
      if (!write_i32((int32_t)PredCoef_Q12[0][i])) return 1;
    }
    for (uint32_t i = 0; i < order; i++) {
      if (!write_i32((int32_t)PredCoef_Q12[1][i])) return 1;
    }
    for (uint32_t i = 0; i < order + 1; i++) {
      if (!write_i32((int32_t)psEncC->indices.NLSFIndices[i])) return 1;
    }
    for (uint32_t i = 0; i < order; i++) {
      if (!write_i32((int32_t)pNLSF_Q15[i])) return 1;
    }

    free(psEncC);
  }

  return 0;
}
