/* Native 96 kHz QEXT CELT PLC pitch-period trace. */
#include <stdint.h>
#include <math.h>
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
#include "celt_lpc.h"
#include "mathops.h"

#define GOPUS_TRACE_FIR_MAX 2048
static int trace_lpc_calls;
static float trace_lpc[2][CELT_LPC_ORDER];
static int trace_fir_calls;
static int trace_fir_len[2];
static float trace_fir[2][GOPUS_TRACE_FIR_MAX];
static int trace_sqrt_calls;
static float trace_sqrt_arg[8];
static float trace_sqrt_result[8];
static int trace_iir_calls;
static int trace_iir_len[2];
static float trace_iir_input[2][GOPUS_TRACE_FIR_MAX + 512];
static float trace_iir_mem[2][CELT_LPC_ORDER];
static float trace_iir_output[2][GOPUS_TRACE_FIR_MAX + 512];

static void gopus_trace_celt_lpc(opus_val16 *lpc, const opus_val32 *ac, int p);
static void gopus_trace_celt_fir_c(const opus_val16 *x, const opus_val16 *num,
      opus_val16 *y, int N, int ord, int arch);
static float gopus_trace_sqrt(float x);
static void gopus_trace_celt_iir(const opus_val32 *x, const opus_val16 *den,
      opus_val32 *y, int N, int ord, opus_val16 *mem, int arch);

#define _celt_lpc gopus_trace_celt_lpc
#define celt_fir_c gopus_trace_celt_fir_c
#define celt_iir gopus_trace_celt_iir
#undef celt_sqrt
#define celt_sqrt(x) gopus_trace_sqrt((float)(x))

#include "celt/celt_decoder.c"

#undef _celt_lpc
#undef celt_fir_c
#undef celt_iir
#undef celt_sqrt

static void gopus_trace_celt_lpc(opus_val16 *lpc, const opus_val32 *ac, int p) {
  int index = trace_lpc_calls++;
  _celt_lpc(lpc, ac, p);
  if (index < 2 && p == CELT_LPC_ORDER) {
    for (int i = 0; i < p; i++) trace_lpc[index][i] = (float)lpc[i];
  }
}

static void gopus_trace_celt_fir_c(const opus_val16 *x, const opus_val16 *num,
      opus_val16 *y, int N, int ord, int arch) {
  int index = trace_fir_calls++;
  celt_fir_c(x, num, y, N, ord, arch);
  if (index < 2 && N <= GOPUS_TRACE_FIR_MAX) {
    trace_fir_len[index] = N;
    for (int i = 0; i < N; i++) trace_fir[index][i] = (float)y[i];
  }
}

static float gopus_trace_sqrt(float x) {
  int index = trace_sqrt_calls++;
  float result = (float)sqrt((double)x);
  if (index < 8) {
    trace_sqrt_arg[index] = x;
    trace_sqrt_result[index] = result;
  }
  return result;
}

static void gopus_trace_celt_iir(const opus_val32 *x, const opus_val16 *den,
      opus_val32 *y, int N, int ord, opus_val16 *mem, int arch) {
  int index = trace_iir_calls++;
  if (index < 2 && N <= GOPUS_TRACE_FIR_MAX + 512 && ord == CELT_LPC_ORDER) {
    trace_iir_len[index] = N;
    for (int i = 0; i < N; i++) trace_iir_input[index][i] = (float)x[i];
    for (int i = 0; i < ord; i++) trace_iir_mem[index][i] = (float)mem[i];
  }
  celt_iir(x, den, y, N, ord, mem, arch);
  if (index < 2 && N <= GOPUS_TRACE_FIR_MAX + 512) {
    for (int i = 0; i < N; i++) trace_iir_output[index][i] = (float)y[i];
  }
}

typedef struct { int celt_dec_offset; } gopus_opus_decoder_prefix;

static int read_exact(void *dst, size_t n) { return fread(dst, 1, n, stdin) == n; }
static int write_exact(const void *src, size_t n) { return fwrite(src, 1, n, stdout) == n; }

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact(b, sizeof(b))) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) |
         ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int write_u32(uint32_t v) {
  unsigned char b[4];
  b[0] = (unsigned char)(v & 0xff);
  b[1] = (unsigned char)((v >> 8) & 0xff);
  b[2] = (unsigned char)((v >> 16) & 0xff);
  b[3] = (unsigned char)((v >> 24) & 0xff);
  return write_exact(b, sizeof(b));
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
  uint32_t version, channels, packet_len;
  unsigned char *packet = NULL;
  float *pcm = NULL;
  OpusDecoder *dec = NULL;
  CELTDecoder *celt = NULL;
  int err = OPUS_OK;
  int decoded;
  int qext_scale;
  int decode_buffer_size;
  int frame_size = 1920;
  int channel;

  if (!set_binary_stdio()) { fprintf(stderr, "binary stdio\n"); return 1; }
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, "GCLI", 4) != 0) { fprintf(stderr, "input magic\n"); return 1; }
  if (!read_u32(&version) || version != 1 || !read_u32(&channels) ||
      channels == 0 || channels > 2 || !read_u32(&packet_len) || packet_len == 0 ||
      packet_len > 1275) { fprintf(stderr, "input header\n"); return 1; }
  packet = (unsigned char *)malloc(packet_len);
  pcm = (float *)malloc((size_t)frame_size * channels * sizeof(*pcm));
  if (packet == NULL || pcm == NULL || !read_exact(packet, packet_len)) { fprintf(stderr, "input packet\n"); return 1; }

  dec = opus_decoder_create(96000, (int)channels, &err);
  if (dec == NULL || err != OPUS_OK) { fprintf(stderr, "decoder create %d\n", err); return 1; }
  celt = (CELTDecoder *)((unsigned char *)dec +
      ((gopus_opus_decoder_prefix *)dec)->celt_dec_offset);

  /* Establish the same two-frame received history as the Go native-96 test. */
  for (int i = 0; i < 2; i++) {
    decoded = opus_decode_float(dec, packet, (opus_int32)packet_len, pcm, frame_size, 0);
    if (decoded != frame_size) { fprintf(stderr, "received decode %d\n", decoded); return 1; }
  }
  trace_lpc_calls = 0;
  trace_fir_calls = 0;
  trace_sqrt_calls = 0;
  trace_iir_calls = 0;
  decoded = opus_decode_float(dec, NULL, 0, pcm, frame_size, 0);
  if (decoded != frame_size) { fprintf(stderr, "lost decode %d\n", decoded); return 1; }

  qext_scale = celt->qext_scale;
  decode_buffer_size = QEXT_SCALE(DEC_PITCH_BUF_SIZE);
  if (qext_scale != 2 || celt->overlap != 240) { fprintf(stderr, "geometry qext=%d overlap=%d\n", qext_scale, celt->overlap); return 1; }

  if (!write_exact("GCLO", 4) || !write_u32(1) ||
      !write_u32((uint32_t)celt->last_pitch_index) ||
      !write_u32((uint32_t)decoded) || !write_u32(channels)) { fprintf(stderr, "output header\n"); return 1; }

  for (channel = 0; channel < (int)channels; channel++) {
    celt_sig *mem = celt->_decode_mem + channel * (decode_buffer_size + celt->overlap);
    celt_sig *out_syn = mem + decode_buffer_size - frame_size;
    for (int i = 0; i < frame_size; i++) {
      union { float f; uint32_t u; } bits;
      bits.f = (float)out_syn[i];
      if (!write_u32(bits.u)) { fprintf(stderr, "output synthesis\n"); return 1; }
    }
  }
  for (channel = 0; channel < (int)channels; channel++) {
    union { float f; uint32_t u; } bits;
    bits.f = (float)celt->preemph_memD[channel];
    if (!write_u32(bits.u)) { fprintf(stderr, "output preemph\n"); return 1; }
  }
  if (!write_u32((uint32_t)trace_lpc_calls) || !write_u32((uint32_t)trace_fir_calls)) { fprintf(stderr, "output trace counts\n"); return 1; }
  for (int i = 0; i < trace_lpc_calls && i < 2; i++) {
    for (int j = 0; j < CELT_LPC_ORDER; j++) {
      union { float f; uint32_t u; } bits;
      bits.f = trace_lpc[i][j];
      if (!write_u32(bits.u)) { fprintf(stderr, "output lpc\n"); return 1; }
    }
  }
  for (int i = 0; i < trace_fir_calls && i < 2; i++) {
    if (!write_u32((uint32_t)trace_fir_len[i])) { fprintf(stderr, "output fir len\n"); return 1; }
    for (int j = 0; j < trace_fir_len[i]; j++) {
      union { float f; uint32_t u; } bits;
      bits.f = trace_fir[i][j];
      if (!write_u32(bits.u)) { fprintf(stderr, "output fir\n"); return 1; }
    }
  }
  if (!write_u32((uint32_t)trace_sqrt_calls)) { fprintf(stderr, "output sqrt count\n"); return 1; }
  for (int i = 0; i < trace_sqrt_calls && i < 8; i++) {
    union { float f; uint32_t u; } arg, result;
    arg.f = trace_sqrt_arg[i];
    result.f = trace_sqrt_result[i];
    if (!write_u32(arg.u) || !write_u32(result.u)) { fprintf(stderr, "output sqrt\n"); return 1; }
  }
  if (!write_u32((uint32_t)trace_iir_calls)) { fprintf(stderr, "output iir count\n"); return 1; }
  for (int i = 0; i < trace_iir_calls && i < 2; i++) {
    if (!write_u32((uint32_t)trace_iir_len[i])) { fprintf(stderr, "output iir len\n"); return 1; }
    for (int j = 0; j < trace_iir_len[i]; j++) {
      union { float f; uint32_t u; } bits;
      bits.f = trace_iir_input[i][j];
      if (!write_u32(bits.u)) { fprintf(stderr, "output iir input\n"); return 1; }
    }
    for (int j = 0; j < CELT_LPC_ORDER; j++) {
      union { float f; uint32_t u; } bits;
      bits.f = trace_iir_mem[i][j];
      if (!write_u32(bits.u)) { fprintf(stderr, "output iir mem\n"); return 1; }
    }
    for (int j = 0; j < trace_iir_len[i]; j++) {
      union { float f; uint32_t u; } bits;
      bits.f = trace_iir_output[i][j];
      if (!write_u32(bits.u)) { fprintf(stderr, "output iir\n"); return 1; }
    }
  }

  opus_decoder_destroy(dec);
  free(packet);
  free(pcm);
  return 0;
}
