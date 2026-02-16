#include "textflag.h"

// WORD-encoded instructions not supported by Go assembler:
//   FABS Vd.2D, Vn.2D       = 0x4EE0F800 | (Vn<<5) | Vd
//   FADD Vd.2D, Vn.2D, Vm.2D = 0x4E60D400 | (Vm<<16) | (Vn<<5) | Vd
//   FADDP Dd, Vn.2D          = 0x7E70D800 | (Vn<<5) | Vd

// func absSum(x []float64) float64
//
// Computes sum of absolute values of a float64 slice using NEON SIMD.
// Uses 2 vector accumulators (4 float64 lanes total) for throughput.
//
// Register allocation:
//   R0  = x base pointer
//   R1  = length
//   V0  = accumulator 0 (2x float64)
//   V1  = accumulator 1 (2x float64)
//   V2, V3 = temporaries
TEXT ·absSum(SB), NOSPLIT, $0-32
	MOVD  x_base+0(FP), R0
	MOVD  x_len+8(FP), R1

	// Zero accumulators
	VEOR  V0.B16, V0.B16, V0.B16
	VEOR  V1.B16, V1.B16, V1.B16

	// If length == 0, return 0
	CBZ   R1, as_done

	// Main loop: 4 elements per iteration
	CMP   $4, R1
	BLT   as_tail2

as_loop4:
	VLD1.P 32(R0), [V2.D2, V3.D2]   // load 4 float64
	WORD $0x4EE0F842                  // FABS V2.2D, V2.2D
	WORD $0x4EE0F863                  // FABS V3.2D, V3.2D
	WORD $0x4E62D400                  // FADD V0.2D, V0.2D, V2.2D
	WORD $0x4E63D421                  // FADD V1.2D, V1.2D, V3.2D
	SUB    $4, R1
	CMP    $4, R1
	BGE    as_loop4

as_tail2:
	// Handle 2 remaining elements
	CMP   $2, R1
	BLT   as_tail1
	VLD1.P 16(R0), [V2.D2]
	WORD $0x4EE0F842                  // FABS V2.2D, V2.2D
	WORD $0x4E62D400                  // FADD V0.2D, V0.2D, V2.2D
	SUB    $2, R1

as_tail1:
	// Handle 1 remaining element
	CBZ   R1, as_reduce
	FMOVD (R0), F2
	FABSD  F2, F2
	FADDD  F2, F0, F0                // F0 is low lane of V0

as_reduce:
	// Combine accumulators: V0 += V1
	WORD $0x4E61D400                  // FADD V0.2D, V0.2D, V1.2D
	// Horizontal sum of V0
	WORD $0x7E70D800                  // FADDP D0, V0.2D

as_done:
	FMOVD  F0, ret+24(FP)
	RET
