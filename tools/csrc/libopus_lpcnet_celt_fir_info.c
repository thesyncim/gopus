/* Selected float celt_fir call from libopus dnn/lpcnet_enc.c. */
#include <stdint.h>
#include <stdio.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "celt/celt_lpc.h"
#include "celt/cpu_support.h"

#if defined(FIXED_POINT) || !defined(ENABLE_DEEP_PLC)
#error "LPCNet celt_fir oracle requires selected float neural libopus"
#endif

#define INPUT_MAGIC "GLRI"
#define OUTPUT_MAGIC "GLRO"
#define FIR_ORDER 16
#define FIR_LENGTH 160

static int read_exact(void *dst, size_t size) {
  return fread(dst, 1, size, stdin) == size;
}

static int write_exact(const void *src, size_t size) {
  return fwrite(src, 1, size, stdout) == size;
}

static int read_u32(uint32_t *out) {
  unsigned char bytes[4];
  if (!read_exact(bytes, sizeof(bytes))) return 0;
  *out = (uint32_t)bytes[0] | ((uint32_t)bytes[1] << 8) |
      ((uint32_t)bytes[2] << 16) | ((uint32_t)bytes[3] << 24);
  return 1;
}

static int write_u32(uint32_t value) {
  unsigned char bytes[4];
  bytes[0] = (unsigned char)value;
  bytes[1] = (unsigned char)(value >> 8);
  bytes[2] = (unsigned char)(value >> 16);
  bytes[3] = (unsigned char)(value >> 24);
  return write_exact(bytes, sizeof(bytes));
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

int main(void) {
  unsigned char magic[4];
  uint32_t version;
  int i, arch;
  float input[FIR_ORDER + FIR_LENGTH];
  float coeffs[FIR_ORDER];
  float output[FIR_LENGTH];

#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 1;
#endif
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0 ||
      !read_u32(&version) || version != 1) return 1;
  for (i = 0; i < FIR_ORDER + FIR_LENGTH; i++) if (!read_float(&input[i])) return 1;
  for (i = 0; i < FIR_ORDER; i++) if (!read_float(&coeffs[i])) return 1;

  arch = opus_select_arch();
  celt_fir(&input[FIR_ORDER], coeffs, output, FIR_LENGTH, FIR_ORDER, arch);

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32((uint32_t)arch)) return 1;
  for (i = 0; i < FIR_LENGTH; i++) if (!write_float(output[i])) return 1;
  return 0;
}
