#include <stdint.h>
#include <stdio.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "silk/main.h"

#define INPUT_MAGIC "GSLT"
#define OUTPUT_MAGIC "GSLU"

enum {
  MODE_LTP_QUANT = 0,
  MODE_LTP_VQ = 1
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

static int read_i32(int32_t *out) {
  uint32_t raw;
  if (!read_u32(&raw)) return 0;
  *out = (int32_t)raw;
  return 1;
}

static int write_u32(uint32_t value) {
  return write_exact(&value, sizeof(value));
}

static int write_i32(int32_t value) {
  return write_exact(&value, sizeof(value));
}

static int read_i32_vector(opus_int32 *out, int n) {
  int i;
  int32_t raw;
  for (i = 0; i < n; i++) {
    if (!read_i32(&raw)) return 0;
    out[i] = (opus_int32)raw;
  }
  return 1;
}

static int write_quant_record(
    opus_int8 periodicity_index,
    opus_int32 sum_log_gain_Q7,
    opus_int pred_gain_Q7,
    const opus_int16 B_Q14[MAX_NB_SUBFR * LTP_ORDER],
    const opus_int8 cbk_index[MAX_NB_SUBFR]
) {
  int i;
  if (!write_i32((int32_t)periodicity_index)) return 0;
  if (!write_i32((int32_t)sum_log_gain_Q7)) return 0;
  if (!write_i32((int32_t)pred_gain_Q7)) return 0;
  for (i = 0; i < MAX_NB_SUBFR * LTP_ORDER; i++) {
    if (!write_i32((int32_t)B_Q14[i])) return 0;
  }
  for (i = 0; i < MAX_NB_SUBFR; i++) {
    if (!write_i32((int32_t)cbk_index[i])) return 0;
  }
  return 1;
}

static int eval_quant(void) {
  int32_t raw;
  int nb_subfr;
  int subfr_len;
  opus_int16 B_Q14[MAX_NB_SUBFR * LTP_ORDER] = {0};
  opus_int8 cbk_index[MAX_NB_SUBFR] = {0};
  opus_int8 periodicity_index = 0;
  opus_int32 sum_log_gain_Q7;
  opus_int pred_gain_Q7 = 0;
  opus_int32 XX_Q17[MAX_NB_SUBFR * LTP_ORDER * LTP_ORDER] = {0};
  opus_int32 xX_Q17[MAX_NB_SUBFR * LTP_ORDER] = {0};

  if (!read_i32(&raw)) return 0;
  nb_subfr = (int)raw;
  if (nb_subfr != 2 && nb_subfr != 4) return 0;
  if (!read_i32(&raw)) return 0;
  subfr_len = (int)raw;
  if (subfr_len <= 0) return 0;
  if (!read_i32(&raw)) return 0;
  sum_log_gain_Q7 = (opus_int32)raw;
  if (!read_i32_vector(XX_Q17, nb_subfr * LTP_ORDER * LTP_ORDER)) return 0;
  if (!read_i32_vector(xX_Q17, nb_subfr * LTP_ORDER)) return 0;

  silk_quant_LTP_gains(B_Q14, cbk_index, &periodicity_index, &sum_log_gain_Q7,
      &pred_gain_Q7, XX_Q17, xX_Q17, subfr_len, nb_subfr, 0);
  return write_quant_record(periodicity_index, sum_log_gain_Q7, pred_gain_Q7, B_Q14, cbk_index);
}

static int eval_vq(void) {
  int32_t raw;
  int cbk;
  int subfr_len;
  opus_int32 max_gain_Q7;
  opus_int8 ind = 0;
  opus_int32 res_nrg_Q15 = 0;
  opus_int32 rate_dist_Q8 = 0;
  opus_int gain_Q7 = 0;
  opus_int32 XX_Q17[LTP_ORDER * LTP_ORDER] = {0};
  opus_int32 xX_Q17[LTP_ORDER] = {0};

  if (!read_i32(&raw)) return 0;
  cbk = (int)raw;
  if (cbk < 0 || cbk >= NB_LTP_CBKS) return 0;
  if (!read_i32(&raw)) return 0;
  subfr_len = (int)raw;
  if (subfr_len <= 0) return 0;
  if (!read_i32(&raw)) return 0;
  max_gain_Q7 = (opus_int32)raw;
  if (!read_i32_vector(XX_Q17, LTP_ORDER * LTP_ORDER)) return 0;
  if (!read_i32_vector(xX_Q17, LTP_ORDER)) return 0;

  silk_VQ_WMat_EC_c(&ind, &res_nrg_Q15, &rate_dist_Q8, &gain_Q7,
      XX_Q17, xX_Q17, silk_LTP_vq_ptrs_Q7[cbk], silk_LTP_vq_gain_ptrs_Q7[cbk],
      silk_LTP_gain_BITS_Q5_ptrs[cbk], subfr_len, max_gain_Q7, silk_LTP_vq_sizes[cbk]);
  if (!write_i32((int32_t)ind)) return 0;
  if (!write_i32((int32_t)res_nrg_Q15)) return 0;
  if (!write_i32((int32_t)rate_dist_Q8)) return 0;
  return write_i32((int32_t)gain_Q7);
}

static int eval_record(uint32_t mode) {
  switch (mode) {
    case MODE_LTP_QUANT: return eval_quant();
    case MODE_LTP_VQ: return eval_vq();
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
  if (mode > MODE_LTP_VQ) return 1;

  if (!write_exact(OUTPUT_MAGIC, sizeof(magic)) || !write_u32(1) || !write_u32(count)) return 1;
  for (i = 0; i < count; i++) {
    if (!eval_record(mode)) return 1;
  }
  return 0;
}
