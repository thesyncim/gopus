#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"

#define CELT_C
#include "entcode.h"
#include "entenc.h"
#include "entdec.h"

#include "entenc.c"
#include "entdec.c"
#include "entcode.c"

#define INPUT_MAGIC "GRCI"
#define OUTPUT_MAGIC "GRCO"

enum {
  OP_ENCODE = 0,
  OP_ENCODE_BIN = 1,
  OP_ENC_BIT = 2,
  OP_ENC_ICDF8 = 3,
  OP_ENC_ICDF16 = 4,
  OP_ENC_UINT = 5,
  OP_ENC_BITS = 6,
  OP_PATCH = 7,
  OP_SHRINK = 8,
  OP_DONE = 9
};

typedef struct {
  uint32_t tell;
  uint32_t tell_frac;
  uint32_t range_bytes;
  uint32_t rng;
  uint32_t val;
  uint32_t rem;
  uint32_t ext;
  uint32_t error;
} trace_t;

static const unsigned char icdf8_0[] = {128, 0};
static const unsigned char icdf8_1[] = {220, 170, 100, 20, 0};
static const unsigned char icdf8_2[] = {250, 240, 200, 150, 80, 20, 0};

static const opus_uint16 icdf16_0[] = {500, 300, 100, 0};
static const opus_uint16 icdf16_1[] = {65000, 60000, 45000, 20000, 1000, 0};

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

static const unsigned char *select_icdf8(uint32_t id) {
  switch (id) {
    case 0: return icdf8_0;
    case 1: return icdf8_1;
    case 2: return icdf8_2;
  }
  return NULL;
}

static const opus_uint16 *select_icdf16(uint32_t id) {
  switch (id) {
    case 0: return icdf16_0;
    case 1: return icdf16_1;
  }
  return NULL;
}

static void capture_trace(const ec_enc *enc, trace_t *trace) {
  trace->tell = (uint32_t)ec_tell((ec_ctx *)enc);
  trace->tell_frac = (uint32_t)ec_tell_frac((ec_ctx *)enc);
  trace->range_bytes = ec_range_bytes((ec_ctx *)enc);
  trace->rng = enc->rng;
  trace->val = enc->val;
  trace->rem = (uint32_t)(int32_t)enc->rem;
  trace->ext = enc->ext;
  trace->error = (uint32_t)(int32_t)enc->error;
}

static int run_op(ec_enc *enc, uint32_t op, uint32_t a, uint32_t b, uint32_t c, uint32_t d, int *shrunk) {
  const unsigned char *icdf8;
  const opus_uint16 *icdf16;
  (void)d;
  switch (op) {
    case OP_ENCODE:
      ec_encode(enc, a, b, c);
      return 1;
    case OP_ENCODE_BIN:
      ec_encode_bin(enc, a, b, c);
      return 1;
    case OP_ENC_BIT:
      ec_enc_bit_logp(enc, (int)a, b);
      return 1;
    case OP_ENC_ICDF8:
      icdf8 = select_icdf8(c);
      if (icdf8 == NULL) return 0;
      ec_enc_icdf(enc, (int)a, icdf8, b);
      return 1;
    case OP_ENC_ICDF16:
      icdf16 = select_icdf16(c);
      if (icdf16 == NULL) return 0;
      ec_enc_icdf16(enc, (int)a, icdf16, b);
      return 1;
    case OP_ENC_UINT:
      ec_enc_uint(enc, a, b);
      return 1;
    case OP_ENC_BITS:
      ec_enc_bits(enc, a, b);
      return 1;
    case OP_PATCH:
      ec_enc_patch_initial_bits(enc, a, b);
      return 1;
    case OP_SHRINK:
      ec_enc_shrink(enc, a);
      *shrunk = 1;
      return 1;
    case OP_DONE:
      ec_enc_done(enc);
      return 1;
  }
  return 0;
}

static uint32_t compact_packet(const ec_enc *enc, int shrunk, unsigned char *dst) {
  uint32_t len;
  uint32_t partial;
  if (shrunk || enc->error) {
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
  char magic[4];
  uint32_t version;
  uint32_t storage;
  uint32_t op_count;
  uint32_t i;
  unsigned char *buf;
  unsigned char *packet;
  trace_t *traces;
  ec_enc enc;
  int shrunk;
  uint32_t packet_len;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) return 1;
  if (!read_u32(&version) || version != 1 || !read_u32(&storage) || !read_u32(&op_count)) return 1;
  if (storage == 0 || op_count == 0) return 1;

  buf = (unsigned char *)calloc(storage, 1);
  packet = (unsigned char *)calloc(storage, 1);
  traces = (trace_t *)calloc(op_count, sizeof(*traces));
  if (buf == NULL || packet == NULL || traces == NULL) return 1;

  ec_enc_init(&enc, buf, storage);
  shrunk = 0;
  for (i = 0; i < op_count; i++) {
    uint32_t op;
    uint32_t a;
    uint32_t b;
    uint32_t c;
    uint32_t d;
    if (!read_u32(&op) || !read_u32(&a) || !read_u32(&b) || !read_u32(&c) || !read_u32(&d)) return 1;
    if (!run_op(&enc, op, a, b, c, d, &shrunk)) return 1;
    capture_trace(&enc, &traces[i]);
  }

  packet_len = compact_packet(&enc, shrunk, packet);
  if (!write_exact(OUTPUT_MAGIC, sizeof(magic)) || !write_u32(1) || !write_u32(op_count) || !write_u32(packet_len)) return 1;
  for (i = 0; i < op_count; i++) {
    if (!write_u32(traces[i].tell) || !write_u32(traces[i].tell_frac) ||
        !write_u32(traces[i].range_bytes) || !write_u32(traces[i].rng) ||
        !write_u32(traces[i].val) || !write_u32(traces[i].rem) ||
        !write_u32(traces[i].ext) || !write_u32(traces[i].error)) {
      return 1;
    }
  }
  if (!write_exact(packet, packet_len)) return 1;
  return 0;
}
