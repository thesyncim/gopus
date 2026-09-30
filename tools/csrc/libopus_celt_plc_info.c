#include <stdint.h>
#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define CELT_DECODER_C

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "celt/celt_lpc.h"
#include "celt/cpu_support.h"
#include "celt/mathops.h"
#include "celt/os_support.h"
#include "celt/pitch.h"

#ifdef GOPUS_CELT_PITCH_KERNEL_TRACE
#define PITCH_TRACE_MAX 32

static int write_u32(uint32_t value);
static int write_float_array(const float *src, uint32_t n);

extern int __real__celt_autocorr(const opus_val16 *x, opus_val32 *ac,
    const celt_coef *window, int overlap, int lag, int n, int arch);
extern void __real__celt_lpc(opus_val16 *lpc, const opus_val32 *ac, int p);
extern void __real_celt_pitch_xcorr_avx2(const float *x, const float *y,
    float *xcorr, int len, int max_pitch, int arch);

static int g_pitch_kernel_trace_enabled;
static uint32_t g_pitch_xcorr_calls;
static uint32_t g_pitch_autocorr_calls;
static uint32_t g_pitch_lpc_calls;
static uint32_t g_pitch_trace_overflow;
static uint32_t g_pitch_xcorr_len;
static uint32_t g_pitch_xcorr_max_pitch;
static uint32_t g_pitch_xcorr_arch;
static uint32_t g_pitch_xcorr_x_count;
static uint32_t g_pitch_xcorr_y_count;
static uint32_t g_pitch_xcorr_output_count;
static uint32_t g_pitch_autocorr_n;
static uint32_t g_pitch_autocorr_lag;
static uint32_t g_pitch_autocorr_arch;
static uint32_t g_pitch_autocorr_window_present;
static uint32_t g_pitch_autocorr_overlap;
static uint32_t g_pitch_autocorr_ac_count;
static uint32_t g_pitch_lpc_order;
static uint32_t g_pitch_lpc_ac_count;
static float g_pitch_xcorr_x[PITCH_TRACE_MAX];
static float g_pitch_xcorr_y[PITCH_TRACE_MAX];
static float g_pitch_xcorr_output[PITCH_TRACE_MAX];
static float g_pitch_autocorr_input[PITCH_TRACE_MAX];
static float g_pitch_autocorr_ac[PITCH_TRACE_MAX];
static float g_pitch_lpc_ac[PITCH_TRACE_MAX];
static float g_pitch_lpc_output[PITCH_TRACE_MAX];

void __wrap_celt_pitch_xcorr_avx2(const float *x, const float *y,
    float *xcorr, int len, int max_pitch, int arch) {
  uint32_t call = 0;
  if (g_pitch_kernel_trace_enabled) {
    call = g_pitch_xcorr_calls++;
    if (call == 0) {
      g_pitch_xcorr_len = len > 0 ? (uint32_t)len : 0;
      g_pitch_xcorr_max_pitch = max_pitch > 0 ? (uint32_t)max_pitch : 0;
      g_pitch_xcorr_arch = (uint32_t)arch;
      if (len < 0 || max_pitch < 0 || len > PITCH_TRACE_MAX ||
          max_pitch > PITCH_TRACE_MAX || len + max_pitch - 1 > PITCH_TRACE_MAX) {
        g_pitch_trace_overflow = 1;
      } else {
        uint32_t i;
        g_pitch_xcorr_x_count = (uint32_t)len;
        g_pitch_xcorr_y_count = (uint32_t)(len + max_pitch - 1);
        g_pitch_xcorr_output_count = (uint32_t)max_pitch;
        for (i = 0; i < g_pitch_xcorr_x_count; i++) g_pitch_xcorr_x[i] = x[i];
        for (i = 0; i < g_pitch_xcorr_y_count; i++) g_pitch_xcorr_y[i] = y[i];
      }
    }
  }

  __real_celt_pitch_xcorr_avx2(x, y, xcorr, len, max_pitch, arch);

  if (g_pitch_kernel_trace_enabled && call == 0 && !g_pitch_trace_overflow) {
    uint32_t i;
    for (i = 0; i < g_pitch_xcorr_output_count; i++) g_pitch_xcorr_output[i] = xcorr[i];
  }
}

int __wrap__celt_autocorr(const opus_val16 *x, opus_val32 *ac,
    const celt_coef *window, int overlap, int lag, int n, int arch) {
  uint32_t call = 0;
  if (g_pitch_kernel_trace_enabled) {
    call = g_pitch_autocorr_calls++;
    if (call == 0) {
      g_pitch_autocorr_n = n > 0 ? (uint32_t)n : 0;
      g_pitch_autocorr_lag = lag >= 0 ? (uint32_t)lag : 0;
      g_pitch_autocorr_arch = (uint32_t)arch;
      g_pitch_autocorr_window_present = window != NULL;
      g_pitch_autocorr_overlap = overlap >= 0 ? (uint32_t)overlap : 0;
      if (n < 0 || n > PITCH_TRACE_MAX || lag < 0 || lag + 1 > PITCH_TRACE_MAX) {
        g_pitch_trace_overflow = 1;
      } else {
        uint32_t i;
        for (i = 0; i < (uint32_t)n; i++) g_pitch_autocorr_input[i] = x[i];
      }
    }
  }

  int result = __real__celt_autocorr(x, ac, window, overlap, lag, n, arch);

  if (g_pitch_kernel_trace_enabled && call == 0 && !g_pitch_trace_overflow) {
    uint32_t i;
    g_pitch_autocorr_ac_count = (uint32_t)lag + 1;
    for (i = 0; i < g_pitch_autocorr_ac_count; i++) g_pitch_autocorr_ac[i] = ac[i];
  }
  return result;
}

void __wrap__celt_lpc(opus_val16 *lpc, const opus_val32 *ac, int p) {
  uint32_t call = 0;
  if (g_pitch_kernel_trace_enabled) {
    call = g_pitch_lpc_calls++;
    if (call == 0) {
      g_pitch_lpc_order = p > 0 ? (uint32_t)p : 0;
      if (p < 0 || p + 1 > PITCH_TRACE_MAX) {
        g_pitch_trace_overflow = 1;
      } else {
        uint32_t i;
        g_pitch_lpc_ac_count = (uint32_t)p + 1;
        for (i = 0; i < g_pitch_lpc_ac_count; i++) g_pitch_lpc_ac[i] = ac[i];
      }
    }
  }

  __real__celt_lpc(lpc, ac, p);

  if (g_pitch_kernel_trace_enabled && call == 0 && !g_pitch_trace_overflow) {
    uint32_t i;
    for (i = 0; i < (uint32_t)p; i++) g_pitch_lpc_output[i] = lpc[i];
  }
}

static void reset_pitch_kernel_trace(void) {
  g_pitch_xcorr_calls = 0;
  g_pitch_autocorr_calls = 0;
  g_pitch_lpc_calls = 0;
  g_pitch_trace_overflow = 0;
  g_pitch_xcorr_len = 0;
  g_pitch_xcorr_max_pitch = 0;
  g_pitch_xcorr_arch = 0;
  g_pitch_xcorr_x_count = 0;
  g_pitch_xcorr_y_count = 0;
  g_pitch_xcorr_output_count = 0;
  g_pitch_autocorr_n = 0;
  g_pitch_autocorr_lag = 0;
  g_pitch_autocorr_arch = 0;
  g_pitch_autocorr_window_present = 0;
  g_pitch_autocorr_overlap = 0;
  g_pitch_autocorr_ac_count = 0;
  g_pitch_lpc_order = 0;
  g_pitch_lpc_ac_count = 0;
}

static int write_pitch_kernel_trace(void) {
  if (g_pitch_trace_overflow || g_pitch_autocorr_n > PITCH_TRACE_MAX ||
      g_pitch_autocorr_ac_count > PITCH_TRACE_MAX ||
      g_pitch_lpc_order > PITCH_TRACE_MAX || g_pitch_lpc_ac_count > PITCH_TRACE_MAX) return 0;
  return write_u32(g_pitch_xcorr_calls) &&
      write_u32(g_pitch_xcorr_len) &&
      write_u32(g_pitch_xcorr_max_pitch) &&
      write_u32(g_pitch_xcorr_arch) &&
      write_u32(g_pitch_xcorr_x_count) &&
      write_u32(g_pitch_xcorr_y_count) &&
      write_u32(g_pitch_xcorr_output_count) &&
      write_float_array(g_pitch_xcorr_x, g_pitch_xcorr_x_count) &&
      write_float_array(g_pitch_xcorr_y, g_pitch_xcorr_y_count) &&
      write_float_array(g_pitch_xcorr_output, g_pitch_xcorr_output_count) &&
      write_u32(g_pitch_autocorr_calls) &&
      write_u32(g_pitch_lpc_calls) &&
      write_u32(g_pitch_trace_overflow) &&
      write_u32(g_pitch_autocorr_n) &&
      write_u32(g_pitch_autocorr_lag) &&
      write_u32(g_pitch_autocorr_arch) &&
      write_u32(g_pitch_autocorr_window_present) &&
      write_u32(g_pitch_autocorr_overlap) &&
      write_u32(g_pitch_autocorr_ac_count) &&
      write_float_array(g_pitch_autocorr_input, g_pitch_autocorr_n) &&
      write_float_array(g_pitch_autocorr_ac, g_pitch_autocorr_ac_count) &&
      write_u32(g_pitch_lpc_order) &&
      write_u32(g_pitch_lpc_ac_count) &&
      write_float_array(g_pitch_lpc_ac, g_pitch_lpc_ac_count) &&
      write_float_array(g_pitch_lpc_output, g_pitch_lpc_order);
}
#endif

#define INPUT_MAGIC "GCPI"
#define OUTPUT_MAGIC "GCPO"
#define PLC_LPC_ORDER 24
#define PLC_DECODE_BUFFER_SIZE 2048
#define PLC_MAX_PERIOD 1024

/* Mode 6 invokes the pinned decoder's actual loss path. Capture the decay
 * operands at the source MIN32 and its immediately following celt_sqrt call,
 * so the stage oracle reports values from the same frame/channel execution. */
static int g_capture_periodic_energy;
static int g_periodic_energy_count;
static int g_periodic_decay_pending;
static opus_val32 g_periodic_energy1[2];
static opus_val32 g_periodic_energy2[2];
static opus_val16 g_periodic_decay[2];

static opus_val32 gopus_capture_min32(opus_val32 a, opus_val32 b) {
  opus_val32 result = a < b ? a : b;
  if (g_capture_periodic_energy && g_periodic_energy_count < 2) {
    int channel = g_periodic_energy_count++;
    g_periodic_energy1[channel] = result;
    g_periodic_energy2[channel] = b;
    g_periodic_decay_pending = 1;
  }
  return result;
}

static opus_val16 gopus_capture_celt_sqrt(opus_val32 value) {
  opus_val16 result = (opus_val16)sqrt(value);
  if (g_capture_periodic_energy && g_periodic_decay_pending) {
    g_periodic_decay[g_periodic_energy_count - 1] = result;
    g_periodic_decay_pending = 0;
  }
  return result;
}

/* Compile the pinned decoder source with the selected reference configuration.
 * Standalone PLC loops can produce different reduction codegen, so retain the
 * complete celt_decode_lost() context. Linked leaves come from that archive. */
#undef MIN32
#define MIN32(a, b) gopus_capture_min32((a), (b))
#undef celt_sqrt
#define celt_sqrt(x) gopus_capture_celt_sqrt((x))
#include "celt/celt_decoder.c"
#undef MIN32
#undef celt_sqrt

enum {
  MODE_LPC = 0,
  MODE_FIR = 1,
  MODE_IIR = 2,
  MODE_PITCH_DOWNSAMPLE = 3,
  MODE_PITCH_SEARCH = 4,
  MODE_REMOVE_DOUBLING = 5,
  MODE_PERIODIC_CONCEAL = 6,
  MODE_RAW_AUTOCORR = 7,
  MODE_XCORR_KERNEL = 8,
  MODE_PITCH_DOWNSAMPLE_TRACE = 9
};

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

static int read_float(float *out) {
  union {
    uint32_t u;
    float f;
  } bits;
  if (!read_u32(&bits.u)) return 0;
  *out = bits.f;
  return 1;
}

static int write_float(float v) {
  union {
    float f;
    uint32_t u;
  } bits;
  bits.f = v;
  return write_u32(bits.u);
}

static int read_float_array(float *dst, uint32_t n) {
  uint32_t i;
  for (i = 0; i < n; i++) {
    if (!read_float(&dst[i])) return 0;
  }
  return 1;
}

static int write_float_array(const float *src, uint32_t n) {
  uint32_t i;
  for (i = 0; i < n; i++) {
    if (!write_float(src[i])) return 0;
  }
  return 1;
}

static int run_lpc(void) {
  int arch = opus_select_arch();
  uint32_t n = 0;
  uint32_t overlap = 0;
  opus_val16 *x = NULL;
  celt_coef *window = NULL;
  opus_val32 ac[PLC_LPC_ORDER + 1];
  opus_val16 lpc[PLC_LPC_ORDER];
  uint32_t i;

  if (!read_u32(&n) || !read_u32(&overlap)) return 0;
  if (n == 0 || overlap > n / 2 || n > 4096) return 0;

  x = (opus_val16 *)malloc((size_t)n * sizeof(*x));
  window = overlap == 0 ? NULL : (celt_coef *)malloc((size_t)overlap * sizeof(*window));
  if (x == NULL || (overlap != 0 && window == NULL)) {
    free(x);
    free(window);
    return 0;
  }
  if (overlap != 0 && !read_float_array((float *)window, overlap)) {
    free(x);
    free(window);
    return 0;
  }
  if (!read_float_array((float *)x, n)) {
    free(x);
    free(window);
    return 0;
  }

  _celt_autocorr(x, ac, window, (int)overlap, PLC_LPC_ORDER, (int)n, arch);
  ac[0] *= 1.0001f;
  for (i = 1; i <= PLC_LPC_ORDER; i++) {
    ac[i] -= ac[i] * (0.008f * 0.008f) * i * i;
  }
  _celt_lpc(lpc, ac, PLC_LPC_ORDER);

  if (!write_u32(PLC_LPC_ORDER)) {
    free(x);
    free(window);
    return 0;
  }
  if (!write_float_array((const float *)lpc, PLC_LPC_ORDER)) {
    free(x);
    free(window);
    return 0;
  }
  if (!write_u32(PLC_LPC_ORDER + 1)) {
    free(x);
    free(window);
    return 0;
  }
  if (!write_float_array((const float *)ac, PLC_LPC_ORDER + 1)) {
    free(x);
    free(window);
    return 0;
  }
  free(x);
  free(window);
  return 1;
}

static int run_fir(void) {
  int arch = opus_select_arch();
  uint32_t total = 0;
  uint32_t start = 0;
  uint32_t n = 0;
  opus_val16 *x = NULL;
  opus_val16 lpc[PLC_LPC_ORDER];
  opus_val16 *y = NULL;

  if (!read_u32(&total) || !read_u32(&start) || !read_u32(&n)) return 0;
  if (n == 0 || total > 4096 || start < PLC_LPC_ORDER || start + n > total) return 0;
  x = (opus_val16 *)malloc((size_t)total * sizeof(*x));
  y = (opus_val16 *)malloc((size_t)n * sizeof(*y));
  if (x == NULL || y == NULL) {
    free(x);
    free(y);
    return 0;
  }
  if (!read_float_array((float *)lpc, PLC_LPC_ORDER) || !read_float_array((float *)x, total)) {
    free(x);
    free(y);
    return 0;
  }

  celt_fir(x + start, lpc, y, (int)n, PLC_LPC_ORDER, arch);
  if (!write_u32(n) || !write_float_array((const float *)y, n)) {
    free(x);
    free(y);
    return 0;
  }
  free(x);
  free(y);
  return 1;
}

static int run_xcorr_kernel(void) {
  int arch = opus_select_arch();
  uint32_t order = 0;
  opus_val16 x[PLC_LPC_ORDER];
  opus_val16 y[PLC_LPC_ORDER + 3];
  opus_val32 sum[4];

  if (!read_u32(&order) || order == 0 || order > PLC_LPC_ORDER) return 0;
  if (!read_float_array((float *)sum, 4) ||
      !read_float_array((float *)x, order) ||
      !read_float_array((float *)y, order + 3)) return 0;

  xcorr_kernel(x, y, sum, (int)order, arch);
  return write_u32(4) && write_float_array((const float *)sum, 4);
}

static int run_iir(void) {
  int arch = opus_select_arch();
  uint32_t n = 0;
  uint32_t hist_n = 0;
  opus_val32 *x = NULL;
  opus_val16 lpc[PLC_LPC_ORDER];
  opus_val16 mem[PLC_LPC_ORDER];
  uint32_t i;

  if (!read_u32(&n) || !read_u32(&hist_n)) return 0;
  if (n == 0 || n > 4096 || hist_n < PLC_LPC_ORDER || hist_n > 8192) return 0;
  x = (opus_val32 *)malloc((size_t)n * sizeof(*x));
  if (x == NULL) return 0;
  if (!read_float_array((float *)lpc, PLC_LPC_ORDER)) {
    free(x);
    return 0;
  }
  for (i = 0; i < hist_n; i++) {
    float v;
    if (!read_float(&v)) {
      free(x);
      return 0;
    }
    if (i >= hist_n - PLC_LPC_ORDER) {
      mem[hist_n - 1 - i] = (opus_val16)v;
    }
  }
  if (!read_float_array((float *)x, n)) {
    free(x);
    return 0;
  }

  celt_iir(x, lpc, x, (int)n, PLC_LPC_ORDER, mem, arch);
  if (!write_u32(n) || !write_float_array((const float *)x, n)) {
    free(x);
    return 0;
  }
  free(x);
  return 1;
}

static int run_pitch_downsample(int capture_kernels) {
  int arch = opus_select_arch();
  uint32_t channels = 0;
  uint32_t len = 0;
  uint32_t factor = 0;
  uint32_t in_per_channel = 0;
  celt_sig *input = NULL;
  celt_sig *planes[2] = {NULL, NULL};
  opus_val16 *x_lp = NULL;

  if (!read_u32(&channels) || !read_u32(&len) || !read_u32(&factor)) return 0;
  if (channels == 0 || channels > 2 || len == 0 || len > 4096 || factor == 0 || factor > 8) return 0;
  in_per_channel = len * factor;
  input = (celt_sig *)malloc((size_t)channels * in_per_channel * sizeof(*input));
  x_lp = (opus_val16 *)malloc((size_t)len * sizeof(*x_lp));
  if (input == NULL || x_lp == NULL) {
    free(input);
    free(x_lp);
    return 0;
  }
  if (!read_float_array((float *)input, channels * in_per_channel)) {
    free(input);
    free(x_lp);
    return 0;
  }
  planes[0] = input;
  if (channels == 2) planes[1] = input + in_per_channel;

  if (capture_kernels) {
#ifdef GOPUS_CELT_PITCH_KERNEL_TRACE
    reset_pitch_kernel_trace();
    g_pitch_kernel_trace_enabled = 1;
#else
    free(input);
    free(x_lp);
    return 0;
#endif
  }
  pitch_downsample(planes, x_lp, (int)len, (int)channels, (int)factor, arch);
  if (capture_kernels) {
#ifdef GOPUS_CELT_PITCH_KERNEL_TRACE
    g_pitch_kernel_trace_enabled = 0;
#endif
  }
  if (!write_u32(len) || !write_float_array((const float *)x_lp, len)) {
    free(input);
    free(x_lp);
    return 0;
  }
#ifdef GOPUS_CELT_PITCH_KERNEL_TRACE
  if (capture_kernels && !write_pitch_kernel_trace()) {
    free(input);
    free(x_lp);
    return 0;
  }
#endif
  free(input);
  free(x_lp);
  return 1;
}

static int run_raw_autocorr(void) {
  int arch = opus_select_arch();
  uint32_t n = 0;
  uint32_t lag = 0;
  uint32_t overlap = 0;
  opus_val16 *x = NULL;
  celt_coef *window = NULL;
  opus_val32 *ac = NULL;

  if (!read_u32(&n) || !read_u32(&lag) || !read_u32(&overlap)) return 0;
  if (n == 0 || n > 4096 || lag >= n || lag > 64 || overlap > n / 2) return 0;
  x = (opus_val16 *)malloc((size_t)n * sizeof(*x));
  window = overlap == 0 ? NULL : (celt_coef *)malloc((size_t)overlap * sizeof(*window));
  ac = (opus_val32 *)malloc((size_t)(lag + 1) * sizeof(*ac));
  if (x == NULL || (overlap != 0 && window == NULL) || ac == NULL) {
    free(x);
    free(window);
    free(ac);
    return 0;
  }
  if ((overlap != 0 && !read_float_array((float *)window, overlap)) || !read_float_array((float *)x, n)) {
    free(x);
    free(window);
    free(ac);
    return 0;
  }

  _celt_autocorr(x, ac, window, (int)overlap, (int)lag, (int)n, arch);
  if (!write_u32(lag + 1) || !write_float_array((const float *)ac, lag + 1)) {
    free(x);
    free(window);
    free(ac);
    return 0;
  }
  free(x);
  free(window);
  free(ac);
  return 1;
}

static int run_pitch_search(void) {
  int arch = opus_select_arch();
  uint32_t len = 0;
  uint32_t max_pitch = 0;
  opus_val16 *x_lp = NULL;
  opus_val16 *y = NULL;
  int pitch = 0;

  if (!read_u32(&len) || !read_u32(&max_pitch)) return 0;
  if (len == 0 || len > 4096 || max_pitch == 0 || max_pitch > 4096) return 0;
  x_lp = (opus_val16 *)malloc((size_t)len * sizeof(*x_lp));
  y = (opus_val16 *)malloc((size_t)(len + max_pitch) * sizeof(*y));
  if (x_lp == NULL || y == NULL) {
    free(x_lp);
    free(y);
    return 0;
  }
  if (!read_float_array((float *)x_lp, len) || !read_float_array((float *)y, len + max_pitch)) {
    free(x_lp);
    free(y);
    return 0;
  }
  pitch_search(x_lp, y, (int)len, (int)max_pitch, &pitch, arch);
  if (!write_u32((uint32_t)(int32_t)pitch)) {
    free(x_lp);
    free(y);
    return 0;
  }
  free(x_lp);
  free(y);
  return 1;
}

static int run_remove_doubling(void) {
  int arch = opus_select_arch();
  uint32_t total = 0;
  uint32_t maxperiod = 0;
  uint32_t minperiod = 0;
  uint32_t n = 0;
  uint32_t t0_u32 = 0;
  uint32_t prev_period = 0;
  opus_val16 prev_gain = 0;
  opus_val16 *x = NULL;
  opus_val16 gain = 0;
  int t0 = 0;

  if (!read_u32(&total) || !read_u32(&maxperiod) || !read_u32(&minperiod) ||
      !read_u32(&n) || !read_u32(&t0_u32) || !read_u32(&prev_period) ||
      !read_float((float *)&prev_gain)) {
    return 0;
  }
  if (total == 0 || total > 8192 || maxperiod == 0 || minperiod == 0 ||
      n == 0 || maxperiod + n > total) {
    return 0;
  }
  x = (opus_val16 *)malloc((size_t)total * sizeof(*x));
  if (x == NULL) return 0;
  if (!read_float_array((float *)x, total)) {
    free(x);
    return 0;
  }

  t0 = (int)(int32_t)t0_u32;
  gain = remove_doubling(x, (int)maxperiod, (int)minperiod, (int)n,
                         &t0, (int)prev_period, prev_gain, arch);
  if (!write_u32((uint32_t)(int32_t)t0) || !write_float((float)gain)) {
    free(x);
    return 0;
  }
  free(x);
  return 1;
}

static int run_periodic_conceal(void) {
  uint32_t channels = 0;
  uint32_t frame_size = 0;
  uint32_t overlap = 0;
  uint32_t continue_periodic = 0;
  uint32_t last_pitch_period = 0;
  float *window = NULL;
  CELTDecoder *st = NULL;
  int decode_buffer_size = 0;
  int count = 0;
  int lm = 0;
  uint32_t c;

  if (!read_u32(&channels) || !read_u32(&frame_size) || !read_u32(&overlap) ||
      !read_u32(&continue_periodic) || !read_u32(&last_pitch_period)) {
    return 0;
  }
  if (channels == 0 || channels > 2 || frame_size < 120 || frame_size > 960 ||
      overlap == 0 || overlap > 960 || continue_periodic > 1) {
    return 0;
  }
  if (frame_size % 120 != 0 || ((frame_size / 120) & ((frame_size / 120) - 1)) != 0) return 0;
  for (count = 1; count < (int)(frame_size / 120); count <<= 1) lm++;
  count = (int)(frame_size + overlap);

  window = (celt_coef *)malloc((size_t)overlap * sizeof(*window));
  if (window == NULL) return 0;
  if (!read_float_array((float *)window, overlap)) {
    free(window);
    return 0;
  }
  st = (CELTDecoder *)calloc(1, (size_t)celt_decoder_get_size((int)channels));
  if (st == NULL || celt_decoder_init(st, 48000, (int)channels) != OPUS_OK) goto done;
  if (overlap != (uint32_t)st->overlap || memcmp(window, st->mode->window, overlap * sizeof(*window)) != 0) goto done;

  /* This mode consumes the 48 kHz synthetic history (QEXT scale 1). */
  decode_buffer_size = PLC_DECODE_BUFFER_SIZE;
  for (c = 0; c < channels; c++) {
    celt_sig *hist = st->_decode_mem + c * (decode_buffer_size + st->overlap);
    if (!read_float_array((float *)hist, PLC_DECODE_BUFFER_SIZE)) goto done;
  }

  st->plc_duration = 0;
  st->loss_duration = 0;
  st->skip_plc = 0;
  st->start = 0;
  st->last_frame_type = continue_periodic ? FRAME_PLC_PERIODIC : FRAME_NORMAL;
  if (continue_periodic) {
    opus_val16 *lpc;
    celt_glog *old_band_e;
    celt_glog *old_log_e;
    celt_glog *old_log_e2;
    celt_glog *background_log_e;
    if (last_pitch_period < 15 || last_pitch_period > PLC_MAX_PERIOD) goto done;
    st->last_pitch_index = (int)last_pitch_period;
    old_band_e = (celt_glog *)(st->_decode_mem +
        (decode_buffer_size + st->overlap) * channels);
    old_log_e = old_band_e + 2 * st->mode->nbEBands;
    old_log_e2 = old_log_e + 2 * st->mode->nbEBands;
    background_log_e = old_log_e2 + 2 * st->mode->nbEBands;
    lpc = (opus_val16 *)(background_log_e + 2 * st->mode->nbEBands);
    if (!read_float_array((float *)lpc, channels * PLC_LPC_ORDER)) goto done;
  }

  g_periodic_energy_count = 0;
  g_periodic_decay_pending = 0;
  g_capture_periodic_energy = 1;
#ifdef ENABLE_DEEP_PLC
  celt_decode_lost(st, (int)frame_size, lm, NULL);
#else
  celt_decode_lost(st, (int)frame_size, lm);
#endif
  g_capture_periodic_energy = 0;
  if (g_periodic_energy_count != (int)channels || g_periodic_decay_pending) goto done;

  if (!write_u32((uint32_t)st->last_pitch_index) || !write_u32((uint32_t)count)) goto done;
  for (c = 0; c < channels; c++) {
    celt_sig *hist = st->_decode_mem + c * (decode_buffer_size + st->overlap);
    if (!write_float_array((const float *)(hist + decode_buffer_size - frame_size), (uint32_t)count)) goto done;
  }
  for (c = 0; c < channels; c++) {
    if (!write_float((float)g_periodic_energy1[c]) ||
        !write_float((float)g_periodic_energy2[c]) ||
        !write_float((float)g_periodic_decay[c])) goto done;
  }

  free(st);
  free(window);
  return 1;

done:
  g_capture_periodic_energy = 0;
  free(st);
  free(window);
  return 0;
}

int main(void) {
  unsigned char magic[4];
  uint32_t version = 0;
  uint32_t mode = 0;
  int ok = 0;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) return 1;
  if (!read_u32(&version) || version != 1 || !read_u32(&mode)) return 1;
  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(mode)) return 1;

  if (mode == MODE_LPC) {
    ok = run_lpc();
  } else if (mode == MODE_FIR) {
    ok = run_fir();
  } else if (mode == MODE_IIR) {
    ok = run_iir();
  } else if (mode == MODE_PITCH_DOWNSAMPLE) {
    ok = run_pitch_downsample(0);
#ifdef GOPUS_CELT_PITCH_KERNEL_TRACE
  } else if (mode == MODE_PITCH_DOWNSAMPLE_TRACE) {
    ok = run_pitch_downsample(1);
#endif
  } else if (mode == MODE_PITCH_SEARCH) {
    ok = run_pitch_search();
  } else if (mode == MODE_REMOVE_DOUBLING) {
    ok = run_remove_doubling();
  } else if (mode == MODE_PERIODIC_CONCEAL) {
    ok = run_periodic_conceal();
  } else if (mode == MODE_RAW_AUTOCORR) {
    ok = run_raw_autocorr();
  } else if (mode == MODE_XCORR_KERNEL) {
    ok = run_xcorr_kernel();
  } else {
    return 1;
  }
  return ok ? 0 : 1;
}
