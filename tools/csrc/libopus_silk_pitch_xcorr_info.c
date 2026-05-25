#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "celt/pitch.h"

#define INPUT_MAGIC "GSPX"
#define OUTPUT_MAGIC "GSPY"

void celt_pitch_xcorr_c(const opus_val16 *_x, const opus_val16 *_y, opus_val32 *xcorr,
    int len, int max_pitch, int arch);

void celt_fatal(const char *str, const char *file, int line) {
  (void)str;
  (void)file;
  (void)line;
  abort();
}

#if defined(OPUS_ARM_MAY_HAVE_NEON_INTR) && !defined(GOPUS_LINK_OPUS_ARM_NEON)
opus_val32 celt_inner_prod_neon(const opus_val16 *x, const opus_val16 *y, int N) {
  return celt_inner_prod_c(x, y, N);
}

void dual_inner_prod_neon(const opus_val16 *x, const opus_val16 *y01,
    const opus_val16 *y02, int N, opus_val32 *xy1, opus_val32 *xy2) {
  dual_inner_prod_c(x, y01, y02, N, xy1, xy2);
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
  return read_exact(out, sizeof(*out));
}

static int write_u32(uint32_t value) {
  return write_exact(&value, sizeof(value));
}

static int eval_record(void) {
  uint32_t raw;
  uint32_t length;
  uint32_t max_pitch;
  uint32_t i;
  opus_val16 x[256];
  opus_val16 y[384];
  opus_val32 out[128];
  if (!read_u32(&length) || !read_u32(&max_pitch)) return 0;
  if (length == 0 || length > 256 || max_pitch == 0 || max_pitch > 128) return 0;
  for (i = 0; i < length; i++) {
    if (!read_u32(&raw)) return 0;
    memcpy(&x[i], &raw, sizeof(x[i]));
  }
  for (i = 0; i < length + max_pitch; i++) {
    if (!read_u32(&raw)) return 0;
    memcpy(&y[i], &raw, sizeof(y[i]));
  }
  celt_pitch_xcorr_c(x, y, out, (int)length, (int)max_pitch, 0);
  if (!write_u32(max_pitch)) return 0;
  for (i = 0; i < max_pitch; i++) {
    memcpy(&raw, &out[i], sizeof(raw));
    if (!write_u32(raw)) return 0;
  }
  return 1;
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
  if (mode != 0) return 1;

  if (!write_exact(OUTPUT_MAGIC, sizeof(magic)) || !write_u32(1) || !write_u32(count)) return 1;
  for (i = 0; i < count; i++) {
    if (!eval_record()) return 1;
  }
  return 0;
}
