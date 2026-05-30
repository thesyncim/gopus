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
#include "kiss_fft.h"

/* Oracle helper that dumps the static 48000/960 custom mode's mode->mdct lookup
 * (the real tables celt_decode_with_ec uses), so the gopus_fixedpoint build can
 * bake and validate the identical trig / FFT twiddle / bitrev / factor tables.
 *
 * Output (after the GCMO header, version 1, count word = 0):
 *   u32 n
 *   u32 maxshift
 *   u32 trig_len; trig_len x i16 trig          (mode->mdct.trig, celt_coef)
 *   For shift in [0, maxshift]:
 *     u32 nfft
 *     i32 scale
 *     i32 scale_shift
 *     i32 shift
 *     16 x i16 factors
 *     u32 bitrev_len; bitrev_len x i16 bitrev
 *     u32 tw_len; tw_len x (i16 r, i16 i) twiddles  (the kfft's own twiddles)
 */

#define INPUT_MAGIC "GTMI"
#define OUTPUT_MAGIC "GTMO"

#if defined(ENABLE_ASSERTIONS) || defined(ENABLE_HARDENING)
#ifdef __GNUC__
__attribute__((noreturn))
#endif
void celt_fatal(const char *str, const char *file, int line) {
  fprintf(stderr, "Fatal (internal) error in %s, line %d: %s\n", file, line, str);
  abort();
}
#endif

static int read_exact(void *dst, size_t n) { return fread(dst, 1, n, stdin) == n; }
static int write_exact(const void *src, size_t n) { return fwrite(src, 1, n, stdout) == n; }

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact(b, 4)) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) | ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
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

static int write_i16(int16_t v) {
  unsigned char b[2];
  b[0] = (unsigned char)((uint16_t)v & 0xFF);
  b[1] = (unsigned char)(((uint16_t)v >> 8) & 0xFF);
  return write_exact(b, 2);
}

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

int main(void) {
  char magic[4];
  uint32_t version, mode_word, count;
  const CELTMode *mode;
  const mdct_lookup *m;
  int err = 0;
  int shift;
  int n2;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, 4) || memcmp(magic, INPUT_MAGIC, 4) != 0) return 1;
  if (!read_u32(&version) || version != 1 || !read_u32(&mode_word) || !read_u32(&count)) return 1;
  (void)mode_word;
  (void)count;

  mode = opus_custom_mode_create(48000, 960, &err);
  if (mode == NULL || err != OPUS_OK) return 1;
  m = &mode->mdct;

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(0)) return 1;
  if (!write_u32((uint32_t)m->n)) return 1;
  if (!write_u32((uint32_t)m->maxshift)) return 1;

  /* trig length: clt_mdct_init allocates n-(n2>>maxshift) entries. */
  n2 = m->n >> 1;
  {
    uint32_t trig_len = (uint32_t)(m->n - (n2 >> m->maxshift));
    uint32_t i;
    if (!write_u32(trig_len)) return 1;
    for (i = 0; i < trig_len; i++) {
      if (!write_i16((int16_t)m->trig[i])) return 1;
    }
  }

  /* mode->window (overlap entries), mode->eBands (nbEBands+1), preemph[0]. */
  {
    uint32_t i;
    if (!write_u32((uint32_t)mode->overlap)) return 1;
    for (i = 0; i < (uint32_t)mode->overlap; i++) {
      if (!write_i16((int16_t)mode->window[i])) return 1;
    }
    if (!write_u32((uint32_t)(mode->nbEBands + 1))) return 1;
    for (i = 0; i < (uint32_t)(mode->nbEBands + 1); i++) {
      if (!write_i16((int16_t)mode->eBands[i])) return 1;
    }
    if (!write_i32((int32_t)mode->preemph[0])) return 1;
  }

  for (shift = 0; shift <= m->maxshift; shift++) {
    const kiss_fft_state *st = m->kfft[shift];
    uint32_t i;
    if (!write_u32((uint32_t)st->nfft)) return 1;
    if (!write_i32((int32_t)st->scale)) return 1;
    if (!write_i32((int32_t)st->scale_shift)) return 1;
    if (!write_i32((int32_t)st->shift)) return 1;
    for (i = 0; i < 16; i++) {
      if (!write_i16((int16_t)st->factors[i])) return 1;
    }
    if (!write_u32((uint32_t)st->nfft)) return 1;
    for (i = 0; i < (uint32_t)st->nfft; i++) {
      if (!write_i16((int16_t)st->bitrev[i])) return 1;
    }
    /* Each kfft references its base's twiddle table; dump nfft<<shift entries
     * (the base length) so the Go side can reconstruct the shared table. The
     * standalone state (shift<=0) owns nfft twiddles. */
    {
      int sh = st->shift > 0 ? st->shift : 0;
      uint32_t tw_len = (uint32_t)(st->nfft << sh);
      if (!write_u32(tw_len)) return 1;
      for (i = 0; i < tw_len; i++) {
        if (!write_i16((int16_t)st->twiddles[i].r)) return 1;
        if (!write_i16((int16_t)st->twiddles[i].i)) return 1;
      }
    }
  }

  return 0;
}
