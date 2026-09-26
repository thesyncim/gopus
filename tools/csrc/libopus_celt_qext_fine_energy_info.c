/* QEXT-mode quant_fine_energy() oracle linked against the selected libopus
   archive. The float build uses 14-band old-energy and error strides. */
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
#include "entenc.h"
#include "modes.h"
#include "quant_bands.h"

#define BANDS 14
#define MAX_CHANNELS 2

#if defined(ENABLE_ASSERTIONS) || defined(ENABLE_HARDENING)
void celt_fatal(const char *str, const char *file, int line) {
  (void)str; (void)file; (void)line;
  abort();
}
#endif

static int read_exact(void *p, size_t n) { return fread(p, 1, n, stdin) == n; }
static int write_exact(const void *p, size_t n) { return fwrite(p, 1, n, stdout) == n; }
static int read_u32(uint32_t *v) {
  unsigned char b[4];
  if (!read_exact(b, sizeof(b))) return 0;
  *v = (uint32_t)b[0] | ((uint32_t)b[1] << 8) |
       ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}
static int write_u32(uint32_t v) {
  unsigned char b[4];
  b[0] = (unsigned char)v;
  b[1] = (unsigned char)(v >> 8);
  b[2] = (unsigned char)(v >> 16);
  b[3] = (unsigned char)(v >> 24);
  return write_exact(b, sizeof(b));
}
static int read_float(celt_glog *v) {
  uint32_t bits;
  if (!read_u32(&bits)) return 0;
  memcpy(v, &bits, sizeof(bits));
  return 1;
}
static int write_float(celt_glog v) {
  uint32_t bits;
  memcpy(&bits, &v, sizeof(bits));
  return write_u32(bits);
}

static uint32_t compact_packet(const ec_enc *enc, unsigned char *dst) {
  uint32_t partial;
  uint32_t n;
  if (enc->error) {
    memcpy(dst, enc->buf, enc->storage);
    return enc->storage;
  }
  partial = (enc->nend_bits & 7) != 0 && enc->end_offs < enc->storage ? 1U : 0U;
  n = enc->offs + partial + enc->end_offs;
  if (enc->offs > 0) memcpy(dst, enc->buf, enc->offs);
  if (partial) dst[enc->offs] = enc->buf[enc->storage - enc->end_offs - 1];
  if (enc->end_offs > 0)
    memcpy(dst + enc->offs + partial, enc->buf + enc->storage - enc->end_offs, enc->end_offs);
  return n;
}

int main(void) {
  uint32_t version, channels, fs, end, storage, raw;
  const CELTMode *base;
  CELTMode qext;
  celt_glog old_band_e[MAX_CHANNELS * BANDS];
  celt_glog error[MAX_CHANNELS * BANDS];
  int fine_bits[BANDS];
  unsigned char buf[128] = {0};
  unsigned char packet[128] = {0};
  char magic[4];
  uint32_t packet_len, range;
  ec_enc enc;
  int mode_error = 0;
  int i;

#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 1;
#endif

  if (!read_exact(magic, 4) || memcmp(magic, "GQFI", 4) != 0) return 1;
  if (!read_u32(&version) || version != 1 ||
      !read_u32(&channels) || !read_u32(&fs) ||
      !read_u32(&end) || !read_u32(&storage)) return 1;
  if ((channels != 1 && channels != 2) || (fs != 48000 && fs != 96000) ||
      end < 1 || end > BANDS || storage < 1 || storage > sizeof(buf)) return 1;

  base = opus_custom_mode_create((opus_int32)fs, (int)fs/50, &mode_error);
  if (base == NULL || mode_error != 0) return 1;
  compute_qext_mode(&qext, base);
  if (qext.nbEBands != BANDS) return 1;

  for (i = 0; i < (int)(channels * BANDS); i++)
    if (!read_float(&old_band_e[i])) return 1;
  for (i = 0; i < (int)(channels * BANDS); i++)
    if (!read_float(&error[i])) return 1;
  for (i = 0; i < BANDS; i++) {
    if (!read_u32(&raw) || raw > 8) return 1;
    fine_bits[i] = (int)raw;
  }

  ec_enc_init(&enc, buf, storage);
  quant_fine_energy(&qext, 0, (int)end, old_band_e, error, NULL, fine_bits, &enc, (int)channels);
  range = enc.rng;
  ec_enc_done(&enc);
  packet_len = compact_packet(&enc, packet);

  if (!write_exact("GQFO", 4) || !write_u32(1) ||
      !write_u32((uint32_t)enc.error) || !write_u32(range) ||
      !write_u32(packet_len)) return 1;
  for (i = 0; i < (int)(channels * BANDS); i++)
    if (!write_float(old_band_e[i])) return 1;
  for (i = 0; i < (int)(channels * BANDS); i++)
    if (!write_float(error[i])) return 1;
  return write_exact(packet, packet_len) ? 0 : 1;
}
