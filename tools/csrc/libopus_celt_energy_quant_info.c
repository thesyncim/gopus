#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "celt.h"
#include "quant_bands.h"

#define INPUT_MAGIC "GCEI"
#define OUTPUT_MAGIC "GCEO"
#define HELPER_MAX_BANDS 21
#define HELPER_MAX_CHANNELS 2

enum {
  OP_QUANT_FINE = 0,
  OP_FINALISE = 1
};

#if defined(ENABLE_ASSERTIONS) || defined(ENABLE_HARDENING)
void celt_fatal(const char *str, const char *file, int line) {
  (void)str;
  (void)file;
  (void)line;
  abort();
}
#endif

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

static int read_f32(float *out) {
  uint32_t raw;
  if (!read_u32(&raw)) return 0;
  memcpy(out, &raw, sizeof(*out));
  return 1;
}

static int write_u32(uint32_t value) {
  return write_exact(&value, sizeof(value));
}

static int write_f32(float value) {
  uint32_t raw;
  memcpy(&raw, &value, sizeof(raw));
  return write_u32(raw);
}

static int read_magic(const char *want) {
  char got[4];
  return read_exact(got, sizeof(got)) && memcmp(got, want, sizeof(got)) == 0;
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

static int eval_record(const CELTMode *mode) {
  int32_t raw;
  int op;
  int start;
  int end;
  int channels;
  int storage;
  int bits_left;
  int i;
  int total;
  celt_glog old_bands[HELPER_MAX_CHANNELS * HELPER_MAX_BANDS];
  celt_glog error[HELPER_MAX_CHANNELS * HELPER_MAX_BANDS];
  int prev_quant[HELPER_MAX_BANDS];
  int fine_quant[HELPER_MAX_BANDS];
  int extra_quant[HELPER_MAX_BANDS];
  int fine_priority[HELPER_MAX_BANDS];
  unsigned char *buf;
  unsigned char *packet;
  uint32_t packet_len;
  ec_enc enc;

  if (!read_i32(&raw)) return 0;
  op = (int)raw;
  if (!read_i32(&raw)) return 0;
  start = (int)raw;
  if (!read_i32(&raw)) return 0;
  end = (int)raw;
  if (!read_i32(&raw)) return 0;
  channels = (int)raw;
  if (!read_i32(&raw)) return 0;
  storage = (int)raw;
  if (!read_i32(&raw)) return 0;
  bits_left = (int)raw;

  if ((op != OP_QUANT_FINE && op != OP_FINALISE) ||
      start < 0 || end < start || end > HELPER_MAX_BANDS ||
      (channels != 1 && channels != 2) ||
      storage <= 0 || storage > 256) {
    return 0;
  }

  total = channels * HELPER_MAX_BANDS;
  for (i = 0; i < total; i++) {
    float v;
    if (!read_f32(&v)) return 0;
    old_bands[i] = (celt_glog)v;
  }
  for (i = 0; i < total; i++) {
    float v;
    if (!read_f32(&v)) return 0;
    error[i] = (celt_glog)v;
  }
  for (i = 0; i < HELPER_MAX_BANDS; i++) {
    if (!read_i32(&raw)) return 0;
    prev_quant[i] = (int)raw;
  }
  for (i = 0; i < HELPER_MAX_BANDS; i++) {
    if (!read_i32(&raw)) return 0;
    fine_quant[i] = (int)raw;
  }
  for (i = 0; i < HELPER_MAX_BANDS; i++) {
    if (!read_i32(&raw)) return 0;
    extra_quant[i] = (int)raw;
  }
  for (i = 0; i < HELPER_MAX_BANDS; i++) {
    if (!read_i32(&raw)) return 0;
    fine_priority[i] = (int)raw;
  }

  buf = (unsigned char *)calloc((size_t)storage, 1);
  packet = (unsigned char *)calloc((size_t)storage, 1);
  if (buf == NULL || packet == NULL) {
    free(buf);
    free(packet);
    return 0;
  }
  ec_enc_init(&enc, buf, (opus_uint32)storage);
  if (op == OP_QUANT_FINE) {
    quant_fine_energy(mode, start, end, old_bands, error, prev_quant, extra_quant, &enc, channels);
  } else {
    quant_energy_finalise(mode, start, end, old_bands, error, fine_quant, fine_priority, bits_left, &enc, channels);
  }
  ec_enc_done(&enc);
  packet_len = compact_packet(&enc, packet);

  if (!write_u32((uint32_t)(int32_t)enc.error)) return 0;
  if (!write_u32(packet_len)) return 0;
  for (i = 0; i < total; i++) {
    if (!write_f32((float)old_bands[i])) return 0;
  }
  for (i = 0; i < total; i++) {
    if (!write_f32((float)error[i])) return 0;
  }
  if (!write_exact(packet, packet_len)) return 0;

  free(buf);
  free(packet);
  return 1;
}

int main(void) {
  uint32_t version;
  uint32_t count;
  uint32_t i;
  int err = 0;
  const CELTMode *mode;

  if (!set_binary_stdio()) return 1;
  if (!read_magic(INPUT_MAGIC)) return 1;
  if (!read_u32(&version) || version != 1) return 1;
  if (!read_u32(&count)) return 1;

  mode = opus_custom_mode_create(48000, 960, &err);
  if (mode == NULL || mode->nbEBands != HELPER_MAX_BANDS) return 1;

  if (!write_exact(OUTPUT_MAGIC, 4)) return 1;
  if (!write_u32(1)) return 1;
  if (!write_u32(count)) return 1;
  for (i = 0; i < count; i++) {
    if (!eval_record(mode)) return 1;
  }
  return 0;
}
