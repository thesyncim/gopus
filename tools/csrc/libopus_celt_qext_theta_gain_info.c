/* Calls the pinned QEXT build's celt_cos_norm2 theta gains from mathops.h.
   The main-band stereo path uses these gains even without an extension coder.

   Input:  "GQTG", u32 version(1), u32 count, count * u32 theta_q30.
   Output: "GQTO", u32 version(1), u32 count, count * (f32 mid, f32 side).
   All words are little-endian. */
#include <stdint.h>
#include <stdio.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#ifdef HAVE_CONFIG_H
#include "config.h"
#endif

#ifndef ENABLE_QEXT
#error This oracle requires a QEXT-enabled libopus build.
#endif
#ifdef FIXED_POINT
#error This oracle requires the float libopus build.
#endif

#include "mathops.h"

static int read_exact(void *dst, size_t n) { return fread(dst, 1, n, stdin) == n; }
static int write_exact(const void *src, size_t n) { return fwrite(src, 1, n, stdout) == n; }

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact(b, sizeof b)) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) |
         ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int write_u32(uint32_t v) {
  unsigned char b[4];
  b[0] = (unsigned char)v;
  b[1] = (unsigned char)(v >> 8);
  b[2] = (unsigned char)(v >> 16);
  b[3] = (unsigned char)(v >> 24);
  return write_exact(b, sizeof b);
}

static int write_f32(float v) {
  uint32_t bits;
  memcpy(&bits, &v, sizeof bits);
  return write_u32(bits);
}

int main(void) {
  char magic[4];
  uint32_t version, count, i;
#ifdef _WIN32
  _setmode(_fileno(stdin), _O_BINARY);
  _setmode(_fileno(stdout), _O_BINARY);
#endif
  if (!read_exact(magic, sizeof magic) || memcmp(magic, "GQTG", 4) != 0 ||
      !read_u32(&version) || version != 1 ||
      !read_u32(&count) || count == 0 || count > 64) return 1;
  if (!write_exact("GQTO", 4) || !write_u32(1) || !write_u32(count)) return 1;
  for (i = 0; i < count; i++) {
    uint32_t theta_q30;
    float theta;
    if (!read_u32(&theta_q30) || theta_q30 > (1u << 30)) return 1;
    theta = theta_q30 * (1.f / (1 << 30));
    if (!write_f32(celt_cos_norm2(theta)) ||
        !write_f32(celt_cos_norm2(1.f - theta))) return 1;
  }
  return 0;
}
