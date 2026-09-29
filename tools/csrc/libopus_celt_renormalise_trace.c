/* Capture the live float CELT renormalise_vector inputs and outputs while
 * decoding one packet through the selected libopus reference. */

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "opus.h"
#include "celt/arch.h"
#include "celt/bands.h"
#include "celt/celt.h"
#include "celt/modes.h"
#include "celt/vq.h"

#define INPUT_MAGIC "GARI"
#define OUTPUT_MAGIC "GARO"
#define TRACE_VERSION 1
#define MAX_CALLS 64
#define MAX_VECTOR 2048

typedef struct {
  uint32_t n;
  float gain;
  float before[MAX_VECTOR];
  float after[MAX_VECTOR];
} renorm_call;

static renorm_call g_calls[MAX_CALLS];
static uint32_t g_call_count;
static uint32_t g_anti_collapse_calls;
static uint32_t g_overflow;
static int g_capture;
static int g_inside_anti_collapse;

extern void __real_anti_collapse(const CELTMode *m, celt_norm *X_,
    unsigned char *collapse_masks, int LM, int C, int size, int start, int end,
    const celt_glog *logE, const celt_glog *prev1logE,
    const celt_glog *prev2logE, const int *pulses, opus_uint32 seed,
    int encode, int arch);
extern void __real_renormalise_vector(celt_norm *X, int N, opus_val32 gain, int arch);

void __wrap_anti_collapse(const CELTMode *m, celt_norm *X_,
    unsigned char *collapse_masks, int LM, int C, int size, int start, int end,
    const celt_glog *logE, const celt_glog *prev1logE,
    const celt_glog *prev2logE, const int *pulses, opus_uint32 seed,
    int encode, int arch) {
  if (g_capture) g_anti_collapse_calls++;
  g_inside_anti_collapse++;
  __real_anti_collapse(m, X_, collapse_masks, LM, C, size, start, end,
      logE, prev1logE, prev2logE, pulses, seed, encode, arch);
  g_inside_anti_collapse--;
}

void __wrap_renormalise_vector(celt_norm *X, int N, opus_val32 gain, int arch) {
  renorm_call *call = NULL;
  int i;
  if (g_capture && g_inside_anti_collapse) {
    if (g_call_count >= MAX_CALLS || N <= 0 || N > MAX_VECTOR) {
      g_overflow = 1;
    } else {
      call = &g_calls[g_call_count++];
      call->n = (uint32_t)N;
      call->gain = (float)gain;
      for (i = 0; i < N; i++) call->before[i] = (float)X[i];
    }
  }
  __real_renormalise_vector(X, N, gain, arch);
  if (call != NULL) {
    for (i = 0; i < N; i++) call->after[i] = (float)X[i];
  }
}

static int read_exact(void *dst, size_t n) { return fread(dst, 1, n, stdin) == n; }
static int write_exact(const void *src, size_t n) { return fwrite(src, 1, n, stdout) == n; }

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact(b, sizeof(b))) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) |
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

static int write_float(float value) {
  union { float f; uint32_t u; } bits;
  bits.f = value;
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
  uint32_t version, sample_rate, channels, frame_size, packet_size;
  unsigned char *packet = NULL;
  float *pcm = NULL;
  OpusDecoder *decoder = NULL;
  int error = OPUS_OK;
  int decoded;
  int status = 1;
  uint32_t i, j;

  if (!set_binary_stdio() || !read_exact(magic, sizeof(magic)) ||
      memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0 ||
      !read_u32(&version) || version != TRACE_VERSION ||
      !read_u32(&sample_rate) || !read_u32(&channels) ||
      !read_u32(&frame_size) || !read_u32(&packet_size)) return 1;
  if ((sample_rate != 48000 && sample_rate != 24000 && sample_rate != 16000 &&
       sample_rate != 12000 && sample_rate != 8000) ||
      (channels != 1 && channels != 2) || frame_size == 0 || frame_size > 2048 ||
      packet_size < 2 || packet_size > 4096) return 2;

  packet = (unsigned char *)malloc(packet_size);
  pcm = (float *)malloc((size_t)frame_size * channels * sizeof(*pcm));
  if (packet == NULL || pcm == NULL || !read_exact(packet, packet_size)) goto done;
  decoder = opus_decoder_create((opus_int32)sample_rate, (int)channels, &error);
  if (decoder == NULL || error != OPUS_OK) goto done;

  g_call_count = 0;
  g_anti_collapse_calls = 0;
  g_overflow = 0;
  g_capture = 1;
  decoded = opus_decode_float(decoder, packet, (opus_int32)packet_size,
      pcm, (int)frame_size, 0);
  g_capture = 0;
  if (decoded < 0) goto done;

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(TRACE_VERSION) ||
      !write_u32((uint32_t)decoded) || !write_u32(g_anti_collapse_calls) ||
      !write_u32(g_call_count) ||
      !write_u32(g_overflow)) goto done;
  for (i = 0; i < g_call_count; i++) {
    if (!write_u32(g_calls[i].n) || !write_float(g_calls[i].gain)) goto done;
    for (j = 0; j < g_calls[i].n; j++) if (!write_float(g_calls[i].before[j])) goto done;
    for (j = 0; j < g_calls[i].n; j++) if (!write_float(g_calls[i].after[j])) goto done;
  }
  for (i = 0; i < (uint32_t)decoded * channels; i++) if (!write_float(pcm[i])) goto done;
  status = 0;

done:
  g_capture = 0;
  if (decoder != NULL) opus_decoder_destroy(decoder);
  free(pcm);
  free(packet);
  if (ferror(stdout)) return 3;
  return status;
}
