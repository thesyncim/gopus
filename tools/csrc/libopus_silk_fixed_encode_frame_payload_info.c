/* Oracle for the FULL libopus FIXED_POINT SILK per-frame encoder
 * silk_encode_frame_FIX (silk/fixed/encode_frame_FIX.c), including the
 * gain/Lambda rate-control loop and the side-info / excitation entropy coding
 * (silk_encode_indices / silk_encode_pulses) into a real ec_enc.
 *
 * It sets up a silk_encoder_state_FIX with the flattened inputs, initializes a
 * fresh ec_enc on a 1275-byte buffer (no packet header / LBRR-flag overhead),
 * runs silk_encode_frame_FIX, and emits the resulting payload bytes plus
 * nBytesOut and the final range so the Go port can be compared byte-exactly.
 *
 * Must be linked against a libopus configured with --enable-fixed-point. */

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
#include "entenc.h"

#define INPUT_MAGIC "GEPI"
#define OUTPUT_MAGIC "GEPO"

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
    int32_t maxBits, ecPrevLagIndex, ecPrevSignalType, lbrrEnabled,
        lbrrGainIncreases, nFramesEncoded, lbrrPrevFrameHadLBRR;

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
        !read_i32(&prevLag) || !read_i32(&firstFrameAfterReset) ||
        !read_i32(&maxBits) || !read_i32(&ecPrevLagIndex) ||
        !read_i32(&ecPrevSignalType) || !read_i32(&lbrrEnabled) ||
        !read_i32(&lbrrGainIncreases) || !read_i32(&nFramesEncoded) ||
        !read_i32(&lbrrPrevFrameHadLBRR)) {
      return 1;
    }
    if (nbSubfr < 1 || nbSubfr > MAX_NB_SUBFR) return 1;

    silk_encoder_state_FIX *psEnc =
        (silk_encoder_state_FIX *)calloc(1, sizeof(silk_encoder_state_FIX));
    if (!psEnc) return 1;

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
    if (predictLPCOrder == 16) {
      psEnc->sCmn.psNLSF_CB = &silk_NLSF_CB_WB;
    } else {
      psEnc->sCmn.psNLSF_CB = &silk_NLSF_CB_NB_MB;
    }
    /* pitch contour / lag-low-bits tables (set by control_codec). */
    if (fsKHz == 8) {
      psEnc->sCmn.pitch_lag_low_bits_iCDF = silk_uniform4_iCDF;
      if (nbSubfr == 4) {
        psEnc->sCmn.pitch_contour_iCDF = silk_pitch_contour_NB_iCDF;
      } else {
        psEnc->sCmn.pitch_contour_iCDF = silk_pitch_contour_10_ms_NB_iCDF;
      }
    } else {
      if (fsKHz == 12) {
        psEnc->sCmn.pitch_lag_low_bits_iCDF = silk_uniform6_iCDF;
      } else {
        psEnc->sCmn.pitch_lag_low_bits_iCDF = silk_uniform8_iCDF;
      }
      if (nbSubfr == 4) {
        psEnc->sCmn.pitch_contour_iCDF = silk_pitch_contour_iCDF;
      } else {
        psEnc->sCmn.pitch_contour_iCDF = silk_pitch_contour_10_ms_iCDF;
      }
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
    psEnc->sCmn.ec_prevLagIndex = (opus_int16)ecPrevLagIndex;
    psEnc->sCmn.ec_prevSignalType = ecPrevSignalType;
    psEnc->sCmn.LBRR_enabled = lbrrEnabled;
    psEnc->sCmn.LBRR_GainIncreases = lbrrGainIncreases;
    psEnc->sCmn.nFramesEncoded = nFramesEncoded;
    if (lbrrPrevFrameHadLBRR && nFramesEncoded > 0) {
      psEnc->sCmn.LBRR_flags[nFramesEncoded - 1] = 1;
    }

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

    for (int i = 0; i < frameLength; i++) {
      int16_t v;
      if (!read_i16(&v)) { free(psEnc); return 1; }
      psEnc->sCmn.inputBuf[i + 1] = v;
    }

    int32_t xBufLen = ltpMemLength + laShape + frameLength;
    for (int i = 0; i < xBufLen; i++) {
      int16_t v;
      if (!read_i16(&v)) { free(psEnc); return 1; }
      psEnc->x_buf[i] = v;
    }

    silk_VAD_Init(&psEnc->sCmn.sVAD);
    psEnc->sCmn.sNSQ.prev_gain_Q16 = 65536;
    psEnc->sCmn.sNSQ.lagPrev = 100;
    psEnc->sCmn.first_frame_after_reset = firstFrameAfterReset;
    psEnc->sCmn.LBRRprevLastGainIndex = psEnc->sShape.LastGainIndex;

    /* Run the VAD (silk_encode_do_VAD_FIX) before the frame encode, matching
     * enc_API which calls it prior to silk_encode_frame_Fxx. */
    silk_encode_do_VAD_FIX(psEnc, opusVADActivity);

    /* Fresh range encoder on a max-size buffer (isolated frame payload). */
    unsigned char ec_buf[1275];
    ec_enc encState;
    ec_enc_init(&encState, ec_buf, sizeof(ec_buf));

    opus_int32 nBytesOut = 0;
    silk_encode_frame_FIX(psEnc, &nBytesOut, &encState, condCoding, maxBits,
                          useCBR);

    opus_uint32 rng = encState.rng;
    /* Finalize the range coder so the first nBytesOut bytes are the payload,
     * matching the Go Done() finalization. */
    ec_enc_done(&encState);

    /* Output: nBytesOut, final range, vadFlag, signalType, LBRR_flag,
     * payload bytes (nBytesOut, capped to 1275). */
    if (!write_i32(nBytesOut)) return 1;
    if (!write_u32(rng)) return 1;
    if (!write_i32(psEnc->sCmn.indices.signalType != TYPE_NO_VOICE_ACTIVITY ? 1
                                                                            : 0))
      return 1;
    if (!write_i32(psEnc->sCmn.indices.signalType)) return 1;
    if (!write_i32(psEnc->sCmn.LBRR_flags[nFramesEncoded])) return 1;
    if (!write_i32(psEnc->sCmn.ec_prevLagIndex)) return 1;
    if (!write_i32(psEnc->sCmn.ec_prevSignalType)) return 1;

    int32_t outBytes = nBytesOut;
    if (outBytes < 0) outBytes = 0;
    if (outBytes > 1275) outBytes = 1275;
    if (!write_i32(outBytes)) return 1;
    if (!write_exact(ec_buf, (size_t)outBytes)) return 1;

    /* LBRR side-info indices and pulses (valid when LBRR_flag set). */
    SideInfoIndices *lb = &psEnc->sCmn.indices_LBRR[nFramesEncoded];
    for (int i = 0; i < nbSubfr; i++) {
      if (!write_i32(lb->GainsIndices[i])) return 1;
    }
    if (!write_i32(lb->signalType)) return 1;
    if (!write_i32(lb->quantOffsetType)) return 1;
    for (int i = 0; i < frameLength; i++) {
      if (!write_i32(psEnc->sCmn.pulses_LBRR[nFramesEncoded][i])) return 1;
    }

    free(psEnc);
  }

  return 0;
}
