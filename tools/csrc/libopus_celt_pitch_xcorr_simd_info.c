#include <stdint.h>
#include <stdio.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "celt/pitch.h"
#include "celt/x86/x86cpu.h"

#define INPUT_MAGIC "GXCI"
#define OUTPUT_MAGIC "GXCO"

enum {
  CPU_AVX = 1u,
  CPU_AVX2 = 2u,
  CPU_FMA = 4u,
  DISPATCH_XCORR_AVX2 = 1u,
  DISPATCH_INNER_PROD_SSE = 2u
};

static int selected_arch;

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

static int read_exact(void *dst, size_t size) {
  return fread(dst, 1, size, stdin) == size;
}

static int write_exact(const void *src, size_t size) {
  return fwrite(src, 1, size, stdout) == size;
}

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact(b, sizeof(b))) return 0;
  *out = (uint32_t)b[0] |
         ((uint32_t)b[1] << 8) |
         ((uint32_t)b[2] << 16) |
         ((uint32_t)b[3] << 24);
  return 1;
}

static int write_u32(uint32_t value) {
  unsigned char b[4];
  b[0] = (unsigned char)(value & 0xffu);
  b[1] = (unsigned char)((value >> 8) & 0xffu);
  b[2] = (unsigned char)((value >> 16) & 0xffu);
  b[3] = (unsigned char)((value >> 24) & 0xffu);
  return write_exact(b, sizeof(b));
}

static uint32_t cpu_features(void) {
  uint32_t features = 0;
#if defined(__GNUC__) || defined(__clang__)
  __builtin_cpu_init();
  if (__builtin_cpu_supports("avx")) features |= CPU_AVX;
  if (__builtin_cpu_supports("avx2")) features |= CPU_AVX2;
  if (__builtin_cpu_supports("fma")) features |= CPU_FMA;
#endif
  return features;
}

static uint32_t selected_dispatches(int arch) {
  uint32_t dispatches = 0;
#if defined(OPUS_X86_PRESUME_AVX2) && !defined(FIXED_POINT)
  dispatches |= DISPATCH_XCORR_AVX2;
#elif defined(OPUS_HAVE_RTCD) && defined(OPUS_X86_MAY_HAVE_AVX2) && !defined(FIXED_POINT)
  if (PITCH_XCORR_IMPL[arch & OPUS_ARCHMASK] == celt_pitch_xcorr_avx2)
    dispatches |= DISPATCH_XCORR_AVX2;
#endif

#if defined(OPUS_X86_PRESUME_SSE) && !defined(FIXED_POINT)
  dispatches |= DISPATCH_INNER_PROD_SSE;
#elif defined(OPUS_HAVE_RTCD) && defined(OPUS_X86_MAY_HAVE_SSE) && !defined(FIXED_POINT)
  if (CELT_INNER_PROD_IMPL[arch & OPUS_ARCHMASK] == celt_inner_prod_sse)
    dispatches |= DISPATCH_INNER_PROD_SSE;
#endif
  return dispatches;
}

static int eval_record(void) {
  uint32_t raw;
  uint32_t length;
  uint32_t max_pitch;
  uint32_t i;
  float x[256];
  float y[512];
  float out[32];

  if (!read_u32(&length) || !read_u32(&max_pitch)) return 0;
  if (length == 0 || length > 256 || max_pitch == 0 || max_pitch > 32) return 0;
  for (i = 0; i < length; i++) {
    if (!read_u32(&raw)) return 0;
    memcpy(&x[i], &raw, sizeof(x[i]));
  }
  for (i = 0; i < length + max_pitch - 1; i++) {
    if (!read_u32(&raw)) return 0;
    memcpy(&y[i], &raw, sizeof(y[i]));
  }

  celt_pitch_xcorr(x, y, out, (int)length, (int)max_pitch, selected_arch);
  if (!write_u32(max_pitch)) return 0;
  for (i = 0; i < max_pitch; i++) {
    memcpy(&raw, &out[i], sizeof(raw));
    if (!write_u32(raw)) return 0;
  }
  return 1;
}

int main(void) {
  char magic[4];
  uint32_t version;
  uint32_t count;
  uint32_t features;
  uint32_t dispatches;
  uint32_t i;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) return 1;
  if (!read_u32(&version) || version != 1 || !read_u32(&count)) return 1;

  selected_arch = opus_select_arch();
  features = cpu_features();
  dispatches = selected_dispatches(selected_arch);
#if defined(GOPUS_REQUIRE_NATIVE_AVX2_FMA)
  if (selected_arch < 4 || (features & (CPU_AVX2 | CPU_FMA)) != (CPU_AVX2 | CPU_FMA) ||
      (dispatches & (DISPATCH_XCORR_AVX2 | DISPATCH_INNER_PROD_SSE)) !=
          (DISPATCH_XCORR_AVX2 | DISPATCH_INNER_PROD_SSE)) {
    fprintf(stderr,
        "paired libopus xcorr requires native AVX2/FMA dispatch: arch=%u cpu=%u dispatch=%u\n",
        (uint32_t)selected_arch, features, dispatches);
    return 1;
  }
#endif

  if (!write_exact(OUTPUT_MAGIC, sizeof(magic)) || !write_u32(2) ||
      !write_u32((uint32_t)selected_arch) || !write_u32(features) || !write_u32(dispatches) ||
      !write_u32(count)) return 1;
  for (i = 0; i < count; i++) {
    if (!eval_record()) return 1;
  }
  return 0;
}
