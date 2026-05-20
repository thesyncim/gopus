#include <stdint.h>
#include <stdio.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "silk/main.h"

#define INPUT_MAGIC "GSNI"
#define OUTPUT_MAGIC "GSNO"

enum {
  MODE_NLSF_DECODE = 0,
  MODE_NLSF2A = 1,
  MODE_A2NLSF = 2,
  MODE_NLSF_STABILIZE = 3
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

static const silk_NLSF_CB_struct *select_cb(uint32_t cb_id) {
  switch (cb_id) {
    case 0: return &silk_NLSF_CB_NB_MB;
    case 1: return &silk_NLSF_CB_WB;
  }
  return NULL;
}

static int write_i16_vector(int order, const opus_int16 values[16]) {
  int i;
  if (!write_u32((uint32_t)order)) return 0;
  for (i = 0; i < 16; i++) {
    int32_t v = i < order ? values[i] : 0;
    if (!write_exact(&v, sizeof(v))) return 0;
  }
  return 1;
}

static int eval_decode(void) {
  uint32_t cb_id;
  uint32_t raw;
  int i;
  opus_int8 indices[MAX_LPC_ORDER + 1];
  opus_int16 nlsf[16] = {0};
  const silk_NLSF_CB_struct *cb;
  if (!read_u32(&cb_id)) return 0;
  cb = select_cb(cb_id);
  if (cb == NULL) return 0;
  for (i = 0; i < cb->order + 1; i++) {
    if (!read_u32(&raw)) return 0;
    indices[i] = (opus_int8)(int32_t)raw;
  }
  silk_NLSF_decode(nlsf, indices, cb);
  return write_i16_vector(cb->order, nlsf);
}

static int eval_nlsf2a(void) {
  uint32_t raw;
  int i;
  int order;
  opus_int16 nlsf[16] = {0};
  opus_int16 a_q12[16] = {0};
  if (!read_u32(&raw)) return 0;
  order = (int)raw;
  if (order != 10 && order != 16) return 0;
  for (i = 0; i < order; i++) {
    if (!read_u32(&raw)) return 0;
    nlsf[i] = (opus_int16)(int32_t)raw;
  }
  silk_NLSF2A(a_q12, nlsf, order, 0);
  return write_i16_vector(order, a_q12);
}

static int eval_a2nlsf(void) {
  uint32_t raw;
  int i;
  int order;
  opus_int32 a_q16[16] = {0};
  opus_int16 nlsf[16] = {0};
  if (!read_u32(&raw)) return 0;
  order = (int)raw;
  if (order != 10 && order != 16) return 0;
  for (i = 0; i < order; i++) {
    if (!read_u32(&raw)) return 0;
    a_q16[i] = (opus_int32)(int32_t)raw;
  }
  silk_A2NLSF(nlsf, a_q16, order);
  return write_i16_vector(order, nlsf);
}

static int eval_stabilize(void) {
  uint32_t cb_id;
  uint32_t raw;
  int i;
  opus_int16 nlsf[16] = {0};
  const silk_NLSF_CB_struct *cb;
  if (!read_u32(&cb_id)) return 0;
  cb = select_cb(cb_id);
  if (cb == NULL) return 0;
  for (i = 0; i < cb->order; i++) {
    if (!read_u32(&raw)) return 0;
    nlsf[i] = (opus_int16)(int32_t)raw;
  }
  silk_NLSF_stabilize(nlsf, cb->deltaMin_Q15, cb->order);
  return write_i16_vector(cb->order, nlsf);
}

static int eval_record(uint32_t mode) {
  switch (mode) {
    case MODE_NLSF_DECODE: return eval_decode();
    case MODE_NLSF2A: return eval_nlsf2a();
    case MODE_A2NLSF: return eval_a2nlsf();
    case MODE_NLSF_STABILIZE: return eval_stabilize();
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
  if (mode > MODE_NLSF_STABILIZE) return 1;

  if (!write_exact(OUTPUT_MAGIC, sizeof(magic)) || !write_u32(1) || !write_u32(count)) return 1;
  for (i = 0; i < count; i++) {
    if (!eval_record(mode)) return 1;
  }
  return 0;
}
