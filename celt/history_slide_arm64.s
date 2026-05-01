//go:build arm64

#include "textflag.h"

// func slidePlanarHistoryPrefixLarge(hist []float64, frameSize, keep int)
//
// Copies hist[frameSize:frameSize+keep] to hist[:keep]. The caller guarantees
// dst < src and bounds, so a forward copy is overlap-safe.
TEXT ·slidePlanarHistoryPrefixLarge(SB), NOSPLIT, $0-40
	MOVD hist_base+0(FP), R0
	MOVD frameSize+24(FP), R1
	MOVD keep+32(FP), R2

	LSL $3, R1, R1
	ADD R0, R1, R1

	LSR $2, R2, R3
	AND $3, R2, R2
	CBZ R3, slide_tail

slide_loop4:
	VLD1.P 32(R1), [V0.D2, V1.D2]
	VST1.P [V0.D2, V1.D2], 32(R0)
	SUBS $1, R3
	BNE slide_loop4

slide_tail:
	TBZ $1, R2, slide_tail1
	VLD1.P 16(R1), [V0.D2]
	VST1.P [V0.D2], 16(R0)

slide_tail1:
	TBZ $0, R2, slide_done
	FMOVD (R1), F0
	FMOVD F0, (R0)

slide_done:
	RET
