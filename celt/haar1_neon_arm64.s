//go:build arm64 && !purego

#include "textflag.h"

// Vector float ops the Go arm64 assembler lacks mnemonics for (it only exposes
// scalar FADDS/FSUBS/FMULS and the vector FMA VFMLA/VFMLS). Encodings:
//   FMUL Vd.4S, Vn.4S, Vm.4S = 0x6E20DC00 | (Vm<<16) | (Vn<<5) | Vd
//   FADD Vd.4S, Vn.4S, Vm.4S = 0x4E20D400 | (Vm<<16) | (Vn<<5) | Vd
//   FSUB Vd.4S, Vn.4S, Vm.4S = 0x4EA0D400 | (Vm<<16) | (Vn<<5) | Vd

// func haar1Stride1NEON(x []float32, n0 int)
//
// In-place Hadamard butterfly over n0 contiguous (even,odd) float32 pairs,
// stride==1 case of haar1: for each pair (a,b) with c = 1/sqrt(2),
//   out_even = c*a + c*b
//   out_odd  = c*a - c*b
// Each product is a bare FMUL and each combine is a separate FADD/FSUB, the same
// lane math libopus's own NEON kernels use. That is bit-exact with the non-fused
// scalar oracle (purego/amd64); on the quality-gated fused arm64 build the
// scalar reference contracts a*b+c into FMA, so this path is opus_compare-gated
// there rather than byte-identical.
//
// Register map:
//   R0 = x base (advanced by ADD after each 4-pair group and the scalar tail)
//   R1 = n0
//   R2 = vector iteration count (n0/4); R3 = scalar tail count (n0%4)
//   V3 = invSqrt2 broadcast
//   V0 = even lanes, V1 = odd lanes; V4 = c*even, V5 = c*odd
TEXT ·haar1Stride1NEON(SB), NOSPLIT, $0-32
	MOVD x_base+0(FP), R0
	MOVD n0+24(FP), R1

	CBZ  R1, h1_done

	FMOVS $0.70710678118654752440, F3
	VDUP  V3.S[0], V3.S4          // V3 = {c,c,c,c}

	LSR  $2, R1, R2               // R2 = n0/4 (4 pairs per iteration)
	AND  $3, R1, R3               // R3 = n0%4
	CBZ  R2, h1_tail

h1_loop4:
	// Load 4 pairs: V0 = {even0..even3}, V1 = {odd0..odd3}
	VLD2 (R0), [V0.S4, V1.S4]

	WORD $0x6E23DC04             // FMUL V4.4S, V0.4S, V3.4S  (c*even)
	WORD $0x6E23DC25             // FMUL V5.4S, V1.4S, V3.4S  (c*odd)

	WORD $0x4E25D480            // FADD V0.4S, V4.4S, V5.4S  (even_out = c*even + c*odd)
	WORD $0x4EA5D481            // FSUB V1.4S, V4.4S, V5.4S  (odd_out  = c*even - c*odd)

	VST2 [V0.S4, V1.S4], (R0)
	ADD  $32, R0

	SUBS $1, R2
	BNE  h1_loop4

h1_tail:
	CBZ  R3, h1_done

h1_tail1:
	FMOVS (R0), F0               // even (x[2j])
	FMOVS 4(R0), F1              // odd  (x[2j+1])
	FMULS F3, F0, F4            // c*even
	FMULS F3, F1, F5           // c*odd
	FADDS F4, F5, F0           // even_out = c*even + c*odd
	FSUBS F5, F4, F1          // odd_out  = c*even - c*odd
	FMOVS F0, (R0)
	FMOVS F1, 4(R0)
	ADD   $8, R0
	SUBS  $1, R3
	BNE   h1_tail1

h1_done:
	RET
