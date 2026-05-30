#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"
#include "celt/arch.h"
#include "celt/entdec.h"
#include "celt/modes.h"
#include "celt/quant_bands.h"
#include "celt/celt.h"
#include "opus_defines.h"

/* Oracle helper for the libopus FIXED_POINT CELT decode pipeline. Built against
 * the --enable-fixed-point reference tree so config.h defines FIXED_POINT and the
 * quant_bands.c energy unquantizers plus celt_decode_with_ec resolve to their
 * integer paths.
 *
 * MODE_ENERGY runs the energy unquantizers (unquant_coarse_energy +
 * unquant_fine_energy + unquant_energy_finalise) on a caller-supplied coded
 * bitstream, dumping the resulting Q24 oldEBands. It builds a minimal CELTMode
 * (only nbEBands matters to the unquantizers) and a real ec_dec over the bytes.
 *
 * MODE_DECODE runs the full celt_decode_with_ec on a real CELT packet using the
 * static 48000/960 custom mode and dumps the decoded int16 PCM. */

#define INPUT_MAGIC "GCDI"
#define OUTPUT_MAGIC "GCDO"

enum {
  MODE_ENERGY = 0,
  MODE_DECODE = 1
};

int celt_decode_with_ec(CELTDecoder *st, const unsigned char *data, int len,
                        opus_res *pcm, int frame_size, ec_dec *dec, int accum);

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
  return read_exact(out, sizeof(*out));
}

static int write_u32(uint32_t value) {
  return write_exact(&value, sizeof(value));
}

/* MODE_ENERGY wire format (after the GCDI header, version 1, mode and unused
 * count word):
 *   u32 nbEBands, u32 start, u32 end, u32 effEnd, u32 C, u32 LM, u32 intra
 *   u32 nbytes
 *   nbytes x u8 coded   (padded to a 4-byte boundary on the wire)
 *   nbEBands x i32 fine_quant
 *   nbEBands x i32 fine_priority
 *   i32 bits_left
 * Output (after the GCDO header, version 1, count = C*nbEBands):
 *   C*nbEBands x i32 oldEBands (celt_glog, Q24) */
static int eval_energy(void) {
  uint32_t nbEBands, start, end, effEnd, C, LM, intra, nbytes;
  uint32_t total, i, padded;
  unsigned char *coded = NULL;
  int *fine_quant = NULL;
  int *fine_priority = NULL;
  celt_glog *oldEBands = NULL;
  int32_t bits_left = 0;
  CELTMode mode;
  ec_dec dec;
  int ok = 0;

  if (!read_u32(&nbEBands) || !read_u32(&start) || !read_u32(&end) ||
      !read_u32(&effEnd) || !read_u32(&C) || !read_u32(&LM) ||
      !read_u32(&intra) || !read_u32(&nbytes)) {
    return 0;
  }
  (void)effEnd;

  coded = (unsigned char *)malloc(nbytes ? nbytes : 1);
  if (!coded) goto done;
  if (nbytes && !read_exact(coded, nbytes)) goto done;
  padded = (nbytes + 3u) & ~3u;
  for (i = nbytes; i < padded; i++) {
    unsigned char pad;
    if (!read_exact(&pad, 1)) goto done;
  }

  total = C * nbEBands;
  fine_quant = (int *)malloc(nbEBands * sizeof(*fine_quant));
  fine_priority = (int *)malloc(nbEBands * sizeof(*fine_priority));
  oldEBands = (celt_glog *)malloc(total * sizeof(*oldEBands));
  if (!fine_quant || !fine_priority || !oldEBands) goto done;

  for (i = 0; i < nbEBands; i++) {
    uint32_t v;
    if (!read_u32(&v)) goto done;
    fine_quant[i] = (int)(int32_t)v;
  }
  for (i = 0; i < nbEBands; i++) {
    uint32_t v;
    if (!read_u32(&v)) goto done;
    fine_priority[i] = (int)(int32_t)v;
  }
  {
    uint32_t v;
    if (!read_u32(&v)) goto done;
    bits_left = (int32_t)v;
  }

  memset(oldEBands, 0, total * sizeof(*oldEBands));
  memset(&mode, 0, sizeof(mode));
  mode.nbEBands = (int)nbEBands;

  ec_dec_init(&dec, coded, nbytes);
  unquant_coarse_energy(&mode, (int)start, (int)end, oldEBands, (int)intra,
                        &dec, (int)C, (int)LM);
  unquant_fine_energy(&mode, (int)start, (int)end, oldEBands, NULL, fine_quant,
                      &dec, (int)C);
  unquant_energy_finalise(&mode, (int)start, (int)end, oldEBands, fine_quant,
                          fine_priority, (int)bits_left, &dec, (int)C);

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(total)) {
    goto done;
  }
  for (i = 0; i < total; i++) {
    if (!write_u32((uint32_t)(int32_t)oldEBands[i])) goto done;
  }
  ok = 1;

done:
  free(coded);
  free(fine_quant);
  free(fine_priority);
  free(oldEBands);
  return ok;
}

/* MODE_DECODE wire format (after the GCDI header, version 1, mode and unused
 * count word):
 *   u32 channels, u32 frame_size, u32 start, u32 end, u32 nbytes
 *   nbytes x u8 packet (padded to a 4-byte boundary on the wire)
 * Output (after the GCDO header, version 1, count = channels*frame_size):
 *   channels*frame_size x i16 pcm */
static int eval_decode(void) {
  uint32_t channels, frame_size, start, end, nbytes;
  uint32_t i, padded, n;
  unsigned char *packet = NULL;
  opus_res *pcm = NULL;
  int16_t *out = NULL;
  CELTDecoder *dec = NULL;
  ec_dec ec;
  int ret;
  int ok = 0;

  if (!read_u32(&channels) || !read_u32(&frame_size) || !read_u32(&start) ||
      !read_u32(&end) || !read_u32(&nbytes)) {
    return 0;
  }

  packet = (unsigned char *)malloc(nbytes ? nbytes : 1);
  if (!packet) goto done;
  if (nbytes && !read_exact(packet, nbytes)) goto done;
  padded = (nbytes + 3u) & ~3u;
  for (i = nbytes; i < padded; i++) {
    unsigned char pad;
    if (!read_exact(&pad, 1)) goto done;
  }

  dec = (CELTDecoder *)malloc(celt_decoder_get_size((int)channels));
  if (!dec) goto done;
  if (celt_decoder_init(dec, 48000, (int)channels) != OPUS_OK) goto done;
  celt_decoder_ctl(dec, CELT_SET_START_BAND_REQUEST, (int)start);
  celt_decoder_ctl(dec, CELT_SET_END_BAND_REQUEST, (int)end);

  n = channels * frame_size;
  pcm = (opus_res *)malloc((n ? n : 1) * sizeof(*pcm));
  out = (int16_t *)malloc((n ? n : 1) * sizeof(*out));
  if (!pcm || !out) goto done;

  ec_dec_init(&ec, packet, nbytes);
  ret = celt_decode_with_ec(dec, packet, (int)nbytes, pcm, (int)frame_size, &ec, 0);
  if (ret < 0) goto done;

  for (i = 0; i < n; i++) {
    out[i] = RES2INT16(pcm[i]);
  }

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(n)) {
    goto done;
  }
  for (i = 0; i < n; i++) {
    if (!write_exact(&out[i], sizeof(out[i]))) goto done;
  }
  ok = 1;

done:
  free(dec);
  free(packet);
  free(pcm);
  free(out);
  return ok;
}

int main(void) {
  char magic[4];
  uint32_t version;
  uint32_t mode;
  uint32_t count;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) return 1;
  if (!read_u32(&version) || version != 1 || !read_u32(&mode) || !read_u32(&count)) return 1;
  (void)count;

  switch (mode) {
    case MODE_ENERGY:
      return eval_energy() ? 0 : 1;
    case MODE_DECODE:
      return eval_decode() ? 0 : 1;
  }
  return 1;
}
