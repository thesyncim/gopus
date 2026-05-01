package celt

import (
	"math/rand"
	"reflect"
	"testing"
)

func TestKfBfly3InnerCOrderMatchesGeneric(t *testing.T) {
	cases := []struct {
		name    string
		m       int
		N       int
		mm      int
		fstride int
	}{
		{name: "single", m: 8, N: 1, mm: 24, fstride: 5},
		{name: "multi", m: 4, N: 5, mm: 12, fstride: 3},
		{name: "narrow", m: 2, N: 7, mm: 6, fstride: 9},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rng := rand.New(rand.NewSource(int64(tc.m*1000 + tc.N*100 + tc.fstride)))
			foutLen := (tc.N-1)*tc.mm + 3*tc.m
			got := make([]kissCpx, foutLen)
			want := make([]kissCpx, foutLen)
			twLen := tc.fstride*tc.m + 1
			if n := 2*tc.fstride*(tc.m-1) + 1; n > twLen {
				twLen = n
			}
			tw := make([]kissCpx, twLen)
			for i := range got {
				v := kissCpx{r: rng.Float32()*2 - 1, i: rng.Float32()*2 - 1}
				got[i] = v
				want[i] = v
			}
			for i := range tw {
				tw[i] = kissCpx{r: rng.Float32()*2 - 1, i: rng.Float32()*2 - 1}
			}

			kfBfly3InnerCOrder(got, tw, tc.m, tc.N, tc.mm, tc.fstride)
			kfBfly3InnerCOrderGeneric(want, tw, tc.m, tc.N, tc.mm, tc.fstride)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("mismatch")
			}
		})
	}
}
