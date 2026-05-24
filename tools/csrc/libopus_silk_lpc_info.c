#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "silk/float/main_FLP.h"

#define INPUT_MAGIC "GSLI"
#define OUTPUT_MAGIC "GSLO"

#if defined(ENABLE_ASSERTIONS) || defined(ENABLE_HARDENING)
void celt_fatal(const char *str, const char *file, int line) {
  (void)str;
  (void)file;
  (void)line;
  abort();
}
#endif

enum {
  MODE_BURG_MODIFIED_FLP = 0,
  MODE_LPC_ANALYSIS_FILTER_FLP = 1,
  MODE_INNER_PRODUCT_FLP = 2,
  MODE_ENERGY_FLP = 3,
  MODE_FIND_LPC_FLP = 4
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

static int write_double(double value) {
  return write_exact(&value, sizeof(value));
}

void silk_A2NLSF_FLP(
    opus_int16 *NLSF_Q15,
    const silk_float *pAR,
    const opus_int LPC_order
)
{
  opus_int i;
  opus_int32 a_fix_Q16[MAX_LPC_ORDER];

  for (i = 0; i < LPC_order; i++) {
    a_fix_Q16[i] = silk_float2int(pAR[i] * 65536.0f);
  }

  silk_A2NLSF(NLSF_Q15, a_fix_Q16, LPC_order);
}

void silk_NLSF2A_FLP(
    silk_float *pAR,
    const opus_int16 *NLSF_Q15,
    const opus_int LPC_order,
    int arch
)
{
  opus_int i;
  opus_int16 a_fix_Q12[MAX_LPC_ORDER];

  silk_NLSF2A(a_fix_Q12, NLSF_Q15, LPC_order, arch);

  for (i = 0; i < LPC_order; i++) {
    pAR[i] = (silk_float)a_fix_Q12[i] * (1.0f / 4096.0f);
  }
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

static int eval_inner_product(void) {
  uint32_t raw;
  uint32_t length;
  uint32_t i;
  silk_float a[512];
  silk_float b[512];
  double v;
  if (!read_u32(&length)) return 0;
  if (length == 0 || length > 512) return 0;
  for (i = 0; i < length; i++) {
    if (!read_u32(&raw)) return 0;
    memcpy(&a[i], &raw, sizeof(a[i]));
  }
  for (i = 0; i < length; i++) {
    if (!read_u32(&raw)) return 0;
    memcpy(&b[i], &raw, sizeof(b[i]));
  }
  v = silk_inner_product_FLP(a, b, (opus_int)length, 0);
  return write_double(v);
}

static int eval_energy(void) {
  uint32_t raw;
  uint32_t length;
  uint32_t i;
  silk_float x[512];
  double v;
  if (!read_u32(&length)) return 0;
  if (length == 0 || length > 512) return 0;
  for (i = 0; i < length; i++) {
    if (!read_u32(&raw)) return 0;
    memcpy(&x[i], &raw, sizeof(x[i]));
  }
  v = silk_energy_FLP(x, (opus_int)length);
  return write_double(v);
}

static int eval_find_lpc(void) {
  uint32_t raw;
  uint32_t subfr_length;
  uint32_t nb_subfr;
  uint32_t order;
  uint32_t use_interp;
  uint32_t first_frame_after_reset;
  uint32_t total;
  uint32_t i;
  silk_encoder_state st;
  opus_int16 nlsf[16] = {0};
  silk_float min_inv_gain;
  silk_float x[384];

  if (!read_u32(&subfr_length) || !read_u32(&nb_subfr) || !read_u32(&order) ||
      !read_u32(&use_interp) || !read_u32(&first_frame_after_reset) || !read_u32(&raw)) {
    return 0;
  }
  if (subfr_length == 0 || nb_subfr == 0 || (nb_subfr != 2 && nb_subfr != 4) ||
      (order != 10 && order != 16) || use_interp > 1 || first_frame_after_reset > 1) {
    return 0;
  }
  total = nb_subfr * (subfr_length + order);
  if (nb_subfr != 0 && total / nb_subfr != subfr_length + order) return 0;
  if (total > 384) return 0;

  memcpy(&min_inv_gain, &raw, sizeof(min_inv_gain));
  memset(&st, 0, sizeof(st));
  st.subfr_length = (opus_int)subfr_length;
  st.nb_subfr = (opus_int)nb_subfr;
  st.predictLPCOrder = (opus_int)order;
  st.useInterpolatedNLSFs = (opus_int)use_interp;
  st.first_frame_after_reset = (opus_int)first_frame_after_reset;
  st.arch = 0;

  for (i = 0; i < 16; i++) {
    if (!read_u32(&raw)) return 0;
    if (i < order) st.prev_NLSFq_Q15[i] = (opus_int16)(int32_t)raw;
  }
  for (i = 0; i < total; i++) {
    if (!read_u32(&raw)) return 0;
    memcpy(&x[i], &raw, sizeof(x[i]));
  }

  silk_find_LPC_FLP(&st, nlsf, x, min_inv_gain, 0);

  if (!write_u32(order) || !write_u32((uint32_t)(int32_t)st.indices.NLSFInterpCoef_Q2)) return 0;
  for (i = 0; i < 16; i++) {
    int32_t v = 0;
    if (i < order) v = nlsf[i];
    if (!write_u32((uint32_t)v)) return 0;
  }
  return 1;
}

static int eval_record(uint32_t mode) {
  switch (mode) {
    case MODE_BURG_MODIFIED_FLP: return eval_burg_modified();
    case MODE_LPC_ANALYSIS_FILTER_FLP: return eval_lpc_analysis_filter();
    case MODE_INNER_PRODUCT_FLP: return eval_inner_product();
    case MODE_ENERGY_FLP: return eval_energy();
    case MODE_FIND_LPC_FLP: return eval_find_lpc();
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
  if (mode > MODE_FIND_LPC_FLP) return 1;

  if (!write_exact(OUTPUT_MAGIC, sizeof(magic)) || !write_u32(1) || !write_u32(count)) return 1;
  for (i = 0; i < count; i++) {
    if (!eval_record(mode)) return 1;
  }
  return 0;
}
