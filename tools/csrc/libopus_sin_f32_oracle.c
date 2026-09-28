#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <math.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#define INPUT_MAGIC "GSII"
#define OUTPUT_MAGIC "GSIO"

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
  unsigned char b[4] = {
      (unsigned char)value,
      (unsigned char)(value >> 8),
      (unsigned char)(value >> 16),
      (unsigned char)(value >> 24),
  };
  return write_exact(b, sizeof(b));
}

int main(void) {
  unsigned char magic[4];
  uint32_t version, count;

#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 1;
#endif
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0 ||
      !read_u32(&version) || version != 1 || !read_u32(&count) || count > 4096) {
    fprintf(stderr, "invalid float sine probe header\n");
    return 2;
  }
  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(count)) return 3;
  for (uint32_t i = 0; i < count; i++) {
    union {
      uint32_t u;
      float f;
    } input, output;
    volatile double sine;
    if (!read_u32(&input.u)) return 4;
    /* OSCE dnn/osce.c passes a float expression to C sin(double), then stores
       the result in float. Keep the call at runtime so this probes the
       same-platform C libm result, including its invalid-input NaN bits. */
    sine = sin((double)input.f);
    output.f = (float)sine;
    if (!write_u32(output.u)) return 5;
  }
  return 0;
}
