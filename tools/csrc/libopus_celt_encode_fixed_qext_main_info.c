/* Fixed-point CELT main-payload oracle with ENABLE_QEXT arithmetic enabled.
 *
 * The caller selects a packet budget. At budgets that reserve a QEXT side
 * payload, the oracle reports the main and combined final ranges and returns
 * the exact inner packet framing for independent main/side inspection.
 *
 * Input, little endian: "GQXM", version 2, channels, stream_channels,
 * frame_size, start, end, max_bytes, bitrate, complexity, sample_rate, vbr,
 * constrained_vbr, lsb_depth, lfe, AnalysisInfo, then channels*frame_size
 * signed opus_res Q8 samples. Output: "GQXO", version 1, packet length,
 * final range, main range, side-payload flag, tell values, packet.
 */

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "celt/arch.h"
#include "celt/celt.h"
#include "celt/cpu_support.h"
#include "celt/entenc.h"
#include "opus_defines.h"

#if !defined(FIXED_POINT) || !defined(ENABLE_RES24) || !defined(ENABLE_QEXT)
#error "fixed-QEXT main oracle requires FIXED_POINT, ENABLE_RES24, and ENABLE_QEXT"
#endif

int celt_encode_with_ec(CELTEncoder *st, const opus_res *pcm, int frame_size,
    unsigned char *compressed, int nbCompressedBytes, ec_enc *enc);

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (fread(b, 1, 4, stdin) != 4) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) |
      ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int write_u32(uint32_t value) {
  unsigned char b[4];
  b[0] = (unsigned char)value;
  b[1] = (unsigned char)(value >> 8);
  b[2] = (unsigned char)(value >> 16);
  b[3] = (unsigned char)(value >> 24);
  return fwrite(b, 1, 4, stdout) == 4;
}

static int read_float(float *out) {
  uint32_t bits;
  if (!read_u32(&bits)) return 0;
  memcpy(out, &bits, sizeof(bits));
  return 1;
}

static int read_analysis(AnalysisInfo *info) {
  uint32_t valid, bandwidth;
  memset(info, 0, sizeof(*info));
  if (!read_u32(&valid) || valid > 1) return 0;
  info->valid = (int)valid;
  if (!read_float(&info->tonality) || !read_float(&info->tonality_slope) ||
      !read_float(&info->noisiness) || !read_float(&info->activity) ||
      !read_float(&info->music_prob) || !read_float(&info->music_prob_min) ||
      !read_float(&info->music_prob_max) || !read_u32(&bandwidth)) return 0;
  info->bandwidth = (int32_t)bandwidth;
  if (!read_float(&info->activity_probability) ||
      !read_float(&info->max_pitch_ratio) ||
      fread(info->leak_boost, 1, LEAK_BANDS, stdin) != LEAK_BANDS) return 0;
  return 1;
}

int main(void) {
  unsigned char magic[4], packet_storage[1276];
  unsigned char *packet = packet_storage + 1;
  uint32_t version, channels, stream_channels, frame_size, start, end;
  uint32_t max_bytes, bitrate, complexity, sample_rate, vbr, cvbr, lsb_depth, lfe;
  AnalysisInfo analysis;
  CELTEncoder *st = NULL;
  opus_res *pcm = NULL;
  ec_enc ec;
  opus_uint32 final_range;
  uint32_t has_qext;
  uint32_t raw, i;
  int ret;
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 1;
#endif
  if (fread(magic, 1, 4, stdin) != 4 || memcmp(magic, "GQXM", 4) ||
      !read_u32(&version) || version != 2 || !read_u32(&channels) ||
      !read_u32(&stream_channels) || !read_u32(&frame_size) ||
      !read_u32(&start) || !read_u32(&end) || !read_u32(&max_bytes) ||
      !read_u32(&bitrate) || !read_u32(&complexity) ||
      !read_u32(&sample_rate) || !read_u32(&vbr) || !read_u32(&cvbr) ||
      !read_u32(&lsb_depth) || !read_u32(&lfe) || !read_analysis(&analysis)) return 1;
  if ((sample_rate != 48000 && sample_rate != 96000) || channels < 1 || channels > 2 ||
      stream_channels < 1 || stream_channels > channels ||
      frame_size > (sample_rate == 96000 ? 1920u : 960u) ||
      (sample_rate == 48000 && frame_size != 960 && frame_size != 480 && frame_size != 240) ||
      (sample_rate == 96000 && frame_size != 1920 && frame_size != 960 && frame_size != 480 && frame_size != 240) ||
      start >= end || end > 21 ||
      max_bytes < 2 || max_bytes > 1275 || bitrate == 0 ||
      complexity > 10 || vbr > 1 || cvbr > 1 || lsb_depth < 8 ||
      lsb_depth > 24 || lfe > 1) return 1;
  pcm = (opus_res *)malloc((size_t)channels * frame_size * sizeof(*pcm));
  st = (CELTEncoder *)calloc(1, celt_encoder_get_size((int)channels));
  if (!pcm || !st) return 1;
  for (i = 0; i < channels*frame_size; i++) {
    if (!read_u32(&raw)) return 1;
    pcm[i] = (opus_res)(int32_t)raw;
  }
  memset(packet_storage, 0, sizeof(packet_storage));
  if (celt_encoder_init(st, (opus_int32)sample_rate, (int)channels, opus_select_arch()) != OPUS_OK ||
      celt_encoder_ctl(st, CELT_SET_START_BAND_REQUEST, (int)start) != OPUS_OK ||
      celt_encoder_ctl(st, CELT_SET_END_BAND_REQUEST, (int)end) != OPUS_OK ||
      celt_encoder_ctl(st, CELT_SET_CHANNELS(stream_channels)) != OPUS_OK ||
      celt_encoder_ctl(st, OPUS_SET_BITRATE_REQUEST, (opus_int32)(int32_t)bitrate) != OPUS_OK ||
      celt_encoder_ctl(st, OPUS_SET_COMPLEXITY_REQUEST, (int)complexity) != OPUS_OK ||
      celt_encoder_ctl(st, OPUS_SET_VBR_REQUEST, (int)vbr) != OPUS_OK ||
      celt_encoder_ctl(st, OPUS_SET_VBR_CONSTRAINT_REQUEST, (int)cvbr) != OPUS_OK ||
      celt_encoder_ctl(st, OPUS_SET_LSB_DEPTH_REQUEST, (int)lsb_depth) != OPUS_OK ||
      celt_encoder_ctl(st, OPUS_SET_LFE_REQUEST, (int)lfe) != OPUS_OK ||
      celt_encoder_ctl(st, CELT_SET_SIGNALLING_REQUEST, 0) != OPUS_OK ||
      celt_encoder_ctl(st, CELT_SET_ANALYSIS(&analysis)) != OPUS_OK ||
      celt_encoder_ctl(st, OPUS_SET_QEXT(1)) != OPUS_OK) return 1;
  ec_enc_init(&ec, packet, (int)max_bytes);
  ret = celt_encode_with_ec(st, pcm, (int)frame_size, packet, (int)max_bytes, &ec);
  has_qext = (packet_storage[0] & 3) == 3;
  if (ret < 0 || ret > (int)max_bytes ||
      celt_encoder_ctl(st, OPUS_GET_FINAL_RANGE(&final_range)) != OPUS_OK ||
      fwrite("GQXO", 1, 4, stdout) != 4 || !write_u32(1) || !write_u32(1) ||
      !write_u32((uint32_t)ret) || !write_u32(ec.rng) || !write_u32(final_range) ||
      !write_u32(has_qext) ||
      !write_u32((uint32_t)ec_tell_frac(&ec)) || !write_u32((uint32_t)ec_tell(&ec)) ||
      fwrite(packet, 1, (size_t)ret, stdout) != (size_t)ret) return 1;
  free(st);
  free(pcm);
  return 0;
}
