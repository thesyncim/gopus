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

#define INPUT_MAGIC "GSNI"
#define OUTPUT_MAGIC "GSNO"

#if defined(ENABLE_ASSERTIONS) || defined(ENABLE_HARDENING)
void celt_fatal(const char *str, const char *file, int line) {
  (void)str;
  (void)file;
  (void)line;
  abort();
}
#endif

enum {
  MODE_WARPED_AUTOCORRELATION_FLP = 0,
  MODE_APPLY_SINE_WINDOW_FLP = 1,
  MODE_PROCESS_GAINS_FLP = 2
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
  unsigned char b[4];
  if (!read_exact(b, sizeof(b))) return 0;
  *out = (uint32_t)b[0] |
         ((uint32_t)b[1] << 8) |
         ((uint32_t)b[2] << 16) |
         ((uint32_t)b[3] << 24);
  return 1;
}

static int write_u32(uint32_t value) {
  unsigned char b[4];
  b[0] = (unsigned char)(value & 0xffu);
  b[1] = (unsigned char)((value >> 8) & 0xffu);
  b[2] = (unsigned char)((value >> 16) & 0xffu);
  b[3] = (unsigned char)((value >> 24) & 0xffu);
  return write_exact(b, sizeof(b));
}

static int eval_warped_autocorrelation(void) {
  uint32_t raw;
  uint32_t length;
  uint32_t order;
  uint32_t i;
  silk_float warping;
  silk_float input[1024];
  silk_float corr[MAX_SHAPE_LPC_ORDER + 1] = {0};

  if (!read_u32(&length) || !read_u32(&order) || !read_u32(&raw)) return 0;
  /* order must be even, within [2, MAX_SHAPE_LPC_ORDER] */
  if (length == 0 || length > 1024) return 0;
  if ((order & 1) != 0 || order == 0 || order > MAX_SHAPE_LPC_ORDER) return 0;
  memcpy(&warping, &raw, sizeof(warping));
  for (i = 0; i < length; i++) {
    if (!read_u32(&raw)) return 0;
    memcpy(&input[i], &raw, sizeof(input[i]));
  }

  silk_warped_autocorrelation_FLP(corr, input, warping, (opus_int)length, (opus_int)order);

  if (!write_u32(order)) return 0;
  for (i = 0; i < MAX_SHAPE_LPC_ORDER + 1; i++) {
    uint32_t bits = 0;
    if (i <= order) {
      memcpy(&bits, &corr[i], sizeof(bits));
    }
    if (!write_u32(bits)) return 0;
  }
  return 1;
}

static int eval_apply_sine_window(void) {
  uint32_t raw;
  uint32_t length;
  uint32_t win_type;
  uint32_t i;
  silk_float px[2048];
  silk_float px_win[2048];

  if (!read_u32(&length) || !read_u32(&win_type)) return 0;
  if (length == 0 || length > 2048 || (length & 3) != 0) return 0;
  if (win_type != 1 && win_type != 2) return 0;
  for (i = 0; i < length; i++) {
    if (!read_u32(&raw)) return 0;
    memcpy(&px[i], &raw, sizeof(px[i]));
  }

  silk_apply_sine_window_FLP(px_win, px, (opus_int)win_type, (opus_int)length);

  if (!write_u32(length)) return 0;
  for (i = 0; i < length; i++) {
    memcpy(&raw, &px_win[i], sizeof(raw));
    if (!write_u32(raw)) return 0;
  }
  return 1;
}

static int read_float(silk_float *out) {
  uint32_t raw;
  if (!read_u32(&raw)) return 0;
  memcpy(out, &raw, sizeof(*out));
  return 1;
}

static int write_float(silk_float v) {
  uint32_t raw;
  memcpy(&raw, &v, sizeof(raw));
  return write_u32(raw);
}

static int eval_process_gains(void) {
  uint32_t signal_type;
  uint32_t nb_subfr;
  uint32_t subfr_length;
  uint32_t cond_coding;
  uint32_t snr_db_q7;
  uint32_t speech_activity_q8;
  uint32_t input_tilt_q15;
  uint32_t n_states_dd;
  uint32_t quant_offset_type;
  uint32_t last_gain_index;
  uint32_t i;
  silk_encoder_state_FLP psEnc;
  silk_encoder_control_FLP psEncCtrl;

  if (!read_u32(&signal_type) || !read_u32(&nb_subfr) || !read_u32(&subfr_length) ||
      !read_u32(&cond_coding) || !read_u32(&snr_db_q7) || !read_u32(&speech_activity_q8) ||
      !read_u32(&input_tilt_q15) || !read_u32(&n_states_dd) || !read_u32(&quant_offset_type) ||
      !read_u32(&last_gain_index)) {
    return 0;
  }
  if (nb_subfr == 0 || nb_subfr > MAX_NB_SUBFR || subfr_length == 0) return 0;

  memset(&psEnc, 0, sizeof(psEnc));
  memset(&psEncCtrl, 0, sizeof(psEncCtrl));

  psEnc.sCmn.indices.signalType = (opus_int8)signal_type;
  psEnc.sCmn.indices.quantOffsetType = (opus_int8)quant_offset_type;
  psEnc.sCmn.nb_subfr = (opus_int)nb_subfr;
  psEnc.sCmn.subfr_length = (opus_int)subfr_length;
  psEnc.sCmn.SNR_dB_Q7 = (opus_int)(int32_t)snr_db_q7;
  psEnc.sCmn.speech_activity_Q8 = (opus_int)(int32_t)speech_activity_q8;
  psEnc.sCmn.input_tilt_Q15 = (opus_int)(int32_t)input_tilt_q15;
  psEnc.sCmn.nStatesDelayedDecision = (opus_int)n_states_dd;
  psEnc.sShape.LastGainIndex = (opus_int8)last_gain_index;

  if (!read_float(&psEncCtrl.LTPredCodGain)) return 0;
  if (!read_float(&psEncCtrl.input_quality)) return 0;
  if (!read_float(&psEncCtrl.coding_quality)) return 0;
  for (i = 0; i < nb_subfr; i++) {
    if (!read_float(&psEncCtrl.Gains[i])) return 0;
  }
  for (i = 0; i < nb_subfr; i++) {
    if (!read_float(&psEncCtrl.ResNrg[i])) return 0;
  }

  silk_process_gains_FLP(&psEnc, &psEncCtrl, (opus_int)cond_coding);

  if (!write_u32(nb_subfr)) return 0;
  for (i = 0; i < nb_subfr; i++) {
    if (!write_float(psEncCtrl.Gains[i])) return 0;
  }
  for (i = 0; i < nb_subfr; i++) {
    if (!write_u32((uint32_t)psEncCtrl.GainsUnq_Q16[i])) return 0;
  }
  for (i = 0; i < nb_subfr; i++) {
    if (!write_u32((uint32_t)(int32_t)(opus_int8)psEnc.sCmn.indices.GainsIndices[i])) return 0;
  }
  if (!write_float(psEncCtrl.Lambda)) return 0;
  if (!write_u32((uint32_t)(int32_t)psEnc.sCmn.indices.quantOffsetType)) return 0;
  if (!write_u32((uint32_t)(int32_t)psEnc.sShape.LastGainIndex)) return 0;
  if (!write_u32((uint32_t)(int32_t)psEncCtrl.lastGainIndexPrev)) return 0;
  return 1;
}

static int eval_record(uint32_t mode) {
  switch (mode) {
    case MODE_WARPED_AUTOCORRELATION_FLP: return eval_warped_autocorrelation();
    case MODE_APPLY_SINE_WINDOW_FLP: return eval_apply_sine_window();
    case MODE_PROCESS_GAINS_FLP: return eval_process_gains();
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
  if (mode > MODE_PROCESS_GAINS_FLP) return 1;

  if (!write_exact(OUTPUT_MAGIC, sizeof(magic)) || !write_u32(1) || !write_u32(count)) return 1;
  for (i = 0; i < count; i++) {
    if (!eval_record(mode)) return 1;
  }
  return 0;
}
