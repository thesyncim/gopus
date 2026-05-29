/* CELT lost-frame (PLC) concealment-stage trace helper.
 *
 * Captures intermediate buffers of the libopus 1.6.1 noise-PLC concealment path
 * for a target noise-PLC chunk, so the gopus host-only float parity drift in the
 * overlong / multi-frame PLC concealment path can be localised to a single
 * concealment stage:
 *   - presyn[]: per-channel out_syn block (N samples) right before the in-place
 *               post-filter comb_filter() runs, i.e. the post-celt_synthesis
 *               time buffer (which already includes the prefilter_and_fold TDAC
 *               overlap-add at the frame start).
 *   - final[] : post-deemphasis interleaved PCM for that decoded chunk.
 *
 * Implementation mirrors libopus_celt_synthesis_trace.c: it #includes the
 * pinned celt/celt_decoder.c (libopus 1.6.1) so celt_decode_lost() runs the
 * unmodified concealment math, and a capturing macro wrapper around the external
 * comb_filter() snapshots the relevant buffers and forwards to the real
 * implementation, so the math stays byte-identical to libopus.
 *
 * Distinguishing the calls (all inside celt_decode_lost()'s noise branch):
 *   - prefilter_and_fold() calls comb_filter() with window==NULL && overlap==0;
 *     each such call marks one consumed pending fold (i.e. one noise-PLC chunk).
 *     A 0-based counter selects the target chunk.
 *   - The post-filter comb_filter() calls (window!=NULL) over out_syn run right
 *     after celt_synthesis(); the first one per channel snapshots presyn[].
 *
 * For a 200 ms PLC request at 48 kHz with 20 ms (LM=3) frames, the first
 * noise-PLC chunk (celt_decode_lost call index 5) is fold index 0.
 */

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

#define CELT_DECODER_C

#include "opus.h"
#include "arch.h"
#include "os_support.h"
#include "celt.h"
#include "modes.h"
#include "mdct.h"
#include "bands.h"

/* Capture state. */
static int g_fold_comb_calls = 0;    /* count of fold (window==NULL) comb_filter */
static int g_channels = 0;           /* decoder channels (fold calls per chunk) */
static int g_target_fold_index = -1; /* which fold/noise chunk to capture */
static int g_frame_size = 0;         /* full per-channel frame size to snapshot */
static int g_armed = 0;
static int g_capture_N = 0;
static int g_overlap = 0;            /* mode overlap (fold region length) */
static int g_presyn_idx = 0;         /* channels captured for presyn */
static int g_fold_idx = 0;           /* channels captured for fold */
static int g_combin_idx = 0;         /* channels captured for comb input */
static int g_combout_idx = 0;        /* channels captured for comb output */
static int g_spec_idx = 0;           /* channels captured for spectrum */
static int g_combin_len = 0;         /* comb input length captured */
static celt_sig *g_presyn_capture[2] = {NULL, NULL};
static celt_sig *g_fold_capture[2] = {NULL, NULL};
static celt_sig *g_combin_capture[2] = {NULL, NULL};
static celt_sig *g_combout_capture[2] = {NULL, NULL};
static celt_sig *g_spec_capture[2] = {NULL, NULL};
static int g_prespec_idx = 0;        /* channels captured for pre-denorm X */
static float *g_prespec_capture[2] = {NULL, NULL};

/* The prefilter_and_fold() comb_filter is called with start == history ==
 * COMBFILTER_MAXPERIOD + 2 (the gopus combFilterHistory). */
#define FOLD_COMB_HISTORY (COMBFILTER_MAXPERIOD + 2)
static const int g_combin_history = FOLD_COMB_HISTORY;

static void gopus_capture_comb_filter(opus_val32 *y, opus_val32 *x, int T0, int T1, int N,
      opus_val16 g0, opus_val16 g1, int tapset0, int tapset1,
      const celt_coef *window, int overlap, int arch, int ch);
static void gopus_capture_mdct_backward(const mdct_lookup *l, kiss_fft_scalar *in,
      kiss_fft_scalar *out, const celt_coef *window, int overlap, int shift,
      int stride, int arch, int ch, int b);
static void gopus_capture_denormalise_bands(const CELTMode *m, const celt_norm *X,
      celt_sig *freq, const celt_glog *bandLogE, int start, int end, int M,
      int downsample, int silence, int Nsynth);

/* Route comb_filter() through the capturing wrapper. The post-filter loop
 * variable inside celt_decode_lost() is c; prefilter_and_fold() also has a
 * channel loop variable c in scope at its comb_filter call. comb_filter is
 * defined in celt.c (external), so wrapping its call here does not collide with
 * any in-TU definition.
 *
 * clt_mdct_backward is a function-like macro from mdct.h; redefine it to the
 * capturing wrapper so the first per-channel inverse MDCT of the armed chunk can
 * snapshot the post-prefilter_and_fold overlap seed in out_syn before the
 * overlap-add overwrites it. c and b are the celt_synthesis() loop variables. */
#define comb_filter(y, x, T0, T1, N, g0, g1, tapset0, tapset1, window, overlap, arch) \
   gopus_capture_comb_filter((y), (x), (T0), (T1), (N), (g0), (g1), (tapset0), (tapset1), \
         (window), (overlap), (arch), c)
#undef clt_mdct_backward
#define clt_mdct_backward(l, in, out, window, overlap, shift, stride, arch) \
   gopus_capture_mdct_backward((l), (in), (out), (window), (overlap), (shift), (stride), (arch), c, b)
/* N is the celt_synthesis() spectrum length, in scope at every denormalise_bands
 * call site. */
#define denormalise_bands(m, X, freq, bandLogE, start, end, M, downsample, silence) \
   gopus_capture_denormalise_bands((m), (X), (freq), (bandLogE), (start), (end), (M), \
         (downsample), (silence), N)

#include "celt/celt_decoder.c"

#undef comb_filter
#undef clt_mdct_backward
#undef denormalise_bands

/* Capture the raw LCG noise vector by wrapping renormalise_vector after its
 * prototype is visible (so the function-like macro does not mangle the vq.h
 * declaration). The macro is applied only within celt_decoder.c above via a
 * second include is not possible; instead we capture the raw noise on the gopus
 * side and compare the post-renormalise X (prespec) here. */

static void gopus_capture_comb_filter(opus_val32 *y, opus_val32 *x, int T0, int T1, int N,
      opus_val16 g0, opus_val16 g1, int tapset0, int tapset1,
      const celt_coef *window, int overlap, int arch, int ch)
{
   int fold_capture_ch = -1;
   if (window == NULL && overlap == 0) {
      /* prefilter_and_fold() comb_filter: one call per channel per consumed fold.
       * The fold/noise-chunk index is the call count divided by channels. */
      int chunk = (g_channels > 0) ? (g_fold_comb_calls / g_channels) : g_fold_comb_calls;
      int fold_ch = (g_channels > 0) ? (g_fold_comb_calls % g_channels) : 0;
      g_fold_comb_calls++;
      if (chunk == g_target_fold_index) {
         g_armed = 1;
         g_presyn_idx = 0;
         g_spec_idx = 0;
         g_prespec_idx = 0;
         /* Capture the comb_filter input (history + N samples; start==history so
          * the input window is x[-history .. N)). The output is captured after
          * comb_filter runs, below. */
         if (fold_ch >= 0 && fold_ch < 2 && N > 0) {
            if (g_combin_capture[fold_ch]) {
               OPUS_COPY(g_combin_capture[fold_ch], x - g_combin_history, g_combin_history + N);
               g_combin_len = g_combin_history + N;
               g_combin_idx = fold_ch + 1;
            }
            fold_capture_ch = fold_ch;
         }
      } else if (chunk == g_target_fold_index + 1) {
         g_armed = 0;
      }
   } else if (g_armed && window != NULL && ch >= 0 && ch < 2 &&
              g_presyn_capture[ch] && g_presyn_idx == ch && g_frame_size > 0) {
      /* First post-filter comb_filter for this channel in the armed chunk:
       * snapshot the full pre-postfilter out_syn block (frame_size samples; the
       * comb_filter N arg is only shortMdctSize for the windowed leading call). */
      OPUS_COPY(g_presyn_capture[ch], x, g_frame_size);
      g_capture_N = g_frame_size;
      g_presyn_idx = ch + 1;
   }
   comb_filter(y, x, T0, T1, N, g0, g1, tapset0, tapset1, window, overlap, arch);
   if (fold_capture_ch >= 0 && fold_capture_ch < 2 && g_combout_capture[fold_capture_ch] && N > 0) {
      OPUS_COPY(g_combout_capture[fold_capture_ch], y, N);
      g_combout_idx = fold_capture_ch + 1;
   }
}

static void gopus_capture_mdct_backward(const mdct_lookup *l, kiss_fft_scalar *in,
      kiss_fft_scalar *out, const celt_coef *window, int overlap, int shift,
      int stride, int arch, int ch, int b)
{
   if (g_armed && b == 0 && ch >= 0 && ch < 2 && g_fold_capture[ch] &&
       g_fold_idx == ch && g_overlap > 0) {
      /* First inverse MDCT block of this channel in the armed noise chunk: the
       * out buffer still holds the post-prefilter_and_fold overlap seed (the
       * overlap-add has not run yet). Snapshot the leading overlap samples. */
      OPUS_COPY(g_fold_capture[ch], out, g_overlap);
      g_fold_idx = ch + 1;
   }
   clt_mdct_backward_c(l, in, out, window, overlap, shift, stride, arch);
}

static void gopus_capture_denormalise_bands(const CELTMode *m, const celt_norm *X,
      celt_sig *freq, const celt_glog *bandLogE, int start, int end, int M,
      int downsample, int silence, int Nsynth)
{
   if (g_armed && g_prespec_idx < 2 && g_prespec_idx < g_channels && g_prespec_capture[g_prespec_idx] &&
       Nsynth > 0 && Nsynth <= g_frame_size) {
      int k;
      for (k = 0; k < Nsynth; k++) g_prespec_capture[g_prespec_idx][k] = (float)X[k];
      g_prespec_idx++;
   }
   denormalise_bands(m, X, freq, bandLogE, start, end, M, downsample, silence);
   if (g_armed && g_spec_idx < 2 && g_spec_idx < g_channels && g_spec_capture[g_spec_idx] &&
       Nsynth > 0 && Nsynth <= g_frame_size) {
      OPUS_COPY(g_spec_capture[g_spec_idx], freq, Nsynth);
      g_spec_idx++;
   }
}

#define GCLI_MAGIC "GCLI"
#define GCLO_MAGIC "GCLO"

static int read_exact(void *dst, size_t n) { return fread(dst, 1, n, stdin) == n; }
static int write_exact(const void *src, size_t n) { return fwrite(src, 1, n, stdout) == n; }

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact(b, 4)) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) | ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int write_u32(uint32_t v) {
  unsigned char b[4];
  b[0] = (unsigned char)(v & 0xFF);
  b[1] = (unsigned char)((v >> 8) & 0xFF);
  b[2] = (unsigned char)((v >> 16) & 0xFF);
  b[3] = (unsigned char)((v >> 24) & 0xFF);
  return write_exact(b, 4);
}

static int write_float(float v) {
  union { float f; uint32_t u; } bits;
  bits.f = v;
  return write_u32(bits.u);
}

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

int main(void) {
  unsigned char magic[4];
  uint32_t version = 0;
  uint32_t sample_rate = 48000;
  uint32_t channels = 0;
  uint32_t frame_size = 0;     /* per-CELT-frame size (samples/ch), e.g. 960 */
  uint32_t request_size = 0;   /* overlong nil PLC request size (samples/ch) */
  uint32_t target_fold = 0;
  uint32_t target_chunk = 0;   /* which 0-based output chunk holds the noise frame */
  uint32_t packet_count = 0;
  float *frame = NULL;
  float *final_capture = NULL;
  OpusDecoder *dec = NULL;
  int err = OPUS_OK;
  uint32_t i;

  if (!set_binary_stdio()) { fprintf(stderr, "stdio mode\n"); return 1; }
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, GCLI_MAGIC, 4) != 0) {
    fprintf(stderr, "invalid input magic\n"); return 1;
  }
  if (!read_u32(&version) || version != 1) { fprintf(stderr, "bad version\n"); return 1; }
  if (!read_u32(&sample_rate) || !read_u32(&channels) || !read_u32(&frame_size) ||
      !read_u32(&request_size) || !read_u32(&target_fold) || !read_u32(&target_chunk) ||
      !read_u32(&packet_count)) {
    fprintf(stderr, "bad header\n"); return 1;
  }
  if (channels == 0 || channels > 2 || frame_size == 0 || request_size == 0) {
    fprintf(stderr, "bad dims\n"); return 1;
  }
  if (sample_rate != 48000) { fprintf(stderr, "expect 48k\n"); return 1; }

  g_target_fold_index = (int)target_fold;
  g_channels = (int)channels;
  g_frame_size = (int)frame_size;
  g_overlap = 120; /* 48 kHz CELT custom mode overlap. */
  for (i = 0; i < channels; i++) {
    g_presyn_capture[i] = (celt_sig *)calloc((size_t)frame_size, sizeof(celt_sig));
    g_fold_capture[i] = (celt_sig *)calloc((size_t)g_overlap, sizeof(celt_sig));
    g_combin_capture[i] = (celt_sig *)calloc((size_t)(g_combin_history + g_overlap), sizeof(celt_sig));
    g_combout_capture[i] = (celt_sig *)calloc((size_t)g_overlap, sizeof(celt_sig));
    g_spec_capture[i] = (celt_sig *)calloc((size_t)frame_size, sizeof(celt_sig));
    g_prespec_capture[i] = (float *)calloc((size_t)frame_size, sizeof(float));
    if (!g_presyn_capture[i] || !g_fold_capture[i] || !g_combin_capture[i] ||
        !g_combout_capture[i] || !g_spec_capture[i] || !g_prespec_capture[i]) {
      fprintf(stderr, "alloc\n"); return 1;
    }
  }
  final_capture = (float *)malloc((size_t)channels * (size_t)frame_size * sizeof(float));
  if (!final_capture) { fprintf(stderr, "alloc final\n"); return 1; }

  dec = opus_decoder_create((opus_int32)sample_rate, (int)channels, &err);
  if (!dec || err != OPUS_OK) { fprintf(stderr, "decoder_create %d\n", err); return 1; }

  for (i = 0; i < packet_count; i++) {
    uint32_t decode_fec = 0, packet_len = 0;
    unsigned char *packet = NULL;
    int decoded;
    int request;
    if (!read_u32(&decode_fec) || decode_fec > 1 || !read_u32(&packet_len)) {
      fprintf(stderr, "bad packet header\n"); return 1;
    }
    if (packet_len > 0) {
      packet = (unsigned char *)malloc(packet_len);
      if (!packet || !read_exact(packet, packet_len)) { fprintf(stderr, "bad payload\n"); return 1; }
    }
    /* The nil (PLC) request decodes the overlong request; a real packet decodes
     * a single CELT frame. */
    request = (packet_len == 0) ? (int)request_size : (int)frame_size;
    if (frame == NULL) {
      frame = (float *)malloc((size_t)channels * (size_t)request_size * sizeof(float));
      if (!frame) { fprintf(stderr, "alloc frame\n"); return 1; }
    }
    decoded = opus_decode_float(dec, packet, (opus_int32)packet_len, frame, request, (int)decode_fec);
    free(packet);
    if (decoded < 0) { fprintf(stderr, "decode %d\n", decoded); return 1; }
    /* Capture the final PCM of the target chunk from the PLC request output. */
    if (packet_len == 0) {
      uint32_t off = target_chunk * frame_size * channels;
      OPUS_COPY(final_capture, frame + off, (int)(frame_size * channels));
    }
  }

  if (g_presyn_idx < (int)channels || g_fold_idx < (int)channels || g_spec_idx < (int)channels) {
    fprintf(stderr, "target fold %d captured presyn=%d fold=%d spec=%d /%d channels (fold combs=%d)\n",
            g_target_fold_index, g_presyn_idx, g_fold_idx, g_spec_idx, (int)channels, g_fold_comb_calls);
    return 1;
  }

  {
    int CC = (int)channels;
    int N = g_capture_N;
    int ch, j;
    if (N <= 0) { fprintf(stderr, "no capture N\n"); return 1; }
    int ov = g_overlap;
    int cinlen = g_combin_len;
    if (!write_exact(GCLO_MAGIC, 4) || !write_u32(1) ||
        !write_u32((uint32_t)N) || !write_u32((uint32_t)CC) ||
        !write_u32((uint32_t)ov) || !write_u32((uint32_t)cinlen) ||
        !write_u32((uint32_t)g_presyn_idx) || !write_u32((uint32_t)g_fold_idx)) {
      fprintf(stderr, "write header\n"); return 1;
    }
    for (ch = 0; ch < CC; ch++)
      for (j = 0; j < N; j++)
        if (!write_float(g_prespec_capture[ch][j])) { fprintf(stderr, "write prespec\n"); return 1; }
    for (ch = 0; ch < CC; ch++)
      for (j = 0; j < N; j++)
        if (!write_float((float)g_spec_capture[ch][j])) { fprintf(stderr, "write spec\n"); return 1; }
    for (ch = 0; ch < CC; ch++)
      for (j = 0; j < cinlen; j++)
        if (!write_float((float)g_combin_capture[ch][j])) { fprintf(stderr, "write combin\n"); return 1; }
    for (ch = 0; ch < CC; ch++)
      for (j = 0; j < ov; j++)
        if (!write_float((float)g_combout_capture[ch][j])) { fprintf(stderr, "write combout\n"); return 1; }
    for (ch = 0; ch < CC; ch++)
      for (j = 0; j < ov; j++)
        if (!write_float((float)g_fold_capture[ch][j])) { fprintf(stderr, "write fold\n"); return 1; }
    for (ch = 0; ch < CC; ch++)
      for (j = 0; j < N; j++)
        if (!write_float((float)g_presyn_capture[ch][j])) { fprintf(stderr, "write presyn\n"); return 1; }
    for (j = 0; j < N * CC; j++)
      if (!write_float(final_capture[j])) { fprintf(stderr, "write final\n"); return 1; }
  }

  opus_decoder_destroy(dec);
  free(frame);
  free(final_capture);
  for (i = 0; i < 2; i++) {
    free(g_presyn_capture[i]); free(g_fold_capture[i]);
    free(g_combin_capture[i]); free(g_combout_capture[i]);
    free(g_spec_capture[i]); free(g_prespec_capture[i]);
  }
  return 0;
}
