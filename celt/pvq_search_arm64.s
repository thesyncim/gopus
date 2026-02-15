#include "textflag.h"

// func pvqSearchBestPos(absX []float32, y []float32, xy float64, yy float64, n int) int
//
// Finds the position (0..n-1) with the best rate-distortion score for
// placing a pulse in the PVQ greedy search. Uses scalar float32 arithmetic
// with optimized register allocation to minimize overhead.
//
// Register allocation:
//   R0  = absX base pointer
//   R1  = y base pointer
//   R2  = n
//   R3  = j (loop counter)
//   R4  = bestID
//   F10 = xy (float32, constant)
//   F11 = yy (float32, constant)
//   F12 = bestNum
//   F13 = bestDen
//   F0-F5 = temporaries
TEXT ·pvqSearchBestPos(SB), NOSPLIT, $0-80
	MOVD  absX_base+0(FP), R0
	MOVD  y_base+24(FP), R1
	FMOVD xy+48(FP), F10
	FCVTDS F10, F10               // float64 → float32
	FMOVD yy+56(FP), F11
	FCVTDS F11, F11               // float64 → float32
	MOVD  n+64(FP), R2

	// If n <= 0, return 0
	CMP  $1, R2
	BLT  pvq_ret_zero

	// Init: position 0
	FMOVS (R0), F0                // absX[0]
	FADDS F10, F0, F0             // rxy = xy + absX[0]
	FMOVS (R1), F1                // y[0]
	FADDS F11, F1, F13            // bestDen = yy + y[0]
	FMULS F0, F0, F12             // bestNum = rxy * rxy
	MOVD  ZR, R4                  // bestID = 0

	// If n == 1, done
	CMP  $2, R2
	BLT  pvq_done

	MOVD $1, R3                   // j = 1

pvq_loop:
	// rxy = xy + absX[j]
	FMOVS (R0)(R3<<2), F0        // absX[j]
	FADDS F10, F0, F0             // rxy

	// ryy = yy + y[j]
	FMOVS (R1)(R3<<2), F1        // y[j]
	FADDS F11, F1, F1             // ryy

	// num = rxy * rxy
	FMULS F0, F0, F2              // num

	// lhs = bestDen * num
	FMULS F13, F2, F3             // lhs

	// rhs = ryy * bestNum
	FMULS F1, F12, F4             // rhs

	// if lhs > rhs: update best
	FCMPS F4, F3
	BLE   pvq_next

	FMOVS F1, F13                 // bestDen = ryy
	FMOVS F2, F12                 // bestNum = num
	MOVD  R3, R4                  // bestID = j

pvq_next:
	ADD  $1, R3
	CMP  R2, R3
	BLT  pvq_loop

pvq_done:
	MOVD R4, ret+72(FP)
	RET

pvq_ret_zero:
	MOVD ZR, ret+72(FP)
	RET
