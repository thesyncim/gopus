package celt

import (
	"testing"

	"github.com/thesyncim/gopus/rangecoding"
)

func encodeThetaInvBit(t *testing.T, disableInv bool) int {
	t.Helper()

	ctx := &bandCtx{
		encode:        true,
		re:            &rangecoding.Encoder{},
		bandE:         []float64{1.0, 1.0},
		nbBands:       1,
		channels:      2,
		remainingBits: 1 << 20,
		intensity:     0, // Force qn==1 intensity path.
		band:          0,
		disableInv:    disableInv,
	}
	buf := make([]byte, 64)
	ctx.re.Init(buf)

	// Anti-phase stereo so side dominates mid and raw theta > 8192.
	const n = 16
	x := make([]float64, n)
	y := make([]float64, n)
	for i := 0; i < n; i++ {
		x[i] = 1.0
		y[i] = -1.0
	}

	b := 64 << bitRes
	fill := 1
	var sctx splitCtx
	computeTheta(ctx, &sctx, x, y, n, &b, 1, 1, 0, true, &fill)

	rd := &rangecoding.Decoder{}
	rd.Init(ctx.re.Done())
	return rd.DecodeBit(2)
}

func TestComputeThetaPhaseInversionDisable(t *testing.T) {
	enabled := encodeThetaInvBit(t, false)
	if enabled != 1 {
		t.Fatalf("phase inversion bit with disable=false = %d, want 1", enabled)
	}

	disabled := encodeThetaInvBit(t, true)
	if disabled != 0 {
		t.Fatalf("phase inversion bit with disable=true = %d, want 0", disabled)
	}
}
