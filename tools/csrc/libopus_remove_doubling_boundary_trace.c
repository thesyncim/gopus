#include <math.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef HAVE_CONFIG_H
#include "config.h"
#endif

#include "celt/pitch.h"
#include "celt/mathops.h"
#include "celt/cpu_support.h"

#define REMOVE_TRACE_MAX_DUAL 16
#define REMOVE_TRACE_MAX_YY 512
#define REMOVE_TRACE_MAX_GAIN 16

typedef struct {
  uint32_t dual_count;
  float dual_first[REMOVE_TRACE_MAX_DUAL];
  float dual_second[REMOVE_TRACE_MAX_DUAL];
  uint32_t yy_count;
  uint32_t yy_index[REMOVE_TRACE_MAX_YY];
  float yy_x_before[REMOVE_TRACE_MAX_YY];
  float yy_x_after[REMOVE_TRACE_MAX_YY];
  float yy_raw[REMOVE_TRACE_MAX_YY];
  float yy_lookup[REMOVE_TRACE_MAX_YY];
  uint32_t gain_count;
  float gain_xy[REMOVE_TRACE_MAX_GAIN];
  float gain_xx[REMOVE_TRACE_MAX_GAIN];
  float gain_yy[REMOVE_TRACE_MAX_GAIN];
  float gain_result[REMOVE_TRACE_MAX_GAIN];
  uint32_t sqrt_count;
  float gain_denominator[REMOVE_TRACE_MAX_GAIN];
  float gain_sqrt[REMOVE_TRACE_MAX_GAIN];
  const opus_val16 *first_x;
  int32_t n;
  uint32_t expected_yy_count;
  uint32_t overflow;
} remove_trace_capture;

static remove_trace_capture g_remove_trace;
static int g_remove_trace_active;

static opus_val16 capture_pitch_gain(opus_val32 xy, opus_val32 xx,
    opus_val32 yy, opus_val16 result) {
  if (g_remove_trace_active) {
    if (g_remove_trace.gain_count >= REMOVE_TRACE_MAX_GAIN) {
      g_remove_trace.overflow = 1;
    } else {
      uint32_t at = g_remove_trace.gain_count++;
      g_remove_trace.gain_xy[at] = xy;
      g_remove_trace.gain_xx[at] = xx;
      g_remove_trace.gain_yy[at] = yy;
      g_remove_trace.gain_result[at] = result;
    }
  }
  return result;
}

static int write_u32(uint32_t value) {
  unsigned char bytes[4];
  bytes[0] = (unsigned char)value;
  bytes[1] = (unsigned char)(value >> 8);
  bytes[2] = (unsigned char)(value >> 16);
  bytes[3] = (unsigned char)(value >> 24);
  return fwrite(bytes, sizeof(bytes), 1, stdout) == 1;
}

static int read_u32(uint32_t *value) {
  unsigned char bytes[4];
  if (fread(bytes, sizeof(bytes), 1, stdin) != 1) return 0;
  *value = (uint32_t)bytes[0] | ((uint32_t)bytes[1] << 8) |
      ((uint32_t)bytes[2] << 16) | ((uint32_t)bytes[3] << 24);
  return 1;
}

static int write_f32(float value) {
  uint32_t bits;
  memcpy(&bits, &value, sizeof(bits));
  return write_u32(bits);
}

static int read_f32(float *value) {
  uint32_t bits;
  if (!read_u32(&bits)) return 0;
  memcpy(value, &bits, sizeof(bits));
  return 1;
}

static void capture_dual_inner_product(const opus_val16 *x,
    const opus_val16 *y1, const opus_val16 *y2, int n,
    opus_val32 *xy1, opus_val32 *xy2, int arch) {
#if defined(OPUS_X86_PRESUME_SSE) && !defined(FIXED_POINT)
  dual_inner_prod_sse(x, y1, y2, n, xy1, xy2);
#elif defined(OPUS_HAVE_RTCD) && defined(OPUS_X86_MAY_HAVE_SSE) && !defined(FIXED_POINT)
  DUAL_INNER_PROD_IMPL[arch & OPUS_ARCHMASK](x, y1, y2, n, xy1, xy2);
#else
  (void)arch;
  dual_inner_prod_c(x, y1, y2, n, xy1, xy2);
#endif
  if (!g_remove_trace_active) return;
  if (g_remove_trace.dual_count >= REMOVE_TRACE_MAX_DUAL) {
    g_remove_trace.overflow = 1;
    return;
  }
  if (g_remove_trace.dual_count == 0) {
    g_remove_trace.first_x = x;
    g_remove_trace.n = n;
  }
  g_remove_trace.dual_first[g_remove_trace.dual_count] = *xy1;
  g_remove_trace.dual_second[g_remove_trace.dual_count] = *xy2;
  g_remove_trace.dual_count++;
}

static OPUS_INLINE opus_val32 capture_max32(opus_val32 left, opus_val32 right) {
  opus_val32 result = left > right ? left : right;
  if (!g_remove_trace_active || left != 0 ||
      g_remove_trace.yy_count >= g_remove_trace.expected_yy_count) return result;
  if (g_remove_trace.first_x == NULL || g_remove_trace.yy_count >= REMOVE_TRACE_MAX_YY) {
    g_remove_trace.overflow = 1;
    return result;
  }
  {
    uint32_t at = g_remove_trace.yy_count;
    int32_t i = (int32_t)at + 1;
    g_remove_trace.yy_index[at] = (uint32_t)i;
    g_remove_trace.yy_x_before[at] = g_remove_trace.first_x[-i];
    g_remove_trace.yy_x_after[at] = g_remove_trace.first_x[g_remove_trace.n - i];
    g_remove_trace.yy_raw[at] = right;
    g_remove_trace.yy_lookup[at] = result;
    g_remove_trace.yy_count++;
  }
  return result;
}

static OPUS_INLINE opus_val32 capture_celt_sqrt(opus_val32 value) {
  opus_val32 root = (opus_val32)sqrt((double)value);
  if (g_remove_trace_active) {
    if (g_remove_trace.sqrt_count >= REMOVE_TRACE_MAX_GAIN) {
      g_remove_trace.overflow = 1;
    } else {
      uint32_t at = g_remove_trace.sqrt_count++;
      g_remove_trace.gain_denominator[at] = value;
      g_remove_trace.gain_sqrt[at] = root;
    }
  }
  return root;
}

/* The source below is the selected reference's original pitch.c. These
 * wrappers observe its actual locals while retaining its chosen kernels. */
#undef dual_inner_prod
#define dual_inner_prod(x, y1, y2, n, xy1, xy2, arch) \
  capture_dual_inner_product((x), (y1), (y2), (n), (xy1), (xy2), (arch))

#undef MAX32
#define MAX32(a, b) capture_max32((a), (b))

#undef celt_sqrt
#define celt_sqrt(x) capture_celt_sqrt((x))

#define pitch_downsample gopus_instrumented_pitch_downsample
#define celt_pitch_xcorr_c gopus_instrumented_celt_pitch_xcorr_c
#define pitch_search gopus_instrumented_pitch_search
#define remove_doubling gopus_instrumented_remove_doubling

/* pitch.c includes config.h itself when HAVE_CONFIG_H is set. The selected
 * config is already loaded above; suppress a second unguarded inclusion. */
#undef HAVE_CONFIG_H
#include "GOPUS_INSTRUMENTED_PITCH_SOURCE"

#undef pitch_downsample
#undef celt_pitch_xcorr_c
#undef pitch_search
#undef remove_doubling

extern opus_val16 remove_doubling(opus_val16 *x, int maxperiod, int minperiod,
    int n, int *t0, int prev_period, opus_val16 prev_gain, int arch);

static void reset_remove_trace(uint32_t maxperiod) {
  memset(&g_remove_trace, 0, sizeof(g_remove_trace));
  g_remove_trace.expected_yy_count = maxperiod >> 1;
}

static int write_remove_trace(void) {
  uint32_t i;
  if (!write_u32(g_remove_trace.dual_count)) return 0;
  for (i = 0; i < g_remove_trace.dual_count; i++) {
    if (!write_f32(g_remove_trace.dual_first[i]) ||
        !write_f32(g_remove_trace.dual_second[i])) return 0;
  }
  if (!write_u32(g_remove_trace.yy_count)) return 0;
  for (i = 0; i < g_remove_trace.yy_count; i++) {
    if (!write_u32(g_remove_trace.yy_index[i]) ||
        !write_f32(g_remove_trace.yy_x_before[i]) ||
        !write_f32(g_remove_trace.yy_x_after[i]) ||
        !write_f32(g_remove_trace.yy_raw[i]) ||
        !write_f32(g_remove_trace.yy_lookup[i])) return 0;
  }
  if (!write_u32(g_remove_trace.gain_count)) return 0;
  for (i = 0; i < g_remove_trace.gain_count; i++) {
    if (!write_f32(g_remove_trace.gain_xy[i]) ||
        !write_f32(g_remove_trace.gain_xx[i]) ||
        !write_f32(g_remove_trace.gain_yy[i]) ||
        !write_f32(g_remove_trace.gain_result[i])) return 0;
  }
  if (!write_u32(g_remove_trace.sqrt_count)) return 0;
  for (i = 0; i < g_remove_trace.sqrt_count; i++) {
    if (!write_f32(g_remove_trace.gain_denominator[i]) ||
        !write_f32(g_remove_trace.gain_sqrt[i])) return 0;
  }
  return write_u32(g_remove_trace.overflow);
}

int main(void) {
  unsigned char magic[4];
  uint32_t version, total, maxperiod, minperiod, n, t0_bits, prev_period, arch_bits;
  float prev_gain;
  opus_val16 *x = NULL;
  int t0_source, t0_archive;
  opus_val16 gain_source, gain_archive;
  uint32_t i;
  int arch;

  if (fread(magic, sizeof(magic), 1, stdin) != 1 || memcmp(magic, "GPRQ", 4) != 0 ||
      !read_u32(&version) || version != 2 || !read_u32(&total) ||
      !read_u32(&maxperiod) || !read_u32(&minperiod) || !read_u32(&n) ||
      !read_u32(&t0_bits) || !read_u32(&prev_period) || !read_u32(&arch_bits) ||
      !read_f32(&prev_gain)) return 1;
  if (total == 0 || total > 8192 || maxperiod < 2 || maxperiod > 1024 ||
      minperiod == 0 || minperiod > maxperiod || n == 0 || n > 960 ||
      (maxperiod >> 1) + (n >> 1) > total) return 1;

  x = (opus_val16 *)malloc((size_t)total * sizeof(*x));
  if (x == NULL) return 1;
  for (i = 0; i < total; i++) {
    if (!read_f32(&x[i])) {
      free(x);
      return 1;
    }
  }

  t0_source = (int32_t)t0_bits;
  t0_archive = t0_source;
  arch = (int32_t)arch_bits;
  reset_remove_trace(maxperiod);
  g_remove_trace_active = 1;
  gain_source = gopus_instrumented_remove_doubling(x, (int)maxperiod,
      (int)minperiod, (int)n, &t0_source, (int)prev_period, prev_gain, arch);
  g_remove_trace_active = 0;
  gain_archive = remove_doubling(x, (int)maxperiod, (int)minperiod,
      (int)n, &t0_archive, (int)prev_period, prev_gain, arch);

  if (fwrite("GPRS", 4, 1, stdout) != 1 || !write_u32(2) ||
      !write_u32(g_remove_trace.overflow) ||
      !write_u32((uint32_t)arch) ||
      !write_u32((uint32_t)t0_source) || !write_f32(gain_source) ||
      !write_u32((uint32_t)t0_archive) || !write_f32(gain_archive) ||
      !write_remove_trace()) {
    free(x);
    return 1;
  }
  free(x);
  return 0;
}
