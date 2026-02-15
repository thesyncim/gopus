#include "textflag.h"

// func transientEnergyPairs(tmp []float64, x2out []float32, len2 int) float64
//
// Computes energy of sample pairs: x2[i] = float32(tmp[2*i])^2 + float32(tmp[2*i+1])^2
// Returns sum of all x2 values as float64 (containing float32 value).
//
// Register allocation:
//   AX  = tmp base
//   BX  = x2out base
//   CX  = len2
//   DX  = i (counter)
//   X8  = mean accumulator (float32 scalar)
//   X0-X3 = temporaries
TEXT ·transientEnergyPairs(SB), NOSPLIT, $0-64
	MOVQ  tmp_base+0(FP), AX
	MOVQ  x2out_base+24(FP), BX
	MOVQ  len2+48(FP), CX

	VXORPS X8, X8, X8

	TESTQ CX, CX
	JLE   te_done
	XORQ  DX, DX

te_loop:
	// Load tmp[2*i] and tmp[2*i+1]
	LEAQ  (AX)(DX*8), R8
	LEAQ  (R8)(DX*8), R8         // R8 = &tmp[2*i] = base + i*16
	VMOVSD (R8), X0               // tmp[2*i]
	VMOVSD 8(R8), X1              // tmp[2*i+1]

	// Convert to float32
	VCVTSD2SS X0, X0, X0         // t0
	VCVTSD2SS X1, X1, X1         // t1

	// x2 = t0*t0 + t1*t1
	VMULSS X0, X0, X2            // t0*t0
	VMULSS X1, X1, X3            // t1*t1
	VADDSS X3, X2, X2            // x2 = t0*t0 + t1*t1

	// Store x2out[i]
	VMOVSS X2, (BX)(DX*4)

	// Accumulate mean
	VADDSS X2, X8, X8

	INCQ   DX
	CMPQ   DX, CX
	JLT    te_loop

te_done:
	VCVTSS2SD X8, X8, X8
	VMOVSD X8, ret+56(FP)
	VZEROUPPER
	RET
