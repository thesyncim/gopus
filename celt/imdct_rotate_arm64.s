#include "textflag.h"

// func imdctPreRotateF32(fftIn []complex64, spectrum []float64, trig []float32, n2, n4 int)
//
// IMDCT pre-rotation: for each i in [0, n4):
//   x1 = float32(spectrum[2*i])
//   x2 = float32(spectrum[n2-1-2*i])
//   out[2*i]   = x1*trig[i]     - x2*trig[n4+i]
//   out[2*i+1] = x2*trig[i]     + x1*trig[n4+i]
//
// where out is the float32 backing of fftIn (interleaved re, im).
// spectrum is float64, trig is float32.
TEXT ·imdctPreRotateF32(SB), NOSPLIT, $0-88
	MOVD fftIn_base+0(FP), R0      // out base (float32 pairs)
	MOVD spectrum_base+24(FP), R1   // spectrum base (float64)
	MOVD trig_base+48(FP), R2       // trig base (float32)
	MOVD n2+72(FP), R3              // n2
	MOVD n4+80(FP), R4              // n4

	CMP  $1, R4
	BLT  pre_done

	// R5 = &trig[n4] = R2 + n4*4
	LSL  $2, R4, R5
	ADD  R2, R5, R5

	// R6 = &spectrum[n2-1] for reverse access
	// byte offset = (n2-1)*8
	SUB  $1, R3, R6
	LSL  $3, R6, R6
	ADD  R1, R6, R6

	MOVD R0, R7              // out_ptr
	MOVD R1, R8              // fwd spectrum ptr: &spectrum[2*i] (step +16)
	MOVD R6, R9              // rev spectrum ptr: &spectrum[n2-1-2*i] (step -16)
	MOVD R2, R10             // &trig[i] ptr (step +4)
	MOVD R5, R11             // &trig[n4+i] ptr (step +4)

	MOVD R4, R12             // loop counter = n4

pre_loop:
	// Load spectrum[2*i] as float64, convert to float32
	FMOVD (R8), F0
	FCVTDS F0, F0            // x1 = float32(spectrum[2*i])

	// Load spectrum[n2-1-2*i] as float64, convert to float32
	FMOVD (R9), F1
	FCVTDS F1, F1            // x2 = float32(spectrum[n2-1-2*i])

	// Load trig values
	FMOVS (R10), F2          // t0 = trig[i]
	FMOVS (R11), F3          // t1 = trig[n4+i]

	// out[2*i] = x1*t0 - x2*t1
	FMULS F2, F0, F4         // F4 = x1*t0
	FMSUBS F3, F4, F1, F4    // F4 = F4 - x2*t1

	// out[2*i+1] = x2*t0 + x1*t1
	FMULS F2, F1, F5         // F5 = x2*t0
	FMADDS F3, F5, F0, F5    // F5 = F5 + x1*t1

	// Store pair
	FMOVS F4, (R7)
	FMOVS F5, 4(R7)

	// Advance pointers
	ADD  $8, R7, R7          // out_ptr += 2*sizeof(float32)
	ADD  $16, R8, R8         // fwd spectrum += 2*sizeof(float64)
	SUB  $16, R9, R9         // rev spectrum -= 2*sizeof(float64)
	ADD  $4, R10, R10        // trig[i]++
	ADD  $4, R11, R11        // trig[n4+i]++

	SUBS $1, R12, R12
	BNE  pre_loop

pre_done:
	RET

// func imdctPostRotateF32(buf []float32, trig []float32, n2, n4 int)
//
// IMDCT post-rotation with bidirectional access:
//   limit = (n4+1) >> 1
//   yp0 = 0, yp1 = n2-2
//   for i in [0, limit):
//     yr  = buf[yp0+1]*trig[i]     + buf[yp0]*trig[n4+i]
//     yi  = buf[yp0+1]*trig[n4+i]  - buf[yp0]*trig[i]
//     yr2 = buf[yp1+1]*trig[n4-i-1] + buf[yp1]*trig[n2-i-1]
//     yi2 = buf[yp1+1]*trig[n2-i-1] - buf[yp1]*trig[n4-i-1]
//     buf[yp0]   = yr;  buf[yp1+1] = yi
//     buf[yp1]   = yr2; buf[yp0+1] = yi2
//     yp0 += 2; yp1 -= 2
TEXT ·imdctPostRotateF32(SB), NOSPLIT, $0-64
	MOVD buf_base+0(FP), R0
	MOVD trig_base+24(FP), R1
	MOVD n2+48(FP), R2
	MOVD n4+56(FP), R3

	// limit = (n4+1) >> 1
	ADD  $1, R3, R4
	LSR  $1, R4, R4
	CMP  $1, R4
	BLT  post_done

	// R5 = yp0 byte offset (starts at 0)
	MOVD ZR, R5
	// R6 = yp1 byte offset = (n2-2)*4
	SUB  $2, R2, R6
	LSL  $2, R6, R6

	// Forward trig pointers (advance by +4 each iteration)
	MOVD R1, R7              // &trig[i]
	LSL  $2, R3, R8
	ADD  R1, R8, R8          // &trig[n4+i]

	// Backward trig pointers (advance by -4 each iteration)
	SUB  $1, R3, R9
	LSL  $2, R9, R9
	ADD  R1, R9, R9          // &trig[n4-1]
	SUB  $1, R2, R10
	LSL  $2, R10, R10
	ADD  R1, R10, R10        // &trig[n2-1]

	MOVD R4, R11             // loop counter

post_loop:
	// --- Forward rotation (yp0) ---
	ADD  R0, R5, R12         // &buf[yp0]
	FMOVS 4(R12), F0        // re = buf[yp0+1]
	FMOVS (R12), F1         // im = buf[yp0]
	FMOVS (R7), F2          // t0 = trig[i]
	FMOVS (R8), F3          // t1 = trig[n4+i]

	// yr = re*t0 + im*t1
	FMULS F2, F0, F4
	FMADDS F3, F4, F1, F4

	// yi = re*t1 - im*t0
	FMULS F3, F0, F5
	FMSUBS F2, F5, F1, F5

	// --- Backward rotation (yp1): read before write ---
	ADD  R0, R6, R13         // &buf[yp1]
	FMOVS 4(R13), F6        // re2 = buf[yp1+1]
	FMOVS (R13), F7         // im2 = buf[yp1]

	// Store forward results (reads from yp1 are done)
	FMOVS F4, (R12)         // buf[yp0] = yr
	FMOVS F5, 4(R13)        // buf[yp1+1] = yi

	FMOVS (R9), F2          // t0 = trig[n4-i-1]
	FMOVS (R10), F3         // t1 = trig[n2-i-1]

	// yr2 = re2*t0 + im2*t1
	FMULS F2, F6, F4
	FMADDS F3, F4, F7, F4

	// yi2 = re2*t1 - im2*t0
	FMULS F3, F6, F5
	FMSUBS F2, F5, F7, F5

	// Store backward results
	FMOVS F4, (R13)         // buf[yp1] = yr2
	FMOVS F5, 4(R12)        // buf[yp0+1] = yi2

	// Advance pointers
	ADD  $8, R5, R5          // yp0 += 2 (bytes: +8)
	SUB  $8, R6, R6          // yp1 -= 2 (bytes: -8)
	ADD  $4, R7, R7
	ADD  $4, R8, R8
	SUB  $4, R9, R9
	SUB  $4, R10, R10

	SUBS $1, R11, R11
	BNE  post_loop

post_done:
	RET
