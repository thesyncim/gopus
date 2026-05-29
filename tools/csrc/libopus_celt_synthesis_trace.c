/* CELT decode synthesis-stage trace helper.
 *
 * Captures intermediate CELT synthesis buffers of a real opus_decode_float()
 * for a target frame, so the gopus host-only float parity drift can be
 * localised to a single synthesis stage:
 *   - freq[]  : per-channel post-denormalise_bands frequency buffer
 *   - imdct[] : per-channel post-clt_mdct_backward time buffer (pre comb_filter)
 *   - final[] : post-deemphasis interleaved PCM
 *
 * Implementation: this translation unit #includes the pinned
 * celt/celt_decoder.c (libopus 1.6.1) with capturing macro wrappers around the
 * denormalise_bands() calls inside celt_synthesis() (to snapshot freq[]) and the
 * comb_filter() calls inside celt_decode_with_ec() (to snapshot the pre-comb
 * out_syn[], i.e. the post-IMDCT time buffer, before the postfilter rewrites it
 * in place; the seed frame has a non-zero postfilter gain). The wrappers forward
 * to the unmodified archive implementations. Including the .c here means
 * celt_decode_with_ec() and friends are provided by this TU, so the archive copy
 * of celt_decoder.o is not pulled and the synthesis math stays byte-identical to
 * libopus.
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

/* celt/celt_decoder.c defines CELT_DECODER_C before including celt.h so that
 * opus_custom.h exposes the decoder prototypes. Because this TU includes celt.h
 * (transitively opus_custom.h) before #including celt_decoder.c, we must set the
 * same flag up front or the prototypes get hidden behind the include guard. */
#define CELT_DECODER_C

#include "opus.h"
#include "arch.h"
#include "os_support.h"
#include "celt.h"
#include "modes.h"
#include "mdct.h"
#include "bands.h"

/* Capture state, armed for the target frame only. */
static int g_capture_armed = 0;
static int g_capture_N = 0;
static int g_capture_freq_idx = 0;
static int g_imdct_captured[2] = {0, 0};
static celt_sig *g_freq_capture[2] = {NULL, NULL};
static celt_sig *g_imdct_capture[2] = {NULL, NULL};

/* Wrapper around comb_filter(): the postfilter is applied in place over
 * out_syn[c]. Snapshot the full pre-comb out_syn block (== the post-IMDCT time
 * buffer) on the first comb_filter call for each channel before forwarding, so
 * the trace can compare the IMDCT output even when the postfilter gain is
 * non-zero (as it is for the seed frame). */
static void gopus_capture_comb_filter(opus_val32 *y, opus_val32 *x, int T0, int T1, int N,
      opus_val16 g0, opus_val16 g1, int tapset0, int tapset1,
      const celt_coef *window, int overlap, int arch, int ch)
{
   if (g_capture_armed && ch >= 0 && ch < 2 && !g_imdct_captured[ch] &&
       g_imdct_capture[ch] && g_capture_N > 0) {
      OPUS_COPY(g_imdct_capture[ch], x, g_capture_N);
      g_imdct_captured[ch] = 1;
   }
   comb_filter(y, x, T0, T1, N, g0, g1, tapset0, tapset1, window, overlap, arch);
}

/* Wrapper around denormalise_bands(): forwards to the real implementation, then
 * snapshots the freshly written freq[] buffer for the armed target frame. The
 * capture index advances per channel-ordered call inside celt_synthesis(); freq
 * holds the full N-sample interleaved spectrum (N == mode->shortMdctSize<<LM,
 * independent of transient/short-block decomposition). The post-IMDCT (and, for
 * the seed's zero-gain frame, post-comb) buffer is read afterwards from
 * decode_mem so transient short-block IMDCT is captured correctly. */
static void gopus_capture_denormalise_bands(const CELTMode *m, const celt_norm *X,
      celt_sig *freq, const celt_glog *bandLogE, int start, int end, int M,
      int downsample, int silence, int N)
{
   denormalise_bands(m, X, freq, bandLogE, start, end, M, downsample, silence);
   if (g_capture_armed && g_capture_freq_idx < 2 && g_freq_capture[g_capture_freq_idx]) {
      OPUS_COPY(g_freq_capture[g_capture_freq_idx], freq, N);
      g_capture_freq_idx++;
      g_capture_N = N;
   }
}

/* Route the celt_synthesis() denormalise_bands() calls through the capturing
 * wrapper (N is in scope throughout celt_synthesis()) and the celt_decode_with_ec()
 * comb_filter() calls through the postfilter-capture wrapper (c is the channel
 * loop variable in scope at those call sites). */
#define denormalise_bands(m, X, freq, bandLogE, start, end, M, downsample, silence) \
   gopus_capture_denormalise_bands((m), (X), (freq), (bandLogE), (start), (end), (M), \
         (downsample), (silence), N)
#define comb_filter(y, x, T0, T1, N, g0, g1, tapset0, tapset1, window, overlap, arch) \
   gopus_capture_comb_filter((y), (x), (T0), (T1), (N), (g0), (g1), (tapset0), (tapset1), \
         (window), (overlap), (arch), c)

/* Pull in the pinned decoder; celt_synthesis() and celt_decode_with_ec() are
 * defined in this TU and use the wrappers above. */
#include "celt/celt_decoder.c"

#undef denormalise_bands
#undef comb_filter

#define GCSI_MAGIC "GCSI"
#define GCSO_MAGIC "GCSO"

static int read_exact(void *dst, size_t n) {
  return fread(dst, 1, n, stdin) == n;
}

static int write_exact(const void *src, size_t n) {
  return fwrite(src, 1, n, stdout) == n;
}

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
  union {
    float f;
    uint32_t u;
  } bits;
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
  uint32_t frame_size = 0;
  uint32_t target_step = 0;
  uint32_t packet_count = 0;
  float *frame = NULL;
  OpusDecoder *dec = NULL;
  int err = OPUS_OK;
  uint32_t i;
  int max_N;

  if (!set_binary_stdio()) {
    fprintf(stderr, "failed to set binary stdio mode\n");
    return 1;
  }
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, GCSI_MAGIC, sizeof(magic)) != 0) {
    fprintf(stderr, "invalid input magic\n");
    return 1;
  }
  if (!read_u32(&version) || version != 1) {
    fprintf(stderr, "unsupported input version\n");
    return 1;
  }
  if (!read_u32(&sample_rate) || !read_u32(&channels) || !read_u32(&frame_size) ||
      !read_u32(&target_step) || !read_u32(&packet_count)) {
    fprintf(stderr, "failed to read header\n");
    return 1;
  }
  if (channels == 0 || channels > 2 || frame_size == 0) {
    fprintf(stderr, "invalid decoder dimensions\n");
    return 1;
  }
  if (sample_rate != 48000) {
    fprintf(stderr, "synthesis trace currently expects 48 kHz output\n");
    return 1;
  }
  if (target_step >= packet_count) {
    fprintf(stderr, "target step outside packet sequence\n");
    return 1;
  }

  max_N = (int)frame_size;
  for (i = 0; i < channels; i++) {
    g_freq_capture[i] = (celt_sig *)calloc((size_t)max_N, sizeof(celt_sig));
    g_imdct_capture[i] = (celt_sig *)calloc((size_t)max_N, sizeof(celt_sig));
    if (g_freq_capture[i] == NULL || g_imdct_capture[i] == NULL) {
      fprintf(stderr, "failed to allocate capture buffers\n");
      return 1;
    }
  }

  frame = (float *)malloc((size_t)channels * (size_t)frame_size * sizeof(float));
  if (frame == NULL) {
    fprintf(stderr, "failed to allocate frame buffer\n");
    return 1;
  }

  dec = opus_decoder_create((opus_int32)sample_rate, (int)channels, &err);
  if (dec == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_decoder_create failed: %d\n", err);
    return 1;
  }

  for (i = 0; i < packet_count; i++) {
    uint32_t decode_fec = 0;
    uint32_t packet_len = 0;
    unsigned char *packet = NULL;
    int decoded_samples;

    if (!read_u32(&decode_fec) || decode_fec > 1 || !read_u32(&packet_len)) {
      fprintf(stderr, "failed to read packet header\n");
      return 1;
    }
    if (packet_len > 0) {
      packet = (unsigned char *)malloc(packet_len);
      if (packet == NULL || !read_exact(packet, packet_len)) {
        fprintf(stderr, "failed to read packet payload\n");
        return 1;
      }
    }

    if (i == target_step) {
      g_capture_armed = 1;
      g_capture_freq_idx = 0;
      g_imdct_captured[0] = 0;
      g_imdct_captured[1] = 0;
    }
    decoded_samples = opus_decode_float(dec, packet, (opus_int32)packet_len, frame, (int)frame_size, (int)decode_fec);
    free(packet);
    if (decoded_samples < 0) {
      fprintf(stderr, "opus_decode_float failed: %d\n", decoded_samples);
      return 1;
    }

    if (i == target_step) {
      int N = g_capture_N;
      int CC = (int)channels;
      uint32_t ch;
      int j;

      g_capture_armed = 0;

      if (g_capture_freq_idx < CC || N <= 0) {
        fprintf(stderr, "synthesis capture did not run for target step (freq_idx=%d N=%d)\n",
                g_capture_freq_idx, N);
        return 1;
      }
      if (!g_imdct_captured[0] || (CC == 2 && !g_imdct_captured[1])) {
        fprintf(stderr, "pre-comb IMDCT capture did not run for target step\n");
        return 1;
      }

      if (!write_exact(GCSO_MAGIC, 4) || !write_u32(1) ||
          !write_u32((uint32_t)N) || !write_u32((uint32_t)CC) ||
          !write_u32((uint32_t)frame_size)) {
        fprintf(stderr, "failed to write output header\n");
        return 1;
      }

      /* freq[] post-denormalise (raw CELT_SIG scale), per channel. */
      for (ch = 0; ch < (uint32_t)CC; ch++) {
        for (j = 0; j < N; j++) {
          if (!write_float((float)g_freq_capture[ch][j])) {
            fprintf(stderr, "failed to write freq\n");
            return 1;
          }
        }
      }
      /* imdct[] post-IMDCT / overlap-add (raw CELT_SIG scale), captured from the
       * comb_filter input before the postfilter runs in place. */
      for (ch = 0; ch < (uint32_t)CC; ch++) {
        for (j = 0; j < N; j++) {
          if (!write_float((float)g_imdct_capture[ch][j])) {
            fprintf(stderr, "failed to write imdct\n");
            return 1;
          }
        }
      }
      /* final[] post-deemphasis interleaved PCM. */
      for (j = 0; j < N * CC; j++) {
        if (!write_float(frame[j])) {
          fprintf(stderr, "failed to write final\n");
          return 1;
        }
      }

      opus_decoder_destroy(dec);
      dec = NULL;
      for (ch = 0; ch < 2; ch++) {
        free(g_freq_capture[ch]);
        free(g_imdct_capture[ch]);
        g_freq_capture[ch] = NULL;
        g_imdct_capture[ch] = NULL;
      }
      free(frame);
      return 0;
    }
  }

  fprintf(stderr, "target synthesis step was not decoded\n");
  if (dec) opus_decoder_destroy(dec);
  free(frame);
  return 1;
}
