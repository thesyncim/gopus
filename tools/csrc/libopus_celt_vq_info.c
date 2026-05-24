#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "opus.h"
#include "celt/bands.h"
#include "celt/celt.h"
#include "celt/cwrs.h"
#include "celt/mathops.h"
#include "celt/modes.h"
#include "celt/pitch.h"
#include "celt/vq.h"

#define INPUT_MAGIC "GVCI"
#define OUTPUT_MAGIC "GVCO"

void celt_fatal(const char *str, const char *file, int line) {
  (void)str;
  (void)file;
  (void)line;
  abort();
}

enum {
  MODE_EXP_ROTATION = 0,
  MODE_RENORMALISE_VECTOR = 1,
  MODE_DENORMALISE_BANDS = 2,
  MODE_ALG_UNQUANT = 3,
  MODE_ENCODE_PULSES = 4,
  MODE_TYPE_SIZES = 5,
  MODE_LOWBAND_OUT_SCALE = 6,
  MODE_MULT32_32_Q31 = 7,
  MODE_STEREO_MERGE = 8,
  MODE_HAAR1 = 9,
  MODE_OP_PVQ_SEARCH = 10,
  MODE_ALG_QUANT = 11,
  MODE_THETA_DIST = 12,
  MODE_STEREO_ITHETA = 13
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

static int read_float(float *out) {
  uint32_t bits;
  if (!read_u32(&bits)) return 0;
  memcpy(out, &bits, sizeof(*out));
  return 1;
}

static int write_float(float value) {
  uint32_t bits;
  memcpy(&bits, &value, sizeof(bits));
  return write_u32(bits);
}

static uint32_t compact_packet(const ec_enc *enc, unsigned char *dst) {
  uint32_t len;
  uint32_t partial;
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

static int eval_exp_rotation(void) {
  uint32_t len_u, dir_u, stride_u, k_u, spread_u;
  celt_norm *x;
  uint32_t i;

  if (!read_u32(&len_u) || !read_u32(&dir_u) || !read_u32(&stride_u) ||
      !read_u32(&k_u) || !read_u32(&spread_u)) {
    return 0;
  }
  if (len_u == 0 || len_u > 512 || stride_u == 0 || stride_u > len_u) return 0;
  x = (celt_norm *)malloc((size_t)len_u * sizeof(*x));
  if (x == NULL) return 0;
  for (i = 0; i < len_u; i++) {
    if (!read_float(&x[i])) {
      free(x);
      return 0;
    }
  }
  exp_rotation(x, (int)len_u, (int)(int32_t)dir_u, (int)stride_u, (int)k_u, (int)spread_u);
  if (!write_u32(len_u)) {
    free(x);
    return 0;
  }
  for (i = 0; i < len_u; i++) {
    if (!write_float(x[i])) {
      free(x);
      return 0;
    }
  }
  free(x);
  return 1;
}

static int eval_renormalise_vector(void) {
  uint32_t len_u;
  float gain;
  celt_norm *x;
  uint32_t i;

  if (!read_u32(&len_u) || !read_float(&gain)) return 0;
  if (len_u == 0 || len_u > 512) return 0;
  x = (celt_norm *)malloc((size_t)len_u * sizeof(*x));
  if (x == NULL) return 0;
  for (i = 0; i < len_u; i++) {
    if (!read_float(&x[i])) {
      free(x);
      return 0;
    }
  }
  renormalise_vector(x, (int)len_u, gain, 0);
  if (!write_u32(len_u)) {
    free(x);
    return 0;
  }
  for (i = 0; i < len_u; i++) {
    if (!write_float(x[i])) {
      free(x);
      return 0;
    }
  }
  free(x);
  return 1;
}

static int eval_denormalise_bands(void) {
  uint32_t frame_size_u, start_u, end_u, lm_u, downsample_u, silence_u;
  CELTMode *mode;
  celt_norm *x;
  celt_sig *freq;
  celt_glog *band_log_e;
  uint32_t i;
  int err = OPUS_OK;
  int m;

  if (!read_u32(&frame_size_u) || !read_u32(&start_u) || !read_u32(&end_u) ||
      !read_u32(&lm_u) || !read_u32(&downsample_u) || !read_u32(&silence_u)) {
    return 0;
  }
  if (frame_size_u == 0 || frame_size_u > 2048 || lm_u > 3 ||
      downsample_u == 0 || downsample_u > 6 || silence_u > 1) {
    return 0;
  }
  m = 1 << lm_u;
  mode = (CELTMode *)opus_custom_mode_create(48000, (int)frame_size_u, &err);
  if (mode == NULL || err != OPUS_OK) return 0;
  if (end_u > (uint32_t)mode->nbEBands || start_u > end_u) {
    return 0;
  }
  x = (celt_norm *)malloc((size_t)frame_size_u * sizeof(*x));
  freq = (celt_sig *)malloc((size_t)frame_size_u * sizeof(*freq));
  band_log_e = (celt_glog *)malloc((size_t)mode->nbEBands * sizeof(*band_log_e));
  if (x == NULL || freq == NULL || band_log_e == NULL) {
    free(x);
    free(freq);
    free(band_log_e);
    return 0;
  }
  for (i = 0; i < frame_size_u; i++) {
    if (!read_float(&x[i])) {
      free(x);
      free(freq);
      free(band_log_e);
      return 0;
    }
  }
  for (i = 0; i < (uint32_t)mode->nbEBands; i++) {
    if (!read_float(&band_log_e[i])) {
      free(x);
      free(freq);
      free(band_log_e);
      return 0;
    }
  }
  denormalise_bands(mode, x, freq, band_log_e, (int)start_u, (int)end_u,
      m, (int)downsample_u, (int)silence_u);
  if (!write_u32(frame_size_u)) {
    free(x);
    free(freq);
    free(band_log_e);
    return 0;
  }
  for (i = 0; i < frame_size_u; i++) {
    if (!write_float(freq[i])) {
      free(x);
      free(freq);
      free(band_log_e);
      return 0;
    }
  }
  free(x);
  free(freq);
  free(band_log_e);
  return 1;
}

static int eval_alg_unquant(void) {
  uint32_t n_u, k_u, spread_u, b_u, payload_len_u;
  float gain;
  unsigned char *payload;
  celt_norm *x;
  ec_dec dec;
  unsigned collapse;
  uint32_t i;

  if (!read_u32(&n_u) || !read_u32(&k_u) || !read_u32(&spread_u) ||
      !read_u32(&b_u) || !read_float(&gain) || !read_u32(&payload_len_u)) {
    return 0;
  }
  if (n_u == 0 || n_u > 512 || k_u == 0 || b_u == 0 || b_u > n_u ||
      payload_len_u == 0 || payload_len_u > 4096) {
    return 0;
  }
  payload = (unsigned char *)malloc(payload_len_u);
  x = (celt_norm *)malloc((size_t)n_u * sizeof(*x));
  if (payload == NULL || x == NULL) {
    free(payload);
    free(x);
    return 0;
  }
  if (!read_exact(payload, payload_len_u)) {
    free(payload);
    free(x);
    return 0;
  }
  ec_dec_init(&dec, payload, payload_len_u);
  collapse = alg_unquant(x, (int)n_u, (int)k_u, (int)spread_u, (int)b_u, &dec, gain);
  if (!write_u32(collapse) || !write_u32(n_u)) {
    free(payload);
    free(x);
    return 0;
  }
  for (i = 0; i < n_u; i++) {
    if (!write_float(x[i])) {
      free(payload);
      free(x);
      return 0;
    }
  }
  free(payload);
  free(x);
  return 1;
}

static int eval_alg_quant(void) {
  uint32_t n_u, k_u, spread_u, b_u, resynth_u, storage_u;
  float gain;
  celt_norm *x;
  unsigned char *buf;
  unsigned char *packet;
  ec_enc enc;
  unsigned collapse;
  uint32_t packet_len;
  uint32_t i;

  if (!read_u32(&n_u) || !read_u32(&k_u) || !read_u32(&spread_u) ||
      !read_u32(&b_u) || !read_u32(&resynth_u) || !read_float(&gain) ||
      !read_u32(&storage_u)) {
    return 0;
  }
  if (n_u <= 1 || n_u > 512 || k_u == 0 || k_u > 512 || b_u == 0 ||
      b_u > n_u || resynth_u > 1 || storage_u == 0 || storage_u > 4096) {
    return 0;
  }
  x = (celt_norm *)malloc((size_t)n_u * sizeof(*x));
  buf = (unsigned char *)calloc(storage_u, 1);
  packet = (unsigned char *)calloc(storage_u, 1);
  if (x == NULL || buf == NULL || packet == NULL) {
    free(x);
    free(buf);
    free(packet);
    return 0;
  }
  for (i = 0; i < n_u; i++) {
    if (!read_float(&x[i])) {
      free(x);
      free(buf);
      free(packet);
      return 0;
    }
  }
  ec_enc_init(&enc, buf, (opus_uint32)storage_u);
  collapse = alg_quant(x, (int)n_u, (int)k_u, (int)spread_u, (int)b_u,
      &enc, gain, (int)resynth_u, 0);
  ec_enc_done(&enc);
  packet_len = compact_packet(&enc, packet);
  if (!write_u32(collapse) || !write_u32(packet_len) ||
      (packet_len > 0 && !write_exact(packet, packet_len)) || !write_u32(n_u)) {
    free(x);
    free(buf);
    free(packet);
    return 0;
  }
  for (i = 0; i < n_u; i++) {
    if (!write_float(x[i])) {
      free(x);
      free(buf);
      free(packet);
      return 0;
    }
  }
  free(x);
  free(buf);
  free(packet);
  return 1;
}

static int eval_theta_dist(void) {
  uint32_t n_u;
  float ex_f, ey_f;
  celt_ener ex, ey, min_e;
  opus_val16 w0, w1;
  opus_val32 p0, p1, dist;
  celt_norm *x0;
  celt_norm *x1;
  celt_norm *y0;
  celt_norm *y1;
  uint32_t i;

  if (!read_float(&ex_f) || !read_float(&ey_f) || !read_u32(&n_u)) return 0;
  if (n_u == 0 || n_u > 512) return 0;
  x0 = (celt_norm *)malloc((size_t)n_u * sizeof(*x0));
  x1 = (celt_norm *)malloc((size_t)n_u * sizeof(*x1));
  y0 = (celt_norm *)malloc((size_t)n_u * sizeof(*y0));
  y1 = (celt_norm *)malloc((size_t)n_u * sizeof(*y1));
  if (x0 == NULL || x1 == NULL || y0 == NULL || y1 == NULL) {
    free(x0);
    free(x1);
    free(y0);
    free(y1);
    return 0;
  }
  for (i = 0; i < n_u; i++) {
    if (!read_float(&x0[i]) || !read_float(&x1[i]) ||
        !read_float(&y0[i]) || !read_float(&y1[i])) {
      free(x0);
      free(x1);
      free(y0);
      free(y1);
      return 0;
    }
  }
  ex = (celt_ener)ex_f;
  ey = (celt_ener)ey_f;
  min_e = ex < ey ? ex : ey;
  ex = ADD32(ex, min_e/3);
  ey = ADD32(ey, min_e/3);
  w0 = VSHR32(ex, 0);
  w1 = VSHR32(ey, 0);
  p0 = celt_inner_prod_norm_shift(x0, x1, (int)n_u, 0);
  p1 = celt_inner_prod_norm_shift(y0, y1, (int)n_u, 0);
  dist = MULT16_32_Q15(w0, p0) + MULT16_32_Q15(w1, p1);
  if (!write_float(w0) || !write_float(w1) ||
      !write_float(p0) || !write_float(p1) || !write_float(dist)) {
    free(x0);
    free(x1);
    free(y0);
    free(y1);
    return 0;
  }
  free(x0);
  free(x1);
  free(y0);
  free(y1);
  return 1;
}

static int eval_stereo_itheta(void) {
  uint32_t len_u, stereo_u;
  celt_norm *x;
  celt_norm *y;
  opus_int32 itheta;
  uint32_t i;

  if (!read_u32(&len_u) || !read_u32(&stereo_u)) return 0;
  if (len_u == 0 || len_u > 512 || stereo_u > 1) return 0;
  x = (celt_norm *)malloc((size_t)len_u * sizeof(*x));
  y = (celt_norm *)malloc((size_t)len_u * sizeof(*y));
  if (x == NULL || y == NULL) {
    free(x);
    free(y);
    return 0;
  }
  for (i = 0; i < len_u; i++) {
    if (!read_float(&x[i])) {
      free(x);
      free(y);
      return 0;
    }
  }
  for (i = 0; i < len_u; i++) {
    if (!read_float(&y[i])) {
      free(x);
      free(y);
      return 0;
    }
  }
  itheta = stereo_itheta(x, y, (int)stereo_u, (int)len_u, 0);
  if (!write_u32((uint32_t)itheta)) {
    free(x);
    free(y);
    return 0;
  }
  free(x);
  free(y);
  return 1;
}

static int eval_encode_pulses(void) {
  uint32_t n_u, k_u, storage_u;
  int *pulses;
  unsigned char *buf;
  unsigned char *packet;
  ec_enc enc;
  uint32_t packet_len;
  uint32_t i;

  if (!read_u32(&n_u) || !read_u32(&k_u) || !read_u32(&storage_u)) return 0;
  if (n_u == 0 || n_u > 512 || k_u == 0 || storage_u == 0 || storage_u > 4096) return 0;
  pulses = (int *)malloc((size_t)n_u * sizeof(*pulses));
  buf = (unsigned char *)calloc(storage_u, 1);
  packet = (unsigned char *)calloc(storage_u, 1);
  if (pulses == NULL || buf == NULL || packet == NULL) {
    free(pulses);
    free(buf);
    free(packet);
    return 0;
  }
  for (i = 0; i < n_u; i++) {
    uint32_t v;
    if (!read_u32(&v)) {
      free(pulses);
      free(buf);
      free(packet);
      return 0;
    }
    pulses[i] = (int)(int32_t)v;
  }
  ec_enc_init(&enc, buf, (opus_uint32)storage_u);
  encode_pulses(pulses, (int)n_u, (int)k_u, &enc);
  ec_enc_done(&enc);
  packet_len = compact_packet(&enc, packet);
  if (!write_u32(packet_len) || !write_exact(packet, packet_len)) {
    free(pulses);
    free(buf);
    free(packet);
    return 0;
  }
  free(pulses);
  free(buf);
  free(packet);
  return 1;
}

static int eval_type_sizes(void) {
  return write_u32((uint32_t)sizeof(celt_norm)) &&
      write_u32((uint32_t)sizeof(celt_sig)) &&
      write_u32((uint32_t)sizeof(celt_ener)) &&
      write_u32((uint32_t)sizeof(celt_glog)) &&
      write_u32((uint32_t)sizeof(opus_val16)) &&
      write_u32((uint32_t)sizeof(opus_val32)) &&
      write_u32((uint32_t)sizeof(opus_res)) &&
      write_u32((uint32_t)sizeof(((AnalysisInfo *)0)->activity));
}

static int eval_lowband_out_scale(void) {
  uint32_t len_u;
  celt_norm *x;
  opus_val16 n;
  uint32_t i;

  if (!read_u32(&len_u)) return 0;
  if (len_u == 0 || len_u > 512) return 0;
  x = (celt_norm *)malloc((size_t)len_u * sizeof(*x));
  if (x == NULL) return 0;
  for (i = 0; i < len_u; i++) {
    if (!read_float(&x[i])) {
      free(x);
      return 0;
    }
  }
  n = celt_sqrt(SHL32(EXTEND32(len_u),22));
  if (!write_u32(len_u)) {
    free(x);
    return 0;
  }
  for (i = 0; i < len_u; i++) {
    if (!write_float(MULT16_32_Q15(n, x[i]))) {
      free(x);
      return 0;
    }
  }
  free(x);
  return 1;
}

static int eval_mult32_32_q31(void) {
  opus_val32 a;
  opus_val32 b;
  float af;
  float bf;

  if (!read_float(&af) || !read_float(&bf)) return 0;
  a = af;
  b = bf;
  return write_float(MULT32_32_Q31(a, b));
}

static int eval_stereo_merge(void) {
  uint32_t len_u;
  float mid_f;
  celt_norm *x;
  celt_norm *y;
  opus_val32 mid;
  opus_val32 xp;
  opus_val32 side;
  opus_val32 el;
  opus_val32 er;
  opus_val32 t;
  opus_val32 lgain;
  opus_val32 rgain;
  uint32_t i;

  if (!read_u32(&len_u) || !read_float(&mid_f)) return 0;
  if (len_u == 0 || len_u > 512) return 0;
  x = (celt_norm *)malloc((size_t)len_u * sizeof(*x));
  y = (celt_norm *)malloc((size_t)len_u * sizeof(*y));
  if (x == NULL || y == NULL) {
    free(x);
    free(y);
    return 0;
  }
  for (i = 0; i < len_u; i++) {
    if (!read_float(&x[i])) {
      free(x);
      free(y);
      return 0;
    }
  }
  for (i = 0; i < len_u; i++) {
    if (!read_float(&y[i])) {
      free(x);
      free(y);
      return 0;
    }
  }

  mid = mid_f;
  xp = celt_inner_prod_norm_shift(y, x, (int)len_u, 0);
  side = celt_inner_prod_norm_shift(y, y, (int)len_u, 0);
  xp = MULT32_32_Q31(mid, xp);
  el = SHR32(MULT32_32_Q31(mid, mid), 3) + side - 2*xp;
  er = SHR32(MULT32_32_Q31(mid, mid), 3) + side + 2*xp;
  if (er < QCONST32(6e-4f, 28) || el < QCONST32(6e-4f, 28)) {
    memcpy(y, x, (size_t)len_u * sizeof(*y));
  } else {
    t = VSHR32(el, -29);
    lgain = celt_rsqrt_norm32(t);
    t = VSHR32(er, -29);
    rgain = celt_rsqrt_norm32(t);
    for (i = 0; i < len_u; i++) {
      celt_norm r;
      celt_norm l;
      l = MULT32_32_Q31(mid, x[i]);
      r = y[i];
      x[i] = VSHR32(MULT32_32_Q31(lgain, SUB32(l, r)), -15);
      y[i] = VSHR32(MULT32_32_Q31(rgain, ADD32(l, r)), -15);
    }
  }

  if (!write_u32(len_u)) {
    free(x);
    free(y);
    return 0;
  }
  for (i = 0; i < len_u; i++) {
    if (!write_float(x[i])) {
      free(x);
      free(y);
      return 0;
    }
  }
  for (i = 0; i < len_u; i++) {
    if (!write_float(y[i])) {
      free(x);
      free(y);
      return 0;
    }
  }
  free(x);
  free(y);
  return 1;
}

static int eval_haar1(void) {
  uint32_t n0_u;
  uint32_t stride_u;
  uint32_t len_u;
  celt_norm *x;
  uint32_t i;

  if (!read_u32(&n0_u) || !read_u32(&stride_u) || !read_u32(&len_u)) return 0;
  if (n0_u == 0 || n0_u > 512 || stride_u == 0 || stride_u > 32 ||
      len_u == 0 || len_u > 4096 || len_u != n0_u * stride_u) {
    return 0;
  }
  x = (celt_norm *)malloc((size_t)len_u * sizeof(*x));
  if (x == NULL) return 0;
  for (i = 0; i < len_u; i++) {
    if (!read_float(&x[i])) {
      free(x);
      return 0;
    }
  }
  haar1(x, (int)n0_u, (int)stride_u);
  if (!write_u32(len_u)) {
    free(x);
    return 0;
  }
  for (i = 0; i < len_u; i++) {
    if (!write_float(x[i])) {
      free(x);
      return 0;
    }
  }
  free(x);
  return 1;
}

static int eval_op_pvq_search(void) {
  uint32_t n_u;
  uint32_t k_u;
  celt_norm *x;
  int *iy;
  opus_val16 yy;
  uint32_t i;

  if (!read_u32(&n_u) || !read_u32(&k_u)) return 0;
  if (n_u == 0 || n_u > 512 || k_u == 0 || k_u > 512) return 0;
  x = (celt_norm *)malloc((size_t)n_u * sizeof(*x));
  iy = (int *)calloc(n_u, sizeof(*iy));
  if (x == NULL || iy == NULL) {
    free(x);
    free(iy);
    return 0;
  }
  for (i = 0; i < n_u; i++) {
    if (!read_float(&x[i])) {
      free(x);
      free(iy);
      return 0;
    }
  }
  yy = op_pvq_search_c(x, iy, (int)k_u, (int)n_u, 0);
  if (!write_float(yy) || !write_u32(n_u)) {
    free(x);
    free(iy);
    return 0;
  }
  for (i = 0; i < n_u; i++) {
    if (!write_u32((uint32_t)(int32_t)iy[i])) {
      free(x);
      free(iy);
      return 0;
    }
  }
  free(x);
  free(iy);
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
  if (mode > MODE_STEREO_ITHETA) return 1;

  if (!write_exact(OUTPUT_MAGIC, sizeof(magic)) || !write_u32(1) ||
      !write_u32(mode) || !write_u32(count)) {
    return 1;
  }
  for (i = 0; i < count; i++) {
    if (mode == MODE_EXP_ROTATION) {
      if (!eval_exp_rotation()) return 1;
    } else if (mode == MODE_RENORMALISE_VECTOR) {
      if (!eval_renormalise_vector()) return 1;
    } else if (mode == MODE_DENORMALISE_BANDS) {
      if (!eval_denormalise_bands()) return 1;
    } else if (mode == MODE_ALG_UNQUANT) {
      if (!eval_alg_unquant()) return 1;
    } else if (mode == MODE_ALG_QUANT) {
      if (!eval_alg_quant()) return 1;
    } else if (mode == MODE_THETA_DIST) {
      if (!eval_theta_dist()) return 1;
    } else if (mode == MODE_STEREO_ITHETA) {
      if (!eval_stereo_itheta()) return 1;
    } else if (mode == MODE_ENCODE_PULSES) {
      if (!eval_encode_pulses()) return 1;
    } else if (mode == MODE_TYPE_SIZES) {
      if (!eval_type_sizes()) return 1;
    } else if (mode == MODE_LOWBAND_OUT_SCALE) {
      if (!eval_lowband_out_scale()) return 1;
    } else if (mode == MODE_MULT32_32_Q31) {
      if (!eval_mult32_32_q31()) return 1;
    } else if (mode == MODE_STEREO_MERGE) {
      if (!eval_stereo_merge()) return 1;
    } else if (mode == MODE_HAAR1) {
      if (!eval_haar1()) return 1;
    } else {
      if (!eval_op_pvq_search()) return 1;
    }
  }
  return 0;
}
