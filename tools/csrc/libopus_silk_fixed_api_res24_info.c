/* Stateful FIXED_POINT+ENABLE_RES24 silk_Encode oracle for 48 kHz API input. */
#include "config.h"
#include "API.h"
#include "arch.h"
#include "cpu_support.h"
#include "entenc.h"
#include "main_FIX.h"
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#if !defined(FIXED_POINT) || !defined(ENABLE_RES24)
#error "FIXED_POINT and ENABLE_RES24 are required"
#endif

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (fread(b, 1, 4, stdin) != 4) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) |
         ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int read_i32(int32_t *out) {
  uint32_t v;
  if (!read_u32(&v)) return 0;
  *out = (int32_t)v;
  return 1;
}

static int write_u32(uint32_t v) {
  unsigned char b[4] = {(unsigned char)v, (unsigned char)(v >> 8),
                        (unsigned char)(v >> 16), (unsigned char)(v >> 24)};
  return fwrite(b, 1, 4, stdout) == 4;
}

static int write_i32(int32_t v) { return write_u32((uint32_t)v); }

int main(void) {
  char magic[4];
  uint32_t version, api_fs, internal_fs, channels, frame_ms;
  uint32_t bitrate, max_bits, complexity, activity_bits, frames;
  if (fread(magic, 1, 4, stdin) != 4 || memcmp(magic, "GSRQ", 4) ||
      !read_u32(&version) || version != 1 || !read_u32(&api_fs) ||
      !read_u32(&internal_fs) || !read_u32(&channels) ||
      !read_u32(&frame_ms) || !read_u32(&bitrate) ||
      !read_u32(&max_bits) || !read_u32(&complexity) ||
      !read_u32(&activity_bits) || !read_u32(&frames)) return 2;
  if (api_fs != 48000 || (internal_fs != 8000 && internal_fs != 16000) ||
      channels != 1 || frame_ms != 10 || bitrate == 0 || max_bits == 0 ||
      complexity > 10 || (activity_bits != UINT32_MAX && activity_bits > 1) ||
      frames != 3) return 3;

  int n = (int)(api_fs * frame_ms / 1000);
  int arch = opus_select_arch();
  opus_int size = 0;
  if (silk_Get_Encoder_Size(&size, (int)channels) || size <= 0) return 4;
  void *st = calloc(1, size);
  opus_res *pcm = calloc((size_t)n * channels, sizeof(*pcm));
  if (!st || !pcm) { free(st); free(pcm); return 5; }

  silk_EncControlStruct ctl;
  memset(&ctl, 0, sizeof(ctl));
  if (silk_InitEncoder(st, (int)channels, arch, &ctl)) goto fail;
  if (fwrite("GSRP", 1, 4, stdout) != 4 || !write_u32(1) ||
      !write_u32(frames) || !write_u32((uint32_t)arch)) goto fail;

  for (uint32_t frame = 0; frame < frames; frame++) {
    memset(pcm, 0, (size_t)n * channels * sizeof(*pcm));
    for (int i = 0; i < n * (int)channels; i++) {
      int32_t sample;
      if (!read_i32(&sample)) goto fail;
      pcm[i] = sample;
    }

    ctl.nChannelsAPI = ctl.nChannelsInternal = (opus_int)channels;
    ctl.API_sampleRate = (opus_int)api_fs;
    ctl.maxInternalSampleRate = 16000;
    ctl.minInternalSampleRate = 8000;
    ctl.desiredInternalSampleRate = (opus_int)internal_fs;
    ctl.payloadSize_ms = (opus_int)frame_ms;
    ctl.bitRate = (opus_int)bitrate;
    ctl.packetLossPercentage = 0;
    ctl.complexity = (opus_int)complexity;
    ctl.useInBandFEC = 0;
    ctl.useDTX = 0;
    ctl.useCBR = 1;
    ctl.maxBits = (opus_int)max_bits;
    ctl.toMono = 0;
    ctl.reducedDependency = 0;

    unsigned char buf[1275] = {0};
    ec_enc ec;
    ec_enc_init(&ec, buf, sizeof(buf));
    opus_int32 bytes = (opus_int32)sizeof(buf);
    int err = silk_Encode(st, &ctl, pcm, n, &ec, &bytes, 0,
                          (int32_t)activity_bits);
    /* opus_encode_frame_native carries this output into the next SILK call. */
    ctl.opusCanSwitch = ctl.switchReady;
    uint32_t range = ec.rng, tell = (uint32_t)ec_tell(&ec);
    ec_enc_done(&ec);
    if (err || ec.error || bytes <= 0 || bytes > (opus_int32)sizeof(buf)) goto fail;
    silk_encoder *silk = (silk_encoder *)st;
    silk_encoder_state_FIX *state = &silk->state_Fxx[0];
    silk_encoder_state *common = &state->sCmn;
    if (!write_u32(0) || !write_u32((uint32_t)bytes) || !write_u32(range) ||
        !write_u32(tell) || fwrite(buf, 1, (size_t)bytes, stdout) != (size_t)bytes ||
        !write_i32(common->frameCounter) ||
        !write_i32(common->indices.signalType) ||
        !write_i32(common->indices.quantOffsetType) ||
        !write_i32(common->indices.Seed) ||
        !write_i32(common->speech_activity_Q8) ||
        !write_i32(common->input_tilt_Q15) ||
        !write_i32(common->sum_log_gain_Q7) ||
        !write_i32(common->prevSignalType) ||
        !write_i32(common->prevLag) ||
        !write_i32(common->ec_prevSignalType) ||
        !write_i32(common->ec_prevLagIndex) ||
        !write_i32(state->sShape.LastGainIndex) ||
        !write_i32(state->sShape.HarmShapeGain_smth_Q16) ||
        !write_i32(state->sShape.Tilt_smth_Q16) ||
        !write_i32(state->LTPCorr_Q15) ||
        !write_i32(silk->nBitsExceeded) ||
        !write_i32(silk->nBitsUsedLBRR)) goto fail;
    uint32_t x_buf_count = (uint32_t)(sizeof(state->x_buf) / sizeof(state->x_buf[0]));
    if (!write_u32(x_buf_count)) goto fail;
    for (uint32_t i = 0; i < x_buf_count; i++) {
      if (!write_i32(state->x_buf[i])) goto fail;
    }
    if (!write_i32(common->pitchEstimationThreshold_Q16) ||
        !write_i32(common->pitchEstimationComplexity) ||
        !write_i32(common->pitch_LPC_win_length) ||
        !write_i32(common->first_frame_after_reset) ||
        !write_i32(common->arch) ||
        !write_i32(common->nb_subfr)) goto fail;
  }
  if (fgetc(stdin) != EOF || ferror(stdin) || fflush(stdout)) goto fail;
  free(pcm);
  free(st);
  return 0;

fail:
  free(pcm);
  free(st);
  return 6;
}
