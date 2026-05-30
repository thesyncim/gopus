/* Fixed-point CELT transient_analysis / patch_transient_decision kernel oracle.
 *
 * Built against the --enable-fixed-point libopus reference (config.h defines
 * FIXED_POINT, QEXT off). Both kernels are file-static in celt/celt_encoder.c
 * so their FIXED_POINT bodies are reproduced verbatim here; linking the fixed
 * libopus.a supplies the macro/helper inlines (celt_sqrt, celt_ilog2,
 * celt_maxabs16/32) via the headers.
 *
 *   transient_analysis(in, len, C, &tf_estimate, &tf_chan,
 *                      allow_weak_transients, &weak_transient,
 *                      tone_freq, toneishness)   -> is_transient
 *   patch_transient_decision(newE, oldE, nbEBands, start, end, C)
 *
 * Wire protocol: little-endian, magic "GTRI"/"GTRO", version 1.
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

#include "arch.h"
#include "mathops.h"
#include "stack_alloc.h"

#define GTRI_MAGIC "GTRI"
#define GTRO_MAGIC "GTRO"

enum {
  MODE_TRANSIENT = 0,
  MODE_PATCH = 1
};

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

static int read_i32(int32_t *out) {
  uint32_t v;
  if (!read_u32(&v)) return 0;
  *out = (int32_t)v;
  return 1;
}

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

/* Verbatim FIXED_POINT body of transient_analysis() from celt/celt_encoder.c
 * (libopus 1.6.1). Float branches removed; logic is otherwise unchanged. */
static int transient_analysis(const opus_val32 * OPUS_RESTRICT in, int len, int C,
                              opus_val16 *tf_estimate, int *tf_chan, int allow_weak_transients,
                              int *weak_transient, opus_val16 tone_freq, opus_val32 toneishness)
{
   int i;
   VARDECL(opus_val16, tmp);
   opus_val32 mem0,mem1;
   int is_transient = 0;
   opus_int32 mask_metric = 0;
   int c;
   opus_val16 tf_max;
   int len2;
   int forward_shift = 4;
   static const unsigned char inv_table[128] = {
         255,255,156,110, 86, 70, 59, 51, 45, 40, 37, 33, 31, 28, 26, 25,
          23, 22, 21, 20, 19, 18, 17, 16, 16, 15, 15, 14, 13, 13, 12, 12,
          12, 12, 11, 11, 11, 10, 10, 10,  9,  9,  9,  9,  9,  9,  8,  8,
           8,  8,  8,  7,  7,  7,  7,  7,  7,  6,  6,  6,  6,  6,  6,  6,
           6,  6,  6,  6,  6,  6,  6,  6,  6,  5,  5,  5,  5,  5,  5,  5,
           5,  5,  5,  5,  5,  4,  4,  4,  4,  4,  4,  4,  4,  4,  4,  4,
           4,  4,  4,  4,  4,  4,  4,  4,  4,  4,  4,  4,  4,  4,  3,  3,
           3,  3,  3,  3,  3,  3,  3,  3,  3,  3,  3,  3,  3,  3,  3,  2,
   };
   SAVE_STACK;
   int in_shift = IMAX(0, celt_ilog2(1+celt_maxabs32(in, C*len))-14);
   ALLOC(tmp, len, opus_val16);

   *weak_transient = 0;
   if (allow_weak_transients)
   {
      forward_shift = 5;
   }
   len2=len/2;
   for (c=0;c<C;c++)
   {
      opus_val32 mean;
      opus_int32 unmask=0;
      opus_val32 norm;
      opus_val16 maxE;
      mem0=0;
      mem1=0;
      for (i=0;i<len;i++)
      {
         opus_val32 x,y;
         x = SHR32(in[i+c*len],in_shift);
         y = ADD32(mem0, x);
         mem0 = mem1 + y - SHL32(x,1);
         mem1 = x - SHR32(y,1);
         tmp[i] = SROUND16(y, 2);
      }
      OPUS_CLEAR(tmp, 12);

      {
         int shift=0;
         shift = 14-celt_ilog2(MAX16(1, celt_maxabs16(tmp, len)));
         if (shift!=0)
         {
            for (i=0;i<len;i++)
               tmp[i] = SHL16(tmp[i], shift);
         }
      }

      mean=0;
      mem0=0;
      for (i=0;i<len2;i++)
      {
         opus_val32 x2 = PSHR32(MULT16_16(tmp[2*i],tmp[2*i]) + MULT16_16(tmp[2*i+1],tmp[2*i+1]),4);
         mean += PSHR32(x2, 12);
         mem0 = mem0 + PSHR32(x2-mem0,forward_shift);
         tmp[i] = PSHR32(mem0, 12);
      }

      mem0=0;
      maxE=0;
      for (i=len2-1;i>=0;i--)
      {
         mem0 = mem0 + PSHR32(SHL32(tmp[i],4)-mem0,3);
         tmp[i] = PSHR32(mem0, 4);
         maxE = MAX16(maxE, tmp[i]);
      }

      mean = MULT16_16(celt_sqrt(mean), celt_sqrt(MULT16_16(maxE,len2>>1)));
      norm = SHL32(EXTEND32(len2),6+14)/ADD32(EPSILON,SHR32(mean,1));
      unmask=0;
      celt_assert(!celt_isnan(tmp[0]));
      celt_assert(!celt_isnan(norm));
      for (i=12;i<len2-5;i+=4)
      {
         int id;
         id = MAX32(0,MIN32(127,MULT16_32_Q15(tmp[i]+EPSILON,norm)));
         unmask += inv_table[id];
      }
      unmask = 64*unmask*4/(6*(len2-17));
      if (unmask>mask_metric)
      {
         *tf_chan = c;
         mask_metric = unmask;
      }
   }
   is_transient = mask_metric>200;
   if (toneishness > QCONST32(.98f, 29) && tone_freq < QCONST16(0.026f, 13))
   {
      is_transient = 0;
      mask_metric = 0;
   }
   if (allow_weak_transients && is_transient && mask_metric<600) {
      is_transient = 0;
      *weak_transient = 1;
   }
   tf_max = MAX16(0,celt_sqrt(27*mask_metric)-42);
   *tf_estimate = celt_sqrt(MAX32(0, SHL32(MULT16_16(QCONST16(0.0069,14),MIN16(163,tf_max)),14)-QCONST32(0.139,28)));
   RESTORE_STACK;
   return is_transient;
}

/* Verbatim FIXED_POINT body of patch_transient_decision(). */
static int patch_transient_decision(celt_glog *newE, celt_glog *oldE, int nbEBands,
      int start, int end, int C)
{
   int i, c;
   opus_val32 mean_diff=0;
   celt_glog spread_old[26];
   if (C==1)
   {
      spread_old[start] = oldE[start];
      for (i=start+1;i<end;i++)
         spread_old[i] = MAXG(spread_old[i-1]-GCONST(1.0f), oldE[i]);
   } else {
      spread_old[start] = MAXG(oldE[start],oldE[start+nbEBands]);
      for (i=start+1;i<end;i++)
         spread_old[i] = MAXG(spread_old[i-1]-GCONST(1.0f),
                               MAXG(oldE[i],oldE[i+nbEBands]));
   }
   for (i=end-2;i>=start;i--)
      spread_old[i] = MAXG(spread_old[i], spread_old[i+1]-GCONST(1.0f));
   c=0; do {
      for (i=IMAX(2,start);i<end-1;i++)
      {
         opus_val16 x1, x2;
         x1 = MAXG(0, newE[i + c*nbEBands]);
         x2 = MAXG(0, spread_old[i]);
         mean_diff = ADD32(mean_diff, MAXG(0, SUB32(x1, x2)));
      }
   } while (++c<C);
   mean_diff = DIV32(mean_diff, C*(end-1-IMAX(2,start)));
   return mean_diff > GCONST(1.f);
}

static int run_transient(void) {
  uint32_t len_u, c_u, weak_u;
  int len, C, allow_weak;
  int32_t tone_freq_i, toneishness;
  opus_val32 *in = NULL;
  opus_val16 tf_estimate = 0;
  int tf_chan = 0;
  int weak_transient = 0;
  int is_transient;
  int32_t i;

  if (!read_u32(&len_u) || !read_u32(&c_u) || !read_u32(&weak_u)) return 0;
  if (!read_i32(&tone_freq_i) || !read_i32(&toneishness)) return 0;
  len = (int)len_u; C = (int)c_u; allow_weak = (int)weak_u;
  if (len <= 0 || C <= 0) return 0;

  in = (opus_val32 *)malloc((size_t)C * (size_t)len * sizeof(opus_val32));
  if (in == NULL) return 0;
  for (i = 0; i < (int32_t)((uint32_t)C * (uint32_t)len); i++) {
    int32_t v;
    if (!read_i32(&v)) { free(in); return 0; }
    in[i] = (opus_val32)v;
  }

  is_transient = transient_analysis(in, len, C, &tf_estimate, &tf_chan,
                                    allow_weak, &weak_transient,
                                    (opus_val16)tone_freq_i, (opus_val32)toneishness);

  free(in);
  if (!write_u32(MODE_TRANSIENT)) return 0;
  if (!write_i32(is_transient)) return 0;
  if (!write_i32((int32_t)tf_estimate)) return 0;
  if (!write_i32((int32_t)tf_chan)) return 0;
  if (!write_i32((int32_t)weak_transient)) return 0;
  return 1;
}

static int run_patch(void) {
  uint32_t nbEBands_u, start_u, end_u, c_u;
  int nbEBands, start, end, C;
  celt_glog *newE = NULL, *oldE = NULL;
  uint32_t total;
  int32_t i;
  int decision;

  if (!read_u32(&nbEBands_u) || !read_u32(&start_u) ||
      !read_u32(&end_u) || !read_u32(&c_u))
    return 0;
  nbEBands = (int)nbEBands_u; start = (int)start_u; end = (int)end_u; C = (int)c_u;

  total = 2u * (uint32_t)nbEBands;
  newE = (celt_glog *)malloc((size_t)total * sizeof(celt_glog));
  oldE = (celt_glog *)malloc((size_t)total * sizeof(celt_glog));
  if (!newE || !oldE) { free(newE); free(oldE); return 0; }
  for (i = 0; i < (int32_t)total; i++) {
    int32_t v;
    if (!read_i32(&v)) { free(newE); free(oldE); return 0; }
    newE[i] = (celt_glog)v;
  }
  for (i = 0; i < (int32_t)total; i++) {
    int32_t v;
    if (!read_i32(&v)) { free(newE); free(oldE); return 0; }
    oldE[i] = (celt_glog)v;
  }

  decision = patch_transient_decision(newE, oldE, nbEBands, start, end, C);

  free(newE); free(oldE);
  if (!write_u32(MODE_PATCH)) return 0;
  if (!write_i32(decision)) return 0;
  return 1;
}

int main(void) {
  char magic[4];
  uint32_t version;
  uint32_t mode;
  int ok;
  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, 4) || memcmp(magic, GTRI_MAGIC, 4) != 0) {
    fprintf(stderr, "bad input magic\n");
    return 1;
  }
  if (!read_u32(&version) || version != 1) {
    fprintf(stderr, "bad input version\n");
    return 1;
  }
  if (!write_exact(GTRO_MAGIC, 4)) return 1;
  if (!write_u32(1)) return 1;
  if (!read_u32(&mode)) {
    fprintf(stderr, "failed to read mode\n");
    return 1;
  }
  switch (mode) {
    case MODE_TRANSIENT: ok = run_transient(); break;
    case MODE_PATCH: ok = run_patch(); break;
    default:
      fprintf(stderr, "unknown mode %u\n", mode);
      return 1;
  }
  if (!ok) {
    fprintf(stderr, "mode %u failed\n", mode);
    return 1;
  }
  fflush(stdout);
  return 0;
}
