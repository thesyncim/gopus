#include "textflag.h"

// func celtInnerProd(x, y []float64, length int) float64
TEXT ·celtInnerProd(SB), NOSPLIT, $0-64
	MOVQ x_base+0(FP), SI   // x ptr
	MOVQ y_base+24(FP), DI  // y ptr
	MOVQ length+48(FP), CX  // length

	XORPD X0, X0  // sum vec {0,0}
	XORPD X1, X1  // sum vec {0,0}

	TESTQ CX, CX
	JLE celtInnerProd_reduce

	CMPQ CX, $4
	JLT celtInnerProd_tail

celtInnerProd_loop4:
	MOVUPD 0(SI), X2
	MOVUPD 0(DI), X3
	MULPD X3, X2
	ADDPD X2, X0

	MOVUPD 16(SI), X2
	MOVUPD 16(DI), X3
	MULPD X3, X2
	ADDPD X2, X1

	ADDQ $32, SI
	ADDQ $32, DI
	SUBQ $4, CX
	CMPQ CX, $4
	JGE celtInnerProd_loop4

celtInnerProd_tail:
	ADDPD X1, X0
	TESTQ CX, CX
	JZ celtInnerProd_reduce

celtInnerProd_tail_loop:
	MOVSD 0(SI), X2
	MOVSD 0(DI), X3
	MULSD X3, X2
	ADDSD X2, X0
	ADDQ $8, SI
	ADDQ $8, DI
	DECQ CX
	JNZ celtInnerProd_tail_loop

celtInnerProd_reduce:
	// Horizontal sum: X0 = {lo, hi}
	MOVHLPS X0, X1
	ADDSD X1, X0
	MOVSD X0, ret+56(FP)
	RET

// func dualInnerProd(x, y1, y2 []float64, length int) (float64, float64)
TEXT ·dualInnerProd(SB), NOSPLIT, $0-96
	MOVQ x_base+0(FP), SI    // x ptr
	MOVQ y1_base+24(FP), DI  // y1 ptr
	MOVQ y2_base+48(FP), DX  // y2 ptr
	MOVQ length+72(FP), CX   // length

	XORPD X0, X0  // sum1 vec
	XORPD X1, X1  // sum2 vec

	TESTQ CX, CX
	JLE dualInnerProd_reduce

	CMPQ CX, $2
	JLT dualInnerProd_tail

dualInnerProd_loop2:
	MOVUPD 0(SI), X4      // {x[i], x[i+1]}
	MOVUPD 0(DI), X5      // {y1[i], y1[i+1]}
	MOVUPD 0(DX), X6      // {y2[i], y2[i+1]}
	MOVAPD X4, X7          // copy x
	MULPD X5, X4           // x * y1
	MULPD X6, X7           // x * y2
	ADDPD X4, X0           // sum1 += x*y1
	ADDPD X7, X1           // sum2 += x*y2

	ADDQ $16, SI
	ADDQ $16, DI
	ADDQ $16, DX
	SUBQ $2, CX
	CMPQ CX, $2
	JGE dualInnerProd_loop2

dualInnerProd_tail:
	TESTQ CX, CX
	JZ dualInnerProd_reduce

	MOVSD 0(SI), X4
	MOVSD 0(DI), X5
	MOVSD 0(DX), X6
	MOVAPD X4, X7
	MULSD X5, X4
	MULSD X6, X7
	ADDSD X4, X0
	ADDSD X7, X1

dualInnerProd_reduce:
	// Horizontal sum of X0 → ret1
	MOVHLPS X0, X2
	ADDSD X2, X0
	MOVSD X0, ret+80(FP)
	// Horizontal sum of X1 → ret2
	MOVHLPS X1, X2
	ADDSD X2, X1
	MOVSD X1, ret1+88(FP)
	RET

// func celtPitchXcorr(x []float64, y []float64, xcorr []float64, length, maxPitch int)
//
// Unrolled-by-2 inner loop: processes x[j] and x[j+1] together, sharing
// the overlapping y values y[i+j+1..i+j+3]. This reduces loads from 10
// to 7 per 2 x elements. Dual accumulators (a/b) break dependency chains.
//
// Register allocation:
//   R8  = x base pointer
//   R9  = y base pointer
//   R10 = xcorr base pointer
//   R11 = length
//   R12 = maxPitch
//   SI  = outer loop index i
//   DI  = maxPitch - 3 (outer4 limit)
//   AX  = x_ptr (inner loop)
//   BX  = y_ptr (inner loop, &y[i])
//   CX  = inner loop counter
//
//   X0..X3   = accumulators "a" (s0a, s1a, s2a, s3a) for even j
//   X8..X11  = accumulators "b" (s0b, s1b, s2b, s3b) for odd j
//   X4       = x[j]
//   X5       = x[j+1]
//   X6       = y[i+j]   (unique to "a")
//   X7       = y[i+j+1] (shared)
//   X12      = y[i+j+2] (shared)
//   X13      = y[i+j+3] (shared)
//   X14      = y[i+j+4] (unique to "b")
TEXT ·celtPitchXcorr(SB), NOSPLIT, $0-88
	MOVQ x_base+0(FP), R8       // x ptr
	MOVQ y_base+24(FP), R9      // y ptr
	MOVQ xcorr_base+48(FP), R10 // xcorr ptr
	MOVQ length+72(FP), R11     // length
	MOVQ maxPitch+80(FP), R12   // maxPitch

	TESTQ R11, R11
	JLE celtPitchXcorr_done
	TESTQ R12, R12
	JLE celtPitchXcorr_done

	XORQ SI, SI             // i = 0
	LEAQ -3(R12), DI        // DI = maxPitch - 3
	TESTQ DI, DI
	JLE celtPitchXcorr_tail_outer

celtPitchXcorr_outer4:
	// Zero all 8 accumulators
	XORPD X0, X0    // s0a
	XORPD X1, X1    // s1a
	XORPD X2, X2    // s2a
	XORPD X3, X3    // s3a
	XORPD X8, X8    // s0b
	XORPD X9, X9    // s1b
	XORPD X10, X10  // s2b
	XORPD X11, X11  // s3b

	MOVQ R8, AX                // x_ptr
	LEAQ (R9)(SI*8), BX       // y_ptr = &y[i]
	MOVQ R11, CX              // inner count = length

	// Check if we can do the unrolled loop (need at least 2 elements)
	CMPQ CX, $2
	JLT celtPitchXcorr_inner_tail

celtPitchXcorr_inner2:
	// Load x[j] and x[j+1]
	MOVSD (AX), X4              // x[j]
	MOVSD 8(AX), X5            // x[j+1]

	// Load 5 y values: y[i+j+0..4]  (7 loads total per 2 x elements)
	MOVSD (BX), X6             // y[i+j]    (only for "a")
	MOVSD 8(BX), X7            // y[i+j+1]  (shared)
	MOVSD 16(BX), X12          // y[i+j+2]  (shared)
	MOVSD 24(BX), X13          // y[i+j+3]  (shared)
	MOVSD 32(BX), X14          // y[i+j+4]  (only for "b")

	// Accumulate "a" set: s_a += x[j] * y[i+j+k] for k=0..3
	VFMADD231SD X4, X6, X0     // s0a += x[j] * y[i+j]
	VFMADD231SD X4, X7, X1     // s1a += x[j] * y[i+j+1]
	VFMADD231SD X4, X12, X2    // s2a += x[j] * y[i+j+2]
	VFMADD231SD X4, X13, X3    // s3a += x[j] * y[i+j+3]

	// Accumulate "b" set: s_b += x[j+1] * y[i+j+1+k] for k=0..3
	VFMADD231SD X5, X7, X8     // s0b += x[j+1] * y[i+j+1]
	VFMADD231SD X5, X12, X9    // s1b += x[j+1] * y[i+j+2]
	VFMADD231SD X5, X13, X10   // s2b += x[j+1] * y[i+j+3]
	VFMADD231SD X5, X14, X11   // s3b += x[j+1] * y[i+j+4]

	ADDQ $16, AX               // x_ptr += 2
	ADDQ $16, BX               // y_ptr += 2
	SUBQ $2, CX
	CMPQ CX, $2
	JGE celtPitchXcorr_inner2

celtPitchXcorr_inner_tail:
	// Handle the odd trailing element (0 or 1 remaining)
	TESTQ CX, CX
	JZ celtPitchXcorr_inner_done

	// 1 element left: process x[j] with y[i+j+0..3]
	MOVSD (AX), X4             // x[j]
	MOVSD (BX), X6             // y[i+j]
	MOVSD 8(BX), X7            // y[i+j+1]
	MOVSD 16(BX), X12          // y[i+j+2]
	MOVSD 24(BX), X13          // y[i+j+3]

	VFMADD231SD X4, X6, X0     // s0a += x[j] * y[i+j]
	VFMADD231SD X4, X7, X1     // s1a += x[j] * y[i+j+1]
	VFMADD231SD X4, X12, X2    // s2a += x[j] * y[i+j+2]
	VFMADD231SD X4, X13, X3    // s3a += x[j] * y[i+j+3]

celtPitchXcorr_inner_done:
	// Combine accumulators: s0 = s0a + s0b, etc.
	ADDSD X8, X0
	ADDSD X9, X1
	ADDSD X10, X2
	ADDSD X11, X3

	// Store xcorr[i..i+3]
	LEAQ (R10)(SI*8), AX
	MOVSD X0, (AX)
	MOVSD X1, 8(AX)
	MOVSD X2, 16(AX)
	MOVSD X3, 24(AX)

	ADDQ $4, SI
	CMPQ SI, DI
	JLT celtPitchXcorr_outer4

celtPitchXcorr_tail_outer:
	CMPQ SI, R12
	JGE celtPitchXcorr_done

celtPitchXcorr_tail_one:
	XORPD X0, X0
	MOVQ R8, AX
	LEAQ (R9)(SI*8), BX
	MOVQ R11, CX

celtPitchXcorr_tail_inner:
	MOVSD (AX), X4
	MOVSD (BX), X5
	MULSD X4, X5
	ADDSD X5, X0
	ADDQ $8, AX
	ADDQ $8, BX
	DECQ CX
	JNZ celtPitchXcorr_tail_inner

	MOVSD X0, (R10)(SI*8)
	INCQ SI
	CMPQ SI, R12
	JLT celtPitchXcorr_tail_one

celtPitchXcorr_done:
	RET
