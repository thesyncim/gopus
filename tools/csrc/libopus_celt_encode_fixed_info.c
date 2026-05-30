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
#include "celt/entenc.h"
#include "celt/modes.h"
#include "celt/bands.h"
#include "celt/mdct.h"
#include "celt/celt.h"
#include "opus_defines.h"

/* Oracle helper for the libopus FIXED_POINT CELT encode pipeline. Built against
 * the --enable-fixed-point reference tree so config.h defines FIXED_POINT and
 * celt_encode_with_ec plus the band front-end resolve to their integer paths.
 *
 * MODE_ENCODE runs the full celt_encode_with_ec on a real CELTEncoder for the
 * static 48000/960 custom mode and dumps the produced packet bytes (the
 * end-goal reference for the integer encoder).
 *
 * MODE_ENCODE_EXT is MODE_ENCODE with the OPUS_SET_LFE and/or
 * OPUS_SET_ENERGY_MASK controls applied (and explicit VBR/CVBR), exercising the
 * low-frequency-effects and surround energy-mask encode paths.
 *
 * MODE_FRONTEND reproduces the encode front-end inline (celt_preemphasis ->
 * compute_mdcts -> compute_band_energies -> normalise_bands), i.e. the exact
 * sequence celt_encode_with_ec runs just before quant_all_bands, and dumps the
 * intermediate freq (post-MDCT, celt_sig), bandE (compute_band_energies,
 * celt_ener) and normalised X (celt_norm, after normalise_bands). It mirrors a
 * fresh encoder's frame-0 state: preemph_memE == 0 and the in-buffer overlap
 * prefix (from prefilter_mem) == 0. isTransient is caller-supplied so both the
 * normal (shortBlocks==0) and transient (shortBlocks==M) MDCT stripings can be
 * exercised independently of transient_analysis. */

#define INPUT_MAGIC "GCEI"
#define OUTPUT_MAGIC "GCEO"

enum {
  MODE_ENCODE = 0,
  MODE_FRONTEND = 1,
  MODE_ENCODE_SEQ = 2,
  MODE_ENCODE_EXT = 3
};

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

/* Inline copy of the (static) compute_mdcts from celt_encoder.c for the
 * FIXED_POINT non-QEXT build. upsample is fixed to 1 (48k core), so the
 * upsample scaling branch is omitted. */
static void frontend_compute_mdcts(const CELTMode *mode, int shortBlocks,
                                   celt_sig *in, celt_sig *out, int C, int CC,
                                   int LM) {
  const int overlap = mode->overlap;
  int N, B, shift;
  int i, b, c;
  if (shortBlocks) {
    B = shortBlocks;
    N = mode->shortMdctSize;
    shift = mode->maxLM;
  } else {
    B = 1;
    N = mode->shortMdctSize << LM;
    shift = mode->maxLM - LM;
  }
  c = 0;
  do {
    for (b = 0; b < B; b++) {
      clt_mdct_forward(&mode->mdct, in + c * (B * N + overlap) + b * N,
                       &out[b + c * N * B], mode->window, overlap, shift, B, 0);
    }
  } while (++c < CC);
  if (CC == 2 && C == 1) {
    for (i = 0; i < B * N; i++)
      out[i] = ADD32(HALF32(out[i]), HALF32(out[B * N + i]));
  }
}

/* MODE_ENCODE wire format (after the GCEI header, version 1, mode and unused
 * count word):
 *   u32 channels, u32 frame_size, u32 start, u32 end, u32 bitrate,
 *   u32 complexity, u32 nbCompressedBytes, u32 nsamples (= channels*frame_size)
 *   nsamples x i16 pcm (padded to a 4-byte boundary on the wire)
 * Output (after the GCEO header, version 1, count = packet length in bytes):
 *   count x u8 packet (padded to a 4-byte boundary on the wire) */
static int eval_encode(void) {
  uint32_t channels, frame_size, start, end, bitrate, complexity;
  uint32_t nbCompressedBytes, nsamples, i, padded;
  int16_t *pcm16 = NULL;
  opus_res *pcm = NULL;
  unsigned char *packet = NULL;
  CELTEncoder *enc = NULL;
  CELTMode *mode = NULL;
  ec_enc ec;
  int ret, err;
  int ok = 0;

  if (!read_u32(&channels) || !read_u32(&frame_size) || !read_u32(&start) ||
      !read_u32(&end) || !read_u32(&bitrate) || !read_u32(&complexity) ||
      !read_u32(&nbCompressedBytes) || !read_u32(&nsamples)) {
    return 0;
  }

  pcm16 = (int16_t *)malloc((nsamples ? nsamples : 1) * sizeof(*pcm16));
  pcm = (opus_res *)malloc((nsamples ? nsamples : 1) * sizeof(*pcm));
  if (!pcm16 || !pcm) goto done;
  if (nsamples && !read_exact(pcm16, nsamples * sizeof(*pcm16))) goto done;
  padded = ((nsamples * 2u) + 3u) & ~3u;
  for (i = nsamples * 2u; i < padded; i++) {
    unsigned char pad;
    if (!read_exact(&pad, 1)) goto done;
  }
  for (i = 0; i < nsamples; i++)
    pcm[i] = INT16TORES(pcm16[i]);

  mode = (CELTMode *)opus_custom_mode_create(48000, 960, &err);
  if (!mode || err != OPUS_OK) goto done;
  enc = (CELTEncoder *)malloc(celt_encoder_get_size((int)channels));
  if (!enc) goto done;
  if (celt_encoder_init(enc, 48000, (int)channels, 0) != OPUS_OK) goto done;
  celt_encoder_ctl(enc, CELT_SET_START_BAND_REQUEST, (int)start);
  celt_encoder_ctl(enc, CELT_SET_END_BAND_REQUEST, (int)end);
  celt_encoder_ctl(enc, OPUS_SET_BITRATE_REQUEST, (opus_int32)(int32_t)bitrate);
  celt_encoder_ctl(enc, OPUS_SET_COMPLEXITY_REQUEST, (int)complexity);
  celt_encoder_ctl(enc, OPUS_SET_VBR_REQUEST, 0);
  celt_encoder_ctl(enc, CELT_SET_SIGNALLING_REQUEST, 0);

  packet = (unsigned char *)malloc(nbCompressedBytes ? nbCompressedBytes : 1);
  if (!packet) goto done;

  ec_enc_init(&ec, packet, nbCompressedBytes);
  ret = celt_encode_with_ec(enc, pcm, (int)frame_size, packet,
                            (int)nbCompressedBytes, &ec);
  if (ret < 0) goto done;

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32((uint32_t)ret)) {
    goto done;
  }
  if (ret && !write_exact(packet, (size_t)ret)) goto done;
  padded = ((uint32_t)ret + 3u) & ~3u;
  for (i = (uint32_t)ret; i < padded; i++) {
    unsigned char pad = 0;
    if (!write_exact(&pad, 1)) goto done;
  }
  ok = 1;

done:
  free(pcm16);
  free(pcm);
  free(packet);
  free(enc);
  return ok;
}

/* MODE_ENCODE_SEQ wire format (after the GCEI header, version 1, mode and
 * unused count word):
 *   u32 channels, u32 frame_size, u32 start, u32 end, u32 bitrate,
 *   u32 complexity, u32 vbr, u32 constrained_vbr, u32 max_bytes,
 *   u32 nframes, u32 nsamples (= channels*frame_size*nframes)
 *   nsamples x i16 pcm (padded to a 4-byte boundary on the wire)
 * One CELTEncoder is created and configured (OPUS_SET_VBR / VBR_CONSTRAINT),
 * then the nframes consecutive frames are encoded in sequence so all
 * cross-frame state (VBR reservoir/drift, oldBandE/oldLogE, spec_avg,
 * consec_transient, prefilter_mem) carries over.
 * Output (after the GCEO header, version 1, count = nframes):
 *   for each frame: u32 packet_len, packet_len x u8 packet bytes
 *     (each packet padded to a 4-byte boundary on the wire) */
static int eval_encode_seq(void) {
  uint32_t channels, frame_size, start, end, bitrate, complexity;
  uint32_t vbr, constrained_vbr, max_bytes, nframes, nsamples;
  uint32_t i, f, padded;
  int per_frame, ret, err;
  int16_t *pcm16 = NULL;
  opus_res *pcm = NULL;
  unsigned char *packet = NULL;
  CELTEncoder *enc = NULL;
  CELTMode *mode = NULL;
  int ok = 0;

  if (!read_u32(&channels) || !read_u32(&frame_size) || !read_u32(&start) ||
      !read_u32(&end) || !read_u32(&bitrate) || !read_u32(&complexity) ||
      !read_u32(&vbr) || !read_u32(&constrained_vbr) || !read_u32(&max_bytes) ||
      !read_u32(&nframes) || !read_u32(&nsamples)) {
    return 0;
  }

  pcm16 = (int16_t *)malloc((nsamples ? nsamples : 1) * sizeof(*pcm16));
  pcm = (opus_res *)malloc((nsamples ? nsamples : 1) * sizeof(*pcm));
  if (!pcm16 || !pcm) goto done;
  if (nsamples && !read_exact(pcm16, nsamples * sizeof(*pcm16))) goto done;
  padded = ((nsamples * 2u) + 3u) & ~3u;
  for (i = nsamples * 2u; i < padded; i++) {
    unsigned char pad;
    if (!read_exact(&pad, 1)) goto done;
  }
  for (i = 0; i < nsamples; i++)
    pcm[i] = INT16TORES(pcm16[i]);

  mode = (CELTMode *)opus_custom_mode_create(48000, 960, &err);
  if (!mode || err != OPUS_OK) goto done;
  enc = (CELTEncoder *)malloc(celt_encoder_get_size((int)channels));
  if (!enc) goto done;
  if (celt_encoder_init(enc, 48000, (int)channels, 0) != OPUS_OK) goto done;
  celt_encoder_ctl(enc, CELT_SET_START_BAND_REQUEST, (int)start);
  celt_encoder_ctl(enc, CELT_SET_END_BAND_REQUEST, (int)end);
  celt_encoder_ctl(enc, OPUS_SET_BITRATE_REQUEST, (opus_int32)(int32_t)bitrate);
  celt_encoder_ctl(enc, OPUS_SET_COMPLEXITY_REQUEST, (int)complexity);
  celt_encoder_ctl(enc, OPUS_SET_VBR_REQUEST, (int)vbr);
  celt_encoder_ctl(enc, OPUS_SET_VBR_CONSTRAINT_REQUEST, (int)constrained_vbr);
  celt_encoder_ctl(enc, CELT_SET_SIGNALLING_REQUEST, 0);

  packet = (unsigned char *)malloc(max_bytes ? max_bytes : 1);
  if (!packet) goto done;

  per_frame = (int)channels * (int)frame_size;

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(nframes)) {
    goto done;
  }

  for (f = 0; f < nframes; f++) {
    ec_enc ec;
    ec_enc_init(&ec, packet, max_bytes);
    ret = celt_encode_with_ec(enc, pcm + (size_t)f * per_frame,
                              (int)frame_size, packet, (int)max_bytes, &ec);
    if (ret < 0) goto done;
    if (!write_u32((uint32_t)ret)) goto done;
    if (ret && !write_exact(packet, (size_t)ret)) goto done;
    padded = ((uint32_t)ret + 3u) & ~3u;
    for (i = (uint32_t)ret; i < padded; i++) {
      unsigned char pad = 0;
      if (!write_exact(&pad, 1)) goto done;
    }
  }
  ok = 1;

done:
  free(pcm16);
  free(pcm);
  free(packet);
  free(enc);
  return ok;
}

/* MODE_FRONTEND wire format (after the GCEI header, version 1, mode and unused
 * count word):
 *   u32 channels, u32 frame_size, u32 start, u32 end, u32 isTransient,
 *   u32 nsamples (= channels*frame_size)
 *   nsamples x i16 pcm (padded to a 4-byte boundary on the wire)
 * Output (after the GCEO header, version 1, count = C*N):
 *   C*N x i32 freq (celt_sig)
 *   C*nbEBands x i32 bandE (celt_ener)
 *   C*N x i32 X (celt_norm) */
static int eval_frontend(void) {
  uint32_t channels, frame_size, start, end, isTransient, nsamples;
  uint32_t i, padded;
  int C, CC, LM, M, N, overlap, nbEBands, effEnd, effEBands, shortBlocks, c;
  int16_t *pcm16 = NULL;
  opus_res *pcm = NULL;
  celt_sig *in = NULL;
  celt_sig *freq = NULL;
  celt_ener *bandE = NULL;
  celt_norm *X = NULL;
  celt_sig preemph_mem;
  CELTMode *mode = NULL;
  int err;
  int ok = 0;

  if (!read_u32(&channels) || !read_u32(&frame_size) || !read_u32(&start) ||
      !read_u32(&end) || !read_u32(&isTransient) || !read_u32(&nsamples)) {
    return 0;
  }

  pcm16 = (int16_t *)malloc((nsamples ? nsamples : 1) * sizeof(*pcm16));
  pcm = (opus_res *)malloc((nsamples ? nsamples : 1) * sizeof(*pcm));
  if (!pcm16 || !pcm) goto done;
  if (nsamples && !read_exact(pcm16, nsamples * sizeof(*pcm16))) goto done;
  padded = ((nsamples * 2u) + 3u) & ~3u;
  for (i = nsamples * 2u; i < padded; i++) {
    unsigned char pad;
    if (!read_exact(&pad, 1)) goto done;
  }
  for (i = 0; i < nsamples; i++)
    pcm[i] = INT16TORES(pcm16[i]);

  mode = (CELTMode *)opus_custom_mode_create(48000, 960, &err);
  if (!mode || err != OPUS_OK) goto done;

  C = (int)channels;
  CC = (int)channels;
  overlap = mode->overlap;
  nbEBands = mode->nbEBands;
  effEBands = mode->effEBands;

  for (LM = 0; LM <= mode->maxLM; LM++) {
    if ((mode->shortMdctSize << LM) == (int)frame_size) break;
  }
  M = 1 << LM;
  N = M * mode->shortMdctSize;
  shortBlocks = isTransient ? M : 0;

  effEnd = (int)end;
  if (effEnd > effEBands) effEnd = effEBands;

  in = (celt_sig *)malloc((size_t)CC * (N + overlap) * sizeof(*in));
  freq = (celt_sig *)malloc((size_t)CC * N * sizeof(*freq));
  bandE = (celt_ener *)malloc((size_t)nbEBands * CC * sizeof(*bandE));
  X = (celt_norm *)malloc((size_t)C * N * sizeof(*X));
  if (!in || !freq || !bandE || !X) goto done;

  /* Mirror the fresh-encoder frame-0 in-buffer construction: overlap prefix
     comes from prefilter_mem (all zero), and celt_preemphasis runs with
     preemph_memE == 0. upsample == 1, clip == 0 for the 48k core. */
  for (c = 0; c < CC; c++) {
    int j;
    preemph_mem = 0;
    celt_preemphasis(pcm + c, in + c * (N + overlap) + overlap, N, CC, 1,
                     mode->preemph, &preemph_mem, 0);
    for (j = 0; j < overlap; j++)
      in[c * (N + overlap) + j] = 0;
  }

  frontend_compute_mdcts(mode, shortBlocks, in, freq, C, CC, LM);
  compute_band_energies(mode, freq, bandE, effEnd, C, LM, 0);
  normalise_bands(mode, freq, X, bandE, effEnd, C, M);

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32((uint32_t)(C * N))) {
    goto done;
  }
  for (i = 0; i < (uint32_t)(C * N); i++) {
    if (!write_u32((uint32_t)(int32_t)freq[i])) goto done;
  }
  for (i = 0; i < (uint32_t)(C * nbEBands); i++) {
    if (!write_u32((uint32_t)(int32_t)bandE[i])) goto done;
  }
  for (i = 0; i < (uint32_t)(C * N); i++) {
    if (!write_u32((uint32_t)(int32_t)X[i])) goto done;
  }
  ok = 1;

done:
  free(pcm16);
  free(pcm);
  free(in);
  free(freq);
  free(bandE);
  free(X);
  return ok;
}

/* MODE_ENCODE_EXT wire format (after the GCEI header, version 1, mode and
 * unused count word):
 *   u32 channels, u32 frame_size, u32 start, u32 end, u32 bitrate,
 *   u32 complexity, u32 nbCompressedBytes, u32 vbr, u32 constrained_vbr,
 *   u32 lfe, u32 has_mask, u32 nsamples (= channels*frame_size)
 *   nsamples x i16 pcm (padded to a 4-byte boundary on the wire)
 *   if has_mask: channels*nbEBands x i32 energy_mask (celt_glog)
 * Runs one celt_encode_with_ec frame with CELT_SET_LFE and/or
 * CELT_SET_ENERGY_MASK applied, dumping the produced packet bytes.
 * Output mirrors MODE_ENCODE. */
static int eval_encode_ext(void) {
  uint32_t channels, frame_size, start, end, bitrate, complexity;
  uint32_t nbCompressedBytes, vbr, constrained_vbr, lfe, has_mask, nsamples;
  uint32_t i, padded;
  int nbEBands;
  int16_t *pcm16 = NULL;
  opus_res *pcm = NULL;
  celt_glog *mask = NULL;
  unsigned char *packet = NULL;
  CELTEncoder *enc = NULL;
  CELTMode *mode = NULL;
  ec_enc ec;
  int ret, err;
  int ok = 0;

  if (!read_u32(&channels) || !read_u32(&frame_size) || !read_u32(&start) ||
      !read_u32(&end) || !read_u32(&bitrate) || !read_u32(&complexity) ||
      !read_u32(&nbCompressedBytes) || !read_u32(&vbr) ||
      !read_u32(&constrained_vbr) || !read_u32(&lfe) || !read_u32(&has_mask) ||
      !read_u32(&nsamples)) {
    return 0;
  }

  pcm16 = (int16_t *)malloc((nsamples ? nsamples : 1) * sizeof(*pcm16));
  pcm = (opus_res *)malloc((nsamples ? nsamples : 1) * sizeof(*pcm));
  if (!pcm16 || !pcm) goto done;
  if (nsamples && !read_exact(pcm16, nsamples * sizeof(*pcm16))) goto done;
  padded = ((nsamples * 2u) + 3u) & ~3u;
  for (i = nsamples * 2u; i < padded; i++) {
    unsigned char pad;
    if (!read_exact(&pad, 1)) goto done;
  }
  for (i = 0; i < nsamples; i++)
    pcm[i] = INT16TORES(pcm16[i]);

  mode = (CELTMode *)opus_custom_mode_create(48000, 960, &err);
  if (!mode || err != OPUS_OK) goto done;
  nbEBands = mode->nbEBands;

  if (has_mask) {
    uint32_t nmask = channels * (uint32_t)nbEBands;
    mask = (celt_glog *)malloc(nmask * sizeof(*mask));
    if (!mask) goto done;
    for (i = 0; i < nmask; i++) {
      uint32_t v;
      if (!read_u32(&v)) goto done;
      mask[i] = (celt_glog)(int32_t)v;
    }
  }

  enc = (CELTEncoder *)malloc(celt_encoder_get_size((int)channels));
  if (!enc) goto done;
  if (celt_encoder_init(enc, 48000, (int)channels, 0) != OPUS_OK) goto done;
  celt_encoder_ctl(enc, CELT_SET_START_BAND_REQUEST, (int)start);
  celt_encoder_ctl(enc, CELT_SET_END_BAND_REQUEST, (int)end);
  celt_encoder_ctl(enc, OPUS_SET_BITRATE_REQUEST, (opus_int32)(int32_t)bitrate);
  celt_encoder_ctl(enc, OPUS_SET_COMPLEXITY_REQUEST, (int)complexity);
  celt_encoder_ctl(enc, OPUS_SET_VBR_REQUEST, (int)vbr);
  celt_encoder_ctl(enc, OPUS_SET_VBR_CONSTRAINT_REQUEST, (int)constrained_vbr);
  celt_encoder_ctl(enc, CELT_SET_SIGNALLING_REQUEST, 0);
  celt_encoder_ctl(enc, OPUS_SET_LFE_REQUEST, (int)lfe);
  if (has_mask)
    celt_encoder_ctl(enc, OPUS_SET_ENERGY_MASK_REQUEST, mask);

  packet = (unsigned char *)malloc(nbCompressedBytes ? nbCompressedBytes : 1);
  if (!packet) goto done;

  ec_enc_init(&ec, packet, nbCompressedBytes);
  ret = celt_encode_with_ec(enc, pcm, (int)frame_size, packet,
                            (int)nbCompressedBytes, &ec);
  if (ret < 0) goto done;

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32((uint32_t)ret)) {
    goto done;
  }
  if (ret && !write_exact(packet, (size_t)ret)) goto done;
  padded = ((uint32_t)ret + 3u) & ~3u;
  for (i = (uint32_t)ret; i < padded; i++) {
    unsigned char pad = 0;
    if (!write_exact(&pad, 1)) goto done;
  }
  ok = 1;

done:
  free(pcm16);
  free(pcm);
  free(mask);
  free(packet);
  free(enc);
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
    case MODE_ENCODE:
      return eval_encode() ? 0 : 1;
    case MODE_FRONTEND:
      return eval_frontend() ? 0 : 1;
    case MODE_ENCODE_SEQ:
      return eval_encode_seq() ? 0 : 1;
    case MODE_ENCODE_EXT:
      return eval_encode_ext() ? 0 : 1;
  }
  return 1;
}
