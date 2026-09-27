/* Stateful, full-packet oracle for the selected FIXED_POINT silk_Encode API. */
#include "config.h"
#include "API.h"
#include "arch.h"
#include "cpu_support.h"
#include "entenc.h"
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif
#if !defined(FIXED_POINT) || !defined(ENABLE_RES24)
#error "FIXED_POINT and ENABLE_RES24 are required"
#endif

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (fread(b, 1, 4, stdin) != 4) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) |
         ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}
static int write_u32(uint32_t v) {
  unsigned char b[4] = {(unsigned char)v, (unsigned char)(v >> 8),
                       (unsigned char)(v >> 16), (unsigned char)(v >> 24)};
  return fwrite(b, 1, 4, stdout) == 4;
}
int main(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 1;
#endif
  char magic[4];
  uint32_t version, fs, ch, ms, br, cbr, maxbits, complexity, frames;
  if (fread(magic, 1, 4, stdin) != 4 || memcmp(magic, "GSAI", 4) ||
      !read_u32(&version) || version != 1 || !read_u32(&fs) ||
      !read_u32(&ch) || !read_u32(&ms) || !read_u32(&br) ||
      !read_u32(&cbr) || !read_u32(&maxbits) || !read_u32(&complexity) ||
      !read_u32(&frames)) return 2;
  if ((fs != 8000 && fs != 12000 && fs != 16000) || ch < 1 || ch > 2 ||
      (ms != 10 && ms != 20 && ms != 40 && ms != 60) || br < 1 ||
      br > 1000000 || cbr > 1 || maxbits < 1 || maxbits > 1275*8 ||
      complexity > 10 || frames < 1 || frames > 1000) return 3;
  opus_int size = 0;
  int arch = opus_select_arch(), n = fs * ms / 1000;
  if (silk_Get_Encoder_Size(&size, ch) || size <= 0) return 4;
  void *st = calloc(1, size);
  opus_res *pcm = calloc(n*ch, sizeof(*pcm));
  if (!st || !pcm) { free(st); free(pcm); return 5; }
  int result = 6;
  silk_EncControlStruct ctl;
  memset(&ctl, 0, sizeof(ctl));
  if (silk_InitEncoder(st, ch, arch, &ctl)) goto done;
  if (fwrite("GSAO", 1, 4, stdout) != 4 || !write_u32(1) ||
      !write_u32(frames) || !write_u32(arch)) goto done;
  for (uint32_t frame = 0; frame < frames; frame++) {
    uint32_t reset;
    if (!read_u32(&reset) || reset > 1) goto done;
    if (reset && silk_InitEncoder(st, ch, arch, &ctl)) goto done;
    /* Explicit controls; all omitted feature controls are disabled. */
    memset(&ctl, 0, sizeof(ctl));
    ctl.nChannelsAPI = ctl.nChannelsInternal = ch;
    ctl.API_sampleRate = fs;
    ctl.maxInternalSampleRate = ctl.minInternalSampleRate = fs;
    ctl.desiredInternalSampleRate = fs;
    ctl.payloadSize_ms = ms;
    ctl.bitRate = br;
    ctl.complexity = complexity;
    ctl.useCBR = cbr;
    ctl.maxBits = maxbits;
    for (int i = 0; i < n*(int)ch; i++) {
      unsigned char b[2];
      if (fread(b, 1, 2, stdin) != 2) goto done;
      int16_t q = (int16_t)((uint16_t)b[0] | ((uint16_t)b[1] << 8));
      pcm[i] = INT16TORES(q);
    }
    unsigned char buf[1275] = {0};
    ec_enc ec;
    ec_enc_init(&ec, buf, sizeof(buf));
    opus_int32 bytes = sizeof(buf);
    int err = silk_Encode(st, &ctl, pcm, n, &ec, &bytes, 0, 1);
    if (err) { fprintf(stderr, "frame %u silk_Encode=%d\n", frame, err); goto done; }
    uint32_t range = ec.rng, tell = ec_tell(&ec);
    ec_enc_done(&ec);
    if (ec.error || bytes < 0 || bytes > (int)sizeof(buf)) goto done;
    if (!write_u32(bytes) || !write_u32(range) || !write_u32(tell) ||
        fwrite(buf, 1, bytes, stdout) != (size_t)bytes) goto done;
  }
  if (fgetc(stdin) != EOF || ferror(stdin) || fflush(stdout)) goto done;
  result = 0;
done:
  free(pcm);
  free(st);
  return result;
}
