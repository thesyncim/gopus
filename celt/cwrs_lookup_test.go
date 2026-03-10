package celt

import "testing"

func icwrsLookupChecked(n, k int, y []int) (uint32, bool) {
	if n < 2 || k <= 0 || len(y) < n {
		return 0, false
	}

	i, k1 := icwrs1(y[n-1])
	base, ok := pvqUTableLookup(2, k1)
	if !ok {
		return 0, false
	}
	i += base

	j := n - 2
	k1 += abs(y[j])
	if y[j] < 0 {
		neg, ok := pvqUTableLookup(2, k1+1)
		if !ok {
			return 0, false
		}
		i += neg
	}

	for j--; j >= 0; j-- {
		remDims := n - j
		base, ok := pvqUTableLookup(remDims, k1)
		if !ok {
			return 0, false
		}
		i += base
		k1 += abs(y[j])
		if y[j] < 0 {
			neg, ok := pvqUTableLookup(remDims, k1+1)
			if !ok {
				return 0, false
			}
			i += neg
		}
	}

	return i, true
}

func TestCanUseICWRSLookupFastMatchesCoverage(t *testing.T) {
	for n := 1; n <= 64; n++ {
		for k := 0; k <= 96; k++ {
			want := n >= 2 && k > 0 && pvqUHasLookup(n, k) && pvqUHasLookup(n, k+1)
			if got := canUseICWRSLookupFast(n, k); got != want {
				t.Fatalf("canUseICWRSLookupFast(%d, %d) = %v, want %v", n, k, got, want)
			}
		}
	}
}

func TestICWRSLookupFastMatchesChecked(t *testing.T) {
	cases := []struct {
		n, k int
	}{
		{4, 2},
		{8, 4},
		{18, 5},
		{32, 5},
		{36, 7},
		{48, 5},
		{48, 6},
	}

	for _, tc := range cases {
		if !canUseICWRSLookupFast(tc.n, tc.k) {
			t.Fatalf("test case (%d,%d) unexpectedly lacks fast lookup coverage", tc.n, tc.k)
		}
		total := PVQ_V(tc.n, tc.k)
		if total == 0 {
			t.Fatalf("PVQ_V(%d, %d) = 0", tc.n, tc.k)
		}
		step := uint32(1)
		if total > 257 {
			step = total / 257
			if step == 0 {
				step = 1
			}
		}
		for idx := uint32(0); idx < total; idx += step {
			y := DecodePulses(idx, tc.n, tc.k)
			got, gotOK := icwrsLookupFast(tc.n, tc.k, y)
			want, wantOK := icwrsLookupChecked(tc.n, tc.k, y)
			if gotOK != wantOK || got != want {
				t.Fatalf("lookup mismatch for n=%d k=%d idx=%d: got (%d,%v) want (%d,%v)", tc.n, tc.k, idx, got, gotOK, want, wantOK)
			}
		}
	}
}

func BenchmarkICWRSLookupFastCovered(b *testing.B) {
	y := DecodePulses(PVQ_V(48, 5)/2, 48, 5)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = icwrsLookupFast(48, 5, y)
	}
}

func BenchmarkICWRSLookupCheckedCovered(b *testing.B) {
	y := DecodePulses(PVQ_V(48, 5)/2, 48, 5)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = icwrsLookupChecked(48, 5, y)
	}
}
