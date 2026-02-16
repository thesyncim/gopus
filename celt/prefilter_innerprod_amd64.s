#include "textflag.h"

// func prefilterInnerProd(x []float64, y []float64, length int) float64
//
// Float32-accumulated dot product using AVX.
// Converts float64 inputs to float32 via VCVTPD2PS, accumulates with VFMADD231PS.
TEXT ·prefilterInnerProd(SB), NOSPLIT, $0-56
	MOVQ x_base+0(FP), AX
	MOVQ y_base+24(FP), BX
	MOVQ length+48(FP), CX

	TESTQ CX, CX
	JLE   pip_zero

	VXORPS X8, X8, X8                // accumulator = 0

	// R8 = loop count (length / 4)
	MOVQ  CX, R8
	SHRQ  $2, R8
	TESTQ R8, R8
	JZ    pip_tail

pip_loop4:
	// Load 4 float64 from x, convert to 4 float32
	VMOVUPD    (AX), Y0
	VCVTPD2PSY Y0, X0

	// Load 4 float64 from y, convert to 4 float32
	VMOVUPD    (BX), Y1
	VCVTPD2PSY Y1, X1

	// FMA: X8 += X0 * X1
	VFMADD231PS X0, X1, X8

	ADDQ $32, AX
	ADDQ $32, BX
	DECQ R8
	JNZ  pip_loop4

pip_tail:
	// Handle remaining 0-3 elements
	ANDQ  $3, CX
	TESTQ CX, CX
	JZ    pip_reduce

pip_scalar:
	VMOVSD    (AX), X0
	VCVTSD2SS X0, X0, X0
	VMOVSD    (BX), X1
	VCVTSD2SS X1, X1, X1
	VFMADD231SS X0, X1, X8
	ADDQ $8, AX
	ADDQ $8, BX
	DECQ CX
	JNZ  pip_scalar

pip_reduce:
	// Horizontal sum of X8.S4
	VHADDPS X8, X8, X8
	VHADDPS X8, X8, X8
	// X8[0] = sum
	VCVTSS2SD X8, X8, X8             // float32 → float64
	VMOVSD    X8, ret+56(FP)
	VZEROUPPER
	RET

pip_zero:
	XORQ AX, AX
	MOVQ AX, ret+56(FP)
	VZEROUPPER
	RET

// func prefilterDualInnerProd(x []float64, y1 []float64, y2 []float64, length int) (float64, float64)
//
// Two float32-accumulated dot products sharing x input.
TEXT ·prefilterDualInnerProd(SB), NOSPLIT, $0-96
	MOVQ x_base+0(FP), AX
	MOVQ y1_base+24(FP), BX
	MOVQ y2_base+48(FP), DX
	MOVQ length+72(FP), CX

	TESTQ CX, CX
	JLE   dpip_zero

	VXORPS X8, X8, X8                // sum1 accumulator
	VXORPS X9, X9, X9                // sum2 accumulator

	// R8 = loop count (length / 4)
	MOVQ  CX, R8
	SHRQ  $2, R8
	TESTQ R8, R8
	JZ    dpip_tail

dpip_loop4:
	// Load 4 float64 from x, convert to 4 float32
	VMOVUPD    (AX), Y0
	VCVTPD2PSY Y0, X0

	// Load 4 from y1, convert, FMA into X8
	VMOVUPD    (BX), Y1
	VCVTPD2PSY Y1, X1
	VFMADD231PS X0, X1, X8

	// Load 4 from y2, convert, FMA into X9
	VMOVUPD    (DX), Y1
	VCVTPD2PSY Y1, X1
	VFMADD231PS X0, X1, X9

	ADDQ $32, AX
	ADDQ $32, BX
	ADDQ $32, DX
	DECQ R8
	JNZ  dpip_loop4

dpip_tail:
	ANDQ  $3, CX
	TESTQ CX, CX
	JZ    dpip_reduce

dpip_scalar:
	VMOVSD    (AX), X0
	VCVTSD2SS X0, X0, X0

	VMOVSD    (BX), X1
	VCVTSD2SS X1, X1, X1
	VFMADD231SS X0, X1, X8

	VMOVSD    (DX), X1
	VCVTSD2SS X1, X1, X1
	VFMADD231SS X0, X1, X9

	ADDQ $8, AX
	ADDQ $8, BX
	ADDQ $8, DX
	DECQ CX
	JNZ  dpip_scalar

dpip_reduce:
	// Horizontal sum of X8 (sum1)
	VHADDPS X8, X8, X8
	VHADDPS X8, X8, X8
	VCVTSS2SD X8, X8, X8
	VMOVSD    X8, ret+80(FP)

	// Horizontal sum of X9 (sum2)
	VHADDPS X9, X9, X9
	VHADDPS X9, X9, X9
	VCVTSS2SD X9, X9, X9
	VMOVSD    X9, ret1+88(FP)

	VZEROUPPER
	RET

dpip_zero:
	XORQ AX, AX
	MOVQ AX, ret+80(FP)
	MOVQ AX, ret1+88(FP)
	VZEROUPPER
	RET
