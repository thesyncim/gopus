/*
 * Frame-scoped CELT stage and entropy trace for a two-frame public encoder
 * differential. Keep this as a wrapper around the ordinary GEDI helper so the
 * input controls, stateful frame loop, packet records, and final ranges use the
 * same public API path as libopus_encode_diff_info.c.
 */

#include "config.h"
#include "celt/celt.h"
#include "celt/bands.h"
#include "celt/mdct.h"
#include "celt/quant_bands.h"
#include "celt/entenc.h"
#include <stdint.h>
#include <stdio.h>
#include <string.h>

#define main gopus_encode_diff_main
#include "libopus_encode_diff_info.c"
#undef main

#define CELT_TRACE_MAX_CALLS 8
/* celt_encoder.c secondMdct emits two long channel transforms before the
 * sixteen short transforms of a stereo LM=3 transient frame. */
#define CELT_TRACE_MAX_MDCT_CALLS 18
#define CELT_TRACE_MAX_FLOATS 4096
#define CELT_TRACE_MAX_BANDS 64
#define CELT_TRACE_MAX_HISTORY 1024
#define ENTROPY_TRACE_MAX_RAW_CALLS 4096
#define ENTROPY_TRACE_MAX_DONE_CALLS 4
#define TRACE_FRAME 1u

typedef struct {
  uint32_t frame_coeffs, bands, channels, lm;
  float spectrum[CELT_TRACE_MAX_FLOATS];
  float amplitudes[CELT_TRACE_MAX_BANDS * 2];
} celt_trace_band_call;

typedef struct {
  uint32_t bands, channels;
  float amplitudes[CELT_TRACE_MAX_BANDS * 2];
  float log_energy[CELT_TRACE_MAX_BANDS * 2];
} celt_trace_log_call;

typedef struct {
  uint32_t active_coeffs, bands, channels;
  float band_energy[CELT_TRACE_MAX_BANDS * 2];
  float normalized[CELT_TRACE_MAX_FLOATS];
} celt_trace_normalization_call;

typedef struct {
  uint32_t bands, channels, budget_bytes;
  float input[CELT_TRACE_MAX_BANDS * 2];
  float quantized[CELT_TRACE_MAX_BANDS * 2];
  float error[CELT_TRACE_MAX_BANDS * 2];
} celt_trace_coarse_call;

typedef struct {
  uint32_t active_coeffs, bands, channels;
  float band_energy[CELT_TRACE_MAX_BANDS * 2];
  float input[CELT_TRACE_MAX_FLOATS];
  float output[CELT_TRACE_MAX_FLOATS];
} celt_trace_quant_call;

typedef struct {
  uint32_t lookup_n, maxshift, transform_n, shift, stride, overlap, arch;
  uint32_t fft_n, input_count, window_count, trig_count;
  float fft_scale;
  float input[CELT_TRACE_MAX_FLOATS];
  float window[CELT_TRACE_MAX_FLOATS];
  float trig[CELT_TRACE_MAX_FLOATS];
} celt_trace_mdct_call;

typedef struct {
  uint32_t channel, channels, frame_size, upsample;
  uint32_t input_count, output_count, flags;
  float state_before, state_after;
  float coefficients[4];
  float input[CELT_TRACE_MAX_FLOATS];
  float output[CELT_TRACE_MAX_FLOATS];
} celt_trace_preemphasis_call;

typedef struct {
  uint32_t t0, t1, n, tapset0, tapset1, overlap, arch, window_nil;
  uint32_t history_count, input_count, window_count, output_count;
  float gain0, gain1;
  float history[CELT_TRACE_MAX_HISTORY];
  float input[CELT_TRACE_MAX_FLOATS];
  float window[CELT_TRACE_MAX_FLOATS];
  float output[CELT_TRACE_MAX_FLOATS];
} celt_trace_prefilter_call;

static struct {
  uint32_t band_calls, log_calls, normalization_calls, coarse_calls, quant_calls, mdct_calls;
  uint32_t preemphasis_calls, prefilter_calls;
  uint32_t stored_band_calls, stored_log_calls, stored_normalization_calls;
  uint32_t stored_coarse_calls, stored_quant_calls, stored_mdct_calls;
  uint32_t stored_preemphasis_calls, stored_prefilter_calls, overflow;
  uint32_t captured_frame;
  celt_trace_band_call bands[CELT_TRACE_MAX_CALLS];
  celt_trace_log_call logs[CELT_TRACE_MAX_CALLS];
  celt_trace_normalization_call normalizations[CELT_TRACE_MAX_CALLS];
  celt_trace_coarse_call coarse[CELT_TRACE_MAX_CALLS];
  celt_trace_quant_call quant[CELT_TRACE_MAX_CALLS];
  celt_trace_mdct_call mdct[CELT_TRACE_MAX_MDCT_CALLS];
  celt_trace_preemphasis_call preemphasis[CELT_TRACE_MAX_CALLS];
  celt_trace_prefilter_call prefilter[CELT_TRACE_MAX_CALLS];
} celt_encode_trace = { .captured_frame = UINT32_MAX };

typedef struct {
  uint32_t storage, offs, end_offs, end_window;
  int32_t nend_bits, nbits_total;
  uint32_t rng, val, ext;
  int32_t rem, error;
} entropy_context_snapshot;

typedef struct {
  uint32_t fl, bits;
  entropy_context_snapshot before, after;
} entropy_raw_call;

#ifdef GOPUS_CELT_CODER_RANGE_TRACE
enum {
  CELT_RANGE_BEFORE_COARSE = 1,
  CELT_RANGE_AFTER_COARSE = 2,
  CELT_RANGE_BEFORE_QUANT = 3
};

typedef struct {
  uint32_t stage, range, tell_frac;
} celt_coder_range_point;

typedef struct {
  uint32_t coarse_calls, quant_calls, count, overflow;
  ec_enc *coarse_encoder, *quant_encoder;
  celt_coder_range_point points[3];
} celt_coder_range_trace_state;

static celt_coder_range_trace_state celt_coder_range_trace;

static void capture_coder_range(uint32_t stage, ec_enc *enc) {
  if (enc == NULL || celt_coder_range_trace.count >= 3) {
    celt_coder_range_trace.overflow = 1;
    return;
  }
  celt_coder_range_point *point = &celt_coder_range_trace.points[celt_coder_range_trace.count++];
  point->stage = stage;
  point->range = enc->rng;
  point->tell_frac = (uint32_t)ec_tell_frac(enc);
}
#endif

#ifdef GOPUS_CELT_TF_TRACE
#define CELT_TF_TRACE_MAX_BITS 64u
typedef struct {
  uint32_t ordinal, symbol, logp;
  uint32_t range_before, tell_frac_before, range_after, tell_frac_after;
  uint32_t tell_before, tell_after;
} celt_tf_bit_trace;

typedef struct {
  uint32_t active, overflow, coarse_calls, bit_calls, stored_bits;
  uint32_t foreign_calls, icdf_calls, spread_calls, spread_table_match;
  uint32_t same_coder, quant_calls, start, end, lm, storage_bits, transient;
  int32_t entry_tell;
  uint32_t select_encoded, select_symbol, post_count;
  uint32_t spread_symbol, spread_logp;
  uint32_t spread_range_before, spread_tell_frac_before;
  uint32_t spread_range_after, spread_tell_frac_after;
  ec_enc *encoder, *quant_encoder;
  int32_t post_tf_res[CELT_TRACE_MAX_BANDS];
  celt_tf_bit_trace bits[CELT_TF_TRACE_MAX_BITS];
} celt_tf_trace_state;

static celt_tf_trace_state celt_tf_trace;

static int celt_tf_spread_table_match(int symbol, const unsigned char *icdf, unsigned ftb) {
  static const unsigned char expected[4] = {25, 23, 2, 0};
  if (icdf == NULL || ftb != 5 || symbol < 0 || symbol >= 4) return 0;
  /* Check only entries ec_enc_icdf reads for this symbol. */
  if (icdf[symbol] != expected[symbol]) return 0;
  return symbol == 0 || icdf[symbol - 1] == expected[symbol - 1];
}
#endif

static struct {
  uint32_t raw_calls, stored_raw_calls;
  uint32_t done_calls, stored_done_calls;
  uint32_t overflow;
  entropy_context_snapshot done_before[ENTROPY_TRACE_MAX_DONE_CALLS];
  entropy_context_snapshot done_after[ENTROPY_TRACE_MAX_DONE_CALLS];
  entropy_raw_call raw[ENTROPY_TRACE_MAX_RAW_CALLS];
} entropy_trace;

static uint32_t active_frame = UINT32_MAX;
static uint32_t next_frame;

static int trace_selected_frame(void) {
  if (active_frame != TRACE_FRAME) return 0;
  if (celt_encode_trace.captured_frame == UINT32_MAX)
    celt_encode_trace.captured_frame = active_frame;
  return 1;
}

static void snapshot_context(entropy_context_snapshot *dst, const ec_enc *enc) {
  dst->storage = enc->storage;
  dst->offs = enc->offs;
  dst->end_offs = enc->end_offs;
  dst->end_window = enc->end_window;
  dst->nend_bits = (int32_t)enc->nend_bits;
  dst->nbits_total = (int32_t)enc->nbits_total;
  dst->rng = enc->rng;
  dst->val = enc->val;
  dst->ext = enc->ext;
  dst->rem = (int32_t)enc->rem;
  dst->error = (int32_t)enc->error;
}

static int trace_dimensions(int count, int limit) {
  if (count < 0 || count > limit) {
    celt_encode_trace.overflow = 1;
    return 0;
  }
  return 1;
}

extern void __real_clt_mdct_forward_c(const mdct_lookup *l, kiss_fft_scalar *in,
    kiss_fft_scalar *out, const celt_coef *window, int overlap, int shift,
    int stride, int arch);
void __wrap_clt_mdct_forward_c(const mdct_lookup *l, kiss_fft_scalar *in,
    kiss_fft_scalar *out, const celt_coef *window, int overlap, int shift,
    int stride, int arch) {
  if (!trace_selected_frame()) {
    __real_clt_mdct_forward_c(l, in, out, window, overlap, shift, stride, arch);
    return;
  }
  uint32_t call = celt_encode_trace.mdct_calls++;
  if (call >= CELT_TRACE_MAX_MDCT_CALLS || l == NULL || shift < 0 || shift >= 4 ||
      shift > l->maxshift || l->n <= 0 || l->kfft[shift] == NULL ||
      overlap < 0 || in == NULL || (overlap > 0 && window == NULL) || l->trig == NULL) {
    celt_encode_trace.overflow = 1;
  } else {
    int n = l->n;
    const kiss_twiddle_scalar *trig = l->trig;
    for (int i = 0; i < shift; i++) {
      n >>= 1;
      trig += n;
    }
    int n2 = n >> 1;
    int input_count = n2 + overlap;
    if (n <= 0 || n2 <= 0 || !trace_dimensions(input_count, CELT_TRACE_MAX_FLOATS) ||
        !trace_dimensions(overlap, CELT_TRACE_MAX_FLOATS) ||
        !trace_dimensions(n2, CELT_TRACE_MAX_FLOATS)) {
      celt_encode_trace.overflow = 1;
    } else {
      celt_trace_mdct_call *trace = &celt_encode_trace.mdct[call];
      trace->lookup_n = (uint32_t)l->n;
      trace->maxshift = (uint32_t)l->maxshift;
      trace->transform_n = (uint32_t)n;
      trace->shift = (uint32_t)shift;
      trace->stride = (uint32_t)stride;
      trace->overlap = (uint32_t)overlap;
      trace->arch = (uint32_t)arch;
      trace->fft_n = (uint32_t)l->kfft[shift]->nfft;
      trace->fft_scale = (float)l->kfft[shift]->scale;
      trace->input_count = (uint32_t)input_count;
      trace->window_count = (uint32_t)overlap;
      trace->trig_count = (uint32_t)n2;
      memcpy(trace->input, in, (size_t)input_count * sizeof(float));
      if (overlap > 0) memcpy(trace->window, window, (size_t)overlap * sizeof(float));
      memcpy(trace->trig, trig, (size_t)n2 * sizeof(float));
      celt_encode_trace.stored_mdct_calls++;
    }
  }
  __real_clt_mdct_forward_c(l, in, out, window, overlap, shift, stride, arch);
}

int gopus_celt_preemphasis_trace_begin(int channel, const opus_res *pcmp,
    celt_sig *inp, int N, int CC, int upsample, const opus_val16 *coef,
    celt_sig *mem, int clip) {
  if (!trace_selected_frame()) return -1;
  uint32_t call = celt_encode_trace.preemphasis_calls++;
  celt_trace_preemphasis_call *trace;
  int input_count = upsample > 0 ? N / upsample : -1;
  if (call >= CELT_TRACE_MAX_CALLS || call != celt_encode_trace.stored_preemphasis_calls ||
      call >= (uint32_t)CC || channel < 0 || channel >= CC || pcmp == NULL ||
      inp == NULL || mem == NULL || coef == NULL || N < 0 || CC <= 0 ||
      CC > 2 || upsample <= 0 || input_count < 0 ||
      !trace_dimensions(input_count, CELT_TRACE_MAX_FLOATS) ||
      !trace_dimensions(N, CELT_TRACE_MAX_FLOATS)) {
    celt_encode_trace.overflow = 1;
    return -1;
  }

  trace = &celt_encode_trace.preemphasis[call];
  trace->channel = (uint32_t)channel;
  trace->channels = (uint32_t)CC;
  trace->frame_size = (uint32_t)N;
  trace->upsample = (uint32_t)upsample;
  trace->input_count = (uint32_t)input_count;
  trace->output_count = (uint32_t)N;
  trace->flags = clip ? 1u : 0u;
  trace->state_before = (float)*mem;
  memcpy(trace->coefficients, coef, sizeof(trace->coefficients));
  for (int i = 0; i < input_count; i++) trace->input[i] = (float)pcmp[i * CC];
  celt_encode_trace.stored_preemphasis_calls++;
  return (int)call;
}

void gopus_celt_preemphasis_trace_end(int call, const celt_sig *inp,
    int N, const celt_sig *mem) {
  if (call < 0) return;
  if ((uint32_t)call >= CELT_TRACE_MAX_CALLS ||
      (uint32_t)call >= celt_encode_trace.stored_preemphasis_calls ||
      inp == NULL || mem == NULL || N < 0 ||
      !trace_dimensions(N, CELT_TRACE_MAX_FLOATS)) {
    celt_encode_trace.overflow = 1;
    return;
  }
  celt_trace_preemphasis_call *trace = &celt_encode_trace.preemphasis[call];
  trace->state_after = (float)*mem;
  memcpy(trace->output, inp, (size_t)N * sizeof(float));
}

extern void __real_comb_filter(opus_val32 *y, opus_val32 *x, int T0, int T1, int N,
    opus_val16 g0, opus_val16 g1, int tapset0, int tapset1,
    const celt_coef *window, int overlap, int arch);
void __wrap_comb_filter(opus_val32 *y, opus_val32 *x, int T0, int T1, int N,
    opus_val16 g0, opus_val16 g1, int tapset0, int tapset1,
    const celt_coef *window, int overlap, int arch) {
  if (!trace_selected_frame()) {
    __real_comb_filter(y, x, T0, T1, N, g0, g1, tapset0, tapset1, window, overlap, arch);
    return;
  }

  uint32_t call = celt_encode_trace.prefilter_calls++;
  celt_trace_prefilter_call *trace = NULL;
  int window_count = window != NULL ? overlap : 0;
  if (call >= CELT_TRACE_MAX_CALLS || y == NULL || x == NULL || N < 0 || overlap < 0 ||
      !trace_dimensions(N, CELT_TRACE_MAX_FLOATS) ||
      !trace_dimensions(window_count, CELT_TRACE_MAX_FLOATS)) {
    celt_encode_trace.overflow = 1;
  } else {
    trace = &celt_encode_trace.prefilter[call];
    trace->t0 = (uint32_t)T0;
    trace->t1 = (uint32_t)T1;
    trace->n = (uint32_t)N;
    trace->tapset0 = (uint32_t)tapset0;
    trace->tapset1 = (uint32_t)tapset1;
    trace->overlap = (uint32_t)overlap;
    trace->arch = (uint32_t)arch;
    trace->window_nil = window == NULL ? 1u : 0u;
    trace->history_count = CELT_TRACE_MAX_HISTORY;
    trace->input_count = (uint32_t)N;
    trace->window_count = (uint32_t)window_count;
    trace->output_count = (uint32_t)N;
    trace->gain0 = (float)g0;
    trace->gain1 = (float)g1;
    memcpy(trace->history, x - CELT_TRACE_MAX_HISTORY, sizeof(trace->history));
    memcpy(trace->input, x, (size_t)N * sizeof(float));
    if (window_count > 0) memcpy(trace->window, window, (size_t)window_count * sizeof(float));
    celt_encode_trace.stored_prefilter_calls++;
  }

  __real_comb_filter(y, x, T0, T1, N, g0, g1, tapset0, tapset1, window, overlap, arch);

  if (trace != NULL) memcpy(trace->output, y, (size_t)N * sizeof(float));
}

static void trace_copy_bands(float *dst, const float *src, int bands, int channels, int stride) {
  int c;
  if (!trace_dimensions(bands, CELT_TRACE_MAX_BANDS) ||
      !trace_dimensions(channels, 2) || stride < bands) return;
  for (c = 0; c < channels; c++)
    memcpy(dst + c * bands, src + c * stride, (size_t)bands * sizeof(float));
}

static int trace_write_float32(const float *values, uint32_t count) {
  uint32_t i;
  for (i = 0; i < count; i++) {
    uint32_t bits;
    memcpy(&bits, &values[i], sizeof(bits));
    if (!write_u32(bits)) return 0;
  }
  return 1;
}

int __wrap_opus_encode_float(OpusEncoder *st, const float *pcm, int frame_size,
    unsigned char *data, opus_int32 max_data_bytes);
extern int __real_opus_encode_float(OpusEncoder *st, const float *pcm, int frame_size,
    unsigned char *data, opus_int32 max_data_bytes);
int __wrap_opus_encode_float(OpusEncoder *st, const float *pcm, int frame_size,
    unsigned char *data, opus_int32 max_data_bytes) {
  active_frame = next_frame++;
  return __real_opus_encode_float(st, pcm, frame_size, data, max_data_bytes);
}

extern void __real_compute_band_energies(const CELTMode *m, const celt_sig *X,
    celt_ener *bandE, int end, int C, int LM, int arch);
void __wrap_compute_band_energies(const CELTMode *m, const celt_sig *X,
    celt_ener *bandE, int end, int C, int LM, int arch) {
  __real_compute_band_energies(m, X, bandE, end, C, LM, arch);
  if (!trace_selected_frame()) return;
  uint32_t call = celt_encode_trace.band_calls++;
  if (call >= CELT_TRACE_MAX_CALLS) {
    celt_encode_trace.overflow = 1;
    return;
  }
  celt_trace_band_call *trace = &celt_encode_trace.bands[call];
  int frame_coeffs = m->shortMdctSize << LM;
  int spectrum_count = frame_coeffs * C;
  int amplitude_count = end * C;
  if (!trace_dimensions(spectrum_count, CELT_TRACE_MAX_FLOATS) ||
      !trace_dimensions(amplitude_count, CELT_TRACE_MAX_BANDS * 2)) return;
  trace->frame_coeffs = (uint32_t)frame_coeffs;
  trace->bands = (uint32_t)end;
  trace->channels = (uint32_t)C;
  trace->lm = (uint32_t)LM;
  memcpy(trace->spectrum, X, (size_t)spectrum_count * sizeof(float));
  trace_copy_bands(trace->amplitudes, (const float *)bandE, end, C, m->nbEBands);
  celt_encode_trace.stored_band_calls++;
}

extern void __real_amp2Log2(const CELTMode *m, int effEnd, int end,
    celt_ener *bandE, celt_glog *bandLogE, int C);
void __wrap_amp2Log2(const CELTMode *m, int effEnd, int end,
    celt_ener *bandE, celt_glog *bandLogE, int C) {
  __real_amp2Log2(m, effEnd, end, bandE, bandLogE, C);
  if (!trace_selected_frame()) return;
  uint32_t call = celt_encode_trace.log_calls++;
  if (call >= CELT_TRACE_MAX_CALLS) {
    celt_encode_trace.overflow = 1;
    return;
  }
  celt_trace_log_call *trace = &celt_encode_trace.logs[call];
  if (!trace_dimensions(end * C, CELT_TRACE_MAX_BANDS * 2)) return;
  trace->bands = (uint32_t)end;
  trace->channels = (uint32_t)C;
  trace_copy_bands(trace->amplitudes, (const float *)bandE, end, C, m->nbEBands);
  trace_copy_bands(trace->log_energy, (const float *)bandLogE, end, C, m->nbEBands);
  celt_encode_trace.stored_log_calls++;
}

extern void __real_normalise_bands(const CELTMode *m, const celt_sig *freq,
    celt_norm *X, const celt_ener *bandE, int end, int C, int M);
void __wrap_normalise_bands(const CELTMode *m, const celt_sig *freq,
    celt_norm *X, const celt_ener *bandE, int end, int C, int M) {
  __real_normalise_bands(m, freq, X, bandE, end, C, M);
  if (!trace_selected_frame()) return;
  uint32_t call = celt_encode_trace.normalization_calls++;
  if (call >= CELT_TRACE_MAX_CALLS) {
    celt_encode_trace.overflow = 1;
    return;
  }
  celt_trace_normalization_call *trace = &celt_encode_trace.normalizations[call];
  int active = M * m->eBands[end];
  int frame_coeffs = M * m->shortMdctSize;
  if (!trace_dimensions(active * C, CELT_TRACE_MAX_FLOATS) ||
      !trace_dimensions(end * C, CELT_TRACE_MAX_BANDS * 2)) return;
  trace->active_coeffs = (uint32_t)active;
  trace->bands = (uint32_t)end;
  trace->channels = (uint32_t)C;
  trace_copy_bands(trace->band_energy, bandE, end, C, m->nbEBands);
  for (int c = 0; c < C; c++)
    memcpy(trace->normalized + c * active, X + c * frame_coeffs, (size_t)active * sizeof(float));
  celt_encode_trace.stored_normalization_calls++;
}

extern void __real_quant_coarse_energy(const CELTMode *m, int start, int end,
    int effEnd, const celt_glog *eBands, celt_glog *oldEBands,
    opus_uint32 budget, celt_glog *error, ec_enc *enc, int C, int LM,
    int nbAvailableBytes, int force_intra, opus_val32 *delayedIntra,
    int two_pass, int loss_rate, int lfe);
void __wrap_quant_coarse_energy(const CELTMode *m, int start, int end,
    int effEnd, const celt_glog *eBands, celt_glog *oldEBands,
    opus_uint32 budget, celt_glog *error, ec_enc *enc, int C, int LM,
    int nbAvailableBytes, int force_intra, opus_val32 *delayedIntra,
    int two_pass, int loss_rate, int lfe) {
  if (!trace_selected_frame()) {
    __real_quant_coarse_energy(m, start, end, effEnd, eBands, oldEBands, budget,
        error, enc, C, LM, nbAvailableBytes, force_intra, delayedIntra,
        two_pass, loss_rate, lfe);
    return;
  }
  uint32_t call = celt_encode_trace.coarse_calls++;
#ifdef GOPUS_CELT_CODER_RANGE_TRACE
  celt_coder_range_trace.coarse_calls++;
  if (call == 0) {
    celt_coder_range_trace.coarse_encoder = enc;
    capture_coder_range(CELT_RANGE_BEFORE_COARSE, enc);
  } else {
    celt_coder_range_trace.overflow = 1;
  }
#endif
  if (call < CELT_TRACE_MAX_CALLS && trace_dimensions((end - start) * C, CELT_TRACE_MAX_BANDS * 2)) {
    celt_trace_coarse_call *trace = &celt_encode_trace.coarse[call];
    trace->bands = (uint32_t)(end - start);
    trace->channels = (uint32_t)C;
    trace->budget_bytes = (uint32_t)nbAvailableBytes;
    trace_copy_bands(trace->input, eBands + start, end - start, C, m->nbEBands);
    celt_encode_trace.stored_coarse_calls++;
  } else if (call >= CELT_TRACE_MAX_CALLS) {
    celt_encode_trace.overflow = 1;
  }
  __real_quant_coarse_energy(m, start, end, effEnd, eBands, oldEBands, budget,
      error, enc, C, LM, nbAvailableBytes, force_intra, delayedIntra,
      two_pass, loss_rate, lfe);
#ifdef GOPUS_CELT_TF_TRACE
  if (trace_selected_frame()) {
    if (celt_tf_trace.coarse_calls++ == 0 && enc != NULL) {
      celt_tf_trace.encoder = enc;
      celt_tf_trace.entry_tell = ec_tell(enc);
      celt_tf_trace.storage_bits = enc->storage * 8;
      celt_tf_trace.active = 1;
    } else {
      celt_tf_trace.overflow = 1;
    }
  }
#endif
#ifdef GOPUS_CELT_CODER_RANGE_TRACE
  if (call == 0) capture_coder_range(CELT_RANGE_AFTER_COARSE, enc);
#endif
  if (call < CELT_TRACE_MAX_CALLS && call < celt_encode_trace.stored_coarse_calls) {
    celt_trace_coarse_call *trace = &celt_encode_trace.coarse[call];
    trace_copy_bands(trace->quantized, oldEBands + start, end - start, C, m->nbEBands);
    trace_copy_bands(trace->error, error + start, end - start, C, m->nbEBands);
  }
}

extern void __real_quant_all_bands(int encode, const CELTMode *m, int start, int end,
    celt_norm *X, celt_norm *Y, unsigned char *collapse_masks,
    const celt_ener *bandE, int *pulses, int shortBlocks, int spread,
    int dual_stereo, int intensity, int *tf_res, opus_int32 total_bits,
    opus_int32 balance, ec_enc *ec, int LM, int codedBands, opus_uint32 *seed,
    int complexity, int arch, int disable_inv
    ARG_QEXT(ec_enc *ext_ec) ARG_QEXT(int *extra_pulses)
    ARG_QEXT(opus_int32 total_ext_bits) ARG_QEXT(const int *cap));
void __wrap_quant_all_bands(int encode, const CELTMode *m, int start, int end,
    celt_norm *X, celt_norm *Y, unsigned char *collapse_masks,
    const celt_ener *bandE, int *pulses, int shortBlocks, int spread,
    int dual_stereo, int intensity, int *tf_res, opus_int32 total_bits,
    opus_int32 balance, ec_enc *ec, int LM, int codedBands, opus_uint32 *seed,
    int complexity, int arch, int disable_inv
    ARG_QEXT(ec_enc *ext_ec) ARG_QEXT(int *extra_pulses)
    ARG_QEXT(opus_int32 total_ext_bits) ARG_QEXT(const int *cap)) {
  if (!trace_selected_frame()) {
    __real_quant_all_bands(encode, m, start, end, X, Y, collapse_masks, bandE,
        pulses, shortBlocks, spread, dual_stereo, intensity, tf_res, total_bits,
        balance, ec, LM, codedBands, seed, complexity, arch, disable_inv
        ARG_QEXT(ext_ec) ARG_QEXT(extra_pulses) ARG_QEXT(total_ext_bits) ARG_QEXT(cap));
    return;
  }
  uint32_t call = celt_encode_trace.quant_calls++;
#ifdef GOPUS_CELT_CODER_RANGE_TRACE
  celt_coder_range_trace.quant_calls++;
  if (call == 0) {
    celt_coder_range_trace.quant_encoder = ec;
    capture_coder_range(CELT_RANGE_BEFORE_QUANT, ec);
  } else {
    celt_coder_range_trace.overflow = 1;
  }
#endif
#ifdef GOPUS_CELT_TF_TRACE
  if (trace_selected_frame() && ec == celt_tf_trace.encoder) {
    celt_tf_trace.quant_calls++;
    celt_tf_trace.quant_encoder = ec;
    if (celt_tf_trace.quant_calls != 1 || celt_tf_trace.active != 0 ||
        celt_tf_trace.spread_calls != 1 || start < 0 || end < start ||
        end - start > CELT_TRACE_MAX_BANDS || LM < 0 || LM > 3 || tf_res == NULL) {
      celt_tf_trace.overflow = 1;
    } else {
      celt_tf_trace.start = (uint32_t)start;
      celt_tf_trace.end = (uint32_t)end;
      celt_tf_trace.lm = (uint32_t)LM;
      celt_tf_trace.transient = shortBlocks != 0;
      celt_tf_trace.post_count = (uint32_t)(end - start);
      for (int i = 0; i < end - start; i++)
        celt_tf_trace.post_tf_res[i] = (int32_t)tf_res[start + i];
    }
  } else if (trace_selected_frame() && celt_tf_trace.encoder != NULL) {
    celt_tf_trace.foreign_calls++;
  }
#endif
  int active = (1 << LM) * m->eBands[end];
  int channels = Y != NULL ? 2 : 1;
  if (call < CELT_TRACE_MAX_CALLS && trace_dimensions(active * channels, CELT_TRACE_MAX_FLOATS) &&
      trace_dimensions((end - start) * channels, CELT_TRACE_MAX_BANDS * 2)) {
    celt_trace_quant_call *trace = &celt_encode_trace.quant[call];
    trace->active_coeffs = (uint32_t)active;
    trace->bands = (uint32_t)(end - start);
    trace->channels = (uint32_t)channels;
    trace_copy_bands(trace->band_energy, bandE + start, end - start, channels, m->nbEBands);
    memcpy(trace->input, X, (size_t)active * sizeof(float));
    if (Y != NULL) memcpy(trace->input + active, Y, (size_t)active * sizeof(float));
    celt_encode_trace.stored_quant_calls++;
  } else if (call >= CELT_TRACE_MAX_CALLS) {
    celt_encode_trace.overflow = 1;
  }
  __real_quant_all_bands(encode, m, start, end, X, Y, collapse_masks, bandE,
      pulses, shortBlocks, spread, dual_stereo, intensity, tf_res, total_bits,
      balance, ec, LM, codedBands, seed, complexity, arch, disable_inv
      ARG_QEXT(ext_ec) ARG_QEXT(extra_pulses) ARG_QEXT(total_ext_bits) ARG_QEXT(cap));
  if (call < CELT_TRACE_MAX_CALLS && call < celt_encode_trace.stored_quant_calls) {
    celt_trace_quant_call *trace = &celt_encode_trace.quant[call];
    memcpy(trace->output, X, (size_t)active * sizeof(float));
    if (Y != NULL) memcpy(trace->output + active, Y, (size_t)active * sizeof(float));
  }
}

#ifdef GOPUS_CELT_TF_TRACE
extern void __real_ec_enc_bit_logp(ec_enc *enc, int value, unsigned logp);
void __wrap_ec_enc_bit_logp(ec_enc *enc, int value, unsigned logp) {
  celt_tf_bit_trace *trace = NULL;
  if (celt_tf_trace.active) {
    if (enc != celt_tf_trace.encoder) {
      celt_tf_trace.foreign_calls++;
    } else {
      uint32_t call = celt_tf_trace.bit_calls++;
      if (call >= CELT_TF_TRACE_MAX_BITS) {
        celt_tf_trace.overflow = 1;
      } else {
        trace = &celt_tf_trace.bits[call];
        trace->ordinal = call;
        trace->symbol = (uint32_t)value;
        trace->logp = logp;
        trace->range_before = enc->rng;
        trace->tell_frac_before = ec_tell_frac(enc);
        trace->tell_before = ec_tell(enc);
      }
    }
  }
  __real_ec_enc_bit_logp(enc, value, logp);
  if (trace != NULL) {
    trace->range_after = enc->rng;
    trace->tell_frac_after = ec_tell_frac(enc);
    trace->tell_after = ec_tell(enc);
    celt_tf_trace.stored_bits++;
    if (logp == 1) {
      celt_tf_trace.select_encoded++;
      celt_tf_trace.select_symbol = (uint32_t)value;
      if (celt_tf_trace.select_encoded > 1) celt_tf_trace.overflow = 1;
    }
  }
}

extern void __real_ec_enc_icdf(ec_enc *enc, int symbol, const unsigned char *icdf, unsigned ftb);
void __wrap_ec_enc_icdf(ec_enc *enc, int symbol, const unsigned char *icdf, unsigned ftb) {
  int close = 0;
  if (celt_tf_trace.active) {
    if (enc != celt_tf_trace.encoder) {
      celt_tf_trace.foreign_calls++;
    } else {
      celt_tf_trace.icdf_calls++;
      if (celt_tf_spread_table_match(symbol, icdf, ftb)) {
        celt_tf_trace.spread_calls++;
        celt_tf_trace.spread_table_match = 1;
        celt_tf_trace.spread_symbol = (uint32_t)symbol;
        celt_tf_trace.spread_logp = ftb;
        celt_tf_trace.spread_range_before = enc->rng;
        celt_tf_trace.spread_tell_frac_before = ec_tell_frac(enc);
        close = 1;
      }
    }
  }
  __real_ec_enc_icdf(enc, symbol, icdf, ftb);
  if (close) {
    celt_tf_trace.spread_range_after = enc->rng;
    celt_tf_trace.spread_tell_frac_after = ec_tell_frac(enc);
    celt_tf_trace.active = 0;
  }
}
#endif

extern void __real_ec_enc_bits(ec_enc *enc, opus_uint32 fl, unsigned bits);
void __wrap_ec_enc_bits(ec_enc *enc, opus_uint32 fl, unsigned bits) {
  entropy_raw_call *trace = NULL;
  if (active_frame == TRACE_FRAME) {
    uint32_t call = entropy_trace.raw_calls++;
    if (call < ENTROPY_TRACE_MAX_RAW_CALLS) {
      trace = &entropy_trace.raw[call];
      trace->fl = fl;
      trace->bits = bits;
      snapshot_context(&trace->before, enc);
    } else {
      entropy_trace.overflow = 1;
    }
  }
  __real_ec_enc_bits(enc, fl, bits);
  if (trace != NULL) {
    snapshot_context(&trace->after, enc);
    entropy_trace.stored_raw_calls++;
  }
}

extern void __real_ec_enc_done(ec_enc *enc);
void __wrap_ec_enc_done(ec_enc *enc) {
  int selected = active_frame == TRACE_FRAME;
  uint32_t call = 0;
  if (selected) {
    call = entropy_trace.done_calls++;
    if (call < ENTROPY_TRACE_MAX_DONE_CALLS)
      snapshot_context(&entropy_trace.done_before[call], enc);
    else
      entropy_trace.overflow = 1;
  }
  __real_ec_enc_done(enc);
  if (selected && call < ENTROPY_TRACE_MAX_DONE_CALLS) {
    snapshot_context(&entropy_trace.done_after[call], enc);
    entropy_trace.stored_done_calls++;
  }
}

static int write_celt_encode_trace(void) {
  if (!write_exact("GCET", 4) || !write_u32(4) ||
      !write_u32(celt_encode_trace.captured_frame) || !write_u32(celt_encode_trace.overflow)) return 0;
  if (!write_u32(celt_encode_trace.band_calls) || !write_u32(celt_encode_trace.stored_band_calls)) return 0;
  for (uint32_t i = 0; i < celt_encode_trace.stored_band_calls; i++) {
    const celt_trace_band_call *trace = &celt_encode_trace.bands[i];
    uint32_t spectrum_count = trace->frame_coeffs * trace->channels;
    uint32_t band_count = trace->bands * trace->channels;
    if (!write_u32(trace->frame_coeffs) || !write_u32(trace->bands) ||
        !write_u32(trace->channels) || !write_u32(trace->lm) ||
        !trace_write_float32(trace->spectrum, spectrum_count) ||
        !trace_write_float32(trace->amplitudes, band_count)) return 0;
  }
  if (!write_u32(celt_encode_trace.log_calls) || !write_u32(celt_encode_trace.stored_log_calls)) return 0;
  for (uint32_t i = 0; i < celt_encode_trace.stored_log_calls; i++) {
    const celt_trace_log_call *trace = &celt_encode_trace.logs[i];
    uint32_t count = trace->bands * trace->channels;
    if (!write_u32(trace->bands) || !write_u32(trace->channels) ||
        !trace_write_float32(trace->amplitudes, count) || !trace_write_float32(trace->log_energy, count)) return 0;
  }
  if (!write_u32(celt_encode_trace.normalization_calls) || !write_u32(celt_encode_trace.stored_normalization_calls)) return 0;
  for (uint32_t i = 0; i < celt_encode_trace.stored_normalization_calls; i++) {
    const celt_trace_normalization_call *trace = &celt_encode_trace.normalizations[i];
    uint32_t band_count = trace->bands * trace->channels;
    uint32_t coeff_count = trace->active_coeffs * trace->channels;
    if (!write_u32(trace->active_coeffs) || !write_u32(trace->bands) || !write_u32(trace->channels) ||
        !trace_write_float32(trace->band_energy, band_count) || !trace_write_float32(trace->normalized, coeff_count)) return 0;
  }
  if (!write_u32(celt_encode_trace.coarse_calls) || !write_u32(celt_encode_trace.stored_coarse_calls)) return 0;
  for (uint32_t i = 0; i < celt_encode_trace.stored_coarse_calls; i++) {
    const celt_trace_coarse_call *trace = &celt_encode_trace.coarse[i];
    uint32_t count = trace->bands * trace->channels;
    if (!write_u32(trace->bands) || !write_u32(trace->channels) || !write_u32(trace->budget_bytes) ||
        !trace_write_float32(trace->input, count) || !trace_write_float32(trace->quantized, count) ||
        !trace_write_float32(trace->error, count)) return 0;
  }
  if (!write_u32(celt_encode_trace.quant_calls) || !write_u32(celt_encode_trace.stored_quant_calls)) return 0;
  for (uint32_t i = 0; i < celt_encode_trace.stored_quant_calls; i++) {
    const celt_trace_quant_call *trace = &celt_encode_trace.quant[i];
    uint32_t band_count = trace->bands * trace->channels;
    uint32_t coeff_count = trace->active_coeffs * trace->channels;
    if (!write_u32(trace->active_coeffs) || !write_u32(trace->bands) || !write_u32(trace->channels) ||
        !trace_write_float32(trace->band_energy, band_count) || !trace_write_float32(trace->input, coeff_count) ||
        !trace_write_float32(trace->output, coeff_count)) return 0;
  }
  if (!write_u32(celt_encode_trace.mdct_calls) || !write_u32(celt_encode_trace.stored_mdct_calls)) return 0;
  for (uint32_t i = 0; i < celt_encode_trace.stored_mdct_calls; i++) {
    const celt_trace_mdct_call *trace = &celt_encode_trace.mdct[i];
    uint32_t scale_bits;
    memcpy(&scale_bits, &trace->fft_scale, sizeof(scale_bits));
    if (!write_u32(trace->lookup_n) || !write_u32(trace->maxshift) ||
        !write_u32(trace->transform_n) || !write_u32(trace->shift) ||
        !write_u32(trace->stride) || !write_u32(trace->overlap) ||
        !write_u32(trace->arch) || !write_u32(trace->fft_n) || !write_u32(scale_bits) ||
        !write_u32(trace->input_count) || !write_u32(trace->window_count) || !write_u32(trace->trig_count) ||
        !trace_write_float32(trace->input, trace->input_count) ||
        !trace_write_float32(trace->window, trace->window_count) ||
        !trace_write_float32(trace->trig, trace->trig_count)) return 0;
  }
  if (!write_u32(celt_encode_trace.preemphasis_calls) ||
      !write_u32(celt_encode_trace.stored_preemphasis_calls)) return 0;
  for (uint32_t i = 0; i < celt_encode_trace.stored_preemphasis_calls; i++) {
    const celt_trace_preemphasis_call *trace = &celt_encode_trace.preemphasis[i];
    if (!write_u32(trace->channel) || !write_u32(trace->channels) ||
        !write_u32(trace->frame_size) || !write_u32(trace->upsample) ||
        !write_u32(trace->input_count) || !write_u32(trace->output_count) ||
        !write_u32(trace->flags) || !trace_write_float32(&trace->state_before, 1) ||
        !trace_write_float32(&trace->state_after, 1) ||
        !trace_write_float32(trace->coefficients, 4) ||
        !trace_write_float32(trace->input, trace->input_count) ||
        !trace_write_float32(trace->output, trace->output_count)) return 0;
  }
  if (!write_u32(celt_encode_trace.prefilter_calls) ||
      !write_u32(celt_encode_trace.stored_prefilter_calls)) return 0;
  for (uint32_t i = 0; i < celt_encode_trace.stored_prefilter_calls; i++) {
    const celt_trace_prefilter_call *trace = &celt_encode_trace.prefilter[i];
    if (!write_u32(trace->t0) || !write_u32(trace->t1) || !write_u32(trace->n) ||
        !write_u32(trace->tapset0) || !write_u32(trace->tapset1) ||
        !write_u32(trace->overlap) || !write_u32(trace->arch) || !write_u32(trace->window_nil) ||
        !write_u32(trace->history_count) || !write_u32(trace->input_count) ||
        !write_u32(trace->window_count) || !write_u32(trace->output_count) ||
        !trace_write_float32(&trace->gain0, 1) || !trace_write_float32(&trace->gain1, 1) ||
        !trace_write_float32(trace->history, trace->history_count) ||
        !trace_write_float32(trace->input, trace->input_count) ||
        !trace_write_float32(trace->window, trace->window_count) ||
        !trace_write_float32(trace->output, trace->output_count)) return 0;
  }
  return 1;
}

static int write_entropy_snapshot(const entropy_context_snapshot *snapshot) {
  return write_u32(snapshot->storage) && write_u32(snapshot->offs) &&
      write_u32(snapshot->end_offs) && write_u32(snapshot->end_window) &&
      write_u32((uint32_t)snapshot->nend_bits) && write_u32((uint32_t)snapshot->nbits_total) &&
      write_u32(snapshot->rng) && write_u32(snapshot->val) && write_u32(snapshot->ext) &&
      write_u32((uint32_t)snapshot->rem) && write_u32((uint32_t)snapshot->error);
}

static int write_entropy_trace(void) {
  if (!write_exact("GENT", 4) || !write_u32(1) || !write_u32(TRACE_FRAME) ||
      !write_u32(entropy_trace.overflow) || !write_u32(entropy_trace.raw_calls) ||
      !write_u32(entropy_trace.stored_raw_calls) || !write_u32(entropy_trace.done_calls) ||
      !write_u32(entropy_trace.stored_done_calls)) return 0;
  for (uint32_t i = 0; i < entropy_trace.stored_raw_calls; i++) {
    const entropy_raw_call *trace = &entropy_trace.raw[i];
    if (!write_u32(trace->fl) || !write_u32(trace->bits) ||
        !write_entropy_snapshot(&trace->before) || !write_entropy_snapshot(&trace->after)) return 0;
  }
  for (uint32_t i = 0; i < entropy_trace.stored_done_calls; i++) {
    if (!write_entropy_snapshot(&entropy_trace.done_before[i]) ||
        !write_entropy_snapshot(&entropy_trace.done_after[i])) return 0;
  }
  return 1;
}

#ifdef GOPUS_CELT_CODER_RANGE_TRACE
static int write_coder_range_trace(void) {
  uint32_t same_coder = celt_coder_range_trace.coarse_encoder != NULL &&
      celt_coder_range_trace.coarse_encoder == celt_coder_range_trace.quant_encoder;
  if (celt_coder_range_trace.coarse_calls != 1 || celt_coder_range_trace.quant_calls != 1 ||
      !same_coder || celt_coder_range_trace.count != 3)
    celt_coder_range_trace.overflow = 1;
  if (!write_exact("GCRG", 4) || !write_u32(1) || !write_u32(TRACE_FRAME) ||
      !write_u32(celt_coder_range_trace.overflow) ||
      !write_u32(celt_coder_range_trace.coarse_calls) ||
      !write_u32(celt_coder_range_trace.quant_calls) || !write_u32(same_coder) ||
      !write_u32(celt_coder_range_trace.count)) return 0;
  for (uint32_t i = 0; i < celt_coder_range_trace.count; i++) {
    const celt_coder_range_point *point = &celt_coder_range_trace.points[i];
    if (!write_u32(point->stage) || !write_u32(point->range) || !write_u32(point->tell_frac)) return 0;
  }
  return 1;
}
#endif

#ifdef GOPUS_CELT_TF_TRACE
static int write_celt_tf_trace(void) {
  if (celt_tf_trace.coarse_calls != 1 || celt_tf_trace.stored_bits != celt_tf_trace.bit_calls ||
      celt_tf_trace.foreign_calls != 0 || celt_tf_trace.icdf_calls != 1 ||
      celt_tf_trace.spread_calls != 1 || celt_tf_trace.spread_table_match != 1 ||
      celt_tf_trace.quant_calls != 1 || celt_tf_trace.active != 0 ||
      celt_tf_trace.encoder == NULL || celt_tf_trace.end <= celt_tf_trace.start ||
      celt_tf_trace.end > CELT_TRACE_MAX_BANDS ||
      celt_tf_trace.lm > 3 || celt_tf_trace.transient > 1 || celt_tf_trace.select_encoded > 1 ||
      celt_tf_trace.storage_bits == 0 || celt_tf_trace.post_count != celt_tf_trace.end-celt_tf_trace.start)
    celt_tf_trace.overflow = 1;
  if (celt_tf_trace.post_count > CELT_TRACE_MAX_BANDS || celt_tf_trace.stored_bits > CELT_TF_TRACE_MAX_BITS)
    return 0;
  celt_tf_trace.same_coder = celt_tf_trace.encoder != NULL &&
      celt_tf_trace.quant_encoder == celt_tf_trace.encoder;
  if (!celt_tf_trace.same_coder) celt_tf_trace.overflow = 1;
  if (!write_exact("GCTF", 4) || !write_u32(1) || !write_u32(TRACE_FRAME) ||
      !write_u32(celt_tf_trace.overflow) || !write_u32(celt_tf_trace.coarse_calls) ||
      !write_u32(celt_tf_trace.bit_calls) || !write_u32(celt_tf_trace.stored_bits) ||
      !write_u32(celt_tf_trace.foreign_calls) || !write_u32(celt_tf_trace.icdf_calls) ||
      !write_u32(celt_tf_trace.spread_calls) || !write_u32(celt_tf_trace.spread_table_match) ||
      !write_u32(celt_tf_trace.same_coder) || !write_u32(celt_tf_trace.quant_calls) ||
      !write_u32(celt_tf_trace.start) || !write_u32(celt_tf_trace.end) ||
      !write_u32(celt_tf_trace.lm) || !write_u32((uint32_t)celt_tf_trace.entry_tell) ||
      !write_u32(celt_tf_trace.storage_bits) || !write_u32(celt_tf_trace.transient) ||
      !write_u32(celt_tf_trace.select_encoded) || !write_u32(celt_tf_trace.select_symbol) ||
      !write_u32(celt_tf_trace.post_count) || !write_u32(celt_tf_trace.spread_symbol) ||
      !write_u32(celt_tf_trace.spread_logp) || !write_u32(celt_tf_trace.spread_range_before) ||
      !write_u32(celt_tf_trace.spread_tell_frac_before) || !write_u32(celt_tf_trace.spread_range_after) ||
      !write_u32(celt_tf_trace.spread_tell_frac_after)) return 0;
  for (uint32_t i = 0; i < celt_tf_trace.post_count; i++)
    if (!write_u32((uint32_t)celt_tf_trace.post_tf_res[i])) return 0;
  for (uint32_t i = 0; i < celt_tf_trace.stored_bits; i++) {
    const celt_tf_bit_trace *bit = &celt_tf_trace.bits[i];
    if (!write_u32(bit->ordinal) || !write_u32(bit->symbol) || !write_u32(bit->logp) ||
        !write_u32(bit->range_before) || !write_u32(bit->tell_frac_before) ||
        !write_u32(bit->tell_before) || !write_u32(bit->range_after) ||
        !write_u32(bit->tell_frac_after) || !write_u32(bit->tell_after)) return 0;
  }
  return 1;
}
#endif

int main(void) {
  int result = gopus_encode_diff_main();
  if (result != 0) return result;
  if (!write_celt_encode_trace() || !write_entropy_trace()
#ifdef GOPUS_CELT_CODER_RANGE_TRACE
      || !write_coder_range_trace()
#endif
#ifdef GOPUS_CELT_TF_TRACE
      || !write_celt_tf_trace()
#endif
      ) {
    fprintf(stderr, "write selected-frame CELT/entropy trace failed\n");
    return 1;
  }
  return 0;
}
