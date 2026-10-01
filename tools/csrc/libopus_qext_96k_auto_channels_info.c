/* Selected float ENABLE_QEXT public 96 kHz auto-channel encode oracle. */

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#if defined(FIXED_POINT) || !defined(ENABLE_QEXT)
#error "auto-channel oracle requires float ENABLE_QEXT libopus"
#endif

#include "opus.h"
#include "opus_private.h"
#include "celt/celt.h"
#include "celt/entenc.h"

#ifdef GOPUS_STATE_TRACE
static int opus_custom_encoder_get_size(const CELTMode *mode, int channels);
#include "celt/pitch.h"
static void trace_pitch_downsample(celt_sig * OPUS_RESTRICT x[],
    opus_val16 * OPUS_RESTRICT x_lp, int len, int channels, int factor, int arch);
static void trace_pitch_search(const opus_val16 * OPUS_RESTRICT x_lp,
    opus_val16 * OPUS_RESTRICT y, int len, int max_pitch, int *pitch, int arch);
static opus_val16 trace_remove_doubling(opus_val16 *x, int maxperiod,
    int minperiod, int n, int *t0, int prev_period, opus_val16 prev_gain, int arch);
#define pitch_downsample trace_pitch_downsample
#define pitch_search trace_pitch_search
#define remove_doubling trace_remove_doubling
#include "celt_encoder.c"
#undef pitch_downsample
#undef pitch_search
#undef remove_doubling
static int write_u32(uint32_t value);
#endif

#define MAX_FRAMES 32
#define MAX_PACKET_BYTES 4000
#define CHANNELS 2
#define MAX_CELT_TRACE_SAMPLES 3840
#define MAX_CELT_STATE_WORDS 12000
#define MAX_PITCH_TRACE_SAMPLES 2048

typedef struct {
  uint32_t calls;
  uint32_t frame_size;
  uint32_t sample_count;
  uint32_t stream_channels;
  int32_t bitrate;
  int32_t lsb_depth;
  uint32_t max_bytes;
  float pcm[MAX_CELT_TRACE_SAMPLES];
  uint32_t state_word_count;
  uint32_t state_words[MAX_CELT_STATE_WORDS];
  uint32_t pitch_buffer_count;
  float pitch_buffer[MAX_PITCH_TRACE_SAMPLES];
  int32_t pitch_search_result;
  int32_t remove_input_period;
  int32_t remove_output_period;
  float remove_gain;
} CELTFrameTrace;

static OpusEncoder *trace_encoder;
static uint32_t trace_frame;
static CELTFrameTrace celt_trace[MAX_FRAMES];
static int capture_float_celt_frame(CELTEncoder *st, const opus_res *pcm,
    int frame_size, unsigned char *compressed, int nbCompressedBytes, ec_enc *enc);

#ifdef GOPUS_STATE_TRACE
static void trace_pitch_downsample(celt_sig * OPUS_RESTRICT x[],
    opus_val16 * OPUS_RESTRICT x_lp, int len, int channels, int factor, int arch) {
  pitch_downsample(x, x_lp, len, channels, factor, arch);
  if (trace_encoder != NULL && trace_frame < MAX_FRAMES && len > 0 && len <= MAX_PITCH_TRACE_SAMPLES) {
    CELTFrameTrace *trace = &celt_trace[trace_frame];
    trace->pitch_buffer_count = (uint32_t)len;
    for (int i = 0; i < len; i++) trace->pitch_buffer[i] = (float)x_lp[i];
  }
}

static void trace_pitch_search(const opus_val16 * OPUS_RESTRICT x_lp,
    opus_val16 * OPUS_RESTRICT y, int len, int max_pitch, int *pitch, int arch) {
  pitch_search(x_lp, y, len, max_pitch, pitch, arch);
  if (trace_encoder != NULL && trace_frame < MAX_FRAMES) {
    celt_trace[trace_frame].pitch_search_result = (int32_t)*pitch;
  }
}

static opus_val16 trace_remove_doubling(opus_val16 *x, int maxperiod,
    int minperiod, int n, int *t0, int prev_period, opus_val16 prev_gain, int arch) {
  int32_t input_period = (int32_t)*t0;
  opus_val16 gain = remove_doubling(x, maxperiod, minperiod, n, t0,
      prev_period, prev_gain, arch);
  if (trace_encoder != NULL && trace_frame < MAX_FRAMES) {
    CELTFrameTrace *trace = &celt_trace[trace_frame];
    trace->remove_input_period = input_period;
    trace->remove_output_period = (int32_t)*t0;
    trace->remove_gain = (float)gain;
  }
  return gain;
}
#endif

#define celt_encode_with_ec capture_float_celt_frame
#include "src/opus_encoder.c"
#undef celt_encode_with_ec

#ifdef GOPUS_STATE_TRACE
static int append_state_word(CELTFrameTrace *trace, uint32_t value) {
  if (trace->state_word_count >= MAX_CELT_STATE_WORDS) return 0;
  trace->state_words[trace->state_word_count++] = value;
  return 1;
}

static int append_state_float(CELTFrameTrace *trace, float value) {
  uint32_t bits;
  memcpy(&bits, &value, sizeof(bits));
  return append_state_word(trace, bits);
}

static int append_state_float_array(CELTFrameTrace *trace, const float *values, int count) {
  int i;
  if (count < 0 || !append_state_word(trace, (uint32_t)count)) return 0;
  for (i = 0; i < count; i++) {
    if (!append_state_float(trace, values[i])) return 0;
  }
  return 1;
}

static int capture_celt_state(CELTEncoder *opaque, CELTFrameTrace *trace) {
  struct OpusCustomEncoder *st = (struct OpusCustomEncoder *)opaque;
  int channels = st->channels;
  int overlap = st->mode->overlap;
  int bands = st->mode->nbEBands;
  int max_period = QEXT_SCALE2(COMBFILTER_MAXPERIOD, st->qext_scale);
  celt_sig *prefilter_mem = st->in_mem + channels * overlap;
  celt_glog *old_band_e = (celt_glog *)(prefilter_mem + channels * max_period);
  celt_glog *old_log_e = old_band_e + channels * bands;
  celt_glog *old_log_e2 = old_log_e + channels * bands;
  celt_glog *energy_error = old_log_e2 + channels * bands;
  celt_glog *qext_old_band_e = energy_error + channels * bands;
  uint32_t scalars[35] = {
    st->rng, (uint32_t)st->spread_decision, 0, (uint32_t)st->tonal_average,
    (uint32_t)st->lastCodedBands, (uint32_t)st->hf_average,
    (uint32_t)st->tapset_decision, (uint32_t)st->prefilter_period, 0,
    (uint32_t)st->prefilter_tapset, (uint32_t)st->consec_transient,
    (uint32_t)st->vbr_reservoir, (uint32_t)st->vbr_drift,
    (uint32_t)st->vbr_offset, (uint32_t)st->vbr_count, 0, 0,
    (uint32_t)st->intensity, 0, (uint32_t)st->force_intra,
    (uint32_t)st->disable_pf, (uint32_t)st->silk_info.signalType,
    (uint32_t)st->silk_info.offset, (uint32_t)st->analysis.valid,
    (uint32_t)st->analysis.bandwidth, (uint32_t)st->bitrate,
    (uint32_t)st->stream_channels, (uint32_t)st->enable_qext,
    (uint32_t)st->qext_scale, (uint32_t)st->lsb_depth, (uint32_t)st->start,
    (uint32_t)st->end, (uint32_t)st->vbr, (uint32_t)st->constrained_vbr,
    (uint32_t)st->loss_rate
  };
  memcpy(&scalars[2], &st->delayedIntra, sizeof(uint32_t));
  memcpy(&scalars[8], &st->prefilter_gain, sizeof(uint32_t));
  memcpy(&scalars[15], &st->overlap_max, sizeof(uint32_t));
  memcpy(&scalars[16], &st->stereo_saving, sizeof(uint32_t));
  memcpy(&scalars[18], &st->spec_avg, sizeof(uint32_t));
  trace->state_word_count = 0;
  if (!append_state_word(trace, (uint32_t)(sizeof(scalars) / sizeof(scalars[0])))) return 0;
  for (int i = 0; i < (int)(sizeof(scalars) / sizeof(scalars[0])); i++) {
    if (!append_state_word(trace, scalars[i])) return 0;
  }
  return append_state_float_array(trace, (const float *)st->preemph_memE, channels) &&
      append_state_float_array(trace, (const float *)st->in_mem, channels * overlap) &&
      append_state_float_array(trace, (const float *)prefilter_mem, channels * max_period) &&
      append_state_float_array(trace, (const float *)old_band_e, channels * bands) &&
      append_state_float_array(trace, (const float *)old_log_e, channels * bands) &&
      append_state_float_array(trace, (const float *)old_log_e2, channels * bands) &&
      append_state_float_array(trace, (const float *)energy_error, channels * bands) &&
      append_state_float_array(trace, (const float *)qext_old_band_e, channels * NB_QEXT_BANDS);
}
#endif

static int capture_float_celt_frame(CELTEncoder *st, const opus_res *pcm,
    int frame_size, unsigned char *compressed, int nbCompressedBytes, ec_enc *enc) {
  if (trace_encoder != NULL && enc != NULL && trace_frame < MAX_FRAMES) {
    CELTFrameTrace *trace = &celt_trace[trace_frame];
    opus_int32 celt_lsb_depth = 0;
    uint32_t samples = (uint32_t)(frame_size * trace_encoder->channels);
    if (celt_encoder_ctl(st, OPUS_GET_LSB_DEPTH(&celt_lsb_depth)) != OPUS_OK) return -1;
    trace->calls++;
    trace->frame_size = (uint32_t)frame_size;
    trace->sample_count = samples;
    trace->stream_channels = (uint32_t)trace_encoder->stream_channels;
    trace->bitrate = trace_encoder->bitrate_bps;
    trace->lsb_depth = celt_lsb_depth;
    trace->max_bytes = (uint32_t)nbCompressedBytes;
    if (samples <= MAX_CELT_TRACE_SAMPLES) {
      uint32_t i;
      for (i = 0; i < samples; i++) trace->pcm[i] = (float)pcm[i];
    }
  }
  int result = celt_encode_with_ec(st, pcm, frame_size, compressed, nbCompressedBytes, enc);
#ifdef GOPUS_STATE_TRACE
  if (trace_encoder != NULL && trace_frame < MAX_FRAMES && result >= 0) {
    if (!capture_celt_state(st, &celt_trace[trace_frame])) return OPUS_INTERNAL_ERROR;
  }
#endif
  return result;
}

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

static int read_exact(void *dst, size_t n) {
  return fread(dst, 1, n, stdin) == n;
}

static int write_exact(const void *src, size_t n) {
  size_t off = 0;
  const unsigned char *p = (const unsigned char *)src;
  while (off < n) {
    size_t written = fwrite(p + off, 1, n - off, stdout);
    if (written == 0) return 0;
    off += written;
  }
  return 1;
}

static int read_u32(uint32_t *value) {
  unsigned char b[4];
  if (!read_exact(b, sizeof(b))) return 0;
  *value = (uint32_t)b[0] | ((uint32_t)b[1] << 8) |
           ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int write_u32(uint32_t value) {
  unsigned char b[4];
  b[0] = (unsigned char)value;
  b[1] = (unsigned char)(value >> 8);
  b[2] = (unsigned char)(value >> 16);
  b[3] = (unsigned char)(value >> 24);
  return write_exact(b, sizeof(b));
}

int main(void) {
  if (!set_binary_stdio()) return 1;
  unsigned char magic[4];
  uint32_t version, frame_size, frame_count, max_packet, complexity, lsb_depth, qext;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, "GQAI", 4) != 0 ||
      !read_u32(&version) || version != 1 || !read_u32(&frame_size) ||
      !read_u32(&frame_count) || !read_u32(&max_packet) ||
      !read_u32(&complexity) || !read_u32(&lsb_depth) || !read_u32(&qext)) return 1;
  if ((frame_size != 240 && frame_size != 480 && frame_size != 960 && frame_size != 1920) ||
      frame_count == 0 || frame_count > MAX_FRAMES || max_packet == 0 ||
      max_packet > MAX_PACKET_BYTES || complexity > 10 || lsb_depth < 8 ||
      lsb_depth > 24 || qext > 1) return 1;

  size_t samples = (size_t)frame_size * CHANNELS;
  float *pcm = (float *)malloc(samples * sizeof(*pcm));
  unsigned char *packet = (unsigned char *)malloc(max_packet);
  unsigned char **packets = (unsigned char **)calloc(frame_count, sizeof(*packets));
  uint32_t *lengths = (uint32_t *)calloc(frame_count, sizeof(*lengths));
  uint32_t *ranges = (uint32_t *)calloc(frame_count, sizeof(*ranges));
  int32_t *statuses = (int32_t *)calloc(frame_count, sizeof(*statuses));
  if (pcm == NULL || packet == NULL || packets == NULL || lengths == NULL ||
      ranges == NULL || statuses == NULL) return 1;

  int error = OPUS_OK;
  OpusEncoder *enc = opus_encoder_create(96000, CHANNELS, OPUS_APPLICATION_AUDIO, &error);
  if (enc == NULL || error != OPUS_OK) goto fail;
  if (opus_encoder_ctl(enc, OPUS_SET_FORCE_MODE(MODE_CELT_ONLY)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_BANDWIDTH(OPUS_BANDWIDTH_FULLBAND)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_MAX_BANDWIDTH(OPUS_BANDWIDTH_FULLBAND)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_COMPLEXITY((opus_int32)complexity)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_LSB_DEPTH((opus_int32)lsb_depth)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_VBR(1)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_VBR_CONSTRAINT(0)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_QEXT((opus_int32)qext)) != OPUS_OK) goto fail;

  for (uint32_t f = 0; f < frame_count; f++) {
    uint32_t bitrate;
    if (!read_u32(&bitrate) || bitrate == 0 || bitrate > 1500000) goto fail;
    for (size_t i = 0; i < samples; i++) {
      uint32_t bits;
      if (!read_u32(&bits)) goto fail;
      memcpy(&pcm[i], &bits, sizeof(bits));
    }
    if (opus_encoder_ctl(enc, OPUS_SET_BITRATE((opus_int32)bitrate)) != OPUS_OK ||
        opus_encoder_ctl(enc, OPUS_SET_FORCE_MODE(MODE_CELT_ONLY)) != OPUS_OK) goto fail;
    trace_encoder = enc;
    trace_frame = f;
    int n = opus_encode_float(enc, pcm, (int)frame_size, packet, (int)max_packet);
    trace_encoder = NULL;
    statuses[f] = n < 0 ? n : OPUS_OK;
    if (n > 0) {
      packets[f] = (unsigned char *)malloc((size_t)n);
      if (packets[f] == NULL) goto fail;
      memcpy(packets[f], packet, (size_t)n);
      lengths[f] = (uint32_t)n;
      if (opus_encoder_ctl(enc, OPUS_GET_FINAL_RANGE(&ranges[f])) != OPUS_OK) goto fail;
    }
  }

  if (!write_exact("GQAO", 4) || !write_u32(
#ifdef GOPUS_STATE_TRACE
      4
#else
      2
#endif
      ) || !write_u32(frame_count)) goto fail;
  for (uint32_t f = 0; f < frame_count; f++) {
    CELTFrameTrace *trace = &celt_trace[f];
    if (!write_u32((uint32_t)statuses[f]) || !write_u32(lengths[f]) ||
        !write_u32(ranges[f]) || !write_exact(packets[f], lengths[f])) goto fail;
    unsigned char zero[3] = {0, 0, 0};
    size_t pad = (4 - (lengths[f] & 3)) & 3;
    if (pad && !write_exact(zero, pad)) goto fail;
    if (!write_u32(trace->calls) || !write_u32(trace->frame_size) ||
        !write_u32(trace->sample_count) || !write_u32(trace->stream_channels) ||
        !write_u32((uint32_t)trace->bitrate) || !write_u32((uint32_t)trace->lsb_depth) ||
        !write_u32(trace->max_bytes)) goto fail;
    for (uint32_t i = 0; i < trace->sample_count; i++) {
      uint32_t bits;
      memcpy(&bits, &trace->pcm[i], sizeof(bits));
      if (!write_u32(bits)) goto fail;
    }
#ifdef GOPUS_STATE_TRACE
    for (uint32_t i = 0; i < trace->state_word_count; i++) {
      if (!write_u32(trace->state_words[i])) goto fail;
    }
    if (!write_u32(trace->pitch_buffer_count)) goto fail;
    for (uint32_t i = 0; i < trace->pitch_buffer_count; i++) {
      uint32_t bits;
      memcpy(&bits, &trace->pitch_buffer[i], sizeof(bits));
      if (!write_u32(bits)) goto fail;
    }
    uint32_t remove_gain_bits;
    memcpy(&remove_gain_bits, &trace->remove_gain, sizeof(remove_gain_bits));
    if (!write_u32((uint32_t)trace->pitch_search_result) ||
        !write_u32((uint32_t)trace->remove_input_period) ||
        !write_u32((uint32_t)trace->remove_output_period) ||
        !write_u32(remove_gain_bits)) goto fail;
#endif
  }
  for (uint32_t f = 0; f < frame_count; f++) free(packets[f]);
  opus_encoder_destroy(enc);
  free(pcm); free(packet); free(packets); free(lengths); free(ranges); free(statuses);
  return 0;

fail:
  if (enc != NULL) opus_encoder_destroy(enc);
  for (uint32_t f = 0; f < frame_count; f++) free(packets[f]);
  free(pcm); free(packet); free(packets); free(lengths); free(ranges); free(statuses);
  return 1;
}
