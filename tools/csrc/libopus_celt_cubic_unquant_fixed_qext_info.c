#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "celt/arch.h"
#include "celt/entdec.h"
#include "celt/vq.h"

#define INPUT_MAGIC "GQCI"
#define OUTPUT_MAGIC "GQCO"

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

static int read_exact(void *dst, size_t size) { return fread(dst, 1, size, stdin) == size; }
static int write_exact(const void *src, size_t size) { return fwrite(src, 1, size, stdout) == size; }
static int read_u32(uint32_t *out) { return read_exact(out, sizeof(*out)); }
static int write_u32(uint32_t v) { return write_exact(&v, sizeof(v)); }

/* Independent fixed+QEXT cubic_unquant oracle. */
int main(void) {
  char magic[4];
  uint32_t version, n, resolution, blocks, gain, nbytes, padded, i;
  unsigned char *coded = NULL;
  celt_norm *x = NULL;
  ec_dec dec;
  unsigned collapse;
  int ok = 0;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) return 1;
  if (!read_u32(&version) || version != 1 || !read_u32(&n) || n == 0 || n > 512 ||
      !read_u32(&resolution) || resolution > 14 || !read_u32(&blocks) || blocks == 0 || blocks > 16 ||
      !read_u32(&gain) || !read_u32(&nbytes) || nbytes > 4096) return 1;

  coded = (unsigned char *)malloc(nbytes ? nbytes : 1);
  x = (celt_norm *)calloc(n, sizeof(celt_norm));
  if (!coded || !x) goto done;
  if (nbytes && !read_exact(coded, nbytes)) goto done;
  padded = (nbytes + 3u) & ~3u;
  for (i = nbytes; i < padded; i++) {
    unsigned char pad;
    if (!read_exact(&pad, 1)) goto done;
  }

  ec_dec_init(&dec, nbytes ? coded : NULL, nbytes);
  collapse = cubic_unquant(x, (int)n, (int)resolution, (int)blocks, &dec, (opus_val32)(int32_t)gain);
  if (!write_exact(OUTPUT_MAGIC, sizeof(OUTPUT_MAGIC) - 1) || !write_u32(1) || !write_u32(collapse) ||
      !write_u32(dec.rng) || !write_u32(dec.val) || !write_u32((uint32_t)ec_tell(&dec)) ||
      !write_u32((uint32_t)ec_tell_frac(&dec)) || !write_u32((uint32_t)dec.error) ||
      !write_u32(n)) goto done;
  for (i = 0; i < n; i++) {
    if (!write_u32((uint32_t)(int32_t)x[i])) goto done;
  }
  ok = 1;

done:
  free(coded);
  free(x);
  return ok ? 0 : 1;
}
