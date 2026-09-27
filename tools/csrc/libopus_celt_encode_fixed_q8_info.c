/* Selected FIXED_POINT CELT encode oracle on exact opus_res Q8 input.
 * The caller supplies each frame's coding samples, packet budget, analysis
 * snapshot, energy mask, SILK info and prediction mode. One CELTEncoder carries
 * state across all frames.
 *
 * Input, little endian: "GQRI", version 3, 4, or 5, input_channels, stream_channels,
 * frame_size, start, end,
 * bitrate, complexity, sample_rate, vbr, constrained_vbr, lfe, lsb_depth,
 * frame_count. Version 4 inserts qext_enabled after lsb_depth. For each frame:
 * max_bytes, sample_count, sample_count signed Q8
 * words; prefix_count uniform-8 symbols, reset_before, set_prediction,
 * prediction_mode, SILK signal type and quantization offset; AnalysisInfo valid,
 * ten scalar words in celt.h order, 19 leak_boost bytes, has_mask, then
 * channels*21 signed mask words when has_mask. Prefix symbols leave the range
 * coder in a nonempty state before celt_encode_with_ec continues it.
 * Version 5 inserts per-frame stream_channels and signed bitrate after each
 * frame's max_bytes/sample_count pair; zero retains the previous control.
 * Output, little endian: "GQRO", version 1, frame_count; each frame has
 * packet_len, final_range, packet_len packet bytes.
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

#if !defined(FIXED_POINT) || !defined(ENABLE_RES24)
#error "raw Q8 CELT oracle requires selected FIXED_POINT ENABLE_RES24 libopus"
#endif
#if defined(GOPUS_REQUIRE_QEXT) && !defined(ENABLE_QEXT)
#error "fixed-QEXT raw Q8 CELT oracle requires selected ENABLE_QEXT libopus"
#endif

#ifdef GOPUS_STATE_TRACE
/* Include the pinned encoder implementation so this test-only helper can read
 * its private state fields without depending on struct padding or offsets. */
static int opus_custom_encoder_get_size(const CELTMode *mode, int channels);
#include "celt_encoder.c"
#else
int celt_encode_with_ec(CELTEncoder *st, const opus_res *pcm, int frame_size,
    unsigned char *compressed, int nbCompressedBytes, ec_enc *enc);
#endif

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

static int write_float(float value) {
  uint32_t bits;
  memcpy(&bits, &value, sizeof(bits));
  return write_u32(bits);
}

#ifdef GOPUS_STATE_TRACE
static int write_i32_array(const opus_int32 *values, int count) {
  int i;
  if (count < 0 || !write_u32((uint32_t)count)) return 0;
  for (i = 0; i < count; i++) {
    if (!write_u32((uint32_t)values[i])) return 0;
  }
  return 1;
}

/* Emit scalar state first, followed by explicit logical array contents. Keep
 * this order synchronized with CELTFixedQ8EncoderState in Go. */
static int write_encoder_state(CELTEncoder *opaque) {
  struct OpusCustomEncoder *st = (struct OpusCustomEncoder *)opaque;
  int channels = st->channels;
  int overlap = st->mode->overlap;
  int bands = st->mode->nbEBands;
  int max_period = QEXT_SCALE2(COMBFILTER_MAXPERIOD, st->qext_scale);
  celt_sig *prefilter_mem = st->in_mem + channels*overlap;
  celt_glog *oldBandE = (celt_glog *)(st->in_mem + channels*(overlap+max_period));
  celt_glog *oldLogE = oldBandE + channels*bands;
  celt_glog *oldLogE2 = oldLogE + channels*bands;
  celt_glog *energyError = oldLogE2 + channels*bands;
  celt_glog *qextOldBandE = energyError + channels*bands;
  int32_t scalars[] = {
    (int32_t)st->spread_decision,
    (int32_t)st->delayedIntra,
    (int32_t)st->tonal_average,
    (int32_t)st->lastCodedBands,
    (int32_t)st->hf_average,
    (int32_t)st->tapset_decision,
    (int32_t)st->prefilter_period,
    (int32_t)st->prefilter_gain,
    (int32_t)st->prefilter_tapset,
    (int32_t)st->consec_transient,
    (int32_t)st->vbr_reservoir,
    (int32_t)st->vbr_drift,
    (int32_t)st->vbr_offset,
    (int32_t)st->vbr_count,
    (int32_t)st->overlap_max,
    (int32_t)st->stereo_saving,
    (int32_t)st->intensity,
    (int32_t)st->spec_avg,
    (int32_t)st->force_intra,
    (int32_t)st->disable_pf,
    (int32_t)st->silk_info.signalType,
    (int32_t)st->silk_info.offset,
    (int32_t)st->analysis.valid,
    (int32_t)st->analysis.bandwidth,
  };
  int i;
  if (!write_u32(st->rng) || !write_u32((uint32_t)(sizeof(scalars)/sizeof(scalars[0])))) return 0;
  for (i = 0; i < (int)(sizeof(scalars)/sizeof(scalars[0])); i++) {
    if (!write_u32((uint32_t)scalars[i])) return 0;
  }
  if (!write_float(st->analysis.activity) ||
      !write_float(st->analysis.tonality) ||
      !write_float(st->analysis.tonality_slope) ||
      !write_float(st->analysis.max_pitch_ratio) ||
      fwrite(st->analysis.leak_boost, 1, LEAK_BANDS, stdout) != LEAK_BANDS ||
      !write_i32_array(st->energy_mask, st->energy_mask ? channels*bands : 0) ||
      !write_i32_array(st->preemph_memE, channels) ||
      !write_i32_array(st->in_mem, channels*overlap) ||
      !write_i32_array(prefilter_mem, channels*max_period) ||
      !write_i32_array(oldBandE, channels*bands) ||
      !write_i32_array(oldLogE, channels*bands) ||
      !write_i32_array(oldLogE2, channels*bands) ||
      !write_i32_array(energyError, channels*bands) ||
      !write_i32_array(qextOldBandE, channels*NB_QEXT_BANDS)) return 0;
  return 1;
}
#endif

static int read_float(float *out) {
  uint32_t bits;
  if (!read_u32(&bits)) return 0;
  memcpy(out, &bits, sizeof(bits));
  return 1;
}

static int read_analysis(AnalysisInfo *info) {
  uint32_t v;
  memset(info, 0, sizeof(*info));
  if (!read_u32(&v) || v > 1) return 0;
  info->valid = (int)v;
  if (!read_float(&info->tonality) || !read_float(&info->tonality_slope) ||
      !read_float(&info->noisiness) || !read_float(&info->activity) ||
      !read_float(&info->music_prob) || !read_float(&info->music_prob_min) ||
      !read_float(&info->music_prob_max) || !read_u32(&v)) return 0;
  info->bandwidth = (int32_t)v;
  if (!read_float(&info->activity_probability) ||
      !read_float(&info->max_pitch_ratio) ||
      fread(info->leak_boost, 1, LEAK_BANDS, stdin) != LEAK_BANDS) return 0;
  return 1;
}

int main(void) {
  unsigned char magic[4];
  uint32_t version, channels, stream_channels, frame_size, start, end, bitrate, complexity;
  uint32_t sample_rate, vbr, cvbr, lfe, lsb_depth, qext_enabled = 0, count, f;
  CELTEncoder *st = NULL;
  opus_res *pcm = NULL;
  unsigned char packet_storage[4002];
  unsigned char *packet = packet_storage + 1;
  celt_glog mask[2 * 21];
  const int rates[] = {8000, 12000, 16000, 24000, 48000};
  int valid_rate = 0, core_size, i, max_frame_size = 960;
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 1;
#endif
  if (fread(magic, 1, 4, stdin) != 4 || memcmp(magic, "GQRI", 4) ||
      !read_u32(&version) || (version != 3 && version != 4 && version != 5) || !read_u32(&channels) ||
      !read_u32(&stream_channels) ||
      !read_u32(&frame_size) || !read_u32(&start) || !read_u32(&end) ||
      !read_u32(&bitrate) || !read_u32(&complexity) ||
      !read_u32(&sample_rate) || !read_u32(&vbr) || !read_u32(&cvbr) ||
      !read_u32(&lfe) || !read_u32(&lsb_depth)) return 1;
  if (version >= 4 && (!read_u32(&qext_enabled) || qext_enabled > 1)) return 1;
  if (!read_u32(&count)) return 1;
  for (i = 0; i < (int)(sizeof(rates)/sizeof(rates[0])); i++) {
    if (sample_rate == (uint32_t)rates[i]) valid_rate = 1;
  }
#ifdef ENABLE_QEXT
  if (sample_rate == 96000 && version >= 4) {
    valid_rate = 1;
    max_frame_size = 1920;
  }
#endif
  if (!valid_rate || channels < 1 || channels > 2 || stream_channels < 1 ||
      stream_channels > channels || frame_size < 20 ||
      frame_size > (uint32_t)max_frame_size ||
      start >= end || end > 21 ||
      complexity > 10 || vbr > 1 || cvbr > 1 || lfe > 1 ||
      lsb_depth < 8 || lsb_depth > 24 || count < 1 || count > 256) return 1;
#ifdef ENABLE_QEXT
  if (sample_rate == 96000) core_size = (int)frame_size;
  else
#endif
  {
    if ((frame_size*48000)%sample_rate != 0) return 1;
    core_size = (int)(frame_size*48000/sample_rate);
  }
  if (core_size != 120 && core_size != 240 &&
      core_size != 480 && core_size != 960 &&
      !(sample_rate == 96000 && core_size == 1920)) return 1;
  pcm = (opus_res *)malloc((size_t)channels * frame_size * sizeof(*pcm));
  st = (CELTEncoder *)calloc(1, celt_encoder_get_size((int)channels));
  if (!pcm || !st) return 1;
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
      (qext_enabled && celt_encoder_ctl(st, OPUS_SET_QEXT(qext_enabled)) != OPUS_OK) ||
      celt_encoder_ctl(st, CELT_SET_SIGNALLING_REQUEST, 0) != OPUS_OK) return 1;
#ifdef GOPUS_STATE_TRACE
  if (fwrite("GQSO", 1, 4, stdout) != 4 || !write_u32(1) || !write_u32(count)) return 1;
#else
  if (fwrite("GQRO", 1, 4, stdout) != 4 || !write_u32(1) || !write_u32(count)) return 1;
#endif
  for (f = 0; f < count; f++) {
    uint32_t max_bytes, samples, j, raw, prefix_count, set_prediction, has_mask;
    uint32_t reset_before, prediction, signal_type, silk_offset;
    uint32_t frame_stream_channels = 0;
    int32_t frame_bitrate = 0;
    uint32_t max_frame_bytes = qext_enabled ? 4000 : 1275;
    AnalysisInfo analysis;
    SILKInfo silk_info;
    ec_enc ec;
    int ret;
    if (!read_u32(&max_bytes) || !read_u32(&samples) ||
        max_bytes < 2 || max_bytes > max_frame_bytes ||
        samples != channels*frame_size) return 1;
    if (version >= 5) {
      if (!read_u32(&frame_stream_channels) ||
          (frame_stream_channels != 0 &&
           (frame_stream_channels < 1 || frame_stream_channels > channels)) ||
          !read_u32(&raw)) return 1;
      frame_bitrate = (int32_t)raw;
      if (frame_bitrate > 1500000 || frame_bitrate < -1) return 1;
    }
    memset(packet_storage, 0, sizeof(packet_storage));
    for (j = 0; j < samples; j++) {
      if (!read_u32(&raw)) return 1;
      pcm[j] = (opus_res)(int32_t)raw;
    }
    if (!read_u32(&prefix_count) || prefix_count > 64) return 1;
    ec_enc_init(&ec, packet, (int)max_bytes);
    for (j = 0; j < prefix_count; j++) {
      if (!read_u32(&raw) || raw >= 256) return 1;
      ec_enc_uint(&ec, raw, 256);
    }
    if (!read_u32(&reset_before) || reset_before > 1 ||
        !read_u32(&set_prediction) || set_prediction > 1 ||
        !read_u32(&prediction) || prediction > 2 ||
        !read_u32(&signal_type) || !read_u32(&silk_offset)) return 1;
    if (!read_analysis(&analysis) || !read_u32(&has_mask) || has_mask > 1) return 1;
    if (has_mask) {
      for (j = 0; j < channels*21; j++) {
        if (!read_u32(&raw)) return 1;
        mask[j] = (celt_glog)(int32_t)raw;
      }
    }
    if (reset_before && celt_encoder_ctl(st, OPUS_RESET_STATE) != OPUS_OK) return 1;
    if ((frame_stream_channels != 0 &&
         celt_encoder_ctl(st, CELT_SET_CHANNELS(frame_stream_channels)) != OPUS_OK) ||
        (frame_bitrate != 0 &&
         celt_encoder_ctl(st, OPUS_SET_BITRATE_REQUEST, frame_bitrate) != OPUS_OK)) return 1;
    silk_info.signalType = (opus_int32)signal_type;
    silk_info.offset = (opus_int32)silk_offset;
    if ((set_prediction && celt_encoder_ctl(st, CELT_SET_PREDICTION(prediction)) != OPUS_OK) ||
        celt_encoder_ctl(st, CELT_SET_SILK_INFO(&silk_info)) != OPUS_OK ||
        celt_encoder_ctl(st, CELT_SET_ANALYSIS(&analysis)) != OPUS_OK ||
        (has_mask && celt_encoder_ctl(st, OPUS_SET_ENERGY_MASK_REQUEST, mask) != OPUS_OK)) return 1;
    ret = celt_encode_with_ec(st, pcm, (int)frame_size, packet, (int)max_bytes, &ec);
    if (ret < 0 || ret > (int)max_bytes || !write_u32((uint32_t)ret)) return 1;
#ifdef GOPUS_STATE_TRACE
    if (!write_u32(((struct OpusCustomEncoder *)st)->rng)) return 1;
#else
    if (!write_u32(ec.rng)) return 1;
#endif
    if (fwrite(packet, 1, (size_t)ret, stdout) != (size_t)ret) return 1;
#ifdef GOPUS_STATE_TRACE
    if (!write_encoder_state(st)) return 1;
#endif
  }
  free(st);
  free(pcm);
  return 0;
}
