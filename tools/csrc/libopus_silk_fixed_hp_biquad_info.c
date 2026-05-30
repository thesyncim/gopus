/* Oracle for SILK's adaptive high-pass biquad path under FIXED_POINT libopus.
 *
 * Covers three kernels:
 *   mode 0: silk_biquad_alt_stride1 (silk/biquad_alt.c) - exported symbol.
 *   mode 1: silk_biquad_alt_stride2_c body, reproduced verbatim below because
 *           the stride2 entry point is dispatched through an arch macro and is
 *           not a stable exported symbol.
 *   mode 2: silk_HP_variable_cutoff (silk/HP_variable_cutoff.c) cutoff
 *           adaptation, reproduced verbatim through the exported silk_lin2log
 *           plus the fixed-point macros so the smoother state update is
 *           bit-exact.
 *
 * Must be compiled and linked against a libopus configured with
 * --enable-fixed-point (defines FIXED_POINT). Reads a little-endian payload of
 * cases from stdin and writes results to stdout. */

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"

#ifndef FIXED_POINT
#error "this oracle requires a FIXED_POINT libopus build (--enable-fixed-point)"
#endif

#include "SigProc_FIX.h"
#include "tuning_parameters.h"

#define INPUT_MAGIC "HPBI"
#define OUTPUT_MAGIC "HPBO"

#define MODE_STRIDE1 0u
#define MODE_STRIDE2 1u
#define MODE_HPVAR 2u

#define MAX_LEN 4096

#if defined(ENABLE_ASSERTIONS) || defined(ENABLE_HARDENING)
void celt_fatal(const char *str, const char *file, int line) {
  (void)str;
  (void)file;
  (void)line;
  abort();
}
#endif

/* Verbatim copy of silk_biquad_alt_stride2_c from silk/biquad_alt.c. */
static void biquad_alt_stride2(const opus_int16 *in, const opus_int32 *B_Q28,
                               const opus_int32 *A_Q28, opus_int32 *S,
                               opus_int16 *out, const opus_int32 len) {
  opus_int k;
  opus_int32 A0_U_Q28, A0_L_Q28, A1_U_Q28, A1_L_Q28, out32_Q14[2];

  A0_L_Q28 = (-A_Q28[0]) & 0x00003FFF;
  A0_U_Q28 = silk_RSHIFT(-A_Q28[0], 14);
  A1_L_Q28 = (-A_Q28[1]) & 0x00003FFF;
  A1_U_Q28 = silk_RSHIFT(-A_Q28[1], 14);

  for (k = 0; k < len; k++) {
    out32_Q14[0] = silk_LSHIFT(silk_SMLAWB(S[0], B_Q28[0], in[2 * k + 0]), 2);
    out32_Q14[1] = silk_LSHIFT(silk_SMLAWB(S[2], B_Q28[0], in[2 * k + 1]), 2);

    S[0] = S[1] + silk_RSHIFT_ROUND(silk_SMULWB(out32_Q14[0], A0_L_Q28), 14);
    S[2] = S[3] + silk_RSHIFT_ROUND(silk_SMULWB(out32_Q14[1], A0_L_Q28), 14);
    S[0] = silk_SMLAWB(S[0], out32_Q14[0], A0_U_Q28);
    S[2] = silk_SMLAWB(S[2], out32_Q14[1], A0_U_Q28);
    S[0] = silk_SMLAWB(S[0], B_Q28[1], in[2 * k + 0]);
    S[2] = silk_SMLAWB(S[2], B_Q28[1], in[2 * k + 1]);

    S[1] = silk_RSHIFT_ROUND(silk_SMULWB(out32_Q14[0], A1_L_Q28), 14);
    S[3] = silk_RSHIFT_ROUND(silk_SMULWB(out32_Q14[1], A1_L_Q28), 14);
    S[1] = silk_SMLAWB(S[1], out32_Q14[0], A1_U_Q28);
    S[3] = silk_SMLAWB(S[3], out32_Q14[1], A1_U_Q28);
    S[1] = silk_SMLAWB(S[1], B_Q28[2], in[2 * k + 0]);
    S[3] = silk_SMLAWB(S[3], B_Q28[2], in[2 * k + 1]);

    out[2 * k + 0] = (opus_int16)silk_SAT16(silk_RSHIFT(out32_Q14[0] + (1 << 14) - 1, 14));
    out[2 * k + 1] = (opus_int16)silk_SAT16(silk_RSHIFT(out32_Q14[1] + (1 << 14) - 1, 14));
  }
}

/* Verbatim copy of the per-frame body of silk_HP_variable_cutoff
 * (silk/HP_variable_cutoff.c), operating on a single encoder's relevant
 * fields. Returns the updated variable_HP_smth1_Q15. The TYPE_VOICED guard is
 * applied by the caller (the test only probes voiced cases). */
static opus_int32 hp_variable_cutoff(opus_int fs_kHz, opus_int32 prevLag,
                                     opus_int quality_Q15,
                                     opus_int speech_activity_Q8,
                                     opus_int32 variable_HP_smth1_Q15) {
  opus_int32 pitch_freq_Hz_Q16, pitch_freq_log_Q7, delta_freq_Q7;

  pitch_freq_Hz_Q16 =
      silk_DIV32_16(silk_LSHIFT(silk_MUL(fs_kHz, 1000), 16), prevLag);
  pitch_freq_log_Q7 = silk_lin2log(pitch_freq_Hz_Q16) - (16 << 7);

  pitch_freq_log_Q7 = silk_SMLAWB(
      pitch_freq_log_Q7,
      silk_SMULWB(silk_LSHIFT(-quality_Q15, 2), quality_Q15),
      pitch_freq_log_Q7 -
          (silk_lin2log(SILK_FIX_CONST(VARIABLE_HP_MIN_CUTOFF_HZ, 16)) -
           (16 << 7)));

  delta_freq_Q7 = pitch_freq_log_Q7 - silk_RSHIFT(variable_HP_smth1_Q15, 8);
  if (delta_freq_Q7 < 0) {
    delta_freq_Q7 = silk_MUL(delta_freq_Q7, 3);
  }

  delta_freq_Q7 =
      silk_LIMIT_32(delta_freq_Q7, -SILK_FIX_CONST(VARIABLE_HP_MAX_DELTA_FREQ, 7),
                    SILK_FIX_CONST(VARIABLE_HP_MAX_DELTA_FREQ, 7));

  variable_HP_smth1_Q15 = silk_SMLAWB(
      variable_HP_smth1_Q15, silk_SMULBB(speech_activity_Q8, delta_freq_Q7),
      SILK_FIX_CONST(VARIABLE_HP_SMTH_COEF1, 16));

  variable_HP_smth1_Q15 =
      silk_LIMIT_32(variable_HP_smth1_Q15,
                    silk_LSHIFT(silk_lin2log(VARIABLE_HP_MIN_CUTOFF_HZ), 8),
                    silk_LSHIFT(silk_lin2log(VARIABLE_HP_MAX_CUTOFF_HZ), 8));

  return variable_HP_smth1_Q15;
}

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
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) | ((uint32_t)b[2] << 16) |
         ((uint32_t)b[3] << 24);
  return 1;
}

static int read_i32(int32_t *out) {
  uint32_t v;
  if (!read_u32(&v)) return 0;
  *out = (int32_t)v;
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

static int write_i32(int32_t value) { return write_u32((uint32_t)value); }

/* Biquad case: reads mode, len (sample pairs for stride2, samples for
 * stride1), B_Q28[3], A_Q28[2], initial state, input samples; writes the
 * updated state followed by the output samples. */
static int handle_biquad(uint32_t mode) {
  int32_t len, i;
  opus_int32 B_Q28[3], A_Q28[2];
  opus_int32 S[4];
  opus_int16 in[2 * MAX_LEN];
  opus_int16 out[2 * MAX_LEN];
  int32_t nstate = (mode == MODE_STRIDE2) ? 4 : 2;
  int32_t nsamp;

  if (!read_i32(&len)) return 0;
  if (len < 0 || len > MAX_LEN) return 0;
  nsamp = (mode == MODE_STRIDE2) ? (2 * len) : len;

  for (i = 0; i < 3; i++) {
    if (!read_i32(&B_Q28[i])) return 0;
  }
  for (i = 0; i < 2; i++) {
    if (!read_i32(&A_Q28[i])) return 0;
  }
  for (i = 0; i < nstate; i++) {
    if (!read_i32(&S[i])) return 0;
  }
  for (i = 0; i < nsamp; i++) {
    int32_t v;
    if (!read_i32(&v)) return 0;
    in[i] = (opus_int16)v;
  }

  if (mode == MODE_STRIDE2) {
    biquad_alt_stride2(in, B_Q28, A_Q28, S, out, len);
  } else {
    silk_biquad_alt_stride1(in, B_Q28, A_Q28, S, out, len);
  }

  for (i = 0; i < nstate; i++) {
    if (!write_i32(S[i])) return 0;
  }
  for (i = 0; i < nsamp; i++) {
    if (!write_i32((int32_t)out[i])) return 0;
  }
  return 1;
}

/* HP-variable-cutoff case: reads fs_kHz, prevLag, quality_Q15,
 * speech_activity_Q8, variable_HP_smth1_Q15; writes the updated
 * variable_HP_smth1_Q15. */
static int handle_hpvar(void) {
  int32_t fs_kHz, prevLag, quality_Q15, speech_activity_Q8, smth1;
  if (!read_i32(&fs_kHz) || !read_i32(&prevLag) || !read_i32(&quality_Q15) ||
      !read_i32(&speech_activity_Q8) || !read_i32(&smth1)) {
    return 0;
  }
  smth1 = hp_variable_cutoff(fs_kHz, prevLag, quality_Q15, speech_activity_Q8,
                             smth1);
  return write_i32(smth1);
}

int main(void) {
  if (!set_binary_stdio()) return 1;

  char magic[4];
  if (!read_exact(magic, sizeof(magic)) ||
      memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) {
    return 1;
  }
  uint32_t version;
  if (!read_u32(&version) || version != 1) return 1;
  uint32_t count;
  if (!read_u32(&count)) return 1;

  if (!write_exact(OUTPUT_MAGIC, 4)) return 1;
  if (!write_u32(1)) return 1; /* version */
  if (!write_u32(count)) return 1;

  for (uint32_t c = 0; c < count; c++) {
    uint32_t mode;
    if (!read_u32(&mode)) return 1;
    if (mode == MODE_STRIDE1 || mode == MODE_STRIDE2) {
      if (!handle_biquad(mode)) return 1;
    } else if (mode == MODE_HPVAR) {
      if (!handle_hpvar()) return 1;
    } else {
      return 1;
    }
  }

  return 0;
}
