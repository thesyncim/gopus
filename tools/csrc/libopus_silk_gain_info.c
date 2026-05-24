#include <stdint.h>
#include <stdio.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "silk/main.h"
#include "silk/float/structs_FLP.h"

#define INPUT_MAGIC "GSGI"
#define OUTPUT_MAGIC "GSGO"

enum {
  MODE_GAINS_QUANT = 0,
  MODE_GAINS_DEQUANT = 1,
  MODE_GAINS_ID = 2,
  MODE_SHAPE_STATE_SIZES = 3
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

static int write_i32(int32_t value) {
  return write_exact(&value, sizeof(value));
}

static int write_u32(uint32_t value) {
  return write_exact(&value, sizeof(value));
}

static int read_i32(int32_t *out) {
  uint32_t raw;
  if (!read_u32(&raw)) return 0;
  *out = (int32_t)raw;
  return 1;
}

static int write_gain_record(int32_t first_value, const opus_int8 ind[MAX_NB_SUBFR], const opus_int32 gain_Q16[MAX_NB_SUBFR]) {
  int i;
  if (!write_i32(first_value)) return 0;
  for (i = 0; i < MAX_NB_SUBFR; i++) {
    if (!write_i32((int32_t)ind[i])) return 0;
  }
  for (i = 0; i < MAX_NB_SUBFR; i++) {
    if (!write_i32((int32_t)gain_Q16[i])) return 0;
  }
  return 1;
}

static int eval_quant(void) {
  int i;
  int32_t raw;
  int nb_subfr;
  int conditional;
  opus_int8 prev_ind;
  opus_int8 ind[MAX_NB_SUBFR] = {0};
  opus_int32 gain_Q16[MAX_NB_SUBFR] = {0};
  if (!read_i32(&raw)) return 0;
  nb_subfr = (int)raw;
  if (nb_subfr != 2 && nb_subfr != 4) return 0;
  if (!read_i32(&raw)) return 0;
  prev_ind = (opus_int8)raw;
  if (!read_i32(&raw)) return 0;
  conditional = raw != 0;
  for (i = 0; i < MAX_NB_SUBFR; i++) {
    if (!read_i32(&raw)) return 0;
    gain_Q16[i] = (opus_int32)raw;
  }
  silk_gains_quant(ind, gain_Q16, &prev_ind, conditional, nb_subfr);
  return write_gain_record(prev_ind, ind, gain_Q16);
}

static int eval_dequant(void) {
  int i;
  int32_t raw;
  int nb_subfr;
  int conditional;
  opus_int8 prev_ind;
  opus_int8 ind[MAX_NB_SUBFR] = {0};
  opus_int32 gain_Q16[MAX_NB_SUBFR] = {0};
  if (!read_i32(&raw)) return 0;
  nb_subfr = (int)raw;
  if (nb_subfr != 2 && nb_subfr != 4) return 0;
  if (!read_i32(&raw)) return 0;
  prev_ind = (opus_int8)raw;
  if (!read_i32(&raw)) return 0;
  conditional = raw != 0;
  for (i = 0; i < MAX_NB_SUBFR; i++) {
    if (!read_i32(&raw)) return 0;
    ind[i] = (opus_int8)raw;
  }
  silk_gains_dequant(gain_Q16, ind, &prev_ind, conditional, nb_subfr);
  return write_gain_record(prev_ind, ind, gain_Q16);
}

static int eval_id(void) {
  int i;
  int32_t raw;
  int nb_subfr;
  opus_int8 ind[MAX_NB_SUBFR] = {0};
  opus_int32 gain_Q16[MAX_NB_SUBFR] = {0};
  opus_int32 gains_id;
  if (!read_i32(&raw)) return 0;
  nb_subfr = (int)raw;
  if (nb_subfr != 2 && nb_subfr != 4) return 0;
  for (i = 0; i < MAX_NB_SUBFR; i++) {
    if (!read_i32(&raw)) return 0;
    ind[i] = (opus_int8)raw;
  }
  gains_id = silk_gains_ID(ind, nb_subfr);
  gain_Q16[0] = gains_id;
  return write_gain_record(gains_id, ind, gain_Q16);
}

static int eval_shape_state_sizes(void) {
  opus_int8 ind[MAX_NB_SUBFR] = {0};
  opus_int32 gain_Q16[MAX_NB_SUBFR] = {0};
  gain_Q16[0] = (opus_int32)sizeof(silk_shape_state_FLP);
  return write_gain_record((opus_int32)sizeof(((silk_shape_state_FLP *)0)->LastGainIndex), ind, gain_Q16);
}

static int eval_record(uint32_t mode) {
  switch (mode) {
    case MODE_GAINS_QUANT: return eval_quant();
    case MODE_GAINS_DEQUANT: return eval_dequant();
    case MODE_GAINS_ID: return eval_id();
    case MODE_SHAPE_STATE_SIZES: return eval_shape_state_sizes();
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
  if (mode > MODE_SHAPE_STATE_SIZES) return 1;

  if (!write_exact(OUTPUT_MAGIC, sizeof(magic)) || !write_u32(1) || !write_u32(count)) return 1;
  for (i = 0; i < count; i++) {
    if (!eval_record(mode)) return 1;
  }
  return 0;
}
