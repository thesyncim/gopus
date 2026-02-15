#include "textflag.h"

// func imdctPreRotateF32(fftIn []complex64, spectrum []float64, trig []float32, n2, n4 int)
//
// IMDCT pre-rotation using scalar SSE. Processes one rotation per iteration.
// spectrum is float64, trig is float32.
TEXT ·imdctPreRotateF32(SB), NOSPLIT, $0-88
	MOVQ fftIn_base+0(FP), DI      // out base (float32 pairs)
	MOVQ spectrum_base+24(FP), SI   // spectrum base (float64)
	MOVQ trig_base+48(FP), DX       // trig base (float32)
	MOVQ n2+72(FP), CX              // n2
	MOVQ n4+80(FP), R8              // n4

	TESTQ R8, R8
	JLE   pre_done

	// R9 = &trig[n4] = DX + n4*4
	MOVQ R8, R9
	SHLQ $2, R9
	ADDQ DX, R9

	// R10 = &spectrum[n2-1] for reverse access = SI + (n2-1)*8
	MOVQ CX, R10
	DECQ R10
	SHLQ $3, R10
	ADDQ SI, R10

	MOVQ DI, AX             // out_ptr
	MOVQ SI, BX             // fwd spectrum ptr
	MOVQ R10, R11            // rev spectrum ptr
	MOVQ DX, R12             // &trig[i]
	MOVQ R9, R13             // &trig[n4+i]

	MOVQ R8, R14             // loop counter

pre_loop:
	// Load spectrum[2*i] as float64, convert to float32
	MOVSD  (BX), X0
	CVTSD2SS X0, X0          // x1 = float32(spectrum[2*i])

	// Load spectrum[n2-1-2*i] as float64, convert to float32
	MOVSD  (R11), X1
	CVTSD2SS X1, X1          // x2 = float32(spectrum[n2-1-2*i])

	// Load trig values
	MOVSS  (R12), X2         // t0 = trig[i]
	MOVSS  (R13), X3         // t1 = trig[n4+i]

	// out[2*i] = x1*t0 - x2*t1
	MOVSS  X0, X4
	MULSS  X2, X4            // X4 = x1*t0
	MOVSS  X1, X5
	MULSS  X3, X5            // X5 = x2*t1
	SUBSS  X5, X4            // X4 = x1*t0 - x2*t1

	// out[2*i+1] = x2*t0 + x1*t1
	MOVSS  X1, X5
	MULSS  X2, X5            // X5 = x2*t0
	MOVSS  X0, X6
	MULSS  X3, X6            // X6 = x1*t1
	ADDSS  X6, X5            // X5 = x2*t0 + x1*t1

	// Store pair
	MOVSS  X4, (AX)
	MOVSS  X5, 4(AX)

	// Advance pointers
	ADDQ $8, AX              // out_ptr += 2*sizeof(float32)
	ADDQ $16, BX             // fwd spectrum += 2*sizeof(float64)
	SUBQ $16, R11            // rev spectrum -= 2*sizeof(float64)
	ADDQ $4, R12             // trig[i]++
	ADDQ $4, R13             // trig[n4+i]++

	DECQ R14
	JNZ  pre_loop

pre_done:
	RET

// func imdctPostRotateF32(buf []float32, trig []float32, n2, n4 int)
//
// IMDCT post-rotation with bidirectional access using scalar SSE.
TEXT ·imdctPostRotateF32(SB), NOSPLIT, $0-64
	MOVQ buf_base+0(FP), DI
	MOVQ trig_base+24(FP), SI
	MOVQ n2+48(FP), DX
	MOVQ n4+56(FP), CX

	// limit = (n4+1) >> 1
	MOVQ CX, R8
	INCQ R8
	SHRQ $1, R8
	TESTQ R8, R8
	JZ   post_done

	// AX = yp0 byte offset (starts at 0)
	XORQ AX, AX
	// BX = yp1 byte offset = (n2-2)*4
	MOVQ DX, BX
	SUBQ $2, BX
	SHLQ $2, BX

	// Forward trig pointers
	MOVQ SI, R9              // &trig[i]
	MOVQ CX, R10
	SHLQ $2, R10
	ADDQ SI, R10             // &trig[n4+i]

	// Backward trig pointers
	MOVQ CX, R11
	DECQ R11
	SHLQ $2, R11
	ADDQ SI, R11             // &trig[n4-1]
	MOVQ DX, R12
	DECQ R12
	SHLQ $2, R12
	ADDQ SI, R12             // &trig[n2-1]

	MOVQ R8, R13             // loop counter

post_loop:
	// --- Forward rotation (yp0) ---
	LEAQ (DI)(AX*1), R14    // &buf[yp0]
	MOVSS 4(R14), X0        // re = buf[yp0+1]
	MOVSS (R14), X1         // im = buf[yp0]
	MOVSS (R9), X2          // t0 = trig[i]
	MOVSS (R10), X3         // t1 = trig[n4+i]

	// yr = re*t0 + im*t1
	MOVSS X0, X4
	MULSS X2, X4            // X4 = re*t0
	MOVSS X1, X5
	MULSS X3, X5            // X5 = im*t1
	ADDSS X5, X4            // X4 = yr

	// yi = re*t1 - im*t0
	MOVSS X0, X5
	MULSS X3, X5            // X5 = re*t1
	MOVSS X1, X6
	MULSS X2, X6            // X6 = im*t0
	SUBSS X6, X5            // X5 = yi

	// --- Backward rotation (yp1): read before write ---
	LEAQ (DI)(BX*1), R15    // &buf[yp1]
	MOVSS 4(R15), X6        // re2 = buf[yp1+1]
	MOVSS (R15), X7         // im2 = buf[yp1]

	// Store forward results
	MOVSS X4, (R14)          // buf[yp0] = yr
	MOVSS X5, 4(R15)         // buf[yp1+1] = yi

	MOVSS (R11), X2          // t0 = trig[n4-i-1]
	MOVSS (R12), X3          // t1 = trig[n2-i-1]

	// yr2 = re2*t0 + im2*t1
	MOVSS X6, X4
	MULSS X2, X4            // X4 = re2*t0
	MOVSS X7, X5
	MULSS X3, X5            // X5 = im2*t1
	ADDSS X5, X4            // X4 = yr2

	// yi2 = re2*t1 - im2*t0
	MOVSS X6, X5
	MULSS X3, X5            // X5 = re2*t1
	MOVSS X7, X8
	MULSS X2, X8            // X8 = im2*t0
	SUBSS X8, X5            // X5 = yi2

	// Store backward results
	MOVSS X4, (R15)          // buf[yp1] = yr2
	MOVSS X5, 4(R14)         // buf[yp0+1] = yi2

	// Advance pointers
	ADDQ $8, AX
	SUBQ $8, BX
	ADDQ $4, R9
	ADDQ $4, R10
	SUBQ $4, R11
	SUBQ $4, R12

	DECQ R13
	JNZ  post_loop

post_done:
	RET
