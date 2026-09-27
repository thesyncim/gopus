/* FIXED_POINT + ENABLE_QEXT alg_quant oracle for exact integer PVQ refinement. */

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
#include "celt/cpu_support.h"
#include "celt/cwrs.h"
#include "celt/entenc.h"
#include "celt/vq.h"

#if !defined(FIXED_POINT) || !defined(ENABLE_QEXT)
#error "fixed QEXT alg_quant oracle requires FIXED_POINT and ENABLE_QEXT"
#endif

void celt_fatal(const char *str, const char *file, int line) {
  (void)str;
  (void)file;
  (void)line;
  abort();
}

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (fread(b, 1, 4, stdin) != 4) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) |
      ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int write_u32(uint32_t value) {
  unsigned char b[4];
  b[0] = (unsigned char)value;
  b[1] = (unsigned char)(value >> 8);
  b[2] = (unsigned char)(value >> 16);
  b[3] = (unsigned char)(value >> 24);
  return fwrite(b, 1, 4, stdout) == 4;
}

static uint32_t compact_packet(const ec_enc *enc, unsigned char *dst) {
  uint32_t partial, len;
  if (enc->error) {
    memcpy(dst, enc->buf, enc->storage);
    return enc->storage;
  }
  partial = (enc->nend_bits & 7) != 0 && enc->end_offs < enc->storage ? 1U : 0U;
  len = enc->offs + partial + enc->end_offs;
  if (enc->offs > 0) memcpy(dst, enc->buf, enc->offs);
  if (partial) dst[enc->offs] = enc->buf[enc->storage - enc->end_offs - 1];
  if (enc->end_offs > 0) {
    memcpy(dst + enc->offs + partial, enc->buf + enc->storage - enc->end_offs, enc->end_offs);
  }
  return len;
}

int main(void) {
  unsigned char magic[4];
  uint32_t version, count, n, k, spread, blocks, extra_bits, resynth;
  uint32_t storage, ext_storage, raw, i, collapse, main_len, ext_len;
  int32_t gain;
  celt_norm *x = NULL;
  unsigned char *main_buf = NULL, *ext_buf = NULL;
  unsigned char *main_packet = NULL, *ext_packet = NULL;
  ec_enc enc, ext_enc;
  uint32_t main_range, ext_range;
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 1;
#endif
  if (fread(magic, 1, 4, stdin) != 4 || memcmp(magic, "GQVP", 4) ||
      !read_u32(&version) || version != 1 || !read_u32(&count) || count != 1 ||
      !read_u32(&n) || !read_u32(&k) || !read_u32(&spread) || !read_u32(&blocks) ||
      !read_u32(&extra_bits) || !read_u32(&resynth) || !read_u32(&raw) ||
      !read_u32(&storage) || !read_u32(&ext_storage)) return 1;
  gain = (int32_t)raw;
  if (n < 2 || n > 512 || k == 0 || k > 512 || spread > 3 ||
      blocks == 0 || blocks > n || extra_bits < 2 || extra_bits > 12 ||
      resynth > 1 || storage == 0 || storage > 4096 ||
      ext_storage == 0 || ext_storage > 4096) return 1;
  x = (celt_norm *)malloc((size_t)n * sizeof(*x));
  main_buf = (unsigned char *)calloc(storage, 1);
  ext_buf = (unsigned char *)calloc(ext_storage, 1);
  main_packet = (unsigned char *)calloc(storage, 1);
  ext_packet = (unsigned char *)calloc(ext_storage, 1);
  if (x == NULL || main_buf == NULL || ext_buf == NULL ||
      main_packet == NULL || ext_packet == NULL) return 1;
  for (i = 0; i < n; i++) {
    if (!read_u32(&raw)) return 1;
    x[i] = (celt_norm)(int32_t)raw;
  }
  ec_enc_init(&enc, main_buf, storage);
  ec_enc_init(&ext_enc, ext_buf, ext_storage);
  collapse = alg_quant(x, (int)n, (int)k, (int)spread, (int)blocks, &enc,
      (opus_val32)gain, (int)resynth, &ext_enc, (int)extra_bits, opus_select_arch());
  main_range = enc.rng;
  ext_range = ext_enc.rng;
  ec_enc_done(&enc);
  ec_enc_done(&ext_enc);
  main_len = compact_packet(&enc, main_packet);
  ext_len = compact_packet(&ext_enc, ext_packet);
  if (fwrite("GQVO", 1, 4, stdout) != 4 || !write_u32(1) || !write_u32(1) ||
      !write_u32(collapse) || !write_u32(main_range) || !write_u32(ext_range) ||
      !write_u32(main_len) || (main_len && fwrite(main_packet, 1, main_len, stdout) != main_len) ||
      !write_u32(ext_len) || (ext_len && fwrite(ext_packet, 1, ext_len, stdout) != ext_len) ||
      !write_u32(n)) return 1;
  for (i = 0; i < n; i++) {
    if (!write_u32((uint32_t)x[i])) return 1;
  }
  free(x);
  free(main_buf);
  free(ext_buf);
  free(main_packet);
  free(ext_packet);
  return 0;
}
