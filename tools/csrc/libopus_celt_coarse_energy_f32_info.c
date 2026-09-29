/* Float coarse-energy decoder oracle using the pinned quant_bands.c kernel. */
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

#define GCEI_MAGIC "GCEI"
#define GCEO_MAGIC "GCEO"
enum { MAX_BANDS = 21, MAX_CHANNELS = 2, MAX_CASES = 32, MAX_STORAGE = 512 };

static int read_exact(void *dst, size_t n) {
  return fread(dst, 1, n, stdin) == n;
}
static int write_exact(const void *src, size_t n) {
  return fwrite(src, 1, n, stdout) == n;
}
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
static int read_i32(int32_t *out) {
  uint32_t v;
  if (!read_u32(&v)) return 0;
  *out = (int32_t)v;
  return 1;
}
static int write_i32(int32_t v) { return write_u32((uint32_t)v); }
static int read_float(float *out) {
  union { uint32_t u; float f; } v;
  if (!read_u32(&v.u)) return 0;
  *out = v.f;
  return 1;
}
static int write_float(float v) {
  union { uint32_t u; float f; } bits;
  bits.f = v;
  return write_u32(bits.u);
}
static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

static int run_cases(void) {
  uint32_t case_count;
  uint32_t case_index;
  if (!read_u32(&case_count) || case_count == 0 || case_count > MAX_CASES) return 0;
  if (!write_exact(GCEO_MAGIC, 4) || !write_u32(1) || !write_u32(case_count)) return 0;

  for (case_index = 0; case_index < case_count; case_index++) {
    uint32_t channels, bands, lm, intra, storage;
    CELTMode mode;
    celt_glog old_energies[MAX_CHANNELS * MAX_BANDS];
    int32_t qi[MAX_CHANNELS * MAX_BANDS];
    unsigned char packet[MAX_STORAGE];
    ec_enc enc;
    ec_dec dec;
    uint32_t band, channel, count;

    if (!read_u32(&channels) || !read_u32(&bands) || !read_u32(&lm) ||
        !read_u32(&intra) || !read_u32(&storage)) return 0;
    if (channels == 0 || channels > MAX_CHANNELS || bands == 0 || bands > MAX_BANDS ||
        lm > 3 || intra > 1 || storage < 128 || storage > MAX_STORAGE) return 0;
    count = channels * bands;
    for (channel = 0; channel < count; channel++) {
      if (!read_float(&old_energies[channel])) return 0;
    }
    for (channel = 0; channel < count; channel++) {
      if (!read_i32(&qi[channel]) || qi[channel] < -15 || qi[channel] > 15) return 0;
    }

    memset(&mode, 0, sizeof(mode));
    mode.nbEBands = (int)bands;
    memset(packet, 0, sizeof(packet));
    ec_enc_init(&enc, packet, storage);
    for (band = 0; band < bands; band++) {
      uint32_t prob_index = 2 * (band < 20 ? band : 20);
      for (channel = 0; channel < channels; channel++) {
        uint32_t index = channel * bands + band;
        int value = qi[index];
        ec_laplace_encode(&enc, &value,
            (unsigned)e_prob_model[lm][intra][prob_index] << 7,
            (int)e_prob_model[lm][intra][prob_index + 1] << 6);
        if (value != qi[index]) return 0;
      }
    }
    ec_enc_done(&enc);
    ec_dec_init(&dec, packet, storage);
    unquant_coarse_energy(&mode, 0, (int)bands, old_energies, (int)intra,
        &dec, (int)channels, (int)lm);

    if (!write_u32(channels) || !write_u32(bands) || !write_u32(lm) ||
        !write_u32(intra) || !write_u32(storage) ||
        !write_exact(packet, storage)) return 0;
    for (channel = 0; channel < count; channel++) {
      if (!write_float(old_energies[channel])) return 0;
    }
    if (!write_u32((uint32_t)ec_tell(&dec))) return 0;
  }
  return 1;
}

int main(void) {
  char magic[4];
  uint32_t version;
  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, 4) || memcmp(magic, GCEI_MAGIC, 4) != 0 ||
      !read_u32(&version) || version != 1) return 1;
  if (!run_cases()) return 1;
  fflush(stdout);
  return 0;
}
