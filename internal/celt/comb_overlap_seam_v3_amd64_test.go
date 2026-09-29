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

	// The old-delay run ends at i=45. At i=46 its five-tap window straddles
	// stored history and the in-place frame, so the precomputed-window path
	// uses its scalar seam fallback through i=49.
	seam := history - t0 - 2 + 46
	if _, count := combPlanarRun(buf[history:history+n], buf[:history], history, seam); count != 0 {
		t.Fatalf("expected a scalar history seam at frame sample 46, got run length %d", count)
	}

	want := probeLibopusCombFilter(t, history, n, t0, t1, 0, 0, overlap, gain, gain, window, buf)
	hist := make([]celtSig, history)
	for i := range hist {
		hist[i] = celtSig(buf[i])
	}
	got := append([]float32(nil), buf[history:history+n]...)
	combFilterWithSquarePlanarFloat32(got, hist, history, 0, t0, t1, n,
		gain, gain, 0, 0, window, windowSq, overlap)
	for i := range got {
		if gotBits, wantBits := math.Float32bits(got[i]), math.Float32bits(want[i]); gotBits != wantBits {
			t.Fatalf("sample[%d]=%08x want %08x", i, gotBits, wantBits)
		}
	}
}
