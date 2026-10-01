/* Actual selected libopus LPCNet pitch-front-end primitive calls on caller
 * supplied post-filter state. The helper links the selected OSCE/DRED archive.
 *
 * LE input:  "GLXI", version, frame_count; each frame has pitch, exc_buf
 *            [PITCH_BUF_SIZE] and lp_buf[PITCH_BUF_SIZE] float32 words.
 * LE output: "GLXO", version, frame_count, opus_select_arch(); each frame has
 *            raw xcorr[PITCH_MAX_PERIOD-PITCH_MIN_PERIOD] and ener0, ener1,
 *            xx, yy, xy float32 words.
 */
#include <stdint.h>
#include <stdio.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "celt/pitch.h"
#include "celt/cpu_support.h"
#include "lpcnet_private.h"

#if defined(FIXED_POINT) || !defined(ENABLE_DEEP_PLC)
#error "LPCNet pitch frontend requires selected float neural libopus"
#endif

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (fread(b, 1, 4, stdin) != 4) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) |
      ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}
static int write_u32(uint32_t v) {
  unsigned char b[4];
  b[0] = (unsigned char)v;
  b[1] = (unsigned char)(v >> 8);
  b[2] = (unsigned char)(v >> 16);
  b[3] = (unsigned char)(v >> 24);
  return fwrite(b, 1, 4, stdout) == 4;
}
static int read_float(float *out) {
  uint32_t bits;
  if (!read_u32(&bits)) return 0;
  memcpy(out, &bits, sizeof(bits));
  return 1;
}
static int write_float(float value) {
  uint32_t bits;
  memcpy(&bits, &value, sizeof(bits));
  return write_u32(bits);
}

int main(void) {
  unsigned char magic[4];
  uint32_t version, count, frame;
  int arch;
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 1;
#endif
  if (fread(magic, 1, 4, stdin) != 4 || memcmp(magic, "GLXI", 4) ||
      !read_u32(&version) || version != 1 ||
      !read_u32(&count) || count < 1 || count > 32) return 1;
  arch = opus_select_arch();
  if (fwrite("GLXO", 1, 4, stdout) != 4 ||
      !write_u32(1) || !write_u32(count) || !write_u32((uint32_t)arch)) return 1;
  for (frame = 0; frame < count; frame++) {
    float exc[PITCH_BUF_SIZE], lp[PITCH_BUF_SIZE];
    float xcorr[PITCH_MAX_PERIOD-PITCH_MIN_PERIOD];
    float ener0, ener1, xx, yy, xy;
    uint32_t raw_pitch;
    int pitch, i;
    if (!read_u32(&raw_pitch)) return 1;
    pitch = (int)(int32_t)raw_pitch;
    if (pitch < PITCH_MIN_PERIOD || pitch > PITCH_MAX_PERIOD) return 1;
    for (i = 0; i < PITCH_BUF_SIZE; i++) if (!read_float(&exc[i])) return 1;
    for (i = 0; i < PITCH_BUF_SIZE; i++) if (!read_float(&lp[i])) return 1;
    celt_pitch_xcorr(&exc[PITCH_MAX_PERIOD], exc, xcorr, FRAME_SIZE,
        PITCH_MAX_PERIOD-PITCH_MIN_PERIOD, arch);
    ener0 = celt_inner_prod(&exc[PITCH_MAX_PERIOD], &exc[PITCH_MAX_PERIOD], FRAME_SIZE, arch);
    ener1 = celt_inner_prod(exc, exc, FRAME_SIZE, arch);
    xx = celt_inner_prod(&lp[PITCH_MAX_PERIOD], &lp[PITCH_MAX_PERIOD], FRAME_SIZE, arch);
    yy = celt_inner_prod(&lp[PITCH_MAX_PERIOD-pitch], &lp[PITCH_MAX_PERIOD-pitch], FRAME_SIZE, arch);
    xy = celt_inner_prod(&lp[PITCH_MAX_PERIOD], &lp[PITCH_MAX_PERIOD-pitch], FRAME_SIZE, arch);
    for (i = 0; i < PITCH_MAX_PERIOD-PITCH_MIN_PERIOD; i++)
      if (!write_float(xcorr[i])) return 1;
    if (!write_float(ener0) || !write_float(ener1) || !write_float(xx) ||
        !write_float(yy) || !write_float(xy)) return 1;
  }
  return 0;
}
