/* Direct selected-libopus quant_energy_finalise oracle for the QEXT branch.
   The caller chooses whether oldBandE is NULL, as it is when extension bytes
   are present. All float state and packet bytes are transported exactly. */
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
#include "celt.h"
#include "entenc.h"
#include "modes.h"
#include "quant_bands.h"

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

static float read_f32(void) {
  uint32_t bits = read_u32();
  float f;
  memcpy(&f, &bits, sizeof(f));
  return f;
}

static int write_f32(float f) {
  uint32_t bits;
  memcpy(&bits, &f, sizeof(bits));
  return write_u32(bits);
}

int main(void) {
#ifdef _WIN32
  _setmode(_fileno(stdin), _O_BINARY);
  _setmode(_fileno(stdout), _O_BINARY);
#endif
  char magic[4];
  if (!read_exact(magic, 4) || memcmp(magic, "GQFI", 4) != 0 || read_u32() != 1) return 1;
  int channels = (int)read_u32();
  int start = (int)read_u32();
  int end = (int)read_u32();
  int bits_left = (int)read_u32();
  int storage = (int)read_u32();
  int with_old = (int)read_u32();
  if (channels < 1 || channels > 2 || start < 0 || end > 21 || start >= end ||
      bits_left < 0 || bits_left > 100 || storage < 1 || storage > 128 ||
      (with_old != 0 && with_old != 1)) return 1;

  int err = OPUS_OK;
  CELTMode *mode = opus_custom_mode_create(48000, 960, &err);
  if (mode == NULL || err != OPUS_OK || mode->nbEBands != 21) return 1;
  int fine_quant[21], fine_priority[21];
  celt_glog oldBandE[42] = {0}, residual[42] = {0};
  for (int i = 0; i < 21; i++) fine_quant[i] = (int)read_u32();
  for (int i = 0; i < 21; i++) fine_priority[i] = (int)read_u32();
  for (int c = 0; c < channels; c++)
    for (int i = 0; i < end; i++) oldBandE[c * 21 + i] = read_f32();
  for (int c = 0; c < channels; c++)
    for (int i = 0; i < end; i++) residual[c * 21 + i] = read_f32();

  unsigned char packet[128] = {0};
  ec_enc enc;
  ec_enc_init(&enc, packet, storage);
  quant_energy_finalise(mode, start, end, with_old ? oldBandE : NULL,
                        residual, fine_quant, fine_priority, bits_left, &enc, channels);
  uint32_t final_range = enc.rng;
  uint32_t tell_frac = ec_tell_frac(&enc);
  ec_enc_done(&enc);

  if (!write_exact("GQFO", 4) || !write_u32(1) || !write_u32((uint32_t)storage) ||
      !write_exact(packet, (size_t)storage) || !write_u32(final_range) ||
      !write_u32(tell_frac)) return 1;
  for (int c = 0; c < channels; c++)
    for (int i = 0; i < end; i++) if (!write_f32(oldBandE[c * 21 + i])) return 1;
  for (int c = 0; c < channels; c++)
    for (int i = 0; i < end; i++) if (!write_f32(residual[c * 21 + i])) return 1;
  return 0;
}
