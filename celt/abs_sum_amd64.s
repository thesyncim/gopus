#include "textflag.h"

// func absSum(x []float64) float64
//
// Computes sum of absolute values of a float64 slice using AVX SIMD.
// Uses VANDPD with sign-mask to compute abs, 2 accumulators (4 float64 lanes).
//
// Register allocation:
//   AX  = x base pointer
//   CX  = length
//   X0  = accumulator 0 (2x float64)
//   X1  = accumulator 1 (2x float64)
//   X8  = sign mask (all bits except sign bit)
//   X2, X3 = temporaries
TEXT ·absSum(SB), NOSPLIT, $0-32
	MOVQ  x_base+0(FP), AX
	MOVQ  x_len+8(FP), CX

	// Zero accumulators
	VXORPD X0, X0, X0
	VXORPD X1, X1, X1

	// If length == 0, return 0
	TESTQ CX, CX
	JLE   as_done

	// Build abs mask: 0x7FFFFFFFFFFFFFFF repeated
	MOVQ    $0x7FFFFFFFFFFFFFFF, DX
	VMOVQ   DX, X8
	VPBROADCASTQ X8, X8

	// Main loop: 4 elements per iteration
	CMPQ  CX, $4
	JLT   as_tail2

as_loop4:
	VMOVUPD (AX), X2              // load x[i], x[i+1]
	VMOVUPD 16(AX), X3            // load x[i+2], x[i+3]
	VANDPD  X8, X2, X2            // abs
	VANDPD  X8, X3, X3            // abs
	VADDPD  X2, X0, X0            // acc0 += abs
	VADDPD  X3, X1, X1            // acc1 += abs
	ADDQ    $32, AX
	SUBQ    $4, CX
	CMPQ    CX, $4
	JGE     as_loop4

as_tail2:
	// Handle 2 remaining elements
	CMPQ  CX, $2
	JLT   as_tail1
	VMOVUPD (AX), X2
	VANDPD  X8, X2, X2
	VADDPD  X2, X0, X0
	ADDQ    $16, AX
	SUBQ    $2, CX

as_tail1:
	// Handle 1 remaining element
	TESTQ CX, CX
	JLE   as_reduce
	VMOVSD (AX), X2
	VANDPD  X8, X2, X2
	VADDSD  X2, X0, X0

as_reduce:
	// Combine accumulators: X0 += X1
	VADDPD  X1, X0, X0
	// Horizontal sum: X0[0] + X0[1]
	VPERMILPD $1, X0, X2         // swap lanes
	VADDSD    X2, X0, X0         // sum

as_done:
	VMOVSD X0, ret+24(FP)
	VZEROUPPER
	RET
