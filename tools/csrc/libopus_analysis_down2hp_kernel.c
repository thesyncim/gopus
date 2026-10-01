/* Direct source oracle for silk_resampler_down2_hp from src/analysis.c.
 *
 * The caller includes analysis.c through the selected reference include path,
 * so this translation unit calls the original static float kernel in the
 * same translation unit rather than a copied expression. The helper compares
 * its output, state, and high-pass energy against the Go port.
 *
 * Wire format (little-endian):
 *   IN:  "GSDI" u32(version=1) u32(count)
 *        count x { u32(input_len) f32(state[3]) f32(input[input_len]) }
 *   OUT: "GSDO" u32(version=1) u32(count) 64-byte source SHA256 hex
 *        count x { u32(input_len) u32(output_len) f32(state[3]) f32(hp_energy)
 *                   f32(output[output_len]) }
 */

#include <stdint.h>
#include <stdio.h>
#include <string.h>

#ifndef GOPUS_ANALYSIS_SOURCE_SHA256
#error "selected analysis.c source hash is required"
#endif

#include <analysis.c>

#ifdef FIXED_POINT
#error "analysis down2hp source oracle requires the default float reference"
#endif

#define INPUT_MAGIC "GSDI"
#define OUTPUT_MAGIC "GSDO"
#define MAX_INPUT_SAMPLES 960
#define MAX_CASES 32

static const char source_sha256[] = GOPUS_ANALYSIS_SOURCE_SHA256;

static int read_exact(void *dst, size_t n) {
  return fread(dst, 1, n, stdin) == n;
}

static int write_exact(const void *src, size_t n) {
  return fwrite(src, 1, n, stdout) == n;
}

static int read_u32(uint32_t *v) {
  return read_exact(v, sizeof(*v));
}

static int put_u32(uint32_t v) {
  return write_exact(&v, sizeof(v));
}

static float float_from_bits(uint32_t bits) {
  float value;
  memcpy(&value, &bits, sizeof(value));
  return value;
}

static uint32_t float_bits(float value) {
  uint32_t bits;
  memcpy(&bits, &value, sizeof(bits));
  return bits;
}

int main(void) {
  char magic[4];
  uint32_t version, count, record;

  if (sizeof(opus_val32) != sizeof(uint32_t)) return 2;
  if (sizeof(source_sha256) != 65) return 2;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) return 2;
  if (!read_u32(&version) || version != 1 || !read_u32(&count) || count == 0 || count > MAX_CASES) return 2;

  if (!write_exact(OUTPUT_MAGIC, 4) || !put_u32(1) || !put_u32(count) ||
      !write_exact(source_sha256, 64)) return 3;

  for (record = 0; record < count; record++) {
    uint32_t input_len, i;
    float state[3], input[MAX_INPUT_SAMPLES], output[MAX_INPUT_SAMPLES / 2];
    opus_val32 hp_energy;
    uint32_t output_len;

    if (!read_u32(&input_len) || input_len > MAX_INPUT_SAMPLES) return 2;
    output_len = input_len / 2;
    for (i = 0; i < 3; i++) {
      uint32_t bits;
      if (!read_u32(&bits)) return 2;
      state[i] = float_from_bits(bits);
    }
    for (i = 0; i < input_len; i++) {
      uint32_t bits;
      if (!read_u32(&bits)) return 2;
      input[i] = float_from_bits(bits);
    }

    hp_energy = silk_resampler_down2_hp(state, output, input, (int)input_len);
    if (!put_u32(input_len) || !put_u32(output_len)) return 3;
    for (i = 0; i < 3; i++) {
      if (!put_u32(float_bits(state[i]))) return 3;
    }
    if (!put_u32(float_bits((float)hp_energy))) return 3;
    for (i = 0; i < output_len; i++) {
      if (!put_u32(float_bits(output[i]))) return 3;
    }
  }

  if (fgetc(stdin) != EOF || ferror(stdin)) return 2;
  return fflush(stdout) == 0 ? 0 : 3;
}
