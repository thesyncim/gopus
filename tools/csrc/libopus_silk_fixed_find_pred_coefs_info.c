/* Oracle for the libopus FIXED_POINT silk_find_pred_coefs_FIX driver.
 *
 * Must be compiled and linked against a libopus configured with
 * --enable-fixed-point (defines FIXED_POINT). It constructs a minimal
 * silk_encoder_state_FIX / silk_encoder_control_FIX, runs the real
 * silk_find_pred_coefs_FIX, and writes the bit-exact prediction-coefficient
 * search outputs to stdout. */

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"

#ifndef FIXED_POINT
#error "this oracle requires a FIXED_POINT libopus build (--enable-fixed-point)"
#endif

#include "main_FIX.h"
#include "structs.h"
#include "tables.h"

#define INPUT_MAGIC "GFPI"
#define OUTPUT_MAGIC "GFPO"

#define MAX_X 8192
#define MAX_RES 8192

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
  unsigned char b[4];
  if (!read_exact(b, sizeof(b))) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) | ((uint32_t)b[2] << 16) |
         ((uint32_t)b[3] << 24);
  return 1;
}

static int read_i32(int32_t *out) {
  uint32_t u;
  if (!read_u32(&u)) return 0;
  *out = (int32_t)u;
  return 1;
}

static int read_i16(int16_t *out) {
  unsigned char b[2];
  if (!read_exact(b, sizeof(b))) return 0;
  *out = (int16_t)((uint16_t)b[0] | ((uint16_t)b[1] << 8));
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

static int write_i32(int32_t value) { return write_u32((uint32_t)value); }

int main(void) {
  if (!set_binary_stdio()) return 1;

  char magic[4];
  if (!read_exact(magic, sizeof(magic)) ||
      memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) {
    return 1;
  }
  uint32_t version;
  if (!read_u32(&version) || version != 1) return 1;
  uint32_t count;
  if (!read_u32(&count)) return 1;

  if (!write_exact(OUTPUT_MAGIC, 4)) return 1;
  if (!write_u32(1)) return 1; /* version */
  if (!write_u32(count)) return 1;

  for (uint32_t c = 0; c < count; c++) {
    int32_t predictLPCOrder, subfr_length, nb_subfr, frame_length;
    int32_t signalType, useInterp, firstFrame, speechActivityQ8;
    int32_t survivors, condCoding;
    int32_t packetLoss, nFramesPerPacket, lbrrFlag, snrDBQ7, codingQualityQ14;
    int32_t sumLogGainQ7;
    int16_t prevNLSF[MAX_LPC_ORDER];
    int32_t gains[MAX_NB_SUBFR];
    int32_t pitchL[MAX_NB_SUBFR];
    uint32_t res_len, res_start, x_len, x_start;

    if (!read_i32(&predictLPCOrder) || !read_i32(&subfr_length) ||
        !read_i32(&nb_subfr) || !read_i32(&frame_length) ||
        !read_i32(&signalType) || !read_i32(&useInterp) ||
        !read_i32(&firstFrame) || !read_i32(&speechActivityQ8) ||
        !read_i32(&survivors) || !read_i32(&condCoding) ||
        !read_i32(&packetLoss) || !read_i32(&nFramesPerPacket) ||
        !read_i32(&lbrrFlag) || !read_i32(&snrDBQ7) ||
        !read_i32(&codingQualityQ14) || !read_i32(&sumLogGainQ7)) {
      return 1;
    }
    for (int i = 0; i < MAX_LPC_ORDER; i++) {
      if (!read_i16(&prevNLSF[i])) return 1;
    }
    for (int i = 0; i < MAX_NB_SUBFR; i++) {
      if (!read_i32(&gains[i])) return 1;
    }
    for (int i = 0; i < MAX_NB_SUBFR; i++) {
      if (!read_i32(&pitchL[i])) return 1;
    }

    if (!read_u32(&res_len) || !read_u32(&res_start)) return 1;
    if (res_len > MAX_RES) return 1;
    static int16_t resbuf[MAX_RES];
    for (uint32_t i = 0; i < res_len; i++) {
      if (!read_i16(&resbuf[i])) return 1;
    }

    if (!read_u32(&x_len) || !read_u32(&x_start)) return 1;
    if (x_len > MAX_X) return 1;
    static int16_t xbuf[MAX_X];
    for (uint32_t i = 0; i < x_len; i++) {
      if (!read_i16(&xbuf[i])) return 1;
    }

    silk_encoder_state_FIX psEnc;
    silk_encoder_control_FIX psEncCtrl;
    memset(&psEnc, 0, sizeof(psEnc));
    memset(&psEncCtrl, 0, sizeof(psEncCtrl));

    psEnc.sCmn.predictLPCOrder = predictLPCOrder;
    psEnc.sCmn.subfr_length = subfr_length;
    psEnc.sCmn.nb_subfr = nb_subfr;
    psEnc.sCmn.frame_length = frame_length;
    psEnc.sCmn.ltp_mem_length = LTP_MEM_LENGTH_MS * (subfr_length / 5);
    psEnc.sCmn.indices.signalType = (opus_int8)signalType;
    psEnc.sCmn.useInterpolatedNLSFs = useInterp;
    psEnc.sCmn.first_frame_after_reset = firstFrame;
    psEnc.sCmn.speech_activity_Q8 = speechActivityQ8;
    psEnc.sCmn.NLSF_MSVQ_Survivors = survivors;
    psEnc.sCmn.PacketLoss_perc = packetLoss;
    psEnc.sCmn.nFramesPerPacket = nFramesPerPacket;
    psEnc.sCmn.LBRR_flag = (opus_int8)lbrrFlag;
    psEnc.sCmn.SNR_dB_Q7 = snrDBQ7;
    psEnc.sCmn.sum_log_gain_Q7 = sumLogGainQ7;
    psEnc.sCmn.arch = 0;
    if (predictLPCOrder == 16) {
      psEnc.sCmn.psNLSF_CB = &silk_NLSF_CB_WB;
    } else {
      psEnc.sCmn.psNLSF_CB = &silk_NLSF_CB_NB_MB;
    }
    for (int i = 0; i < MAX_LPC_ORDER; i++) {
      psEnc.sCmn.prev_NLSFq_Q15[i] = prevNLSF[i];
    }

    psEncCtrl.coding_quality_Q14 = codingQualityQ14;
    for (int i = 0; i < MAX_NB_SUBFR; i++) {
      psEncCtrl.Gains_Q16[i] = gains[i];
      psEncCtrl.pitchL[i] = pitchL[i];
    }

    silk_find_pred_coefs_FIX(&psEnc, &psEncCtrl, &resbuf[res_start],
                             &xbuf[x_start], condCoding);

    /* Outputs. */
    for (int h = 0; h < 2; h++) {
      for (int i = 0; i < MAX_LPC_ORDER; i++) {
        if (!write_i32((int32_t)psEncCtrl.PredCoef_Q12[h][i])) return 1;
      }
    }
    for (int i = 0; i < LTP_ORDER * MAX_NB_SUBFR; i++) {
      if (!write_i32((int32_t)psEncCtrl.LTPCoef_Q14[i])) return 1;
    }
    if (!write_i32((int32_t)psEncCtrl.LTP_scale_Q14)) return 1;
    if (!write_i32((int32_t)psEnc.sCmn.indices.NLSFInterpCoef_Q2)) return 1;
    if (!write_i32((int32_t)psEnc.sCmn.indices.PERIndex)) return 1;
    for (int i = 0; i < MAX_NB_SUBFR; i++) {
      if (!write_i32((int32_t)psEnc.sCmn.indices.LTPIndex[i])) return 1;
    }
    for (int i = 0; i < MAX_LPC_ORDER + 1; i++) {
      if (!write_i32((int32_t)psEnc.sCmn.indices.NLSFIndices[i])) return 1;
    }
    for (int i = 0; i < MAX_NB_SUBFR; i++) {
      if (!write_i32((int32_t)psEncCtrl.ResNrg[i])) return 1;
    }
    for (int i = 0; i < MAX_NB_SUBFR; i++) {
      if (!write_i32((int32_t)psEncCtrl.ResNrgQ[i])) return 1;
    }
    if (!write_i32((int32_t)psEncCtrl.LTPredCodGain_Q7)) return 1;
    if (!write_i32((int32_t)psEnc.sCmn.sum_log_gain_Q7)) return 1;
    for (int i = 0; i < MAX_LPC_ORDER; i++) {
      if (!write_i32((int32_t)psEnc.sCmn.prev_NLSFq_Q15[i])) return 1;
    }
  }

  return 0;
}
