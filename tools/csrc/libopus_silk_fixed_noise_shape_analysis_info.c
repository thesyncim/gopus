/* Oracle for the libopus FIXED_POINT silk_noise_shape_analysis_FIX driver.
 *
 * Must be compiled and linked against a libopus configured with
 * --enable-fixed-point (defines FIXED_POINT). It constructs a minimal
 * silk_encoder_state_FIX / silk_encoder_control_FIX, runs the real driver, and
 * writes the bit-exact shaping outputs (Gains_Q16, AR_Q13, Tilt_Q14,
 * HarmShapeGain_Q14, LF_shp_Q14, input/coding quality, quantOffsetType, and the
 * updated smoothing accumulators) to stdout. */

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

#include "main_FIX.h"

#define INPUT_MAGIC "GNSI"
#define OUTPUT_MAGIC "GNSO"

#define MAX_X 4096
#define MAX_RES 4096

void silk_noise_shape_analysis_FIX(silk_encoder_state_FIX *psEnc,
                                   silk_encoder_control_FIX *psEncCtrl,
                                   const opus_int16 *pitch_res,
                                   const opus_int16 *x, int arch);

#if defined(ENABLE_ASSERTIONS) || defined(ENABLE_HARDENING)
void celt_fatal(const char *str, const char *file, int line) {
  (void)str;
  (void)file;
  (void)line;
  abort();
}
#endif

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
  uint32_t u;
  if (!read_u32(&u)) return 0;
  *out = (int32_t)u;
  return 1;
}

static int read_i16(int16_t *out) {
  unsigned char b[2];
  if (!read_exact(b, sizeof(b))) return 0;
  *out = (int16_t)((uint16_t)b[0] | ((uint16_t)b[1] << 8));
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
    int32_t la_shape, snr_dB_Q7, iqb0, iqb1, useCBR, speech_activity_Q8;
    int32_t signalType, fs_kHz, nb_subfr, subfr_length, warping_Q16;
    int32_t shapeWinLength, shapingLPCOrder, LTPCorr_Q15, predGain_Q16;
    int32_t pitchL[MAX_NB_SUBFR];
    int32_t harmSmth, tiltSmth;
    uint32_t res_len, x_len;

    if (!read_i32(&la_shape) || !read_i32(&snr_dB_Q7) || !read_i32(&iqb0) ||
        !read_i32(&iqb1) || !read_i32(&useCBR) ||
        !read_i32(&speech_activity_Q8) || !read_i32(&signalType) ||
        !read_i32(&fs_kHz) || !read_i32(&nb_subfr) || !read_i32(&subfr_length) ||
        !read_i32(&warping_Q16) || !read_i32(&shapeWinLength) ||
        !read_i32(&shapingLPCOrder) || !read_i32(&LTPCorr_Q15) ||
        !read_i32(&predGain_Q16)) {
      return 1;
    }
    for (int i = 0; i < MAX_NB_SUBFR; i++) {
      if (!read_i32(&pitchL[i])) return 1;
    }
    if (!read_i32(&harmSmth) || !read_i32(&tiltSmth)) return 1;
    if (!read_u32(&res_len) || !read_u32(&x_len)) return 1;
    if (res_len > MAX_RES || x_len > MAX_X) return 1;

    static int16_t res[MAX_RES];
    static int16_t xbuf[MAX_X];
    for (uint32_t i = 0; i < res_len; i++) {
      if (!read_i16(&res[i])) return 1;
    }
    for (uint32_t i = 0; i < x_len; i++) {
      if (!read_i16(&xbuf[i])) return 1;
    }

    /* Construct the encoder state / control with all unrelated fields zeroed. */
    silk_encoder_state_FIX psEnc;
    silk_encoder_control_FIX psEncCtrl;
    memset(&psEnc, 0, sizeof(psEnc));
    memset(&psEncCtrl, 0, sizeof(psEncCtrl));

    psEnc.sCmn.la_shape = la_shape;
    psEnc.sCmn.SNR_dB_Q7 = snr_dB_Q7;
    psEnc.sCmn.input_quality_bands_Q15[0] = iqb0;
    psEnc.sCmn.input_quality_bands_Q15[1] = iqb1;
    psEnc.sCmn.useCBR = useCBR;
    psEnc.sCmn.speech_activity_Q8 = speech_activity_Q8;
    psEnc.sCmn.indices.signalType = (opus_int8)signalType;
    psEnc.sCmn.fs_kHz = fs_kHz;
    psEnc.sCmn.nb_subfr = nb_subfr;
    psEnc.sCmn.subfr_length = subfr_length;
    psEnc.sCmn.warping_Q16 = warping_Q16;
    psEnc.sCmn.shapeWinLength = shapeWinLength;
    psEnc.sCmn.shapingLPCOrder = shapingLPCOrder;
    psEnc.LTPCorr_Q15 = LTPCorr_Q15;
    psEnc.sShape.HarmShapeGain_smth_Q16 = harmSmth;
    psEnc.sShape.Tilt_smth_Q16 = tiltSmth;

    psEncCtrl.predGain_Q16 = predGain_Q16;
    for (int i = 0; i < MAX_NB_SUBFR; i++) {
      psEncCtrl.pitchL[i] = pitchL[i];
    }

    /* x points to the first LPC analysis block; the driver reads from
     * x - la_shape, so offset into xbuf by la_shape. */
    const opus_int16 *x = xbuf + la_shape;

    silk_noise_shape_analysis_FIX(&psEnc, &psEncCtrl, res, x, 0);

    if (!write_i32(psEncCtrl.input_quality_Q14)) return 1;
    if (!write_i32(psEncCtrl.coding_quality_Q14)) return 1;
    if (!write_i32(psEnc.sCmn.indices.quantOffsetType)) return 1;
    for (int i = 0; i < MAX_NB_SUBFR; i++) {
      if (!write_i32(psEncCtrl.Gains_Q16[i])) return 1;
    }
    for (int i = 0; i < MAX_NB_SUBFR * MAX_SHAPE_LPC_ORDER; i++) {
      if (!write_i32((int32_t)psEncCtrl.AR_Q13[i])) return 1;
    }
    for (int i = 0; i < MAX_NB_SUBFR; i++) {
      if (!write_i32(psEncCtrl.LF_shp_Q14[i])) return 1;
    }
    for (int i = 0; i < MAX_NB_SUBFR; i++) {
      if (!write_i32(psEncCtrl.Tilt_Q14[i])) return 1;
    }
    for (int i = 0; i < MAX_NB_SUBFR; i++) {
      if (!write_i32(psEncCtrl.HarmShapeGain_Q14[i])) return 1;
    }
    if (!write_i32(psEnc.sShape.HarmShapeGain_smth_Q16)) return 1;
    if (!write_i32(psEnc.sShape.Tilt_smth_Q16)) return 1;
  }

  return 0;
}
