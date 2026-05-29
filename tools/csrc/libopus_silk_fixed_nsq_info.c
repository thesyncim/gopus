/* Oracle for the libopus FIXED_POINT silk_noise_shape_quantizer kernel.
 *
 * Must be compiled and linked against a libopus configured with
 * --enable-fixed-point (defines FIXED_POINT). The kernel is a file-static
 * OPUS_INLINE function in silk/NSQ.c, so it cannot be called through the
 * library symbol table. We reproduce its body verbatim here (compiled with the
 * fixed reference headers / macros) so the oracle is bit-exact with the
 * reference implementation.
 *
 * Reads a little-endian payload of cases from stdin and writes the kernel
 * outputs (pulses, xq, sLTP_Q15 window, and the mutated NSQ state) to stdout.
 *
 * The inner kernels are called through the scalar _c reference helpers
 * (silk_noise_shape_quantizer_short_prediction_c /
 * silk_NSQ_noise_shape_feedback_loop_c) rather than the silk/NSQ.h dispatch
 * macros. On arm64 those macros resolve to the NEON variants, which can differ
 * from the canonical C reference by 1 ULP; the Go port targets the scalar
 * reference (matching amd64/CI), so the oracle uses it too. */

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "config.h"

#ifndef FIXED_POINT
#error "this oracle requires a FIXED_POINT libopus build (--enable-fixed-point)"
#endif

#include "SigProc_FIX.h"
#include "define.h"
#include "structs.h"
#include "NSQ.h"

#define INPUT_MAGIC "GNQI"
#define OUTPUT_MAGIC "GNQO"

#if defined(ENABLE_ASSERTIONS) || defined(ENABLE_HARDENING)
void celt_fatal(const char *str, const char *file, int line) {
  (void)str;
  (void)file;
  (void)line;
  abort();
}
#endif

/* --- Verbatim copy of silk_noise_shape_quantizer from silk/NSQ.c ---------- */
static OPUS_INLINE void oracle_noise_shape_quantizer(
    silk_nsq_state      *NSQ,
    opus_int            signalType,
    const opus_int32    x_sc_Q10[],
    opus_int8           pulses[],
    opus_int16          xq[],
    opus_int32          sLTP_Q15[],
    const opus_int16    a_Q12[],
    const opus_int16    b_Q14[],
    const opus_int16    AR_shp_Q13[],
    opus_int            lag,
    opus_int32          HarmShapeFIRPacked_Q14,
    opus_int            Tilt_Q14,
    opus_int32          LF_shp_Q14,
    opus_int32          Gain_Q16,
    opus_int            Lambda_Q10,
    opus_int            offset_Q10,
    opus_int            length,
    opus_int            shapingLPCOrder,
    opus_int            predictLPCOrder,
    int                 arch
)
{
    opus_int     i;
    opus_int32   LTP_pred_Q13, LPC_pred_Q10, n_AR_Q12, n_LTP_Q13;
    opus_int32   n_LF_Q12, r_Q10, rr_Q10, q1_Q0, q1_Q10, q2_Q10, rd1_Q20, rd2_Q20;
    opus_int32   exc_Q14, LPC_exc_Q14, xq_Q14, Gain_Q10;
    opus_int32   tmp1, tmp2, sLF_AR_shp_Q14;
    opus_int32   *psLPC_Q14, *shp_lag_ptr, *pred_lag_ptr;

    (void)arch;
    shp_lag_ptr  = &NSQ->sLTP_shp_Q14[ NSQ->sLTP_shp_buf_idx - lag + HARM_SHAPE_FIR_TAPS / 2 ];
    pred_lag_ptr = &sLTP_Q15[ NSQ->sLTP_buf_idx - lag + LTP_ORDER / 2 ];
    Gain_Q10     = silk_RSHIFT( Gain_Q16, 6 );

    /* Set up short term AR state */
    psLPC_Q14 = &NSQ->sLPC_Q14[ NSQ_LPC_BUF_LENGTH - 1 ];

    for( i = 0; i < length; i++ ) {
        /* Generate dither */
        NSQ->rand_seed = silk_RAND( NSQ->rand_seed );

        /* Short-term prediction (scalar C reference) */
        LPC_pred_Q10 = silk_noise_shape_quantizer_short_prediction_c(psLPC_Q14, a_Q12, predictLPCOrder);

        /* Long-term prediction */
        if( signalType == TYPE_VOICED ) {
            /* Unrolled loop */
            /* Avoids introducing a bias because silk_SMLAWB() always rounds to -inf */
            LTP_pred_Q13 = 2;
            LTP_pred_Q13 = silk_SMLAWB( LTP_pred_Q13, pred_lag_ptr[  0 ], b_Q14[ 0 ] );
            LTP_pred_Q13 = silk_SMLAWB( LTP_pred_Q13, pred_lag_ptr[ -1 ], b_Q14[ 1 ] );
            LTP_pred_Q13 = silk_SMLAWB( LTP_pred_Q13, pred_lag_ptr[ -2 ], b_Q14[ 2 ] );
            LTP_pred_Q13 = silk_SMLAWB( LTP_pred_Q13, pred_lag_ptr[ -3 ], b_Q14[ 3 ] );
            LTP_pred_Q13 = silk_SMLAWB( LTP_pred_Q13, pred_lag_ptr[ -4 ], b_Q14[ 4 ] );
            pred_lag_ptr++;
        } else {
            LTP_pred_Q13 = 0;
        }

        /* Noise shape feedback */
        celt_assert( ( shapingLPCOrder & 1 ) == 0 );   /* check that order is even */
        n_AR_Q12 = silk_NSQ_noise_shape_feedback_loop_c(&NSQ->sDiff_shp_Q14, NSQ->sAR2_Q14, AR_shp_Q13, shapingLPCOrder);

        n_AR_Q12 = silk_SMLAWB( n_AR_Q12, NSQ->sLF_AR_shp_Q14, Tilt_Q14 );

        n_LF_Q12 = silk_SMULWB( NSQ->sLTP_shp_Q14[ NSQ->sLTP_shp_buf_idx - 1 ], LF_shp_Q14 );
        n_LF_Q12 = silk_SMLAWT( n_LF_Q12, NSQ->sLF_AR_shp_Q14, LF_shp_Q14 );

        celt_assert( lag > 0 || signalType != TYPE_VOICED );

        /* Combine prediction and noise shaping signals */
        tmp1 = silk_SUB32_ovflw( silk_LSHIFT32( LPC_pred_Q10, 2 ), n_AR_Q12 );  /* Q12 */
        tmp1 = silk_SUB32_ovflw( tmp1, n_LF_Q12 );                              /* Q12 */
        if( lag > 0 ) {
            /* Symmetric, packed FIR coefficients */
            n_LTP_Q13 = silk_SMULWB( silk_ADD_SAT32( shp_lag_ptr[ 0 ], shp_lag_ptr[ -2 ] ), HarmShapeFIRPacked_Q14 );
            n_LTP_Q13 = silk_SMLAWT( n_LTP_Q13, shp_lag_ptr[ -1 ],                      HarmShapeFIRPacked_Q14 );
            n_LTP_Q13 = silk_LSHIFT( n_LTP_Q13, 1 );
            shp_lag_ptr++;

            tmp2 = silk_SUB32( LTP_pred_Q13, n_LTP_Q13 );                       /* Q13 */
            tmp1 = silk_ADD32_ovflw( tmp2, silk_LSHIFT32( tmp1, 1 ) );          /* Q13 */
            tmp1 = silk_RSHIFT_ROUND( tmp1, 3 );                                /* Q10 */
        } else {
            tmp1 = silk_RSHIFT_ROUND( tmp1, 2 );                                /* Q10 */
        }

        r_Q10 = silk_SUB32( x_sc_Q10[ i ], tmp1 );                              /* residual error Q10 */

        /* Flip sign depending on dither */
        if( NSQ->rand_seed < 0 ) {
            r_Q10 = -r_Q10;
        }
        r_Q10 = silk_LIMIT_32( r_Q10, -(31 << 10), 30 << 10 );

        /* Find two quantization level candidates and measure their rate-distortion */
        q1_Q10 = silk_SUB32( r_Q10, offset_Q10 );
        q1_Q0 = silk_RSHIFT( q1_Q10, 10 );
        if (Lambda_Q10 > 2048) {
            /* For aggressive RDO, the bias becomes more than one pulse. */
            int rdo_offset = Lambda_Q10/2 - 512;
            if (q1_Q10 > rdo_offset) {
                q1_Q0 = silk_RSHIFT( q1_Q10 - rdo_offset, 10 );
            } else if (q1_Q10 < -rdo_offset) {
                q1_Q0 = silk_RSHIFT( q1_Q10 + rdo_offset, 10 );
            } else if (q1_Q10 < 0) {
                q1_Q0 = -1;
            } else {
                q1_Q0 = 0;
            }
        }
        if( q1_Q0 > 0 ) {
            q1_Q10  = silk_SUB32( silk_LSHIFT( q1_Q0, 10 ), QUANT_LEVEL_ADJUST_Q10 );
            q1_Q10  = silk_ADD32( q1_Q10, offset_Q10 );
            q2_Q10  = silk_ADD32( q1_Q10, 1024 );
            rd1_Q20 = silk_SMULBB( q1_Q10, Lambda_Q10 );
            rd2_Q20 = silk_SMULBB( q2_Q10, Lambda_Q10 );
        } else if( q1_Q0 == 0 ) {
            q1_Q10  = offset_Q10;
            q2_Q10  = silk_ADD32( q1_Q10, 1024 - QUANT_LEVEL_ADJUST_Q10 );
            rd1_Q20 = silk_SMULBB( q1_Q10, Lambda_Q10 );
            rd2_Q20 = silk_SMULBB( q2_Q10, Lambda_Q10 );
        } else if( q1_Q0 == -1 ) {
            q2_Q10  = offset_Q10;
            q1_Q10  = silk_SUB32( q2_Q10, 1024 - QUANT_LEVEL_ADJUST_Q10 );
            rd1_Q20 = silk_SMULBB( -q1_Q10, Lambda_Q10 );
            rd2_Q20 = silk_SMULBB(  q2_Q10, Lambda_Q10 );
        } else {            /* Q1_Q0 < -1 */
            q1_Q10  = silk_ADD32( silk_LSHIFT( q1_Q0, 10 ), QUANT_LEVEL_ADJUST_Q10 );
            q1_Q10  = silk_ADD32( q1_Q10, offset_Q10 );
            q2_Q10  = silk_ADD32( q1_Q10, 1024 );
            rd1_Q20 = silk_SMULBB( -q1_Q10, Lambda_Q10 );
            rd2_Q20 = silk_SMULBB( -q2_Q10, Lambda_Q10 );
        }
        rr_Q10  = silk_SUB32( r_Q10, q1_Q10 );
        rd1_Q20 = silk_SMLABB( rd1_Q20, rr_Q10, rr_Q10 );
        rr_Q10  = silk_SUB32( r_Q10, q2_Q10 );
        rd2_Q20 = silk_SMLABB( rd2_Q20, rr_Q10, rr_Q10 );

        if( rd2_Q20 < rd1_Q20 ) {
            q1_Q10 = q2_Q10;
        }

        pulses[ i ] = (opus_int8)silk_RSHIFT_ROUND( q1_Q10, 10 );

        /* Excitation */
        exc_Q14 = silk_LSHIFT( q1_Q10, 4 );
        if ( NSQ->rand_seed < 0 ) {
           exc_Q14 = -exc_Q14;
        }

        /* Add predictions */
        LPC_exc_Q14 = silk_ADD_LSHIFT32( exc_Q14, LTP_pred_Q13, 1 );
        xq_Q14      = silk_ADD32_ovflw( LPC_exc_Q14, silk_LSHIFT32( LPC_pred_Q10, 4 ) );

        /* Scale XQ back to normal level before saving */
        xq[ i ] = (opus_int16)silk_SAT16( silk_RSHIFT_ROUND( silk_SMULWW( xq_Q14, Gain_Q10 ), 8 ) );

        /* Update states */
        psLPC_Q14++;
        *psLPC_Q14 = xq_Q14;
        NSQ->sDiff_shp_Q14 = silk_SUB32_ovflw( xq_Q14, silk_LSHIFT32( x_sc_Q10[ i ], 4 ) );
        sLF_AR_shp_Q14 = silk_SUB32_ovflw( NSQ->sDiff_shp_Q14, silk_LSHIFT32( n_AR_Q12, 2 ) );
        NSQ->sLF_AR_shp_Q14 = sLF_AR_shp_Q14;

        NSQ->sLTP_shp_Q14[ NSQ->sLTP_shp_buf_idx ] = silk_SUB32_ovflw(sLF_AR_shp_Q14, silk_LSHIFT32(n_LF_Q12, 2));
        sLTP_Q15[ NSQ->sLTP_buf_idx ] = silk_LSHIFT( LPC_exc_Q14, 1 );
        NSQ->sLTP_shp_buf_idx++;
        NSQ->sLTP_buf_idx++;

        /* Make dither dependent on quantized signal */
        NSQ->rand_seed = silk_ADD32_ovflw( NSQ->rand_seed, pulses[ i ] );
    }

    /* Update LPC synth buffer */
    silk_memcpy( NSQ->sLPC_Q14, &NSQ->sLPC_Q14[ length ], NSQ_LPC_BUF_LENGTH * sizeof( opus_int32 ) );
}
/* -------------------------------------------------------------------------- */

#define SLTP_SHP_LEN ( 2 * MAX_FRAME_LENGTH )           /* sLTP_shp_Q14 */
#define SLPC_LEN     ( MAX_SUB_FRAME_LENGTH + NSQ_LPC_BUF_LENGTH )
#define SLTP_Q15_LEN ( 2 * MAX_FRAME_LENGTH )           /* generous sLTP_Q15 buffer */

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
  unsigned char b[4];
  if (!read_exact(b, sizeof(b))) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) | ((uint32_t)b[2] << 16) |
         ((uint32_t)b[3] << 24);
  return 1;
}
static int read_i32(int32_t *out) {
  uint32_t u;
  if (!read_u32(&u)) return 0;
  *out = (int32_t)u;
  return 1;
}
static int read_i16(int16_t *out) {
  unsigned char b[2];
  if (!read_exact(b, sizeof(b))) return 0;
  *out = (int16_t)((uint16_t)b[0] | ((uint16_t)b[1] << 8));
  return 1;
}
static int write_u32(uint32_t value) {
  unsigned char b[4];
  b[0] = (unsigned char)(value & 0xffu);
  b[1] = (unsigned char)((value >> 8) & 0xffu);
  b[2] = (unsigned char)((value >> 16) & 0xffu);
  b[3] = (unsigned char)((value >> 24) & 0xffu);
  return write_exact(b, sizeof(b));
}
static int write_i32(int32_t value) { return write_u32((uint32_t)value); }
static int write_i16(int16_t value) {
  uint16_t u = (uint16_t)value;
  unsigned char b[2];
  b[0] = (unsigned char)(u & 0xffu);
  b[1] = (unsigned char)((u >> 8) & 0xffu);
  return write_exact(b, sizeof(b));
}

int main(void) {
  if (!set_binary_stdio()) return 1;

  char magic[4];
  if (!read_exact(magic, sizeof(magic)) ||
      memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) {
    return 1;
  }
  uint32_t version;
  if (!read_u32(&version) || version != 1) return 1;
  uint32_t count;
  if (!read_u32(&count)) return 1;

  if (!write_exact(OUTPUT_MAGIC, 4)) return 1;
  if (!write_u32(1)) return 1; /* version */
  if (!write_u32(count)) return 1;

  for (uint32_t c = 0; c < count; c++) {
    static silk_nsq_state NSQ;
    static opus_int32 x_sc_Q10[MAX_SUB_FRAME_LENGTH];
    static opus_int32 sLTP_Q15[SLTP_Q15_LEN];
    static opus_int8  pulses[MAX_SUB_FRAME_LENGTH];
    static opus_int16 xq[MAX_SUB_FRAME_LENGTH];
    opus_int16 a_Q12[MAX_LPC_ORDER];
    opus_int16 b_Q14[LTP_ORDER];
    opus_int16 AR_shp_Q13[MAX_SHAPE_LPC_ORDER];

    uint32_t length, signalType, predictLPCOrder, shapingLPCOrder;
    int32_t lag, HarmShapeFIRPacked_Q14, Tilt_Q14, LF_shp_Q14, Gain_Q16;
    int32_t Lambda_Q10, offset_Q10;
    int32_t sLTP_shp_buf_idx, sLTP_buf_idx, rand_seed;
    int32_t sLF_AR_shp_Q14, sDiff_shp_Q14;

    if (!read_u32(&length) || !read_u32(&signalType) ||
        !read_u32(&predictLPCOrder) || !read_u32(&shapingLPCOrder)) {
      return 1;
    }
    if (!read_i32(&lag) || !read_i32(&HarmShapeFIRPacked_Q14) ||
        !read_i32(&Tilt_Q14) || !read_i32(&LF_shp_Q14) ||
        !read_i32(&Gain_Q16) || !read_i32(&Lambda_Q10) ||
        !read_i32(&offset_Q10)) {
      return 1;
    }
    if (!read_i32(&sLTP_shp_buf_idx) || !read_i32(&sLTP_buf_idx) ||
        !read_i32(&rand_seed) || !read_i32(&sLF_AR_shp_Q14) ||
        !read_i32(&sDiff_shp_Q14)) {
      return 1;
    }

    memset(&NSQ, 0, sizeof(NSQ));
    NSQ.sLTP_shp_buf_idx = sLTP_shp_buf_idx;
    NSQ.sLTP_buf_idx     = sLTP_buf_idx;
    NSQ.rand_seed        = rand_seed;
    NSQ.sLF_AR_shp_Q14   = sLF_AR_shp_Q14;
    NSQ.sDiff_shp_Q14    = sDiff_shp_Q14;

    for (uint32_t i = 0; i < (uint32_t)MAX_LPC_ORDER; i++)
      if (!read_i16(&a_Q12[i])) return 1;
    for (uint32_t i = 0; i < (uint32_t)LTP_ORDER; i++)
      if (!read_i16(&b_Q14[i])) return 1;
    for (uint32_t i = 0; i < (uint32_t)MAX_SHAPE_LPC_ORDER; i++)
      if (!read_i16(&AR_shp_Q13[i])) return 1;

    for (uint32_t i = 0; i < length; i++)
      if (!read_i32(&x_sc_Q10[i])) return 1;
    for (uint32_t i = 0; i < (uint32_t)SLPC_LEN; i++)
      if (!read_i32(&NSQ.sLPC_Q14[i])) return 1;
    for (uint32_t i = 0; i < (uint32_t)MAX_SHAPE_LPC_ORDER; i++)
      if (!read_i32(&NSQ.sAR2_Q14[i])) return 1;
    for (uint32_t i = 0; i < (uint32_t)SLTP_SHP_LEN; i++)
      if (!read_i32(&NSQ.sLTP_shp_Q14[i])) return 1;
    for (uint32_t i = 0; i < (uint32_t)SLTP_Q15_LEN; i++)
      if (!read_i32(&sLTP_Q15[i])) return 1;

    oracle_noise_shape_quantizer(&NSQ, (opus_int)signalType, x_sc_Q10, pulses,
        xq, sLTP_Q15, a_Q12, b_Q14, AR_shp_Q13, (opus_int)lag,
        HarmShapeFIRPacked_Q14, (opus_int)Tilt_Q14, LF_shp_Q14, Gain_Q16,
        (opus_int)Lambda_Q10, (opus_int)offset_Q10, (opus_int)length,
        (opus_int)shapingLPCOrder, (opus_int)predictLPCOrder, 0);

    /* Outputs. Pulses are int8 but emitted sign-extended as int16 so the Go
     * reader can consume them with its int16 primitive. */
    for (uint32_t i = 0; i < length; i++)
      if (!write_i16((int16_t)pulses[i])) return 1;
    for (uint32_t i = 0; i < length; i++)
      if (!write_i16(xq[i])) return 1;
    for (uint32_t i = 0; i < (uint32_t)SLTP_Q15_LEN; i++)
      if (!write_i32(sLTP_Q15[i])) return 1;
    /* Mutated state */
    for (uint32_t i = 0; i < (uint32_t)SLPC_LEN; i++)
      if (!write_i32(NSQ.sLPC_Q14[i])) return 1;
    for (uint32_t i = 0; i < (uint32_t)MAX_SHAPE_LPC_ORDER; i++)
      if (!write_i32(NSQ.sAR2_Q14[i])) return 1;
    for (uint32_t i = 0; i < (uint32_t)SLTP_SHP_LEN; i++)
      if (!write_i32(NSQ.sLTP_shp_Q14[i])) return 1;
    if (!write_i32(NSQ.sLTP_shp_buf_idx)) return 1;
    if (!write_i32(NSQ.sLTP_buf_idx)) return 1;
    if (!write_i32(NSQ.rand_seed)) return 1;
    if (!write_i32(NSQ.sLF_AR_shp_Q14)) return 1;
    if (!write_i32(NSQ.sDiff_shp_Q14)) return 1;
  }

  return 0;
}
