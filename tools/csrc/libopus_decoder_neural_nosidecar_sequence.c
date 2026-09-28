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

/* Matches the prefix of OpusDecoder in src/opus_decoder.c. The probe checks
   loaded and the PLC cursors without changing the public decode path. */
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

/* Input: model blob length and bytes. Output: DNCP, C dispatch/feature/model
   identity, one C-generated SILK WB packet, then public PCM/range and PLC
   cursors for complexity 0 and 5 across received/loss/recovery/loss/recovery. */

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
  static const opus_int32 sample_rates[] = {8000, 12000, 16000, 24000, 48000};
  uint32_t model_len;
  unsigned char *model;
  unsigned char packet[1275];
  opus_int16 input[320];
  OpusEncoder *enc;
  int packet_len;
  int rate_idx, complexity_idx, step;
  int i, err = 0;
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 18;
#endif
  model_len = get_u32();
  if (model_len == 0 || model_len > 10000000) return 4;
  model = (unsigned char *)malloc(model_len);
  if (!model) return 5;
  if (fread(model, 1, model_len, stdin) != model_len) return 6;
  enc = opus_encoder_create(16000, 1, OPUS_APPLICATION_VOIP, &err);
  if (!enc || err != OPUS_OK) return 14;
  if (opus_encoder_ctl(enc, OPUS_SET_FORCE_MODE(MODE_SILK_ONLY)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_BANDWIDTH(OPUS_BANDWIDTH_WIDEBAND)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_BITRATE(24000)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_SIGNAL(OPUS_SIGNAL_VOICE)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_VBR(0)) != OPUS_OK) return 15;
  for (i = 0; i < 320; i++) {
    int phase = (i * 131) & 1023;
    input[i] = (opus_int16)(60 * ((phase < 512 ? phase : 1023 - phase) - 256));
  }
  packet_len = opus_encode(enc, input, 320, packet, sizeof(packet));
  opus_encoder_destroy(enc);
  if (packet_len <= 0 || opus_packet_get_bandwidth(packet) != OPUS_BANDWIDTH_WIDEBAND) return 16;
  if (fwrite("DNCP", 1, 4, stdout) != 4) return 7;
  put_u32((uint32_t)opus_select_arch());
#ifdef OPUS_HAVE_RTCD
  put_u32(1);
#else
  put_u32(0);
#endif
#ifdef OPUS_ARM_PRESUME_NEON_INTR
  put_u32(1);
#else
  put_u32(0);
#endif
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
  put_u32(1); /* external model */
#else
  put_u32(2); /* pinned built-in model */
#endif
  put_u32((uint32_t)packet_len);
  if (fwrite(packet, 1, packet_len, stdout) != (size_t)packet_len) return 17;
  put_u32((uint32_t)(sizeof(sample_rates) / sizeof(sample_rates[0])));
  for (rate_idx = 0; rate_idx < (int)(sizeof(sample_rates) / sizeof(sample_rates[0])); rate_idx++) {
    const opus_int32 sample_rate = sample_rates[rate_idx];
    const int frame_size = sample_rate / 50;
    put_u32((uint32_t)sample_rate);
    for (complexity_idx = 0; complexity_idx < 2; complexity_idx++) {
      const int complexity = complexity_idx == 0 ? 0 : 5;
      OpusDecoder *dec;
      GopusInternalOpusDecoder *internal;
      err = 0;
      dec = opus_decoder_create(sample_rate, 1, &err);
      if (!dec || err != OPUS_OK) return 8;
      if (opus_decoder_ctl(dec, OPUS_SET_COMPLEXITY(complexity)) != OPUS_OK) return 9;
#ifdef USE_WEIGHTS_FILE
      err = opus_decoder_ctl(dec, OPUS_SET_DNN_BLOB(model, (opus_int32)model_len));
      if (err != OPUS_OK) { fprintf(stderr, "set DNN blob err=%d model_len=%u complexity=%d rate=%d\n", err, model_len, complexity, sample_rate); return 10; }
#endif
      internal = (GopusInternalOpusDecoder *)dec;
      if (!internal->lpcnet.loaded || internal->complexity != complexity) return 11;
      put_u32((uint32_t)complexity);
      put_u32((uint32_t)internal->lpcnet.loaded);
      for (step = 0; step < 5; step++) {
        float pcm[960];
        opus_uint32 range = 0;
        const int lost = step == 1 || step == 3;
        const unsigned char *decode_input = lost ? NULL : packet;
        int length = lost ? 0 : packet_len;
        int ret = opus_decode_float(dec, decode_input, length, pcm, frame_size, 0);
        if (opus_decoder_ctl(dec, OPUS_GET_FINAL_RANGE(&range)) != OPUS_OK) return 12;
        put_u32((uint32_t)ret);
        put_u32((uint32_t)range);
        put_u32((uint32_t)internal->lpcnet.blend);
        put_u32((uint32_t)internal->lpcnet.analysis_pos);
        put_u32((uint32_t)internal->lpcnet.predict_pos);
        if (ret > 0) {
          int i;
          if (ret != frame_size) return 13;
          for (i = 0; i < ret; i++) {
            uint32_t bits;
            memcpy(&bits, &pcm[i], sizeof(bits));
            put_u32(bits);
          }
        }
      }
      opus_decoder_destroy(dec);
    }
  }
  free(model);
  return 0;
}
