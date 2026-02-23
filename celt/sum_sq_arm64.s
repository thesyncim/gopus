//go:build arm64 && gopus_sum_sq_asm && gopus_sum_sq_arm64_asm

#include "textflag.h"

// WORD-encoded instructions:
//   FCVTN  Vd.2S, Vn.D2  = 0x0E616800 | (Vn<<5) | Vd
//   FADDP  V0.4S, V16.4S, V16.4S = 0x6E30D600  (pairwise add 4S)

// func sumOfSquaresF64toF32(x []float64, n int) float64
//
// Converts float64 elements to float32 and accumulates sum of squares
// as float32. Uses FCVTN (f64→f32 narrow) + VFMLA (multiply-accumulate).
//
// Register allocation:
//   R0  = x base pointer
//   R1  = n
//   V16 = float32 accumulator (4 lanes)
//   V0, V1, V4, V5 = temporaries
TEXT ·sumOfSquaresF64toF32(SB), NOSPLIT, $0-40
	MOVD  x_base+0(FP), R0
	MOVD  n+24(FP), R1

	// Zero accumulator
	VEOR  V16.B16, V16.B16, V16.B16

	CBZ   R1, sq_hreduce

	// Main loop: 4 float64 elements per iteration
	// Load 4 float64 -> narrow to 4 float32 -> VFMLA
	CMP   $4, R1
	BLT   sq_tail2

sq_loop4:
	VLD1.P 32(R0), [V0.D2, V1.D2]    // load 4 float64
	WORD $0x0E616804                   // FCVTN V4.2S, V0.D2
	WORD $0x4E616824                   // FCVTN2 V4.4S, V1.D2
	VFMLA V4.S4, V4.S4, V16.S4        // acc += v * v
	SUB    $4, R1
	CMP    $4, R1
	BGE    sq_loop4

sq_tail2:
	// Handle 2 remaining elements
	CMP   $2, R1
	BLT   sq_tail1
	VLD1.P 16(R0), [V0.D2]
	WORD $0x0E616804                   // FCVTN V4.2S, V0.D2
	// Only lower 2 lanes valid, upper 2 are zero from FCVTN
	VFMLA V4.S4, V4.S4, V16.S4
	SUB    $2, R1

sq_tail1:
	// Handle 1 remaining element
	CBZ   R1, sq_hreduce
	FMOVD (R0), F0
	FCVTDS F0, F1                     // float64 -> float32
	FMULS  F1, F1, F1                 // v * v
	FADDS  F1, F16, F16               // acc[0] += v*v

sq_hreduce:
	// Horizontal sum of V16.4S -> scalar float32 in F16
	WORD $0x6E30D600                   // FADDP V0.4S, V16.4S, V16.4S -> [p0+p1, p2+p3, ...]
	// V0 now has [s01, s23, s01, s23] in lower 4S
	// Need to add V0.S[0] + V0.S[1]
	WORD $0x6E20D400                   // FADDP V0.4S, V0.4S, V0.4S -> [total, ...]

	// Convert float32 result to float64
	FCVTSD F0, F0
	FMOVD  F0, ret+32(FP)
	RET
