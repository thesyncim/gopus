#include "textflag.h"

// func sumOfSquaresF64toF32(x []float64, n int) float64
//
// Converts float64 elements to float32 and accumulates sum of squares
// as float32. Uses CVTPD2PS (f64→f32) + MULPS + ADDPS.
//
// Register allocation:
//   AX  = x base pointer
//   CX  = n
//   X8  = float32 accumulator
//   X0-X3 = temporaries
TEXT ·sumOfSquaresF64toF32(SB), NOSPLIT, $0-40
	MOVQ  x_base+0(FP), AX
	MOVQ  n+24(FP), CX

	// Zero accumulator
	VXORPS X8, X8, X8

	TESTQ CX, CX
	JLE   sq_reduce

	// Main loop: 2 float64 elements per iteration
	// CVTPD2PS converts 2 float64 -> 2 float32 (lower lanes of XMM)
	CMPQ  CX, $2
	JLT   sq_tail1

sq_loop2:
	VMOVUPD (AX), X0              // load 2 float64
	VCVTPD2PSX X0, X0             // narrow to 2 float32 in lower lanes
	VMULPS  X0, X0, X0            // v * v (only lower 2 lanes meaningful)
	VADDPS  X0, X8, X8            // acc += v*v
	ADDQ    $16, AX
	SUBQ    $2, CX
	CMPQ    CX, $2
	JGE     sq_loop2

sq_tail1:
	TESTQ CX, CX
	JLE   sq_reduce
	VMOVSD (AX), X0
	VCVTSD2SS X0, X0, X0
	VMULSS  X0, X0, X0
	VADDSS  X0, X8, X8

sq_reduce:
	// Horizontal sum of X8 (4 float32 lanes -> scalar)
	// X8 = [a, b, c, d] where c,d are typically 0
	VMOVHLPS X8, X0, X0          // X0 = [c, d, ?, ?]
	VADDPS   X0, X8, X8          // X8 = [a+c, b+d, ?, ?]
	VPSHUFD  $1, X8, X0          // X0 = [b+d, ?, ?, ?]
	VADDSS   X0, X8, X8          // X8 = [a+c+b+d, ?, ?, ?]

	// Convert float32 result to float64
	VCVTSS2SD X8, X8, X8
	VMOVSD X8, ret+32(FP)
	VZEROUPPER
	RET
