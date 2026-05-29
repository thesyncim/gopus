#include <stdint.h>
#include <stdio.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "celt/entcode.h"
#include "celt/mathops.h"

/* Oracle helper for the libopus FIXED_POINT integer CELT math kernels.
 * Built against the --enable-fixed-point reference tree so config.h defines
 * FIXED_POINT and the celt_* functions resolve to their integer paths. */

#define INPUT_MAGIC "GFMI"
#define OUTPUT_MAGIC "GFMO"

enum {
  MODE_CELT_SQRT = 0
};

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
  return read_exact(out, sizeof(*out));
}

static int write_u32(uint32_t value) {
  return write_exact(&value, sizeof(value));
}

static int eval_record(uint32_t mode) {
  uint32_t a;

  switch (mode) {
    case MODE_CELT_SQRT:
      if (!read_u32(&a)) return 0;
      return write_u32((uint32_t)(int32_t)celt_sqrt((opus_val32)(int32_t)a));
  }
  return 0;
}

int main(void) {
  char magic[4];
  uint32_t version;
  uint32_t mode;
  uint32_t count;
  uint32_t i;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) return 1;
  if (!read_u32(&version) || version != 1 || !read_u32(&mode) || !read_u32(&count)) return 1;
  if (mode > MODE_CELT_SQRT) return 1;

  if (!write_exact(OUTPUT_MAGIC, sizeof(magic)) || !write_u32(1) || !write_u32(count)) return 1;
  for (i = 0; i < count; i++) {
    if (!eval_record(mode)) return 1;
  }
  return 0;
}
