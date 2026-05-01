//go:build arm64 && !purego
#include "textflag.h"

// func deinterleaveStereoIntoImpl(interleaved, left, right []float64, n int)
TEXT ·deinterleaveStereoIntoImpl(SB), NOSPLIT, $0-80
	MOVD interleaved_base+0(FP), R0
	MOVD left_base+24(FP), R1
	MOVD right_base+48(FP), R2
	MOVD n+72(FP), R3

	CMP  $2, R3
	BLT  dsi_tail_init

dsi_loop2:
	MOVD (R0), R4
	MOVD 8(R0), R5
	MOVD 16(R0), R6
	MOVD 24(R0), R7
	MOVD R4, (R1)
	MOVD R6, 8(R1)
	MOVD R5, (R2)
	MOVD R7, 8(R2)
	ADD  $32, R0
	ADD  $16, R1
	ADD  $16, R2
	SUBS $2, R3
	CMP  $2, R3
	BGE  dsi_loop2

dsi_tail_init:
	CBZ  R3, dsi_done
	MOVD (R0), R4
	MOVD 8(R0), R5
	MOVD R4, (R1)
	MOVD R5, (R2)

dsi_done:
	RET

// func interleaveStereoIntoImpl(left, right, interleaved []float64, n int)
TEXT ·interleaveStereoIntoImpl(SB), NOSPLIT, $0-80
	MOVD left_base+0(FP), R0
	MOVD right_base+24(FP), R1
	MOVD interleaved_base+48(FP), R2
	MOVD n+72(FP), R3

	CMP  $2, R3
	BLT  isi_tail_init

isi_loop2:
	MOVD (R0), R4
	MOVD 8(R0), R5
	MOVD (R1), R6
	MOVD 8(R1), R7
	MOVD R4, (R2)
	MOVD R6, 8(R2)
	MOVD R5, 16(R2)
	MOVD R7, 24(R2)
	ADD  $16, R0
	ADD  $16, R1
	ADD  $32, R2
	SUBS $2, R3
	CMP  $2, R3
	BGE  isi_loop2

isi_tail_init:
	CBZ  R3, isi_done
	MOVD (R0), R4
	MOVD (R1), R5
	MOVD R4, (R2)
	MOVD R5, 8(R2)

isi_done:
	RET
