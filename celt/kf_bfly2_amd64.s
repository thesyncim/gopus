//go:build amd64 && !purego

#include "textflag.h"

// Optimized AMD64 SIMD butterfly operations for Kiss FFT
//
// kfBfly2M1 computes radix-2 butterflies with twiddle factor = 1:
//   For each pair [a, b] of complex numbers (a = fout[0], b = fout[1]):
//     fout[0] = a + b
//     fout[1] = a - b
//
// Each complex number is 8 bytes (2x float32: real, imag)
// Each butterfly is 16 bytes (2 complex numbers)
//
// Memory layout: [ar, ai, br, bi, ar, ai, br, bi, ...]
//                |--- cpx a ---|--- cpx b ---|

// ============================================================================
// SSE2 Implementation: Process 4 butterflies per iteration (64 bytes)
// ============================================================================
// Uses 4 XMM registers to hold 4 butterflies
// XMM register: [ar0, ai0, br0, bi0] (one butterfly = 16 bytes)
// We load 4 butterflies, compute add/sub, shuffle results back

// func kfBfly2M1SSE2(fout []kissCpx, n int)
TEXT ·kfBfly2M1SSE2(SB), NOSPLIT, $0-32
	MOVQ	fout_base+0(FP), DI
	MOVQ	n+24(FP), CX
	TESTQ	CX, CX
	JLE	sse2_done

	// Check if we can do 4 butterflies at a time (n >= 4)
	CMPQ	CX, $4
	JL	sse2_tail

sse2_loop4:
	// Load 4 butterflies (64 bytes = 4 * 16)
	MOVUPS	(DI), X0       // [ar0, ai0, br0, bi0]
	MOVUPS	16(DI), X1     // [ar1, ai1, br1, bi1]
	MOVUPS	32(DI), X2     // [ar2, ai2, br2, bi2]
	MOVUPS	48(DI), X3     // [ar3, ai3, br3, bi3]

	// For each butterfly:
	// We need to compute [ar+br, ai+bi, ar-br, ai-bi]
	// From input [ar, ai, br, bi]

	// Butterfly 0: X0 = [ar, ai, br, bi]
	// X4 = [br, bi, ar, ai] (swap halves)
	MOVAPS	X0, X4
	SHUFPS	$0x4E, X4, X4  // swap: [2,3,0,1] = 0x4E
	// X5 = [ar, ai, ar, ai] (duplicate first half)
	MOVAPS	X0, X5
	MOVLHPS	X5, X5         // [0,1,0,1]
	// X6 = [br, bi, br, bi] (duplicate second half)
	MOVAPS	X0, X6
	MOVHLPS	X6, X6         // [2,3,2,3]
	// sum = a + b, diff = a - b
	ADDPS	X6, X5         // X5 = [ar+br, ai+bi, ar+br, ai+bi]
	MOVAPS	X0, X6
	MOVLHPS	X6, X6
	MOVAPS	X0, X7
	MOVHLPS	X7, X7
	SUBPS	X7, X6         // X6 = [ar-br, ai-bi, ar-br, ai-bi]
	// Combine: [ar+br, ai+bi, ar-br, ai-bi]
	MOVLHPS	X6, X5         // X5.hi = X6.lo
	MOVAPS	X5, X0

	// Butterfly 1: X1 = [ar, ai, br, bi]
	MOVAPS	X1, X4
	SHUFPS	$0x4E, X4, X4
	MOVAPS	X1, X5
	MOVLHPS	X5, X5
	MOVAPS	X1, X6
	MOVHLPS	X6, X6
	ADDPS	X6, X5
	MOVAPS	X1, X6
	MOVLHPS	X6, X6
	MOVAPS	X1, X7
	MOVHLPS	X7, X7
	SUBPS	X7, X6
	MOVLHPS	X6, X5
	MOVAPS	X5, X1

	// Butterfly 2: X2 = [ar, ai, br, bi]
	MOVAPS	X2, X4
	SHUFPS	$0x4E, X4, X4
	MOVAPS	X2, X5
	MOVLHPS	X5, X5
	MOVAPS	X2, X6
	MOVHLPS	X6, X6
	ADDPS	X6, X5
	MOVAPS	X2, X6
	MOVLHPS	X6, X6
	MOVAPS	X2, X7
	MOVHLPS	X7, X7
	SUBPS	X7, X6
	MOVLHPS	X6, X5
	MOVAPS	X5, X2

	// Butterfly 3: X3 = [ar, ai, br, bi]
	MOVAPS	X3, X4
	SHUFPS	$0x4E, X4, X4
	MOVAPS	X3, X5
	MOVLHPS	X5, X5
	MOVAPS	X3, X6
	MOVHLPS	X6, X6
	ADDPS	X6, X5
	MOVAPS	X3, X6
	MOVLHPS	X6, X6
	MOVAPS	X3, X7
	MOVHLPS	X7, X7
	SUBPS	X7, X6
	MOVLHPS	X6, X5
	MOVAPS	X5, X3

	// Store results
	MOVUPS	X0, (DI)
	MOVUPS	X1, 16(DI)
	MOVUPS	X2, 32(DI)
	MOVUPS	X3, 48(DI)

	ADDQ	$64, DI
	SUBQ	$4, CX
	CMPQ	CX, $4
	JGE	sse2_loop4

sse2_tail:
	// Handle remaining 1-3 butterflies
	TESTQ	CX, CX
	JLE	sse2_done

sse2_loop1:
	// Load 1 butterfly (16 bytes)
	MOVUPS	(DI), X0       // [ar, ai, br, bi]

	// X5 = [ar, ai, ar, ai]
	MOVAPS	X0, X5
	MOVLHPS	X5, X5
	// X6 = [br, bi, br, bi]
	MOVAPS	X0, X6
	MOVHLPS	X6, X6
	// sum = a + b
	ADDPS	X6, X5         // [ar+br, ai+bi, ar+br, ai+bi]
	// diff = a - b
	MOVAPS	X0, X6
	MOVLHPS	X6, X6
	MOVAPS	X0, X7
	MOVHLPS	X7, X7
	SUBPS	X7, X6         // [ar-br, ai-bi, ar-br, ai-bi]
	// Combine
	MOVLHPS	X6, X5         // [ar+br, ai+bi, ar-br, ai-bi]
	MOVUPS	X5, (DI)

	ADDQ	$16, DI
	DECQ	CX
	JNZ	sse2_loop1

sse2_done:
	RET


// ============================================================================
// AVX Implementation: Process 8 butterflies per iteration (128 bytes)
// ============================================================================
// Uses 256-bit YMM registers (8 floats = 2 butterflies per register)
// Load 4 YMM = 8 butterflies, unroll for better ILP

// func kfBfly2M1AVX(fout []kissCpx, n int)
TEXT ·kfBfly2M1AVX(SB), NOSPLIT, $0-32
	MOVQ	fout_base+0(FP), DI
	MOVQ	n+24(FP), CX
	TESTQ	CX, CX
	JLE	avx_done

	// Check if we can do 8 butterflies at a time (n >= 8)
	CMPQ	CX, $8
	JL	avx_tail

avx_loop8:
	// Load 8 butterflies (128 bytes)
	// Each YMM holds 2 butterflies: [ar0,ai0,br0,bi0, ar1,ai1,br1,bi1]
	VMOVUPS	(DI), Y0       // butterflies 0-1
	VMOVUPS	32(DI), Y1     // butterflies 2-3
	VMOVUPS	64(DI), Y2     // butterflies 4-5
	VMOVUPS	96(DI), Y3     // butterflies 6-7

	// Process Y0: 2 butterflies
	// Extract a and b parts using permute
	// Y4 = [ar0,ai0,ar0,ai0, ar1,ai1,ar1,ai1] (duplicate lo halves within 128-bit lanes)
	VPERMILPS	$0x44, Y0, Y4  // [0,1,0,1, 4,5,4,5]
	// Y5 = [br0,bi0,br0,bi0, br1,bi1,br1,bi1] (duplicate hi halves within 128-bit lanes)
	VPERMILPS	$0xEE, Y0, Y5  // [2,3,2,3, 6,7,6,7]
	// sum = a + b
	VADDPS	Y5, Y4, Y6
	// diff = a - b
	VSUBPS	Y5, Y4, Y7
	// Blend: take lo half of Y6, hi half of Y7
	// Result: [ar0+br0,ai0+bi0, ar0-br0,ai0-bi0, ar1+br1,ai1+bi1, ar1-br1,ai1-bi1]
	VBLENDPS	$0xCC, Y7, Y6, Y0  // mask 0xCC = 11001100b: take Y7 for elements 2,3,6,7

	// Process Y1
	VPERMILPS	$0x44, Y1, Y4
	VPERMILPS	$0xEE, Y1, Y5
	VADDPS	Y5, Y4, Y6
	VSUBPS	Y5, Y4, Y7
	VBLENDPS	$0xCC, Y7, Y6, Y1

	// Process Y2
	VPERMILPS	$0x44, Y2, Y4
	VPERMILPS	$0xEE, Y2, Y5
	VADDPS	Y5, Y4, Y6
	VSUBPS	Y5, Y4, Y7
	VBLENDPS	$0xCC, Y7, Y6, Y2

	// Process Y3
	VPERMILPS	$0x44, Y3, Y4
	VPERMILPS	$0xEE, Y3, Y5
	VADDPS	Y5, Y4, Y6
	VSUBPS	Y5, Y4, Y7
	VBLENDPS	$0xCC, Y7, Y6, Y3

	// Store results
	VMOVUPS	Y0, (DI)
	VMOVUPS	Y1, 32(DI)
	VMOVUPS	Y2, 64(DI)
	VMOVUPS	Y3, 96(DI)

	ADDQ	$128, DI
	SUBQ	$8, CX
	CMPQ	CX, $8
	JGE	avx_loop8

avx_tail:
	// Handle remaining butterflies with SSE (1 at a time for simplicity)
	TESTQ	CX, CX
	JLE	avx_done

avx_loop1:
	VMOVUPS	(DI), X0       // [ar, ai, br, bi]

	// X4 = [ar, ai, ar, ai]
	VPERMILPS	$0x44, X0, X4
	// X5 = [br, bi, br, bi]
	VPERMILPS	$0xEE, X0, X5
	// sum and diff
	VADDPS	X5, X4, X6
	VSUBPS	X5, X4, X7
	// Blend
	VBLENDPS	$0x0C, X7, X6, X0  // mask 0x0C = 1100b: take X7 for elements 2,3
	VMOVUPS	X0, (DI)

	ADDQ	$16, DI
	DECQ	CX
	JNZ	avx_loop1

avx_done:
	VZEROUPPER
	RET


// ============================================================================
// AVX2 Implementation: Same as AVX but can use vpermpd/vpermps for better shuffles
// ============================================================================
// For this butterfly operation, AVX2 doesn't provide significant advantage
// over AVX, so we use the same implementation with potential future improvements

// func kfBfly2M1AVX2(fout []kissCpx, n int)
TEXT ·kfBfly2M1AVX2(SB), NOSPLIT, $0-32
	MOVQ	fout_base+0(FP), DI
	MOVQ	n+24(FP), CX
	TESTQ	CX, CX
	JLE	avx2_done

	// Check if we can do 8 butterflies at a time (n >= 8)
	CMPQ	CX, $8
	JL	avx2_tail

avx2_loop8:
	// Load 8 butterflies (128 bytes)
	VMOVUPS	(DI), Y0
	VMOVUPS	32(DI), Y1
	VMOVUPS	64(DI), Y2
	VMOVUPS	96(DI), Y3

	// Process Y0
	VPERMILPS	$0x44, Y0, Y4
	VPERMILPS	$0xEE, Y0, Y5
	VADDPS	Y5, Y4, Y6
	VSUBPS	Y5, Y4, Y7
	VBLENDPS	$0xCC, Y7, Y6, Y0

	// Process Y1
	VPERMILPS	$0x44, Y1, Y4
	VPERMILPS	$0xEE, Y1, Y5
	VADDPS	Y5, Y4, Y6
	VSUBPS	Y5, Y4, Y7
	VBLENDPS	$0xCC, Y7, Y6, Y1

	// Process Y2
	VPERMILPS	$0x44, Y2, Y4
	VPERMILPS	$0xEE, Y2, Y5
	VADDPS	Y5, Y4, Y6
	VSUBPS	Y5, Y4, Y7
	VBLENDPS	$0xCC, Y7, Y6, Y2

	// Process Y3
	VPERMILPS	$0x44, Y3, Y4
	VPERMILPS	$0xEE, Y3, Y5
	VADDPS	Y5, Y4, Y6
	VSUBPS	Y5, Y4, Y7
	VBLENDPS	$0xCC, Y7, Y6, Y3

	// Store results
	VMOVUPS	Y0, (DI)
	VMOVUPS	Y1, 32(DI)
	VMOVUPS	Y2, 64(DI)
	VMOVUPS	Y3, 96(DI)

	ADDQ	$128, DI
	SUBQ	$8, CX
	CMPQ	CX, $8
	JGE	avx2_loop8

avx2_tail:
	TESTQ	CX, CX
	JLE	avx2_done

avx2_loop1:
	VMOVUPS	(DI), X0
	VPERMILPS	$0x44, X0, X4
	VPERMILPS	$0xEE, X0, X5
	VADDPS	X5, X4, X6
	VSUBPS	X5, X4, X7
	VBLENDPS	$0x0C, X7, X6, X0
	VMOVUPS	X0, (DI)

	ADDQ	$16, DI
	DECQ	CX
	JNZ	avx2_loop1

avx2_done:
	VZEROUPPER
	RET
