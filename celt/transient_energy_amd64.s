#include "textflag.h"

// func transientEnergyPairs(tmp []float64, x2out []float32, len2 int) float64
//
// Computes energy of sample pairs: x2[i] = float32(tmp[2*i])^2 + float32(tmp[2*i+1])^2
// Returns sum of all x2 values as float64 (containing float32 value).
//
// Register allocation:
//   AX  = tmp pointer (advances)
//   BX  = x2out pointer (advances)
//   CX  = remaining count
//   X8  = mean accumulator (float32 scalar)
//   X0-X5, Y0 = temporaries
TEXT ·transientEnergyPairs(SB), NOSPLIT, $0-64
	MOVQ  tmp_base+0(FP), AX
	MOVQ  x2out_base+24(FP), BX
	MOVQ  len2+48(FP), CX

	VXORPS X8, X8, X8

	TESTQ CX, CX
	JLE   te_done

	// Main loop: 2 pairs per iteration (4 float64 → 4 float32 → 2 x2 values)
	CMPQ  CX, $2
	JLT   te_scalar

te_loop2:
	// Load 4 float64 (2 pairs) and convert to 4 float32
	VMOVUPD (AX), Y0                 // 4 float64 in YMM
	VCVTPD2PSY Y0, X0               // 4 float32 in XMM low

	// Square all 4 values (separate MUL, not FMA — matches Go rounding)
	VMULPS  X0, X0, X0              // [t0^2, t1^2, t2^2, t3^2]

	// Pairwise add: VHADDPS gives [t0^2+t1^2, t2^2+t3^2, ...]
	VHADDPS X0, X0, X0              // X0 = [x2_0, x2_1, x2_0, x2_1]

	// Store 2 x2 values (low 64 bits = 2 consecutive float32)
	VMOVSD  X0, (BX)

	// Scalar accumulate mean in order: mean += x2[i], mean += x2[i+1]
	VADDSS  X0, X8, X8              // mean += x2[i]
	VPSHUFD $1, X0, X1              // X1[0] = x2[i+1]
	VADDSS  X1, X8, X8              // mean += x2[i+1]

	ADDQ    $32, AX                  // advance tmp by 4 float64
	ADDQ    $8, BX                   // advance x2out by 2 float32
	SUBQ    $2, CX
	CMPQ    CX, $2
	JGE     te_loop2

te_scalar:
	TESTQ CX, CX
	JLE   te_done

	// Handle 1 remaining pair
	VMOVSD (AX), X0                  // tmp[2*i]
	VMOVSD 8(AX), X1                // tmp[2*i+1]
	VCVTSD2SS X0, X0, X0
	VCVTSD2SS X1, X1, X1
	VMULSS  X0, X0, X2              // t0*t0
	VMULSS  X1, X1, X3              // t1*t1
	VADDSS  X3, X2, X2              // x2 = t0*t0 + t1*t1
	VMOVSS  X2, (BX)                // store x2out[i]
	VADDSS  X2, X8, X8              // mean += x2

te_done:
	VCVTSS2SD X8, X8, X8
	VMOVSD X8, ret+56(FP)
	VZEROUPPER
	RET
