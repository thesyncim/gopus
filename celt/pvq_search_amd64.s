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
	VCVTSD2SS X10, X10, X10      // float64 -> float32
	VMOVSD yy+56(FP), X11
	VCVTSD2SS X11, X11, X11      // float64 -> float32
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

	MOVQ R9, R10

pvq_loop2:
	// --- Iteration j ---
	VMOVSS (AX)(DX*4), X0
	VADDSS X10, X0, X0
	VMOVSS (BX)(DX*4), X1
	VADDSS X11, X1, X1
	VMULSS X0, X0, X2
	VMULSS X13, X2, X3
	VMULSS X1, X12, X4
	VUCOMISS X4, X3
	JBE  pvq_skip1
	MOVSS  X1, X13
	MOVSS  X2, X12
	MOVQ   DX, R8
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
//   AX   = absX base
//   BX   = y base
//   R9   = iy base
//   CX   = n
//   R10  = pulsesLeft (outer counter)
//   DX   = j (inner counter)
//   R8   = bestID
//   R11  = temp
//   X14  = xy (float32, updated per pulse)
//   X15  = yy (float32, updated per pulse)
//   X12  = bestNum
//   X13  = bestDen
//   X8   = constant 1.0f
//   X9   = constant 2.0f
//   X0-X5 = temporaries
TEXT ·pvqSearchPulseLoop(SB), NOSPLIT, $0-120
	MOVQ  absX_base+0(FP), AX
	MOVQ  y_base+24(FP), BX
	MOVQ  iy_base+48(FP), R9
	VMOVSD xy+72(FP), X14
	VCVTSD2SS X14, X14, X14      // float64 -> float32
	VMOVSD yy+80(FP), X15
	VCVTSD2SS X15, X15, X15      // float64 -> float32
	MOVQ  n+88(FP), CX
	MOVQ  pulsesLeft+96(FP), R10

	// Load constants
	MOVL  $0x3F800000, R11       // 1.0f
	MOVQ  R11, X8
	MOVL  $0x40000000, R11       // 2.0f
	MOVQ  R11, X9

	// If pulsesLeft <= 0 or n <= 0, return immediately
	TESTQ R10, R10
	JLE   ppl_done
	TESTQ CX, CX
	JLE   ppl_done

ppl_outer:
	// yy += 1
	VADDSS X8, X15, X15

	// Inner search: find bestID for this pulse
	// Init: position 0
	VMOVSS (AX), X0               // absX[0]
	VADDSS X14, X0, X0            // rxy = xy + absX[0]
	VMOVSS (BX), X1               // y[0]
	VADDSS X15, X1, X13           // bestDen = yy + y[0]
	VMULSS X0, X0, X12            // bestNum = rxy * rxy
	XORQ   R8, R8                 // bestID = 0

	CMPQ  CX, $1
	JLE   ppl_update

	MOVQ  $1, DX                  // j = 1

	// Check if we can do 2x unrolled loop
	LEAQ  -1(CX), R11
	CMPQ  DX, R11
	JGE   ppl_tail

ppl_inner2:
	// --- Iteration j ---
	VMOVSS (AX)(DX*4), X0
	VADDSS X14, X0, X0
	VMOVSS (BX)(DX*4), X1
	VADDSS X15, X1, X1
	VMULSS X0, X0, X2
	VMULSS X13, X2, X3
	VMULSS X1, X12, X4
	VUCOMISS X4, X3
	JBE   ppl_skip1
	MOVSS  X1, X13
	MOVSS  X2, X12
	MOVQ   DX, R8
ppl_skip1:

	// --- Iteration j+1 ---
	LEAQ  1(DX), SI
	VMOVSS (AX)(SI*4), X0
	VADDSS X14, X0, X0
	VMOVSS (BX)(SI*4), X1
	VADDSS X15, X1, X1
	VMULSS X0, X0, X2
	VMULSS X13, X2, X3
	VMULSS X1, X12, X4
	VUCOMISS X4, X3
	JBE   ppl_skip2
	MOVSS  X1, X13
	MOVSS  X2, X12
	MOVQ   SI, R8
ppl_skip2:

	ADDQ  $2, DX
	CMPQ  DX, R11
	JLT   ppl_inner2

ppl_tail:
	CMPQ  DX, CX
	JGE   ppl_update

	VMOVSS (AX)(DX*4), X0
	VADDSS X14, X0, X0
	VMOVSS (BX)(DX*4), X1
	VADDSS X15, X1, X1
	VMULSS X0, X0, X2
	VMULSS X13, X2, X3
	VMULSS X1, X12, X4
	VUCOMISS X4, X3
	JBE   ppl_update
	MOVSS  X1, X13
	MOVSS  X2, X12
	MOVQ   DX, R8

ppl_update:
	// xy += absX[bestID]
	VMOVSS (AX)(R8*4), X0
	VADDSS X0, X14, X14

	// yy += y[bestID]
	VMOVSS (BX)(R8*4), X0
	VADDSS X0, X15, X15

	// y[bestID] += 2
	VMOVSS (BX)(R8*4), X0
	VADDSS X9, X0, X0
	VMOVSS X0, (BX)(R8*4)

	// iy[bestID]++ (int64, 8 bytes per element)
	LEAQ  (R9)(R8*8), R11
	INCQ  (R11)

	// Decrement outer counter
	DECQ  R10
	JNZ   ppl_outer

ppl_done:
	// Convert float32 results back to float64
	VCVTSS2SD X14, X14, X14
	VCVTSS2SD X15, X15, X15
	VMOVSD X14, ret+104(FP)
	VMOVSD X15, ret1+112(FP)
	VZEROUPPER
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
//   AX = x base
//   BX = absX base
//   CX = y base
//   DX = signx base
//   R9 = iy base
//   R10 = n
//   R11 = j (counter)
//   X0, X1 = temporaries
TEXT ·pvqExtractAbsSign(SB), NOSPLIT, $0-128
	MOVQ  x_base+0(FP), AX
	MOVQ  absX_base+24(FP), BX
	MOVQ  y_base+48(FP), CX
	MOVQ  signx_base+72(FP), DX
	MOVQ  iy_base+96(FP), R9
	MOVQ  n+120(FP), R10

	TESTQ R10, R10
	JLE   eas_done

	// Build abs mask for float64: 0x7FFFFFFFFFFFFFFF
	MOVQ    $0x7FFFFFFFFFFFFFFF, R8
	VMOVQ   R8, X2

	XORQ  R11, R11               // j = 0

eas_loop:
	// Load x[j] as float64
	VMOVSD (AX)(R11*8), X0

	// Zero y[j] (float32, 4 bytes) and iy[j] (int64, 8 bytes)
	MOVL  $0, (CX)(R11*4)
	MOVQ  $0, (R9)(R11*8)

	// Compute abs: AND with sign mask
	VANDPD X2, X0, X1            // X1 = |x[j]| as float64

	// Convert to float32
	VCVTSD2SS X1, X1, X1
	VMOVSS X1, (BX)(R11*4)       // absX[j] = float32(|x[j]|)

	// Extract sign: test high bit of original x[j]
	// If x[j] < 0, sign bit is set -> signx = 1
	VMOVQ  X0, R8
	SHRQ   $63, R8               // R8 = sign bit (0 or 1)
	MOVQ   R8, (DX)(R11*8)       // signx[j] = 0 or 1

	INCQ   R11
	CMPQ   R11, R10
	JLT    eas_loop

eas_done:
	VZEROUPPER
	RET
