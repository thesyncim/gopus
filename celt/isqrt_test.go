package celt

import "testing"

func isqrt32Reference(val uint32) uint32 {
	if val == 0 {
		return 0
	}
	g := uint32(0)
	bshift := (ilog32(val) - 1) >> 1
	b := uint32(1) << bshift
	for bshift >= 0 {
		t := (((g << 1) + b) << bshift)
		if t <= val {
			g += b
			val -= t
		}
		b >>= 1
		bshift--
		if bshift < 0 {
			break
		}
	}
	return g
}

func TestISqrt32MatchesReference(t *testing.T) {
	cases := []uint32{
		0, 1, 2, 3, 4, 15, 16, 17,
		(1 << 16) - 1, 1 << 16, (1 << 16) + 1,
		(1 << 24) - 1, 1 << 24, (1 << 24) + 1,
		^uint32(0) - 2, ^uint32(0) - 1, ^uint32(0),
	}
	for i := uint32(0); i < 65536; i += 257 {
		cases = append(cases, i*i, i*i+1)
		if i > 0 {
			cases = append(cases, i*i-1)
		}
	}
	for _, tc := range cases {
		if got, want := isqrt32(tc), isqrt32Reference(tc); got != want {
			t.Fatalf("isqrt32(%d) = %d, want %d", tc, got, want)
		}
	}
}
