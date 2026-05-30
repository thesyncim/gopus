/* Oracle for the libopus FIXED_POINT silk_nsq_del_dec_scale_states kernel
 * (silk/NSQ_del_dec.c).
 *
 * Must be compiled and linked against a libopus configured with
 * --enable-fixed-point (defines FIXED_POINT). The kernel is a file-static
 * OPUS_INLINE function, so it cannot be reached through the library symbol
 * table; we reproduce its body verbatim here.
 *
 * Reads a little-endian payload of cases from stdin and writes the scaled
 * input, mutated sLTP_Q15 / sLTP_shp_Q14 windows, the prev_gain_Q16 update, and
 * the per-state survivor structs to stdout. */

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

#define INPUT_MAGIC "GDSI"
#define OUTPUT_MAGIC "GDSO"

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

#define MAX_STATES 4

/* --- Verbatim copy of silk_nsq_del_dec_scale_states ----------------------- */
static OPUS_INLINE void oracle_nsq_del_dec_scale_states(
    silk_nsq_state      *NSQ,
    NSQ_del_dec_struct  psDelDec[],
    const opus_int16    x16[],
    opus_int32          x_sc_Q10[],
    const opus_int16    sLTP[],
    opus_int32          sLTP_Q15[],
    opus_int            subfr,
    opus_int            nStatesDelayedDecision,
    const opus_int      LTP_scale_Q14,
    const opus_int32    Gains_Q16[],
    const opus_int      pitchL[],
    const opus_int      signal_type,
    const opus_int      decisionDelay,
    opus_int            subfr_length,
    opus_int            ltp_mem_length
)
{
    opus_int            i, k, lag;
    opus_int32          gain_adj_Q16, inv_gain_Q31, inv_gain_Q26;
    NSQ_del_dec_struct  *psDD;

    lag          = pitchL[ subfr ];
    inv_gain_Q31 = silk_INVERSE32_varQ( silk_max( Gains_Q16[ subfr ], 1 ), 47 );

    inv_gain_Q26 = silk_RSHIFT_ROUND( inv_gain_Q31, 5 );
    for( i = 0; i < subfr_length; i++ ) {
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

        for( i = NSQ->sLTP_shp_buf_idx - ltp_mem_length; i < NSQ->sLTP_shp_buf_idx; i++ ) {
            NSQ->sLTP_shp_Q14[ i ] = silk_SMULWW( gain_adj_Q16, NSQ->sLTP_shp_Q14[ i ] );
        }

        if( signal_type == TYPE_VOICED && NSQ->rewhite_flag == 0 ) {
            for( i = NSQ->sLTP_buf_idx - lag - LTP_ORDER / 2; i < NSQ->sLTP_buf_idx - decisionDelay; i++ ) {
                sLTP_Q15[ i ] = silk_SMULWW( gain_adj_Q16, sLTP_Q15[ i ] );
            }
        }

        for( k = 0; k < nStatesDelayedDecision; k++ ) {
            psDD = &psDelDec[ k ];
            psDD->LF_AR_Q14 = silk_SMULWW( gain_adj_Q16, psDD->LF_AR_Q14 );
            psDD->Diff_Q14 = silk_SMULWW( gain_adj_Q16, psDD->Diff_Q14 );
            for( i = 0; i < NSQ_LPC_BUF_LENGTH; i++ ) {
                psDD->sLPC_Q14[ i ] = silk_SMULWW( gain_adj_Q16, psDD->sLPC_Q14[ i ] );
            }
            for( i = 0; i < MAX_SHAPE_LPC_ORDER; i++ ) {
                psDD->sAR2_Q14[ i ] = silk_SMULWW( gain_adj_Q16, psDD->sAR2_Q14[ i ] );
            }
            for( i = 0; i < DECISION_DELAY; i++ ) {
                psDD->Pred_Q15[  i ] = silk_SMULWW( gain_adj_Q16, psDD->Pred_Q15[  i ] );
                psDD->Shape_Q14[ i ] = silk_SMULWW( gain_adj_Q16, psDD->Shape_Q14[ i ] );
            }
        }

        NSQ->prev_gain_Q16 = Gains_Q16[ subfr ];
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

static void payload_read_state(NSQ_del_dec_struct *d) {
  for (int i = 0; i < SLPC_LEN; i++) read_i32(&d->sLPC_Q14[i]);
  for (int i = 0; i < DECISION_DELAY; i++) read_i32(&d->RandState[i]);
  for (int i = 0; i < DECISION_DELAY; i++) read_i32(&d->Q_Q10[i]);
  for (int i = 0; i < DECISION_DELAY; i++) read_i32(&d->Xq_Q14[i]);
  for (int i = 0; i < DECISION_DELAY; i++) read_i32(&d->Pred_Q15[i]);
  for (int i = 0; i < DECISION_DELAY; i++) read_i32(&d->Shape_Q14[i]);
  for (int i = 0; i < MAX_SHAPE_LPC_ORDER; i++) read_i32(&d->sAR2_Q14[i]);
  read_i32(&d->LF_AR_Q14);
  read_i32(&d->Diff_Q14);
  read_i32(&d->Seed);
  read_i32(&d->SeedInit);
  read_i32(&d->RD_Q10);
}
static void payload_write_state(NSQ_del_dec_struct *d) {
  for (int i = 0; i < SLPC_LEN; i++) write_i32(d->sLPC_Q14[i]);
  for (int i = 0; i < DECISION_DELAY; i++) write_i32(d->RandState[i]);
  for (int i = 0; i < DECISION_DELAY; i++) write_i32(d->Q_Q10[i]);
  for (int i = 0; i < DECISION_DELAY; i++) write_i32(d->Xq_Q14[i]);
  for (int i = 0; i < DECISION_DELAY; i++) write_i32(d->Pred_Q15[i]);
  for (int i = 0; i < DECISION_DELAY; i++) write_i32(d->Shape_Q14[i]);
  for (int i = 0; i < MAX_SHAPE_LPC_ORDER; i++) write_i32(d->sAR2_Q14[i]);
  write_i32(d->LF_AR_Q14);
  write_i32(d->Diff_Q14);
  write_i32(d->Seed);
  write_i32(d->SeedInit);
  write_i32(d->RD_Q10);
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
    static opus_int16 x16[MAX_SUB_FRAME_LENGTH];
    static opus_int32 x_sc_Q10[MAX_SUB_FRAME_LENGTH];
    static opus_int16 sLTP[SLTP_Q15_LEN];
    static opus_int32 sLTP_Q15[SLTP_Q15_LEN];

    uint32_t subfr_length, signalType, nStates, rewhite;
    int32_t subfr, ltpScaleQ14, decisionDelay, ltpMemLen;
    int32_t prevGainQ16, sLTP_shp_buf_idx, sLTP_buf_idx;
    int32_t gains[MAX_NB_SUBFR];
    int32_t pitchL[MAX_NB_SUBFR];

    if (!read_u32(&subfr_length) || !read_u32(&signalType) ||
        !read_u32(&nStates) || !read_u32(&rewhite)) {
      return 1;
    }
    if (!read_i32(&subfr) || !read_i32(&ltpScaleQ14) ||
        !read_i32(&decisionDelay) || !read_i32(&ltpMemLen) ||
        !read_i32(&prevGainQ16) || !read_i32(&sLTP_shp_buf_idx) ||
        !read_i32(&sLTP_buf_idx)) {
      return 1;
    }
    for (int i = 0; i < MAX_NB_SUBFR; i++)
      if (!read_i32(&gains[i])) return 1;
    for (int i = 0; i < MAX_NB_SUBFR; i++)
      if (!read_i32(&pitchL[i])) return 1;

    memset(&NSQ, 0, sizeof(NSQ));
    NSQ.sLTP_shp_buf_idx = sLTP_shp_buf_idx;
    NSQ.sLTP_buf_idx     = sLTP_buf_idx;
    NSQ.prev_gain_Q16    = prevGainQ16;
    NSQ.rewhite_flag     = (opus_int)rewhite;

    for (uint32_t i = 0; i < subfr_length; i++)
      if (!read_i16(&x16[i])) return 1;
    for (int i = 0; i < SLTP_Q15_LEN; i++)
      if (!read_i16(&sLTP[i])) return 1;
    for (int i = 0; i < SLTP_SHP_LEN; i++)
      if (!read_i32(&NSQ.sLTP_shp_Q14[i])) return 1;
    for (int i = 0; i < SLTP_Q15_LEN; i++)
      if (!read_i32(&sLTP_Q15[i])) return 1;
    for (uint32_t k = 0; k < nStates; k++)
      payload_read_state(&psDelDec[k]);

    oracle_nsq_del_dec_scale_states(&NSQ, psDelDec, x16, x_sc_Q10, sLTP, sLTP_Q15,
        (opus_int)subfr, (opus_int)nStates, (opus_int)ltpScaleQ14, gains, pitchL,
        (opus_int)signalType, (opus_int)decisionDelay, (opus_int)subfr_length,
        (opus_int)ltpMemLen);

    for (uint32_t i = 0; i < subfr_length; i++)
      if (!write_i32(x_sc_Q10[i])) return 1;
    for (int i = 0; i < SLTP_SHP_LEN; i++)
      if (!write_i32(NSQ.sLTP_shp_Q14[i])) return 1;
    for (int i = 0; i < SLTP_Q15_LEN; i++)
      if (!write_i32(sLTP_Q15[i])) return 1;
    if (!write_i32(NSQ.prev_gain_Q16)) return 1;
    for (uint32_t k = 0; k < nStates; k++)
      payload_write_state(&psDelDec[k]);
  }

  return 0;
}
