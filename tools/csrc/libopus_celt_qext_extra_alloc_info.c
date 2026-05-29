/* Drives libopus clt_compute_extra_allocation() (the ENABLE_QEXT extension-band
   allocation in celt/rate.c) for the native 96 kHz CELT mode and dumps the
   resulting per-band extra_pulses/extra_quant arrays for both the encode and
   decode side, plus the bytes produced by the encode-side range coder.

   This lets the Go port match the QEXT extension-band allocation
   integer-for-integer (the depth ICDF coding plus the pulse/fine-bit
   derivation) against the C reference. The arrays are integer tables, so any
   mismatch fails on every platform.

   Protocol (little-endian):
     in : "GQAI" magic, u32 version,
          u32 channels (C), u32 LM, u32 start, u32 end, u32 qextEnd,
          i32 totalQ3, f32 toneFreq, f32 toneishness, u32 storageBytes,
          u32 nLogE,    f32 bandLogE[nLogE],      nLogE = C*nbEBands
          u32 nQLogE,   f32 qextBandLogE[nQLogE], nQLogE = C*NB_QEXT_BANDS
     out: "GQAO" magic, u32 version(=1),
          u32 totBands,                            totBands = end + qextEnd
          u32 n, i32 enc_extra_pulses[n],          n = totBands
          u32 n, i32 enc_extra_quant[n],
          u32 nbytes, u8 enc_bytes[nbytes],        nbytes = storageBytes
          u32 enc_tell_frac,
          u32 n, i32 dec_extra_pulses[n],
          u32 n, i32 dec_extra_quant[n]            */
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

#include "celt.h"
#include "entcode.h"
#include "entenc.h"
#include "entdec.h"
#include "mdct.h"
#include "modes.h"
#include "rate.h"

#define GQAI_MAGIC "GQAI"
#define GQAO_MAGIC "GQAO"

#if defined(ENABLE_ASSERTIONS) || defined(ENABLE_HARDENING)
#ifdef __GNUC__
__attribute__((noreturn))
#endif
void celt_fatal(const char *str, const char *file, int line) {
  fprintf(stderr, "Fatal (internal) error in %s, line %d: %s\n", file, line, str);
  abort();
}
#endif

static int read_exact(void *dst, size_t n) {
  return fread(dst, 1, n, stdin) == n;
}

static int write_exact(const void *src, size_t n) {
  return fwrite(src, 1, n, stdout) == n;
}

static uint32_t read_u32(void) {
  unsigned char b[4];
  if (!read_exact(b, 4)) {
    fprintf(stderr, "short read u32\n");
    exit(1);
  }
  return (uint32_t)b[0] | ((uint32_t)b[1] << 8) | ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
}

static int32_t read_i32(void) { return (int32_t)read_u32(); }

static float read_f32(void) {
  uint32_t u = read_u32();
  float f;
  memcpy(&f, &u, 4);
  return f;
}

static int write_u32(uint32_t v) {
  unsigned char b[4];
  b[0] = (unsigned char)(v & 0xFF);
  b[1] = (unsigned char)((v >> 8) & 0xFF);
  b[2] = (unsigned char)((v >> 16) & 0xFF);
  b[3] = (unsigned char)((v >> 24) & 0xFF);
  return write_exact(b, 4);
}

static int write_i32(int32_t v) { return write_u32((uint32_t)v); }

int main(void) {
#ifdef _WIN32
  _setmode(_fileno(stdin), _O_BINARY);
  _setmode(_fileno(stdout), _O_BINARY);
#endif
  char magic[4];
  if (!read_exact(magic, 4) || memcmp(magic, GQAI_MAGIC, 4) != 0) {
    fprintf(stderr, "bad input magic\n");
    return 1;
  }
  (void)read_u32(); /* input version */

  int C = (int)read_u32();
  int LM = (int)read_u32();
  int start = (int)read_u32();
  int end = (int)read_u32();
  int qext_end = (int)read_u32();
  opus_int32 total = (opus_int32)read_i32();
  float tone_freq = read_f32();
  float toneishness = read_f32();
  int storage = (int)read_u32();

  int nLogE = (int)read_u32();
  celt_glog *bandLogE = (celt_glog *)malloc(sizeof(celt_glog) * (size_t)nLogE);
  for (int i = 0; i < nLogE; i++) bandLogE[i] = (celt_glog)read_f32();

  int nQLogE = (int)read_u32();
  celt_glog *qext_bandLogE = (celt_glog *)malloc(sizeof(celt_glog) * (size_t)nQLogE);
  for (int i = 0; i < nQLogE; i++) qext_bandLogE[i] = (celt_glog)read_f32();

  int err = OPUS_OK;
  CELTMode *mode = opus_custom_mode_create(96000, 1920, &err);
  if (mode == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_custom_mode_create(96000,1920) failed err=%d\n", err);
    return 1;
  }

  CELTMode qext;
  compute_qext_mode(&qext, mode);

  int tot_bands = end + qext_end;
  int *enc_pulses = (int *)calloc((size_t)tot_bands, sizeof(int));
  int *enc_quant = (int *)calloc((size_t)tot_bands, sizeof(int));
  int *dec_pulses = (int *)calloc((size_t)tot_bands, sizeof(int));
  int *dec_quant = (int *)calloc((size_t)tot_bands, sizeof(int));

  unsigned char *buf = (unsigned char *)calloc((size_t)storage, 1);

  /* Encode side. */
  ec_enc enc;
  ec_enc_init(&enc, buf, storage);
  clt_compute_extra_allocation(mode, &qext, start, end, qext_end, bandLogE, qext_bandLogE,
                               total, enc_pulses, enc_quant, C, LM, &enc, 1, tone_freq, toneishness);
  opus_uint32 enc_tell = ec_tell_frac(&enc);
  ec_enc_done(&enc);

  /* Decode side, replaying the bytes the encoder just produced. */
  ec_dec dec;
  ec_dec_init(&dec, buf, storage);
  clt_compute_extra_allocation(mode, &qext, start, end, qext_end, NULL, NULL,
                               total, dec_pulses, dec_quant, C, LM, &dec, 0, 0, 0);

  if (!write_exact(GQAO_MAGIC, 4)) return 1;
  write_u32(1u);
  write_u32((uint32_t)tot_bands);

  write_u32((uint32_t)tot_bands);
  for (int i = 0; i < tot_bands; i++) write_i32((int32_t)enc_pulses[i]);
  write_u32((uint32_t)tot_bands);
  for (int i = 0; i < tot_bands; i++) write_i32((int32_t)enc_quant[i]);

  write_u32((uint32_t)storage);
  if (storage > 0 && !write_exact(buf, (size_t)storage)) return 1;
  write_u32((uint32_t)enc_tell);

  write_u32((uint32_t)tot_bands);
  for (int i = 0; i < tot_bands; i++) write_i32((int32_t)dec_pulses[i]);
  write_u32((uint32_t)tot_bands);
  for (int i = 0; i < tot_bands; i++) write_i32((int32_t)dec_quant[i]);

  fflush(stdout);
  return 0;
}
