/* Oracle for the libopus FIXED_POINT silk_NSQ_c outer state machine.
 *
 * Must be compiled and linked against a libopus configured with
 * --enable-fixed-point (defines FIXED_POINT).
 *
 * silk_NSQ_c and its file-static helpers (silk_nsq_scale_states, the inner
 * silk_noise_shape_quantizer) are reproduced verbatim here rather than linked
 * from the library. The reason: on arm64 the prebuilt library compiles NSQ.c
 * with the NEON intrinsic dispatch (OPUS_ARM_PRESUME_NEON_INTR), so its inner
 * silk_noise_shape_quantizer_short_prediction / silk_NSQ_noise_shape_feedback_loop
 * resolve to the *_neon variants, which can differ from the canonical C
 * reference by 1 ULP. The Go port targets the scalar reference (matching
 * amd64/CI), so the verbatim copies here call the scalar _c helpers directly
 * (silk_noise_shape_quantizer_short_prediction_c /
 * silk_NSQ_noise_shape_feedback_loop_c), exactly like the inner-kernel oracle.
 *
 * silk_LPC_analysis_filter (used by the voiced rewhitening) has no such NEON
 * dispatch in the FIXED_POINT build (USE_CELT_FIR == 0), so it is called
 * through the exported library symbol.
 *
 * Reads a little-endian payload of cases from stdin and writes the frame
 * pulses plus the fully mutated NSQ state to stdout. */

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
#include "main.h"
#include "NSQ.h"

#define INPUT_MAGIC "GNOI"
#define OUTPUT_MAGIC "GNOO"

#if defined(ENABLE_ASSERTIONS) || defined(ENABLE_HARDENING)
void celt_fatal(const char *str, const char *file, int line) {
  (void)str;
  (void)file;
  (void)line;
  abort();
}
#endif

/* --- Verbatim copy of silk_noise_shape_quantizer (silk/NSQ.c), routed through
 *     the scalar _c inner helpers. ----------------------------------------- */
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
    opus_int            predictLPCOrder
)
{
    opus_int     i;
    opus_int32   LTP_pred_Q13, LPC_pred_Q10, n_AR_Q12, n_LTP_Q13;
    opus_int32   n_LF_Q12, r_Q10, rr_Q10, q1_Q0, q1_Q10, q2_Q10, rd1_Q20, rd2_Q20;
    opus_int32   exc_Q14, LPC_exc_Q14, xq_Q14, Gain_Q10;
    opus_int32   tmp1, tmp2, sLF_AR_shp_Q14;
    opus_int32   *psLPC_Q14, *shp_lag_ptr, *pred_lag_ptr;

    shp_lag_ptr  = &NSQ->sLTP_shp_Q14[ NSQ->sLTP_shp_buf_idx - lag + HARM_SHAPE_FIR_TAPS / 2 ];
    pred_lag_ptr = &sLTP_Q15[ NSQ->sLTP_buf_idx - lag + LTP_ORDER / 2 ];
    Gain_Q10     = silk_RSHIFT( Gain_Q16, 6 );

    psLPC_Q14 = &NSQ->sLPC_Q14[ NSQ_LPC_BUF_LENGTH - 1 ];

    for( i = 0; i < length; i++ ) {
        NSQ->rand_seed = silk_RAND( NSQ->rand_seed );

        LPC_pred_Q10 = silk_noise_shape_quantizer_short_prediction_c(psLPC_Q14, a_Q12, predictLPCOrder);

        if( signalType == TYPE_VOICED ) {
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

        celt_assert( ( shapingLPCOrder & 1 ) == 0 );
        n_AR_Q12 = silk_NSQ_noise_shape_feedback_loop_c(&NSQ->sDiff_shp_Q14, NSQ->sAR2_Q14, AR_shp_Q13, shapingLPCOrder);

        n_AR_Q12 = silk_SMLAWB( n_AR_Q12, NSQ->sLF_AR_shp_Q14, Tilt_Q14 );

        n_LF_Q12 = silk_SMULWB( NSQ->sLTP_shp_Q14[ NSQ->sLTP_shp_buf_idx - 1 ], LF_shp_Q14 );
        n_LF_Q12 = silk_SMLAWT( n_LF_Q12, NSQ->sLF_AR_shp_Q14, LF_shp_Q14 );

        celt_assert( lag > 0 || signalType != TYPE_VOICED );

        tmp1 = silk_SUB32_ovflw( silk_LSHIFT32( LPC_pred_Q10, 2 ), n_AR_Q12 );
        tmp1 = silk_SUB32_ovflw( tmp1, n_LF_Q12 );
        if( lag > 0 ) {
            n_LTP_Q13 = silk_SMULWB( silk_ADD_SAT32( shp_lag_ptr[ 0 ], shp_lag_ptr[ -2 ] ), HarmShapeFIRPacked_Q14 );
            n_LTP_Q13 = silk_SMLAWT( n_LTP_Q13, shp_lag_ptr[ -1 ],                      HarmShapeFIRPacked_Q14 );
            n_LTP_Q13 = silk_LSHIFT( n_LTP_Q13, 1 );
            shp_lag_ptr++;

            tmp2 = silk_SUB32( LTP_pred_Q13, n_LTP_Q13 );
            tmp1 = silk_ADD32_ovflw( tmp2, silk_LSHIFT32( tmp1, 1 ) );
            tmp1 = silk_RSHIFT_ROUND( tmp1, 3 );
        } else {
            tmp1 = silk_RSHIFT_ROUND( tmp1, 2 );
        }

        r_Q10 = silk_SUB32( x_sc_Q10[ i ], tmp1 );

        if( NSQ->rand_seed < 0 ) {
            r_Q10 = -r_Q10;
        }
        r_Q10 = silk_LIMIT_32( r_Q10, -(31 << 10), 30 << 10 );

        q1_Q10 = silk_SUB32( r_Q10, offset_Q10 );
        q1_Q0 = silk_RSHIFT( q1_Q10, 10 );
        if (Lambda_Q10 > 2048) {
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
        } else {
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

        exc_Q14 = silk_LSHIFT( q1_Q10, 4 );
        if ( NSQ->rand_seed < 0 ) {
           exc_Q14 = -exc_Q14;
        }

        LPC_exc_Q14 = silk_ADD_LSHIFT32( exc_Q14, LTP_pred_Q13, 1 );
        xq_Q14      = silk_ADD32_ovflw( LPC_exc_Q14, silk_LSHIFT32( LPC_pred_Q10, 4 ) );

        xq[ i ] = (opus_int16)silk_SAT16( silk_RSHIFT_ROUND( silk_SMULWW( xq_Q14, Gain_Q10 ), 8 ) );

        psLPC_Q14++;
        *psLPC_Q14 = xq_Q14;
        NSQ->sDiff_shp_Q14 = silk_SUB32_ovflw( xq_Q14, silk_LSHIFT32( x_sc_Q10[ i ], 4 ) );
        sLF_AR_shp_Q14 = silk_SUB32_ovflw( NSQ->sDiff_shp_Q14, silk_LSHIFT32( n_AR_Q12, 2 ) );
        NSQ->sLF_AR_shp_Q14 = sLF_AR_shp_Q14;

        NSQ->sLTP_shp_Q14[ NSQ->sLTP_shp_buf_idx ] = silk_SUB32_ovflw(sLF_AR_shp_Q14, silk_LSHIFT32(n_LF_Q12, 2));
        sLTP_Q15[ NSQ->sLTP_buf_idx ] = silk_LSHIFT( LPC_exc_Q14, 1 );
        NSQ->sLTP_shp_buf_idx++;
        NSQ->sLTP_buf_idx++;

        NSQ->rand_seed = silk_ADD32_ovflw( NSQ->rand_seed, pulses[ i ] );
    }

    silk_memcpy( NSQ->sLPC_Q14, &NSQ->sLPC_Q14[ length ], NSQ_LPC_BUF_LENGTH * sizeof( opus_int32 ) );
}

/* --- Verbatim copy of silk_nsq_scale_states (silk/NSQ.c). ----------------- */
static OPUS_INLINE void oracle_nsq_scale_states(
    const silk_encoder_state *psEncC,
    silk_nsq_state      *NSQ,
    const opus_int16    x16[],
    opus_int32          x_sc_Q10[],
    const opus_int16    sLTP[],
    opus_int32          sLTP_Q15[],
    opus_int            subfr,
    const opus_int      LTP_scale_Q14,
    const opus_int32    Gains_Q16[ MAX_NB_SUBFR ],
    const opus_int      pitchL[ MAX_NB_SUBFR ],
    const opus_int      signal_type
)
{
    opus_int   i, lag;
    opus_int32 gain_adj_Q16, inv_gain_Q31, inv_gain_Q26;

    lag          = pitchL[ subfr ];
    inv_gain_Q31 = silk_INVERSE32_varQ( silk_max( Gains_Q16[ subfr ], 1 ), 47 );

    inv_gain_Q26 = silk_RSHIFT_ROUND( inv_gain_Q31, 5 );
    for( i = 0; i < psEncC->subfr_length; i++ ) {
        x_sc_Q10[ i ] = silk_SMULWW( x16[ i ], inv_gain_Q26 );
    }

    if( NSQ->rewhite_flag ) {
        if( subfr == 0 ) {
            inv_gain_Q31 = silk_LSHIFT( silk_SMULWB( inv_gain_Q31, LTP_scale_Q14 ), 2 );
        }
        for( i = NSQ->sLTP_buf_idx - lag - LTP_ORDER / 2; i < NSQ->sLTP_buf_idx; i++ ) {
            sLTP_Q15[ i ] = silk_SMULWB( inv_gain_Q31, sLTP[ i ] );
        }
    }

    if( Gains_Q16[ subfr ] != NSQ->prev_gain_Q16 ) {
        gain_adj_Q16 =  silk_DIV32_varQ( NSQ->prev_gain_Q16, Gains_Q16[ subfr ], 16 );

        for( i = NSQ->sLTP_shp_buf_idx - psEncC->ltp_mem_length; i < NSQ->sLTP_shp_buf_idx; i++ ) {
            NSQ->sLTP_shp_Q14[ i ] = silk_SMULWW( gain_adj_Q16, NSQ->sLTP_shp_Q14[ i ] );
        }

        if( signal_type == TYPE_VOICED && NSQ->rewhite_flag == 0 ) {
            for( i = NSQ->sLTP_buf_idx - lag - LTP_ORDER / 2; i < NSQ->sLTP_buf_idx; i++ ) {
                sLTP_Q15[ i ] = silk_SMULWW( gain_adj_Q16, sLTP_Q15[ i ] );
            }
        }

        NSQ->sLF_AR_shp_Q14 = silk_SMULWW( gain_adj_Q16, NSQ->sLF_AR_shp_Q14 );
        NSQ->sDiff_shp_Q14 = silk_SMULWW( gain_adj_Q16, NSQ->sDiff_shp_Q14 );

        for( i = 0; i < NSQ_LPC_BUF_LENGTH; i++ ) {
            NSQ->sLPC_Q14[ i ] = silk_SMULWW( gain_adj_Q16, NSQ->sLPC_Q14[ i ] );
        }
        for( i = 0; i < MAX_SHAPE_LPC_ORDER; i++ ) {
            NSQ->sAR2_Q14[ i ] = silk_SMULWW( gain_adj_Q16, NSQ->sAR2_Q14[ i ] );
        }

        NSQ->prev_gain_Q16 = Gains_Q16[ subfr ];
    }
}

/* --- Verbatim copy of silk_NSQ_c (silk/NSQ.c). silk_LPC_analysis_filter is
 *     the exported library symbol (no NEON dispatch). --------------------- */
static void oracle_NSQ(
    const silk_encoder_state    *psEncC,
    silk_nsq_state              *NSQ,
    SideInfoIndices             *psIndices,
    const opus_int16            x16[],
    opus_int8                   pulses[],
    const opus_int16            *PredCoef_Q12,
    const opus_int16            LTPCoef_Q14[ LTP_ORDER * MAX_NB_SUBFR ],
    const opus_int16            AR_Q13[ MAX_NB_SUBFR * MAX_SHAPE_LPC_ORDER ],
    const opus_int              HarmShapeGain_Q14[ MAX_NB_SUBFR ],
    const opus_int              Tilt_Q14[ MAX_NB_SUBFR ],
    const opus_int32            LF_shp_Q14[ MAX_NB_SUBFR ],
    const opus_int32            Gains_Q16[ MAX_NB_SUBFR ],
    const opus_int              pitchL[ MAX_NB_SUBFR ],
    const opus_int              Lambda_Q10,
    const opus_int              LTP_scale_Q14
)
{
    opus_int            k, lag, start_idx, LSF_interpolation_flag;
    const opus_int16    *A_Q12, *B_Q14, *AR_shp_Q13;
    opus_int16          *pxq;
    opus_int32          *sLTP_Q15;
    opus_int16          *sLTP;
    opus_int32          HarmShapeFIRPacked_Q14;
    opus_int            offset_Q10;
    opus_int32          *x_sc_Q10;

    sLTP_Q15 = malloc((size_t)(psEncC->ltp_mem_length + psEncC->frame_length) * sizeof(opus_int32));
    sLTP     = malloc((size_t)(psEncC->ltp_mem_length + psEncC->frame_length) * sizeof(opus_int16));
    x_sc_Q10 = malloc((size_t)psEncC->subfr_length * sizeof(opus_int32));
    if (!sLTP_Q15 || !sLTP || !x_sc_Q10) { free(sLTP_Q15); free(sLTP); free(x_sc_Q10); abort(); }
    /* Match libopus' uninitialized stack VARDECLs by zeroing; the only reads
     * outside written regions are on invalid configs the test never builds. */
    memset(sLTP_Q15, 0, (size_t)(psEncC->ltp_mem_length + psEncC->frame_length) * sizeof(opus_int32));
    memset(sLTP, 0, (size_t)(psEncC->ltp_mem_length + psEncC->frame_length) * sizeof(opus_int16));

    NSQ->rand_seed = psIndices->Seed;

    lag = NSQ->lagPrev;

    offset_Q10 = silk_Quantization_Offsets_Q10[ psIndices->signalType >> 1 ][ psIndices->quantOffsetType ];

    if( psIndices->NLSFInterpCoef_Q2 == 4 ) {
        LSF_interpolation_flag = 0;
    } else {
        LSF_interpolation_flag = 1;
    }

    NSQ->sLTP_shp_buf_idx = psEncC->ltp_mem_length;
    NSQ->sLTP_buf_idx     = psEncC->ltp_mem_length;
    pxq                   = &NSQ->xq[ psEncC->ltp_mem_length ];
    for( k = 0; k < psEncC->nb_subfr; k++ ) {
        A_Q12      = &PredCoef_Q12[ (( k >> 1 ) | ( 1 - LSF_interpolation_flag )) * MAX_LPC_ORDER ];
        B_Q14      = &LTPCoef_Q14[ k * LTP_ORDER ];
        AR_shp_Q13 = &AR_Q13[ k * MAX_SHAPE_LPC_ORDER ];

        HarmShapeFIRPacked_Q14  =                          silk_RSHIFT( HarmShapeGain_Q14[ k ], 2 );
        HarmShapeFIRPacked_Q14 |= silk_LSHIFT( (opus_int32)silk_RSHIFT( HarmShapeGain_Q14[ k ], 1 ), 16 );

        NSQ->rewhite_flag = 0;
        if( psIndices->signalType == TYPE_VOICED ) {
            lag = pitchL[ k ];

            if( ( k & ( 3 - silk_LSHIFT( LSF_interpolation_flag, 1 ) ) ) == 0 ) {
                start_idx = psEncC->ltp_mem_length - lag - psEncC->predictLPCOrder - LTP_ORDER / 2;
                celt_assert( start_idx > 0 );

                silk_LPC_analysis_filter( &sLTP[ start_idx ], &NSQ->xq[ start_idx + k * psEncC->subfr_length ],
                    A_Q12, psEncC->ltp_mem_length - start_idx, psEncC->predictLPCOrder, psEncC->arch );

                NSQ->rewhite_flag = 1;
                NSQ->sLTP_buf_idx = psEncC->ltp_mem_length;
            }
        }

        oracle_nsq_scale_states( psEncC, NSQ, x16, x_sc_Q10, sLTP, sLTP_Q15, k, LTP_scale_Q14, Gains_Q16, pitchL, psIndices->signalType );

        oracle_noise_shape_quantizer( NSQ, psIndices->signalType, x_sc_Q10, pulses, pxq, sLTP_Q15, A_Q12, B_Q14,
            AR_shp_Q13, lag, HarmShapeFIRPacked_Q14, Tilt_Q14[ k ], LF_shp_Q14[ k ], Gains_Q16[ k ], Lambda_Q10,
            offset_Q10, psEncC->subfr_length, psEncC->shapingLPCOrder, psEncC->predictLPCOrder );

        x16    += psEncC->subfr_length;
        pulses += psEncC->subfr_length;
        pxq    += psEncC->subfr_length;
    }

    NSQ->lagPrev = pitchL[ psEncC->nb_subfr - 1 ];

    silk_memmove( NSQ->xq,           &NSQ->xq[           psEncC->frame_length ], psEncC->ltp_mem_length * sizeof( opus_int16 ) );
    silk_memmove( NSQ->sLTP_shp_Q14, &NSQ->sLTP_shp_Q14[ psEncC->frame_length ], psEncC->ltp_mem_length * sizeof( opus_int32 ) );

    free(sLTP_Q15);
    free(sLTP);
    free(x_sc_Q10);
}
/* -------------------------------------------------------------------------- */

#define SLTP_SHP_LEN (2 * MAX_FRAME_LENGTH)
#define SLPC_LEN (MAX_SUB_FRAME_LENGTH + NSQ_LPC_BUF_LENGTH)
#define XQ_LEN (2 * MAX_FRAME_LENGTH)

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
    static silk_encoder_state psEncC;
    silk_nsq_state *NSQ;
    static SideInfoIndices indices;

    static opus_int16 x16[MAX_FRAME_LENGTH];
    static opus_int8 pulses[MAX_FRAME_LENGTH];

    static opus_int16 PredCoef_Q12[2 * MAX_LPC_ORDER];
    static opus_int16 LTPCoef_Q14[LTP_ORDER * MAX_NB_SUBFR];
    static opus_int16 AR_Q13[MAX_NB_SUBFR * MAX_SHAPE_LPC_ORDER];
    static opus_int HarmShapeGain_Q14[MAX_NB_SUBFR];
    static opus_int Tilt_Q14[MAX_NB_SUBFR];
    static opus_int32 LF_shp_Q14[MAX_NB_SUBFR];
    static opus_int32 Gains_Q16[MAX_NB_SUBFR];
    static opus_int pitchL[MAX_NB_SUBFR];

    uint32_t nb_subfr, frame_length, subfr_length, ltp_mem_length;
    uint32_t predictLPCOrder, shapingLPCOrder;
    int32_t signalType, quantOffsetType, nlsfInterpCoefQ2, seed;
    int32_t Lambda_Q10, LTP_scale_Q14;
    int32_t lagPrev, sLTP_buf_idx, sLTP_shp_buf_idx, rand_seed_in;
    int32_t prev_gain_Q16, rewhite_flag, sLF_AR_shp_Q14, sDiff_shp_Q14;

    if (!read_u32(&nb_subfr) || !read_u32(&frame_length) ||
        !read_u32(&subfr_length) || !read_u32(&ltp_mem_length) ||
        !read_u32(&predictLPCOrder) || !read_u32(&shapingLPCOrder)) {
      return 1;
    }
    if (!read_i32(&signalType) || !read_i32(&quantOffsetType) ||
        !read_i32(&nlsfInterpCoefQ2) || !read_i32(&seed) ||
        !read_i32(&Lambda_Q10) || !read_i32(&LTP_scale_Q14)) {
      return 1;
    }
    if (!read_i32(&lagPrev) || !read_i32(&sLTP_buf_idx) ||
        !read_i32(&sLTP_shp_buf_idx) || !read_i32(&rand_seed_in) ||
        !read_i32(&prev_gain_Q16) || !read_i32(&rewhite_flag) ||
        !read_i32(&sLF_AR_shp_Q14) || !read_i32(&sDiff_shp_Q14)) {
      return 1;
    }

    memset(&psEncC, 0, sizeof(psEncC));
    memset(&indices, 0, sizeof(indices));
    NSQ = &psEncC.sNSQ;

    psEncC.nb_subfr = (opus_int)nb_subfr;
    psEncC.frame_length = (opus_int)frame_length;
    psEncC.subfr_length = (opus_int)subfr_length;
    psEncC.ltp_mem_length = (opus_int)ltp_mem_length;
    psEncC.predictLPCOrder = (opus_int)predictLPCOrder;
    psEncC.shapingLPCOrder = (opus_int)shapingLPCOrder;
    psEncC.arch = 0;

    indices.signalType = (opus_int8)signalType;
    indices.quantOffsetType = (opus_int8)quantOffsetType;
    indices.NLSFInterpCoef_Q2 = (opus_int8)nlsfInterpCoefQ2;
    indices.Seed = (opus_int8)seed;

    for (int i = 0; i < 2 * MAX_LPC_ORDER; i++)
      if (!read_i16(&PredCoef_Q12[i])) return 1;
    for (int i = 0; i < LTP_ORDER * MAX_NB_SUBFR; i++)
      if (!read_i16(&LTPCoef_Q14[i])) return 1;
    for (int i = 0; i < MAX_NB_SUBFR * MAX_SHAPE_LPC_ORDER; i++)
      if (!read_i16(&AR_Q13[i])) return 1;
    for (int i = 0; i < MAX_NB_SUBFR; i++) {
      int32_t v;
      if (!read_i32(&v)) return 1;
      HarmShapeGain_Q14[i] = (opus_int)v;
    }
    for (int i = 0; i < MAX_NB_SUBFR; i++) {
      int32_t v;
      if (!read_i32(&v)) return 1;
      Tilt_Q14[i] = (opus_int)v;
    }
    for (int i = 0; i < MAX_NB_SUBFR; i++)
      if (!read_i32(&LF_shp_Q14[i])) return 1;
    for (int i = 0; i < MAX_NB_SUBFR; i++)
      if (!read_i32(&Gains_Q16[i])) return 1;
    for (int i = 0; i < MAX_NB_SUBFR; i++) {
      int32_t v;
      if (!read_i32(&v)) return 1;
      pitchL[i] = (opus_int)v;
    }

    for (uint32_t i = 0; i < frame_length; i++)
      if (!read_i16(&x16[i])) return 1;

    NSQ->lagPrev = (opus_int)lagPrev;
    NSQ->sLTP_buf_idx = (opus_int)sLTP_buf_idx;
    NSQ->sLTP_shp_buf_idx = (opus_int)sLTP_shp_buf_idx;
    NSQ->rand_seed = rand_seed_in;
    NSQ->prev_gain_Q16 = prev_gain_Q16;
    NSQ->rewhite_flag = (opus_int)rewhite_flag;
    NSQ->sLF_AR_shp_Q14 = sLF_AR_shp_Q14;
    NSQ->sDiff_shp_Q14 = sDiff_shp_Q14;

    for (int i = 0; i < XQ_LEN; i++)
      if (!read_i16(&NSQ->xq[i])) return 1;
    for (int i = 0; i < SLTP_SHP_LEN; i++)
      if (!read_i32(&NSQ->sLTP_shp_Q14[i])) return 1;
    for (int i = 0; i < SLPC_LEN; i++)
      if (!read_i32(&NSQ->sLPC_Q14[i])) return 1;
    for (int i = 0; i < MAX_SHAPE_LPC_ORDER; i++)
      if (!read_i32(&NSQ->sAR2_Q14[i])) return 1;

    memset(pulses, 0, sizeof(pulses));

    oracle_NSQ(&psEncC, NSQ, &indices, x16, pulses, PredCoef_Q12, LTPCoef_Q14,
        AR_Q13, HarmShapeGain_Q14, Tilt_Q14, LF_shp_Q14, Gains_Q16, pitchL,
        (opus_int)Lambda_Q10, (opus_int)LTP_scale_Q14);

    for (uint32_t i = 0; i < frame_length; i++)
      if (!write_i16((int16_t)pulses[i])) return 1;

    for (int i = 0; i < XQ_LEN; i++)
      if (!write_i16(NSQ->xq[i])) return 1;
    for (int i = 0; i < SLTP_SHP_LEN; i++)
      if (!write_i32(NSQ->sLTP_shp_Q14[i])) return 1;
    for (int i = 0; i < SLPC_LEN; i++)
      if (!write_i32(NSQ->sLPC_Q14[i])) return 1;
    for (int i = 0; i < MAX_SHAPE_LPC_ORDER; i++)
      if (!write_i32(NSQ->sAR2_Q14[i])) return 1;
    if (!write_i32(NSQ->sLF_AR_shp_Q14)) return 1;
    if (!write_i32(NSQ->sDiff_shp_Q14)) return 1;
    if (!write_i32(NSQ->lagPrev)) return 1;
    if (!write_i32(NSQ->sLTP_buf_idx)) return 1;
    if (!write_i32(NSQ->sLTP_shp_buf_idx)) return 1;
    if (!write_i32(NSQ->rand_seed)) return 1;
    if (!write_i32(NSQ->prev_gain_Q16)) return 1;
    if (!write_i32(NSQ->rewhite_flag)) return 1;
  }

  return 0;
}
