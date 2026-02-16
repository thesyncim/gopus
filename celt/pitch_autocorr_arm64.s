#include "textflag.h"

// WORD-encoded instructions:
//   FCVTN  Vd.2S, Vn.D2  = 0x0E616800 | (Vn<<5) | Vd
//   FCVTN2 Vd.4S, Vn.D2  = 0x4E616800 | (Vn<<5) | Vd
//   FADDP  Vd.4S, Vn.4S, Vm.4S = 0x6E20D400 | (Vm<<16) | (Vn<<5) | Vd

// func pitchAutocorr5(lp []float64, length int, ac *[5]float64)
//
// Computes 5 autocorrelation values with float32 accumulation.
// For each lag 0..4: ac[lag] = sum(float32(lp[i])*float32(lp[i+lag])) for i in [0,fastN)
//   plus correction terms for i in [lag+fastN, length).
//
// Uses the same FCVTN + FMLA pattern as prefilterPitchXcorr.
TEXT ·pitchAutocorr5(SB), NOSPLIT, $0-40
	MOVD lp_base+0(FP), R0
	MOVD length+24(FP), R1
	MOVD ac+32(FP), R2

	// fastN = length - 4; if < 0, fastN = 0
	SUBS $4, R1, R3
	CSEL LT, ZR, R3, R3           // R3 = max(0, length-4) = fastN

	MOVD $16, R15                  // post-increment stride

	// Process lag 0 through 4
	MOVD ZR, R4                    // lag counter

pa_lag_loop:
	VEOR V16.B16, V16.B16, V16.B16  // accumulator

	MOVD R0, R5                    // lp_base pointer
	LSL  $3, R4, R6               // lag * 8 (byte offset)
	ADD  R0, R6, R6               // lp + lag

	// Inner loop: process fastN elements, 4 at a time
	LSR  $2, R3, R7               // fastN / 4
	CBZ  R7, pa_tail2

	MOVD R5, R8                   // x = lp[i] pointer
	MOVD R6, R9                   // y = lp[i+lag] pointer

pa_inner4:
	VLD1.P (R8)(R15), [V0.D2]
	VLD1.P (R8)(R15), [V1.D2]
	WORD $0x0E616804              // FCVTN  V4.2S, V0.D2
	WORD $0x4E616824              // FCVTN2 V4.4S, V1.D2

	VLD1.P (R9)(R15), [V0.D2]
	VLD1.P (R9)(R15), [V1.D2]
	WORD $0x0E616805              // FCVTN  V5.2S, V0.D2
	WORD $0x4E616825              // FCVTN2 V5.4S, V1.D2

	VFMLA V4.S4, V5.S4, V16.S4

	SUBS $1, R7
	BNE  pa_inner4

pa_tail2:
	// Handle 2-element remainder of fastN
	AND  $2, R3, R7
	CBZ  R7, pa_tail_reduce

	VLD1 (R8), [V0.D2]
	ADD  $16, R8
	WORD $0x0E616804              // FCVTN  V4.2S, V0.D2

	VLD1 (R9), [V0.D2]
	ADD  $16, R9
	WORD $0x0E616805              // FCVTN  V5.2S, V0.D2

	VFMLA V4.S4, V5.S4, V16.S4

pa_tail_reduce:
	// Horizontal sum
	WORD $0x6E30D600              // FADDP V0.4S, V16.4S, V16.4S
	VEXT $4, V0.B16, V0.B16, V1.B16
	FADDS F0, F1, F0

	// Handle odd element of fastN
	AND  $1, R3, R7
	CBZ  R7, pa_correction

	FMOVD  (R8), F2
	FCVTDS F2, F2
	FMOVD  (R9), F3
	FCVTDS F3, F3
	FMADDS F2, F0, F3, F0

pa_correction:
	// Correction loop: for i = lag+fastN to length-1
	// Uses reversed indexing: lp[i] * lp[i-lag]
	ADD  R4, R3, R7               // i = lag + fastN
	CMP  R1, R7                   // if i >= length, skip
	BGE  pa_store_lag

pa_corr_loop:
	// lp[i] and lp[i-lag]
	LSL   $3, R7, R8
	ADD   R0, R8, R8              // &lp[i]
	FMOVD (R8), F2
	FCVTDS F2, F2

	SUB   R4, R7, R9              // i - lag
	LSL   $3, R9, R9
	ADD   R0, R9, R9
	FMOVD (R9), F3
	FCVTDS F3, F3

	FMADDS F2, F0, F3, F0

	ADD  $1, R7
	CMP  R1, R7
	BLT  pa_corr_loop

pa_store_lag:
	// Convert float32 sum to float64 and store
	FCVTSD F0, F0
	LSL    $3, R4, R6
	ADD    R2, R6, R6
	FMOVD  F0, (R6)

	ADD  $1, R4
	CMP  $5, R4
	BLT  pa_lag_loop

	RET
