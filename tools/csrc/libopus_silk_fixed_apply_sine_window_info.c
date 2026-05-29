/* Oracle for the libopus FIXED_POINT silk_apply_sine_window kernel.
 *
 * Must be compiled and linked against a libopus configured with
 * --enable-fixed-point (defines FIXED_POINT). Reads a little-endian payload of
 * cases from stdin and writes the bit-exact windowed output to stdout. */

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"

#ifndef FIXED_POINT
#error "this oracle requires a FIXED_POINT libopus build (--enable-fixed-point)"
#endif

#include "SigProc_FIX.h"

#define INPUT_MAGIC "GASI"
#define OUTPUT_MAGIC "GASO"

#define MAX_LEN 120

void silk_apply_sine_window(opus_int16 px_win[], const opus_int16 px[],
                            const opus_int win_type, const opus_int length);

#if defined(ENABLE_ASSERTIONS) || defined(ENABLE_HARDENING)
void celt_fatal(const char *str, const char *file, int line) {
  (void)str;
  (void)file;
  (void)line;
  abort();
}
#endif

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

static int read_exact(void *dst, size_t size) {
  return fread(dst, 1, size, stdin) == size;
}

static int write_exact(const void *src, size_t size) {
  return fwrite(src, 1, size, stdout) == size;
}

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact(b, sizeof(b))) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) | ((uint32_t)b[2] << 16) |
         ((uint32_t)b[3] << 24);
  return 1;
}

static int read_i32(int32_t *out) {
  uint32_t u;
  if (!read_u32(&u)) return 0;
  *out = (int32_t)u;
  return 1;
}

static int read_i16(int16_t *out) {
  unsigned char b[2];
  if (!read_exact(b, sizeof(b))) return 0;
  *out = (int16_t)((uint16_t)b[0] | ((uint16_t)b[1] << 8));
  return 1;
}

static int write_u32(uint32_t value) {
  unsigned char b[4];
  b[0] = (unsigned char)(value & 0xffu);
  b[1] = (unsigned char)((value >> 8) & 0xffu);
  b[2] = (unsigned char)((value >> 16) & 0xffu);
  b[3] = (unsigned char)((value >> 24) & 0xffu);
  return write_exact(b, sizeof(b));
}

static int write_i16(int16_t value) {
  unsigned char b[2];
  uint16_t u = (uint16_t)value;
  b[0] = (unsigned char)(u & 0xffu);
  b[1] = (unsigned char)((u >> 8) & 0xffu);
  return write_exact(b, sizeof(b));
}

int main(void) {
  if (!set_binary_stdio()) return 1;

  char magic[4];
  if (!read_exact(magic, sizeof(magic)) ||
      memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) {
    return 1;
  }
  uint32_t version;
  if (!read_u32(&version) || version != 1) return 1;
  uint32_t count;
  if (!read_u32(&count)) return 1;

  if (!write_exact(OUTPUT_MAGIC, 4)) return 1;
  if (!write_u32(1)) return 1; /* version */
  if (!write_u32(count)) return 1;

  for (uint32_t c = 0; c < count; c++) {
    int32_t win_type;
    uint32_t len;
    if (!read_i32(&win_type) || !read_u32(&len)) return 1;
    if (len > MAX_LEN || len < 16 || (len & 3) != 0) return 1;
    if (win_type != 1 && win_type != 2) return 1;

    static int16_t in[MAX_LEN];
    static int16_t out[MAX_LEN];

    for (uint32_t i = 0; i < len; i++) {
      if (!read_i16(&in[i])) return 1;
    }

    silk_apply_sine_window(out, in, (opus_int)win_type, (opus_int)len);

    for (uint32_t i = 0; i < len; i++) {
      if (!write_i16(out[i])) return 1;
    }
  }

  return 0;
}
