//go:build arm64 && gopus_spread_asm && gopus_spread_arm64_asm

#include "textflag.h"

// func spreadCountThresholds(x []float64, n int, nf float64) (t0, t1, t2 int)
//
// Counts coefficients below three thresholds using scalar float64 with
// tight register allocation (no bounds checks, no Go overhead).
//
// Register allocation:
//   R0  = x pointer
//   R1  = n (loop counter)
//   R3  = count0 (threshold 0.25)
//   R4  = count1 (threshold 0.0625)
//   R5  = count2 (threshold 0.015625)
//   F20 = nf
//   F21 = 0.25
//   F22 = 0.0625
//   F23 = 0.015625
TEXT ·spreadCountThresholds(SB), NOSPLIT, $0-64
	MOVD  x_base+0(FP), R0
	MOVD  n+24(FP), R1
	FMOVD nf+32(FP), F20

	// Load threshold constants
	MOVD $0x3FD0000000000000, R6    // 0.25
	FMOVD R6, F21
	MOVD $0x3FB0000000000000, R6    // 0.0625
	FMOVD R6, F22
	MOVD $0x3F90000000000000, R6    // 0.015625
	FMOVD R6, F23

	// Zero counters
	MOVD ZR, R3
	MOVD ZR, R4
	MOVD ZR, R5

	CBZ  R1, sc_done

	// Check if we can do 2x unrolled loop
	SUBS $2, R1, R2
	BLT  sc_tail

sc_loop2:
	// Element 0
	FMOVD (R0), F0
	FMULD F0, F0, F1               // x*x
	FMULD F1, F20, F1              // x*x*nf

	FCMPD F21, F1                  // compare 0.25 vs x2N
	CINC  HI, R3, R3              // count0++ if 0.25 > x2N (HI = unsigned higher)
	FCMPD F22, F1
	CINC  HI, R4, R4
	FCMPD F23, F1
	CINC  HI, R5, R5

	// Element 1
	FMOVD 8(R0), F0
	FMULD F0, F0, F1
	FMULD F1, F20, F1

	FCMPD F21, F1
	CINC  HI, R3, R3
	FCMPD F22, F1
	CINC  HI, R4, R4
	FCMPD F23, F1
	CINC  HI, R5, R5

	ADD  $16, R0
	SUBS $2, R2
	BGE  sc_loop2

sc_tail:
	// Check for remaining element (R2 == -1 means we're done, -2 means no tail)
	ADDS $1, R2
	BLT  sc_done

	FMOVD (R0), F0
	FMULD F0, F0, F1
	FMULD F1, F20, F1

	FCMPD F21, F1
	CINC  HI, R3, R3
	FCMPD F22, F1
	CINC  HI, R4, R4
	FCMPD F23, F1
	CINC  HI, R5, R5

sc_done:
	MOVD R3, ret+40(FP)
	MOVD R4, ret1+48(FP)
	MOVD R5, ret2+56(FP)
	RET
