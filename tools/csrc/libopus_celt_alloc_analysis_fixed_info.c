/* Fixed-point CELT encode-side allocation-decision analysis kernel oracle.
 *
 * Built against the --enable-fixed-point libopus reference (config.h defines
 * FIXED_POINT, QEXT off). The analysed kernels (l1_metric, tf_analysis,
 * tf_encode, alloc_trim_analysis) are file-static in celt/celt_encoder.c, so
 * their FIXED_POINT bodies are reproduced verbatim here; linking the fixed
 * libopus.a supplies the helper symbols (haar1, celt_inner_prod_norm_shift) and
 * the macro/inline definitions (celt_log2, fixed_generic.h) via the headers.
 *
 * Wire protocol: little-endian, magic "GAAI"/"GAAO", version 1.
 *   MODE_TF_ANALYSIS = 0 : tf_analysis -> tf_select, tf_res[len]
 *   MODE_TF_ENCODE   = 1 : tf_encode   -> coded buffer + rewritten tf_res[end]
 *   MODE_ALLOC_TRIM  = 2 : alloc_trim_analysis -> trim_index, stereo_saving
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
#include "entenc.h"
#include "entcode.h"
#include "bands.h"
#include "vq.h"
#include "celt.h"

#define INPUT_MAGIC "GAAI"
#define OUTPUT_MAGIC "GAAO"

enum {
  MODE_TF_ANALYSIS = 0,
  MODE_TF_ENCODE = 1,
  MODE_ALLOC_TRIM = 2
};

/* tf_select_table is exported by celt/celt.c (extern in celt.h). */

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
static int write_i32(int32_t v) { return write_u32((uint32_t)v); }
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

/* ---- Verbatim FIXED_POINT static bodies from celt/celt_encoder.c ---- */

static opus_val32 l1_metric(const celt_norm *tmp, int N, int LM, opus_val16 bias)
{
   int i;
   opus_val32 L1;
   L1 = 0;
   for (i=0;i<N;i++)
      L1 += EXTEND32(ABS16(SHR32(tmp[i], NORM_SHIFT-14)));
   L1 = MAC16_32_Q15(L1, LM*bias, L1);
   return L1;
}

static int tf_analysis(const CELTMode *m, int len, int isTransient,
      int *tf_res, int lambda, celt_norm *X, int N0, int LM,
      opus_val16 tf_estimate, int tf_chan, int *importance)
{
   int i;
   VARDECL(int, metric);
   int cost0;
   int cost1;
   VARDECL(int, path0);
   VARDECL(int, path1);
   VARDECL(celt_norm, tmp);
   VARDECL(celt_norm, tmp_1);
   int sel;
   int selcost[2];
   int tf_select=0;
   opus_val16 bias;

   SAVE_STACK;
   bias = MULT16_16_Q14(QCONST16(.04f,15), MAX16(-QCONST16(.25f,14), QCONST16(.5f,14)-tf_estimate));

   ALLOC(metric, len, int);
   ALLOC(tmp, (m->eBands[len]-m->eBands[len-1])<<LM, celt_norm);
   ALLOC(tmp_1, (m->eBands[len]-m->eBands[len-1])<<LM, celt_norm);
   ALLOC(path0, len, int);
   ALLOC(path1, len, int);

   for (i=0;i<len;i++)
   {
      int k, N;
      int narrow;
      opus_val32 L1, best_L1;
      int best_level=0;
      N = (m->eBands[i+1]-m->eBands[i])<<LM;
      narrow = (m->eBands[i+1]-m->eBands[i])==1;
      OPUS_COPY(tmp, &X[tf_chan*N0 + (m->eBands[i]<<LM)], N);
      L1 = l1_metric(tmp, N, isTransient ? LM : 0, bias);
      best_L1 = L1;
      if (isTransient && !narrow)
      {
         OPUS_COPY(tmp_1, tmp, N);
         haar1(tmp_1, N>>LM, 1<<LM);
         L1 = l1_metric(tmp_1, N, LM+1, bias);
         if (L1<best_L1)
         {
            best_L1 = L1;
            best_level = -1;
         }
      }
      for (k=0;k<LM+!(isTransient||narrow);k++)
      {
         int B;
         if (isTransient)
            B = (LM-k-1);
         else
            B = k+1;
         haar1(tmp, N>>k, 1<<k);
         L1 = l1_metric(tmp, N, B, bias);
         if (L1 < best_L1)
         {
            best_L1 = L1;
            best_level = k+1;
         }
      }
      if (isTransient)
         metric[i] = 2*best_level;
      else
         metric[i] = -2*best_level;
      if (narrow && (metric[i]==0 || metric[i]==-2*LM))
         metric[i]-=1;
   }
   tf_select = 0;
   for (sel=0;sel<2;sel++)
   {
      cost0 = importance[0]*abs(metric[0]-2*tf_select_table[LM][4*isTransient+2*sel+0]);
      cost1 = importance[0]*abs(metric[0]-2*tf_select_table[LM][4*isTransient+2*sel+1]) + (isTransient ? 0 : lambda);
      for (i=1;i<len;i++)
      {
         int curr0, curr1;
         curr0 = IMIN(cost0, cost1 + lambda);
         curr1 = IMIN(cost0 + lambda, cost1);
         cost0 = curr0 + importance[i]*abs(metric[i]-2*tf_select_table[LM][4*isTransient+2*sel+0]);
         cost1 = curr1 + importance[i]*abs(metric[i]-2*tf_select_table[LM][4*isTransient+2*sel+1]);
      }
      cost0 = IMIN(cost0, cost1);
      selcost[sel]=cost0;
   }
   if (selcost[1]<selcost[0] && isTransient)
      tf_select=1;
   cost0 = importance[0]*abs(metric[0]-2*tf_select_table[LM][4*isTransient+2*tf_select+0]);
   cost1 = importance[0]*abs(metric[0]-2*tf_select_table[LM][4*isTransient+2*tf_select+1]) + (isTransient ? 0 : lambda);
   for (i=1;i<len;i++)
   {
      int curr0, curr1;
      int from0, from1;
      from0 = cost0;
      from1 = cost1 + lambda;
      if (from0 < from1)
      {
         curr0 = from0;
         path0[i]= 0;
      } else {
         curr0 = from1;
         path0[i]= 1;
      }
      from0 = cost0 + lambda;
      from1 = cost1;
      if (from0 < from1)
      {
         curr1 = from0;
         path1[i]= 0;
      } else {
         curr1 = from1;
         path1[i]= 1;
      }
      cost0 = curr0 + importance[i]*abs(metric[i]-2*tf_select_table[LM][4*isTransient+2*tf_select+0]);
      cost1 = curr1 + importance[i]*abs(metric[i]-2*tf_select_table[LM][4*isTransient+2*tf_select+1]);
   }
   tf_res[len-1] = cost0 < cost1 ? 0 : 1;
   for (i=len-2;i>=0;i--)
   {
      if (tf_res[i+1] == 1)
         tf_res[i] = path1[i+1];
      else
         tf_res[i] = path0[i+1];
   }
   RESTORE_STACK;
   return tf_select;
}

static void tf_encode(int start, int end, int isTransient, int *tf_res, int LM, int tf_select, ec_enc *enc)
{
   int curr, i;
   int tf_select_rsv;
   int tf_changed;
   int logp;
   opus_uint32 budget;
   opus_uint32 tell;
   budget = enc->storage*8;
   tell = ec_tell(enc);
   logp = isTransient ? 2 : 4;
   tf_select_rsv = LM>0 && tell+logp+1 <= budget;
   budget -= tf_select_rsv;
   curr = tf_changed = 0;
   for (i=start;i<end;i++)
   {
      if (tell+logp<=budget)
      {
         ec_enc_bit_logp(enc, tf_res[i] ^ curr, logp);
         tell = ec_tell(enc);
         curr = tf_res[i];
         tf_changed |= curr;
      }
      else
         tf_res[i] = curr;
      logp = isTransient ? 4 : 5;
   }
   if (tf_select_rsv &&
         tf_select_table[LM][4*isTransient+0+tf_changed]!=
         tf_select_table[LM][4*isTransient+2+tf_changed])
      ec_enc_bit_logp(enc, tf_select, 1);
   else
      tf_select = 0;
   for (i=start;i<end;i++)
      tf_res[i] = tf_select_table[LM][4*isTransient+2*tf_select+tf_res[i]];
}

static int alloc_trim_analysis(const CELTMode *m, const celt_norm *X,
      const celt_glog *bandLogE, int end, int LM, int C, int N0,
      AnalysisInfo *analysis, opus_val16 *stereo_saving, opus_val16 tf_estimate,
      int intensity, celt_glog surround_trim, opus_int32 equiv_rate, int arch)
{
   int i;
   opus_val32 diff=0;
   int c;
   int trim_index;
   opus_val16 trim = QCONST16(5.f, 8);
   opus_val16 logXC, logXC2;
   if (equiv_rate < 64000) {
      trim = QCONST16(4.f, 8);
   } else if (equiv_rate < 80000) {
      opus_int32 frac = (equiv_rate-64000) >> 10;
      trim = QCONST16(4.f, 8) + QCONST16(1.f/16.f, 8)*frac;
   }
   if (C==2)
   {
      opus_val16 sum = 0;
      opus_val16 minXC;
      for (i=0;i<8;i++)
      {
         opus_val32 partial;
         partial = celt_inner_prod_norm_shift(&X[m->eBands[i]<<LM], &X[N0+(m->eBands[i]<<LM)],
               (m->eBands[i+1]-m->eBands[i])<<LM, arch);
         sum = ADD16(sum, EXTRACT16(SHR32(partial, 18)));
      }
      sum = MULT16_16_Q15(QCONST16(1.f/8, 15), sum);
      sum = MIN16(QCONST16(1.f, 10), ABS16(sum));
      minXC = sum;
      for (i=8;i<intensity;i++)
      {
         opus_val32 partial;
         partial = celt_inner_prod_norm_shift(&X[m->eBands[i]<<LM], &X[N0+(m->eBands[i]<<LM)],
               (m->eBands[i+1]-m->eBands[i])<<LM, arch);
         minXC = MIN16(minXC, ABS16(EXTRACT16(SHR32(partial, 18))));
      }
      minXC = MIN16(QCONST16(1.f, 10), ABS16(minXC));
      logXC = celt_log2(QCONST32(1.001f, 20)-MULT16_16(sum, sum));
      logXC2 = MAX16(HALF16(logXC), celt_log2(QCONST32(1.001f, 20)-MULT16_16(minXC, minXC)));
#ifdef FIXED_POINT
      logXC = PSHR32(logXC-QCONST16(6.f, 10),10-8);
      logXC2 = PSHR32(logXC2-QCONST16(6.f, 10),10-8);
#endif
      trim += MAX16(-QCONST16(4.f, 8), MULT16_16_Q15(QCONST16(.75f,15),logXC));
      *stereo_saving = MIN16(*stereo_saving + QCONST16(0.25f, 8), -HALF16(logXC2));
   }
   c=0; do {
      for (i=0;i<end-1;i++)
      {
         diff += SHR32(bandLogE[i+c*m->nbEBands], 5)*(opus_int32)(2+2*i-end);
      }
   } while (++c<C);
   diff /= C*(end-1);
   trim -= MAX32(-QCONST16(2.f, 8), MIN32(QCONST16(2.f, 8), SHR32(diff+QCONST32(1.f, DB_SHIFT-5),DB_SHIFT-13)/6 ));
   trim -= SHR16(surround_trim, DB_SHIFT-8);
   trim -= 2*SHR16(tf_estimate, 14-8);
#ifndef DISABLE_FLOAT_API
   if (analysis->valid)
   {
      trim -= MAX16(-QCONST16(2.f, 8), MIN16(QCONST16(2.f, 8),
            (opus_val16)(QCONST16(2.f, 8)*(analysis->tonality_slope+.05f))));
   }
#else
   (void)analysis;
#endif
#ifdef FIXED_POINT
   trim_index = PSHR32(trim, 8);
#else
   trim_index = (int)floor(.5f+trim);
#endif
   trim_index = IMAX(0, IMIN(10, trim_index));
   return trim_index;
}

/* ---- Drivers ---- */

/* A minimal mode carrying just the eBands table and nbEBands the kernels read. */
static CELTMode g_mode;
static opus_int16 g_ebands[64];

static int run_tf_analysis(void) {
  uint32_t len_u, nbEBands_u, isTransient_u, lambda_u, N0_u, LM_u, tfChan_u;
  int32_t tfEstimate_i;
  int len, nbEBands, isTransient, lambda, N0, LM, tfChan, i;
  celt_norm *X = NULL;
  int *importance = NULL, *tf_res = NULL;
  int tf_select;
  uint32_t Xlen;

  if (!read_u32(&nbEBands_u) || !read_u32(&len_u) || !read_u32(&isTransient_u) ||
      !read_i32(&lambda_u) || !read_u32(&N0_u) || !read_u32(&LM_u) ||
      !read_i32(&tfEstimate_i) || !read_u32(&tfChan_u))
    return 0;
  nbEBands = (int)nbEBands_u; len = (int)len_u; isTransient = (int)isTransient_u;
  lambda = (int)(int32_t)lambda_u; N0 = (int)N0_u; LM = (int)LM_u; tfChan = (int)tfChan_u;
  if (nbEBands <= 0 || nbEBands+1 > 64) return 0;

  for (i=0;i<nbEBands+1;i++) {
    int32_t v;
    if (!read_i32(&v)) return 0;
    g_ebands[i] = (opus_int16)v;
  }
  if (!read_u32(&Xlen)) return 0;
  X = (celt_norm *)malloc((size_t)Xlen * sizeof(celt_norm));
  if (!X) return 0;
  for (i=0;i<(int)Xlen;i++) {
    int32_t v;
    if (!read_i32(&v)) { free(X); return 0; }
    X[i] = (celt_norm)v;
  }
  importance = (int *)malloc((size_t)len * sizeof(int));
  tf_res = (int *)malloc((size_t)len * sizeof(int));
  if (!importance || !tf_res) { free(X); free(importance); free(tf_res); return 0; }
  for (i=0;i<len;i++) {
    int32_t v;
    if (!read_i32(&v)) { free(X); free(importance); free(tf_res); return 0; }
    importance[i] = (int)v;
  }

  memset(&g_mode, 0, sizeof(g_mode));
  g_mode.nbEBands = nbEBands;
  g_mode.eBands = g_ebands;

  tf_select = tf_analysis(&g_mode, len, isTransient, tf_res, lambda, X, N0, LM,
                          (opus_val16)tfEstimate_i, tfChan, importance);

  free(X); free(importance);
  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(MODE_TF_ANALYSIS)) { free(tf_res); return 0; }
  if (!write_i32(tf_select)) { free(tf_res); return 0; }
  if (!write_u32((uint32_t)len)) { free(tf_res); return 0; }
  for (i=0;i<len;i++)
    if (!write_i32(tf_res[i])) { free(tf_res); return 0; }
  free(tf_res);
  return 1;
}

static int run_tf_encode(void) {
  uint32_t start_u, end_u, isTransient_u, LM_u, tfSelect_u, bufSize_u, preBits_u;
  int start, end, isTransient, LM, tfSelect, bufSize, preBits, i;
  int *tf_res = NULL;
  unsigned char *buf = NULL;
  ec_enc enc;

  if (!read_u32(&start_u) || !read_u32(&end_u) || !read_u32(&isTransient_u) ||
      !read_u32(&LM_u) || !read_u32(&tfSelect_u) || !read_u32(&bufSize_u) ||
      !read_u32(&preBits_u))
    return 0;
  start = (int)start_u; end = (int)end_u; isTransient = (int)isTransient_u;
  LM = (int)LM_u; tfSelect = (int)tfSelect_u; bufSize = (int)bufSize_u; preBits = (int)preBits_u;
  if (bufSize <= 0 || end <= 0) return 0;

  tf_res = (int *)malloc((size_t)end * sizeof(int));
  buf = (unsigned char *)malloc((size_t)bufSize);
  if (!tf_res || !buf) { free(tf_res); free(buf); return 0; }
  for (i=0;i<end;i++) {
    int32_t v;
    if (!read_i32(&v)) { free(tf_res); free(buf); return 0; }
    tf_res[i] = (int)v;
  }

  ec_enc_init(&enc, buf, (opus_uint32)bufSize);
  for (i=0;i<preBits;i++)
    ec_enc_bit_logp(&enc, 0, 1);

  tf_encode(start, end, isTransient, tf_res, LM, tfSelect, &enc);
  ec_enc_done(&enc);

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(MODE_TF_ENCODE)) { free(tf_res); free(buf); return 0; }
  if (!write_u32((uint32_t)bufSize)) { free(tf_res); free(buf); return 0; }
  if (!write_exact(buf, (size_t)bufSize)) { free(tf_res); free(buf); return 0; }
  if (!write_u32((uint32_t)end)) { free(tf_res); free(buf); return 0; }
  for (i=0;i<end;i++)
    if (!write_i32(tf_res[i])) { free(tf_res); free(buf); return 0; }
  free(tf_res); free(buf);
  return 1;
}

static int run_alloc_trim(void) {
  uint32_t nbEBands_u, end_u, LM_u, C_u, N0_u, intensity_u, valid_u;
  int32_t stereoSaving_i, tfEstimate_i, surroundTrim_i, equivRate_i;
  float tonalitySlope;
  int nbEBands, end, LM, C, N0, intensity, valid, i;
  celt_norm *X = NULL;
  celt_glog *bandLogE = NULL;
  uint32_t Xlen, blen;
  opus_val16 stereo_saving;
  int trim_index;
  AnalysisInfo analysis;

  if (!read_u32(&nbEBands_u) || !read_u32(&end_u) || !read_u32(&LM_u) ||
      !read_u32(&C_u) || !read_u32(&N0_u) || !read_i32(&stereoSaving_i) ||
      !read_i32(&tfEstimate_i) || !read_u32(&intensity_u) ||
      !read_i32(&surroundTrim_i) || !read_i32(&equivRate_i) || !read_u32(&valid_u))
    return 0;
  {
    uint32_t tb;
    if (!read_u32(&tb)) return 0;
    memcpy(&tonalitySlope, &tb, 4);
  }
  nbEBands = (int)nbEBands_u; end = (int)end_u; LM = (int)LM_u; C = (int)C_u;
  N0 = (int)N0_u; intensity = (int)intensity_u; valid = (int)valid_u;
  if (nbEBands <= 0 || nbEBands+1 > 64) return 0;

  for (i=0;i<nbEBands+1;i++) {
    int32_t v;
    if (!read_i32(&v)) return 0;
    g_ebands[i] = (opus_int16)v;
  }
  if (!read_u32(&Xlen)) return 0;
  X = (celt_norm *)malloc((size_t)Xlen * sizeof(celt_norm));
  if (!X) return 0;
  for (i=0;i<(int)Xlen;i++) {
    int32_t v;
    if (!read_i32(&v)) { free(X); return 0; }
    X[i] = (celt_norm)v;
  }
  if (!read_u32(&blen)) { free(X); return 0; }
  bandLogE = (celt_glog *)malloc((size_t)blen * sizeof(celt_glog));
  if (!bandLogE) { free(X); return 0; }
  for (i=0;i<(int)blen;i++) {
    int32_t v;
    if (!read_i32(&v)) { free(X); free(bandLogE); return 0; }
    bandLogE[i] = (celt_glog)v;
  }

  memset(&g_mode, 0, sizeof(g_mode));
  g_mode.nbEBands = nbEBands;
  g_mode.eBands = g_ebands;

  memset(&analysis, 0, sizeof(analysis));
  analysis.valid = valid;
  analysis.tonality_slope = tonalitySlope;

  stereo_saving = (opus_val16)stereoSaving_i;

  trim_index = alloc_trim_analysis(&g_mode, X, bandLogE, end, LM, C, N0,
                                   &analysis, &stereo_saving,
                                   (opus_val16)tfEstimate_i, intensity,
                                   (celt_glog)surroundTrim_i,
                                   (opus_int32)equivRate_i, 0);

  free(X); free(bandLogE);
  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(MODE_ALLOC_TRIM)) return 0;
  if (!write_i32(trim_index)) return 0;
  if (!write_i32((int32_t)stereo_saving)) return 0;
  return 1;
}

int main(void) {
  char magic[4];
  uint32_t version, mode;
  int ok;
  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, 4) || memcmp(magic, INPUT_MAGIC, 4) != 0) {
    fprintf(stderr, "bad input magic\n");
    return 1;
  }
  if (!read_u32(&version) || version != 1) {
    fprintf(stderr, "bad input version\n");
    return 1;
  }
  if (!read_u32(&mode)) {
    fprintf(stderr, "failed to read mode\n");
    return 1;
  }
  switch (mode) {
    case MODE_TF_ANALYSIS: ok = run_tf_analysis(); break;
    case MODE_TF_ENCODE: ok = run_tf_encode(); break;
    case MODE_ALLOC_TRIM: ok = run_alloc_trim(); break;
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
