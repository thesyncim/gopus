/* Decode one cubic leaf with the selected libopus QEXT archive. All payload
   fields and float samples are transported as little-endian 32-bit values. */
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

#include "opus.h"
#include "vq.h"
#include "entdec.h"

static int read_exact(void *dst, size_t n) { return fread(dst, 1, n, stdin) == n; }
static int write_exact(const void *src, size_t n) { return fwrite(src, 1, n, stdout) == n; }

static uint32_t read_u32(void) {
  unsigned char b[4];
  if (!read_exact(b, 4)) exit(1);
  return (uint32_t)b[0] | ((uint32_t)b[1] << 8) | ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
}

static int write_u32(uint32_t v) {
  unsigned char b[4] = {(unsigned char)v, (unsigned char)(v >> 8),
                        (unsigned char)(v >> 16), (unsigned char)(v >> 24)};
  return write_exact(b, 4);
}

int main(void) {
#ifdef _WIN32
  _setmode(_fileno(stdin), _O_BINARY);
  _setmode(_fileno(stdout), _O_BINARY);
#endif
  char magic[4];
  if (!read_exact(magic, 4) || memcmp(magic, "GQDI", 4) != 0 || read_u32() != 1) return 1;
  int n = (int)read_u32();
  int res = (int)read_u32();
  int blocks = (int)read_u32();
  uint32_t gain_bits = read_u32();
  int packet_len = (int)read_u32();
  if (n < 1 || n > 64 || res < 1 || res > 14 || blocks < 1 || blocks > 8 ||
      packet_len < 1 || packet_len > 256) return 1;
  unsigned char packet[256];
  if (!read_exact(packet, (size_t)packet_len)) return 1;
  opus_val32 gain;
  memcpy(&gain, &gain_bits, sizeof(gain));
  celt_norm x[64] = {0};
  ec_dec dec;
  ec_dec_init(&dec, packet, (opus_uint32)packet_len);
  unsigned collapse = cubic_unquant(x, n, res, blocks, &dec, gain);
  if (!write_exact("GQDO", 4) || !write_u32(1) || !write_u32((uint32_t)n) ||
      !write_u32(collapse) || !write_u32(dec.rng) ||
      !write_u32((uint32_t)ec_tell_frac(&dec))) return 1;
  for (int i = 0; i < n; i++) {
    uint32_t bits;
    memcpy(&bits, &x[i], sizeof(bits));
    if (!write_u32(bits)) return 1;
  }
  return 0;
}
