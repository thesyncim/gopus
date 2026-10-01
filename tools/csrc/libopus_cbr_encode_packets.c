/* libopus_cbr_encode_packets.c — CBR encode oracle for gopus parity testing.
 *
 * Reads float32 LE PCM frames from stdin and encodes them with libopus
 * configured for CBR (OPUS_SET_VBR(0)), emitting each packet in the standard
 * gopus oracle wire format.
 *
 * Wire format (all little-endian):
 *   IN:  "GCBR" u32(version=1) u32(application) u32(bandwidth) u32(channels)
 *              u32(bitrate) u32(frame_size) u32(complexity) u32(num_frames)
 *              [num_frames × frame_size × channels × float32]
 *   OUT: "GCBO" u32(version=2) u32(version_string_len) u8(version_string…)
 *              u32(OPUS_ARCHMASK) u32(build_feature_bits) u32(opus_select_arch())
 *              u32(num_frames)
 *              [num_frames × u32(packet_len) u32(final_range) u8(packet…)]
 *
 * application values:
 *   0 = OPUS_APPLICATION_AUDIO
 *   1 = OPUS_APPLICATION_VOIP
 *   2 = OPUS_APPLICATION_RESTRICTED_SILK
 *   3 = OPUS_APPLICATION_RESTRICTED_CELT
 *
 * bandwidth values (match opus_defines.h):
 *   1101 = OPUS_BANDWIDTH_NARROWBAND
 *   1102 = OPUS_BANDWIDTH_MEDIUMBAND
 *   1103 = OPUS_BANDWIDTH_WIDEBAND
 *   1104 = OPUS_BANDWIDTH_SUPERWIDEBAND
 *   1105 = OPUS_BANDWIDTH_FULLBAND
 *
 * Reference: libopus src/opus_encoder.c opus_encode_float()
 *            src/opus_demo.c -cbr flag: opus_encoder_ctl(enc, OPUS_SET_VBR(0))
 */

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "opus.h"
#include "config.h"
#include "celt/cpu_support.h"
#ifdef GOPUS_CELT_TRACE
#include "celt/celt.h"
#include "celt/bands.h"
#include "celt/mdct.h"
#ifdef GOPUS_CELT_PITCH_TRACE
#include "celt/pitch.h"
#include "celt/celt_lpc.h"
#endif
#include "celt/quant_bands.h"
#endif

#define INPUT_MAGIC  "GCBR"
#define OUTPUT_MAGIC "GCBO"
#define MAX_PACKET_BYTES 4000

/* These bits describe the generated config.h seen by this helper translation
 * unit. The selected runtime architecture is reported separately below. */
#define GCBO_FEATURE_RTCD                    (1u << 0)
#define GCBO_FEATURE_X86_MAY_SSE             (1u << 1)
#define GCBO_FEATURE_X86_MAY_SSE2            (1u << 2)
#define GCBO_FEATURE_X86_MAY_SSE4_1          (1u << 3)
#define GCBO_FEATURE_X86_MAY_AVX2            (1u << 4)
#define GCBO_FEATURE_X86_PRESUME_SSE         (1u << 5)
#define GCBO_FEATURE_X86_PRESUME_SSE2        (1u << 6)
#define GCBO_FEATURE_X86_PRESUME_SSE4_1      (1u << 7)
#define GCBO_FEATURE_X86_PRESUME_AVX2        (1u << 8)
#define GCBO_FEATURE_ARM_MAY_NEON             (1u << 9)
#define GCBO_FEATURE_ARM_PRESUME_NEON         (1u << 10)
#define GCBO_FEATURE_ARM_MAY_NEON_INTR        (1u << 11)
#define GCBO_FEATURE_ARM_PRESUME_NEON_INTR    (1u << 12)
#define GCBO_FEATURE_ARM_MAY_DOTPROD          (1u << 13)
#define GCBO_FEATURE_ARM_PRESUME_DOTPROD      (1u << 14)

static uint32_t build_feature_bits(void) {
  uint32_t bits = 0;
#ifdef OPUS_HAVE_RTCD
  bits |= GCBO_FEATURE_RTCD;
#endif
#ifdef OPUS_X86_MAY_HAVE_SSE
  bits |= GCBO_FEATURE_X86_MAY_SSE;
#endif
#ifdef OPUS_X86_PRESUME_SSE
  bits |= GCBO_FEATURE_X86_PRESUME_SSE;
#endif
#ifdef OPUS_X86_MAY_HAVE_SSE2
  bits |= GCBO_FEATURE_X86_MAY_SSE2;
#endif
#ifdef OPUS_X86_PRESUME_SSE2
  bits |= GCBO_FEATURE_X86_PRESUME_SSE2;
#endif
#ifdef OPUS_X86_MAY_HAVE_SSE4_1
  bits |= GCBO_FEATURE_X86_MAY_SSE4_1;
#endif
#ifdef OPUS_X86_PRESUME_SSE4_1
  bits |= GCBO_FEATURE_X86_PRESUME_SSE4_1;
#endif
#ifdef OPUS_X86_MAY_HAVE_AVX2
  bits |= GCBO_FEATURE_X86_MAY_AVX2;
#endif
#ifdef OPUS_X86_PRESUME_AVX2
  bits |= GCBO_FEATURE_X86_PRESUME_AVX2;
#endif
#ifdef OPUS_ARM_MAY_HAVE_NEON
  bits |= GCBO_FEATURE_ARM_MAY_NEON;
#endif
#ifdef OPUS_ARM_PRESUME_NEON
  bits |= GCBO_FEATURE_ARM_PRESUME_NEON;
#endif
#ifdef OPUS_ARM_MAY_HAVE_NEON_INTR
  bits |= GCBO_FEATURE_ARM_MAY_NEON_INTR;
#endif
#ifdef OPUS_ARM_PRESUME_NEON_INTR
  bits |= GCBO_FEATURE_ARM_PRESUME_NEON_INTR;
#endif
#ifdef OPUS_ARM_MAY_HAVE_DOTPROD
  bits |= GCBO_FEATURE_ARM_MAY_DOTPROD;
#endif
#ifdef OPUS_ARM_PRESUME_DOTPROD
  bits |= GCBO_FEATURE_ARM_PRESUME_DOTPROD;
#endif
  return bits;
}

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin),  _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

static int read_exact(void *dst, size_t n) {
  size_t got = fread(dst, 1, n, stdin);
  return got == n;
}

static int write_exact(const void *src, size_t n) {
  size_t off = 0;
  const unsigned char *p = (const unsigned char *)src;
  while (off < n) {
    size_t w = fwrite(p + off, 1, n - off, stdout);
    if (w == 0) return 0;
    off += w;
  }
  return 1;
}

static int read_u32(uint32_t *v) {
  unsigned char b[4];
  if (!read_exact(b, 4)) return 0;
  *v = (uint32_t)b[0] | ((uint32_t)b[1] << 8) |
       ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int write_u32(uint32_t v) {
  unsigned char b[4];
  b[0] = (unsigned char)(v & 0xff);
  b[1] = (unsigned char)((v >> 8) & 0xff);
  b[2] = (unsigned char)((v >> 16) & 0xff);
  b[3] = (unsigned char)((v >> 24) & 0xff);
  return write_exact(b, 4);
}

#ifdef GOPUS_CELT_TRACE
#ifndef GOPUS_CELT_TRACE_FRAME
#define GOPUS_CELT_TRACE_FRAME 0
#endif
#define CELT_TRACE_MAX_CALLS 8
/* celt_encoder.c computes two long channel transforms and up to 2*8 short
 * transforms for a stereo transient at LM=3, including secondMdct analysis. */
#define CELT_TRACE_MAX_MDCT_CALLS 18
#define CELT_TRACE_MAX_FLOATS 4096
#define CELT_TRACE_MAX_BANDS 64
#define CELT_TRACE_MAX_HISTORY 1024

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

#ifdef GOPUS_CELT_PITCH_TRACE
typedef struct {
  uint32_t frame_size, channels, enabled, complexity, arch, max_period, min_period;
  float tf_estimate, tone_freq, toneishness, max_pitch_ratio;
} celt_trace_pitch_controls_call;

typedef struct {
  uint32_t length, channels, factor, arch, input_count, output_count;
  float input[CELT_TRACE_MAX_FLOATS];
  float output[CELT_TRACE_MAX_FLOATS];
} celt_trace_pitch_downsample_call;

typedef struct {
  uint32_t length, max_pitch, x_offset, arch, buffer_count;
  int32_t result;
  float buffer[CELT_TRACE_MAX_FLOATS];
} celt_trace_pitch_search_call;

typedef struct {
  uint32_t max_period, min_period, n, arch, buffer_count;
  int32_t t0_before, prev_period, t0_after;
  float prev_gain, gain;
  float buffer[CELT_TRACE_MAX_FLOATS];
} celt_trace_remove_doubling_call;

#ifdef GOPUS_CELT_PITCH_KERNEL_TRACE
typedef struct {
  uint32_t downsample_ordinal, n, lag, overlap, arch, window_nil, input_count, autocorrelation_count;
  int32_t result;
  float input[CELT_TRACE_MAX_FLOATS];
  float autocorrelation[5];
} celt_trace_pitch_autocorr_call;

typedef struct {
  uint32_t downsample_ordinal, order, autocorrelation_count, coefficient_count;
  float autocorrelation[5];
  float coefficients[4];
} celt_trace_pitch_lpc_call;
#endif
#endif

static struct {
  uint32_t band_calls, log_calls, normalization_calls, coarse_calls, quant_calls, mdct_calls;
  uint32_t preemphasis_calls, prefilter_calls;
  uint32_t stored_band_calls, stored_log_calls, stored_normalization_calls;
  uint32_t stored_coarse_calls, stored_quant_calls, stored_mdct_calls;
  uint32_t stored_preemphasis_calls, stored_prefilter_calls, overflow;
#ifdef GOPUS_CELT_PITCH_TRACE
  uint32_t pitch_controls_calls, stored_pitch_controls_calls;
  uint32_t pitch_downsample_calls, stored_pitch_downsample_calls;
  uint32_t pitch_search_calls, stored_pitch_search_calls;
  uint32_t remove_doubling_calls, stored_remove_doubling_calls;
#endif
#ifdef GOPUS_CELT_PITCH_KERNEL_TRACE
  uint32_t pitch_autocorr_calls, stored_pitch_autocorr_calls;
  uint32_t pitch_lpc_calls, stored_pitch_lpc_calls;
#endif
  celt_trace_band_call bands[CELT_TRACE_MAX_CALLS];
  celt_trace_log_call logs[CELT_TRACE_MAX_CALLS];
  celt_trace_normalization_call normalizations[CELT_TRACE_MAX_CALLS];
  celt_trace_coarse_call coarse[CELT_TRACE_MAX_CALLS];
  celt_trace_quant_call quant[CELT_TRACE_MAX_CALLS];
  celt_trace_mdct_call mdct[CELT_TRACE_MAX_MDCT_CALLS];
  celt_trace_preemphasis_call preemphasis[CELT_TRACE_MAX_CALLS];
  celt_trace_prefilter_call prefilter[CELT_TRACE_MAX_CALLS];
#ifdef GOPUS_CELT_PITCH_TRACE
  celt_trace_pitch_controls_call pitch_controls[CELT_TRACE_MAX_CALLS];
  celt_trace_pitch_downsample_call pitch_downsample[CELT_TRACE_MAX_CALLS];
  celt_trace_pitch_search_call pitch_search[CELT_TRACE_MAX_CALLS];
  celt_trace_remove_doubling_call remove_doubling[CELT_TRACE_MAX_CALLS];
#endif
#ifdef GOPUS_CELT_PITCH_KERNEL_TRACE
  celt_trace_pitch_autocorr_call pitch_autocorr[CELT_TRACE_MAX_CALLS];
  celt_trace_pitch_lpc_call pitch_lpc[CELT_TRACE_MAX_CALLS];
#endif
} celt_encode_trace;

static uint32_t celt_encode_active_frame;
static uint32_t celt_encode_captured_frame = UINT32_MAX;
#ifdef GOPUS_CELT_PITCH_TRACE
static opus_val16 *celt_trace_pitch_buffer;
static uint32_t celt_trace_pitch_buffer_count;
static int celt_trace_selected_frame(void);
#ifdef GOPUS_CELT_PITCH_KERNEL_TRACE
static int celt_trace_pitch_kernel_scope_active;
static uint32_t celt_trace_pitch_kernel_downsample_ordinal;
static uint32_t celt_trace_pitch_kernel_autocorr_scope_calls;
static uint32_t celt_trace_pitch_kernel_lpc_scope_calls;
static opus_val32 *celt_trace_pitch_kernel_autocorrelation;
#endif

void gopus_celt_pitch_controls_trace(int frame_size, int channels, int enabled, int complexity,
    int arch, opus_val16 tf_estimate, opus_val16 tone_freq, opus_val32 toneishness,
    float max_pitch_ratio, int max_period, int min_period) {
  celt_trace_pitch_controls_call *trace = NULL;
  uint32_t call = 0;
  if (celt_trace_selected_frame()) {
    call = celt_encode_trace.pitch_controls_calls++;
    if (call >= CELT_TRACE_MAX_CALLS || frame_size <= 0 || channels <= 0 || channels > 2 ||
        enabled < 0 || enabled > 1 || complexity < 0 || max_period <= 0 || min_period <= 0) {
      celt_encode_trace.overflow = 1;
    } else {
      trace = &celt_encode_trace.pitch_controls[call];
      trace->frame_size = (uint32_t)frame_size;
      trace->channels = (uint32_t)channels;
      trace->enabled = (uint32_t)enabled;
      trace->complexity = (uint32_t)complexity;
      trace->arch = (uint32_t)arch;
      trace->max_period = (uint32_t)max_period;
      trace->min_period = (uint32_t)min_period;
      trace->tf_estimate = (float)tf_estimate;
      trace->tone_freq = (float)tone_freq;
      trace->toneishness = (float)toneishness;
      trace->max_pitch_ratio = (float)max_pitch_ratio;
      celt_encode_trace.stored_pitch_controls_calls++;
    }
  }
}
#endif

static int celt_trace_selected_frame(void) {
  if (celt_encode_active_frame != GOPUS_CELT_TRACE_FRAME) return 0;
  if (celt_encode_captured_frame == UINT32_MAX)
    celt_encode_captured_frame = celt_encode_active_frame;
  return 1;
}

static int trace_dimensions(int values, int limit) {
  if (values < 0 || values > limit) {
    celt_encode_trace.overflow = 1;
    return 0;
  }
  return 1;
}

#ifdef GOPUS_CELT_PITCH_TRACE
extern void __real_pitch_downsample(celt_sig * OPUS_RESTRICT x[], opus_val16 * OPUS_RESTRICT x_lp,
    int len, int C, int factor, int arch);
void __wrap_pitch_downsample(celt_sig * OPUS_RESTRICT x[], opus_val16 * OPUS_RESTRICT x_lp,
    int len, int C, int factor, int arch) {
  celt_trace_pitch_downsample_call *trace = NULL;
  uint32_t call = 0;
  int selected = celt_trace_selected_frame();
  if (selected) {
    call = celt_encode_trace.pitch_downsample_calls++;
    if (call >= CELT_TRACE_MAX_CALLS || x == NULL || x_lp == NULL || len <= 0 || C <= 0 || C > 2 ||
        factor <= 0 || len > CELT_TRACE_MAX_FLOATS / factor ||
        len * factor > CELT_TRACE_MAX_FLOATS / C) {
      celt_encode_trace.overflow = 1;
    } else {
      trace = &celt_encode_trace.pitch_downsample[call];
      trace->length = (uint32_t)len;
      trace->channels = (uint32_t)C;
      trace->factor = (uint32_t)factor;
      trace->arch = (uint32_t)arch;
      trace->input_count = (uint32_t)(len * factor * C);
      trace->output_count = (uint32_t)len;
      for (int channel = 0; channel < C; channel++) {
        if (x[channel] == NULL) {
          celt_encode_trace.overflow = 1;
          trace = NULL;
          break;
        }
        memcpy(trace->input + (size_t)channel * (size_t)(len * factor), x[channel],
            (size_t)(len * factor) * sizeof(float));
      }
      if (trace != NULL) celt_encode_trace.stored_pitch_downsample_calls++;
    }
    celt_trace_pitch_buffer = x_lp;
    celt_trace_pitch_buffer_count = len > 0 ? (uint32_t)len : 0;
  }

#ifdef GOPUS_CELT_PITCH_KERNEL_TRACE
  if (selected) {
    if (celt_trace_pitch_kernel_scope_active) celt_encode_trace.overflow = 1;
    celt_trace_pitch_kernel_scope_active = 1;
    celt_trace_pitch_kernel_downsample_ordinal = call;
    celt_trace_pitch_kernel_autocorr_scope_calls = 0;
    celt_trace_pitch_kernel_lpc_scope_calls = 0;
    celt_trace_pitch_kernel_autocorrelation = NULL;
  }
#endif

  __real_pitch_downsample(x, x_lp, len, C, factor, arch);

  if (trace != NULL)
    memcpy(trace->output, x_lp, (size_t)trace->output_count * sizeof(float));
#ifdef GOPUS_CELT_PITCH_KERNEL_TRACE
  if (selected) {
    if (celt_trace_pitch_kernel_autocorr_scope_calls != 1 || celt_trace_pitch_kernel_lpc_scope_calls != 1)
      celt_encode_trace.overflow = 1;
    celt_trace_pitch_kernel_scope_active = 0;
    celt_trace_pitch_kernel_autocorrelation = NULL;
  }
#endif
}

#ifdef GOPUS_CELT_PITCH_KERNEL_TRACE
extern int __real__celt_autocorr(const opus_val16 *x, opus_val32 *ac,
    const celt_coef *window, int overlap, int lag, int n, int arch);
int __wrap__celt_autocorr(const opus_val16 *x, opus_val32 *ac,
    const celt_coef *window, int overlap, int lag, int n, int arch) {
  celt_trace_pitch_autocorr_call *trace = NULL;
  uint32_t downsample_ordinal = celt_trace_pitch_kernel_downsample_ordinal;
  if (celt_trace_pitch_kernel_scope_active) {
    celt_trace_pitch_kernel_autocorr_scope_calls++;
    uint32_t call = celt_encode_trace.pitch_autocorr_calls++;
    if (call >= CELT_TRACE_MAX_CALLS || downsample_ordinal >= CELT_TRACE_MAX_CALLS ||
        x == NULL || ac == NULL || x != celt_trace_pitch_buffer ||
        n <= 0 || n != (int)celt_trace_pitch_buffer_count || lag != 4 || overlap != 0 || window != NULL ||
        n > CELT_TRACE_MAX_FLOATS) {
      celt_encode_trace.overflow = 1;
    } else if (celt_encode_trace.stored_pitch_autocorr_calls >= CELT_TRACE_MAX_CALLS) {
      celt_encode_trace.overflow = 1;
    } else {
      trace = &celt_encode_trace.pitch_autocorr[celt_encode_trace.stored_pitch_autocorr_calls];
      trace->downsample_ordinal = downsample_ordinal;
      trace->n = (uint32_t)n;
      trace->lag = (uint32_t)lag;
      trace->overlap = (uint32_t)overlap;
      trace->arch = (uint32_t)arch;
      trace->window_nil = window == NULL ? 1u : 0u;
      trace->input_count = (uint32_t)n;
      trace->autocorrelation_count = (uint32_t)(lag + 1);
      memcpy(trace->input, x, (size_t)n * sizeof(float));
      celt_trace_pitch_kernel_autocorrelation = ac;
    }
  }

  int result = __real__celt_autocorr(x, ac, window, overlap, lag, n, arch);

  if (trace != NULL) {
    trace->result = (int32_t)result;
    memcpy(trace->autocorrelation, ac, (size_t)trace->autocorrelation_count * sizeof(float));
    celt_encode_trace.stored_pitch_autocorr_calls++;
  }
  return result;
}

extern void __real__celt_lpc(opus_val16 *lpc, const opus_val32 *ac, int order);
void __wrap__celt_lpc(opus_val16 *lpc, const opus_val32 *ac, int order) {
  celt_trace_pitch_lpc_call *trace = NULL;
  uint32_t downsample_ordinal = celt_trace_pitch_kernel_downsample_ordinal;
  if (celt_trace_pitch_kernel_scope_active) {
    celt_trace_pitch_kernel_lpc_scope_calls++;
    uint32_t call = celt_encode_trace.pitch_lpc_calls++;
    if (call >= CELT_TRACE_MAX_CALLS || downsample_ordinal >= CELT_TRACE_MAX_CALLS ||
        lpc == NULL || ac == NULL || ac != celt_trace_pitch_kernel_autocorrelation || order != 4) {
      celt_encode_trace.overflow = 1;
    } else if (celt_encode_trace.stored_pitch_lpc_calls >= CELT_TRACE_MAX_CALLS) {
      celt_encode_trace.overflow = 1;
    } else {
      trace = &celt_encode_trace.pitch_lpc[celt_encode_trace.stored_pitch_lpc_calls];
      trace->downsample_ordinal = downsample_ordinal;
      trace->order = (uint32_t)order;
      trace->autocorrelation_count = (uint32_t)(order + 1);
      trace->coefficient_count = (uint32_t)order;
      memcpy(trace->autocorrelation, ac, (size_t)(order + 1) * sizeof(float));
    }
  }

  __real__celt_lpc(lpc, ac, order);

  if (trace != NULL) {
    memcpy(trace->coefficients, lpc, (size_t)trace->coefficient_count * sizeof(float));
    celt_encode_trace.stored_pitch_lpc_calls++;
  }
}
#endif

extern void __real_pitch_search(const opus_val16 * OPUS_RESTRICT x_lp, opus_val16 * OPUS_RESTRICT y,
    int len, int max_pitch, int *pitch, int arch);
void __wrap_pitch_search(const opus_val16 * OPUS_RESTRICT x_lp, opus_val16 * OPUS_RESTRICT y,
    int len, int max_pitch, int *pitch, int arch) {
  celt_trace_pitch_search_call *trace = NULL;
  uint32_t call = 0;
  if (celt_trace_selected_frame()) {
    call = celt_encode_trace.pitch_search_calls++;
    uintptr_t y_address = (uintptr_t)y;
    uintptr_t x_address = (uintptr_t)x_lp;
    uint32_t x_offset = UINT32_MAX;
    if (y != NULL && x_lp != NULL && x_address >= y_address &&
        (x_address - y_address) % sizeof(*y) == 0 &&
        (x_address - y_address) / sizeof(*y) <= UINT32_MAX) {
      x_offset = (uint32_t)((x_address - y_address) / sizeof(*y));
    }
    if (call >= CELT_TRACE_MAX_CALLS || x_lp == NULL || y == NULL || pitch == NULL || len <= 0 || max_pitch <= 0 ||
        y != celt_trace_pitch_buffer || celt_trace_pitch_buffer_count == 0 ||
        celt_trace_pitch_buffer_count > CELT_TRACE_MAX_FLOATS || x_offset == UINT32_MAX ||
        x_offset >= celt_trace_pitch_buffer_count) {
      celt_encode_trace.overflow = 1;
    } else {
      trace = &celt_encode_trace.pitch_search[call];
      trace->length = (uint32_t)len;
      trace->max_pitch = (uint32_t)max_pitch;
      trace->x_offset = x_offset;
      trace->arch = (uint32_t)arch;
      trace->buffer_count = celt_trace_pitch_buffer_count;
      memcpy(trace->buffer, y, (size_t)trace->buffer_count * sizeof(float));
      celt_encode_trace.stored_pitch_search_calls++;
    }
  }

  __real_pitch_search(x_lp, y, len, max_pitch, pitch, arch);

  if (trace != NULL) trace->result = (int32_t)*pitch;
}

extern opus_val16 __real_remove_doubling(opus_val16 *x, int maxperiod, int minperiod,
    int N, int *T0, int prev_period, opus_val16 prev_gain, int arch);
opus_val16 __wrap_remove_doubling(opus_val16 *x, int maxperiod, int minperiod,
    int N, int *T0, int prev_period, opus_val16 prev_gain, int arch) {
  celt_trace_remove_doubling_call *trace = NULL;
  uint32_t call = 0;
  int32_t t0_before = T0 != NULL ? (int32_t)*T0 : 0;
  if (celt_trace_selected_frame()) {
    call = celt_encode_trace.remove_doubling_calls++;
    if (call >= CELT_TRACE_MAX_CALLS || x == NULL || T0 == NULL || maxperiod <= 0 || minperiod <= 0 || N <= 0 ||
        x != celt_trace_pitch_buffer || celt_trace_pitch_buffer_count == 0 ||
        celt_trace_pitch_buffer_count > CELT_TRACE_MAX_FLOATS) {
      celt_encode_trace.overflow = 1;
    } else {
      trace = &celt_encode_trace.remove_doubling[call];
      trace->max_period = (uint32_t)maxperiod;
      trace->min_period = (uint32_t)minperiod;
      trace->n = (uint32_t)N;
      trace->arch = (uint32_t)arch;
      trace->buffer_count = celt_trace_pitch_buffer_count;
      trace->t0_before = t0_before;
      trace->prev_period = (int32_t)prev_period;
      trace->prev_gain = (float)prev_gain;
      memcpy(trace->buffer, x, (size_t)trace->buffer_count * sizeof(float));
      celt_encode_trace.stored_remove_doubling_calls++;
    }
  }

  opus_val16 gain = __real_remove_doubling(x, maxperiod, minperiod, N, T0, prev_period, prev_gain, arch);

  if (trace != NULL) {
    trace->t0_after = (int32_t)*T0;
    trace->gain = (float)gain;
  }
  return gain;
}
#endif

extern void __real_clt_mdct_forward_c(const mdct_lookup *l, kiss_fft_scalar *in,
    kiss_fft_scalar *out, const celt_coef *window, int overlap, int shift,
    int stride, int arch);
void __wrap_clt_mdct_forward_c(const mdct_lookup *l, kiss_fft_scalar *in,
    kiss_fft_scalar *out, const celt_coef *window, int overlap, int shift,
    int stride, int arch) {
  if (!celt_trace_selected_frame()) {
    __real_clt_mdct_forward_c(l, in, out, window, overlap, shift, stride, arch);
    return;
  }

  uint32_t call = celt_encode_trace.mdct_calls++;
  if (call >= CELT_TRACE_MAX_MDCT_CALLS) {
    celt_encode_trace.overflow = 1;
  } else if (l == NULL || shift < 0 || shift >= 4 || shift > l->maxshift ||
             l->n <= 0 || l->kfft[shift] == NULL || overlap < 0 ||
             in == NULL || (overlap > 0 && window == NULL) || l->trig == NULL) {
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
    int window_count = overlap;
    int trig_count = n2;
    if (n <= 0 || n2 <= 0 || !trace_dimensions(input_count, CELT_TRACE_MAX_FLOATS) ||
        !trace_dimensions(window_count, CELT_TRACE_MAX_FLOATS) ||
        !trace_dimensions(trig_count, CELT_TRACE_MAX_FLOATS)) {
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
      trace->window_count = (uint32_t)window_count;
      trace->trig_count = (uint32_t)trig_count;
      memcpy(trace->input, in, (size_t)input_count * sizeof(float));
      if (window_count > 0)
        memcpy(trace->window, window, (size_t)window_count * sizeof(float));
      memcpy(trace->trig, trig, (size_t)trig_count * sizeof(float));
      celt_encode_trace.stored_mdct_calls++;
    }
  }

  __real_clt_mdct_forward_c(l, in, out, window, overlap, shift, stride, arch);
}

int gopus_celt_preemphasis_trace_begin(int channel, const opus_res *pcmp,
    celt_sig *inp, int N, int CC, int upsample, const opus_val16 *coef,
    celt_sig *mem, int clip) {
  if (!celt_trace_selected_frame()) return -1;
  uint32_t call = celt_encode_trace.preemphasis_calls++;
  celt_trace_preemphasis_call *trace;
  int input_count = upsample > 0 ? N / upsample : -1;
  if (call >= CELT_TRACE_MAX_CALLS || call != celt_encode_trace.stored_preemphasis_calls ||
      call >= (uint32_t)CC || channel < 0 || channel >= CC ||
      pcmp == NULL || inp == NULL || mem == NULL || coef == NULL || N < 0 ||
      CC <= 0 || CC > 2 || upsample <= 0 || input_count < 0 ||
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
      (uint32_t)call >= celt_encode_trace.stored_preemphasis_calls || inp == NULL || mem == NULL ||
      N < 0 || !trace_dimensions(N, CELT_TRACE_MAX_FLOATS)) {
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
  if (!celt_trace_selected_frame()) {
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

extern void __real_compute_band_energies(const CELTMode *m, const celt_sig *X,
    celt_ener *bandE, int end, int C, int LM, int arch);
void __wrap_compute_band_energies(const CELTMode *m, const celt_sig *X,
    celt_ener *bandE, int end, int C, int LM, int arch) {
  __real_compute_band_energies(m, X, bandE, end, C, LM, arch);
  if (!celt_trace_selected_frame()) return;
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
  if (!celt_trace_selected_frame()) return;
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
  if (!celt_trace_selected_frame()) return;
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
  if (!celt_trace_selected_frame()) {
    __real_quant_coarse_energy(m, start, end, effEnd, eBands, oldEBands, budget,
        error, enc, C, LM, nbAvailableBytes, force_intra, delayedIntra,
        two_pass, loss_rate, lfe);
    return;
  }
  uint32_t call = celt_encode_trace.coarse_calls++;
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
    opus_int32 balance, ec_ctx *ec, int LM, int codedBands, opus_uint32 *seed,
    int complexity, int arch, int disable_inv
    ARG_QEXT(ec_ctx *ext_ec) ARG_QEXT(int *extra_pulses)
    ARG_QEXT(opus_int32 total_ext_bits) ARG_QEXT(const int *cap));
void __wrap_quant_all_bands(int encode, const CELTMode *m, int start, int end,
    celt_norm *X, celt_norm *Y, unsigned char *collapse_masks,
    const celt_ener *bandE, int *pulses, int shortBlocks, int spread,
    int dual_stereo, int intensity, int *tf_res, opus_int32 total_bits,
    opus_int32 balance, ec_ctx *ec, int LM, int codedBands, opus_uint32 *seed,
    int complexity, int arch, int disable_inv
    ARG_QEXT(ec_ctx *ext_ec) ARG_QEXT(int *extra_pulses)
    ARG_QEXT(opus_int32 total_ext_bits) ARG_QEXT(const int *cap)) {
  if (!celt_trace_selected_frame()) {
    __real_quant_all_bands(encode, m, start, end, X, Y, collapse_masks, bandE,
        pulses, shortBlocks, spread, dual_stereo, intensity, tf_res, total_bits,
        balance, ec, LM, codedBands, seed, complexity, arch, disable_inv
        ARG_QEXT(ext_ec) ARG_QEXT(extra_pulses) ARG_QEXT(total_ext_bits) ARG_QEXT(cap));
    return;
  }
  uint32_t call = celt_encode_trace.quant_calls++;
  /* quant_all_bands receives LM and expands M = 1 << LM before indexing X/Y. */
  int active = (1 << LM) * m->eBands[end];
  int channels = Y != NULL ? 2 : 1;
  if (call < CELT_TRACE_MAX_CALLS && trace_dimensions(active * channels, CELT_TRACE_MAX_FLOATS) &&
      trace_dimensions((end - start) * channels, CELT_TRACE_MAX_BANDS * 2)) {
    celt_trace_quant_call *trace = &celt_encode_trace.quant[call];
    trace->active_coeffs = (uint32_t)active;
    trace->bands = (uint32_t)(end - start);
    trace->channels = Y != NULL ? 2u : 1u;
    trace_copy_bands(trace->band_energy, bandE + start, end - start, (int)trace->channels, m->nbEBands);
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

static int write_celt_encode_trace(void) {
#ifdef GOPUS_CELT_PITCH_KERNEL_TRACE
  if (!write_exact("GCET", 4) || !write_u32(6) ||
#elif defined(GOPUS_CELT_PITCH_TRACE)
  if (!write_exact("GCET", 4) || !write_u32(5) ||
#else
  if (!write_exact("GCET", 4) || !write_u32(4) ||
#endif
      !write_u32(celt_encode_captured_frame) || !write_u32(celt_encode_trace.overflow)) return 0;
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
    if (!write_u32(trace->lookup_n) || !write_u32(trace->maxshift) ||
        !write_u32(trace->transform_n) || !write_u32(trace->shift) ||
        !write_u32(trace->stride) || !write_u32(trace->overlap) ||
        !write_u32(trace->arch) || !write_u32(trace->fft_n)) return 0;
    uint32_t scale_bits;
    memcpy(&scale_bits, &trace->fft_scale, sizeof(scale_bits));
    if (!write_u32(scale_bits) || !write_u32(trace->input_count) ||
        !write_u32(trace->window_count) || !write_u32(trace->trig_count) ||
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
#ifdef GOPUS_CELT_PITCH_TRACE
  if (!write_u32(celt_encode_trace.pitch_controls_calls) ||
      !write_u32(celt_encode_trace.stored_pitch_controls_calls)) return 0;
  for (uint32_t i = 0; i < celt_encode_trace.stored_pitch_controls_calls; i++) {
    const celt_trace_pitch_controls_call *trace = &celt_encode_trace.pitch_controls[i];
    if (!write_u32(trace->frame_size) || !write_u32(trace->channels) ||
        !write_u32(trace->enabled) || !write_u32(trace->complexity) || !write_u32(trace->arch) ||
        !write_u32(trace->max_period) || !write_u32(trace->min_period) ||
        !trace_write_float32(&trace->tf_estimate, 1) || !trace_write_float32(&trace->tone_freq, 1) ||
        !trace_write_float32(&trace->toneishness, 1) || !trace_write_float32(&trace->max_pitch_ratio, 1)) return 0;
  }
  if (!write_u32(celt_encode_trace.pitch_downsample_calls) ||
      !write_u32(celt_encode_trace.stored_pitch_downsample_calls)) return 0;
  for (uint32_t i = 0; i < celt_encode_trace.stored_pitch_downsample_calls; i++) {
    const celt_trace_pitch_downsample_call *trace = &celt_encode_trace.pitch_downsample[i];
    if (!write_u32(trace->length) || !write_u32(trace->channels) || !write_u32(trace->factor) ||
        !write_u32(trace->arch) || !write_u32(trace->input_count) || !write_u32(trace->output_count) ||
        !trace_write_float32(trace->input, trace->input_count) ||
        !trace_write_float32(trace->output, trace->output_count)) return 0;
  }
  if (!write_u32(celt_encode_trace.pitch_search_calls) ||
      !write_u32(celt_encode_trace.stored_pitch_search_calls)) return 0;
  for (uint32_t i = 0; i < celt_encode_trace.stored_pitch_search_calls; i++) {
    const celt_trace_pitch_search_call *trace = &celt_encode_trace.pitch_search[i];
    if (!write_u32(trace->length) || !write_u32(trace->max_pitch) || !write_u32(trace->x_offset) ||
        !write_u32(trace->arch) || !write_u32(trace->buffer_count) || !write_u32((uint32_t)trace->result) ||
        !trace_write_float32(trace->buffer, trace->buffer_count)) return 0;
  }
  if (!write_u32(celt_encode_trace.remove_doubling_calls) ||
      !write_u32(celt_encode_trace.stored_remove_doubling_calls)) return 0;
  for (uint32_t i = 0; i < celt_encode_trace.stored_remove_doubling_calls; i++) {
    const celt_trace_remove_doubling_call *trace = &celt_encode_trace.remove_doubling[i];
    if (!write_u32(trace->max_period) || !write_u32(trace->min_period) || !write_u32(trace->n) ||
        !write_u32(trace->arch) || !write_u32(trace->buffer_count) ||
        !write_u32((uint32_t)trace->t0_before) || !write_u32((uint32_t)trace->prev_period) ||
        !write_u32((uint32_t)trace->t0_after) || !trace_write_float32(&trace->prev_gain, 1) ||
        !trace_write_float32(&trace->gain, 1) ||
        !trace_write_float32(trace->buffer, trace->buffer_count)) return 0;
  }
#ifdef GOPUS_CELT_PITCH_KERNEL_TRACE
  if (!write_u32(celt_encode_trace.pitch_autocorr_calls) ||
      !write_u32(celt_encode_trace.stored_pitch_autocorr_calls)) return 0;
  for (uint32_t i = 0; i < celt_encode_trace.stored_pitch_autocorr_calls; i++) {
    const celt_trace_pitch_autocorr_call *trace = &celt_encode_trace.pitch_autocorr[i];
    if (!write_u32(trace->downsample_ordinal) || !write_u32(trace->n) ||
        !write_u32(trace->lag) || !write_u32(trace->overlap) || !write_u32(trace->arch) ||
        !write_u32(trace->window_nil) || !write_u32(trace->input_count) ||
        !write_u32(trace->autocorrelation_count) || !write_u32((uint32_t)trace->result) ||
        !trace_write_float32(trace->input, trace->input_count) ||
        !trace_write_float32(trace->autocorrelation, trace->autocorrelation_count)) return 0;
  }
  if (!write_u32(celt_encode_trace.pitch_lpc_calls) ||
      !write_u32(celt_encode_trace.stored_pitch_lpc_calls)) return 0;
  for (uint32_t i = 0; i < celt_encode_trace.stored_pitch_lpc_calls; i++) {
    const celt_trace_pitch_lpc_call *trace = &celt_encode_trace.pitch_lpc[i];
    if (!write_u32(trace->downsample_ordinal) || !write_u32(trace->order) ||
        !write_u32(trace->autocorrelation_count) || !write_u32(trace->coefficient_count) ||
        !trace_write_float32(trace->autocorrelation, trace->autocorrelation_count) ||
        !trace_write_float32(trace->coefficients, trace->coefficient_count)) return 0;
  }
#endif
#endif
  return 1;
}
#endif

int main(void) {
  if (!set_binary_stdio()) {
    fprintf(stderr, "set_binary_stdio failed\n");
    return 1;
  }

  /* Read and validate input header magic */
  char magic[4];
  if (!read_exact(magic, 4) || memcmp(magic, INPUT_MAGIC, 4) != 0) {
    fprintf(stderr, "bad input magic\n");
    return 1;
  }
  uint32_t version;
  if (!read_u32(&version) || version != 1) {
    fprintf(stderr, "bad input version %u\n", version);
    return 1;
  }

  uint32_t app_code, bandwidth, channels, bitrate, frame_size, complexity, num_frames;
  if (!read_u32(&app_code)   || !read_u32(&bandwidth)  || !read_u32(&channels) ||
      !read_u32(&bitrate)    || !read_u32(&frame_size)  || !read_u32(&complexity) ||
      !read_u32(&num_frames)) {
    fprintf(stderr, "truncated header\n");
    return 1;
  }

  /* Map application code to libopus constant */
  int application;
  switch (app_code) {
    case 0: application = OPUS_APPLICATION_AUDIO;             break;
    case 1: application = OPUS_APPLICATION_VOIP;              break;
    case 2: application = OPUS_APPLICATION_RESTRICTED_SILK;   break;
    case 3: application = OPUS_APPLICATION_RESTRICTED_CELT;   break;
    default:
      fprintf(stderr, "unknown application code %u\n", app_code);
      return 1;
  }

  /* Validate bandwidth is a valid Opus bandwidth constant */
  if (bandwidth != OPUS_BANDWIDTH_NARROWBAND    &&
      bandwidth != OPUS_BANDWIDTH_MEDIUMBAND    &&
      bandwidth != OPUS_BANDWIDTH_WIDEBAND      &&
      bandwidth != OPUS_BANDWIDTH_SUPERWIDEBAND &&
      bandwidth != OPUS_BANDWIDTH_FULLBAND) {
    fprintf(stderr, "unknown bandwidth %u\n", bandwidth);
    return 1;
  }

  if (channels < 1 || channels > 2) {
    fprintf(stderr, "unsupported channels %u\n", channels);
    return 1;
  }
  if (frame_size == 0 || num_frames == 0) {
    fprintf(stderr, "invalid frame_size=%u num_frames=%u\n", frame_size, num_frames);
    return 1;
  }
  if (complexity > 10) {
    fprintf(stderr, "invalid complexity %u\n", complexity);
    return 1;
  }

  /* Create encoder — always at 48 kHz, matching gopus internal rate */
  int err = OPUS_OK;
  OpusEncoder *enc = opus_encoder_create(48000, (int)channels, application, &err);
  if (enc == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_encoder_create failed: %d\n", err);
    return 1;
  }

  /* libopus CBR setup: VBR=0, CVBR=0 — mirrors opus_demo -cbr
   * Reference: src/opus_demo.c lines that handle "-cbr":
   *   opus_encoder_ctl(enc, OPUS_SET_VBR(0)) */
  if (opus_encoder_ctl(enc, OPUS_SET_VBR(0)) != OPUS_OK) {
    fprintf(stderr, "OPUS_SET_VBR(0) failed\n");
    opus_encoder_destroy(enc);
    return 1;
  }
  if (opus_encoder_ctl(enc, OPUS_SET_BITRATE((opus_int32)bitrate)) != OPUS_OK) {
    fprintf(stderr, "OPUS_SET_BITRATE failed\n");
    opus_encoder_destroy(enc);
    return 1;
  }
  if (opus_encoder_ctl(enc, OPUS_SET_BANDWIDTH((opus_int32)bandwidth)) != OPUS_OK) {
    fprintf(stderr, "OPUS_SET_BANDWIDTH failed\n");
    opus_encoder_destroy(enc);
    return 1;
  }
  if (opus_encoder_ctl(enc, OPUS_SET_COMPLEXITY((opus_int32)complexity)) != OPUS_OK) {
    fprintf(stderr, "OPUS_SET_COMPLEXITY failed\n");
    opus_encoder_destroy(enc);
    return 1;
  }

  /* Allocate PCM buffer and packet buffer */
  size_t samples_per_frame = (size_t)frame_size * (size_t)channels;
  float *pcm = (float *)malloc(samples_per_frame * sizeof(float));
  if (pcm == NULL) {
    fprintf(stderr, "pcm malloc failed\n");
    opus_encoder_destroy(enc);
    return 1;
  }
  unsigned char *pkt_buf = (unsigned char *)malloc(MAX_PACKET_BYTES);
  if (pkt_buf == NULL) {
    fprintf(stderr, "packet buf malloc failed\n");
    free(pcm);
    opus_encoder_destroy(enc);
    return 1;
  }

  /* Collect each frame before writing so its packet and final range stay
   * associated even if libopus ever emits a zero-byte DTX frame. */
  unsigned char **packets = (unsigned char **)malloc(num_frames * sizeof(unsigned char *));
  int *packet_lens = (int *)malloc(num_frames * sizeof(int));
  uint32_t *final_ranges = (uint32_t *)malloc(num_frames * sizeof(uint32_t));
  if (packets == NULL || packet_lens == NULL || final_ranges == NULL) {
    fprintf(stderr, "packet array malloc failed\n");
    free(final_ranges);
    free(packet_lens);
    free(packets);
    free(pkt_buf);
    free(pcm);
    opus_encoder_destroy(enc);
    return 1;
  }
  uint32_t actual_frames = 0;

  for (uint32_t f = 0; f < num_frames; f++) {
    /* Read PCM: float32 LE samples interleaved by channel */
    for (size_t s = 0; s < samples_per_frame; s++) {
      unsigned char raw[4];
      uint32_t bits;
      if (!read_exact(raw, 4)) {
        fprintf(stderr, "truncated PCM at frame %u sample %zu\n", f, s);
        goto cleanup_fail;
      }
      bits = (uint32_t)raw[0] | ((uint32_t)raw[1] << 8) |
             ((uint32_t)raw[2] << 16) | ((uint32_t)raw[3] << 24);
      memcpy(&pcm[s], &bits, sizeof(float));
    }

#ifdef GOPUS_CELT_TRACE
    celt_encode_active_frame = f;
#endif
    int n = opus_encode_float(enc, pcm, (int)frame_size, pkt_buf, MAX_PACKET_BYTES);
    if (n < 0) {
      fprintf(stderr, "opus_encode_float frame %u failed: %d\n", f, n);
      goto cleanup_fail;
    }
    opus_uint32 final_range = 0;
    if (opus_encoder_ctl(enc, OPUS_GET_FINAL_RANGE(&final_range)) != OPUS_OK) {
      fprintf(stderr, "OPUS_GET_FINAL_RANGE frame %u failed\n", f);
      goto cleanup_fail;
    }
    packets[actual_frames] = NULL;
    if (n > 0) {
      packets[actual_frames] = (unsigned char *)malloc((size_t)n);
      if (packets[actual_frames] == NULL) {
        fprintf(stderr, "packet copy malloc failed at frame %u\n", f);
        goto cleanup_fail;
      }
      memcpy(packets[actual_frames], pkt_buf, (size_t)n);
    }
    packet_lens[actual_frames] = n;
    final_ranges[actual_frames] = (uint32_t)final_range;
    actual_frames++;
  }

  /* Report the version string, generated config macros, and runtime dispatch
   * selected by the same archive used for encoding. */
  const char *version_string = opus_get_version_string();
  uint32_t version_len = (uint32_t)strlen(version_string);
  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(2) ||
      !write_u32(version_len) || !write_exact(version_string, version_len) ||
      !write_u32((uint32_t)OPUS_ARCHMASK) || !write_u32(build_feature_bits()) ||
      !write_u32((uint32_t)opus_select_arch()) || !write_u32(actual_frames)) {
    fprintf(stderr, "write output header failed\n");
    goto cleanup_fail;
  }
  /* Write each packet and its range-coder state */
  for (uint32_t i = 0; i < actual_frames; i++) {
    if (!write_u32((uint32_t)packet_lens[i]) || !write_u32(final_ranges[i]) ||
        !write_exact(packets[i], (size_t)packet_lens[i])) {
      fprintf(stderr, "write frame %u failed\n", i);
      goto cleanup_fail;
    }
  }
#ifdef GOPUS_CELT_TRACE
  if (!write_celt_encode_trace()) {
    fprintf(stderr, "write CELT stage trace failed\n");
    goto cleanup_fail;
  }
#endif

  for (uint32_t i = 0; i < actual_frames; i++) free(packets[i]);
  free(packets);
  free(packet_lens);
  free(final_ranges);
  free(pkt_buf);
  free(pcm);
  opus_encoder_destroy(enc);
  return 0;

cleanup_fail:
  for (uint32_t i = 0; i < actual_frames; i++) free(packets[i]);
  free(packets);
  free(packet_lens);
  free(final_ranges);
  free(pkt_buf);
  free(pcm);
  opus_encoder_destroy(enc);
  return 1;
}
