#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "celt/cwrs.h"
#include "celt/entdec.h"
#include "celt/entenc.h"
#include "celt/vq.h"

#define INPUT_MAGIC "GQVI"
#define OUTPUT_MAGIC "GQVO"

enum {
  MODE_ALG_QUANT_QEXT = 0,
  MODE_ALG_UNQUANT_QEXT = 1
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

static int eval_alg_quant_qext(void) {
  uint32_t n_u, k_u, spread_u, b_u, resynth_u, storage_u, ext_storage_u, extra_bits_u;
  float gain;
  celt_norm *x;
  unsigned char *buf;
  unsigned char *ext_buf;
  unsigned char *packet;
  unsigned char *ext_packet;
  ec_enc enc;
  ec_enc ext_enc;
  unsigned collapse;
  uint32_t packet_len;
  uint32_t ext_packet_len;
  uint32_t i;

  if (!read_u32(&n_u) || !read_u32(&k_u) || !read_u32(&spread_u) ||
      !read_u32(&b_u) || !read_u32(&resynth_u) || !read_u32(&extra_bits_u) ||
      !read_float(&gain) || !read_u32(&storage_u) || !read_u32(&ext_storage_u)) {
    return 0;
  }
  if (n_u <= 1 || n_u > 512 || k_u == 0 || k_u > 512 || b_u == 0 ||
      b_u > n_u || resynth_u > 1 || extra_bits_u < 2 || extra_bits_u > 12 ||
      storage_u == 0 || storage_u > 4096 || ext_storage_u == 0 || ext_storage_u > 4096) {
    return 0;
  }
  x = (celt_norm *)malloc((size_t)n_u * sizeof(*x));
  buf = (unsigned char *)calloc(storage_u, 1);
  ext_buf = (unsigned char *)calloc(ext_storage_u, 1);
  packet = (unsigned char *)calloc(storage_u, 1);
  ext_packet = (unsigned char *)calloc(ext_storage_u, 1);
  if (x == NULL || buf == NULL || ext_buf == NULL || packet == NULL || ext_packet == NULL) {
    free(x);
    free(buf);
    free(ext_buf);
    free(packet);
    free(ext_packet);
    return 0;
  }
  for (i = 0; i < n_u; i++) {
    if (!read_float(&x[i])) {
      free(x);
      free(buf);
      free(ext_buf);
      free(packet);
      free(ext_packet);
      return 0;
    }
  }
  ec_enc_init(&enc, buf, (opus_uint32)storage_u);
  ec_enc_init(&ext_enc, ext_buf, (opus_uint32)ext_storage_u);
  collapse = alg_quant(x, (int)n_u, (int)k_u, (int)spread_u, (int)b_u,
      &enc, gain, (int)resynth_u, &ext_enc, (int)extra_bits_u, 0);
  ec_enc_done(&enc);
  ec_enc_done(&ext_enc);
  packet_len = compact_packet(&enc, packet);
  ext_packet_len = compact_packet(&ext_enc, ext_packet);
  if (!write_u32(collapse) || !write_u32(packet_len) ||
      (packet_len > 0 && !write_exact(packet, packet_len)) ||
      !write_u32(ext_packet_len) ||
      (ext_packet_len > 0 && !write_exact(ext_packet, ext_packet_len)) ||
      !write_u32(n_u)) {
    free(x);
    free(buf);
    free(ext_buf);
    free(packet);
    free(ext_packet);
    return 0;
  }
  for (i = 0; i < n_u; i++) {
    if (!write_float(x[i])) {
      free(x);
      free(buf);
      free(ext_buf);
      free(packet);
      free(ext_packet);
      return 0;
    }
  }
  free(x);
  free(buf);
  free(ext_buf);
  free(packet);
  free(ext_packet);
  return 1;
}

static int eval_alg_unquant_qext(void) {
  uint32_t n_u, k_u, spread_u, b_u, payload_len_u, ext_payload_len_u, extra_bits_u;
  float gain;
  unsigned char *payload;
  unsigned char *ext_payload;
  celt_norm *x;
  ec_dec dec;
  ec_dec ext_dec;
  unsigned collapse;
  uint32_t i;

  if (!read_u32(&n_u) || !read_u32(&k_u) || !read_u32(&spread_u) ||
      !read_u32(&b_u) || !read_u32(&extra_bits_u) || !read_float(&gain) ||
      !read_u32(&payload_len_u) || !read_u32(&ext_payload_len_u)) {
    return 0;
  }
  if (n_u <= 1 || n_u > 512 || k_u == 0 || k_u > 512 || b_u == 0 ||
      b_u > n_u || extra_bits_u < 2 || extra_bits_u > 12 ||
      payload_len_u == 0 || payload_len_u > 4096 ||
      ext_payload_len_u == 0 || ext_payload_len_u > 4096) {
    return 0;
  }
  payload = (unsigned char *)malloc(payload_len_u);
  ext_payload = (unsigned char *)malloc(ext_payload_len_u);
  x = (celt_norm *)malloc((size_t)n_u * sizeof(*x));
  if (payload == NULL || ext_payload == NULL || x == NULL) {
    free(payload);
    free(ext_payload);
    free(x);
    return 0;
  }
  if (!read_exact(payload, payload_len_u) || !read_exact(ext_payload, ext_payload_len_u)) {
    free(payload);
    free(ext_payload);
    free(x);
    return 0;
  }
  ec_dec_init(&dec, payload, payload_len_u);
  ec_dec_init(&ext_dec, ext_payload, ext_payload_len_u);
  collapse = alg_unquant(x, (int)n_u, (int)k_u, (int)spread_u, (int)b_u,
      &dec, gain, &ext_dec, (int)extra_bits_u);
  if (!write_u32(collapse) || !write_u32(n_u)) {
    free(payload);
    free(ext_payload);
    free(x);
    return 0;
  }
  for (i = 0; i < n_u; i++) {
    if (!write_float(x[i])) {
      free(payload);
      free(ext_payload);
      free(x);
      return 0;
    }
  }
  free(payload);
  free(ext_payload);
  free(x);
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
  if (mode > MODE_ALG_UNQUANT_QEXT) return 1;

  if (!write_exact(OUTPUT_MAGIC, sizeof(magic)) || !write_u32(1) ||
      !write_u32(mode) || !write_u32(count)) {
    return 1;
  }
  for (i = 0; i < count; i++) {
    if (mode == MODE_ALG_QUANT_QEXT) {
      if (!eval_alg_quant_qext()) return 1;
    } else {
      if (!eval_alg_unquant_qext()) return 1;
    }
  }
  return 0;
}
