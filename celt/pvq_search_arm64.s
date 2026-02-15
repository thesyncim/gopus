#include "textflag.h"

// func pvqSearchBestPos(absX []float32, y []float32, xy float64, yy float64, n int) int
//
// Finds the position (0..n-1) with the best rate-distortion score for
// placing a pulse in the PVQ greedy search. Uses scalar float32 arithmetic.
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
	FCVTDS F10, F10               // float64 -> float32
	FMOVD yy+56(FP), F11
	FCVTDS F11, F11               // float64 -> float32
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
	FMOVS (R0)(R3<<2), F0        // absX[j]
	FADDS F10, F0, F0             // rxy
	FMOVS (R1)(R3<<2), F1        // y[j]
	FADDS F11, F1, F1             // ryy
	FMULS F0, F0, F2              // num
	FMULS F13, F2, F3             // lhs = bestDen * num
	FMULS F1, F12, F4             // rhs = ryy * bestNum
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

// func pvqSearchPulseLoop(absX, y []float32, iy []int, xy, yy float64, n, pulsesLeft int) (float64, float64)
//
// Places pulsesLeft pulses one at a time, merging the outer pulse loop and
// inner position search into a single assembly call.
//
// Stack frame layout (FP offsets):
//   absX:       0(FP)  = base+0, len+8, cap+16   (24 bytes)
//   y:          24(FP) = base+24, len+32, cap+40  (24 bytes)
//   iy:         48(FP) = base+48, len+56, cap+64  (24 bytes)
//   xy:         72(FP) (float64)
//   yy:         80(FP) (float64)
//   n:          88(FP) (int)
//   pulsesLeft: 96(FP) (int)
//   ret0 (xy):  104(FP) (float64)
//   ret1 (yy):  112(FP) (float64)
//
// Register allocation:
//   R0  = absX base
//   R1  = y base
//   R2  = iy base
//   R3  = n
//   R4  = pulsesLeft (outer counter)
//   R5  = j (inner counter)
//   R6  = bestID
//   R7  = temp
//   F16 = xy (float32, updated per pulse)
//   F17 = yy (float32, updated per pulse)
//   F18 = bestNum
//   F19 = bestDen
//   F20 = constant 1.0f
//   F21 = constant 2.0f
//   F0-F5 = temporaries
TEXT ·pvqSearchPulseLoop(SB), NOSPLIT, $0-120
	MOVD  absX_base+0(FP), R0
	MOVD  y_base+24(FP), R1
	MOVD  iy_base+48(FP), R2
	FMOVD xy+72(FP), F16
	FCVTDS F16, F16               // float64 -> float32
	FMOVD yy+80(FP), F17
	FCVTDS F17, F17               // float64 -> float32
	MOVD  n+88(FP), R3
	MOVD  pulsesLeft+96(FP), R4

	// Load constants
	FMOVS $1.0, F20               // 1.0f
	FMOVS $2.0, F21               // 2.0f

	// If pulsesLeft <= 0 or n <= 0, return immediately
	CBZ   R4, pl_done
	CBZ   R3, pl_done

pl_outer:
	// yy += 1
	FADDS F20, F17, F17

	// Inner search: find bestID for this pulse
	// Init: position 0
	FMOVS (R0), F0                // absX[0]
	FADDS F16, F0, F0             // rxy = xy + absX[0]
	FMOVS (R1), F1                // y[0]
	FADDS F17, F1, F19            // bestDen = yy + y[0]
	FMULS F0, F0, F18             // bestNum = rxy * rxy
	MOVD  ZR, R6                  // bestID = 0

	CMP   $2, R3
	BLT   pl_update

	MOVD  $1, R5                  // j = 1

	// Check if we can do 2x unrolled loop (need j+1 < n, i.e. j < n-1)
	SUB   $1, R3, R9             // R9 = n-1
	CMP   R9, R5
	BGE   pl_inner_tail

pl_inner2:
	// --- Iteration j ---
	FMOVS (R0)(R5<<2), F0        // absX[j]
	FADDS F16, F0, F0             // rxy
	FMOVS (R1)(R5<<2), F1        // y[j]
	FADDS F17, F1, F1             // ryy
	FMULS F0, F0, F2              // num = rxy^2
	FMULS F19, F2, F3             // lhs = bestDen * num
	FMULS F1, F18, F4             // rhs = ryy * bestNum
	FCMPS F4, F3
	BLE   pl_skip1
	FMOVS F1, F19                 // bestDen = ryy
	FMOVS F2, F18                 // bestNum = num
	MOVD  R5, R6                  // bestID = j
pl_skip1:

	// --- Iteration j+1 ---
	ADD   $1, R5, R7             // R7 = j+1
	FMOVS (R0)(R7<<2), F0        // absX[j+1]
	FADDS F16, F0, F0
	FMOVS (R1)(R7<<2), F1        // y[j+1]
	FADDS F17, F1, F1
	FMULS F0, F0, F2
	FMULS F19, F2, F3
	FMULS F1, F18, F4
	FCMPS F4, F3
	BLE   pl_skip2
	FMOVS F1, F19
	FMOVS F2, F18
	MOVD  R7, R6                  // bestID = j+1
pl_skip2:

	ADD   $2, R5
	CMP   R9, R5
	BLT   pl_inner2

pl_inner_tail:
	// Handle last element if n is even (j == n-1)
	CMP   R3, R5
	BGE   pl_update

	FMOVS (R0)(R5<<2), F0
	FADDS F16, F0, F0
	FMOVS (R1)(R5<<2), F1
	FADDS F17, F1, F1
	FMULS F0, F0, F2
	FMULS F19, F2, F3
	FMULS F1, F18, F4
	FCMPS F4, F3
	BLE   pl_update
	FMOVS F1, F19
	FMOVS F2, F18
	MOVD  R5, R6

pl_update:
	// xy += absX[bestID]
	FMOVS (R0)(R6<<2), F0
	FADDS F0, F16, F16

	// yy += y[bestID]
	FMOVS (R1)(R6<<2), F0
	FADDS F0, F17, F17

	// y[bestID] += 2
	FMOVS (R1)(R6<<2), F0
	FADDS F21, F0, F0
	FMOVS F0, (R1)(R6<<2)

	// iy[bestID]++ (int64, 8 bytes per element)
	LSL   $3, R6, R7
	ADD   R2, R7
	MOVD  (R7), R8
	ADD   $1, R8
	MOVD  R8, (R7)

	// Decrement outer counter
	SUB   $1, R4
	CBNZ  R4, pl_outer

pl_done:
	// Convert float32 results back to float64
	FCVTSD F16, F16
	FCVTSD F17, F17
	FMOVD  F16, ret+104(FP)
	FMOVD  F17, ret1+112(FP)
	RET

// func pvqExtractAbsSign(x []float64, absX []float32, y []float32, signx []int, iy []int, n int)
//
// Converts float64 input to float32 absolute values, extracts sign bits,
// and zeros y and iy arrays. Scalar loop — savings come from eliminating
// Go loop overhead and bounds checks.
//
// Stack frame layout (FP offsets):
//   x:      0(FP) = base+0, len+8, cap+16       (24 bytes)
//   absX:   24(FP) = base+24, len+32, cap+40     (24 bytes)
//   y:      48(FP) = base+48, len+56, cap+64     (24 bytes)
//   signx:  72(FP) = base+72, len+80, cap+88     (24 bytes)
//   iy:     96(FP) = base+96, len+104, cap+112   (24 bytes)
//   n:      120(FP)
//
// Register allocation:
//   R0 = x base
//   R1 = absX base
//   R2 = y base
//   R3 = signx base
//   R4 = iy base
//   R5 = n
//   R6 = j (counter)
//   F0 = temp float64
//   F1 = temp float32
TEXT ·pvqExtractAbsSign(SB), NOSPLIT, $0-128
	MOVD  x_base+0(FP), R0
	MOVD  absX_base+24(FP), R1
	MOVD  y_base+48(FP), R2
	MOVD  signx_base+72(FP), R3
	MOVD  iy_base+96(FP), R4
	MOVD  n+120(FP), R5

	CBZ   R5, eas_done
	MOVD  ZR, R6                  // j = 0

eas_loop:
	// Load x[j] as float64
	FMOVD (R0)(R6<<3), F0

	// Zero y[j] and iy[j]
	MOVW  ZR, (R2)(R6<<2)        // y[j] = 0 (float32 = 4 bytes)
	MOVD  ZR, (R4)(R6<<3)        // iy[j] = 0 (int64 = 8 bytes)

	// Check sign: if x[j] < 0, signx = 1, absX = float32(-x)
	// else signx = 0, absX = float32(x)
	FABSD  F0, F1                 // F1 = |x[j]| as float64
	FCVTDS F1, F1                 // F1 = float32(|x[j]|)
	FMOVS  F1, (R1)(R6<<2)       // absX[j] = float32(|x[j]|)

	// Extract sign bit: FCMPD with zero, set signx
	FCMPD  $(0.0), F0
	CSET   MI, R7                 // R7 = 1 if F0 < 0, else 0
	MOVD   R7, (R3)(R6<<3)       // signx[j] = sign (int64 = 8 bytes)

	ADD    $1, R6
	CMP    R5, R6
	BLT    eas_loop

eas_done:
	RET
