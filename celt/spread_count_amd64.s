//go:build amd64 && gopus_spread_asm

#include "textflag.h"

// func spreadCountThresholds(x []float64, n int, nf float64) (t0, t1, t2 int)
//
// Counts coefficients below three thresholds using SSE2/AVX float64 SIMD.
// Processes 2 float64 values per iteration.
//
// Thresholds: 0.25, 0.0625, 0.015625
// Comparison: x[j]*x[j]*nf < threshold
TEXT ·spreadCountThresholds(SB), NOSPLIT, $0-64
	MOVQ x_base+0(FP), AX
	MOVQ n+24(FP), CX
	VMOVSD nf+32(FP), X15

	// Broadcast nf to both lanes
	VMOVDDUP X15, X15                // X15 = [nf, nf]

	// Load thresholds
	MOVQ $0x3FD0000000000000, DX     // 0.25
	VMOVQ DX, X12
	VMOVDDUP X12, X12                // X12 = [0.25, 0.25]

	MOVQ $0x3FB0000000000000, DX     // 0.0625
	VMOVQ DX, X13
	VMOVDDUP X13, X13

	MOVQ $0x3F90000000000000, DX     // 0.015625
	VMOVQ DX, X14
	VMOVDDUP X14, X14

	// Zero count accumulators
	XORQ R8, R8                      // count0
	XORQ R9, R9                      // count1
	XORQ R10, R10                    // count2

	// R11 = n / 2
	MOVQ  CX, R11
	SHRQ  $1, R11
	TESTQ R11, R11
	JZ    sc_tail

sc_loop2:
	// Load 2 float64
	VMOVUPD (AX), X0

	// x2N = x * x * nf
	VMULPD X0, X0, X1               // X1 = x*x
	VMULPD X1, X15, X1              // X1 = x*x*nf

	// Compare: x2N < threshold (VCMPPD $1 = less-than)
	// Go assembler order is VCMPPD imm8, src1, src2, dst => (src1 < src2).
	VCMPPD $1, X1, X12, X2          // X2 = (x2N < 0.25) mask
	VCMPPD $1, X1, X13, X3          // X3 = (x2N < 0.0625) mask
	VCMPPD $1, X1, X14, X4          // X4 = (x2N < 0.015625) mask

	// Extract mask bits and count
	VMOVMSKPD X2, DX
	POPCNTL DX, DX
	ADDQ DX, R8

	VMOVMSKPD X3, DX
	POPCNTL DX, DX
	ADDQ DX, R9

	VMOVMSKPD X4, DX
	POPCNTL DX, DX
	ADDQ DX, R10

	ADDQ $16, AX
	DECQ R11
	JNZ  sc_loop2

sc_tail:
	// Handle odd trailing element
	TESTQ $1, CX
	JZ    sc_done

	VMOVSD (AX), X0
	VMULSD X0, X0, X1
	VMULSD X1, X15, X1

	// Scalar comparisons
	VUCOMISD X1, X12                 // compare 0.25 vs x2N
	JBE  sc_t0_skip                  // skip if 0.25 <= x2N
	INCQ R8
sc_t0_skip:
	VUCOMISD X1, X13
	JBE  sc_t1_skip
	INCQ R9
sc_t1_skip:
	VUCOMISD X1, X14
	JBE  sc_done
	INCQ R10

sc_done:
	MOVQ R8, ret+40(FP)
	MOVQ R9, ret1+48(FP)
	MOVQ R10, ret2+56(FP)
	VZEROUPPER
	RET
