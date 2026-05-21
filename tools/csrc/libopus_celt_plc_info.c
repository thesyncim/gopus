#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

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

#define INPUT_MAGIC "GCPI"
#define OUTPUT_MAGIC "GCPO"
#define PLC_LPC_ORDER 24
#define PLC_DECODE_BUFFER_SIZE 2048
#define PLC_MAX_PERIOD 1024
#define PLC_PITCH_LAG_MAX 720
#define PLC_PITCH_LAG_MIN 100

enum {
  MODE_LPC = 0,
  MODE_FIR = 1,
  MODE_IIR = 2,
  MODE_PITCH_DOWNSAMPLE = 3,
  MODE_PITCH_SEARCH = 4,
  MODE_REMOVE_DOUBLING = 5,
  MODE_PERIODIC_CONCEAL = 6
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
    ac[i] -= ac[i] * (0.008f * 0.008f) * (float)(i * i);
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

static int run_pitch_downsample(void) {
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

  pitch_downsample(planes, x_lp, (int)len, (int)channels, (int)factor, arch);
  if (!write_u32(len) || !write_float_array((const float *)x_lp, len)) {
    free(input);
    free(x_lp);
    return 0;
  }
  free(input);
  free(x_lp);
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
  int arch = opus_select_arch();
  uint32_t channels = 0;
  uint32_t frame_size = 0;
  uint32_t overlap = 0;
  uint32_t continue_periodic = 0;
  uint32_t last_pitch_period = 0;
  celt_coef *window = NULL;
  celt_sig *decode_mem = NULL;
  celt_sig *generated = NULL;
  opus_val16 *lp_pitch_buf = NULL;
  int pitch_index = 0;
  int count = 0;
  uint32_t c;

  if (!read_u32(&channels) || !read_u32(&frame_size) || !read_u32(&overlap) ||
      !read_u32(&continue_periodic) || !read_u32(&last_pitch_period)) {
    return 0;
  }
  if (channels == 0 || channels > 2 || frame_size == 0 || frame_size > PLC_DECODE_BUFFER_SIZE - PLC_MAX_PERIOD ||
      overlap == 0 || overlap > 960 || continue_periodic > 1) {
    return 0;
  }
  count = (int)(frame_size + overlap);
  if (count > PLC_DECODE_BUFFER_SIZE) return 0;

  window = (celt_coef *)malloc((size_t)overlap * sizeof(*window));
  decode_mem = (celt_sig *)calloc((size_t)channels * (PLC_DECODE_BUFFER_SIZE + overlap), sizeof(*decode_mem));
  generated = (celt_sig *)malloc((size_t)channels * (size_t)count * sizeof(*generated));
  lp_pitch_buf = (opus_val16 *)malloc((PLC_DECODE_BUFFER_SIZE >> 1) * sizeof(*lp_pitch_buf));
  if (window == NULL || decode_mem == NULL || generated == NULL || lp_pitch_buf == NULL) {
    free(window);
    free(decode_mem);
    free(generated);
    free(lp_pitch_buf);
    return 0;
  }
  if (!read_float_array((float *)window, overlap)) {
    free(window);
    free(decode_mem);
    free(generated);
    free(lp_pitch_buf);
    return 0;
  }
  for (c = 0; c < channels; c++) {
    celt_sig *hist = decode_mem + c * (PLC_DECODE_BUFFER_SIZE + overlap);
    if (!read_float_array((float *)hist, PLC_DECODE_BUFFER_SIZE)) {
      free(window);
      free(decode_mem);
      free(generated);
      free(lp_pitch_buf);
      return 0;
    }
  }

  if (continue_periodic && last_pitch_period >= 15 && last_pitch_period <= PLC_MAX_PERIOD) {
    pitch_index = (int)last_pitch_period;
  } else {
    celt_sig *planes[2] = {NULL, NULL};
    planes[0] = decode_mem;
    if (channels == 2) planes[1] = decode_mem + PLC_DECODE_BUFFER_SIZE + overlap;
    pitch_downsample(planes, lp_pitch_buf, PLC_DECODE_BUFFER_SIZE >> 1, (int)channels, 2, arch);
    pitch_search(lp_pitch_buf + (PLC_PITCH_LAG_MAX >> 1), lp_pitch_buf,
                 PLC_DECODE_BUFFER_SIZE - PLC_PITCH_LAG_MAX,
                 PLC_PITCH_LAG_MAX - PLC_PITCH_LAG_MIN, &pitch_index, arch);
    pitch_index = PLC_PITCH_LAG_MAX - pitch_index;
  }
  if (pitch_index < 15 || pitch_index > PLC_MAX_PERIOD) {
    free(window);
    free(decode_mem);
    free(generated);
    free(lp_pitch_buf);
    return 0;
  }

  for (c = 0; c < channels; c++) {
    celt_sig *buf = decode_mem + c * (PLC_DECODE_BUFFER_SIZE + overlap);
    opus_val16 lpc[PLC_LPC_ORDER];
    opus_val16 *exc = NULL;
    opus_val16 *fir_tmp = NULL;
    opus_val16 decay;
    opus_val16 attenuation;
    opus_val16 fade = continue_periodic ? QCONST16(.8f, 15) : Q15ONE;
    opus_val32 S1 = 0;
    int exc_length = 2 * pitch_index < PLC_MAX_PERIOD ? 2 * pitch_index : PLC_MAX_PERIOD;
    int extrapolation_offset = PLC_MAX_PERIOD - pitch_index;
    int i;
    int j;

    exc = (opus_val16 *)malloc((PLC_MAX_PERIOD + PLC_LPC_ORDER) * sizeof(*exc));
    fir_tmp = (opus_val16 *)malloc((size_t)exc_length * sizeof(*fir_tmp));
    if (exc == NULL || fir_tmp == NULL) {
      free(exc);
      free(fir_tmp);
      free(window);
      free(decode_mem);
      free(generated);
      free(lp_pitch_buf);
      return 0;
    }
    for (i = 0; i < PLC_MAX_PERIOD + PLC_LPC_ORDER; i++) {
      exc[i] = SROUND16(buf[PLC_DECODE_BUFFER_SIZE - PLC_MAX_PERIOD - PLC_LPC_ORDER + i], SIG_SHIFT);
    }

    if (!continue_periodic) {
      opus_val32 ac[PLC_LPC_ORDER + 1];
      _celt_autocorr(exc + PLC_LPC_ORDER, ac, window, (int)overlap,
                      PLC_LPC_ORDER, PLC_MAX_PERIOD, arch);
      ac[0] *= 1.0001f;
      for (i = 1; i <= PLC_LPC_ORDER; i++) {
        ac[i] -= ac[i] * (0.008f * 0.008f) * (float)(i * i);
      }
      _celt_lpc(lpc, ac, PLC_LPC_ORDER);
    } else {
      if (!read_float_array((float *)lpc, PLC_LPC_ORDER)) {
        free(exc);
        free(fir_tmp);
        free(window);
        free(decode_mem);
        free(generated);
        free(lp_pitch_buf);
        return 0;
      }
    }

    celt_fir(exc + PLC_LPC_ORDER + PLC_MAX_PERIOD - exc_length, lpc,
             fir_tmp, exc_length, PLC_LPC_ORDER, arch);
    OPUS_COPY(exc + PLC_LPC_ORDER + PLC_MAX_PERIOD - exc_length, fir_tmp, exc_length);

    {
      opus_val32 E1 = 1, E2 = 1;
      int decay_length = exc_length >> 1;
      for (i = 0; i < decay_length; i++) {
        opus_val16 e;
        e = exc[PLC_LPC_ORDER + PLC_MAX_PERIOD - decay_length + i];
        E1 += MULT16_16(e, e);
        e = exc[PLC_LPC_ORDER + PLC_MAX_PERIOD - 2 * decay_length + i];
        E2 += MULT16_16(e, e);
      }
      if (E1 > E2) E1 = E2;
      decay = celt_sqrt(frac_div32(SHR32(E1, 1), E2));
    }

    OPUS_MOVE(buf, buf + frame_size, PLC_DECODE_BUFFER_SIZE - frame_size);
    attenuation = MULT16_16_Q15(fade, decay);
    for (i = 0, j = 0; i < count; i++, j++) {
      opus_val16 tmp;
      if (j >= pitch_index) {
        j -= pitch_index;
        attenuation = MULT16_16_Q15(attenuation, decay);
      }
      buf[PLC_DECODE_BUFFER_SIZE - frame_size + i] =
          SHL32(EXTEND32(MULT16_16_Q15(attenuation, exc[PLC_LPC_ORDER + extrapolation_offset + j])), SIG_SHIFT);
      tmp = SROUND16(buf[PLC_DECODE_BUFFER_SIZE - PLC_MAX_PERIOD - frame_size + extrapolation_offset + j], SIG_SHIFT);
      S1 += MULT16_16(tmp, tmp);
    }

    {
      opus_val16 lpc_mem[PLC_LPC_ORDER];
      for (i = 0; i < PLC_LPC_ORDER; i++) {
        lpc_mem[i] = SROUND16(buf[PLC_DECODE_BUFFER_SIZE - frame_size - 1 - i], SIG_SHIFT);
      }
      celt_iir(buf + PLC_DECODE_BUFFER_SIZE - frame_size, lpc,
               buf + PLC_DECODE_BUFFER_SIZE - frame_size, count, PLC_LPC_ORDER,
               lpc_mem, arch);
    }

    {
      opus_val32 S2 = 0;
      for (i = 0; i < count; i++) {
        opus_val16 tmp = SROUND16(buf[PLC_DECODE_BUFFER_SIZE - frame_size + i], SIG_SHIFT);
        S2 += MULT16_16(tmp, tmp);
      }
      if (!(S1 > 0.2f * S2)) {
        for (i = 0; i < count; i++) {
          buf[PLC_DECODE_BUFFER_SIZE - frame_size + i] = 0;
        }
      } else if (S1 < S2) {
        opus_val16 ratio = celt_sqrt(frac_div32(SHR32(S1, 1) + 1, S2 + 1));
        for (i = 0; i < (int)overlap; i++) {
          opus_val16 tmp_g = Q15ONE - MULT16_16_Q15(COEF2VAL16(window[i]), Q15ONE - ratio);
          buf[PLC_DECODE_BUFFER_SIZE - frame_size + i] =
              MULT16_32_Q15(tmp_g, buf[PLC_DECODE_BUFFER_SIZE - frame_size + i]);
        }
        for (i = (int)overlap; i < count; i++) {
          buf[PLC_DECODE_BUFFER_SIZE - frame_size + i] =
              MULT16_32_Q15(ratio, buf[PLC_DECODE_BUFFER_SIZE - frame_size + i]);
        }
      }
    }

    for (i = 0; i < count; i++) {
      generated[c * count + i] = buf[PLC_DECODE_BUFFER_SIZE - frame_size + i];
    }
    free(exc);
    free(fir_tmp);
  }

  if (!write_u32((uint32_t)pitch_index) || !write_u32((uint32_t)count)) {
    free(window);
    free(decode_mem);
    free(generated);
    free(lp_pitch_buf);
    return 0;
  }
  for (c = 0; c < channels; c++) {
    if (!write_float_array((const float *)(generated + c * count), (uint32_t)count)) {
      free(window);
      free(decode_mem);
      free(generated);
      free(lp_pitch_buf);
      return 0;
    }
  }
  free(window);
  free(decode_mem);
  free(generated);
  free(lp_pitch_buf);
  return 1;
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
    ok = run_pitch_downsample();
  } else if (mode == MODE_PITCH_SEARCH) {
    ok = run_pitch_search();
  } else if (mode == MODE_REMOVE_DOUBLING) {
    ok = run_remove_doubling();
  } else if (mode == MODE_PERIODIC_CONCEAL) {
    ok = run_periodic_conceal();
  } else {
    return 1;
  }
  return ok ? 0 : 1;
}
