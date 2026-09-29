/* libopus_cbr_encode_packets.c — CBR encode oracle for gopus parity testing.
 *
 * Reads float32 LE PCM frames from stdin and encodes them with libopus
 * configured for CBR (OPUS_SET_VBR(0)), emitting each packet in the standard
 * gopus oracle wire format.
 *
 * Wire format (all little-endian):
 *   IN:  "GCBR" u32(version=1) u32(application) u32(bandwidth) u32(channels)
 *              u32(bitrate) u32(frame_size) u32(complexity) u32(num_frames)
 *              [num_frames × frame_size × channels × float32]
 *   OUT: "GCBO" u32(version=2) u32(version_string_len) u8(version_string…)
 *              u32(OPUS_ARCHMASK) u32(build_feature_bits) u32(opus_select_arch())
 *              u32(num_frames)
 *              [num_frames × u32(packet_len) u32(final_range) u8(packet…)]
 *
 * application values:
 *   0 = OPUS_APPLICATION_AUDIO
 *   1 = OPUS_APPLICATION_VOIP
 *   2 = OPUS_APPLICATION_RESTRICTED_SILK
 *   3 = OPUS_APPLICATION_RESTRICTED_CELT
 *
 * bandwidth values (match opus_defines.h):
 *   1101 = OPUS_BANDWIDTH_NARROWBAND
 *   1102 = OPUS_BANDWIDTH_MEDIUMBAND
 *   1103 = OPUS_BANDWIDTH_WIDEBAND
 *   1104 = OPUS_BANDWIDTH_SUPERWIDEBAND
 *   1105 = OPUS_BANDWIDTH_FULLBAND
 *
 * Reference: libopus src/opus_encoder.c opus_encode_float()
 *            src/opus_demo.c -cbr flag: opus_encoder_ctl(enc, OPUS_SET_VBR(0))
 */

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "opus.h"
#include "config.h"
#include "celt/cpu_support.h"
#ifdef GOPUS_CELT_TRACE
#include "celt/bands.h"
#include "celt/quant_bands.h"
#endif

#define INPUT_MAGIC  "GCBR"
#define OUTPUT_MAGIC "GCBO"
#define MAX_PACKET_BYTES 4000

/* These bits describe the generated config.h seen by this helper translation
 * unit. The selected runtime architecture is reported separately below. */
#define GCBO_FEATURE_RTCD                    (1u << 0)
#define GCBO_FEATURE_X86_MAY_SSE             (1u << 1)
#define GCBO_FEATURE_X86_MAY_SSE2            (1u << 2)
#define GCBO_FEATURE_X86_MAY_SSE4_1          (1u << 3)
#define GCBO_FEATURE_X86_MAY_AVX2            (1u << 4)
#define GCBO_FEATURE_X86_PRESUME_SSE         (1u << 5)
#define GCBO_FEATURE_X86_PRESUME_SSE2        (1u << 6)
#define GCBO_FEATURE_X86_PRESUME_SSE4_1      (1u << 7)
#define GCBO_FEATURE_X86_PRESUME_AVX2        (1u << 8)
#define GCBO_FEATURE_ARM_MAY_NEON             (1u << 9)
#define GCBO_FEATURE_ARM_PRESUME_NEON         (1u << 10)
#define GCBO_FEATURE_ARM_MAY_NEON_INTR        (1u << 11)
#define GCBO_FEATURE_ARM_PRESUME_NEON_INTR    (1u << 12)
#define GCBO_FEATURE_ARM_MAY_DOTPROD          (1u << 13)
#define GCBO_FEATURE_ARM_PRESUME_DOTPROD      (1u << 14)

static uint32_t build_feature_bits(void) {
  uint32_t bits = 0;
#ifdef OPUS_HAVE_RTCD
  bits |= GCBO_FEATURE_RTCD;
#endif
#ifdef OPUS_X86_MAY_HAVE_SSE
  bits |= GCBO_FEATURE_X86_MAY_SSE;
#endif
#ifdef OPUS_X86_PRESUME_SSE
  bits |= GCBO_FEATURE_X86_PRESUME_SSE;
#endif
#ifdef OPUS_X86_MAY_HAVE_SSE2
  bits |= GCBO_FEATURE_X86_MAY_SSE2;
#endif
#ifdef OPUS_X86_PRESUME_SSE2
  bits |= GCBO_FEATURE_X86_PRESUME_SSE2;
#endif
#ifdef OPUS_X86_MAY_HAVE_SSE4_1
  bits |= GCBO_FEATURE_X86_MAY_SSE4_1;
#endif
#ifdef OPUS_X86_PRESUME_SSE4_1
  bits |= GCBO_FEATURE_X86_PRESUME_SSE4_1;
#endif
#ifdef OPUS_X86_MAY_HAVE_AVX2
  bits |= GCBO_FEATURE_X86_MAY_AVX2;
#endif
#ifdef OPUS_X86_PRESUME_AVX2
  bits |= GCBO_FEATURE_X86_PRESUME_AVX2;
#endif
#ifdef OPUS_ARM_MAY_HAVE_NEON
  bits |= GCBO_FEATURE_ARM_MAY_NEON;
#endif
#ifdef OPUS_ARM_PRESUME_NEON
  bits |= GCBO_FEATURE_ARM_PRESUME_NEON;
#endif
#ifdef OPUS_ARM_MAY_HAVE_NEON_INTR
  bits |= GCBO_FEATURE_ARM_MAY_NEON_INTR;
#endif
#ifdef OPUS_ARM_PRESUME_NEON_INTR
  bits |= GCBO_FEATURE_ARM_PRESUME_NEON_INTR;
#endif
#ifdef OPUS_ARM_MAY_HAVE_DOTPROD
  bits |= GCBO_FEATURE_ARM_MAY_DOTPROD;
#endif
#ifdef OPUS_ARM_PRESUME_DOTPROD
  bits |= GCBO_FEATURE_ARM_PRESUME_DOTPROD;
#endif
  return bits;
}

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin),  _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

static int read_exact(void *dst, size_t n) {
  size_t got = fread(dst, 1, n, stdin);
  return got == n;
}

static int write_exact(const void *src, size_t n) {
  size_t off = 0;
  const unsigned char *p = (const unsigned char *)src;
  while (off < n) {
    size_t w = fwrite(p + off, 1, n - off, stdout);
    if (w == 0) return 0;
    off += w;
  }
  return 1;
}

static int read_u32(uint32_t *v) {
  unsigned char b[4];
  if (!read_exact(b, 4)) return 0;
  *v = (uint32_t)b[0] | ((uint32_t)b[1] << 8) |
       ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int write_u32(uint32_t v) {
  unsigned char b[4];
  b[0] = (unsigned char)(v & 0xff);
  b[1] = (unsigned char)((v >> 8) & 0xff);
  b[2] = (unsigned char)((v >> 16) & 0xff);
  b[3] = (unsigned char)((v >> 24) & 0xff);
  return write_exact(b, 4);
}

#ifdef GOPUS_CELT_TRACE
#define CELT_TRACE_MAX_CALLS 8
#define CELT_TRACE_MAX_FLOATS 4096
#define CELT_TRACE_MAX_BANDS 64

typedef struct {
  uint32_t frame_coeffs, bands, channels, lm;
  float spectrum[CELT_TRACE_MAX_FLOATS];
  float amplitudes[CELT_TRACE_MAX_BANDS * 2];
} celt_trace_band_call;

typedef struct {
  uint32_t bands, channels;
  float amplitudes[CELT_TRACE_MAX_BANDS * 2];
  float log_energy[CELT_TRACE_MAX_BANDS * 2];
} celt_trace_log_call;

typedef struct {
  uint32_t active_coeffs, bands, channels;
  float band_energy[CELT_TRACE_MAX_BANDS * 2];
  float normalized[CELT_TRACE_MAX_FLOATS];
} celt_trace_normalization_call;

typedef struct {
  uint32_t bands, channels, budget_bytes;
  float input[CELT_TRACE_MAX_BANDS * 2];
  float quantized[CELT_TRACE_MAX_BANDS * 2];
  float error[CELT_TRACE_MAX_BANDS * 2];
} celt_trace_coarse_call;

typedef struct {
  uint32_t active_coeffs, bands, channels;
  float band_energy[CELT_TRACE_MAX_BANDS * 2];
  float input[CELT_TRACE_MAX_FLOATS];
  float output[CELT_TRACE_MAX_FLOATS];
} celt_trace_quant_call;

static struct {
  uint32_t band_calls, log_calls, normalization_calls, coarse_calls, quant_calls;
  uint32_t stored_band_calls, stored_log_calls, stored_normalization_calls;
  uint32_t stored_coarse_calls, stored_quant_calls, overflow;
  celt_trace_band_call bands[CELT_TRACE_MAX_CALLS];
  celt_trace_log_call logs[CELT_TRACE_MAX_CALLS];
  celt_trace_normalization_call normalizations[CELT_TRACE_MAX_CALLS];
  celt_trace_coarse_call coarse[CELT_TRACE_MAX_CALLS];
  celt_trace_quant_call quant[CELT_TRACE_MAX_CALLS];
} celt_encode_trace;

static int trace_dimensions(int values, int limit) {
  if (values < 0 || values > limit) {
    celt_encode_trace.overflow = 1;
    return 0;
  }
  return 1;
}

static void trace_copy_bands(float *dst, const float *src, int bands, int channels, int stride) {
  int c;
  if (!trace_dimensions(bands, CELT_TRACE_MAX_BANDS) ||
      !trace_dimensions(channels, 2) || stride < bands) return;
  for (c = 0; c < channels; c++)
    memcpy(dst + c * bands, src + c * stride, (size_t)bands * sizeof(float));
}

static int trace_write_float32(const float *values, uint32_t count) {
  uint32_t i;
  for (i = 0; i < count; i++) {
    uint32_t bits;
    memcpy(&bits, &values[i], sizeof(bits));
    if (!write_u32(bits)) return 0;
  }
  return 1;
}

extern void __real_compute_band_energies(const CELTMode *m, const celt_sig *X,
    celt_ener *bandE, int end, int C, int LM, int arch);
void __wrap_compute_band_energies(const CELTMode *m, const celt_sig *X,
    celt_ener *bandE, int end, int C, int LM, int arch) {
  uint32_t call = celt_encode_trace.band_calls++;
  __real_compute_band_energies(m, X, bandE, end, C, LM, arch);
  if (call >= CELT_TRACE_MAX_CALLS) {
    celt_encode_trace.overflow = 1;
    return;
  }
  celt_trace_band_call *trace = &celt_encode_trace.bands[call];
  int frame_coeffs = m->shortMdctSize << LM;
  int spectrum_count = frame_coeffs * C;
  int amplitude_count = end * C;
  if (!trace_dimensions(spectrum_count, CELT_TRACE_MAX_FLOATS) ||
      !trace_dimensions(amplitude_count, CELT_TRACE_MAX_BANDS * 2)) return;
  trace->frame_coeffs = (uint32_t)frame_coeffs;
  trace->bands = (uint32_t)end;
  trace->channels = (uint32_t)C;
  trace->lm = (uint32_t)LM;
  memcpy(trace->spectrum, X, (size_t)spectrum_count * sizeof(float));
  trace_copy_bands(trace->amplitudes, (const float *)bandE, end, C, m->nbEBands);
  celt_encode_trace.stored_band_calls++;
}

extern void __real_amp2Log2(const CELTMode *m, int effEnd, int end,
    celt_ener *bandE, celt_glog *bandLogE, int C);
void __wrap_amp2Log2(const CELTMode *m, int effEnd, int end,
    celt_ener *bandE, celt_glog *bandLogE, int C) {
  uint32_t call = celt_encode_trace.log_calls++;
  __real_amp2Log2(m, effEnd, end, bandE, bandLogE, C);
  if (call >= CELT_TRACE_MAX_CALLS) {
    celt_encode_trace.overflow = 1;
    return;
  }
  celt_trace_log_call *trace = &celt_encode_trace.logs[call];
  if (!trace_dimensions(end * C, CELT_TRACE_MAX_BANDS * 2)) return;
  trace->bands = (uint32_t)end;
  trace->channels = (uint32_t)C;
  trace_copy_bands(trace->amplitudes, (const float *)bandE, end, C, m->nbEBands);
  trace_copy_bands(trace->log_energy, (const float *)bandLogE, end, C, m->nbEBands);
  celt_encode_trace.stored_log_calls++;
}

extern void __real_normalise_bands(const CELTMode *m, const celt_sig *freq,
    celt_norm *X, const celt_ener *bandE, int end, int C, int M);
void __wrap_normalise_bands(const CELTMode *m, const celt_sig *freq,
    celt_norm *X, const celt_ener *bandE, int end, int C, int M) {
  uint32_t call = celt_encode_trace.normalization_calls++;
  __real_normalise_bands(m, freq, X, bandE, end, C, M);
  if (call >= CELT_TRACE_MAX_CALLS) {
    celt_encode_trace.overflow = 1;
    return;
  }
  celt_trace_normalization_call *trace = &celt_encode_trace.normalizations[call];
  int active = M * m->eBands[end];
  int frame_coeffs = M * m->shortMdctSize;
  if (!trace_dimensions(active * C, CELT_TRACE_MAX_FLOATS) ||
      !trace_dimensions(end * C, CELT_TRACE_MAX_BANDS * 2)) return;
  trace->active_coeffs = (uint32_t)active;
  trace->bands = (uint32_t)end;
  trace->channels = (uint32_t)C;
  trace_copy_bands(trace->band_energy, bandE, end, C, m->nbEBands);
  for (int c = 0; c < C; c++)
    memcpy(trace->normalized + c * active, X + c * frame_coeffs, (size_t)active * sizeof(float));
  celt_encode_trace.stored_normalization_calls++;
}

extern void __real_quant_coarse_energy(const CELTMode *m, int start, int end,
    int effEnd, const celt_glog *eBands, celt_glog *oldEBands,
    opus_uint32 budget, celt_glog *error, ec_enc *enc, int C, int LM,
    int nbAvailableBytes, int force_intra, opus_val32 *delayedIntra,
    int two_pass, int loss_rate, int lfe);
void __wrap_quant_coarse_energy(const CELTMode *m, int start, int end,
    int effEnd, const celt_glog *eBands, celt_glog *oldEBands,
    opus_uint32 budget, celt_glog *error, ec_enc *enc, int C, int LM,
    int nbAvailableBytes, int force_intra, opus_val32 *delayedIntra,
    int two_pass, int loss_rate, int lfe) {
  uint32_t call = celt_encode_trace.coarse_calls++;
  if (call < CELT_TRACE_MAX_CALLS && trace_dimensions((end - start) * C, CELT_TRACE_MAX_BANDS * 2)) {
    celt_trace_coarse_call *trace = &celt_encode_trace.coarse[call];
    trace->bands = (uint32_t)(end - start);
    trace->channels = (uint32_t)C;
    trace->budget_bytes = (uint32_t)nbAvailableBytes;
    trace_copy_bands(trace->input, eBands + start, end - start, C, m->nbEBands);
    celt_encode_trace.stored_coarse_calls++;
  } else if (call >= CELT_TRACE_MAX_CALLS) {
    celt_encode_trace.overflow = 1;
  }
  __real_quant_coarse_energy(m, start, end, effEnd, eBands, oldEBands, budget,
      error, enc, C, LM, nbAvailableBytes, force_intra, delayedIntra,
      two_pass, loss_rate, lfe);
  if (call < CELT_TRACE_MAX_CALLS && call < celt_encode_trace.stored_coarse_calls) {
    celt_trace_coarse_call *trace = &celt_encode_trace.coarse[call];
    trace_copy_bands(trace->quantized, oldEBands + start, end - start, C, m->nbEBands);
    trace_copy_bands(trace->error, error + start, end - start, C, m->nbEBands);
  }
}

extern void __real_quant_all_bands(int encode, const CELTMode *m, int start, int end,
    celt_norm *X, celt_norm *Y, unsigned char *collapse_masks,
    const celt_ener *bandE, int *pulses, int shortBlocks, int spread,
    int dual_stereo, int intensity, int *tf_res, opus_int32 total_bits,
    opus_int32 balance, ec_ctx *ec, int M, int codedBands, opus_uint32 *seed,
    int complexity, int arch, int disable_inv
    ARG_QEXT(ec_ctx *ext_ec) ARG_QEXT(int *extra_pulses)
    ARG_QEXT(opus_int32 total_ext_bits) ARG_QEXT(const int *cap));
void __wrap_quant_all_bands(int encode, const CELTMode *m, int start, int end,
    celt_norm *X, celt_norm *Y, unsigned char *collapse_masks,
    const celt_ener *bandE, int *pulses, int shortBlocks, int spread,
    int dual_stereo, int intensity, int *tf_res, opus_int32 total_bits,
    opus_int32 balance, ec_ctx *ec, int M, int codedBands, opus_uint32 *seed,
    int complexity, int arch, int disable_inv
    ARG_QEXT(ec_ctx *ext_ec) ARG_QEXT(int *extra_pulses)
    ARG_QEXT(opus_int32 total_ext_bits) ARG_QEXT(const int *cap)) {
  uint32_t call = celt_encode_trace.quant_calls++;
  int active = M * m->eBands[end];
  int channels = Y != NULL ? 2 : 1;
  if (call < CELT_TRACE_MAX_CALLS && trace_dimensions(active * channels, CELT_TRACE_MAX_FLOATS) &&
      trace_dimensions((end - start) * channels, CELT_TRACE_MAX_BANDS * 2)) {
    celt_trace_quant_call *trace = &celt_encode_trace.quant[call];
    trace->active_coeffs = (uint32_t)active;
    trace->bands = (uint32_t)(end - start);
    trace->channels = Y != NULL ? 2u : 1u;
    trace_copy_bands(trace->band_energy, bandE + start, end - start, (int)trace->channels, m->nbEBands);
    memcpy(trace->input, X, (size_t)active * sizeof(float));
    if (Y != NULL) memcpy(trace->input + active, Y, (size_t)active * sizeof(float));
    celt_encode_trace.stored_quant_calls++;
  } else if (call >= CELT_TRACE_MAX_CALLS) {
    celt_encode_trace.overflow = 1;
  }
  __real_quant_all_bands(encode, m, start, end, X, Y, collapse_masks, bandE,
      pulses, shortBlocks, spread, dual_stereo, intensity, tf_res, total_bits,
      balance, ec, M, codedBands, seed, complexity, arch, disable_inv
      ARG_QEXT(ext_ec) ARG_QEXT(extra_pulses) ARG_QEXT(total_ext_bits) ARG_QEXT(cap));
  if (call < CELT_TRACE_MAX_CALLS && call < celt_encode_trace.stored_quant_calls) {
    celt_trace_quant_call *trace = &celt_encode_trace.quant[call];
    memcpy(trace->output, X, (size_t)active * sizeof(float));
    if (Y != NULL) memcpy(trace->output + active, Y, (size_t)active * sizeof(float));
  }
}

static int write_celt_encode_trace(void) {
  if (!write_exact("GCET", 4) || !write_u32(1) || !write_u32(celt_encode_trace.overflow)) return 0;
  if (!write_u32(celt_encode_trace.band_calls) || !write_u32(celt_encode_trace.stored_band_calls)) return 0;
  for (uint32_t i = 0; i < celt_encode_trace.stored_band_calls; i++) {
    const celt_trace_band_call *trace = &celt_encode_trace.bands[i];
    uint32_t spectrum_count = trace->frame_coeffs * trace->channels;
    uint32_t band_count = trace->bands * trace->channels;
    if (!write_u32(trace->frame_coeffs) || !write_u32(trace->bands) ||
        !write_u32(trace->channels) || !write_u32(trace->lm) ||
        !trace_write_float32(trace->spectrum, spectrum_count) ||
        !trace_write_float32(trace->amplitudes, band_count)) return 0;
  }
  if (!write_u32(celt_encode_trace.log_calls) || !write_u32(celt_encode_trace.stored_log_calls)) return 0;
  for (uint32_t i = 0; i < celt_encode_trace.stored_log_calls; i++) {
    const celt_trace_log_call *trace = &celt_encode_trace.logs[i];
    uint32_t count = trace->bands * trace->channels;
    if (!write_u32(trace->bands) || !write_u32(trace->channels) ||
        !trace_write_float32(trace->amplitudes, count) || !trace_write_float32(trace->log_energy, count)) return 0;
  }
  if (!write_u32(celt_encode_trace.normalization_calls) || !write_u32(celt_encode_trace.stored_normalization_calls)) return 0;
  for (uint32_t i = 0; i < celt_encode_trace.stored_normalization_calls; i++) {
    const celt_trace_normalization_call *trace = &celt_encode_trace.normalizations[i];
    uint32_t band_count = trace->bands * trace->channels;
    uint32_t coeff_count = trace->active_coeffs * trace->channels;
    if (!write_u32(trace->active_coeffs) || !write_u32(trace->bands) || !write_u32(trace->channels) ||
        !trace_write_float32(trace->band_energy, band_count) || !trace_write_float32(trace->normalized, coeff_count)) return 0;
  }
  if (!write_u32(celt_encode_trace.coarse_calls) || !write_u32(celt_encode_trace.stored_coarse_calls)) return 0;
  for (uint32_t i = 0; i < celt_encode_trace.stored_coarse_calls; i++) {
    const celt_trace_coarse_call *trace = &celt_encode_trace.coarse[i];
    uint32_t count = trace->bands * trace->channels;
    if (!write_u32(trace->bands) || !write_u32(trace->channels) || !write_u32(trace->budget_bytes) ||
        !trace_write_float32(trace->input, count) || !trace_write_float32(trace->quantized, count) ||
        !trace_write_float32(trace->error, count)) return 0;
  }
  if (!write_u32(celt_encode_trace.quant_calls) || !write_u32(celt_encode_trace.stored_quant_calls)) return 0;
  for (uint32_t i = 0; i < celt_encode_trace.stored_quant_calls; i++) {
    const celt_trace_quant_call *trace = &celt_encode_trace.quant[i];
    uint32_t band_count = trace->bands * trace->channels;
    uint32_t coeff_count = trace->active_coeffs * trace->channels;
    if (!write_u32(trace->active_coeffs) || !write_u32(trace->bands) || !write_u32(trace->channels) ||
        !trace_write_float32(trace->band_energy, band_count) || !trace_write_float32(trace->input, coeff_count) ||
        !trace_write_float32(trace->output, coeff_count)) return 0;
  }
  return 1;
}
#endif

int main(void) {
  if (!set_binary_stdio()) {
    fprintf(stderr, "set_binary_stdio failed\n");
    return 1;
  }

  /* Read and validate input header magic */
  char magic[4];
  if (!read_exact(magic, 4) || memcmp(magic, INPUT_MAGIC, 4) != 0) {
    fprintf(stderr, "bad input magic\n");
    return 1;
  }
  uint32_t version;
  if (!read_u32(&version) || version != 1) {
    fprintf(stderr, "bad input version %u\n", version);
    return 1;
  }

  uint32_t app_code, bandwidth, channels, bitrate, frame_size, complexity, num_frames;
  if (!read_u32(&app_code)   || !read_u32(&bandwidth)  || !read_u32(&channels) ||
      !read_u32(&bitrate)    || !read_u32(&frame_size)  || !read_u32(&complexity) ||
      !read_u32(&num_frames)) {
    fprintf(stderr, "truncated header\n");
    return 1;
  }

  /* Map application code to libopus constant */
  int application;
  switch (app_code) {
    case 0: application = OPUS_APPLICATION_AUDIO;             break;
    case 1: application = OPUS_APPLICATION_VOIP;              break;
    case 2: application = OPUS_APPLICATION_RESTRICTED_SILK;   break;
    case 3: application = OPUS_APPLICATION_RESTRICTED_CELT;   break;
    default:
      fprintf(stderr, "unknown application code %u\n", app_code);
      return 1;
  }

  /* Validate bandwidth is a valid Opus bandwidth constant */
  if (bandwidth != OPUS_BANDWIDTH_NARROWBAND    &&
      bandwidth != OPUS_BANDWIDTH_MEDIUMBAND    &&
      bandwidth != OPUS_BANDWIDTH_WIDEBAND      &&
      bandwidth != OPUS_BANDWIDTH_SUPERWIDEBAND &&
      bandwidth != OPUS_BANDWIDTH_FULLBAND) {
    fprintf(stderr, "unknown bandwidth %u\n", bandwidth);
    return 1;
  }

  if (channels < 1 || channels > 2) {
    fprintf(stderr, "unsupported channels %u\n", channels);
    return 1;
  }
  if (frame_size == 0 || num_frames == 0) {
    fprintf(stderr, "invalid frame_size=%u num_frames=%u\n", frame_size, num_frames);
    return 1;
  }
  if (complexity > 10) {
    fprintf(stderr, "invalid complexity %u\n", complexity);
    return 1;
  }

  /* Create encoder — always at 48 kHz, matching gopus internal rate */
  int err = OPUS_OK;
  OpusEncoder *enc = opus_encoder_create(48000, (int)channels, application, &err);
  if (enc == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_encoder_create failed: %d\n", err);
    return 1;
  }

  /* libopus CBR setup: VBR=0, CVBR=0 — mirrors opus_demo -cbr
   * Reference: src/opus_demo.c lines that handle "-cbr":
   *   opus_encoder_ctl(enc, OPUS_SET_VBR(0)) */
  if (opus_encoder_ctl(enc, OPUS_SET_VBR(0)) != OPUS_OK) {
    fprintf(stderr, "OPUS_SET_VBR(0) failed\n");
    opus_encoder_destroy(enc);
    return 1;
  }
  if (opus_encoder_ctl(enc, OPUS_SET_BITRATE((opus_int32)bitrate)) != OPUS_OK) {
    fprintf(stderr, "OPUS_SET_BITRATE failed\n");
    opus_encoder_destroy(enc);
    return 1;
  }
  if (opus_encoder_ctl(enc, OPUS_SET_BANDWIDTH((opus_int32)bandwidth)) != OPUS_OK) {
    fprintf(stderr, "OPUS_SET_BANDWIDTH failed\n");
    opus_encoder_destroy(enc);
    return 1;
  }
  if (opus_encoder_ctl(enc, OPUS_SET_COMPLEXITY((opus_int32)complexity)) != OPUS_OK) {
    fprintf(stderr, "OPUS_SET_COMPLEXITY failed\n");
    opus_encoder_destroy(enc);
    return 1;
  }

  /* Allocate PCM buffer and packet buffer */
  size_t samples_per_frame = (size_t)frame_size * (size_t)channels;
  float *pcm = (float *)malloc(samples_per_frame * sizeof(float));
  if (pcm == NULL) {
    fprintf(stderr, "pcm malloc failed\n");
    opus_encoder_destroy(enc);
    return 1;
  }
  unsigned char *pkt_buf = (unsigned char *)malloc(MAX_PACKET_BYTES);
  if (pkt_buf == NULL) {
    fprintf(stderr, "packet buf malloc failed\n");
    free(pcm);
    opus_encoder_destroy(enc);
    return 1;
  }

  /* Collect each frame before writing so its packet and final range stay
   * associated even if libopus ever emits a zero-byte DTX frame. */
  unsigned char **packets = (unsigned char **)malloc(num_frames * sizeof(unsigned char *));
  int *packet_lens = (int *)malloc(num_frames * sizeof(int));
  uint32_t *final_ranges = (uint32_t *)malloc(num_frames * sizeof(uint32_t));
  if (packets == NULL || packet_lens == NULL || final_ranges == NULL) {
    fprintf(stderr, "packet array malloc failed\n");
    free(final_ranges);
    free(packet_lens);
    free(packets);
    free(pkt_buf);
    free(pcm);
    opus_encoder_destroy(enc);
    return 1;
  }
  uint32_t actual_frames = 0;

  for (uint32_t f = 0; f < num_frames; f++) {
    /* Read PCM: float32 LE samples interleaved by channel */
    for (size_t s = 0; s < samples_per_frame; s++) {
      unsigned char raw[4];
      uint32_t bits;
      if (!read_exact(raw, 4)) {
        fprintf(stderr, "truncated PCM at frame %u sample %zu\n", f, s);
        goto cleanup_fail;
      }
      bits = (uint32_t)raw[0] | ((uint32_t)raw[1] << 8) |
             ((uint32_t)raw[2] << 16) | ((uint32_t)raw[3] << 24);
      memcpy(&pcm[s], &bits, sizeof(float));
    }

    int n = opus_encode_float(enc, pcm, (int)frame_size, pkt_buf, MAX_PACKET_BYTES);
    if (n < 0) {
      fprintf(stderr, "opus_encode_float frame %u failed: %d\n", f, n);
      goto cleanup_fail;
    }
    opus_uint32 final_range = 0;
    if (opus_encoder_ctl(enc, OPUS_GET_FINAL_RANGE(&final_range)) != OPUS_OK) {
      fprintf(stderr, "OPUS_GET_FINAL_RANGE frame %u failed\n", f);
      goto cleanup_fail;
    }
    packets[actual_frames] = NULL;
    if (n > 0) {
      packets[actual_frames] = (unsigned char *)malloc((size_t)n);
      if (packets[actual_frames] == NULL) {
        fprintf(stderr, "packet copy malloc failed at frame %u\n", f);
        goto cleanup_fail;
      }
      memcpy(packets[actual_frames], pkt_buf, (size_t)n);
    }
    packet_lens[actual_frames] = n;
    final_ranges[actual_frames] = (uint32_t)final_range;
    actual_frames++;
  }

  /* Report the version string, generated config macros, and runtime dispatch
   * selected by the same archive used for encoding. */
  const char *version_string = opus_get_version_string();
  uint32_t version_len = (uint32_t)strlen(version_string);
  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(2) ||
      !write_u32(version_len) || !write_exact(version_string, version_len) ||
      !write_u32((uint32_t)OPUS_ARCHMASK) || !write_u32(build_feature_bits()) ||
      !write_u32((uint32_t)opus_select_arch()) || !write_u32(actual_frames)) {
    fprintf(stderr, "write output header failed\n");
    goto cleanup_fail;
  }
  /* Write each packet and its range-coder state */
  for (uint32_t i = 0; i < actual_frames; i++) {
    if (!write_u32((uint32_t)packet_lens[i]) || !write_u32(final_ranges[i]) ||
        !write_exact(packets[i], (size_t)packet_lens[i])) {
      fprintf(stderr, "write frame %u failed\n", i);
      goto cleanup_fail;
    }
  }
#ifdef GOPUS_CELT_TRACE
  if (!write_celt_encode_trace()) {
    fprintf(stderr, "write CELT stage trace failed\n");
    goto cleanup_fail;
  }
#endif

  for (uint32_t i = 0; i < actual_frames; i++) free(packets[i]);
  free(packets);
  free(packet_lens);
  free(final_ranges);
  free(pkt_buf);
  free(pcm);
  opus_encoder_destroy(enc);
  return 0;

cleanup_fail:
  for (uint32_t i = 0; i < actual_frames; i++) free(packets[i]);
  free(packets);
  free(packet_lens);
  free(final_ranges);
  free(pkt_buf);
  free(pcm);
  opus_encoder_destroy(enc);
  return 1;
}
