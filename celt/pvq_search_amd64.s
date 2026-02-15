#include "textflag.h"

// func pvqSearchBestPos(absX []float32, y []float32, xy float64, yy float64, n int) int
//
// Finds the position (0..n-1) with the best rate-distortion score for
// placing a pulse in the PVQ greedy search. Uses scalar float32 arithmetic
// with 2x unrolled inner loop.
//
// Register allocation:
//   AX  = absX base pointer
//   BX  = y base pointer
//   CX  = n
//   DX  = j (loop counter)
//   R8  = bestID
//   X10 = xy (float32, constant)
//   X11 = yy (float32, constant)
//   X12 = bestNum
//   X13 = bestDen
//   X0-X5 = temporaries
TEXT ·pvqSearchBestPos(SB), NOSPLIT, $0-80
	MOVQ  absX_base+0(FP), AX
	MOVQ  y_base+24(FP), BX
	VMOVSD xy+48(FP), X10
	VCVTSD2SS X10, X10, X10      // float64 → float32
	VMOVSD yy+56(FP), X11
	VCVTSD2SS X11, X11, X11      // float64 → float32
	MOVQ  n+64(FP), CX

	// If n <= 0, return 0
	TESTQ CX, CX
	JLE   pvq_ret_zero

	// Init: position 0
	VMOVSS (AX), X0               // absX[0]
	VADDSS X10, X0, X0            // rxy = xy + absX[0]
	VMOVSS (BX), X1               // y[0]
	VADDSS X11, X1, X13           // bestDen = yy + y[0]
	VMULSS X0, X0, X12            // bestNum = rxy * rxy
	XORQ   R8, R8                 // bestID = 0

	// If n == 1, done
	CMPQ CX, $1
	JLE  pvq_done

	MOVQ $1, DX                   // j = 1

	// Check if we can do 2x unrolled loop
	LEAQ -1(CX), R9               // R9 = n-1 (last valid j)
	CMPQ DX, R9
	JGE  pvq_tail

	// R10 = n-1 (unroll limit: process j, j+1)
	MOVQ R9, R10

pvq_loop2:
	// --- Iteration j ---
	VMOVSS (AX)(DX*4), X0        // absX[j]
	VADDSS X10, X0, X0           // rxy
	VMOVSS (BX)(DX*4), X1        // y[j]
	VADDSS X11, X1, X1           // ryy
	VMULSS X0, X0, X2            // num = rxy^2
	VMULSS X13, X2, X3           // lhs = bestDen * num
	VMULSS X1, X12, X4           // rhs = ryy * bestNum
	VUCOMISS X4, X3
	JBE  pvq_skip1
	MOVSS  X1, X13               // bestDen = ryy
	MOVSS  X2, X12               // bestNum = num
	MOVQ   DX, R8                // bestID = j
pvq_skip1:

	// --- Iteration j+1 ---
	LEAQ 1(DX), SI
	VMOVSS (AX)(SI*4), X0
	VADDSS X10, X0, X0
	VMOVSS (BX)(SI*4), X1
	VADDSS X11, X1, X1
	VMULSS X0, X0, X2
	VMULSS X13, X2, X3
	VMULSS X1, X12, X4
	VUCOMISS X4, X3
	JBE  pvq_skip2
	MOVSS  X1, X13
	MOVSS  X2, X12
	MOVQ   SI, R8
pvq_skip2:

	ADDQ $2, DX
	CMPQ DX, R10
	JLT  pvq_loop2

pvq_tail:
	// Handle remaining element if n is even (j == n-1)
	CMPQ DX, CX
	JGE  pvq_done

	VMOVSS (AX)(DX*4), X0
	VADDSS X10, X0, X0
	VMOVSS (BX)(DX*4), X1
	VADDSS X11, X1, X1
	VMULSS X0, X0, X2
	VMULSS X13, X2, X3
	VMULSS X1, X12, X4
	VUCOMISS X4, X3
	JBE  pvq_done
	MOVSS  X1, X13
	MOVSS  X2, X12
	MOVQ   DX, R8

pvq_done:
	MOVQ R8, ret+72(FP)
	VZEROUPPER
	RET

pvq_ret_zero:
	MOVQ $0, ret+72(FP)
	VZEROUPPER
	RET
