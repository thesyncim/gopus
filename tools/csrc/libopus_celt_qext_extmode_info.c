/* Dumps the libopus QEXT *extension* mode derived by compute_qext_mode() from
   the native 96 kHz CELT mode, plus the precomputed qext_cache PulseCache.

   This is the >20 kHz extension layer: compute_qext_mode() rewrites eBands,
   logN, nbEBands and effEBands for the qext extension bands, and
   opus_custom_mode_create() precomputes the matching pulse cache into
   mode->qext_cache (via compute_pulse_cache on the derived mode). gopus mirrors
   this in computeQEXTModeConfig() and the qextCache* tables; this oracle lets
   the test match the C result exactly rather than hand-transcribed constants.

   Protocol (little-endian):
     in : "GQEI" magic, u32 version
     out: "GQEO" magic, u32 version(=1), then
          u32 baseFs, u32 baseShortMdctSize,
          -- derived extension mode (compute_qext_mode output) --
          u32 nbEBands, u32 effEBands,
          u32 nbEBands+1, i16 eBands[nbEBands+1],
          u32 nbEBands,   i16 logN[nbEBands],
          -- precomputed qext_cache (PulseCache) --
          u32 cacheSize,
          u32 indexLen,  i16 index[indexLen],   indexLen = nbEBands*(maxLM+2)
          u32 bitsLen,   u8  bits[bitsLen],      bitsLen  = cacheSize
          u32 capsLen,   u8  caps[capsLen]       capsLen  = (maxLM+1)*2*nbEBands

   The derived nbEBands/maxLM are those of the extension mode (NB_QEXT_BANDS,
   inherited maxLM). */
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
#include "mdct.h"
#include "modes.h"
#include "rate.h"

#define GQEI_MAGIC "GQEI"
#define GQEO_MAGIC "GQEO"

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

static int write_u32(uint32_t v) {
  unsigned char b[4];
  b[0] = (unsigned char)(v & 0xFF);
  b[1] = (unsigned char)((v >> 8) & 0xFF);
  b[2] = (unsigned char)((v >> 16) & 0xFF);
  b[3] = (unsigned char)((v >> 24) & 0xFF);
  return write_exact(b, 4);
}

static int write_i16(int16_t v) {
  unsigned char b[2];
  uint16_t u = (uint16_t)v;
  b[0] = (unsigned char)(u & 0xFF);
  b[1] = (unsigned char)((u >> 8) & 0xFF);
  return write_exact(b, 2);
}

static int write_u8(uint8_t v) { return write_exact(&v, 1); }

int main(void) {
#ifdef _WIN32
  _setmode(_fileno(stdin), _O_BINARY);
  _setmode(_fileno(stdout), _O_BINARY);
#endif
  char magic[4];
  if (!read_exact(magic, 4) || memcmp(magic, GQEI_MAGIC, 4) != 0) {
    fprintf(stderr, "bad input magic\n");
    return 1;
  }
  unsigned char ver[4];
  if (!read_exact(ver, 4)) {
    fprintf(stderr, "missing version\n");
    return 1;
  }

  int err = OPUS_OK;
  CELTMode *mode = opus_custom_mode_create(96000, 1920, &err);
  if (mode == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_custom_mode_create(96000,1920) failed err=%d\n", err);
    return 1;
  }

  /* Derive the extension mode exactly as celt_encoder.c/celt_decoder.c do. */
  CELTMode qext;
  compute_qext_mode(&qext, mode);

  if (!write_exact(GQEO_MAGIC, 4)) return 1;
  write_u32(1u); /* output version */
  write_u32((uint32_t)mode->Fs);
  write_u32((uint32_t)mode->shortMdctSize);

  write_u32((uint32_t)qext.nbEBands);
  write_u32((uint32_t)qext.effEBands);

  int i;
  write_u32((uint32_t)(qext.nbEBands + 1));
  for (i = 0; i <= qext.nbEBands; i++) write_i16((int16_t)qext.eBands[i]);

  write_u32((uint32_t)qext.nbEBands);
  for (i = 0; i < qext.nbEBands; i++) write_i16((int16_t)qext.logN[i]);

  /* The precomputed pulse cache for the extension mode. compute_qext_mode()
     copies it from mode->qext_cache, which opus_custom_mode_create() filled by
     running compute_pulse_cache() on the derived mode. */
  const PulseCache *c = &qext.cache;
  int indexLen = qext.nbEBands * (qext.maxLM + 2);
  int capsLen = (qext.maxLM + 1) * 2 * qext.nbEBands;

  write_u32((uint32_t)c->size);

  write_u32((uint32_t)indexLen);
  for (i = 0; i < indexLen; i++) write_i16((int16_t)c->index[i]);

  write_u32((uint32_t)c->size);
  for (i = 0; i < c->size; i++) write_u8((uint8_t)c->bits[i]);

  write_u32((uint32_t)capsLen);
  for (i = 0; i < capsLen; i++) write_u8((uint8_t)c->caps[i]);

  fflush(stdout);
  /* mode is a static const in static_mode_list; nothing to free. */
  return 0;
}
