/* Native intrinsic diagnostic for libopus 1.6.1
 * celt/x86/pitch_avx.c:xcorr_kernel_avx(). The linked full-kernel oracle is
 * authoritative; same-run disassembly records this probe's operand order. */
#include <immintrin.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

static float from_bits(uint32_t bits) {
  float value;
  memcpy(&value, &bits, sizeof(value));
  return value;
}

static uint32_t to_bits(float value) {
  uint32_t bits;
  memcpy(&bits, &value, sizeof(bits));
  return bits;
}

__attribute__((target("avx2,fma"), noinline))
static uint32_t fma_intrinsic_bits(uint32_t a, uint32_t b, uint32_t acc) {
  __m256 va = _mm256_set1_ps(from_bits(a));
  __m256 vb = _mm256_set1_ps(from_bits(b));
  __m256 vc = _mm256_set1_ps(from_bits(acc));
  __m256 out = _mm256_fmadd_ps(va, vb, vc);
  return to_bits(_mm_cvtss_f32(_mm256_castps256_ps128(out)));
}

__attribute__((target("avx2,fma"), noinline))
static uint32_t addps_bits(uint32_t a, uint32_t b) {
  __m256 va = _mm256_set1_ps(from_bits(a));
  __m256 vb = _mm256_set1_ps(from_bits(b));
  __m256 out = _mm256_add_ps(va, vb);
  return to_bits(_mm_cvtss_f32(_mm256_castps256_ps128(out)));
}

int main(void) {
  static const struct {
    const char *name;
    uint32_t a, b, acc;
  } probes[] = {
      {"qA_qB_zero", 0x7fc01234u, 0xffc05678u, 0x00000000u},
      {"qB_qA_zero", 0xffc05678u, 0x7fc01234u, 0x00000000u},
      {"sA_qB_zero", 0x7fa01234u, 0xffc05678u, 0x00000000u},
      {"qA_sB_zero", 0x7fc01234u, 0xffa05678u, 0x00000000u},
      {"sA_sB_zero", 0x7fa01234u, 0xffa05678u, 0x00000000u},
      {"qA_one_qB", 0x7fc01234u, 0x3f800000u, 0xffc05678u},
      {"sA_one_qB", 0x7fa01234u, 0x3f800000u, 0xffc05678u},
      {"qA_one_sB", 0x7fc01234u, 0x3f800000u, 0xffa05678u},
      {"one_qA_qB", 0x3f800000u, 0x7fc01234u, 0xffc05678u},
      {"one_sA_qB", 0x3f800000u, 0x7fa01234u, 0xffc05678u},
      {"inf_zero_qB", 0x7f800000u, 0x00000000u, 0xffc05678u},
      {"inf_zero_sB", 0x7f800000u, 0x00000000u, 0xffa05678u},
      {"inf_zero_zero", 0x7f800000u, 0x00000000u, 0x00000000u},
      {"inf_one_neginf", 0x7f800000u, 0x3f800000u, 0xff800000u},
      {"neginf_one_inf", 0xff800000u, 0x3f800000u, 0x7f800000u},
      {"rounding", 0x3fcca800u, 0x3f979800u, 0xa20c2545u},
  };
  size_t i;

  __builtin_cpu_init();
  if (!__builtin_cpu_supports("avx2") || !__builtin_cpu_supports("fma")) {
    fprintf(stderr, "native AVX2/FMA is required for primitive probe\n");
    return 1;
  }
  for (i = 0; i < sizeof(probes) / sizeof(probes[0]); i++) {
    const uint32_t a = probes[i].a, b = probes[i].b, acc = probes[i].acc;
    printf("%s a=%08x b=%08x acc=%08x fma_intrinsic=%08x add_ab=%08x add_ba=%08x\n",
           probes[i].name, a, b, acc, fma_intrinsic_bits(a, b, acc),
           addps_bits(a, b), addps_bits(b, a));
  }
  return 0;
}
