/* Dumps the native 96 kHz QEXT CELT mode tables and scalar fields from
   libopus's opus_custom_mode_create(96000, 1920). Used to byte/numeric-verify
   gopus's native 96 kHz mode definition under the gopus_qext build tag.

   Protocol (little-endian):
     in : "GQMI" magic, u32 version
     out: "GQMO" magic, u32 version(=1), then
          u32 Fs, u32 overlap, u32 nbEBands, u32 effEBands,
          u32 maxLM, u32 nbShortMdcts, u32 shortMdctSize,
          f32 preemph[4],
          u32 nbEBands+1, i16 eBands[nbEBands+1],
          u32 nbEBands,   i16 logN[nbEBands],
          u32 overlap,    f32 window[overlap],
          u32 mdct.n, u32 mdct.maxshift,
          u32 trigLen,    f32 trig[trigLen]
   trigLen = N - (N2>>maxshift), the exact allocation in clt_mdct_init,
   with N=mdct.n and N2=mdct.n/2 (per-shift trig segments concatenated). */
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

#define GQMI_MAGIC "GQMI"
#define GQMO_MAGIC "GQMO"

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

static int write_f32(float v) {
  uint32_t u;
  memcpy(&u, &v, 4);
  return write_u32(u);
}

int main(void) {
#ifdef _WIN32
  _setmode(_fileno(stdin), _O_BINARY);
  _setmode(_fileno(stdout), _O_BINARY);
#endif
  char magic[4];
  if (!read_exact(magic, 4) || memcmp(magic, GQMI_MAGIC, 4) != 0) {
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

  if (!write_exact(GQMO_MAGIC, 4)) return 1;
  write_u32(1u); /* output version */
  write_u32((uint32_t)mode->Fs);
  write_u32((uint32_t)mode->overlap);
  write_u32((uint32_t)mode->nbEBands);
  write_u32((uint32_t)mode->effEBands);
  write_u32((uint32_t)mode->maxLM);
  write_u32((uint32_t)mode->nbShortMdcts);
  write_u32((uint32_t)mode->shortMdctSize);

  int i;
  for (i = 0; i < 4; i++) write_f32((float)mode->preemph[i]);

  write_u32((uint32_t)(mode->nbEBands + 1));
  for (i = 0; i <= mode->nbEBands; i++) write_i16((int16_t)mode->eBands[i]);

  write_u32((uint32_t)mode->nbEBands);
  for (i = 0; i < mode->nbEBands; i++) write_i16((int16_t)mode->logN[i]);

  write_u32((uint32_t)mode->overlap);
  for (i = 0; i < mode->overlap; i++) write_f32((float)mode->window[i]);

  write_u32((uint32_t)mode->mdct.n);
  write_u32((uint32_t)mode->mdct.maxshift);

  uint32_t trigLen = (uint32_t)(mode->mdct.n - ((mode->mdct.n / 2) >> mode->mdct.maxshift));
  write_u32(trigLen);
  for (i = 0; i < (int)trigLen; i++) write_f32((float)mode->mdct.trig[i]);

  fflush(stdout);
  /* mode is a static const in static_mode_list; nothing to free. */
  return 0;
}
