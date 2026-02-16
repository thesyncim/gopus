#include "textflag.h"

// func pitchAutocorr5(lp []float64, length int, ac *[5]float64)
//
// Computes 5 autocorrelation values with float32 accumulation.
// Uses AVX for f64→f32 conversion and FMA for accumulation.
TEXT ·pitchAutocorr5(SB), NOSPLIT, $0-40
	MOVQ lp_base+0(FP), AX
	MOVQ length+24(FP), CX
	MOVQ ac+32(FP), DI

	// fastN = max(0, length - 4)
	MOVQ CX, R11
	SUBQ $4, R11
	TESTQ R11, R11
	JGE  pa_fn_ok
	XORQ R11, R11
pa_fn_ok:

	// Process lag 0 through 4
	XORQ SI, SI                   // lag counter

pa_lag_loop:
	VXORPS X8, X8, X8            // accumulator

	MOVQ AX, R8                  // x = lp[i]
	MOVQ SI, R9
	SHLQ $3, R9
	ADDQ AX, R9                  // y = lp[i+lag]

	// Inner loop: fastN elements, 4 at a time
	MOVQ  R11, R10
	SHRQ  $2, R10
	TESTQ R10, R10
	JZ    pa_scalar

pa_inner4:
	VMOVUPD    (R8), Y0
	VCVTPD2PSY Y0, X0
	VMOVUPD    (R9), Y1
	VCVTPD2PSY Y1, X1
	VFMADD231PS X0, X1, X8
	ADDQ $32, R8
	ADDQ $32, R9
	DECQ R10
	JNZ  pa_inner4

pa_scalar:
	// Handle remaining fastN % 4 elements
	MOVQ  R11, R10
	ANDQ  $3, R10
	TESTQ R10, R10
	JZ    pa_reduce

pa_scalar_loop:
	VMOVSD    (R8), X0
	VCVTSD2SS X0, X0, X0
	VMOVSD    (R9), X1
	VCVTSD2SS X1, X1, X1
	VFMADD231SS X0, X1, X8
	ADDQ $8, R8
	ADDQ $8, R9
	DECQ R10
	JNZ  pa_scalar_loop

pa_reduce:
	// Horizontal sum
	VHADDPS X8, X8, X8
	VHADDPS X8, X8, X8
	// X8[0] = sum

	// Correction loop: for i = lag+fastN to length-1
	// Uses lp[i] * lp[i-lag]
	MOVQ SI, R10
	ADDQ R11, R10                // i = lag + fastN
	CMPQ R10, CX
	JGE  pa_store_lag

pa_corr_loop:
	// lp[i]
	MOVQ R10, R12
	SHLQ $3, R12
	VMOVSD (AX)(R12*1), X0
	VCVTSD2SS X0, X0, X0

	// lp[i-lag]
	MOVQ R10, R13
	SUBQ SI, R13
	SHLQ $3, R13
	VMOVSD (AX)(R13*1), X1
	VCVTSD2SS X1, X1, X1

	VFMADD231SS X0, X1, X8
	INCQ R10
	CMPQ R10, CX
	JLT  pa_corr_loop

pa_store_lag:
	// Convert sum to float64 and store ac[lag]
	VCVTSS2SD X8, X8, X8
	MOVQ SI, R10
	SHLQ $3, R10
	VMOVSD X8, (DI)(R10*1)

	INCQ SI
	CMPQ SI, $5
	JLT  pa_lag_loop

	VZEROUPPER
	RET
