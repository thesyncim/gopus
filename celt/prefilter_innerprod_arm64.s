#include "textflag.h"

// WORD-encoded instructions not supported by Go assembler:
//   FCVTN  Vd.2S, Vn.D2  = 0x0E616800 | (Vn<<5) | Vd
//   FCVTN2 Vd.4S, Vn.D2  = 0x4E616800 | (Vn<<5) | Vd
//   FADDP  Vd.4S, Vn.4S, Vm.4S = 0x6E20D400 | (Vm<<16) | (Vn<<5) | Vd

// func prefilterInnerProd(x, y []float64, length int) float64
//
// Float32-accumulated dot product using NEON.
// Converts float64 inputs to float32 via FCVTN, accumulates with VFMLA .S4.
TEXT ·prefilterInnerProd(SB), NOSPLIT, $0-56
	MOVD x_base+0(FP), R0
	MOVD y_base+24(FP), R1
	MOVD length+48(FP), R2

	// Return 0 if length <= 0
	CMP  $1, R2
	BLT  pip_zero

	VEOR V16.B16, V16.B16, V16.B16   // accumulator = 0

	MOVD $16, R15                     // post-increment stride

	// R3 = length / 4
	LSR  $2, R2, R3
	CBZ  R3, pip_tail

pip_loop4:
	// Load 4 float64 from x, narrow to 4 float32
	VLD1.P (R0)(R15), [V0.D2]
	VLD1.P (R0)(R15), [V1.D2]
	WORD $0x0E616804                  // FCVTN  V4.2S, V0.D2
	WORD $0x4E616824                  // FCVTN2 V4.4S, V1.D2

	// Load 4 float64 from y, narrow to 4 float32
	VLD1.P (R1)(R15), [V0.D2]
	VLD1.P (R1)(R15), [V1.D2]
	WORD $0x0E616805                  // FCVTN  V5.2S, V0.D2
	WORD $0x4E616825                  // FCVTN2 V5.4S, V1.D2

	// FMA: V16 += V4 * V5
	VFMLA V4.S4, V5.S4, V16.S4

	SUBS $1, R3
	BNE  pip_loop4

pip_tail:
	// Handle 2-element remainder
	AND  $2, R2, R3
	CBZ  R3, pip_reduce

	VLD1.P (R0)(R15), [V0.D2]
	WORD $0x0E616804                  // FCVTN  V4.2S, V0.D2

	VLD1.P (R1)(R15), [V0.D2]
	WORD $0x0E616805                  // FCVTN  V5.2S, V0.D2

	VFMLA V4.S4, V5.S4, V16.S4

pip_reduce:
	// Horizontal sum of V16.S4
	WORD $0x6E30D600                  // FADDP V0.4S, V16.4S, V16.4S
	VEXT $4, V0.B16, V0.B16, V1.B16
	FADDS F0, F1, F0

	// Handle odd trailing element
	AND  $1, R2, R3
	CBZ  R3, pip_store

	FMOVD  (R0), F2
	FCVTDS F2, F2
	FMOVD  (R1), F3
	FCVTDS F3, F3
	FMADDS F2, F0, F3, F0

pip_store:
	// Convert float32 sum to float64 and return
	FCVTSD F0, F0
	FMOVD  F0, ret+56(FP)
	RET

pip_zero:
	MOVD ZR, ret+56(FP)
	RET

// func prefilterDualInnerProd(x, y1, y2 []float64, length int) (float64, float64)
//
// Two float32-accumulated dot products sharing x input.
TEXT ·prefilterDualInnerProd(SB), NOSPLIT, $0-96
	MOVD x_base+0(FP), R0
	MOVD y1_base+24(FP), R1
	MOVD y2_base+48(FP), R2
	MOVD length+72(FP), R3

	// Return (0,0) if length <= 0
	CMP  $1, R3
	BLT  dpip_zero

	VEOR V16.B16, V16.B16, V16.B16   // sum1 accumulator
	VEOR V17.B16, V17.B16, V17.B16   // sum2 accumulator

	MOVD $16, R15

	// R4 = length / 4
	LSR  $2, R3, R4
	CBZ  R4, dpip_tail

dpip_loop4:
	// Load 4 float64 from x, narrow to 4 float32
	VLD1.P (R0)(R15), [V0.D2]
	VLD1.P (R0)(R15), [V1.D2]
	WORD $0x0E616804                  // FCVTN  V4.2S, V0.D2
	WORD $0x4E616824                  // FCVTN2 V4.4S, V1.D2

	// Load 4 from y1, narrow, FMA into V16
	VLD1.P (R1)(R15), [V0.D2]
	VLD1.P (R1)(R15), [V1.D2]
	WORD $0x0E616805                  // FCVTN  V5.2S, V0.D2
	WORD $0x4E616825                  // FCVTN2 V5.4S, V1.D2
	VFMLA V4.S4, V5.S4, V16.S4

	// Load 4 from y2, narrow, FMA into V17
	VLD1.P (R2)(R15), [V0.D2]
	VLD1.P (R2)(R15), [V1.D2]
	WORD $0x0E616805                  // FCVTN  V5.2S, V0.D2
	WORD $0x4E616825                  // FCVTN2 V5.4S, V1.D2
	VFMLA V4.S4, V5.S4, V17.S4

	SUBS $1, R4
	BNE  dpip_loop4

dpip_tail:
	// Handle 2-element remainder
	AND  $2, R3, R4
	CBZ  R4, dpip_reduce

	VLD1.P (R0)(R15), [V0.D2]
	WORD $0x0E616804                  // FCVTN  V4.2S, V0.D2

	VLD1.P (R1)(R15), [V0.D2]
	WORD $0x0E616805                  // FCVTN  V5.2S, V0.D2
	VFMLA V4.S4, V5.S4, V16.S4

	VLD1.P (R2)(R15), [V0.D2]
	WORD $0x0E616805                  // FCVTN  V5.2S, V0.D2
	VFMLA V4.S4, V5.S4, V17.S4

dpip_reduce:
	// Horizontal sum of V16 (sum1) and V17 (sum2)
	WORD $0x6E31D600                  // FADDP V0.4S, V16.4S, V17.4S → [s1p0,s1p1,s2p0,s2p1]
	WORD $0x6E20D400                  // FADDP V0.4S, V0.4S, V0.4S   → [sum1,sum2,…,…]

	// V0.S[0] = sum1, V0.S[1] = sum2
	// Handle odd trailing element
	AND  $1, R3, R4
	CBZ  R4, dpip_store

	// Extract sum1 and sum2 before scalar ops
	VEXT   $4, V0.B16, V0.B16, V1.B16  // V1.S[0] = sum2

	FMOVD  (R0), F2
	FCVTDS F2, F2                       // xi = float32(x[last])

	FMOVD  (R1), F3
	FCVTDS F3, F3
	FMADDS F2, F0, F3, F0              // sum1 += xi * y1[last]

	FMOVD  (R2), F3
	FCVTDS F3, F3
	FMADDS F2, F1, F3, F1              // sum2 += xi * y2[last]

	// Widen and store
	FCVTSD F0, F0
	FMOVD  F0, ret+80(FP)
	FCVTSD F1, F1
	FMOVD  F1, ret1+88(FP)
	RET

dpip_store:
	// Extract sum2 from V0.S[1]
	VEXT   $4, V0.B16, V0.B16, V1.B16

	FCVTSD F0, F0
	FMOVD  F0, ret+80(FP)
	FCVTSD F1, F1
	FMOVD  F1, ret1+88(FP)
	RET

dpip_zero:
	MOVD ZR, ret+80(FP)
	MOVD ZR, ret1+88(FP)
	RET
