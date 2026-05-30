/* Oracle for the libopus FIXED_POINT silk_noise_shape_quantizer_del_dec and
 * silk_nsq_del_dec_scale_states kernels (silk/NSQ_del_dec.c).
 *
 * Must be compiled and linked against a libopus configured with
 * --enable-fixed-point (defines FIXED_POINT). Both kernels are file-static
 * OPUS_INLINE functions, so they cannot be reached through the library symbol
 * table; we reproduce their bodies verbatim here (compiled with the fixed
 * reference headers / macros) so the oracle is bit-exact with the reference.
 *
 * The short-term prediction and noise-shape feedback are inlined directly (the
 * del-dec kernel calls silk_noise_shape_quantizer_short_prediction which on
 * arm64 dispatches to NEON; the Go port targets the scalar reference, so the
 * scalar C reference is used here too).
 *
 * Reads a little-endian payload of cases from stdin and writes the kernel
 * outputs (pulses, xq, the sLTP_Q15 window, the mutated NSQ scalar/buffer state,
 * smpl_buf_idx, and the per-state survivor structs) to stdout. */

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

#define INPUT_MAGIC "GDDI"
#define OUTPUT_MAGIC "GDDO"

#if defined(ENABLE_ASSERTIONS) || defined(ENABLE_HARDENING)
void celt_fatal(const char *str, const char *file, int line) {
  (void)str;
  (void)file;
  (void)line;
  abort();
}
#endif

typedef struct {
    opus_int32 sLPC_Q14[ MAX_SUB_FRAME_LENGTH + NSQ_LPC_BUF_LENGTH ];
    opus_int32 RandState[ DECISION_DELAY ];
    opus_int32 Q_Q10[     DECISION_DELAY ];
    opus_int32 Xq_Q14[    DECISION_DELAY ];
    opus_int32 Pred_Q15[  DECISION_DELAY ];
    opus_int32 Shape_Q14[ DECISION_DELAY ];
    opus_int32 sAR2_Q14[ MAX_SHAPE_LPC_ORDER ];
    opus_int32 LF_AR_Q14;
    opus_int32 Diff_Q14;
    opus_int32 Seed;
    opus_int32 SeedInit;
    opus_int32 RD_Q10;
} NSQ_del_dec_struct;

typedef struct {
    opus_int32 Q_Q10;
    opus_int32 RD_Q10;
    opus_int32 xq_Q14;
    opus_int32 LF_AR_Q14;
    opus_int32 Diff_Q14;
    opus_int32 sLTP_shp_Q14;
    opus_int32 LPC_exc_Q14;
} NSQ_sample_struct;

typedef NSQ_sample_struct NSQ_sample_pair[ 2 ];

#define MAX_STATES 4

/* --- Verbatim copy of silk_noise_shape_quantizer_del_dec from NSQ_del_dec.c
 * with the inner kernels called through the scalar _c references. --------- */
static OPUS_INLINE void oracle_noise_shape_quantizer_del_dec(
    silk_nsq_state      *NSQ,
    NSQ_del_dec_struct  psDelDec[],
    opus_int            signalType,
    const opus_int32    x_Q10[],
    opus_int8           pulses[],
    opus_int16          xq[],
    opus_int32          sLTP_Q15[],
    opus_int32          delayedGain_Q10[],
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
    opus_int            subfr,
    opus_int            shapingLPCOrder,
    opus_int            predictLPCOrder,
    opus_int            warping_Q16,
    opus_int            nStatesDelayedDecision,
    opus_int            *smpl_buf_idx,
    opus_int            decisionDelay,
    int                 arch
)
{
    opus_int     i, j, k, Winner_ind, RDmin_ind, RDmax_ind, last_smple_idx;
    opus_int32   Winner_rand_state;
    opus_int32   LTP_pred_Q14, LPC_pred_Q14, n_AR_Q14, n_LTP_Q14;
    opus_int32   n_LF_Q14, r_Q10, rr_Q10, rd1_Q10, rd2_Q10, RDmin_Q10, RDmax_Q10;
    opus_int32   q1_Q0, q1_Q10, q2_Q10, exc_Q14, LPC_exc_Q14, xq_Q14, Gain_Q10;
    opus_int32   tmp1, tmp2, sLF_AR_shp_Q14;
    opus_int32   *pred_lag_ptr, *shp_lag_ptr, *psLPC_Q14;

    NSQ_sample_pair psSampleState[ MAX_STATES ];
    NSQ_del_dec_struct *psDD;
    NSQ_sample_struct  *psSS;

    (void)arch;

    shp_lag_ptr  = &NSQ->sLTP_shp_Q14[ NSQ->sLTP_shp_buf_idx - lag + HARM_SHAPE_FIR_TAPS / 2 ];
    pred_lag_ptr = &sLTP_Q15[ NSQ->sLTP_buf_idx - lag + LTP_ORDER / 2 ];
    Gain_Q10     = silk_RSHIFT( Gain_Q16, 6 );

    for( i = 0; i < length; i++ ) {
        /* Long-term prediction */
        if( signalType == TYPE_VOICED ) {
            LTP_pred_Q14 = 2;
            LTP_pred_Q14 = silk_SMLAWB( LTP_pred_Q14, pred_lag_ptr[  0 ], b_Q14[ 0 ] );
            LTP_pred_Q14 = silk_SMLAWB( LTP_pred_Q14, pred_lag_ptr[ -1 ], b_Q14[ 1 ] );
            LTP_pred_Q14 = silk_SMLAWB( LTP_pred_Q14, pred_lag_ptr[ -2 ], b_Q14[ 2 ] );
            LTP_pred_Q14 = silk_SMLAWB( LTP_pred_Q14, pred_lag_ptr[ -3 ], b_Q14[ 3 ] );
            LTP_pred_Q14 = silk_SMLAWB( LTP_pred_Q14, pred_lag_ptr[ -4 ], b_Q14[ 4 ] );
            LTP_pred_Q14 = silk_LSHIFT( LTP_pred_Q14, 1 );
            pred_lag_ptr++;
        } else {
            LTP_pred_Q14 = 0;
        }

        /* Long-term shaping */
        if( lag > 0 ) {
            n_LTP_Q14 = silk_SMULWB( silk_ADD_SAT32( shp_lag_ptr[ 0 ], shp_lag_ptr[ -2 ] ), HarmShapeFIRPacked_Q14 );
            n_LTP_Q14 = silk_SMLAWT( n_LTP_Q14, shp_lag_ptr[ -1 ], HarmShapeFIRPacked_Q14 );
            n_LTP_Q14 = silk_SUB_LSHIFT32( LTP_pred_Q14, n_LTP_Q14, 2 );
            shp_lag_ptr++;
        } else {
            n_LTP_Q14 = 0;
        }

        for( k = 0; k < nStatesDelayedDecision; k++ ) {
            psDD = &psDelDec[ k ];
            psSS = psSampleState[ k ];

            psDD->Seed = silk_RAND( psDD->Seed );

            psLPC_Q14 = &psDD->sLPC_Q14[ NSQ_LPC_BUF_LENGTH - 1 + i ];
            LPC_pred_Q14 = silk_noise_shape_quantizer_short_prediction_c(psLPC_Q14, a_Q12, predictLPCOrder);
            LPC_pred_Q14 = silk_LSHIFT( LPC_pred_Q14, 4 );

            tmp2 = silk_SMLAWB( psDD->Diff_Q14, psDD->sAR2_Q14[ 0 ], warping_Q16 );
            tmp1 = silk_SMLAWB( psDD->sAR2_Q14[ 0 ], silk_SUB32_ovflw(psDD->sAR2_Q14[ 1 ], tmp2), warping_Q16 );
            psDD->sAR2_Q14[ 0 ] = tmp2;
            n_AR_Q14 = silk_RSHIFT( shapingLPCOrder, 1 );
            n_AR_Q14 = silk_SMLAWB( n_AR_Q14, tmp2, AR_shp_Q13[ 0 ] );
            for( j = 2; j < shapingLPCOrder; j += 2 ) {
                tmp2 = silk_SMLAWB( psDD->sAR2_Q14[ j - 1 ], silk_SUB32_ovflw(psDD->sAR2_Q14[ j + 0 ], tmp1), warping_Q16 );
                psDD->sAR2_Q14[ j - 1 ] = tmp1;
                n_AR_Q14 = silk_SMLAWB( n_AR_Q14, tmp1, AR_shp_Q13[ j - 1 ] );
                tmp1 = silk_SMLAWB( psDD->sAR2_Q14[ j + 0 ], silk_SUB32_ovflw(psDD->sAR2_Q14[ j + 1 ], tmp2), warping_Q16 );
                psDD->sAR2_Q14[ j + 0 ] = tmp2;
                n_AR_Q14 = silk_SMLAWB( n_AR_Q14, tmp2, AR_shp_Q13[ j ] );
            }
            psDD->sAR2_Q14[ shapingLPCOrder - 1 ] = tmp1;
            n_AR_Q14 = silk_SMLAWB( n_AR_Q14, tmp1, AR_shp_Q13[ shapingLPCOrder - 1 ] );

            n_AR_Q14 = silk_LSHIFT( n_AR_Q14, 1 );
            n_AR_Q14 = silk_SMLAWB( n_AR_Q14, psDD->LF_AR_Q14, Tilt_Q14 );
            n_AR_Q14 = silk_LSHIFT( n_AR_Q14, 2 );

            n_LF_Q14 = silk_SMULWB( psDD->Shape_Q14[ *smpl_buf_idx ], LF_shp_Q14 );
            n_LF_Q14 = silk_SMLAWT( n_LF_Q14, psDD->LF_AR_Q14, LF_shp_Q14 );
            n_LF_Q14 = silk_LSHIFT( n_LF_Q14, 2 );

            tmp1 = silk_ADD_SAT32( n_AR_Q14, n_LF_Q14 );
            tmp2 = silk_ADD32_ovflw( n_LTP_Q14, LPC_pred_Q14 );
            tmp1 = silk_SUB_SAT32( tmp2, tmp1 );
            tmp1 = silk_RSHIFT_ROUND( tmp1, 4 );

            r_Q10 = silk_SUB32( x_Q10[ i ], tmp1 );

            if ( psDD->Seed < 0 ) {
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
                rd1_Q10 = silk_SMULBB( q1_Q10, Lambda_Q10 );
                rd2_Q10 = silk_SMULBB( q2_Q10, Lambda_Q10 );
            } else if( q1_Q0 == 0 ) {
                q1_Q10  = offset_Q10;
                q2_Q10  = silk_ADD32( q1_Q10, 1024 - QUANT_LEVEL_ADJUST_Q10 );
                rd1_Q10 = silk_SMULBB( q1_Q10, Lambda_Q10 );
                rd2_Q10 = silk_SMULBB( q2_Q10, Lambda_Q10 );
            } else if( q1_Q0 == -1 ) {
                q2_Q10  = offset_Q10;
                q1_Q10  = silk_SUB32( q2_Q10, 1024 - QUANT_LEVEL_ADJUST_Q10 );
                rd1_Q10 = silk_SMULBB( -q1_Q10, Lambda_Q10 );
                rd2_Q10 = silk_SMULBB(  q2_Q10, Lambda_Q10 );
            } else {
                q1_Q10  = silk_ADD32( silk_LSHIFT( q1_Q0, 10 ), QUANT_LEVEL_ADJUST_Q10 );
                q1_Q10  = silk_ADD32( q1_Q10, offset_Q10 );
                q2_Q10  = silk_ADD32( q1_Q10, 1024 );
                rd1_Q10 = silk_SMULBB( -q1_Q10, Lambda_Q10 );
                rd2_Q10 = silk_SMULBB( -q2_Q10, Lambda_Q10 );
            }
            rr_Q10  = silk_SUB32( r_Q10, q1_Q10 );
            rd1_Q10 = silk_RSHIFT( silk_SMLABB( rd1_Q10, rr_Q10, rr_Q10 ), 10 );
            rr_Q10  = silk_SUB32( r_Q10, q2_Q10 );
            rd2_Q10 = silk_RSHIFT( silk_SMLABB( rd2_Q10, rr_Q10, rr_Q10 ), 10 );

            if( rd1_Q10 < rd2_Q10 ) {
                psSS[ 0 ].RD_Q10 = silk_ADD32( psDD->RD_Q10, rd1_Q10 );
                psSS[ 1 ].RD_Q10 = silk_ADD32( psDD->RD_Q10, rd2_Q10 );
                psSS[ 0 ].Q_Q10  = q1_Q10;
                psSS[ 1 ].Q_Q10  = q2_Q10;
            } else {
                psSS[ 0 ].RD_Q10 = silk_ADD32( psDD->RD_Q10, rd2_Q10 );
                psSS[ 1 ].RD_Q10 = silk_ADD32( psDD->RD_Q10, rd1_Q10 );
                psSS[ 0 ].Q_Q10  = q2_Q10;
                psSS[ 1 ].Q_Q10  = q1_Q10;
            }

            exc_Q14 = silk_LSHIFT32( psSS[ 0 ].Q_Q10, 4 );
            if ( psDD->Seed < 0 ) {
                exc_Q14 = -exc_Q14;
            }
            LPC_exc_Q14 = silk_ADD32( exc_Q14, LTP_pred_Q14 );
            xq_Q14      = silk_ADD32_ovflw( LPC_exc_Q14, LPC_pred_Q14 );
            psSS[ 0 ].Diff_Q14     = silk_SUB32_ovflw( xq_Q14, silk_LSHIFT32( x_Q10[ i ], 4 ) );
            sLF_AR_shp_Q14         = silk_SUB32_ovflw( psSS[ 0 ].Diff_Q14, n_AR_Q14 );
            psSS[ 0 ].sLTP_shp_Q14 = silk_SUB_SAT32( sLF_AR_shp_Q14, n_LF_Q14 );
            psSS[ 0 ].LF_AR_Q14    = sLF_AR_shp_Q14;
            psSS[ 0 ].LPC_exc_Q14  = LPC_exc_Q14;
            psSS[ 0 ].xq_Q14       = xq_Q14;

            exc_Q14 = silk_LSHIFT32( psSS[ 1 ].Q_Q10, 4 );
            if ( psDD->Seed < 0 ) {
                exc_Q14 = -exc_Q14;
            }
            LPC_exc_Q14 = silk_ADD32( exc_Q14, LTP_pred_Q14 );
            xq_Q14      = silk_ADD32_ovflw( LPC_exc_Q14, LPC_pred_Q14 );
            psSS[ 1 ].Diff_Q14     = silk_SUB32_ovflw( xq_Q14, silk_LSHIFT32( x_Q10[ i ], 4 ) );
            sLF_AR_shp_Q14         = silk_SUB32_ovflw( psSS[ 1 ].Diff_Q14, n_AR_Q14 );
            psSS[ 1 ].sLTP_shp_Q14 = silk_SUB_SAT32( sLF_AR_shp_Q14, n_LF_Q14 );
            psSS[ 1 ].LF_AR_Q14    = sLF_AR_shp_Q14;
            psSS[ 1 ].LPC_exc_Q14  = LPC_exc_Q14;
            psSS[ 1 ].xq_Q14       = xq_Q14;
        }

        *smpl_buf_idx  = ( *smpl_buf_idx - 1 ) % DECISION_DELAY;
        if( *smpl_buf_idx < 0 ) *smpl_buf_idx += DECISION_DELAY;
        last_smple_idx = ( *smpl_buf_idx + decisionDelay ) % DECISION_DELAY;

        RDmin_Q10 = psSampleState[ 0 ][ 0 ].RD_Q10;
        Winner_ind = 0;
        for( k = 1; k < nStatesDelayedDecision; k++ ) {
            if( psSampleState[ k ][ 0 ].RD_Q10 < RDmin_Q10 ) {
                RDmin_Q10  = psSampleState[ k ][ 0 ].RD_Q10;
                Winner_ind = k;
            }
        }

        Winner_rand_state = psDelDec[ Winner_ind ].RandState[ last_smple_idx ];
        for( k = 0; k < nStatesDelayedDecision; k++ ) {
            if( psDelDec[ k ].RandState[ last_smple_idx ] != Winner_rand_state ) {
                psSampleState[ k ][ 0 ].RD_Q10 = silk_ADD32( psSampleState[ k ][ 0 ].RD_Q10, silk_int32_MAX >> 4 );
                psSampleState[ k ][ 1 ].RD_Q10 = silk_ADD32( psSampleState[ k ][ 1 ].RD_Q10, silk_int32_MAX >> 4 );
            }
        }

        RDmax_Q10  = psSampleState[ 0 ][ 0 ].RD_Q10;
        RDmin_Q10  = psSampleState[ 0 ][ 1 ].RD_Q10;
        RDmax_ind = 0;
        RDmin_ind = 0;
        for( k = 1; k < nStatesDelayedDecision; k++ ) {
            if( psSampleState[ k ][ 0 ].RD_Q10 > RDmax_Q10 ) {
                RDmax_Q10  = psSampleState[ k ][ 0 ].RD_Q10;
                RDmax_ind = k;
            }
            if( psSampleState[ k ][ 1 ].RD_Q10 < RDmin_Q10 ) {
                RDmin_Q10  = psSampleState[ k ][ 1 ].RD_Q10;
                RDmin_ind = k;
            }
        }

        if( RDmin_Q10 < RDmax_Q10 ) {
            silk_memcpy( ( (opus_int32 *)&psDelDec[ RDmax_ind ] ) + i,
                         ( (opus_int32 *)&psDelDec[ RDmin_ind ] ) + i, sizeof( NSQ_del_dec_struct ) - i * sizeof( opus_int32) );
            silk_memcpy( &psSampleState[ RDmax_ind ][ 0 ], &psSampleState[ RDmin_ind ][ 1 ], sizeof( NSQ_sample_struct ) );
        }

        psDD = &psDelDec[ Winner_ind ];
        if( subfr > 0 || i >= decisionDelay ) {
            pulses[  i - decisionDelay ] = (opus_int8)silk_RSHIFT_ROUND( psDD->Q_Q10[ last_smple_idx ], 10 );
            xq[ i - decisionDelay ] = (opus_int16)silk_SAT16( silk_RSHIFT_ROUND(
                silk_SMULWW( psDD->Xq_Q14[ last_smple_idx ], delayedGain_Q10[ last_smple_idx ] ), 8 ) );
            NSQ->sLTP_shp_Q14[ NSQ->sLTP_shp_buf_idx - decisionDelay ] = psDD->Shape_Q14[ last_smple_idx ];
            sLTP_Q15[          NSQ->sLTP_buf_idx     - decisionDelay ] = psDD->Pred_Q15[  last_smple_idx ];
        }
        NSQ->sLTP_shp_buf_idx++;
        NSQ->sLTP_buf_idx++;

        for( k = 0; k < nStatesDelayedDecision; k++ ) {
            psDD                                     = &psDelDec[ k ];
            psSS                                     = &psSampleState[ k ][ 0 ];
            psDD->LF_AR_Q14                          = psSS->LF_AR_Q14;
            psDD->Diff_Q14                           = psSS->Diff_Q14;
            psDD->sLPC_Q14[ NSQ_LPC_BUF_LENGTH + i ] = psSS->xq_Q14;
            psDD->Xq_Q14[    *smpl_buf_idx ]         = psSS->xq_Q14;
            psDD->Q_Q10[     *smpl_buf_idx ]         = psSS->Q_Q10;
            psDD->Pred_Q15[  *smpl_buf_idx ]         = silk_LSHIFT32( psSS->LPC_exc_Q14, 1 );
            psDD->Shape_Q14[ *smpl_buf_idx ]         = psSS->sLTP_shp_Q14;
            psDD->Seed                               = silk_ADD32_ovflw( psDD->Seed, silk_RSHIFT_ROUND( psSS->Q_Q10, 10 ) );
            psDD->RandState[ *smpl_buf_idx ]         = psDD->Seed;
            psDD->RD_Q10                             = psSS->RD_Q10;
        }
        delayedGain_Q10[     *smpl_buf_idx ]         = Gain_Q10;
    }

    for( k = 0; k < nStatesDelayedDecision; k++ ) {
        psDD = &psDelDec[ k ];
        silk_memcpy( psDD->sLPC_Q14, &psDD->sLPC_Q14[ length ], NSQ_LPC_BUF_LENGTH * sizeof( opus_int32 ) );
    }
}
/* -------------------------------------------------------------------------- */

#define SLTP_SHP_LEN ( 2 * MAX_FRAME_LENGTH )
#define SLPC_LEN     ( MAX_SUB_FRAME_LENGTH + NSQ_LPC_BUF_LENGTH )
#define SLTP_Q15_LEN ( 2 * MAX_FRAME_LENGTH )

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
  if (!write_u32(1)) return 1;
  if (!write_u32(count)) return 1;

  for (uint32_t c = 0; c < count; c++) {
    static silk_nsq_state NSQ;
    static NSQ_del_dec_struct psDelDec[MAX_STATES];
    static opus_int32 x_Q10[MAX_SUB_FRAME_LENGTH];
    static opus_int32 sLTP_Q15[SLTP_Q15_LEN];
    static opus_int32 delayedGain_Q10[DECISION_DELAY];
    /* Output buffers carry a DECISION_DELAY prefix so the kernel's
     * pulses[i-decisionDelay]/xq[i-decisionDelay] writes (which can be negative
     * when subfr>0) land at non-negative absolute offsets. */
    static opus_int8  pulses_buf[DECISION_DELAY + MAX_SUB_FRAME_LENGTH];
    static opus_int16 xq_buf[DECISION_DELAY + MAX_SUB_FRAME_LENGTH];
    opus_int8  *pulses = pulses_buf + DECISION_DELAY;
    opus_int16 *xq = xq_buf + DECISION_DELAY;
    opus_int16 a_Q12[MAX_LPC_ORDER];
    opus_int16 b_Q14[LTP_ORDER];
    opus_int16 AR_shp_Q13[MAX_SHAPE_LPC_ORDER];

    uint32_t length, signalType, predictLPCOrder, shapingLPCOrder, nStates;
    int32_t lag, HarmShapeFIRPacked_Q14, Tilt_Q14, LF_shp_Q14, Gain_Q16;
    int32_t Lambda_Q10, offset_Q10, warping_Q16, subfr;
    int32_t smpl_buf_idx, decisionDelay;
    int32_t sLTP_shp_buf_idx, sLTP_buf_idx;

    if (!read_u32(&length) || !read_u32(&signalType) ||
        !read_u32(&predictLPCOrder) || !read_u32(&shapingLPCOrder) ||
        !read_u32(&nStates)) {
      return 1;
    }
    if (!read_i32(&lag) || !read_i32(&HarmShapeFIRPacked_Q14) ||
        !read_i32(&Tilt_Q14) || !read_i32(&LF_shp_Q14) ||
        !read_i32(&Gain_Q16) || !read_i32(&Lambda_Q10) ||
        !read_i32(&offset_Q10) || !read_i32(&warping_Q16) ||
        !read_i32(&subfr) || !read_i32(&smpl_buf_idx) ||
        !read_i32(&decisionDelay)) {
      return 1;
    }
    if (!read_i32(&sLTP_shp_buf_idx) || !read_i32(&sLTP_buf_idx)) {
      return 1;
    }

    memset(&NSQ, 0, sizeof(NSQ));
    memset(pulses_buf, 0, sizeof(pulses_buf));
    memset(xq_buf, 0, sizeof(xq_buf));
    NSQ.sLTP_shp_buf_idx = sLTP_shp_buf_idx;
    NSQ.sLTP_buf_idx     = sLTP_buf_idx;

    for (uint32_t i = 0; i < (uint32_t)MAX_LPC_ORDER; i++)
      if (!read_i16(&a_Q12[i])) return 1;
    for (uint32_t i = 0; i < (uint32_t)LTP_ORDER; i++)
      if (!read_i16(&b_Q14[i])) return 1;
    for (uint32_t i = 0; i < (uint32_t)MAX_SHAPE_LPC_ORDER; i++)
      if (!read_i16(&AR_shp_Q13[i])) return 1;

    for (uint32_t i = 0; i < length; i++)
      if (!read_i32(&x_Q10[i])) return 1;
    for (uint32_t i = 0; i < (uint32_t)SLTP_SHP_LEN; i++)
      if (!read_i32(&NSQ.sLTP_shp_Q14[i])) return 1;
    for (uint32_t i = 0; i < (uint32_t)SLTP_Q15_LEN; i++)
      if (!read_i32(&sLTP_Q15[i])) return 1;
    for (uint32_t i = 0; i < (uint32_t)DECISION_DELAY; i++)
      if (!read_i32(&delayedGain_Q10[i])) return 1;

    /* Per-state survivor structs. */
    for (uint32_t k = 0; k < nStates; k++) {
      NSQ_del_dec_struct *d = &psDelDec[k];
      for (uint32_t i = 0; i < (uint32_t)SLPC_LEN; i++)
        if (!read_i32(&d->sLPC_Q14[i])) return 1;
      for (uint32_t i = 0; i < (uint32_t)DECISION_DELAY; i++)
        if (!read_i32(&d->RandState[i])) return 1;
      for (uint32_t i = 0; i < (uint32_t)DECISION_DELAY; i++)
        if (!read_i32(&d->Q_Q10[i])) return 1;
      for (uint32_t i = 0; i < (uint32_t)DECISION_DELAY; i++)
        if (!read_i32(&d->Xq_Q14[i])) return 1;
      for (uint32_t i = 0; i < (uint32_t)DECISION_DELAY; i++)
        if (!read_i32(&d->Pred_Q15[i])) return 1;
      for (uint32_t i = 0; i < (uint32_t)DECISION_DELAY; i++)
        if (!read_i32(&d->Shape_Q14[i])) return 1;
      for (uint32_t i = 0; i < (uint32_t)MAX_SHAPE_LPC_ORDER; i++)
        if (!read_i32(&d->sAR2_Q14[i])) return 1;
      if (!read_i32(&d->LF_AR_Q14)) return 1;
      if (!read_i32(&d->Diff_Q14)) return 1;
      if (!read_i32(&d->Seed)) return 1;
      if (!read_i32(&d->SeedInit)) return 1;
      if (!read_i32(&d->RD_Q10)) return 1;
    }

    opus_int sbi = (opus_int)smpl_buf_idx;
    oracle_noise_shape_quantizer_del_dec(&NSQ, psDelDec, (opus_int)signalType,
        x_Q10, pulses, xq, sLTP_Q15, delayedGain_Q10, a_Q12, b_Q14, AR_shp_Q13,
        (opus_int)lag, HarmShapeFIRPacked_Q14, (opus_int)Tilt_Q14, LF_shp_Q14,
        Gain_Q16, (opus_int)Lambda_Q10, (opus_int)offset_Q10, (opus_int)length,
        (opus_int)subfr, (opus_int)shapingLPCOrder, (opus_int)predictLPCOrder,
        (opus_int)warping_Q16, (opus_int)nStates, &sbi, (opus_int)decisionDelay, 0);

    /* Outputs: full prefixed buffers (DECISION_DELAY + length), pulses
     * sign-extended to int16. */
    for (uint32_t i = 0; i < (uint32_t)DECISION_DELAY + length; i++)
      if (!write_i16((int16_t)pulses_buf[i])) return 1;
    for (uint32_t i = 0; i < (uint32_t)DECISION_DELAY + length; i++)
      if (!write_i16(xq_buf[i])) return 1;
    for (uint32_t i = 0; i < (uint32_t)SLTP_Q15_LEN; i++)
      if (!write_i32(sLTP_Q15[i])) return 1;
    for (uint32_t i = 0; i < (uint32_t)SLTP_SHP_LEN; i++)
      if (!write_i32(NSQ.sLTP_shp_Q14[i])) return 1;
    for (uint32_t i = 0; i < (uint32_t)DECISION_DELAY; i++)
      if (!write_i32(delayedGain_Q10[i])) return 1;
    if (!write_i32(NSQ.sLTP_shp_buf_idx)) return 1;
    if (!write_i32(NSQ.sLTP_buf_idx)) return 1;
    if (!write_i32((int32_t)sbi)) return 1;

    /* Per-state survivor structs after the call. */
    for (uint32_t k = 0; k < nStates; k++) {
      NSQ_del_dec_struct *d = &psDelDec[k];
      for (uint32_t i = 0; i < (uint32_t)SLPC_LEN; i++)
        if (!write_i32(d->sLPC_Q14[i])) return 1;
      for (uint32_t i = 0; i < (uint32_t)DECISION_DELAY; i++)
        if (!write_i32(d->RandState[i])) return 1;
      for (uint32_t i = 0; i < (uint32_t)DECISION_DELAY; i++)
        if (!write_i32(d->Q_Q10[i])) return 1;
      for (uint32_t i = 0; i < (uint32_t)DECISION_DELAY; i++)
        if (!write_i32(d->Xq_Q14[i])) return 1;
      for (uint32_t i = 0; i < (uint32_t)DECISION_DELAY; i++)
        if (!write_i32(d->Pred_Q15[i])) return 1;
      for (uint32_t i = 0; i < (uint32_t)DECISION_DELAY; i++)
        if (!write_i32(d->Shape_Q14[i])) return 1;
      for (uint32_t i = 0; i < (uint32_t)MAX_SHAPE_LPC_ORDER; i++)
        if (!write_i32(d->sAR2_Q14[i])) return 1;
      if (!write_i32(d->LF_AR_Q14)) return 1;
      if (!write_i32(d->Diff_Q14)) return 1;
      if (!write_i32(d->Seed)) return 1;
      if (!write_i32(d->SeedInit)) return 1;
      if (!write_i32(d->RD_Q10)) return 1;
    }
  }

  return 0;
}
