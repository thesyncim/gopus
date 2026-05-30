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

/* Oracle helper for the libopus FIXED_POINT celt/celt_decoder.c deemphasis
 * (and the deemphasis_stereo_simple shortcut). Built against the
 * --enable-fixed-point reference tree so config.h defines FIXED_POINT and the
 * macros (SIG2RES, ADD_RES, SATURATE, MULT16_32_Q15, SIG_SAT, VERY_SMALL)
 * resolve to their integer ENABLE_RES24 forms.
 *
 * The deemphasis function is static (RESYNTH undefined) so it cannot be
 * linked from libopus.a. The body below is reproduced verbatim from
 * celt/celt_decoder.c; linking the fixed libopus.a only provides SAT16 and
 * the math macro definitions via the headers. */

#define INPUT_MAGIC "GDPI"
#define OUTPUT_MAGIC "GDPO"

/* Verbatim copy of celt/celt_decoder.c deemphasis_stereo_simple for the
 * !CUSTOM_MODES && !ENABLE_OPUS_CUSTOM_API && !ENABLE_QEXT build. */
static void deemphasis_stereo_simple(celt_sig *in[], opus_res *pcm, int N, const opus_val16 coef0,
      celt_sig *mem)
{
   celt_sig * OPUS_RESTRICT x0;
   celt_sig * OPUS_RESTRICT x1;
   celt_sig m0, m1;
   int j;
   x0=in[0];
   x1=in[1];
   m0 = mem[0];
   m1 = mem[1];
   for (j=0;j<N;j++)
   {
      celt_sig tmp0, tmp1;
      /* Add VERY_SMALL to x[] first to reduce dependency chain. */
      tmp0 = SATURATE(x0[j] + VERY_SMALL + m0, SIG_SAT);
      tmp1 = SATURATE(x1[j] + VERY_SMALL + m1, SIG_SAT);
      m0 = MULT16_32_Q15(coef0, tmp0);
      m1 = MULT16_32_Q15(coef0, tmp1);
      pcm[2*j  ] = SIG2RES(tmp0);
      pcm[2*j+1] = SIG2RES(tmp1);
   }
   mem[0] = m0;
   mem[1] = m1;
}

/* Verbatim copy of celt/celt_decoder.c deemphasis for this build config:
 * FIXED_POINT, ENABLE_RES24, and none of CUSTOM_MODES / ENABLE_OPUS_CUSTOM_API
 * / ENABLE_QEXT. The coef[1]!=0 custom-modes branch is therefore compiled
 * out, matching the reference. */
static void deemphasis(celt_sig *in[], opus_res *pcm, int N, int C, int downsample, const opus_val16 *coef,
      celt_sig *mem, int accum)
{
   int c;
   int Nd;
   int apply_downsampling=0;
   opus_val16 coef0;
   celt_sig *scratch;
   /* Short version for common case. */
   if (downsample == 1 && C == 2 && !accum)
   {
      deemphasis_stereo_simple(in, pcm, N, coef[0], mem);
      return;
   }
   scratch = (celt_sig *)malloc((N ? N : 1) * sizeof(celt_sig));
   coef0 = coef[0];
   Nd = N/downsample;
   c=0; do {
      int j;
      celt_sig * OPUS_RESTRICT x;
      opus_res  * OPUS_RESTRICT y;
      celt_sig m = mem[c];
      x =in[c];
      y = pcm+c;
      if (downsample>1)
      {
         /* Shortcut for the standard (non-custom modes) case */
         for (j=0;j<N;j++)
         {
            celt_sig tmp = SATURATE(x[j] + VERY_SMALL + m, SIG_SAT);
            m = MULT16_32_Q15(coef0, tmp);
            scratch[j] = tmp;
         }
         apply_downsampling=1;
      } else {
         /* Shortcut for the standard (non-custom modes) case */
         if (accum)
         {
            for (j=0;j<N;j++)
            {
               celt_sig tmp = SATURATE(x[j] + m + VERY_SMALL, SIG_SAT);
               m = MULT16_32_Q15(coef0, tmp);
               y[j*C] = ADD_RES(y[j*C], SIG2RES(tmp));
            }
         } else
         {
            for (j=0;j<N;j++)
            {
               celt_sig tmp = SATURATE(x[j] + VERY_SMALL + m, SIG_SAT);
               m = MULT16_32_Q15(coef0, tmp);
               y[j*C] = SIG2RES(tmp);
            }
         }
      }
      mem[c] = m;

      if (apply_downsampling)
      {
         /* Perform down-sampling */
         if (accum)
         {
            for (j=0;j<Nd;j++)
               y[j*C] = ADD_RES(y[j*C], SIG2RES(scratch[j*downsample]));
         } else
         {
            for (j=0;j<Nd;j++)
               y[j*C] = SIG2RES(scratch[j*downsample]);
         }
      }
   } while (++c<C);
   free(scratch);
}

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

/* Wire format (after the GDPI header and version word):
 *   u32 N, u32 C, u32 downsample, u32 accum
 *   2 x i32 coef            (coef[0], coef[1]; coef[2],coef[3] unused here, but
 *                            we send all four to mirror mode->preemph layout)
 *   2 more i32 coef (coef[2], coef[3])
 *   C x N i32 in            (per-channel celt_sig input, channel-major)
 *   C x i32 mem             (input filter state)
 *   Nd*C x i32 pcm_in       (pre-existing pcm buffer, used when accum)
 * Output (after the GDPO header, version 1):
 *   u32 count = Nd*C
 *   count x i32 pcm         (opus_res output, interleaved stride C)
 *   u32 memcount = C
 *   C x i32 mem             (updated filter state)
 */
static int eval_deemphasis(void) {
  uint32_t N, C, downsample, accum;
  opus_val16 coef[4];
  celt_sig *inbuf[2] = {NULL, NULL};
  celt_sig mem[2] = {0, 0};
  opus_res *pcm = NULL;
  uint32_t i, c, Nd, pcmlen;
  int ok = 0;

  if (!read_u32(&N) || !read_u32(&C) || !read_u32(&downsample) || !read_u32(&accum)) {
    return 0;
  }
  if (C < 1 || C > 2 || downsample < 1) return 0;
  Nd = N / downsample;
  pcmlen = Nd * C;

  for (i = 0; i < 4; i++) {
    uint32_t v;
    if (!read_u32(&v)) goto done;
    coef[i] = (opus_val16)(int32_t)v;
  }

  for (c = 0; c < C; c++) {
    inbuf[c] = (celt_sig *)malloc((N ? N : 1) * sizeof(celt_sig));
    if (!inbuf[c]) goto done;
    for (i = 0; i < N; i++) {
      uint32_t v;
      if (!read_u32(&v)) goto done;
      inbuf[c][i] = (celt_sig)(int32_t)v;
    }
  }

  for (c = 0; c < C; c++) {
    uint32_t v;
    if (!read_u32(&v)) goto done;
    mem[c] = (celt_sig)(int32_t)v;
  }

  pcm = (opus_res *)malloc((pcmlen ? pcmlen : 1) * sizeof(opus_res));
  if (!pcm) goto done;
  for (i = 0; i < pcmlen; i++) {
    uint32_t v;
    if (!read_u32(&v)) goto done;
    pcm[i] = (opus_res)(int32_t)v;
  }

  deemphasis(inbuf, pcm, (int)N, (int)C, (int)downsample, coef, mem, (int)accum);

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(pcmlen)) {
    goto done;
  }
  for (i = 0; i < pcmlen; i++) {
    if (!write_u32((uint32_t)(int32_t)pcm[i])) goto done;
  }
  if (!write_u32(C)) goto done;
  for (c = 0; c < C; c++) {
    if (!write_u32((uint32_t)(int32_t)mem[c])) goto done;
  }
  ok = 1;

done:
  free(inbuf[0]);
  free(inbuf[1]);
  free(pcm);
  return ok;
}

int main(void) {
  char magic[4];
  uint32_t version;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) return 1;
  if (!read_u32(&version) || version != 1) return 1;

  return eval_deemphasis() ? 0 : 1;
}
