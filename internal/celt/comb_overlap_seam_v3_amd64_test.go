//go:build amd64.v3 && !gopus_fixed_point

package celt

import (
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestCombFilterWindowSquareV3OverlapSeamMatchesLibopus(t *testing.T) {
	requireCELTV3OracleTarget(t)
	libopustest.RequireOracle(t)
	requirePairedCELTOracleMode(t)

	const (
		history = combFilterHistory
		n       = 120
		t0      = 48
		t1      = 96
		gain    = float32(0.5625)
		overlap = 120
	)
	window := GetWindowBufferF32(overlap)
	windowSq := GetWindowSquareBufferF32(overlap)
	buf := make([]float32, history+n+2)
	rng := rand.New(rand.NewSource(0x48_96_120))
	for i := range buf {
		buf[i] = (rng.Float32()*2 - 1) * 2048
	}

	// The comb filter runs in place on decode_mem. From frame sample
	// t0-2 = 46 the old-delay five-tap window reaches already filtered frame
	// samples; through sample 49 it straddles the history before the frame
	// and the frame itself.
	want := probeLibopusCombFilter(t, history, n, t0, t1, 0, 0, overlap, gain, gain, window, buf)
	got := append([]float32(nil), buf...)
	combFilterInPlace(got, history, t0, t1, n, gain, gain, 0, 0, windowSq, overlap)
	for i := range n {
		if gotBits, wantBits := math.Float32bits(got[history+i]), math.Float32bits(want[i]); gotBits != wantBits {
			t.Fatalf("sample[%d]=%08x want %08x", i, gotBits, wantBits)
		}
	}
}
