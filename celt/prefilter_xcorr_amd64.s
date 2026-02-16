#include "textflag.h"

// func prefilterPitchXcorr(x []float64, y []float64, xcorr []float64, length, maxPitch int)
//
// Float32-accumulated pitch cross-correlation using AVX2+FMA3.
// Converts float64 inputs to float32 via VCVTPD2PS, accumulates with VFMADD231PS.
TEXT ·prefilterPitchXcorr(SB), NOSPLIT, $0-88
	MOVQ x_base+0(FP), AX
	MOVQ y_base+24(FP), BX
	MOVQ xcorr_base+48(FP), CX
	MOVQ length+72(FP), DX
	MOVQ maxPitch+80(FP), DI

	CMPQ DX, $1
	JLT  pxc_done
	CMPQ DI, $1
	JLT  pxc_done

	XORQ SI, SI

pxc_outer1:
	VXORPS Y8, Y8, Y8
	MOVQ   AX, R8
	MOVQ   SI, R9
	SHLQ   $3, R9
	ADDQ   BX, R9

	// 8-wide main loop: process 8 elements per iteration
	MOVQ DX, R10
	SHRQ $3, R10
	TESTQ R10, R10
	JZ   pxc_mid1

pxc_inner1_8:
	VMOVUPD (R8), Y0
	VCVTPD2PSY Y0, X0
	VMOVUPD (R9), Y1
	VCVTPD2PSY Y1, X1
	VFMADD231PS X0, X1, X8
	VMOVUPD 32(R8), Y2
	VCVTPD2PSY Y2, X2
	VMOVUPD 32(R9), Y3
	VCVTPD2PSY Y3, X3
	VFMADD231PS X2, X3, X8
	ADDQ $64, R8
	ADDQ $64, R9
	DECQ R10
	JNZ  pxc_inner1_8

pxc_mid1:
	// Handle 4-element remainder
	TESTQ $4, DX
	JZ   pxc_tail1

	VMOVUPD (R8), Y0
	VCVTPD2PSY Y0, X0
	VMOVUPD (R9), Y1
	VCVTPD2PSY Y1, X1
	VFMADD231PS X0, X1, X8
	ADDQ $32, R8
	ADDQ $32, R9

pxc_tail1:
	MOVQ DX, R10
	ANDQ $3, R10
	TESTQ R10, R10
	JZ   pxc_reduce1

pxc_scalar1:
	VMOVSD (R8), X0
	VCVTSD2SS X0, X0, X0
	VMOVSD (R9), X1
	VCVTSD2SS X1, X1, X1
	VFMADD231SS X0, X1, X8
	ADDQ $8, R8
	ADDQ $8, R9
	DECQ R10
	JNZ  pxc_scalar1

pxc_reduce1:
	// Horizontal sum of X8.S4 = [a,b,c,d]
	VHADDPS X8, X8, X8
	VHADDPS X8, X8, X8
	// X8[0] = a+b+c+d
	// Convert float32 → float64 and store
	VCVTSS2SD X8, X8, X8
	MOVQ SI, R10
	SHLQ $3, R10
	ADDQ CX, R10
	VMOVSD X8, (R10)

	INCQ SI
	CMPQ SI, DI
	JLT  pxc_outer1

pxc_done:
	VZEROUPPER
	RET
