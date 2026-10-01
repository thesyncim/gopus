/* Selected fixed libopus clt_compute_allocation on a nonpositive bit budget.
 * One request holds independent cases. All words on the wire are little-endian.
 * in:  "GAZI", version, count; per case start,end,LM,C,totalQ3,prev,
 *      signalBandwidth,trim (signed 32-bit words)
 * out: "GAZO", version, count; per case codedBands,balance,intensity,dual,
 *      tellFrac,range, then cap,pulses,ebits,finePriority (21 words each)
 */
#include <stdint.h>
#include <stdio.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "celt/arch.h"
#include "celt/celt.h"
#include "celt/entenc.h"
#include "celt/modes.h"
#include "celt/rate.h"

#if !defined(FIXED_POINT) || !defined(ENABLE_RES24)
#error "allocation oracle requires selected fixed libopus"
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
static int write_i32(int32_t v) { return write_u32((uint32_t)v); }

int main(void) {
  unsigned char magic[4];
  uint32_t version, count, k;
  int err;
  const CELTMode *mode;
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 1;
#endif
  if (fread(magic, 1, 4, stdin) != 4 || memcmp(magic, "GAZI", 4) ||
      !read_u32(&version) || version != 1 || !read_u32(&count) ||
      count < 1 || count > 32) return 1;
  mode = opus_custom_mode_create(48000, 960, &err);
  if (!mode || err) return 1;
  if (fwrite("GAZO", 1, 4, stdout) != 4 ||
      !write_u32(1) || !write_u32(count)) return 1;
  for (k = 0; k < count; k++) {
    uint32_t raw[8];
    int start, end, lm, channels, prev, signal_bandwidth, trim;
    opus_int32 total, balance = 0;
    int cap[21], offsets[21] = {0}, pulses[21] = {0};
    int ebits[21] = {0}, priority[21] = {0};
    unsigned char packet[64] = {0};
    int intensity, dual = 0, coded_bands, i;
    ec_enc enc;
    for (i = 0; i < 8; i++) if (!read_u32(&raw[i])) return 1;
    start = (int)(int32_t)raw[0];
    end = (int)(int32_t)raw[1];
    lm = (int)(int32_t)raw[2];
    channels = (int)(int32_t)raw[3];
    total = (opus_int32)(int32_t)raw[4];
    prev = (int)(int32_t)raw[5];
    signal_bandwidth = (int)(int32_t)raw[6];
    trim = (int)(int32_t)raw[7];
    if (start < 0 || start >= end || end > 21 || lm < 0 || lm > 3 ||
        channels < 1 || channels > 2 || total > 0 || prev < 0 || prev > 21 ||
        signal_bandwidth < start || signal_bandwidth > 21 ||
        trim < 0 || trim > 10) return 1;
    init_caps(mode, cap, lm, channels);
    intensity = end;
    ec_enc_init(&enc, packet, sizeof(packet));
    coded_bands = clt_compute_allocation(mode, start, end, offsets, cap,
        trim, &intensity, &dual, total, &balance, pulses, ebits, priority,
        channels, lm, &enc, 1, prev, signal_bandwidth);
    if (!write_i32(coded_bands) || !write_i32(balance) ||
        !write_i32(intensity) || !write_i32(dual) ||
        !write_u32(ec_tell_frac(&enc)) || !write_u32(enc.rng)) return 1;
    for (i = 0; i < 21; i++) if (!write_i32(cap[i])) return 1;
    for (i = 0; i < 21; i++) if (!write_i32(pulses[i])) return 1;
    for (i = 0; i < 21; i++) if (!write_i32(ebits[i])) return 1;
    for (i = 0; i < 21; i++) if (!write_i32(priority[i])) return 1;
  }
  return 0;
}
