#include "textflag.h"

// func celtPitchXcorr(x []float64, y []float64, xcorr []float64, length, maxPitch int)
//
// Vectorized 4-way pitch cross-correlation using AVX2+FMA3.
// Inner loop uses VFMADD231PD (4×float64 FMA in YMM registers).
// Outer loop processes 4 correlations at a time; tail handles 0-3 remaining.
TEXT ·celtPitchXcorr(SB), NOSPLIT, $0-88
	// Load arguments (ABI0: all on stack)
	MOVQ x_base+0(FP), DI       // x ptr
	MOVQ y_base+24(FP), SI      // y ptr
	MOVQ xcorr_base+48(FP), DX  // xcorr ptr
	MOVQ length+72(FP), CX      // length
	MOVQ maxPitch+80(FP), R8    // maxPitch

	// Early return if length <= 0 || maxPitch <= 0
	TESTQ CX, CX
	JLE   done
	TESTQ R8, R8
	JLE   done

	// R9 = outer index i (starts at 0)
	XORQ R9, R9
	// R10 = maxPitch - 3  (4-way loop runs while i < R10)
	MOVQ R8, R10
	SUBQ $3, R10
	JLE  outer_tail

outer4:
	// Zero 4 YMM accumulators (4×float64 each)
	VXORPD Y8, Y8, Y8
	VXORPD Y9, Y9, Y9
	VXORPD Y10, Y10, Y10
	VXORPD Y11, Y11, Y11

	// Setup pointers for 4 correlations
	MOVQ DI, AX              // x_ptr = &x[0]
	MOVQ R9, R15
	SHLQ $3, R15             // R15 = i * 8
	LEAQ (SI)(R15*1), BX     // y0_ptr = &y[i]
	LEAQ 8(SI)(R15*1), R11   // y1_ptr = &y[i+1]
	LEAQ 16(SI)(R15*1), R12  // y2_ptr = &y[i+2]
	LEAQ 24(SI)(R15*1), R13  // y3_ptr = &y[i+3]

	// Inner count = length rounded down to multiple of 4
	MOVQ CX, R14
	ANDQ $~3, R14            // R14 = length & ~3
	TESTQ R14, R14
	JZ   reduce4

inner4:
	// Load x quad (4×float64)
	VMOVUPD (AX), Y0
	// Load 4 y quads (one per correlation)
	VMOVUPD (BX), Y1
	VMOVUPD (R11), Y2
	VMOVUPD (R12), Y3
	VMOVUPD (R13), Y4
	// Fused multiply-accumulate: acc += x * y
	VFMADD231PD Y1, Y0, Y8
	VFMADD231PD Y2, Y0, Y9
	VFMADD231PD Y3, Y0, Y10
	VFMADD231PD Y4, Y0, Y11
	// Advance all pointers by 32 bytes (4 doubles)
	ADDQ $32, AX
	ADDQ $32, BX
	ADDQ $32, R11
	ADDQ $32, R12
	ADDQ $32, R13
	// Decrement and loop
	SUBQ $4, R14
	JNZ  inner4

reduce4:
	// Horizontal reduction: YMM [a,b,c,d] → scalar a+b+c+d
	// acc0
	VEXTRACTF128 $1, Y8, X0    // X0 = [c, d]
	VADDPD       X0, X8, X0    // X0 = [a+c, b+d]
	VSHUFPD      $1, X0, X0, X1 // X1 = [b+d, a+c]
	VADDSD       X1, X0, X8    // X8[0] = a+b+c+d
	// acc1
	VEXTRACTF128 $1, Y9, X0
	VADDPD       X0, X9, X0
	VSHUFPD      $1, X0, X0, X1
	VADDSD       X1, X0, X9
	// acc2
	VEXTRACTF128 $1, Y10, X0
	VADDPD       X0, X10, X0
	VSHUFPD      $1, X0, X0, X1
	VADDSD       X1, X0, X10
	// acc3
	VEXTRACTF128 $1, Y11, X0
	VADDPD       X0, X11, X0
	VSHUFPD      $1, X0, X0, X1
	VADDSD       X1, X0, X11

	// Handle 0-3 remaining elements with scalar FMA
	MOVQ CX, R14
	ANDQ $3, R14
	TESTQ R14, R14
	JZ   store4

scalar_tail4:
	VMOVSD  (AX), X0
	VMOVSD  (BX), X1
	VFMADD231SD X1, X0, X8
	VMOVSD  (R11), X1
	VFMADD231SD X1, X0, X9
	VMOVSD  (R12), X1
	VFMADD231SD X1, X0, X10
	VMOVSD  (R13), X1
	VFMADD231SD X1, X0, X11
	ADDQ $8, AX
	ADDQ $8, BX
	ADDQ $8, R11
	ADDQ $8, R12
	ADDQ $8, R13
	DECQ R14
	JNZ  scalar_tail4

store4:
	// Store 4 results to xcorr[i..i+3]
	MOVQ    R9, R15
	SHLQ    $3, R15
	VMOVSD  X8, (DX)(R15*1)
	VMOVSD  X9, 8(DX)(R15*1)
	VMOVSD  X10, 16(DX)(R15*1)
	VMOVSD  X11, 24(DX)(R15*1)

	// Advance outer index and loop
	ADDQ $4, R9
	CMPQ R9, R10
	JL   outer4

outer_tail:
	// Handle remaining 0-3 correlations (one at a time)
	CMPQ R9, R8
	JGE  done

outer1:
	// Zero single accumulator
	VXORPD Y8, Y8, Y8

	// Setup pointers
	MOVQ DI, AX
	MOVQ R9, R15
	SHLQ $3, R15
	LEAQ (SI)(R15*1), BX

	// Vectorized inner loop (4 elements at a time)
	MOVQ CX, R14
	ANDQ $~3, R14
	TESTQ R14, R14
	JZ   reduce1

inner1:
	VMOVUPD      (AX), Y0
	VMOVUPD      (BX), Y1
	VFMADD231PD  Y1, Y0, Y8
	ADDQ $32, AX
	ADDQ $32, BX
	SUBQ $4, R14
	JNZ  inner1

reduce1:
	// Horizontal reduction
	VEXTRACTF128 $1, Y8, X0
	VADDPD       X0, X8, X0
	VSHUFPD      $1, X0, X0, X1
	VADDSD       X1, X0, X8

	// Scalar tail for remaining 0-3 elements
	MOVQ CX, R14
	ANDQ $3, R14
	TESTQ R14, R14
	JZ   store1

scalar_tail1:
	VMOVSD       (AX), X0
	VMOVSD       (BX), X1
	VFMADD231SD  X1, X0, X8
	ADDQ $8, AX
	ADDQ $8, BX
	DECQ R14
	JNZ  scalar_tail1

store1:
	MOVQ   R9, R15
	SHLQ   $3, R15
	VMOVSD X8, (DX)(R15*1)

	INCQ R9
	CMPQ R9, R8
	JL   outer1

done:
	VZEROUPPER
	RET
