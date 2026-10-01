#include <math.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif
#include "opus.h"
#include "opus_multistream.h"
#include "opus_projection.h"

static void u32(uint32_t v) {
  unsigned char b[4] = {v, v >> 8, v >> 16, v >> 24};
  if (fwrite(b, 1, 4, stdout) != 4) _Exit(2);
}

int main(void) {
  unsigned char packets[12][1500], mapping[1] = {0}, matrix[2] = {255, 127};
  int sizes[12], error;
  float input[960], floats[960];
  opus_int16 shorts[960];
  opus_int32 ints[960];
#ifdef _WIN32
  _setmode(_fileno(stdout), _O_BINARY);
#endif
  OpusEncoder *enc = opus_encoder_create(48000, 1, OPUS_APPLICATION_RESTRICTED_LOWDELAY, &error);
  if (!enc || error) return 3;
  if (opus_encoder_ctl(enc, OPUS_SET_BITRATE(64000)) ||
      opus_encoder_ctl(enc, OPUS_SET_VBR(0))) return 4;
  fwrite("MSCO", 1, 4, stdout);
  u32(1);
  u32(12);
  for (int k = 0; k < 12; ++k) {
    for (int i = 0; i < 960; ++i)
      input[i] = .8f * sinf((float)(6.2831853071795864769 * 113 * (k * 960 + i) / 48000.0));
    sizes[k] = opus_encode_float(enc, input, 960, packets[k], 1500);
    if (sizes[k] <= 0) return 5;
    u32(sizes[k]);
    fwrite(packets[k], 1, sizes[k], stdout);
  }
  opus_encoder_destroy(enc);
  /* A successful float/int24 call clears clipping memory; reset clears it;
     a loss call bypasses clipping and preserves it. All cases end in recovery. */
  for (int scenario = 0; scenario < 4; ++scenario) {
    OpusMSDecoder *ms = NULL;
    OpusProjectionDecoder *pr = NULL;
    if (scenario < 2) {
      ms = opus_multistream_decoder_create(48000, 1, 1, 0, mapping, &error);
      if (!ms || error || opus_multistream_decoder_ctl(ms, OPUS_SET_GAIN(4608))) return 6;
    } else {
      pr = opus_projection_decoder_create(48000, 1, 1, 0, matrix, 2, &error);
      if (!pr || error || opus_projection_decoder_ctl(pr, OPUS_SET_GAIN(4608))) return 7;
    }
    int prime = scenario == 3 ? 8 : 1;
    int steps = prime + (scenario < 2 ? 3 : 2);
    u32(steps);
    for (int step = 0; step < steps; ++step) {
      int format = 1, packet = step < prime ? step : 1;
      if (step == prime && scenario < 2) {
        if (opus_multistream_decoder_ctl(ms, OPUS_SET_GAIN(0))) return 8;
        format = scenario == 0 ? 0 : 2;
        packet = 0;
      } else if (step == prime && scenario == 2) {
        if (opus_projection_decoder_ctl(pr, OPUS_RESET_STATE) ||
            opus_projection_decoder_ctl(pr, OPUS_SET_GAIN(0))) return 9;
      } else if (step == prime && scenario == 3) packet = -1;
      const unsigned char *data = packet < 0 ? NULL : packets[packet];
      int size = packet < 0 ? 0 : sizes[packet];
      int n = format == 0 ? opus_multistream_decode_float(ms, data, size, floats, 960, 0) :
              format == 2 ? opus_multistream_decode24(ms, data, size, ints, 960, 0) :
              ms ? opus_multistream_decode(ms, data, size, shorts, 960, 0) :
                   opus_projection_decode(pr, data, size, shorts, 960, 0);
      if (n != 960) return 10;
      opus_uint32 range;
      if (ms ? opus_multistream_decoder_ctl(ms, OPUS_GET_FINAL_RANGE(&range)) :
               opus_projection_decoder_ctl(pr, OPUS_GET_FINAL_RANGE(&range))) return 11;
      u32(format); u32(n); u32(range);
      for (int i = 0; i < n; ++i) {
        uint32_t bits;
        if (format == 0) memcpy(&bits, &floats[i], 4);
        else bits = format == 1 ? (uint32_t)(int32_t)shorts[i] : (uint32_t)ints[i];
        u32(bits);
      }
    }
    if (ms) opus_multistream_decoder_destroy(ms);
    if (pr) opus_projection_decoder_destroy(pr);
  }
  return ferror(stdout) ? 12 : 0;
}
