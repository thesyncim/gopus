#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "silk/main.h"
#include "silk/API.h"
#include "silk/tuning_parameters.h"
#include "silk/float/main_FLP.h"

#define INPUT_MAGIC "GSSI"
#define OUTPUT_MAGIC "GSSO"
#define MAX_STEREO_SAMPLES 320

enum {
  MODE_STEREO_QUANT_PRED = 0,
  MODE_STEREO_FIND_PREDICTOR = 1,
  MODE_STEREO_LR_TO_MS = 2,
  MODE_STEREO_STATE_SIZES = 3,
  MODE_STEREO_PACKET0_WRAPPER = 4
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

static int read_i32(int32_t *out) {
  uint32_t raw;
  if (!read_u32(&raw)) return 0;
  *out = (int32_t)raw;
  return 1;
}

static int read_f32(float *out) {
  uint32_t raw;
  union {
    uint32_t u;
    float f;
  } u;
  if (!read_u32(&raw)) return 0;
  u.u = raw;
  *out = u.f;
  return 1;
}

static int write_i32(int32_t value) {
  return write_exact(&value, sizeof(value));
}

static int write_stereo_record(int32_t first, int32_t second, const int32_t extra[6]) {
  int i;
  if (!write_i32(first) || !write_i32(second)) return 0;
  for (i = 0; i < 6; i++) {
    if (!write_i32(extra[i])) return 0;
  }
  return 1;
}

static int write_lr_to_ms_record(
    const stereo_enc_state *state,
    const opus_int8 ix[2][3],
    opus_int8 mid_only,
    const opus_int32 rates[2],
    const opus_int16 mid[MAX_STEREO_SAMPLES + 2],
    const opus_int16 side[MAX_STEREO_SAMPLES + 2],
    int frame_length
) {
  int i;
  int n;
  if (!write_i32((int32_t)mid_only)) return 0;
  if (!write_i32((int32_t)rates[0]) || !write_i32((int32_t)rates[1])) return 0;
  for (n = 0; n < 2; n++) {
    for (i = 0; i < 3; i++) {
      if (!write_i32((int32_t)ix[n][i])) return 0;
    }
  }
  if (!write_i32((int32_t)state->pred_prev_Q13[0]) || !write_i32((int32_t)state->pred_prev_Q13[1])) return 0;
  if (!write_i32((int32_t)state->sMid[0]) || !write_i32((int32_t)state->sMid[1])) return 0;
  if (!write_i32((int32_t)state->sSide[0]) || !write_i32((int32_t)state->sSide[1])) return 0;
  for (i = 0; i < 4; i++) {
    if (!write_i32((int32_t)state->mid_side_amp_Q0[i])) return 0;
  }
  if (!write_i32((int32_t)state->smth_width_Q14) ||
      !write_i32((int32_t)state->width_prev_Q14) ||
      !write_i32((int32_t)state->silent_side_len)) {
    return 0;
  }
  for (i = 1; i <= frame_length; i++) {
    if (!write_i32((int32_t)mid[i])) return 0;
  }
  for (i = 1; i <= frame_length; i++) {
    if (!write_i32((int32_t)side[i])) return 0;
  }
  return 1;
}

static int eval_quant_pred(void) {
  int32_t raw;
  opus_int32 pred_Q13[2];
  opus_int8 ix[2][3] = {{0}};
  int32_t extra[6];
  if (!read_i32(&raw)) return 0;
  pred_Q13[0] = (opus_int32)raw;
  if (!read_i32(&raw)) return 0;
  pred_Q13[1] = (opus_int32)raw;
  silk_stereo_quant_pred(pred_Q13, ix);
  extra[0] = ix[0][0];
  extra[1] = ix[0][1];
  extra[2] = ix[0][2];
  extra[3] = ix[1][0];
  extra[4] = ix[1][1];
  extra[5] = ix[1][2];
  return write_stereo_record(pred_Q13[0], pred_Q13[1], extra);
}

static int eval_find_predictor(void) {
  int i;
  int32_t raw;
  opus_int length;
  opus_int smooth_coef_Q16;
  opus_int32 ratio_Q14;
  opus_int32 mid_res_amp_Q0[2];
  opus_int16 x[MAX_STEREO_SAMPLES];
  opus_int16 y[MAX_STEREO_SAMPLES];
  int32_t extra[6] = {0};
  opus_int32 pred_Q13;

  if (!read_i32(&raw)) return 0;
  length = (opus_int)raw;
  if (length <= 0 || length > MAX_STEREO_SAMPLES) return 0;
  if (!read_i32(&raw)) return 0;
  mid_res_amp_Q0[0] = (opus_int32)raw;
  if (!read_i32(&raw)) return 0;
  mid_res_amp_Q0[1] = (opus_int32)raw;
  if (!read_i32(&raw)) return 0;
  smooth_coef_Q16 = (opus_int)raw;
  for (i = 0; i < length; i++) {
    if (!read_i32(&raw)) return 0;
    x[i] = (opus_int16)raw;
  }
  for (i = 0; i < length; i++) {
    if (!read_i32(&raw)) return 0;
    y[i] = (opus_int16)raw;
  }

  pred_Q13 = silk_stereo_find_predictor(&ratio_Q14, x, y, mid_res_amp_Q0, length, smooth_coef_Q16);
  extra[0] = mid_res_amp_Q0[0];
  extra[1] = mid_res_amp_Q0[1];
  return write_stereo_record(pred_Q13, ratio_Q14, extra);
}

static int eval_lr_to_ms(void) {
  int i;
  int32_t raw;
  opus_int frame_length;
  opus_int fs_kHz;
  opus_int total_rate_bps;
  opus_int prev_speech_act_Q8;
  opus_int toMono;
  stereo_enc_state state;
  opus_int16 x1[MAX_STEREO_SAMPLES + 2] = {0};
  opus_int16 x2[MAX_STEREO_SAMPLES + 2] = {0};
  opus_int8 ix[2][3] = {{0}};
  opus_int8 mid_only = 0;
  opus_int32 rates[2] = {0, 0};

  memset(&state, 0, sizeof(state));
  if (!read_i32(&raw)) return 0;
  frame_length = (opus_int)raw;
  if (frame_length <= 0 || frame_length > MAX_STEREO_SAMPLES) return 0;
  if (!read_i32(&raw)) return 0;
  fs_kHz = (opus_int)raw;
  if (!read_i32(&raw)) return 0;
  total_rate_bps = (opus_int)raw;
  if (!read_i32(&raw)) return 0;
  prev_speech_act_Q8 = (opus_int)raw;
  if (!read_i32(&raw)) return 0;
  toMono = (opus_int)(raw != 0);

  if (!read_i32(&raw)) return 0; state.pred_prev_Q13[0] = (opus_int16)raw;
  if (!read_i32(&raw)) return 0; state.pred_prev_Q13[1] = (opus_int16)raw;
  if (!read_i32(&raw)) return 0; state.sMid[0] = (opus_int16)raw;
  if (!read_i32(&raw)) return 0; state.sMid[1] = (opus_int16)raw;
  if (!read_i32(&raw)) return 0; state.sSide[0] = (opus_int16)raw;
  if (!read_i32(&raw)) return 0; state.sSide[1] = (opus_int16)raw;
  for (i = 0; i < 4; i++) {
    if (!read_i32(&raw)) return 0;
    state.mid_side_amp_Q0[i] = (opus_int32)raw;
  }
  if (!read_i32(&raw)) return 0; state.smth_width_Q14 = (opus_int16)raw;
  if (!read_i32(&raw)) return 0; state.width_prev_Q14 = (opus_int16)raw;
  if (!read_i32(&raw)) return 0; state.silent_side_len = (opus_int16)raw;

  for (i = 0; i < frame_length; i++) {
    if (!read_i32(&raw)) return 0;
    x1[i + 2] = (opus_int16)raw;
  }
  for (i = 0; i < frame_length; i++) {
    if (!read_i32(&raw)) return 0;
    x2[i + 2] = (opus_int16)raw;
  }

  silk_stereo_LR_to_MS(&state, &x1[2], &x2[2], ix, &mid_only, rates, total_rate_bps,
      prev_speech_act_Q8, toMono, fs_kHz, frame_length);
  return write_lr_to_ms_record(&state, ix, mid_only, rates, x1, x2, frame_length);
}

static int eval_state_sizes(void) {
  int32_t extra[6];
  stereo_enc_state state;
  extra[0] = (int32_t)sizeof(state.sSide[0]);
  extra[1] = (int32_t)sizeof(state.mid_side_amp_Q0[0]);
  extra[2] = (int32_t)sizeof(state.smth_width_Q14);
  extra[3] = (int32_t)sizeof(state.width_prev_Q14);
  extra[4] = (int32_t)sizeof(state.silent_side_len);
  extra[5] = (int32_t)sizeof(state);
  return write_stereo_record((int32_t)sizeof(state.pred_prev_Q13[0]), (int32_t)sizeof(state.sMid[0]), extra);
}

static int eval_packet0_wrapper(void) {
  int i, n, activity;
  int32_t raw;
  int enc_size = 0;
  opus_int32 input_bit_rate;
  opus_int input_max_bits;
  opus_int input_use_cbr;
  opus_int input_payload_size_ms;
  opus_int nSamplesIn;
  opus_int nBlocksOf10ms;
  opus_int nSamplesToBufferMax;
  opus_int nSamplesToBuffer;
  opus_int nSamplesFromInput;
  opus_int curr_nBitsUsedLBRR = 0;
  opus_int32 nBits;
  opus_int32 TargetRate_bps;
  opus_int32 MStargetRates_bps[2] = {0, 0};
  opus_int32 channelRate_bps;
  opus_int maxBits;
  opus_int useCBR;
  opus_int condCoding;
  opus_int32 nBytesOut = 0;
  opus_int ret;
  opus_int tellAfterSideInfo;
  silk_encoder *psEnc;
  silk_EncControlStruct control;
  ec_enc range_enc;
  unsigned char payload[4000];
  opus_int16 buf[960];
  float samples[960 * 2];

  if (!read_i32(&raw)) return 0;
  nSamplesIn = (opus_int)raw;
  if (nSamplesIn <= 0 || nSamplesIn > 960) return 0;
  if (!read_i32(&raw)) return 0;
  input_bit_rate = (opus_int32)raw;
  if (!read_i32(&raw)) return 0;
  input_max_bits = (opus_int)raw;
  if (!read_i32(&raw)) return 0;
  input_use_cbr = (opus_int)raw;
  if (!read_i32(&raw)) return 0;
  input_payload_size_ms = (opus_int)raw;
  if (!read_i32(&raw)) return 0;
  activity = (int)raw;
  for (i = 0; i < nSamplesIn * 2; i++) {
    if (!read_f32(&samples[i])) return 0;
  }

  if (silk_Get_Encoder_Size(&enc_size, 2) != 0 || enc_size <= 0) return 0;
  psEnc = (silk_encoder *)malloc((size_t)enc_size);
  if (psEnc == NULL) return 0;
  if (silk_InitEncoder(psEnc, 2, 0, &control) != 0) {
    free(psEnc);
    return 0;
  }

  memset(&control, 0, sizeof(control));
  control.nChannelsAPI = 2;
  control.nChannelsInternal = 2;
  control.API_sampleRate = 48000;
  control.maxInternalSampleRate = 16000;
  control.minInternalSampleRate = 8000;
  control.desiredInternalSampleRate = 16000;
  control.payloadSize_ms = input_payload_size_ms;
  control.bitRate = input_bit_rate;
  control.packetLossPercentage = 0;
  control.complexity = 10;
  control.useInBandFEC = 0;
  control.useDRED = 0;
  control.LBRR_coded = 0;
  control.useDTX = 0;
  control.useCBR = input_use_cbr;
  control.maxBits = input_max_bits;
  control.toMono = 0;
  control.opusCanSwitch = 0;
  control.reducedDependency = 0;

  for (n = 0; n < control.nChannelsAPI; n++) {
    psEnc->state_Fxx[n].sCmn.nFramesEncoded = 0;
  }
  if (control.nChannelsInternal > psEnc->nChannelsInternal) {
    if (silk_init_encoder(&psEnc->state_Fxx[1], psEnc->state_Fxx[0].sCmn.arch) != 0) {
      free(psEnc);
      return 0;
    }
    silk_memset(psEnc->sStereo.pred_prev_Q13, 0, sizeof(psEnc->sStereo.pred_prev_Q13));
    silk_memset(psEnc->sStereo.sSide, 0, sizeof(psEnc->sStereo.sSide));
    psEnc->sStereo.mid_side_amp_Q0[0] = 0;
    psEnc->sStereo.mid_side_amp_Q0[1] = 1;
    psEnc->sStereo.mid_side_amp_Q0[2] = 0;
    psEnc->sStereo.mid_side_amp_Q0[3] = 1;
    psEnc->sStereo.width_prev_Q14 = 0;
    psEnc->sStereo.smth_width_Q14 = SILK_FIX_CONST(1, 14);
    silk_memcpy(&psEnc->state_Fxx[1].sCmn.resampler_state, &psEnc->state_Fxx[0].sCmn.resampler_state, sizeof(silk_resampler_state_struct));
    silk_memcpy(&psEnc->state_Fxx[1].sCmn.In_HP_State, &psEnc->state_Fxx[0].sCmn.In_HP_State, sizeof(psEnc->state_Fxx[1].sCmn.In_HP_State));
  }
  psEnc->nChannelsAPI = control.nChannelsAPI;
  psEnc->nChannelsInternal = control.nChannelsInternal;

  nBlocksOf10ms = silk_DIV32(100 * nSamplesIn, control.API_sampleRate);
  for (n = 0; n < control.nChannelsInternal; n++) {
    opus_int force_fs_kHz = (n == 1) ? psEnc->state_Fxx[0].sCmn.fs_kHz : 0;
    if (silk_control_encoder(&psEnc->state_Fxx[n], &control, psEnc->allowBandwidthSwitch, n, force_fs_kHz) != 0) {
      free(psEnc);
      return 0;
    }
    if (psEnc->state_Fxx[n].sCmn.first_frame_after_reset) {
      for (i = 0; i < psEnc->state_Fxx[0].sCmn.nFramesPerPacket; i++) {
        psEnc->state_Fxx[n].sCmn.LBRR_flags[i] = 0;
      }
    }
    psEnc->state_Fxx[n].sCmn.inDTX = psEnc->state_Fxx[n].sCmn.useDTX;
  }

  nSamplesToBufferMax = 10 * nBlocksOf10ms * psEnc->state_Fxx[0].sCmn.fs_kHz;
  nSamplesToBuffer = psEnc->state_Fxx[0].sCmn.frame_length - psEnc->state_Fxx[0].sCmn.inputBufIx;
  if (nSamplesToBuffer > nSamplesToBufferMax) nSamplesToBuffer = nSamplesToBufferMax;
  nSamplesFromInput = silk_DIV32_16(nSamplesToBuffer * psEnc->state_Fxx[0].sCmn.API_fs_Hz, psEnc->state_Fxx[0].sCmn.fs_kHz * 1000);
  if (nSamplesFromInput > nSamplesIn || nSamplesFromInput > 960) {
    free(psEnc);
    return 0;
  }
  for (n = 0; n < nSamplesFromInput; n++) {
    buf[n] = FLOAT2INT16(samples[2 * n]);
  }
  if (psEnc->nPrevChannelsInternal == 1) {
    silk_memcpy(&psEnc->state_Fxx[1].sCmn.resampler_state, &psEnc->state_Fxx[0].sCmn.resampler_state, sizeof(psEnc->state_Fxx[1].sCmn.resampler_state));
  }
  if (silk_resampler(&psEnc->state_Fxx[0].sCmn.resampler_state,
      &psEnc->state_Fxx[0].sCmn.inputBuf[psEnc->state_Fxx[0].sCmn.inputBufIx + 2], buf, nSamplesFromInput) != 0) {
    free(psEnc);
    return 0;
  }
  psEnc->state_Fxx[0].sCmn.inputBufIx += nSamplesToBuffer;

  nSamplesToBuffer = psEnc->state_Fxx[1].sCmn.frame_length - psEnc->state_Fxx[1].sCmn.inputBufIx;
  if (nSamplesToBuffer > 10 * nBlocksOf10ms * psEnc->state_Fxx[1].sCmn.fs_kHz) {
    nSamplesToBuffer = 10 * nBlocksOf10ms * psEnc->state_Fxx[1].sCmn.fs_kHz;
  }
  for (n = 0; n < nSamplesFromInput; n++) {
    buf[n] = FLOAT2INT16(samples[2 * n + 1]);
  }
  if (silk_resampler(&psEnc->state_Fxx[1].sCmn.resampler_state,
      &psEnc->state_Fxx[1].sCmn.inputBuf[psEnc->state_Fxx[1].sCmn.inputBufIx + 2], buf, nSamplesFromInput) != 0) {
    free(psEnc);
    return 0;
  }
  psEnc->state_Fxx[1].sCmn.inputBufIx += nSamplesToBuffer;

  ec_enc_init(&range_enc, payload, sizeof(payload));
  {
    opus_uint8 iCDF[2] = {0, 0};
    iCDF[0] = 256 - silk_RSHIFT(256, (psEnc->state_Fxx[0].sCmn.nFramesPerPacket + 1) * control.nChannelsInternal);
    ec_enc_icdf(&range_enc, 0, iCDF, 8);
    curr_nBitsUsedLBRR = ec_tell(&range_enc);
  }
  for (n = 0; n < control.nChannelsInternal; n++) {
    psEnc->state_Fxx[n].sCmn.LBRR_flag = 0;
  }
  curr_nBitsUsedLBRR = ec_tell(&range_enc) - curr_nBitsUsedLBRR;
  silk_HP_variable_cutoff(psEnc->state_Fxx);

  nBits = silk_DIV32_16(silk_MUL(control.bitRate, control.payloadSize_ms), 1000);
  if (curr_nBitsUsedLBRR < 10) {
    psEnc->nBitsUsedLBRR = 0;
  } else if (psEnc->nBitsUsedLBRR < 10) {
    psEnc->nBitsUsedLBRR = curr_nBitsUsedLBRR;
  } else {
    psEnc->nBitsUsedLBRR = (psEnc->nBitsUsedLBRR + curr_nBitsUsedLBRR) / 2;
  }
  nBits -= psEnc->nBitsUsedLBRR;
  nBits = silk_DIV32_16(nBits, psEnc->state_Fxx[0].sCmn.nFramesPerPacket);
  if (control.payloadSize_ms == 10) {
    TargetRate_bps = silk_SMULBB(nBits, 100);
  } else {
    TargetRate_bps = silk_SMULBB(nBits, 50);
  }
  TargetRate_bps -= silk_DIV32_16(silk_MUL(psEnc->nBitsExceeded, 1000), BITRESERVOIR_DECAY_TIME_MS);
  TargetRate_bps = silk_LIMIT(TargetRate_bps, control.bitRate, 5000);

  silk_stereo_LR_to_MS(&psEnc->sStereo, &psEnc->state_Fxx[0].sCmn.inputBuf[2], &psEnc->state_Fxx[1].sCmn.inputBuf[2],
      psEnc->sStereo.predIx[psEnc->state_Fxx[0].sCmn.nFramesEncoded],
      &psEnc->sStereo.mid_only_flags[psEnc->state_Fxx[0].sCmn.nFramesEncoded],
      MStargetRates_bps, TargetRate_bps, psEnc->state_Fxx[0].sCmn.speech_activity_Q8, control.toMono,
      psEnc->state_Fxx[0].sCmn.fs_kHz, psEnc->state_Fxx[0].sCmn.frame_length);

  if (psEnc->sStereo.mid_only_flags[0] == 0) {
    silk_encode_do_VAD_Fxx(&psEnc->state_Fxx[1], activity);
  } else {
    psEnc->state_Fxx[1].sCmn.VAD_flags[0] = 0;
  }
  silk_stereo_encode_pred(&range_enc, psEnc->sStereo.predIx[0]);
  if (psEnc->state_Fxx[1].sCmn.VAD_flags[0] == 0) {
    silk_stereo_encode_mid_only(&range_enc, psEnc->sStereo.mid_only_flags[0]);
  }
  silk_encode_do_VAD_Fxx(&psEnc->state_Fxx[0], activity);

  channelRate_bps = MStargetRates_bps[0];
  maxBits = control.maxBits;
  useCBR = control.useCBR;
  if (MStargetRates_bps[1] > 0) {
    useCBR = 0;
    maxBits -= control.maxBits / 2;
  }
  if (channelRate_bps > 0) {
    silk_control_SNR(&psEnc->state_Fxx[0].sCmn, channelRate_bps);
  }
  condCoding = CODE_INDEPENDENTLY;
  tellAfterSideInfo = ec_tell(&range_enc);
  ret = silk_encode_frame_FLP(&psEnc->state_Fxx[0], &nBytesOut, &range_enc, condCoding, maxBits, useCBR);
  psEnc->state_Fxx[0].sCmn.controlled_since_last_payload = 0;
  psEnc->state_Fxx[0].sCmn.inputBufIx = 0;
  psEnc->state_Fxx[0].sCmn.nFramesEncoded++;

  if (!write_i32(TargetRate_bps) ||
      !write_i32(MStargetRates_bps[0]) ||
      !write_i32(MStargetRates_bps[1]) ||
      !write_i32(psEnc->state_Fxx[0].sCmn.VAD_flags[0]) ||
      !write_i32(psEnc->state_Fxx[1].sCmn.VAD_flags[0]) ||
      !write_i32(psEnc->state_Fxx[0].sCmn.speech_activity_Q8) ||
      !write_i32(psEnc->state_Fxx[1].sCmn.speech_activity_Q8) ||
      !write_i32(psEnc->state_Fxx[0].sCmn.input_tilt_Q15) ||
      !write_i32(psEnc->state_Fxx[1].sCmn.input_tilt_Q15) ||
      !write_i32(psEnc->state_Fxx[0].sCmn.SNR_dB_Q7) ||
      !write_i32(psEnc->state_Fxx[1].sCmn.SNR_dB_Q7) ||
      !write_i32(maxBits) ||
      !write_i32(useCBR) ||
      !write_i32(condCoding) ||
      !write_i32(tellAfterSideInfo) ||
      !write_i32(psEnc->sStereo.mid_only_flags[0]) ||
      !write_i32(ret) ||
      !write_i32(nBytesOut) ||
      !write_i32(ec_tell(&range_enc)) ||
      !write_i32((opus_int32)range_enc.rng) ||
      !write_i32(psEnc->state_Fxx[0].sShape.LastGainIndex) ||
      !write_i32(psEnc->state_Fxx[0].sCmn.indices.signalType) ||
      !write_i32(psEnc->state_Fxx[0].sCmn.indices.quantOffsetType) ||
      !write_i32(psEnc->state_Fxx[0].sCmn.indices.Seed) ||
      !write_i32(psEnc->state_Fxx[0].sCmn.frameCounter) ||
      !write_i32(psEnc->state_Fxx[0].sCmn.prevSignalType) ||
      !write_i32(psEnc->state_Fxx[0].sCmn.prevLag) ||
      !write_i32(psEnc->state_Fxx[0].sCmn.nFramesEncoded) ||
      !write_i32(psEnc->state_Fxx[0].sCmn.input_quality_bands_Q15[0]) ||
      !write_i32(psEnc->state_Fxx[0].sCmn.input_quality_bands_Q15[1]) ||
      !write_i32(psEnc->state_Fxx[0].sCmn.input_quality_bands_Q15[2]) ||
      !write_i32(psEnc->state_Fxx[0].sCmn.input_quality_bands_Q15[3])) {
    free(psEnc);
    return 0;
  }
  free(psEnc);
  return 1;
}

static int eval_record(uint32_t mode) {
  switch (mode) {
    case MODE_STEREO_QUANT_PRED: return eval_quant_pred();
    case MODE_STEREO_FIND_PREDICTOR: return eval_find_predictor();
    case MODE_STEREO_LR_TO_MS: return eval_lr_to_ms();
    case MODE_STEREO_STATE_SIZES: return eval_state_sizes();
    case MODE_STEREO_PACKET0_WRAPPER: return eval_packet0_wrapper();
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
  if (mode > MODE_STEREO_PACKET0_WRAPPER) return 1;

  if (!write_exact(OUTPUT_MAGIC, sizeof(magic)) || !write_u32(1) || !write_u32(count)) return 1;
  for (i = 0; i < count; i++) {
    if (!eval_record(mode)) return 1;
  }
  return 0;
}
