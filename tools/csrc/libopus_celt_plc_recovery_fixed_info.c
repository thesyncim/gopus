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
#include "celt/celt.h"
#include "opus_defines.h"

/* Oracle helper for the libopus FIXED_POINT CELT loss-recovery path.
 * Built against the --enable-fixed-point reference tree so config.h defines
 * FIXED_POINT and celt_decode_with_ec resolves to the integer path.
 *
 * It primes a real CELTDecoder with one decoded prior-good packet, runs
 * num_lost consecutive lost frames via celt_decode_with_ec(NULL,...), then
 * decodes a real good packet (loss_duration!=0 at entry) and dumps the
 * recovered good-frame int16 PCM. This exercises the energy-prediction safety
 * block that clamps oldBandE toward the last-frame minima / continues a
 * downward slope before unquant_coarse_energy. */

#define INPUT_MAGIC "GPRI"
#define OUTPUT_MAGIC "GPRO"

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

static int read_u32(uint32_t *out) { return read_exact(out, sizeof(*out)); }
static int write_u32(uint32_t value) { return write_exact(&value, sizeof(value)); }

static unsigned char *read_packet(uint32_t nbytes) {
  uint32_t i, padded;
  unsigned char *packet = (unsigned char *)malloc(nbytes ? nbytes : 1);
  if (!packet) return NULL;
  if (nbytes && !read_exact(packet, nbytes)) {
    free(packet);
    return NULL;
  }
  padded = (nbytes + 3u) & ~3u;
  for (i = nbytes; i < padded; i++) {
    unsigned char pad;
    if (!read_exact(&pad, 1)) {
      free(packet);
      return NULL;
    }
  }
  return packet;
}

/* Wire format (after the GPRI header, version 1, mode and unused count word):
 *   u32 channels, u32 frame_size, u32 start, u32 end, u32 num_lost
 *   u32 prime_nbytes, prime packet (padded to 4 bytes)
 *   u32 good_nbytes,  good  packet (padded to 4 bytes)
 * Output (after the GPRO header, version 1, count = channels*frame_size):
 *   channels*frame_size x i16 recovered good-frame pcm */
static int eval_recovery(void) {
  uint32_t channels, frame_size, start, end, num_lost;
  uint32_t prime_nbytes, good_nbytes;
  uint32_t i, k, n;
  unsigned char *prime = NULL, *good = NULL;
  opus_res *pcm = NULL;
  int16_t *out = NULL;
  CELTDecoder *dec = NULL;
  ec_dec ec;
  int ret;
  int ok = 0;

  if (!read_u32(&channels) || !read_u32(&frame_size) || !read_u32(&start) ||
      !read_u32(&end) || !read_u32(&num_lost) || !read_u32(&prime_nbytes)) {
    return 0;
  }
  prime = read_packet(prime_nbytes);
  if (!prime) goto done;
  if (!read_u32(&good_nbytes)) goto done;
  good = read_packet(good_nbytes);
  if (!good) goto done;

  dec = (CELTDecoder *)malloc(celt_decoder_get_size((int)channels));
  if (!dec) goto done;
  if (celt_decoder_init(dec, 48000, (int)channels) != OPUS_OK) goto done;
  celt_decoder_ctl(dec, CELT_SET_START_BAND_REQUEST, (int)start);
  celt_decoder_ctl(dec, CELT_SET_END_BAND_REQUEST, (int)end);

  n = channels * frame_size;
  pcm = (opus_res *)malloc((n ? n : 1) * sizeof(*pcm));
  if (!pcm) goto done;
  out = (int16_t *)malloc((n ? n : 1) * sizeof(*out));
  if (!out) goto done;

  /* Prime with the prior good packet. */
  ec_dec_init(&ec, prime, prime_nbytes);
  ret = celt_decode_with_ec(dec, prime, (int)prime_nbytes, pcm, (int)frame_size, &ec, 0);
  if (ret < 0) goto done;

  /* Lose num_lost frames. */
  for (k = 0; k < num_lost; k++) {
    ret = celt_decode_with_ec(dec, NULL, 0, pcm, (int)frame_size, NULL, 0);
    if (ret < 0) goto done;
  }

  /* Decode the recovery good packet (loss_duration != 0 at entry). */
  ec_dec_init(&ec, good, good_nbytes);
  ret = celt_decode_with_ec(dec, good, (int)good_nbytes, pcm, (int)frame_size, &ec, 0);
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
  free(prime);
  free(good);
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
  (void)mode;

  return eval_recovery() ? 0 : 1;
}
