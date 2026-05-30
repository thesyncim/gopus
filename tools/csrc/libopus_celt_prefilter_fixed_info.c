/* Fixed-point CELT encode-side prefilter oracle.
 *
 * Built against the --enable-fixed-point libopus reference (config.h defines
 * FIXED_POINT, ENABLE_RES24, ENABLE_QEXT off, OPUS_ARM_ASM off). It exercises
 * the value-producing portion of run_prefilter (celt/celt_encoder.c): the
 * single-tone fallback, the pitch analysis (pitch_downsample + pitch_search +
 * remove_doubling, linked from the reference library), the gain/qg
 * quantisation and tapset decision, and the post-filter parameter bitstream
 * emission (octave/period/gain/tapset) through a real ec_enc.
 *
 * run_prefilter is static and tied to the encoder state, so the value-producing
 * block is reproduced here verbatim from celt/celt_encoder.c (FIXED_POINT path)
 * with the encoder fields passed in as explicit inputs. The exported
 * pitch_downsample/pitch_search/remove_doubling are linked from libopus.a so the
 * real integer kernels are exercised.
 */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#ifdef HAVE_CONFIG_H
#include "config.h"
#endif

#undef OPUS_ARM_MAY_HAVE_NEON_INTR
#undef OPUS_ARM_PRESUME_NEON_INTR
#undef OPUS_ARM_MAY_HAVE_NEON
#undef OPUS_ARM_PRESUME_NEON
#undef OPUS_HAVE_RTCD

#include "arch.h"
#include "pitch.h"
#include "celt.h"
#include "entenc.h"

#define GPRI_MAGIC "GPRI"
#define GPRO_MAGIC "GPRO"

#define COMBFILTER_MAXPERIOD 1024
#define COMBFILTER_MINPERIOD 15

static int read_exact(void *dst, size_t n) {
  return fread(dst, 1, n, stdin) == n;
}

static int write_exact(const void *src, size_t n) {
  return fwrite(src, 1, n, stdout) == n;
}

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact(b, 4)) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) | ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int read_i32(int32_t *out) {
  uint32_t v;
  if (!read_u32(&v)) return 0;
  *out = (int32_t)v;
  return 1;
}

static int write_u32(uint32_t v) {
  unsigned char b[4];
  b[0] = (unsigned char)(v & 0xFF);
  b[1] = (unsigned char)((v >> 8) & 0xFF);
  b[2] = (unsigned char)((v >> 16) & 0xFF);
  b[3] = (unsigned char)((v >> 24) & 0xFF);
  return write_exact(b, 4);
}

static int write_i32(int32_t v) {
  return write_u32((uint32_t)v);
}

static int read_i16(int16_t *out) {
  unsigned char b[2];
  if (!read_exact(b, 2)) return 0;
  *out = (int16_t)((uint16_t)b[0] | ((uint16_t)b[1] << 8));
  return 1;
}

static int read_f32(float *out) {
  uint32_t v;
  if (!read_u32(&v)) return 0;
  memcpy(out, &v, 4);
  return 1;
}

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

static opus_val32 *read_i32_array(uint32_t n) {
  opus_val32 *buf = (opus_val32 *)malloc((size_t)(n == 0 ? 1 : n) * sizeof(opus_val32));
  uint32_t i;
  if (buf == NULL) return NULL;
  for (i = 0; i < n; i++) {
    int32_t v;
    if (!read_i32(&v)) { free(buf); return NULL; }
    buf[i] = (opus_val32)v;
  }
  return buf;
}

int main(void) {
  char magic[4];
  uint32_t version;
  uint32_t cc, n;
  int32_t complexity, loss_rate, nb_avail_bytes, prefilter_period;
  int32_t prefilter_tapset_decision, enabled_in, hybrid_in;
  int32_t tell, total_bits;
  int16_t prefilter_gain, tf_estimate, tone_freq;
  int32_t toneishness;
  int32_t analysis_valid;
  float max_pitch_ratio;
  opus_val32 *pre0 = NULL, *pre1 = NULL;
  opus_val32 *pre[2];
  int max_period = COMBFILTER_MAXPERIOD;
  int min_period = COMBFILTER_MINPERIOD;
  int pitch_index;
  opus_val16 gain1;
  opus_val16 pf_threshold;
  int pf_on;
  int qg;
  int enabled, complexity_i;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, 4) || memcmp(magic, GPRI_MAGIC, 4) != 0) {
    fprintf(stderr, "bad input magic\n");
    return 1;
  }
  if (!read_u32(&version) || version != 1) {
    fprintf(stderr, "bad input version\n");
    return 1;
  }

  if (!read_u32(&cc) || !read_u32(&n)) return 1;
  if (!read_i32(&complexity) || !read_i32(&loss_rate)) return 1;
  if (!read_i32(&nb_avail_bytes) || !read_i32(&prefilter_period)) return 1;
  if (!read_i32(&prefilter_tapset_decision)) return 1;
  if (!read_i32(&enabled_in) || !read_i32(&hybrid_in)) return 1;
  if (!read_i32(&tell) || !read_i32(&total_bits)) return 1;
  if (!read_i16(&prefilter_gain) || !read_i16(&tf_estimate) || !read_i16(&tone_freq)) return 1;
  if (!read_i32(&toneishness)) return 1;
  if (!read_i32(&analysis_valid)) return 1;
  if (!read_f32(&max_pitch_ratio)) return 1;

  pre0 = read_i32_array((uint32_t)max_period + n);
  pre[0] = pre0;
  pre[1] = NULL;
  if (cc == 2) {
    pre1 = read_i32_array((uint32_t)max_period + n);
    pre[1] = pre1;
  }
  if (pre0 == NULL || (cc == 2 && pre1 == NULL)) { free(pre0); free(pre1); return 1; }

  enabled = (int)enabled_in;
  complexity_i = (int)complexity;

  /* --- Verbatim value-producing block from run_prefilter (FIXED_POINT). --- */
  if (enabled && toneishness > QCONST32(.99f, 29)) {
    int multiple = 1;
    if (tone_freq >= QCONST16(3.1416f, 13)) tone_freq = QCONST16(3.141593f, 13) - tone_freq;
    while (tone_freq >= multiple*QCONST16(0.39f, 13)) multiple++;
    if (tone_freq > QCONST16(0.006148f, 13)) {
      pitch_index = IMIN((51472*multiple+tone_freq/2)/tone_freq, COMBFILTER_MAXPERIOD-2);
    } else {
      pitch_index = COMBFILTER_MINPERIOD;
    }
    gain1 = QCONST16(.75f, 15);
  } else if (enabled && complexity_i >= 5) {
    opus_val16 *pitch_buf = (opus_val16 *)malloc((size_t)((max_period+(int)n)>>1) * sizeof(opus_val16));
    if (pitch_buf == NULL) { free(pre0); free(pre1); return 1; }
    pitch_downsample(pre, pitch_buf, (max_period+(int)n)>>1, (int)cc, 2, 0);
    pitch_search(pitch_buf+(max_period>>1), pitch_buf, (int)n,
          max_period-3*min_period, &pitch_index, 0);
    pitch_index = max_period-pitch_index;

    gain1 = remove_doubling(pitch_buf, max_period, min_period,
          (int)n, &pitch_index, prefilter_period, prefilter_gain, 0);
    if (pitch_index > max_period-2)
      pitch_index = max_period-2;
    gain1 = MULT16_16_Q15(QCONST16(.7f,15),gain1);
    if (loss_rate>2)
      gain1 = HALF32(gain1);
    if (loss_rate>4)
      gain1 = HALF32(gain1);
    if (loss_rate>8)
      gain1 = 0;
    free(pitch_buf);
  } else {
    gain1 = 0;
    pitch_index = COMBFILTER_MINPERIOD;
  }
  if (analysis_valid)
    gain1 = (opus_val16)(gain1 * max_pitch_ratio);

  pf_threshold = QCONST16(.2f,15);
  if (abs(pitch_index-prefilter_period)*10>pitch_index) {
    pf_threshold += QCONST16(.2f,15);
    if (tf_estimate > QCONST16(.98f, 14))
      gain1 = 0;
  }
  if (nb_avail_bytes<25)
    pf_threshold += QCONST16(.1f,15);
  if (nb_avail_bytes<35)
    pf_threshold += QCONST16(.1f,15);
  if (prefilter_gain > QCONST16(.4f,15))
    pf_threshold -= QCONST16(.1f,15);
  if (prefilter_gain > QCONST16(.55f,15))
    pf_threshold -= QCONST16(.1f,15);

  pf_threshold = MAX16(pf_threshold, QCONST16(.2f,15));
  if (gain1<pf_threshold) {
    gain1 = 0;
    pf_on = 0;
    qg = 0;
  } else {
    if (ABS16(gain1-prefilter_gain)<QCONST16(.1f,15))
      gain1=prefilter_gain;
    qg = ((gain1+1536)>>10)/3-1;
    qg = IMAX(0, IMIN(7, qg));
    gain1 = QCONST16(0.09375f,15)*(qg+1);
    pf_on = 1;
  }
  /* --- end verbatim block --- */

  free(pre0);
  free(pre1);

  /* Bitstream emission: drive a real ec_enc exactly as celt_encoder.c does
   * after run_prefilter (the pf_on branch). */
  {
    unsigned char scratch[2048];
    unsigned char *ec_buf = NULL;
    ec_enc enc;
    int prefilter_tapset = (int)prefilter_tapset_decision;
    int nbytes;
    int pass;
    /* Pass 0 measures the emitted bit count on a scratch buffer; pass 1 emits
     * into a buffer sized exactly to nbytes so the finalised range/raw layout
     * has no internal gap and matches the gopus Encoder.Done() packing. */
    for (pass = 0; pass < 2; pass++) {
      unsigned char *buf = (pass == 0) ? scratch : ec_buf;
      opus_uint32 size = (pass == 0) ? (opus_uint32)sizeof(scratch) : (opus_uint32)nbytes;
      int pi = pitch_index;
      ec_enc_init(&enc, buf, size);
      if (pf_on==0) {
        if(!hybrid_in && tell+16<=total_bits)
          ec_enc_bit_logp(&enc, 0, 1);
      } else {
        int octave;
        ec_enc_bit_logp(&enc, 1, 1);
        pi += 1;
        octave = EC_ILOG(pi)-5;
        ec_enc_uint(&enc, octave, 6);
        ec_enc_bits(&enc, pi-(16<<octave), 4+octave);
        pi -= 1;
        ec_enc_bits(&enc, qg, 3);
        ec_enc_icdf(&enc, prefilter_tapset, tapset_icdf, 2);
      }
      if (pass == 0) {
        nbytes = (ec_tell(&enc)+7)>>3;
        if (nbytes < 0) nbytes = 0;
        ec_buf = (unsigned char *)calloc((size_t)(nbytes == 0 ? 1 : nbytes), 1);
        if (ec_buf == NULL) return 1;
      } else {
        ec_enc_done(&enc);
      }
    }

    if (!write_exact(GPRO_MAGIC, 4)) { free(ec_buf); return 1; }
    if (!write_u32(1)) { free(ec_buf); return 1; }
    if (!write_i32(pitch_index)) { free(ec_buf); return 1; }
    if (!write_i32((int32_t)gain1)) { free(ec_buf); return 1; }
    if (!write_i32(qg)) { free(ec_buf); return 1; }
    if (!write_i32(pf_on)) { free(ec_buf); return 1; }
    if (!write_i32(prefilter_tapset)) { free(ec_buf); return 1; }
    if (!write_u32((uint32_t)nbytes)) { free(ec_buf); return 1; }
    if (!write_exact(ec_buf, (size_t)nbytes)) { free(ec_buf); return 1; }
    free(ec_buf);
  }
  fflush(stdout);
  return 0;
}
