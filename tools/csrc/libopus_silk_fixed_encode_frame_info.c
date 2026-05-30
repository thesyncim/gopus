/* Oracle for the libopus FIXED_POINT SILK per-frame analysis chain that
 * silk_encode_frame_FIX (silk/fixed/encode_frame_FIX.c) drives:
 *
 *   silk_VAD_GetSA_Q8 -> silk_find_pitch_lags_FIX ->
 *   silk_noise_shape_analysis_FIX -> silk_find_pred_coefs_FIX ->
 *   silk_process_gains_FIX -> silk_NSQ
 *
 * It does NOT run the entropy-coding / rate-control loop of
 * silk_encode_frame_FIX: those range-coder kernels are integer in the default
 * build and validated separately. This oracle isolates the assembled
 * FIXED_POINT analysis chain that determines the side-info indices, gains and
 * excitation pulses fed into the (shared) range encoder, so the Go driver
 * silkEncodeFrameFIX can be compared bit-exactly against it.
 *
 * Must be compiled and linked against a libopus configured with
 * --enable-fixed-point (defines FIXED_POINT). */

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
#include "stack_alloc.h"
#include "tuning_parameters.h"

#define INPUT_MAGIC "GEFI"
#define OUTPUT_MAGIC "GEFO"

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
  uint32_t v;
  if (!read_u32(&v)) return 0;
  *out = (int32_t)v;
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
  if (!write_u32(1)) return 1;
  if (!write_u32(count)) return 1;

  for (uint32_t c = 0; c < count; c++) {
    int32_t fsKHz, frameLength, subfrLength, nbSubfr, ltpMemLength, laPitch,
        laShape, pitchLPCWinLength, pitchEstimationLPCOrder, predictLPCOrder,
        shapingLPCOrder, shapeWinLength, complexity, nStatesDelayedDecision,
        warpingQ16, useCBR, nlsfMSVQSurvivors, pitchEstThresQ16, snrDBQ7,
        packetLossPerc, nFramesPerPacket, lbrrFlag, condCoding, opusVADActivity,
        frameCounter, prevSignalType, prevLag, firstFrameAfterReset;

    if (!read_i32(&fsKHz) || !read_i32(&frameLength) || !read_i32(&subfrLength) ||
        !read_i32(&nbSubfr) || !read_i32(&ltpMemLength) || !read_i32(&laPitch) ||
        !read_i32(&laShape) || !read_i32(&pitchLPCWinLength) ||
        !read_i32(&pitchEstimationLPCOrder) || !read_i32(&predictLPCOrder) ||
        !read_i32(&shapingLPCOrder) || !read_i32(&shapeWinLength) ||
        !read_i32(&complexity) || !read_i32(&nStatesDelayedDecision) ||
        !read_i32(&warpingQ16) || !read_i32(&useCBR) ||
        !read_i32(&nlsfMSVQSurvivors) || !read_i32(&pitchEstThresQ16) ||
        !read_i32(&snrDBQ7) || !read_i32(&packetLossPerc) ||
        !read_i32(&nFramesPerPacket) || !read_i32(&lbrrFlag) ||
        !read_i32(&condCoding) || !read_i32(&opusVADActivity) ||
        !read_i32(&frameCounter) || !read_i32(&prevSignalType) ||
        !read_i32(&prevLag) || !read_i32(&firstFrameAfterReset)) {
      return 1;
    }
    if (nbSubfr < 1 || nbSubfr > MAX_NB_SUBFR) return 1;

    silk_encoder_state_FIX *psEnc =
        (silk_encoder_state_FIX *)calloc(1, sizeof(silk_encoder_state_FIX));
    silk_encoder_control_FIX sEncCtrl;
    if (!psEnc) return 1;
    memset(&sEncCtrl, 0, sizeof(sEncCtrl));

    psEnc->sCmn.fs_kHz = fsKHz;
    psEnc->sCmn.frame_length = frameLength;
    psEnc->sCmn.subfr_length = subfrLength;
    psEnc->sCmn.nb_subfr = nbSubfr;
    psEnc->sCmn.ltp_mem_length = ltpMemLength;
    psEnc->sCmn.la_pitch = laPitch;
    psEnc->sCmn.la_shape = laShape;
    psEnc->sCmn.pitch_LPC_win_length = pitchLPCWinLength;
    psEnc->sCmn.pitchEstimationLPCOrder = pitchEstimationLPCOrder;
    psEnc->sCmn.predictLPCOrder = predictLPCOrder;
    psEnc->sCmn.shapingLPCOrder = shapingLPCOrder;
    psEnc->sCmn.shapeWinLength = shapeWinLength;
    psEnc->sCmn.pitchEstimationComplexity = complexity;
    psEnc->sCmn.nStatesDelayedDecision = nStatesDelayedDecision;
    psEnc->sCmn.warping_Q16 = warpingQ16;
    psEnc->sCmn.useCBR = useCBR;
    psEnc->sCmn.NLSF_MSVQ_Survivors = nlsfMSVQSurvivors;
    /* NLSF codebook: WB for order 16, NB/MB otherwise (matches control_codec). */
    if (predictLPCOrder == 16) {
      psEnc->sCmn.psNLSF_CB = &silk_NLSF_CB_WB;
    } else {
      psEnc->sCmn.psNLSF_CB = &silk_NLSF_CB_NB_MB;
    }
    psEnc->sCmn.pitchEstimationThreshold_Q16 = pitchEstThresQ16;
    psEnc->sCmn.SNR_dB_Q7 = snrDBQ7;
    psEnc->sCmn.PacketLoss_perc = packetLossPerc;
    psEnc->sCmn.nFramesPerPacket = nFramesPerPacket;
    psEnc->sCmn.LBRR_flag = (opus_int8)lbrrFlag;
    psEnc->sCmn.frameCounter = frameCounter;
    psEnc->sCmn.prevSignalType = (opus_int8)prevSignalType;
    psEnc->sCmn.prevLag = prevLag;
    psEnc->sCmn.first_frame_after_reset = firstFrameAfterReset;

    /* Mutable smoothing / VQ carry state. */
    int32_t sumLogGainQ7, harmShapeGainSmthQ16, tiltSmthQ16, lastGainIndex,
        ltpCorrQ15;
    if (!read_i32(&sumLogGainQ7) || !read_i32(&harmShapeGainSmthQ16) ||
        !read_i32(&tiltSmthQ16) || !read_i32(&lastGainIndex) ||
        !read_i32(&ltpCorrQ15)) {
      free(psEnc);
      return 1;
    }
    psEnc->sCmn.sum_log_gain_Q7 = sumLogGainQ7;
    psEnc->sShape.HarmShapeGain_smth_Q16 = harmShapeGainSmthQ16;
    psEnc->sShape.Tilt_smth_Q16 = tiltSmthQ16;
    psEnc->sShape.LastGainIndex = (opus_int8)lastGainIndex;
    psEnc->LTPCorr_Q15 = ltpCorrQ15;

    for (int i = 0; i < MAX_LPC_ORDER; i++) {
      int16_t v;
      if (!read_i16(&v)) { free(psEnc); return 1; }
      psEnc->sCmn.prev_NLSFq_Q15[i] = v;
    }

    /* VAD input: inputBuf+1 holds frame_length samples. */
    for (int i = 0; i < frameLength; i++) {
      int16_t v;
      if (!read_i16(&v)) { free(psEnc); return 1; }
      psEnc->sCmn.inputBuf[i + 1] = v;
    }

    /* x_buf: ltp_mem_length + la_shape + frame_length samples, with the new
     * frame already in place at x_frame + la_shape. */
    int32_t xBufLen = ltpMemLength + laShape + frameLength;
    for (int i = 0; i < xBufLen; i++) {
      int16_t v;
      if (!read_i16(&v)) { free(psEnc); return 1; }
      psEnc->x_buf[i] = v;
    }

    opus_int16 *x_frame = psEnc->x_buf + ltpMemLength;

    /* Initialize VAD state (matches silk_VAD_Init in control_codec). */
    silk_VAD_Init(&psEnc->sCmn.sVAD);
    /* NSQ initial state as set by control_codec first-frame reset. */
    psEnc->sCmn.sNSQ.prev_gain_Q16 = 65536;
    psEnc->sCmn.sNSQ.lagPrev = 100;
    psEnc->sCmn.first_frame_after_reset = firstFrameAfterReset;

    /****************************/
    /* Voice Activity Detection */
    /****************************/
    silk_VAD_GetSA_Q8(&psEnc->sCmn, psEnc->sCmn.inputBuf + 1, psEnc->sCmn.arch);
    {
      const opus_int activity_threshold = SILK_FIX_CONST(SPEECH_ACTIVITY_DTX_THRES, 8);
      if (opusVADActivity == VAD_NO_ACTIVITY &&
          psEnc->sCmn.speech_activity_Q8 >= activity_threshold) {
        psEnc->sCmn.speech_activity_Q8 = activity_threshold - 1;
      }
      if (psEnc->sCmn.speech_activity_Q8 < activity_threshold) {
        psEnc->sCmn.indices.signalType = TYPE_NO_VOICE_ACTIVITY;
        psEnc->sCmn.noSpeechCounter++;
      } else {
        psEnc->sCmn.noSpeechCounter = 0;
        psEnc->sCmn.indices.signalType = TYPE_UNVOICED;
      }
    }
    int vadFlag =
        (psEnc->sCmn.indices.signalType != TYPE_NO_VOICE_ACTIVITY) ? 1 : 0;

    /* indices.Seed = frameCounter++ & 3 (matches silk_encode_frame_FIX top). */
    psEnc->sCmn.indices.Seed = (opus_int8)(psEnc->sCmn.frameCounter++ & 3);

    VARDECL(opus_int16, res_pitch);
    ALLOC(res_pitch, laPitch + frameLength + ltpMemLength, opus_int16);
    opus_int16 *res_pitch_frame = res_pitch + ltpMemLength;

    /* Find pitch lags + initial LPC analysis. */
    silk_find_pitch_lags_FIX(psEnc, &sEncCtrl, res_pitch,
                             x_frame - ltpMemLength, psEnc->sCmn.arch);

    /* Noise shape analysis. */
    silk_noise_shape_analysis_FIX(psEnc, &sEncCtrl, res_pitch_frame, x_frame,
                                  psEnc->sCmn.arch);

    /* Find prediction coefficients (LPC + LTP). */
    silk_find_pred_coefs_FIX(psEnc, &sEncCtrl, res_pitch_frame, x_frame,
                             condCoding);

    /* Process gains (also computes Lambda_Q10). */
    silk_process_gains_FIX(psEnc, &sEncCtrl, condCoding);

    /* Noise shaping quantization (silk_NSQ, non-del-dec path). */
    silk_NSQ(&psEnc->sCmn, &psEnc->sCmn.sNSQ, &psEnc->sCmn.indices, x_frame,
             psEnc->sCmn.pulses, sEncCtrl.PredCoef_Q12[0], sEncCtrl.LTPCoef_Q14,
             sEncCtrl.AR_Q13, sEncCtrl.HarmShapeGain_Q14, sEncCtrl.Tilt_Q14,
             sEncCtrl.LF_shp_Q14, sEncCtrl.Gains_Q16, sEncCtrl.pitchL,
             sEncCtrl.Lambda_Q10, sEncCtrl.LTP_scale_Q14, psEnc->sCmn.arch);

    /************/
    /* Outputs  */
    /************/
    if (!write_i32(vadFlag)) return 1;
    if (!write_i32(psEnc->sCmn.speech_activity_Q8)) return 1;
    if (!write_i32(psEnc->sCmn.input_tilt_Q15)) return 1;
    if (!write_i32(psEnc->sCmn.indices.signalType)) return 1;
    if (!write_i32(psEnc->sCmn.indices.quantOffsetType)) return 1;
    if (!write_i32(psEnc->sCmn.indices.Seed)) return 1;
    if (!write_i32(psEnc->sCmn.indices.NLSFInterpCoef_Q2)) return 1;
    if (!write_i32(psEnc->sCmn.indices.PERIndex)) return 1;
    if (!write_i32(psEnc->sCmn.indices.LTP_scaleIndex)) return 1;
    if (!write_i32(psEnc->sCmn.indices.lagIndex)) return 1;
    if (!write_i32(psEnc->sCmn.indices.contourIndex)) return 1;
    if (!write_i32(sEncCtrl.LTPredCodGain_Q7)) return 1;
    if (!write_i32(sEncCtrl.Lambda_Q10)) return 1;
    if (!write_i32(sEncCtrl.LTP_scale_Q14)) return 1;
    if (!write_i32(psEnc->LTPCorr_Q15)) return 1;
    if (!write_i32(psEnc->sShape.LastGainIndex)) return 1;

    for (int i = 0; i < predictLPCOrder + 1; i++) {
      if (!write_i32(psEnc->sCmn.indices.NLSFIndices[i])) return 1;
    }
    for (int k = 0; k < nbSubfr; k++) {
      if (!write_i32(psEnc->sCmn.indices.GainsIndices[k])) return 1;
    }
    for (int k = 0; k < nbSubfr; k++) {
      if (!write_i32(psEnc->sCmn.indices.LTPIndex[k])) return 1;
    }
    for (int k = 0; k < nbSubfr; k++) {
      if (!write_i32(sEncCtrl.Gains_Q16[k])) return 1;
    }
    for (int k = 0; k < nbSubfr; k++) {
      if (!write_i32(sEncCtrl.pitchL[k])) return 1;
    }
    /* PredCoef_Q12[0] and [1]. */
    for (int b = 0; b < 2; b++) {
      for (int i = 0; i < predictLPCOrder; i++) {
        if (!write_i32(sEncCtrl.PredCoef_Q12[b][i])) return 1;
      }
    }
    for (int i = 0; i < nbSubfr * LTP_ORDER; i++) {
      if (!write_i32(sEncCtrl.LTPCoef_Q14[i])) return 1;
    }
    /* Excitation pulses. */
    for (int i = 0; i < frameLength; i++) {
      if (!write_i32(psEnc->sCmn.pulses[i])) return 1;
    }

    free(psEnc);
  }

  return 0;
}
