/* libopus_silk_ctrl_info.c
 *
 * Frame-level SILK encoder-control oracle.
 *
 * Encodes a PCM stream with libopus 1.6.1 (VBR, CVBR, or CBR) and, for every internal
 * SILK frame, dumps the fully-populated silk_encoder_control_FLP state that
 * drives NSQ + rate control, plus the chosen per-SILK-frame payload size.
 *
 * The dump is produced by linking tools/csrc/silk_encode_frame_FLP_dump.c (a
 * verbatim copy of silk/float/encode_frame_FLP.c with oracle callbacks) BEFORE
 * libopus.a, so this oracle's silk_encode_frame_FLP overrides the archived one
 * and all other libopus code is reused unchanged.
 *
 * Input wire format (little-endian), identical header to the VBR/CVBR oracle:
 *
 *   magic "GSCI" + u32(version=1) + u32(mode) + u32(application)
 *        + u32(sample_rate) + u32(channels) + u32(frame_size)
 *        + u32(bitrate) + u32(bandwidth) + u32(signal) + u32(n_frames)
 *        then n_frames * frame_size * channels float32 samples.
 *
 *   mode: 0 = VBR, 1 = CVBR, 2 = CBR. Other fields match
 *   libopus_vbr_cvbr_encode_info.c.
 *
 * Output wire format:
 *
 *   magic "GSCO" + u32(version=8) + u32(n_frames)
 *   then n_frames packet records: u32(packet_len) u32(final_range) bytes[len]
 *   then u32(n_ctrl)
 *   then n_ctrl control records, each:
 *     u32(opus_frame_index)   // which opus_encode call
 *     u32(channel)            // 0 = mid/mono, 1 = side
 *     u32(nb_subfr)
 *     i32(signalType)
 *     i32(quantOffsetType)
 *     i32(maxBits)
 *     i32(useCBR)
 *     i32(nBytes)             // chosen SILK-frame payload size (-1 if unknown)
 *     f32(predGain)
 *     f32(LTPredCodGain)
 *     f32(Lambda)
 *     f32(input_quality)
 *     f32(coding_quality)
 *     i32 GainsUnq_Q16[4]
 *     f32 Gains[4]            // post-quant gains (Q0)
 *     f32 AR[4*16]
 *     f32 LF_MA_shp[4]
 *     f32 LF_AR_shp[4]
 *     f32 Tilt[4]
 *     f32 HarmShapeGain[4]
 *     f32 LTPCoef[5*4]
 *     f32 LTP_scale
 *     i32 pitchL[4]
 *   then u32(n_stage)
 *   then n_stage rate-control stage records, each:
 *     i32(frame), i32(channel), i32(iteration), i32(stage), i32(tell), u32(range)
 *     i32(signalType), i32(quantOffsetType), i32(seed), i32(lagIndex)
 *     i32(contourIndex), i32(NLSFInterpCoef_Q2), i32(PERIndex), i32(LTP_scaleIndex)
 *     i32 GainsIndices[4], i32 LTPIndex[4], i32 NLSFIndices[17]
 *     i32(pulse_count), i8 pulses[pulse_count]
 *     stage 0 is immediately after NSQ, 1 is after index coding, and 2 is after
 *     pulse coding. Pulses are present only at stage 0. Records cover request
 *     frames 6, 13, and 50 when present. The final i32 is nonzero if the trace
 *     buffer overflows or a traced frame exceeds MAX_FRAME_LENGTH.
 *   then u32(n_lpc_calls), followed by actual Linux silk_find_LPC_FLP call records:
 *     i32(opus_frame), i32(channel), i32(nFramesEncoded), i32(API_fs_Hz)
 *     i32(fs_kHz), i32(frame_length), i32(subfr_length), i32(nb_subfr)
 *     i32(predictLPCOrder), i32(useInterpolatedNLSFs), i32(first_frame_after_reset)
 *     i32(arch), f32(minInvGain), i32(prev_NLSFq_Q15[16])
 *     u32(input_count), f32(input[input_count])
 *     i32(NLSFInterpCoef_Q2 after the call), i32(NLSF_Q15[16] after the call)
 *   then i32(lpc_trace_overflow).
 *   then u32(n_ltp_contexts), followed in Linux wrapped builds by records
 *   for bounded target frames 6, 13, and 50 when present, one per SILK channel:
 *     i32(opus_frame), i32(channel), i32(signalType), i32(filterCalled)
 *     i32(subfr_length), i32(nb_subfr), i32(pre_length), u32(output_count)
 *     f32(Gains[nb_subfr]) captured before the LTP-filter reciprocal;
 *     when filterCalled: for each subframe, i32(pitchL), f32(invGain),
 *       f32(B[LTP_ORDER]); then for each output sample in subframe order,
 *       f32(x), f32(lag[LTP_ORDER]), f32(actual LTP_res output).
 *   then i32(ltp_trace_overflow).
 *   then u32(n_gain_tweak_records), followed by actual noise-shape gain-tweak
 *   records for bounded frames 6, 13, and 50 when present, one per SILK channel:
 *     i32(frame), i32(channel), i32(nb_subfr), i32(shapingLPCOrder),
 *     i32(warping_Q16), i32(pow_call_count), u32(pre_gain_mask),
 *     f32(gain_mult_exponent), f32(gain_mult), f32(gain_add), then for each
 *     subframe f32(pre-tweak gain), f32(post-tweak gain); final i32 overflow.
 *   The pre-tweak gains come from the first actual silk_bwexpander_FLP call
 *   for each control AR row; the active power call returns gain_mult and the
 *   gain_add bits match the pinned C expression's constant-folded literal.
 *   then u32(n_noise_shape_records), followed by actual noise-shape analysis
 *   records for frames 6, 13, and 50 when present, one per subframe and SILK
 *   channel. The record captures the regular or warped autocorrelation branch
 *   selected by the real noise-shape call:
 *     i32(frame), i32(channel), i32(subframe), i32(numSubframes), i32(order),
 *     i32(windowLength), i32(warping_Q16), i32(autoCorrCalls), i32(schurCalls),
 *     f32(autoCorrWarping),
 *     f32(window[windowLength]), f32(rawAutoCorr[order+1]),
 *     f32(adjustedAutoCorr[order+1]), f32(rc[order]), f32(nrg)
 *   then i32(noise_shape_failure_flags). Windows are copied from the input to the
 *   real autocorrelation function selected by noise_shape_analysis_FLP.c;
 *   raw correlations come from that function's real output, adjusted
 *   correlations from the real silk_schur_FLP input after the pinned
 *   white-noise addition, and rc/nrg from that real Schur call. The
 *   warping_Q16 field identifies whether the regular or warped autocorrelation
 *   branch ran. Incomplete, duplicated, or unsupported selected contexts fail
 *   closed with a nonzero flag. Bit 0 marks an invalid context lifecycle, bit 1
 *   invalid dimensions, bit 2 autocorrelation branch/argument/cardinality
 *   failure, and bit 3 Schur pairing/cardinality failure. The sequence follows
 *   silk/float/noise_shape_analysis_FLP.c's window, autocorrelation,
 *   white-noise, and Schur operations.
 *
 * Reference: libopus src/opus_encoder.c opus_encode_float(); the dumped struct
 * is silk/float/structs_FLP.h silk_encoder_control_FLP, captured in
 * silk_encode_frame_FLP (silk/float/encode_frame_FLP.c) just after
 * silk_process_gains_FLP() and at the final payload-size computation.
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
#include "main_FLP.h"

#define INPUT_MAGIC  "GSCI"
#define OUTPUT_MAGIC "GSCO"
#define MAX_PACKET_BYTES 4000
#define MAX_CTRL_RECORDS 4096
#define MAX_TRACE_RECORDS 512
#define MAX_LPC_TRACE_RECORDS 8
#define MAX_LPC_INPUT (MAX_FRAME_LENGTH + MAX_NB_SUBFR * MAX_LPC_ORDER)
#define MAX_LTP_TRACE_RECORDS 8
#define MAX_GAIN_TWEAK_TRACE_RECORDS 8
#define MAX_NOISE_SHAPE_TRACE_RECORDS 32
/* Bounds from silk/define.h: MAX_SHAPE_LPC_ORDER and SHAPE_LPC_WIN_MAX. */
#define MAX_SHAPE_WINDOW SHAPE_LPC_WIN_MAX
#define MAX_SHAPE_ORDER MAX_SHAPE_LPC_ORDER

enum {
  NOISE_SHAPE_FAILURE_CONTEXT = 1 << 0,
  NOISE_SHAPE_FAILURE_GEOMETRY = 1 << 1,
  NOISE_SHAPE_FAILURE_AUTOCORR = 1 << 2,
  NOISE_SHAPE_FAILURE_SCHUR = 1 << 3
};

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin),  _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

static int read_exact(void *dst, size_t n) {
  return fread(dst, 1, n, stdin) == n;
}

static int write_exact(const void *src, size_t n) {
  const unsigned char *p = (const unsigned char *)src;
  size_t off = 0;
  while (off < n) {
    size_t w = fwrite(p + off, 1, n - off, stdout);
    if (w == 0) return 0;
    off += w;
  }
  return 1;
}

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact(b, 4)) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) |
         ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int write_u32(uint32_t v) {
  unsigned char b[4];
  b[0] = (unsigned char)(v & 0xff);
  b[1] = (unsigned char)((v >> 8)  & 0xff);
  b[2] = (unsigned char)((v >> 16) & 0xff);
  b[3] = (unsigned char)((v >> 24) & 0xff);
  return write_exact(b, 4);
}

static int write_i32(int32_t v) { return write_u32((uint32_t)v); }
static int write_f32(float f) {
  uint32_t bits;
  memcpy(&bits, &f, 4);
  return write_u32(bits);
}

/* ---- control record collection ---- */

typedef struct {
  int32_t  opus_frame_index;
  int32_t  channel;
  int32_t  nb_subfr;
  int32_t  signalType;
  int32_t  quantOffsetType;
  int32_t  maxBits;
  int32_t  useCBR;
  int32_t  nBytes;
  float    predGain;
  float    LTPredCodGain;
  float    Lambda;
  float    input_quality;
  float    coding_quality;
  int32_t  SNR_dB_Q7;
  int32_t  input_quality_bands_Q15[4];
  int32_t  speech_activity_Q8;
  int32_t  GainsUnq_Q16[MAX_NB_SUBFR];
  float    Gains[MAX_NB_SUBFR];
  float    AR[MAX_NB_SUBFR * MAX_SHAPE_LPC_ORDER];
  float    LF_MA_shp[MAX_NB_SUBFR];
  float    LF_AR_shp[MAX_NB_SUBFR];
  float    Tilt[MAX_NB_SUBFR];
  float    HarmShapeGain[MAX_NB_SUBFR];
  float    LTPCoef[LTP_ORDER * MAX_NB_SUBFR];
  float    LTP_scale;
  int32_t  pitchL[MAX_NB_SUBFR];
} ctrl_record;

typedef struct {
  int32_t opus_frame_index;
  int32_t channel;
  int32_t iter;
  int32_t stage;
  int32_t tell;
  uint32_t range;
  int32_t signalType;
  int32_t quantOffsetType;
  int32_t seed;
  int32_t lagIndex;
  int32_t contourIndex;
  int32_t nlsfInterp;
  int32_t perIndex;
  int32_t ltpScaleIndex;
  int32_t gains[MAX_NB_SUBFR];
  int32_t ltp[MAX_NB_SUBFR];
  int32_t nlsf[MAX_LPC_ORDER + 1];
  int32_t n_pulses;
  opus_int8 pulses[MAX_FRAME_LENGTH];
} encode_stage_record;

typedef struct {
  int32_t opus_frame_index;
  int32_t channel;
  int32_t nFramesEncoded;
  int32_t API_fs_Hz;
  int32_t fs_kHz;
  int32_t frame_length;
  int32_t subfr_length;
  int32_t nb_subfr;
  int32_t predictLPCOrder;
  int32_t useInterpolatedNLSFs;
  int32_t first_frame_after_reset;
  int32_t arch;
  float minInvGain;
  int32_t prev_NLSFq_Q15[MAX_LPC_ORDER];
  uint32_t input_count;
  float input[MAX_LPC_INPUT];
  int32_t selected_interp;
  int32_t NLSF_Q15[MAX_LPC_ORDER];
} lpc_call_record;

typedef struct {
  int32_t opus_frame_index;
  int32_t channel;
  int32_t signalType;
  int32_t filterCalled;
  int32_t subfr_length;
  int32_t nb_subfr;
  int32_t pre_length;
  uint32_t output_count;
  int32_t pitchL[MAX_NB_SUBFR];
  float Gains[MAX_NB_SUBFR];
  float invGains[MAX_NB_SUBFR];
  float B[LTP_ORDER * MAX_NB_SUBFR];
  float x[MAX_LPC_INPUT];
  float lag[MAX_LPC_INPUT * LTP_ORDER];
  float result[MAX_LPC_INPUT];
} ltp_trace_record;

typedef struct {
  int32_t opus_frame_index;
  int32_t channel;
  int32_t nb_subfr;
  int32_t shaping_lpc_order;
  int32_t warping_Q16;
  int32_t pow_call_count;
  uint32_t pre_gain_mask;
  float gain_mult_exponent;
  float gain_mult;
  float gain_add;
  float pre_gain[MAX_NB_SUBFR];
  float post_gain[MAX_NB_SUBFR];
} gain_tweak_trace_record;

typedef struct {
  int32_t opus_frame_index;
  int32_t channel;
  int32_t subframe;
  int32_t num_subframes;
  int32_t order;
  int32_t window_length;
  int32_t warping_Q16;
  int32_t autoCorrCalls;
  int32_t schurCalls;
  float autoCorrWarping;
  float window[MAX_SHAPE_WINDOW];
  float rawAutoCorr[MAX_SHAPE_ORDER + 1];
  float adjustedAutoCorr[MAX_SHAPE_ORDER + 1];
  float rc[MAX_SHAPE_ORDER];
  float nrg;
} noise_shape_trace_record;

static ctrl_record g_ctrl[MAX_CTRL_RECORDS];
static int         g_ctrl_count = 0;
static encode_stage_record g_stage[MAX_TRACE_RECORDS];
static int         g_stage_count = 0;
static int         g_stage_overflow = 0;
static lpc_call_record g_lpc_call[MAX_LPC_TRACE_RECORDS];
static int         g_lpc_call_count = 0;
static int         g_lpc_call_overflow = 0;
static ltp_trace_record g_ltp_trace[MAX_LTP_TRACE_RECORDS];
static int         g_ltp_trace_count = 0;
static int         g_ltp_trace_overflow = 0;
static gain_tweak_trace_record g_gain_tweak_trace[MAX_GAIN_TWEAK_TRACE_RECORDS];
static int         g_gain_tweak_trace_count = 0;
static int         g_gain_tweak_trace_overflow = 0;
static noise_shape_trace_record g_noise_shape_trace[MAX_NOISE_SHAPE_TRACE_RECORDS];
static int         g_noise_shape_trace_count = 0;
static int         g_noise_shape_trace_overflow = 0;
static int32_t     g_cur_opus_frame = 0;

static int gopus_silk_trace_frame_selected(int32_t frame) {
  return frame == 6 || frame == 13 || frame == 50;
}

/* The two state_Fxx encoder pointers, used to recover the channel index. */
static const void *g_state_ptr[2] = { NULL, NULL };

#ifdef __linux__
static int g_gain_tweak_context_index = -1;
static const silk_encoder_control_FLP *g_gain_tweak_ctrl = NULL;
static int g_noise_shape_context_open = 0;
static int g_noise_shape_context_seen = 0;
static int32_t g_noise_shape_context_frame = -1;
static int32_t g_noise_shape_context_channel = -1;

static silk_float silk_gain_add_arch_literal(void) {
  /* Pinned GCC 13.3 AMD64 v3 scalar/SIMD archives fold
   * noise_shape_analysis_FLP.c:291, with MIN_QGAIN_DB=2 from define.h:119,
   * to this binary32 literal. */
  const uint32_t bits = UINT32_C(0x3f9fc94c);
  silk_float value;
  memcpy(&value, &bits, sizeof(value));
  return value;
}

/* The copied frame wrapper brackets the real noise-shape function. */
void gopus_silk_gain_tweak_set_context(const silk_encoder_state_FLP *psEnc,
    const silk_encoder_control_FLP *psEncCtrl) {
  gain_tweak_trace_record *r;
  g_gain_tweak_context_index = -1;
  g_gain_tweak_ctrl = NULL;
  if (!gopus_silk_trace_frame_selected(g_cur_opus_frame)) return;
  if (g_noise_shape_context_open)
    g_noise_shape_trace_overflow |= NOISE_SHAPE_FAILURE_CONTEXT;
  g_noise_shape_context_open = 1;
  g_noise_shape_context_seen = 0;
  g_noise_shape_context_frame = g_cur_opus_frame;
  g_noise_shape_context_channel = psEnc->sCmn.channelNb;
  if (g_gain_tweak_trace_count >= MAX_GAIN_TWEAK_TRACE_RECORDS) {
    g_gain_tweak_trace_overflow = 1;
    return;
  }
  if (psEnc->sCmn.nb_subfr <= 0 || psEnc->sCmn.nb_subfr > MAX_NB_SUBFR ||
      psEnc->sCmn.shapingLPCOrder <= 0 ||
      psEnc->sCmn.shapingLPCOrder > MAX_SHAPE_LPC_ORDER) {
    g_gain_tweak_trace_overflow = 1;
    return;
  }
  r = &g_gain_tweak_trace[g_gain_tweak_trace_count];
  memset(r, 0, sizeof(*r));
  r->opus_frame_index = g_cur_opus_frame;
  r->channel = psEnc->sCmn.channelNb;
  r->nb_subfr = psEnc->sCmn.nb_subfr;
  r->shaping_lpc_order = psEnc->sCmn.shapingLPCOrder;
  r->warping_Q16 = psEnc->sCmn.warping_Q16;
  r->gain_add = silk_gain_add_arch_literal();
  g_gain_tweak_ctrl = psEncCtrl;
  g_gain_tweak_context_index = g_gain_tweak_trace_count++;
}

void gopus_silk_gain_tweak_finish_context(
    const silk_encoder_control_FLP *psEncCtrl) {
  if (g_gain_tweak_context_index >= 0) {
    gain_tweak_trace_record *r = &g_gain_tweak_trace[g_gain_tweak_context_index];
    int k;
    for (k = 0; k < r->nb_subfr; k++) r->post_gain[k] = psEncCtrl->Gains[k];
    if (r->pow_call_count != 1 ||
        r->pre_gain_mask != ((UINT32_C(1) << r->nb_subfr) - 1)) {
      g_gain_tweak_trace_overflow = 1;
    }
  }
  if (g_noise_shape_context_open) {
    if (!g_noise_shape_context_seen ||
        g_noise_shape_context_frame != g_cur_opus_frame) {
      g_noise_shape_trace_overflow |= NOISE_SHAPE_FAILURE_CONTEXT;
    }
    g_noise_shape_context_open = 0;
    g_noise_shape_context_seen = 0;
    g_noise_shape_context_frame = -1;
    g_noise_shape_context_channel = -1;
  }
  g_gain_tweak_context_index = -1;
  g_gain_tweak_ctrl = NULL;
}

extern double __real_pow(double x, double y);
double __wrap_pow(double x, double y) {
  double result = __real_pow(x, y);
  if (g_gain_tweak_context_index >= 0) {
    gain_tweak_trace_record *r = &g_gain_tweak_trace[g_gain_tweak_context_index];
    r->pow_call_count++;
    if (x == 2.0 && r->pow_call_count == 1) {
      /* pow has a C double interface; SILK stores the result as silk_float. */
      r->gain_mult_exponent = (float)y;
      r->gain_mult = (float)result;
    } else {
      g_gain_tweak_trace_overflow = 1;
    }
  }
  return result;
}

extern void __real_silk_bwexpander_FLP(
    silk_float *ar, const opus_int d, const silk_float chirp);
void __wrap_silk_bwexpander_FLP(
    silk_float *ar, const opus_int d, const silk_float chirp) {
  if (g_gain_tweak_context_index >= 0) {
    gain_tweak_trace_record *r = &g_gain_tweak_trace[g_gain_tweak_context_index];
    /* The active row pointer is the call site's stable subframe identifier. */
    int k;
    for (k = 0; k < r->nb_subfr; k++) {
      uint32_t bit = UINT32_C(1) << k;
      if (ar == &g_gain_tweak_ctrl->AR[k * MAX_SHAPE_LPC_ORDER] &&
          (r->pre_gain_mask & bit) == 0) {
        r->pre_gain[k] = g_gain_tweak_ctrl->Gains[k];
        r->pre_gain_mask |= bit;
        break;
      }
    }
  }
  __real_silk_bwexpander_FLP(ar, d, chirp);
}
#else
void gopus_silk_gain_tweak_set_context(const silk_encoder_state_FLP *psEnc,
    const silk_encoder_control_FLP *psEncCtrl) {
  (void)psEnc;
  (void)psEncCtrl;
}
void gopus_silk_gain_tweak_finish_context(
    const silk_encoder_control_FLP *psEncCtrl) {
  (void)psEncCtrl;
}
#endif

#ifdef __linux__
static int g_noise_shape_active = 0;
static int g_noise_shape_frame = -1;
static int g_noise_shape_channel = -1;
static int g_noise_shape_num_subframes = 0;
static int g_noise_shape_order = 0;
static int g_noise_shape_window_length = 0;
static int g_noise_shape_warping_Q16 = 0;
static int g_noise_shape_auto_calls = 0;
static int g_noise_shape_schur_calls = 0;
static int g_noise_shape_first_record = 0;
static int g_noise_shape_current_record = -1;
static const silk_float *g_noise_shape_corr_results = NULL;

static int silk_float_is_finite(silk_float value) {
  uint32_t bits;
  memcpy(&bits, &value, sizeof(bits));
  return (bits & UINT32_C(0x7f800000)) != UINT32_C(0x7f800000);
}

static noise_shape_trace_record *gopus_silk_noise_shape_autocorr_begin(
    const silk_float *input,
    opus_int input_length,
    opus_int order,
    int warped,
    silk_float effective_warping) {
  noise_shape_trace_record *r;
  int subframe;
  int expected_warped;
  int record_index;

  if (!g_noise_shape_active) return NULL;
  subframe = g_noise_shape_auto_calls++;
  expected_warped = g_noise_shape_warping_Q16 > 0;
  if (g_noise_shape_current_record >= 0 ||
      subframe >= g_noise_shape_num_subframes ||
      warped != expected_warped ||
      input_length != g_noise_shape_window_length ||
      order != g_noise_shape_order || input_length <= order ||
      input_length > MAX_SHAPE_WINDOW ||
      !silk_float_is_finite(effective_warping) ||
      g_noise_shape_trace_count >= MAX_NOISE_SHAPE_TRACE_RECORDS) {
    g_noise_shape_trace_overflow |= NOISE_SHAPE_FAILURE_AUTOCORR;
    return NULL;
  }

  record_index = g_noise_shape_trace_count++;
  r = &g_noise_shape_trace[record_index];
  memset(r, 0, sizeof(*r));
  r->opus_frame_index = g_noise_shape_frame;
  r->channel = g_noise_shape_channel;
  r->subframe = subframe;
  r->num_subframes = g_noise_shape_num_subframes;
  r->order = g_noise_shape_order;
  r->window_length = input_length;
  r->warping_Q16 = g_noise_shape_warping_Q16;
  r->autoCorrCalls = 1;
  r->autoCorrWarping = effective_warping;
  memcpy(r->window, input, (size_t)input_length * sizeof(r->window[0]));
  g_noise_shape_current_record = record_index;
  g_noise_shape_corr_results = NULL;
  return r;
}

static void gopus_silk_noise_shape_autocorr_finish(
    noise_shape_trace_record *r,
    const silk_float *corr,
    opus_int order) {
  int k;
  if (r == NULL) return;
  g_noise_shape_corr_results = corr;
  for (k = 0; k <= order; k++) r->rawAutoCorr[k] = corr[k];
}

extern void __real_silk_noise_shape_analysis_FLP(
    silk_encoder_state_FLP *psEnc,
    silk_encoder_control_FLP *psEncCtrl,
    const silk_float *pitch_res,
    const silk_float *x);

void __wrap_silk_noise_shape_analysis_FLP(
    silk_encoder_state_FLP *psEnc,
    silk_encoder_control_FLP *psEncCtrl,
    const silk_float *pitch_res,
    const silk_float *x) {
  int selected = gopus_silk_trace_frame_selected(g_cur_opus_frame);
  int num_subframes, order, window_length, warping_Q16;

  if (!selected) {
    __real_silk_noise_shape_analysis_FLP(psEnc, psEncCtrl, pitch_res, x);
    return;
  }
  if (!g_noise_shape_context_open || g_noise_shape_context_seen ||
      g_noise_shape_context_frame != g_cur_opus_frame ||
      g_noise_shape_context_channel != psEnc->sCmn.channelNb) {
    g_noise_shape_trace_overflow |= NOISE_SHAPE_FAILURE_CONTEXT;
    __real_silk_noise_shape_analysis_FLP(psEnc, psEncCtrl, pitch_res, x);
    return;
  }
  g_noise_shape_context_seen = 1;
  if (g_noise_shape_active) {
    g_noise_shape_trace_overflow |= NOISE_SHAPE_FAILURE_CONTEXT;
    __real_silk_noise_shape_analysis_FLP(psEnc, psEncCtrl, pitch_res, x);
    return;
  }

  num_subframes = psEnc->sCmn.nb_subfr;
  order = psEnc->sCmn.shapingLPCOrder;
  window_length = psEnc->sCmn.shapeWinLength;
  warping_Q16 = psEnc->sCmn.warping_Q16;
  if (psEnc->sCmn.channelNb < 0 || psEnc->sCmn.channelNb > 1 ||
      num_subframes < 1 || num_subframes > MAX_NB_SUBFR ||
      order < 1 || order > MAX_SHAPE_ORDER ||
      window_length <= order || window_length > MAX_SHAPE_WINDOW ||
      warping_Q16 < 0 || warping_Q16 > 32767) {
    g_noise_shape_trace_overflow |= NOISE_SHAPE_FAILURE_GEOMETRY;
    __real_silk_noise_shape_analysis_FLP(psEnc, psEncCtrl, pitch_res, x);
    return;
  }

  g_noise_shape_active = 1;
  g_noise_shape_frame = g_cur_opus_frame;
  g_noise_shape_channel = psEnc->sCmn.channelNb;
  g_noise_shape_num_subframes = num_subframes;
  g_noise_shape_order = order;
  g_noise_shape_window_length = window_length;
  g_noise_shape_warping_Q16 = warping_Q16;
  g_noise_shape_auto_calls = 0;
  g_noise_shape_schur_calls = 0;
  g_noise_shape_first_record = g_noise_shape_trace_count;
  g_noise_shape_current_record = -1;
  g_noise_shape_corr_results = NULL;

  __real_silk_noise_shape_analysis_FLP(psEnc, psEncCtrl, pitch_res, x);

  if (g_noise_shape_auto_calls != num_subframes ||
      g_noise_shape_trace_count - g_noise_shape_first_record != num_subframes ||
      g_noise_shape_current_record >= 0)
    g_noise_shape_trace_overflow |= NOISE_SHAPE_FAILURE_AUTOCORR;
  if (g_noise_shape_schur_calls != num_subframes)
    g_noise_shape_trace_overflow |= NOISE_SHAPE_FAILURE_SCHUR;
  g_noise_shape_active = 0;
  g_noise_shape_frame = -1;
  g_noise_shape_channel = -1;
  g_noise_shape_current_record = -1;
  g_noise_shape_corr_results = NULL;
}

extern void __real_silk_autocorrelation_FLP(
    silk_float *results,
    const silk_float *inputData,
    opus_int inputDataSize,
    opus_int correlationCount,
    int arch);

void __wrap_silk_autocorrelation_FLP(
    silk_float *results,
    const silk_float *inputData,
    opus_int inputDataSize,
    opus_int correlationCount,
    int arch) {
  opus_int order = g_noise_shape_order;
  noise_shape_trace_record *r;
  if (g_noise_shape_active && correlationCount != order + 1) {
    g_noise_shape_trace_overflow |= NOISE_SHAPE_FAILURE_AUTOCORR;
    order = -1;
  }
  r = gopus_silk_noise_shape_autocorr_begin(
      inputData, inputDataSize, order, 0, 0.0f);

  __real_silk_autocorrelation_FLP(results, inputData, inputDataSize,
      correlationCount, arch);

  gopus_silk_noise_shape_autocorr_finish(r, results, order);
}

extern void __real_silk_warped_autocorrelation_FLP(
    silk_float *corr,
    const silk_float *input,
    const silk_float warping,
    const opus_int length,
    const opus_int order);

void __wrap_silk_warped_autocorrelation_FLP(
    silk_float *corr,
    const silk_float *input,
    const silk_float warping,
    const opus_int length,
    const opus_int order) {
  noise_shape_trace_record *r = gopus_silk_noise_shape_autocorr_begin(
      input, length, order, 1, warping);

  __real_silk_warped_autocorrelation_FLP(corr, input, warping, length, order);

  gopus_silk_noise_shape_autocorr_finish(r, corr, order);
}

extern silk_float __real_silk_schur_FLP(
    silk_float refl_coef[],
    const silk_float auto_corr[],
    opus_int order);

silk_float __wrap_silk_schur_FLP(
    silk_float refl_coef[],
    const silk_float auto_corr[],
    opus_int order) {
  noise_shape_trace_record *r = NULL;
  silk_float nrg;

  if (g_noise_shape_active) {
    g_noise_shape_schur_calls++;
    if (g_noise_shape_current_record < 0 || order != g_noise_shape_order ||
        auto_corr != g_noise_shape_corr_results) {
      g_noise_shape_trace_overflow |= NOISE_SHAPE_FAILURE_SCHUR;
      g_noise_shape_current_record = -1;
      g_noise_shape_corr_results = NULL;
    } else {
      r = &g_noise_shape_trace[g_noise_shape_current_record];
      r->schurCalls = 1;
      memcpy(r->adjustedAutoCorr, auto_corr,
          (size_t)(order + 1) * sizeof(r->adjustedAutoCorr[0]));
      g_noise_shape_current_record = -1;
      g_noise_shape_corr_results = NULL;
    }
  }

  nrg = __real_silk_schur_FLP(refl_coef, auto_corr, order);

  if (r != NULL) {
    int k;
    for (k = 0; k < order; k++) r->rc[k] = refl_coef[k];
    r->nrg = nrg;
  }
  return nrg;
}
#endif

#ifdef __linux__
static int g_ltp_context_index = -1;

/* The copied encode-frame wrapper sets this immediately around the actual
 * silk_find_pred_coefs_FLP call. Creating a context before the call also makes
 * an unvoiced/no-filter branch explicit in the trace output. */
void gopus_silk_ltp_set_context(const silk_encoder_state_FLP *psEnc,
    const silk_encoder_control_FLP *psEncCtrl) {
  ltp_trace_record *r;
  g_ltp_context_index = -1;
  if (!gopus_silk_trace_frame_selected(g_cur_opus_frame)) return;
  if (g_ltp_trace_count >= MAX_LTP_TRACE_RECORDS) {
    g_ltp_trace_overflow = 1;
    return;
  }
  r = &g_ltp_trace[g_ltp_trace_count];
  memset(r, 0, sizeof(*r));
  r->opus_frame_index = g_cur_opus_frame;
  r->channel = psEnc->sCmn.channelNb;
  r->signalType = psEnc->sCmn.indices.signalType;
  r->subfr_length = psEnc->sCmn.subfr_length;
  r->nb_subfr = psEnc->sCmn.nb_subfr;
  r->pre_length = psEnc->sCmn.predictLPCOrder;
  if (r->nb_subfr <= 0 || r->nb_subfr > MAX_NB_SUBFR) {
    g_ltp_trace_overflow = 1;
    return;
  }
  {
    int k;
    for (k = 0; k < r->nb_subfr; k++) r->Gains[k] = psEncCtrl->Gains[k];
  }
  g_ltp_context_index = g_ltp_trace_count++;
}

void gopus_silk_ltp_clear_context(void) {
  g_ltp_context_index = -1;
}
#else
void gopus_silk_ltp_set_context(const silk_encoder_state_FLP *psEnc,
    const silk_encoder_control_FLP *psEncCtrl) {
  (void)psEnc;
  (void)psEncCtrl;
}

void gopus_silk_ltp_clear_context(void) {
}
#endif

#ifdef __linux__
extern void __real_silk_LTP_analysis_filter_FLP(
    silk_float                      *LTP_res,
    const silk_float                *x,
    const silk_float                B[ LTP_ORDER * MAX_NB_SUBFR ],
    const opus_int                  pitchL[ MAX_NB_SUBFR ],
    const silk_float                invGains[ MAX_NB_SUBFR ],
    const opus_int                  subfr_length,
    const opus_int                  nb_subfr,
    const opus_int                  pre_length );

void __wrap_silk_LTP_analysis_filter_FLP(
    silk_float                      *LTP_res,
    const silk_float                *x,
    const silk_float                B[ LTP_ORDER * MAX_NB_SUBFR ],
    const opus_int                  pitchL[ MAX_NB_SUBFR ],
    const silk_float                invGains[ MAX_NB_SUBFR ],
    const opus_int                  subfr_length,
    const opus_int                  nb_subfr,
    const opus_int                  pre_length )
{
  ltp_trace_record *r = NULL;
  uint64_t output_count = 0;
  if (g_ltp_context_index >= 0) {
    r = &g_ltp_trace[g_ltp_context_index];
    r->filterCalled = 1;
    if (subfr_length > 0 && nb_subfr > 0 && pre_length >= 0) {
      output_count = (uint64_t)(uint32_t)nb_subfr *
          ((uint64_t)(uint32_t)subfr_length + (uint64_t)(uint32_t)pre_length);
    }
    if (subfr_length <= 0 || subfr_length > MAX_FRAME_LENGTH ||
        nb_subfr <= 0 || nb_subfr > MAX_NB_SUBFR ||
        pre_length < 0 || pre_length > MAX_LPC_ORDER ||
        subfr_length != r->subfr_length || nb_subfr != r->nb_subfr ||
        pre_length != r->pre_length || output_count == 0 ||
        output_count > MAX_LPC_INPUT) {
      g_ltp_trace_overflow = 1;
      r = NULL;
    } else {
      int k, i, j, offset = 0;
      r->output_count = (uint32_t)output_count;
      for (k = 0; k < nb_subfr; k++) {
        const silk_float *x_ptr = x + k * subfr_length;
        const silk_float *x_lag_ptr = x_ptr - pitchL[k];
        r->pitchL[k] = pitchL[k];
        r->invGains[k] = invGains[k];
        for (j = 0; j < LTP_ORDER; j++)
          r->B[k * LTP_ORDER + j] = B[k * LTP_ORDER + j];
        for (i = 0; i < subfr_length + pre_length; i++, offset++) {
          r->x[offset] = x_ptr[i];
          for (j = 0; j < LTP_ORDER; j++) {
            r->lag[offset * LTP_ORDER + j] =
                x_lag_ptr[i + LTP_ORDER / 2 - j];
          }
        }
      }
    }
  }

  __real_silk_LTP_analysis_filter_FLP(
      LTP_res, x, B, pitchL, invGains, subfr_length, nb_subfr, pre_length);

  if (r != NULL) {
    uint32_t i;
    for (i = 0; i < r->output_count; i++) r->result[i] = LTP_res[i];
  }
}
#endif

#ifdef __linux__
extern void __real_silk_find_LPC_FLP(
    silk_encoder_state *psEncC,
    opus_int16 NLSF_Q15[],
    const silk_float x[],
    const silk_float minInvGain,
    int arch );

void __wrap_silk_find_LPC_FLP(
    silk_encoder_state *psEncC,
    opus_int16 NLSF_Q15[],
    const silk_float x[],
    const silk_float minInvGain,
    int arch )
{
  lpc_call_record *r = NULL;
  opus_int input_count = 0;
  if( gopus_silk_trace_frame_selected(g_cur_opus_frame) ) {
    if( psEncC->predictLPCOrder <= 0 || psEncC->predictLPCOrder > MAX_LPC_ORDER ||
        psEncC->nb_subfr <= 0 || psEncC->nb_subfr > MAX_NB_SUBFR || psEncC->subfr_length <= 0 ) {
      g_lpc_call_overflow = 1;
    } else {
      input_count = psEncC->nb_subfr *
          ( psEncC->subfr_length + psEncC->predictLPCOrder );
    }
    if( g_lpc_call_count >= MAX_LPC_TRACE_RECORDS || input_count <= 0 || input_count > MAX_LPC_INPUT ) {
      g_lpc_call_overflow = 1;
    } else {
      int i;
      r = &g_lpc_call[ g_lpc_call_count++ ];
      memset( r, 0, sizeof( *r ) );
      r->opus_frame_index = g_cur_opus_frame;
      r->channel = psEncC->channelNb;
      r->nFramesEncoded = psEncC->nFramesEncoded;
      r->API_fs_Hz = psEncC->API_fs_Hz;
      r->fs_kHz = psEncC->fs_kHz;
      r->frame_length = psEncC->frame_length;
      r->subfr_length = psEncC->subfr_length;
      r->nb_subfr = psEncC->nb_subfr;
      r->predictLPCOrder = psEncC->predictLPCOrder;
      r->useInterpolatedNLSFs = psEncC->useInterpolatedNLSFs;
      r->first_frame_after_reset = psEncC->first_frame_after_reset;
      r->arch = arch;
      r->minInvGain = minInvGain;
      r->input_count = (uint32_t)input_count;
      for( i = 0; i < psEncC->predictLPCOrder; i++ ) {
        r->prev_NLSFq_Q15[i] = psEncC->prev_NLSFq_Q15[i];
      }
      memcpy( r->input, x, (size_t)input_count * sizeof( r->input[0] ) );
    }
  }

  __real_silk_find_LPC_FLP( psEncC, NLSF_Q15, x, minInvGain, arch );

  if( r != NULL ) {
    int i;
    r->selected_interp = psEncC->indices.NLSFInterpCoef_Q2;
    for( i = 0; i < psEncC->predictLPCOrder; i++ ) {
      r->NLSF_Q15[i] = NLSF_Q15[i];
    }
  }
}
#endif

void gopus_silk_ctrl_dump(
    const silk_encoder_state_FLP   *psEnc,
    const silk_encoder_control_FLP *psEncCtrl,
    opus_int                        maxBits,
    opus_int                        useCBR )
{
  ctrl_record *r;
  int i, ch = 0;
  if (g_ctrl_count >= MAX_CTRL_RECORDS) return;
  r = &g_ctrl[g_ctrl_count++];
  memset(r, 0, sizeof(*r));

  /* Lazily register the (up to two) distinct SILK encoder-state pointers so we
   * can label channels: state_Fxx[0] = mid/mono, state_Fxx[1] = side. */
  if (g_state_ptr[0] == NULL) g_state_ptr[0] = (const void *)psEnc;
  if ((const void *)psEnc != g_state_ptr[0] && g_state_ptr[1] == NULL)
    g_state_ptr[1] = (const void *)psEnc;
  if ((const void *)psEnc == g_state_ptr[1]) ch = 1;
  else ch = 0;

  r->opus_frame_index = g_cur_opus_frame;
  r->channel          = ch;
  r->nb_subfr         = psEnc->sCmn.nb_subfr;
  r->signalType       = psEnc->sCmn.indices.signalType;
  r->quantOffsetType  = psEnc->sCmn.indices.quantOffsetType;
  r->maxBits          = maxBits;
  r->useCBR           = useCBR;
  r->nBytes           = -1;
  r->predGain         = psEncCtrl->predGain;
  r->LTPredCodGain    = psEncCtrl->LTPredCodGain;
  r->Lambda           = psEncCtrl->Lambda;
  r->input_quality    = psEncCtrl->input_quality;
  r->coding_quality   = psEncCtrl->coding_quality;
  r->SNR_dB_Q7        = psEnc->sCmn.SNR_dB_Q7;
  for (i = 0; i < 4; i++) r->input_quality_bands_Q15[i] = psEnc->sCmn.input_quality_bands_Q15[i];
  r->speech_activity_Q8 = psEnc->sCmn.speech_activity_Q8;
  for (i = 0; i < MAX_NB_SUBFR; i++) {
    r->GainsUnq_Q16[i] = psEncCtrl->GainsUnq_Q16[i];
    r->Gains[i]        = psEncCtrl->Gains[i];
    r->LF_MA_shp[i]    = psEncCtrl->LF_MA_shp[i];
    r->LF_AR_shp[i]    = psEncCtrl->LF_AR_shp[i];
    r->Tilt[i]         = psEncCtrl->Tilt[i];
    r->HarmShapeGain[i]= psEncCtrl->HarmShapeGain[i];
    r->pitchL[i]       = psEncCtrl->pitchL[i];
  }
  for (i = 0; i < MAX_NB_SUBFR * MAX_SHAPE_LPC_ORDER; i++)
    r->AR[i] = psEncCtrl->AR[i];
  for (i = 0; i < LTP_ORDER * MAX_NB_SUBFR; i++)
    r->LTPCoef[i] = psEncCtrl->LTPCoef[i];
  r->LTP_scale = psEncCtrl->LTP_scale;
}

void gopus_silk_nbytes_dump(
    const silk_encoder_state_FLP   *psEnc,
    opus_int32                      nBytesOut )
{
  /* Attach to the most recent ctrl record for the matching channel. */
  int ch = 0, i;
  if ((const void *)psEnc == g_state_ptr[1]) ch = 1;
  for (i = g_ctrl_count - 1; i >= 0; i--) {
    if (g_ctrl[i].opus_frame_index == g_cur_opus_frame &&
        g_ctrl[i].channel == ch && g_ctrl[i].nBytes < 0) {
      g_ctrl[i].nBytes = nBytesOut;
      return;
    }
  }
}

/* Keep frame-level stage diagnostics bounded to the first packet/range
 * witnesses in the native CBR matrix. stage=0 is immediately after NSQ,
 * stage=1 is after side-info coding, and stage=2 is after pulse coding. */
void gopus_silk_encode_stage_dump(
    const silk_encoder_state_FLP *psEnc,
    const ec_enc                  *psRangeEnc,
    opus_int                       iter,
    opus_int                       stage )
{
  encode_stage_record *r;
  int i, ch = 0;
  if (!gopus_silk_trace_frame_selected(g_cur_opus_frame)) return;
  if (psEnc->sCmn.frame_length < 0 || psEnc->sCmn.frame_length > MAX_FRAME_LENGTH) {
    g_stage_overflow = 1;
    return;
  }
  if (g_stage_count >= MAX_TRACE_RECORDS) {
    g_stage_overflow = 1;
    return;
  }
  r = &g_stage[g_stage_count++];
  memset(r, 0, sizeof(*r));
  if ((const void *)psEnc == g_state_ptr[1]) ch = 1;
  r->opus_frame_index = g_cur_opus_frame;
  r->channel = ch;
  r->iter = iter;
  r->stage = stage;
  r->tell = ec_tell((ec_ctx *)psRangeEnc);
  r->range = psRangeEnc->rng;
  r->signalType = psEnc->sCmn.indices.signalType;
  r->quantOffsetType = psEnc->sCmn.indices.quantOffsetType;
  r->seed = psEnc->sCmn.indices.Seed;
  r->lagIndex = psEnc->sCmn.indices.lagIndex;
  r->contourIndex = psEnc->sCmn.indices.contourIndex;
  r->nlsfInterp = psEnc->sCmn.indices.NLSFInterpCoef_Q2;
  r->perIndex = psEnc->sCmn.indices.PERIndex;
  r->ltpScaleIndex = psEnc->sCmn.indices.LTP_scaleIndex;
  for (i = 0; i < MAX_NB_SUBFR; i++) {
    r->gains[i] = psEnc->sCmn.indices.GainsIndices[i];
    r->ltp[i] = psEnc->sCmn.indices.LTPIndex[i];
  }
  for (i = 0; i < MAX_LPC_ORDER + 1; i++)
    r->nlsf[i] = psEnc->sCmn.indices.NLSFIndices[i];
  if (stage == 0) {
    r->n_pulses = psEnc->sCmn.frame_length;
    memcpy(r->pulses, psEnc->sCmn.pulses, (size_t)r->n_pulses * sizeof(r->pulses[0]));
  }
}

int main(void) {
  char magic[4];
  uint32_t version, mode, application, sample_rate, channels, frame_size;
  uint32_t bitrate, bandwidth, signal, n_frames, i;
  int err;
  OpusEncoder *enc = NULL;
  float     *pcm    = NULL;
  unsigned char *packet = NULL;
  uint32_t final_range = 0;

  if (!set_binary_stdio()) { fprintf(stderr, "set binary stdio failed\n"); return 1; }

  if (!read_exact(magic, 4) || memcmp(magic, INPUT_MAGIC, 4) != 0) {
    fprintf(stderr, "bad input magic\n"); return 1;
  }
  if (!read_u32(&version) || version != 1) { fprintf(stderr, "unsupported version\n"); return 1; }
  if (!read_u32(&mode)        || mode > 2     ||
      !read_u32(&application) ||
      !read_u32(&sample_rate) || sample_rate == 0 ||
      !read_u32(&channels)    || channels < 1 || channels > 2 ||
      !read_u32(&frame_size)  || frame_size == 0 ||
      !read_u32(&bitrate)     ||
      !read_u32(&bandwidth)   ||
      !read_u32(&signal)      ||
      !read_u32(&n_frames)) {
    fprintf(stderr, "truncated header\n"); return 1;
  }

  pcm = (float *)malloc(sizeof(float) * frame_size * channels);
  packet = (unsigned char *)malloc(MAX_PACKET_BYTES);
  if (!pcm || !packet) { fprintf(stderr, "malloc failed\n"); free(pcm); free(packet); return 1; }

  enc = opus_encoder_create((opus_int32)sample_rate, (int)channels, (int)application, &err);
  if (!enc || err != OPUS_OK) {
    fprintf(stderr, "opus_encoder_create failed: %d\n", err);
    free(pcm); free(packet); return 1;
  }

  if ((mode == 2 ? opus_encoder_ctl(enc, OPUS_SET_VBR(0)) :
                   (opus_encoder_ctl(enc, OPUS_SET_VBR(1)) != OPUS_OK ||
                    opus_encoder_ctl(enc, OPUS_SET_VBR_CONSTRAINT((int)mode)) != OPUS_OK)) ||
      opus_encoder_ctl(enc, OPUS_SET_BITRATE((opus_int32)bitrate)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_BANDWIDTH((int)bandwidth)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_SIGNAL((int)signal)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_COMPLEXITY(10)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_INBAND_FEC(0)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_DTX(0)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_PACKET_LOSS_PERC(0)) != OPUS_OK) {
    fprintf(stderr, "opus_encoder_ctl setup failed\n");
    opus_encoder_destroy(enc); free(pcm); free(packet); return 1;
  }
  if (channels == 2 && mode != 2) {
    if (opus_encoder_ctl(enc, OPUS_SET_FORCE_CHANNELS(2)) != OPUS_OK) {
      fprintf(stderr, "OPUS_SET_FORCE_CHANNELS failed\n");
      opus_encoder_destroy(enc); free(pcm); free(packet); return 1;
    }
  }

  /* Recover the two SILK encoder-state pointers so the dump hook can identify
   * the channel. The OpusEncoder layout starts with the silk_encoder, whose
   * first field is the stereo state; state_Fxx[] follows the leading scalars.
   * Rather than depend on opaque offsets, derive the pointers lazily inside the
   * hook by remembering the first two distinct psEnc values seen. */

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(8) || !write_u32(n_frames)) {
    fprintf(stderr, "write output header failed\n");
    opus_encoder_destroy(enc); free(pcm); free(packet); return 1;
  }

  /* Collect packet records into memory so the ctrl block can follow. */
  {
    uint32_t *pkt_len = (uint32_t *)malloc(sizeof(uint32_t) * n_frames);
    uint32_t *pkt_fr  = (uint32_t *)malloc(sizeof(uint32_t) * n_frames);
    unsigned char **pkt_data = (unsigned char **)malloc(sizeof(unsigned char *) * n_frames);
    if (!pkt_len || !pkt_fr || !pkt_data) {
      fprintf(stderr, "malloc records failed\n");
      opus_encoder_destroy(enc); free(pcm); free(packet); return 1;
    }

    for (i = 0; i < n_frames; i++) {
      uint32_t n_samples = frame_size * channels, j;
      int ret;
      for (j = 0; j < n_samples; j++) {
        uint32_t bits;
        if (!read_u32(&bits)) { fprintf(stderr, "truncated PCM\n"); return 1; }
        memcpy(&pcm[j], &bits, 4);
      }
      g_cur_opus_frame = (int32_t)i;
      ret = opus_encode_float(enc, pcm, (int)frame_size, packet, MAX_PACKET_BYTES);
      if (ret < 0) {
        fprintf(stderr, "opus_encode_float frame %u: %d (%s)\n", i, ret, opus_strerror(ret));
        opus_encoder_destroy(enc); free(pcm); free(packet); return 1;
      }
      opus_encoder_ctl(enc, OPUS_GET_FINAL_RANGE(&final_range));
      pkt_len[i]  = (uint32_t)ret;
      pkt_fr[i]   = final_range;
      pkt_data[i] = (unsigned char *)malloc((size_t)ret > 0 ? (size_t)ret : 1);
      memcpy(pkt_data[i], packet, (size_t)ret);
    }

    for (i = 0; i < n_frames; i++) {
      if (!write_u32(pkt_len[i]) || !write_u32(pkt_fr[i]) ||
          !write_exact(pkt_data[i], pkt_len[i])) {
        fprintf(stderr, "write packet record failed\n"); return 1;
      }
    }

    /* ctrl block */
    if (!write_u32((uint32_t)g_ctrl_count)) { fprintf(stderr, "write ctrl count failed\n"); return 1; }
    for (i = 0; i < (uint32_t)g_ctrl_count; i++) {
      ctrl_record *r = &g_ctrl[i];
      int k;
      if (!write_i32(r->opus_frame_index) || !write_i32(r->channel) ||
          !write_i32(r->nb_subfr) || !write_i32(r->signalType) ||
          !write_i32(r->quantOffsetType) || !write_i32(r->maxBits) ||
          !write_i32(r->useCBR) || !write_i32(r->nBytes) ||
          !write_f32(r->predGain) || !write_f32(r->LTPredCodGain) ||
          !write_f32(r->Lambda) || !write_f32(r->input_quality) ||
          !write_f32(r->coding_quality) || !write_i32(r->SNR_dB_Q7) ||
          !write_i32(r->input_quality_bands_Q15[0]) || !write_i32(r->input_quality_bands_Q15[1]) ||
          !write_i32(r->input_quality_bands_Q15[2]) || !write_i32(r->input_quality_bands_Q15[3]) ||
          !write_i32(r->speech_activity_Q8)) { fprintf(stderr, "write ctrl head failed\n"); return 1; }
      for (k = 0; k < MAX_NB_SUBFR; k++) if (!write_i32(r->GainsUnq_Q16[k])) return 1;
      for (k = 0; k < MAX_NB_SUBFR; k++) if (!write_f32(r->Gains[k])) return 1;
      for (k = 0; k < MAX_NB_SUBFR * MAX_SHAPE_LPC_ORDER; k++) if (!write_f32(r->AR[k])) return 1;
      for (k = 0; k < MAX_NB_SUBFR; k++) if (!write_f32(r->LF_MA_shp[k])) return 1;
      for (k = 0; k < MAX_NB_SUBFR; k++) if (!write_f32(r->LF_AR_shp[k])) return 1;
      for (k = 0; k < MAX_NB_SUBFR; k++) if (!write_f32(r->Tilt[k])) return 1;
      for (k = 0; k < MAX_NB_SUBFR; k++) if (!write_f32(r->HarmShapeGain[k])) return 1;
      for (k = 0; k < LTP_ORDER * MAX_NB_SUBFR; k++) if (!write_f32(r->LTPCoef[k])) return 1;
      if (!write_f32(r->LTP_scale)) return 1;
      for (k = 0; k < MAX_NB_SUBFR; k++) if (!write_i32(r->pitchL[k])) return 1;
    }

    if (!write_u32((uint32_t)g_stage_count)) return 1;
    for (i = 0; i < (uint32_t)g_stage_count; i++) {
      encode_stage_record *r = &g_stage[i];
      int k;
      if (!write_i32(r->opus_frame_index) || !write_i32(r->channel) ||
          !write_i32(r->iter) || !write_i32(r->stage) || !write_i32(r->tell) ||
          !write_u32(r->range) || !write_i32(r->signalType) ||
          !write_i32(r->quantOffsetType) || !write_i32(r->seed) ||
          !write_i32(r->lagIndex) || !write_i32(r->contourIndex) ||
          !write_i32(r->nlsfInterp) || !write_i32(r->perIndex) ||
          !write_i32(r->ltpScaleIndex)) return 1;
      for (k = 0; k < MAX_NB_SUBFR; k++) if (!write_i32(r->gains[k])) return 1;
      for (k = 0; k < MAX_NB_SUBFR; k++) if (!write_i32(r->ltp[k])) return 1;
      for (k = 0; k < MAX_LPC_ORDER + 1; k++) if (!write_i32(r->nlsf[k])) return 1;
      if (!write_i32(r->n_pulses) ||
          !write_exact(r->pulses, (size_t)r->n_pulses * sizeof(r->pulses[0]))) return 1;
    }
    if (!write_i32(g_stage_overflow)) return 1;
    if (!write_u32((uint32_t)g_lpc_call_count)) return 1;
    for (i = 0; i < (uint32_t)g_lpc_call_count; i++) {
      lpc_call_record *r = &g_lpc_call[i];
      uint32_t j;
      if (!write_i32(r->opus_frame_index) || !write_i32(r->channel) ||
          !write_i32(r->nFramesEncoded) || !write_i32(r->API_fs_Hz) ||
          !write_i32(r->fs_kHz) || !write_i32(r->frame_length) ||
          !write_i32(r->subfr_length) || !write_i32(r->nb_subfr) ||
          !write_i32(r->predictLPCOrder) || !write_i32(r->useInterpolatedNLSFs) ||
          !write_i32(r->first_frame_after_reset) || !write_i32(r->arch) ||
          !write_f32(r->minInvGain)) return 1;
      for (j = 0; j < MAX_LPC_ORDER; j++) if (!write_i32(r->prev_NLSFq_Q15[j])) return 1;
      if (!write_u32(r->input_count)) return 1;
      for (j = 0; j < r->input_count; j++) if (!write_f32(r->input[j])) return 1;
      if (!write_i32(r->selected_interp)) return 1;
      for (j = 0; j < MAX_LPC_ORDER; j++) if (!write_i32(r->NLSF_Q15[j])) return 1;
    }
    if (!write_i32(g_lpc_call_overflow)) return 1;
    if (!write_u32((uint32_t)g_ltp_trace_count)) return 1;
    for (i = 0; i < (uint32_t)g_ltp_trace_count; i++) {
      ltp_trace_record *r = &g_ltp_trace[i];
      int k, j;
      if (!write_i32(r->opus_frame_index) || !write_i32(r->channel) ||
          !write_i32(r->signalType) || !write_i32(r->filterCalled) ||
          !write_i32(r->subfr_length) || !write_i32(r->nb_subfr) ||
          !write_i32(r->pre_length) || !write_u32(r->output_count)) return 1;
      for (k = 0; k < r->nb_subfr; k++) if (!write_f32(r->Gains[k])) return 1;
      if (r->filterCalled) {
        for (k = 0; k < r->nb_subfr; k++) {
          if (!write_i32(r->pitchL[k]) || !write_f32(r->invGains[k])) return 1;
          for (j = 0; j < LTP_ORDER; j++)
            if (!write_f32(r->B[k * LTP_ORDER + j])) return 1;
        }
        for (j = 0; j < (int)r->output_count; j++) {
          if (!write_f32(r->x[j])) return 1;
          for (k = 0; k < LTP_ORDER; k++)
            if (!write_f32(r->lag[j * LTP_ORDER + k])) return 1;
          if (!write_f32(r->result[j])) return 1;
        }
      }
    }
    if (!write_i32(g_ltp_trace_overflow)) return 1;
    if (!write_u32((uint32_t)g_gain_tweak_trace_count)) return 1;
    for (i = 0; i < (uint32_t)g_gain_tweak_trace_count; i++) {
      gain_tweak_trace_record *r = &g_gain_tweak_trace[i];
      int k;
      if (!write_i32(r->opus_frame_index) || !write_i32(r->channel) ||
          !write_i32(r->nb_subfr) || !write_i32(r->shaping_lpc_order) ||
          !write_i32(r->warping_Q16) || !write_i32(r->pow_call_count) ||
          !write_u32(r->pre_gain_mask) ||
          !write_f32(r->gain_mult_exponent) || !write_f32(r->gain_mult) ||
          !write_f32(r->gain_add)) return 1;
      for (k = 0; k < r->nb_subfr; k++) {
        if (!write_f32(r->pre_gain[k]) || !write_f32(r->post_gain[k])) return 1;
      }
    }
    if (!write_i32(g_gain_tweak_trace_overflow)) return 1;
    if (!write_u32((uint32_t)g_noise_shape_trace_count)) return 1;
    for (i = 0; i < (uint32_t)g_noise_shape_trace_count; i++) {
      noise_shape_trace_record *r = &g_noise_shape_trace[i];
      int j;
      if (!write_i32(r->opus_frame_index) || !write_i32(r->channel) ||
          !write_i32(r->subframe) || !write_i32(r->num_subframes) ||
          !write_i32(r->order) || !write_i32(r->window_length) ||
          !write_i32(r->warping_Q16) || !write_i32(r->autoCorrCalls) ||
          !write_i32(r->schurCalls) || !write_f32(r->autoCorrWarping)) return 1;
      for (j = 0; j < r->window_length; j++)
        if (!write_f32(r->window[j])) return 1;
      for (j = 0; j <= r->order; j++)
        if (!write_f32(r->rawAutoCorr[j])) return 1;
      for (j = 0; j <= r->order; j++)
        if (!write_f32(r->adjustedAutoCorr[j])) return 1;
      for (j = 0; j < r->order; j++)
        if (!write_f32(r->rc[j])) return 1;
      if (!write_f32(r->nrg)) return 1;
    }
    if (!write_i32(g_noise_shape_trace_overflow)) return 1;
  }

  opus_encoder_destroy(enc);
  free(pcm); free(packet);
  return 0;
}
