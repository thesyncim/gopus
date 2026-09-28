#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#ifdef HAVE_CONFIG_H
#include "config.h"
#endif

#include "opus.h"
#include "opus_multistream.h"
#include "opus_private.h"
#include "silk/control.h"
#include "lpcnet_private.h"
#include "cpu_support.h"

#ifndef ENABLE_DEEP_PLC
#error ENABLE_DEEP_PLC is required
#endif
#if !defined(ENABLE_DRED) && !defined(ENABLE_OSCE)
#error ENABLE_DRED or ENABLE_OSCE is required
#endif

/* Matches the prefix of OpusDecoder in src/opus_decoder.c. */
typedef struct {
  int celt_dec_offset;
  int silk_dec_offset;
  int channels;
  opus_int32 Fs;
  silk_DecControlStruct DecControl;
  int decode_gain;
  int complexity;
  int ignore_extensions;
  int arch;
  LPCNetPLCState lpcnet;
} GopusInternalOpusDecoder;

static void put_u32(uint32_t x) {
  unsigned char b[4] = {(unsigned char)x, (unsigned char)(x >> 8),
      (unsigned char)(x >> 16), (unsigned char)(x >> 24)};
  if (fwrite(b, 1, sizeof(b), stdout) != sizeof(b)) exit(2);
}

static uint32_t get_u32(void) {
  unsigned char b[4];
  if (fread(b, 1, sizeof(b), stdin) != sizeof(b)) exit(3);
  return (uint32_t)b[0] | ((uint32_t)b[1] << 8) |
      ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
}

int main(void) {
  uint32_t model_len;
  unsigned char *model;
  unsigned char packet[1275];
  opus_int16 input[320];
  int err = 0;
  int packet_len;
  int i, complexity_idx, step;
  unsigned char mapping[1] = {0};
  OpusEncoder *enc;
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 18;
#endif
  model_len = get_u32();
  if (model_len == 0 || model_len > 10000000) return 4;
  model = (unsigned char *)malloc(model_len);
  if (!model || fread(model, 1, model_len, stdin) != model_len) return 5;

  enc = opus_encoder_create(16000, 1, OPUS_APPLICATION_VOIP, &err);
  if (!enc || err != OPUS_OK) return 6;
  if (opus_encoder_ctl(enc, OPUS_SET_FORCE_MODE(MODE_SILK_ONLY)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_BANDWIDTH(OPUS_BANDWIDTH_WIDEBAND)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_BITRATE(24000)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_SIGNAL(OPUS_SIGNAL_VOICE)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_VBR(0)) != OPUS_OK) return 7;
  for (i = 0; i < 320; i++) {
    int phase = (i * 131) & 1023;
    input[i] = (opus_int16)(60 * ((phase < 512 ? phase : 1023 - phase) - 256));
  }
  packet_len = opus_encode(enc, input, 320, packet, sizeof(packet));
  opus_encoder_destroy(enc);
  if (packet_len <= 0 || opus_packet_get_bandwidth(packet) != OPUS_BANDWIDTH_WIDEBAND) return 8;

  if (fwrite("MNSQ", 1, 4, stdout) != 4) return 9;
  put_u32((uint32_t)opus_select_arch());
  {
    uint32_t features = 0;
#ifdef ENABLE_DRED
    features |= 1;
#endif
#ifdef ENABLE_OSCE
    features |= 2;
#endif
#ifdef ENABLE_QEXT
    features |= 4;
#endif
    put_u32(features);
  }
#ifdef USE_WEIGHTS_FILE
  put_u32(1);
#else
  put_u32(2);
#endif
  put_u32((uint32_t)packet_len);
  if (fwrite(packet, 1, packet_len, stdout) != (size_t)packet_len) return 10;

  for (complexity_idx = 0; complexity_idx < 2; complexity_idx++) {
    const int complexity = complexity_idx == 0 ? 0 : 5;
    OpusMSDecoder *ms;
    OpusDecoder *child = NULL;
    GopusInternalOpusDecoder *internal;
    float first_pcm[320];
    uint32_t first_bits[320];
    uint32_t first_range = 0;
    int first_ret;
    int first_blend;
    int first_analysis_pos;
    int first_predict_pos;
    err = 0;
    ms = opus_multistream_decoder_create(16000, 1, 1, 0, mapping, &err);
    if (!ms || err != OPUS_OK) return 11;
    if (opus_multistream_decoder_ctl(ms, OPUS_SET_COMPLEXITY(complexity)) != OPUS_OK) return 12;
    if (opus_multistream_decoder_ctl(ms, OPUS_MULTISTREAM_GET_DECODER_STATE(0, &child)) != OPUS_OK || !child) return 13;
    first_ret = opus_multistream_decode_float(ms, packet, packet_len, first_pcm, 320, 0);
    if (first_ret != 320 || opus_multistream_decoder_ctl(ms, OPUS_GET_FINAL_RANGE(&first_range)) != OPUS_OK) return 14;
    first_blend = ((GopusInternalOpusDecoder *)child)->lpcnet.blend;
    first_analysis_pos = ((GopusInternalOpusDecoder *)child)->lpcnet.analysis_pos;
    first_predict_pos = ((GopusInternalOpusDecoder *)child)->lpcnet.predict_pos;
    for (i = 0; i < first_ret; i++) memcpy(&first_bits[i], &first_pcm[i], sizeof(first_bits[i]));
    internal = (GopusInternalOpusDecoder *)child;
    if (internal->complexity != complexity) return 16;
    put_u32((uint32_t)complexity);
    put_u32((uint32_t)internal->lpcnet.loaded);
    put_u32((uint32_t)first_ret);
    put_u32((uint32_t)first_range);
    put_u32((uint32_t)first_blend);
    put_u32((uint32_t)first_analysis_pos);
    put_u32((uint32_t)first_predict_pos);
    for (i = 0; i < first_ret; i++) put_u32(first_bits[i]);
    for (step = 1; step < 6; step++) {
      float pcm[320];
      opus_uint32 range = 0;
      const int loss = step == 1 ||
          (complexity == 0 && (step == 2 || step == 5)) ||
          (complexity == 5 && step == 4);
      const unsigned char *decode_input = loss ? NULL : packet;
      int length = loss ? 0 : packet_len;
#ifdef USE_WEIGHTS_FILE
      if (step == 1 || step == 2) {
        err = opus_decoder_ctl(child, OPUS_SET_DNN_BLOB(model, (opus_int32)model_len));
        if (err != OPUS_OK) return 19;
      }
#endif
      if (complexity == 0 && step == 2 &&
          opus_multistream_decoder_ctl(ms, OPUS_SET_COMPLEXITY(5)) != OPUS_OK) return 20;
      int ret = opus_multistream_decode_float(ms, decode_input, length, pcm, 320, 0);
      if (!internal->lpcnet.loaded || ret != 320) return 21;
      if (opus_multistream_decoder_ctl(ms, OPUS_GET_FINAL_RANGE(&range)) != OPUS_OK) return 17;
      put_u32((uint32_t)ret);
      put_u32((uint32_t)range);
      put_u32((uint32_t)internal->lpcnet.blend);
      put_u32((uint32_t)internal->lpcnet.analysis_pos);
      put_u32((uint32_t)internal->lpcnet.predict_pos);
      if (ret > 0) {
        if (ret > 320) return 18;
        for (i = 0; i < ret; i++) {
          uint32_t bits;
          memcpy(&bits, &pcm[i], sizeof(bits));
          put_u32(bits);
        }
      }
    }
    opus_multistream_decoder_destroy(ms);
  }
  free(model);
  return 0;
}
