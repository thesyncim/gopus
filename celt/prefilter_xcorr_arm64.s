#include "textflag.h"

// WORD-encoded instructions not supported by Go assembler:
//   FCVTN  Vd.2S, Vn.D2  = 0x0E616800 | (Vn<<5) | Vd   (narrow float64→float32, low half)
//   FCVTN2 Vd.4S, Vn.D2  = 0x4E616800 | (Vn<<5) | Vd   (narrow float64→float32, high half)
//   FADDP  Vd.4S, Vn.4S, Vm.4S = 0x6E20D400 | (Vm<<16) | (Vn<<5) | Vd

// func prefilterPitchXcorr(x []float64, y []float64, xcorr []float64, length, maxPitch int)
//
// Float32-accumulated 4-way pitch cross-correlation using ARM64 NEON.
// Converts float64 inputs to float32 via FCVTN, accumulates with VFMLA .S4 (4×float32).
TEXT ·prefilterPitchXcorr(SB), NOSPLIT, $0-88
	MOVD x_base+0(FP), R0
	MOVD y_base+24(FP), R1
	MOVD xcorr_base+48(FP), R2
	MOVD length+72(FP), R3
	MOVD maxPitch+80(FP), R4

	CMP  $1, R3
	BLT  pxc_done
	CMP  $1, R4
	BLT  pxc_done

	MOVD $16, R15
	MOVD ZR, R5
	SUBS $3, R4, R6
	BLE  pxc_outer_tail

pxc_outer4:
	// Zero 4 float32 accumulators V16-V19
	VEOR V16.B16, V16.B16, V16.B16
	VEOR V17.B16, V17.B16, V17.B16
	VEOR V18.B16, V18.B16, V18.B16
	VEOR V19.B16, V19.B16, V19.B16

	// Setup 5 pointers: x_ptr, y0..y3
	MOVD R0, R7
	LSL  $3, R5, R13
	ADD  R1, R13, R8
	ADD  $8, R8, R9
	ADD  $16, R8, R10
	ADD  $24, R8, R11

	// R12 = length / 8 (8-wide main loop)
	LSR  $3, R3, R12
	CBZ  R12, pxc_mid4

pxc_inner8:
	// --- First 4 elements ---
	// Load 4 float64 from x, convert to 4 float32
	VLD1.P (R7)(R15), [V0.D2]
	VLD1.P (R7)(R15), [V1.D2]
	WORD $0x0E616804                    // FCVTN  V4.2S, V0.D2
	WORD $0x4E616824                    // FCVTN2 V4.4S, V1.D2

	// Load next 4 float64 from x, convert to 4 float32
	VLD1.P (R7)(R15), [V2.D2]
	VLD1.P (R7)(R15), [V3.D2]
	WORD $0x0E616845                    // FCVTN  V5.2S, V2.D2
	WORD $0x4E616865                    // FCVTN2 V5.4S, V3.D2

	// y0: 2×(load, convert, FMA into V16)
	VLD1.P (R8)(R15), [V0.D2]
	VLD1.P (R8)(R15), [V1.D2]
	WORD $0x0E616806                    // FCVTN  V6.2S, V0.D2
	WORD $0x4E616826                    // FCVTN2 V6.4S, V1.D2
	VFMLA V4.S4, V6.S4, V16.S4
	VLD1.P (R8)(R15), [V2.D2]
	VLD1.P (R8)(R15), [V3.D2]
	WORD $0x0E616847                    // FCVTN  V7.2S, V2.D2
	WORD $0x4E616867                    // FCVTN2 V7.4S, V3.D2
	VFMLA V5.S4, V7.S4, V16.S4

	// y1: 2×(load, convert, FMA into V17)
	VLD1.P (R9)(R15), [V0.D2]
	VLD1.P (R9)(R15), [V1.D2]
	WORD $0x0E616806                    // FCVTN  V6.2S, V0.D2
	WORD $0x4E616826                    // FCVTN2 V6.4S, V1.D2
	VFMLA V4.S4, V6.S4, V17.S4
	VLD1.P (R9)(R15), [V2.D2]
	VLD1.P (R9)(R15), [V3.D2]
	WORD $0x0E616847                    // FCVTN  V7.2S, V2.D2
	WORD $0x4E616867                    // FCVTN2 V7.4S, V3.D2
	VFMLA V5.S4, V7.S4, V17.S4

	// y2: 2×(load, convert, FMA into V18)
	VLD1.P (R10)(R15), [V0.D2]
	VLD1.P (R10)(R15), [V1.D2]
	WORD $0x0E616806                    // FCVTN  V6.2S, V0.D2
	WORD $0x4E616826                    // FCVTN2 V6.4S, V1.D2
	VFMLA V4.S4, V6.S4, V18.S4
	VLD1.P (R10)(R15), [V2.D2]
	VLD1.P (R10)(R15), [V3.D2]
	WORD $0x0E616847                    // FCVTN  V7.2S, V2.D2
	WORD $0x4E616867                    // FCVTN2 V7.4S, V3.D2
	VFMLA V5.S4, V7.S4, V18.S4

	// y3: 2×(load, convert, FMA into V19)
	VLD1.P (R11)(R15), [V0.D2]
	VLD1.P (R11)(R15), [V1.D2]
	WORD $0x0E616806                    // FCVTN  V6.2S, V0.D2
	WORD $0x4E616826                    // FCVTN2 V6.4S, V1.D2
	VFMLA V4.S4, V6.S4, V19.S4
	VLD1.P (R11)(R15), [V2.D2]
	VLD1.P (R11)(R15), [V3.D2]
	WORD $0x0E616847                    // FCVTN  V7.2S, V2.D2
	WORD $0x4E616867                    // FCVTN2 V7.4S, V3.D2
	VFMLA V5.S4, V7.S4, V19.S4

	SUBS $1, R12, R12
	BNE  pxc_inner8

pxc_mid4:
	// Handle 4-element remainder (length%8 >= 4)
	TST  $4, R3
	BEQ  pxc_tail4

	VLD1.P (R7)(R15), [V0.D2]
	VLD1.P (R7)(R15), [V1.D2]
	WORD $0x0E616804                    // FCVTN  V4.2S, V0.D2
	WORD $0x4E616824                    // FCVTN2 V4.4S, V1.D2

	VLD1.P (R8)(R15), [V0.D2]
	VLD1.P (R8)(R15), [V1.D2]
	WORD $0x0E616805                    // FCVTN  V5.2S, V0.D2
	WORD $0x4E616825                    // FCVTN2 V5.4S, V1.D2
	VFMLA V4.S4, V5.S4, V16.S4

	VLD1.P (R9)(R15), [V0.D2]
	VLD1.P (R9)(R15), [V1.D2]
	WORD $0x0E616805                    // FCVTN  V5.2S, V0.D2
	WORD $0x4E616825                    // FCVTN2 V5.4S, V1.D2
	VFMLA V4.S4, V5.S4, V17.S4

	VLD1.P (R10)(R15), [V0.D2]
	VLD1.P (R10)(R15), [V1.D2]
	WORD $0x0E616805                    // FCVTN  V5.2S, V0.D2
	WORD $0x4E616825                    // FCVTN2 V5.4S, V1.D2
	VFMLA V4.S4, V5.S4, V18.S4

	VLD1.P (R11)(R15), [V0.D2]
	VLD1.P (R11)(R15), [V1.D2]
	WORD $0x0E616805                    // FCVTN  V5.2S, V0.D2
	WORD $0x4E616825                    // FCVTN2 V5.4S, V1.D2
	VFMLA V4.S4, V5.S4, V19.S4

pxc_tail4:
	// Handle 2-element remainder (length%4 >= 2)
	AND  $2, R3, R13
	CBZ  R13, pxc_reduce4

	// Load 2 float64 from x, convert to 2 float32 in low lanes (upper lanes zero)
	VLD1.P (R7)(R15), [V0.D2]
	WORD $0x0E616804                    // FCVTN  V4.2S, V0.D2 (V4.S[2:3]=0)

	// Use .S4 not .S2: FCVTN zeros V4.S[2:3] and V5.S[2:3], so the extra
	// FMA lanes add 0*0=0 to the accumulator, preserving V16.S[2:3].
	// Using .S2 would zero the upper 64 bits of the destination (ARM64 rule).
	VLD1.P (R8)(R15), [V0.D2]
	WORD $0x0E616805                    // FCVTN  V5.2S, V0.D2
	VFMLA V4.S4, V5.S4, V16.S4

	VLD1.P (R9)(R15), [V0.D2]
	WORD $0x0E616805                    // FCVTN  V5.2S, V0.D2
	VFMLA V4.S4, V5.S4, V17.S4

	VLD1.P (R10)(R15), [V0.D2]
	WORD $0x0E616805                    // FCVTN  V5.2S, V0.D2
	VFMLA V4.S4, V5.S4, V18.S4

	VLD1.P (R11)(R15), [V0.D2]
	WORD $0x0E616805                    // FCVTN  V5.2S, V0.D2
	VFMLA V4.S4, V5.S4, V19.S4

pxc_reduce4:
	// Horizontal reduce: FADDP pairwise across all 4 accumulators
	WORD $0x6E31D600                    // FADDP V0.4S, V16.4S, V17.4S → [s0p0,s0p1,s1p0,s1p1]
	WORD $0x6E33D641                    // FADDP V1.4S, V18.4S, V19.4S → [s2p0,s2p1,s3p0,s3p1]
	WORD $0x6E21D400                    // FADDP V0.4S, V0.4S, V1.4S   → [sum0,sum1,sum2,sum3]

	// Handle odd trailing element (length%2 == 1)
	AND  $1, R3, R13
	CBZ  R13, pxc_store4

	// Extract ALL 4 sums BEFORE any scalar ops (scalar ops zero upper lanes)
	VEXT   $4, V0.B16, V0.B16, V4.B16  // V4.S[0] = sum1
	VEXT   $8, V0.B16, V0.B16, V5.B16  // V5.S[0] = sum2
	VEXT   $12, V0.B16, V0.B16, V6.B16 // V6.S[0] = sum3
	// V0.S[0] = sum0 (already there)

	FMOVD  (R7), F2
	FCVTDS F2, F2                       // F2 = float32(x[j])

	FMOVD  (R8), F3
	FCVTDS F3, F3
	FMADDS F2, F0, F3, F0              // sum0 += x * y0

	FMOVD  (R9), F3
	FCVTDS F3, F3
	FMADDS F2, F4, F3, F4              // sum1 += x * y1

	FMOVD  (R10), F3
	FCVTDS F3, F3
	FMADDS F2, F5, F3, F5              // sum2 += x * y2

	FMOVD  (R11), F3
	FCVTDS F3, F3
	FMADDS F2, F6, F3, F6              // sum3 += x * y3

	// Store 4 float64 results with scalar widening
	FCVTSD F0, F0                       // float32→float64
	LSL    $3, R5, R13
	ADD    R2, R13, R14
	FMOVD  F0, (R14)
	FCVTSD F4, F4
	FMOVD  F4, 8(R14)
	FCVTSD F5, F5
	FMOVD  F5, 16(R14)
	FCVTSD F6, F6
	FMOVD  F6, 24(R14)
	B      pxc_outer4_next

pxc_store4:
	// Convert V0.S4 = [sum0, sum1, sum2, sum3] to float64 and store
	FCVTSD F0, F16                      // sum0 → float64
	LSL    $3, R5, R13
	ADD    R2, R13, R14
	FMOVD  F16, (R14)

	VEXT   $4, V0.B16, V0.B16, V1.B16  // V1.S[0] = sum1
	FCVTSD F1, F16
	FMOVD  F16, 8(R14)

	VEXT   $8, V0.B16, V0.B16, V1.B16  // V1.S[0] = sum2
	FCVTSD F1, F16
	FMOVD  F16, 16(R14)

	VEXT   $12, V0.B16, V0.B16, V1.B16 // V1.S[0] = sum3
	FCVTSD F1, F16
	FMOVD  F16, 24(R14)

pxc_outer4_next:
	ADD $4, R5
	CMP R6, R5
	BLT pxc_outer4

pxc_outer_tail:
	CMP R4, R5
	BGE pxc_done

pxc_outer1:
	VEOR V16.B16, V16.B16, V16.B16

	MOVD R0, R7
	LSL  $3, R5, R13
	ADD  R1, R13, R8

	// 8-wide main loop for single correlation
	LSR  $3, R3, R12
	CBZ  R12, pxc_mid1

pxc_inner1_8:
	VLD1.P (R7)(R15), [V0.D2]
	VLD1.P (R7)(R15), [V1.D2]
	WORD $0x0E616804                    // FCVTN  V4.2S, V0.D2
	WORD $0x4E616824                    // FCVTN2 V4.4S, V1.D2
	VLD1.P (R7)(R15), [V2.D2]
	VLD1.P (R7)(R15), [V3.D2]
	WORD $0x0E616845                    // FCVTN  V5.2S, V2.D2
	WORD $0x4E616865                    // FCVTN2 V5.4S, V3.D2

	VLD1.P (R8)(R15), [V0.D2]
	VLD1.P (R8)(R15), [V1.D2]
	WORD $0x0E616806                    // FCVTN  V6.2S, V0.D2
	WORD $0x4E616826                    // FCVTN2 V6.4S, V1.D2
	VFMLA V4.S4, V6.S4, V16.S4
	VLD1.P (R8)(R15), [V2.D2]
	VLD1.P (R8)(R15), [V3.D2]
	WORD $0x0E616847                    // FCVTN  V7.2S, V2.D2
	WORD $0x4E616867                    // FCVTN2 V7.4S, V3.D2
	VFMLA V5.S4, V7.S4, V16.S4

	SUBS $1, R12, R12
	BNE  pxc_inner1_8

pxc_mid1:
	// Handle 4-element remainder
	TST  $4, R3
	BEQ  pxc_tail1

	VLD1.P (R7)(R15), [V0.D2]
	VLD1.P (R7)(R15), [V1.D2]
	WORD $0x0E616804                    // FCVTN  V4.2S, V0.D2
	WORD $0x4E616824                    // FCVTN2 V4.4S, V1.D2
	VLD1.P (R8)(R15), [V0.D2]
	VLD1.P (R8)(R15), [V1.D2]
	WORD $0x0E616805                    // FCVTN  V5.2S, V0.D2
	WORD $0x4E616825                    // FCVTN2 V5.4S, V1.D2
	VFMLA V4.S4, V5.S4, V16.S4

pxc_tail1:
	// Handle 2-element remainder (use .S4 to preserve V16.S[2:3])
	AND  $2, R3, R13
	CBZ  R13, pxc_reduce1

	VLD1.P (R7)(R15), [V0.D2]
	WORD $0x0E616804                    // FCVTN  V4.2S, V0.D2 (upper lanes zero)
	VLD1.P (R8)(R15), [V0.D2]
	WORD $0x0E616805                    // FCVTN  V5.2S, V0.D2 (upper lanes zero)
	VFMLA V4.S4, V5.S4, V16.S4

pxc_reduce1:
	// Horizontal sum of V16.S4
	WORD $0x6E30D600                    // FADDP V0.4S, V16.4S, V16.4S → [a+b, c+d, ...]
	VEXT $4, V0.B16, V0.B16, V1.B16    // V1.S[0] = (c+d)
	FADDS F0, F1, F0                    // F0 = (a+b) + (c+d)

	// Handle odd trailing element
	AND  $1, R3, R13
	CBZ  R13, pxc_store1

	FMOVD  (R7), F2
	FCVTDS F2, F2
	FMOVD  (R8), F3
	FCVTDS F3, F3
	FMADDS F2, F0, F3, F0

pxc_store1:
	FCVTSD F0, F0                       // float32→float64
	LSL    $3, R5, R13
	ADD    R2, R13, R14
	FMOVD  F0, (R14)

	ADD $1, R5
	CMP R4, R5
	BLT pxc_outer1

pxc_done:
	RET
