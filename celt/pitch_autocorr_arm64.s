//go:build arm64 && !purego
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
// Lags 0..3 follow libopus' ARM64 xcorr_kernel_neon_float FMA order; lag 4
// follows its celt_inner_prod_neon reduction order.
TEXT ·pitchAutocorr5(SB), NOSPLIT, $0-40
	MOVD lp_base+0(FP), R0
	MOVD length+24(FP), R1
	MOVD ac+32(FP), R2

	// fastN = length - 4; if < 0, fastN = 0
	SUBS $4, R1, R3
	CSEL LT, ZR, R3, R3           // R3 = max(0, length-4) = fastN

	MOVD $16, R15                  // post-increment stride

	CBNZ R3, pa_lags0_3

	// Very short inputs are not used by CELT prefilter pitch analysis, but keep
	// the original generic lag loop as the bounds-safe fallback.
	MOVD ZR, R4                    // lag counter
	B    pa_lag_loop

pa_lags0_3:
	// libopus' ARM64 float path computes autocorr lags 0..3 through
	// xcorr_kernel_neon_float(): one scalar x sample is fused into four lag
	// accumulators before advancing.  Borderline pitch decisions depend on this
	// FMA order.
	VEOR V16.B16, V16.B16, V16.B16
	VEOR V17.B16, V17.B16, V17.B16
	VEOR V18.B16, V18.B16, V18.B16
	VEOR V19.B16, V19.B16, V19.B16

	MOVD R3, R7                    // fastN counter
	MOVD R0, R8                    // x pointer
	MOVD R0, R9                    // y base pointer

pa_lags0_3_loop:
	FMOVD  (R8), F0
	FCVTDS F0, F0

	FMOVD  (R9), F1
	FCVTDS F1, F1
	FMADDS F0, F16, F1, F16

	FMOVD  8(R9), F1
	FCVTDS F1, F1
	FMADDS F0, F17, F1, F17

	FMOVD  16(R9), F1
	FCVTDS F1, F1
	FMADDS F0, F18, F1, F18

	FMOVD  24(R9), F1
	FCVTDS F1, F1
	FMADDS F0, F19, F1, F19

	ADD  $8, R8
	ADD  $8, R9
	SUBS $1, R7
	BNE  pa_lags0_3_loop

	// _celt_autocorr() correction terms for k=0..3.
	VEOR V20.B16, V20.B16, V20.B16
	VEOR V21.B16, V21.B16, V21.B16
	VEOR V22.B16, V22.B16, V22.B16
	VEOR V23.B16, V23.B16, V23.B16

	MOVD R3, R7
pa_corr0_loop:
	CMP  R1, R7
	BGE  pa_corr1_init
	LSL  $3, R7, R8
	ADD  R0, R8, R8
	FMOVD  (R8), F0
	FCVTDS F0, F0
	FMADDS F0, F20, F0, F20
	ADD  $1, R7
	B    pa_corr0_loop

pa_corr1_init:
	ADD  $1, R3, R7
pa_corr1_loop:
	CMP  R1, R7
	BGE  pa_corr2_init
	LSL  $3, R7, R8
	ADD  R0, R8, R8
	FMOVD  (R8), F0
	FCVTDS F0, F0
	SUB  $1, R7, R10
	LSL  $3, R10, R10
	ADD  R0, R10, R10
	FMOVD  (R10), F1
	FCVTDS F1, F1
	FMADDS F0, F21, F1, F21
	ADD  $1, R7
	B    pa_corr1_loop

pa_corr2_init:
	ADD  $2, R3, R7
pa_corr2_loop:
	CMP  R1, R7
	BGE  pa_corr3_init
	LSL  $3, R7, R8
	ADD  R0, R8, R8
	FMOVD  (R8), F0
	FCVTDS F0, F0
	SUB  $2, R7, R10
	LSL  $3, R10, R10
	ADD  R0, R10, R10
	FMOVD  (R10), F1
	FCVTDS F1, F1
	FMADDS F0, F22, F1, F22
	ADD  $1, R7
	B    pa_corr2_loop

pa_corr3_init:
	ADD  $3, R3, R7
pa_corr3_loop:
	CMP  R1, R7
	BGE  pa_store_lags0_3
	LSL  $3, R7, R8
	ADD  R0, R8, R8
	FMOVD  (R8), F0
	FCVTDS F0, F0
	SUB  $3, R7, R10
	LSL  $3, R10, R10
	ADD  R0, R10, R10
	FMOVD  (R10), F1
	FCVTDS F1, F1
	FMADDS F0, F23, F1, F23
	ADD  $1, R7
	B    pa_corr3_loop

pa_store_lags0_3:
	FADDS F20, F16, F16
	FADDS F21, F17, F17
	FADDS F22, F18, F18
	FADDS F23, F19, F19

	FCVTSD F16, F0
	FMOVD  F0, 0(R2)
	FCVTSD F17, F0
	FMOVD  F0, 8(R2)
	FCVTSD F18, F0
	FMOVD  F0, 16(R2)
	FCVTSD F19, F0
	FMOVD  F0, 24(R2)

	// Lag 4 uses celt_inner_prod_neon() in libopus; the existing per-lag
	// vector reduction matches that accumulation order.
	MOVD $4, R4

pa_lag_loop:
	VEOR V16.B16, V16.B16, V16.B16  // accumulator

	MOVD R0, R5                    // lp_base pointer
	LSL  $3, R4, R6               // lag * 8 (byte offset)
	ADD  R0, R6, R6               // lp + lag

	// Inner loop pointers
	MOVD R5, R8                   // x = lp[i] pointer
	MOVD R6, R9                   // y = lp[i+lag] pointer

	// Inner loop: process fastN elements, 8 at a time
	LSR  $3, R3, R7               // fastN / 8
	CBZ  R7, pa_mid4

pa_inner8:
	VLD1.P (R8)(R15), [V0.D2]
	VLD1.P (R8)(R15), [V1.D2]
	WORD $0x0E616804              // FCVTN  V4.2S, V0.D2
	WORD $0x4E616824              // FCVTN2 V4.4S, V1.D2

	VLD1.P (R9)(R15), [V0.D2]
	VLD1.P (R9)(R15), [V1.D2]
	WORD $0x0E616805              // FCVTN  V5.2S, V0.D2
	WORD $0x4E616825              // FCVTN2 V5.4S, V1.D2

	VFMLA V4.S4, V5.S4, V16.S4

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
	BNE  pa_inner8

pa_mid4:
	// Handle 4-element remainder of fastN
	AND  $4, R3, R7
	CBZ  R7, pa_tail2

	VLD1.P (R8)(R15), [V0.D2]
	VLD1.P (R8)(R15), [V1.D2]
	WORD $0x0E616804              // FCVTN  V4.2S, V0.D2
	WORD $0x4E616824              // FCVTN2 V4.4S, V1.D2

	VLD1.P (R9)(R15), [V0.D2]
	VLD1.P (R9)(R15), [V1.D2]
	WORD $0x0E616805              // FCVTN  V5.2S, V0.D2
	WORD $0x4E616825              // FCVTN2 V5.4S, V1.D2

	VFMLA V4.S4, V5.S4, V16.S4

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
