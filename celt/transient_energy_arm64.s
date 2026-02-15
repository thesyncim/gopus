#include "textflag.h"

// WORD-encoded instructions:
//   FCVTN  Vd.2S, Vn.D2  = 0x0E616800 | (Vn<<5) | Vd

// func transientEnergyPairs(tmp []float64, x2out []float32, len2 int) float64
//
// Computes energy of sample pairs: x2[i] = float32(tmp[2*i])^2 + float32(tmp[2*i+1])^2
// Returns sum of all x2 values as float64 (containing float32 value).
//
// Register allocation:
//   R0  = tmp base
//   R1  = x2out base
//   R2  = len2
//   R3  = i (counter)
//   F16 = scalar mean accumulator (float32)
//   F0-F5 = temporaries
TEXT ·transientEnergyPairs(SB), NOSPLIT, $0-64
	MOVD  tmp_base+0(FP), R0
	MOVD  x2out_base+24(FP), R1
	MOVD  len2+48(FP), R2

	// Zero accumulator
	FMOVS ZR, F16

	CBZ   R2, te_done
	MOVD  ZR, R3

te_loop:
	// Load tmp[2*i] and tmp[2*i+1] as float64
	LSL   $4, R3, R4             // R4 = i * 16 (2 float64 = 16 bytes)
	ADD   R0, R4                 // R4 = &tmp[2*i]
	FMOVD (R4), F0               // tmp[2*i]
	FMOVD 8(R4), F1              // tmp[2*i+1]

	// Convert to float32
	FCVTDS F0, F2                 // t0 = float32(tmp[2*i])
	FCVTDS F1, F3                 // t1 = float32(tmp[2*i+1])

	// x2 = t0*t0 + t1*t1 (separate ops to match Go rounding)
	FMULS  F2, F2, F4            // t0*t0
	FMULS  F3, F3, F5            // t1*t1
	FADDS  F5, F4, F4            // x2 = t0*t0 + t1*t1

	// Store x2out[i]
	FMOVS  F4, (R1)(R3<<2)

	// Accumulate mean
	FADDS  F4, F16, F16

	ADD    $1, R3
	CMP    R2, R3
	BLT    te_loop

te_done:
	FCVTSD F16, F16
	FMOVD  F16, ret+56(FP)
	RET
