/* Stateful, selected-C oracle for dnn/osce_features.c:osce_calculate_features.
 * The helper links the scalar OSCE reference used by the public OSCE gates.
 * All integers and float bits on the wire are little-endian.
 */
#include "config.h"
#include "osce_features.h"
#include "osce_config.h"

#include <stdint.h>
#include <stdio.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#ifndef ENABLE_OSCE
#error "OSCE feature oracle requires ENABLE_OSCE"
#endif
#ifdef FIXED_POINT
#error "OSCE feature oracle requires the float libopus build"
#endif

#define MAX_ORACLE_FRAMES 32

typedef struct {
  int32_t lpc_order;
  int32_t signal_type;
  int32_t num_bits;
  int16_t pred[2][16];
  int16_t ltp[20];
  int32_t gains[4];
  int32_t pitch[4];
  int16_t xq[320];
} oracle_frame;

static int read_u16(uint16_t *value) {
  int a = fgetc(stdin);
  int b = fgetc(stdin);
  if (a == EOF || b == EOF) return 0;
  *value = (uint16_t)((uint16_t)a | ((uint16_t)b << 8));
  return 1;
}

static int read_u32(uint32_t *value) {
  uint16_t a, b;
  if (!read_u16(&a) || !read_u16(&b)) return 0;
  *value = (uint32_t)a | ((uint32_t)b << 16);
  return 1;
}

static int write_u32(uint32_t value) {
  return fputc((int)(value & 255), stdout) != EOF &&
         fputc((int)((value >> 8) & 255), stdout) != EOF &&
         fputc((int)((value >> 16) & 255), stdout) != EOF &&
         fputc((int)((value >> 24) & 255), stdout) != EOF;
}

static int write_float_bits(float value) {
  uint32_t bits;
  memcpy(&bits, &value, sizeof(bits));
  return write_u32(bits);
}

int main(void) {
  static oracle_frame frames[MAX_ORACLE_FRAMES];
  static silk_decoder_state decoder;
  silk_decoder_control control;
  float features[4 * OSCE_FEATURE_DIM];
  float numbits[2];
  int periods[4];
  uint32_t version, count, word;
  char magic[4];
  unsigned f, i, j;

#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 2;
#endif
  if (fread(magic, 1, sizeof(magic), stdin) != sizeof(magic) ||
      memcmp(magic, "GLFI", sizeof(magic)) != 0 ||
      !read_u32(&version) || version != 1 ||
      !read_u32(&count) || count == 0 || count > MAX_ORACLE_FRAMES) return 3;

  for (f = 0; f < count; f++) {
    oracle_frame *frame = &frames[f];
    if (!read_u32(&word)) return 4;
    frame->lpc_order = (int32_t)word;
    if (!read_u32(&word)) return 4;
    frame->signal_type = (int32_t)word;
    if (!read_u32(&word)) return 4;
    frame->num_bits = (int32_t)word;
    if ((frame->lpc_order != 10 && frame->lpc_order != 16) ||
        frame->signal_type < 0 || frame->signal_type > 2 ||
        frame->num_bits < 0) return 4;
    for (i = 0; i < 2; i++) {
      for (j = 0; j < 16; j++) {
        uint16_t value;
        if (!read_u16(&value)) return 4;
        frame->pred[i][j] = (int16_t)value;
      }
    }
    for (i = 0; i < 20; i++) {
      uint16_t value;
      if (!read_u16(&value)) return 4;
      frame->ltp[i] = (int16_t)value;
    }
    for (i = 0; i < 4; i++) {
      if (!read_u32(&word)) return 4;
      frame->gains[i] = (int32_t)word;
      if (frame->gains[i] <= 0) return 4;
    }
    for (i = 0; i < 4; i++) {
      if (!read_u32(&word)) return 4;
      frame->pitch[i] = (int32_t)word;
      if (frame->pitch[i] < 0 || frame->pitch[i] > 288 ||
          (frame->signal_type == 2 && frame->pitch[i] == 0)) return 4;
    }
    for (i = 0; i < 320; i++) {
      uint16_t value;
      if (!read_u16(&value)) return 4;
      frame->xq[i] = (int16_t)value;
    }
  }
  if (fgetc(stdin) != EOF || ferror(stdin)) return 4;

  decoder.nb_subfr = 4;
  if (fwrite("GLFO", 1, 4, stdout) != 4 ||
      !write_u32(1) || !write_u32(count)) return 5;
  for (f = 0; f < count; f++) {
    oracle_frame *frame = &frames[f];
    memset(&control, 0, sizeof(control));
    decoder.LPC_order = frame->lpc_order;
    decoder.indices.signalType = (opus_int8)frame->signal_type;
    for (i = 0; i < 2; i++) {
      for (j = 0; j < 16; j++) control.PredCoef_Q12[i][j] = frame->pred[i][j];
    }
    for (i = 0; i < 20; i++) control.LTPCoef_Q14[i] = frame->ltp[i];
    for (i = 0; i < 4; i++) {
      control.Gains_Q16[i] = frame->gains[i];
      control.pitchL[i] = frame->pitch[i];
    }
    osce_calculate_features(&decoder, &control, features, numbits,
                            periods, frame->xq, frame->num_bits);
    for (i = 0; i < 4 * OSCE_FEATURE_DIM; i++) {
      if (!write_float_bits(features[i])) return 5;
    }
    for (i = 0; i < 2; i++) {
      if (!write_float_bits(numbits[i])) return 5;
    }
    for (i = 0; i < 4; i++) {
      if (!write_u32((uint32_t)periods[i])) return 5;
    }
  }
  return fflush(stdout) == 0 ? 0 : 5;
}
