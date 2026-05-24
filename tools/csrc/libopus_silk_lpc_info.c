#include <stdint.h>
#include <stdio.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "silk/float/main_FLP.h"

#define INPUT_MAGIC "GSLI"
#define OUTPUT_MAGIC "GSLO"

enum {
  MODE_BURG_MODIFIED_FLP = 0,
  MODE_LPC_ANALYSIS_FILTER_FLP = 1
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

static int eval_burg_modified(void) {
  uint32_t raw;
  uint32_t subfr_length;
  uint32_t nb_subfr;
  uint32_t order;
  uint32_t total;
  uint32_t i;
  silk_float min_inv_gain;
  silk_float x[384];
  silk_float a[16] = {0};
  silk_float res_nrg;
  if (!read_u32(&subfr_length) || !read_u32(&nb_subfr) || !read_u32(&order) || !read_u32(&raw)) return 0;
  if (subfr_length == 0 || nb_subfr == 0 || (order != 10 && order != 16)) return 0;
  total = subfr_length * nb_subfr;
  if (nb_subfr != 0 && total / nb_subfr != subfr_length) return 0;
  if (total > 384) return 0;
  memcpy(&min_inv_gain, &raw, sizeof(min_inv_gain));
  for (i = 0; i < total; i++) {
    if (!read_u32(&raw)) return 0;
    memcpy(&x[i], &raw, sizeof(x[i]));
  }
  res_nrg = silk_burg_modified_FLP(a, x, min_inv_gain, (opus_int)subfr_length, (opus_int)nb_subfr, (opus_int)order, 0);
  memcpy(&raw, &res_nrg, sizeof(raw));
  if (!write_u32(raw) || !write_u32(order)) return 0;
  for (i = 0; i < 16; i++) {
    uint32_t bits = 0;
    if (i < order) {
      memcpy(&bits, &a[i], sizeof(bits));
    }
    if (!write_u32(bits)) return 0;
  }
  return 1;
}

static int eval_lpc_analysis_filter(void) {
  uint32_t raw;
  uint32_t length;
  uint32_t order;
  uint32_t i;
  silk_float pred[16] = {0};
  silk_float s[512];
  silk_float r[512];
  if (!read_u32(&length) || !read_u32(&order)) return 0;
  if (length == 0 || length > 512 || (order != 10 && order != 16) || length < order) return 0;
  for (i = 0; i < order; i++) {
    if (!read_u32(&raw)) return 0;
    memcpy(&pred[i], &raw, sizeof(pred[i]));
  }
  for (i = 0; i < length; i++) {
    if (!read_u32(&raw)) return 0;
    memcpy(&s[i], &raw, sizeof(s[i]));
  }
  silk_LPC_analysis_filter_FLP(r, pred, s, (opus_int)length, (opus_int)order);
  if (!write_u32(length)) return 0;
  for (i = 0; i < length; i++) {
    memcpy(&raw, &r[i], sizeof(raw));
    if (!write_u32(raw)) return 0;
  }
  return 1;
}

static int eval_record(uint32_t mode) {
  switch (mode) {
    case MODE_BURG_MODIFIED_FLP: return eval_burg_modified();
    case MODE_LPC_ANALYSIS_FILTER_FLP: return eval_lpc_analysis_filter();
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
  if (mode > MODE_LPC_ANALYSIS_FILTER_FLP) return 1;

  if (!write_exact(OUTPUT_MAGIC, sizeof(magic)) || !write_u32(1) || !write_u32(count)) return 1;
  for (i = 0; i < count; i++) {
    if (!eval_record(mode)) return 1;
  }
  return 0;
}
