/* Float encoder coarse-energy oracle using pinned quant_coarse_energy_impl. */
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

#include "quant_bands.c"

#define INPUT_MAGIC "GCQI"
#define OUTPUT_MAGIC "GCQO"
enum { MAX_BANDS = 21, MAX_CHANNELS = 2, MAX_CASES = 32, MAX_STORAGE = 512 };

static int read_exact(void *dst, size_t n) {
  return fread(dst, 1, n, stdin) == n;
}

static int write_exact(const void *src, size_t n) {
  return fwrite(src, 1, n, stdout) == n;
}

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact(b, sizeof(b))) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) |
      ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int write_u32(uint32_t value) {
  unsigned char b[4];
  b[0] = (unsigned char)(value & 0xff);
  b[1] = (unsigned char)((value >> 8) & 0xff);
  b[2] = (unsigned char)((value >> 16) & 0xff);
  b[3] = (unsigned char)((value >> 24) & 0xff);
  return write_exact(b, sizeof(b));
}

static int read_i32(int32_t *out) {
  uint32_t value;
  if (!read_u32(&value)) return 0;
  *out = (int32_t)value;
  return 1;
}

static int write_i32(int32_t value) {
  return write_u32((uint32_t)value);
}

static int read_float(float *out) {
  uint32_t bits;
  if (!read_u32(&bits)) return 0;
  memcpy(out, &bits, sizeof(bits));
  return 1;
}

static int write_float(float value) {
  uint32_t bits;
  memcpy(&bits, &value, sizeof(bits));
  return write_u32(bits);
}

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

static uint32_t compact_packet(const ec_enc *enc, unsigned char *dst) {
  uint32_t partial;
  uint32_t len;
  if (enc->error) {
    memcpy(dst, enc->buf, enc->storage);
    return enc->storage;
  }
  partial = (enc->nend_bits & 7) != 0 && enc->end_offs < enc->storage ? 1U : 0U;
  len = enc->offs + partial + enc->end_offs;
  if (enc->offs > 0) memcpy(dst, enc->buf, enc->offs);
  if (partial) dst[enc->offs] = enc->buf[enc->storage - enc->end_offs - 1];
  if (enc->end_offs > 0) {
    memcpy(dst + enc->offs + partial, enc->buf + enc->storage - enc->end_offs,
        enc->end_offs);
  }
  return len;
}

static int run_case(void) {
  uint32_t channels, bands, start, end, lm, intra, storage;
  float max_decay;
  uint32_t i, total, packet_len;
  CELTMode mode;
  celt_glog energies[MAX_CHANNELS * MAX_BANDS];
  celt_glog old_energies[MAX_CHANNELS * MAX_BANDS];
  celt_glog error[MAX_CHANNELS * MAX_BANDS];
  unsigned char packet[MAX_STORAGE];
  unsigned char compact[MAX_STORAGE];
  ec_enc enc;
  int badness;
  uint32_t tell;
  int enc_error;

  if (!read_u32(&channels) || !read_u32(&bands) || !read_u32(&start) ||
      !read_u32(&end) || !read_u32(&lm) || !read_u32(&intra) ||
      !read_u32(&storage) || !read_float(&max_decay)) return 0;
  if (channels == 0 || channels > MAX_CHANNELS || bands == 0 || bands > MAX_BANDS ||
      start >= end || end > bands || lm > 3 || intra > 1 ||
      storage == 0 || storage > MAX_STORAGE || max_decay < 0) return 0;

  total = channels * bands;
  for (i = 0; i < total; i++) {
    if (!read_float(&energies[i])) return 0;
  }
  for (i = 0; i < total; i++) {
    if (!read_float(&old_energies[i])) return 0;
    error[i] = 0;
  }

  memset(&mode, 0, sizeof(mode));
  mode.nbEBands = (int)bands;
  memset(packet, 0, sizeof(packet));
  memset(compact, 0, sizeof(compact));
  ec_enc_init(&enc, packet, storage);
  badness = quant_coarse_energy_impl(&mode, (int)start, (int)end,
      energies, old_energies, (opus_int32)(storage * 8), ec_tell(&enc),
      e_prob_model[lm][intra], error, &enc, (int)channels, (int)lm,
      (int)intra, max_decay, 0);
  tell = (uint32_t)ec_tell(&enc);
  ec_enc_done(&enc);
  enc_error = enc.error;
  packet_len = compact_packet(&enc, compact);

  if (!write_u32(channels) || !write_u32(bands) || !write_u32(start) ||
      !write_u32(end) || !write_u32(lm) || !write_u32(intra) ||
      !write_u32(storage) || !write_u32(packet_len) ||
      !write_i32((int32_t)badness) || !write_u32(tell) ||
      !write_i32((int32_t)enc_error) || !write_exact(compact, packet_len)) return 0;
  for (i = 0; i < total; i++) {
    if (!write_float(old_energies[i])) return 0;
  }
  for (i = 0; i < total; i++) {
    if (!write_float(error[i])) return 0;
  }
  return 1;
}

int main(void) {
  char magic[4];
  uint32_t version, count, i;
  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0 ||
      !read_u32(&version) || version != 1 || !read_u32(&count) ||
      count == 0 || count > MAX_CASES) return 1;
  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(count)) return 1;
  for (i = 0; i < count; i++) {
    if (!run_case()) return 1;
  }
  return 0;
}
