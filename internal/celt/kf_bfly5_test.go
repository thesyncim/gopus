package celt

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

func TestKfBfly5N1MatchesReference(t *testing.T) {
	cases := []struct {
		name    string
		m       int
		fstride int
	}{
		{name: "nfft60", m: 12, fstride: 8},
		{name: "nfft120", m: 24, fstride: 4},
		{name: "nfft240", m: 48, fstride: 2},
		{name: "nfft480", m: 96, fstride: 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rng := rand.New(rand.NewSource(int64(tc.m*100 + tc.fstride)))
			foutGot := make([]kissCpx, 5*tc.m)
			foutWant := make([]kissCpx, 5*tc.m)
			twLen := 2*tc.fstride*tc.m + 1
			if tw4Len := 4*tc.fstride*(tc.m-1) + 1; tw4Len > twLen {
				twLen = tw4Len
			}
			tw := make([]kissCpx, twLen)
			for i := range foutGot {
				v := kissCpx{
					r: rng.Float32()*2 - 1,
					i: rng.Float32()*2 - 1,
				}
				foutGot[i] = v
				foutWant[i] = v
			}
			for i := range tw {
				tw[i] = kissCpx{
					r: rng.Float32()*2 - 1,
					i: rng.Float32()*2 - 1,
				}
			}

			kfBfly5N1(foutGot, tw, tc.m, tc.fstride)
			kfBfly5N1CurrentReference(foutWant, tw, tc.m, tc.fstride)
			if !reflect.DeepEqual(foutGot, foutWant) {
				t.Fatalf("mismatch")
			}
		})
	}
}

func BenchmarkKfBfly5N1Current(b *testing.B) {
	cases := []struct {
		name    string
		m       int
		fstride int
	}{
		{name: "nfft60", m: 12, fstride: 8},
		{name: "nfft120", m: 24, fstride: 4},
		{name: "nfft240", m: 48, fstride: 2},
		{name: "nfft480", m: 96, fstride: 1},
	}

	for _, tc := range cases {
		b.Run(fmt.Sprintf("%s_m%d_f%d", tc.name, tc.m, tc.fstride), func(b *testing.B) {
			rng := rand.New(rand.NewSource(int64(tc.m*100 + tc.fstride)))
			base := make([]kissCpx, 5*tc.m)
			twLen := 2*tc.fstride*tc.m + 1
			if tw4Len := 4*tc.fstride*(tc.m-1) + 1; tw4Len > twLen {
				twLen = tw4Len
			}
			tw := make([]kissCpx, twLen)
			work := make([]kissCpx, len(base))
			for i := range base {
				base[i] = kissCpx{
					r: rng.Float32()*2 - 1,
					i: rng.Float32()*2 - 1,
				}
			}
			for i := range tw {
				tw[i] = kissCpx{
					r: rng.Float32()*2 - 1,
					i: rng.Float32()*2 - 1,
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				copy(work, base)
				kfBfly5N1(work, tw, tc.m, tc.fstride)
			}
		})
	}
}

func BenchmarkKfBfly5N1Reference(b *testing.B) {
	cases := []struct {
		name    string
		m       int
		fstride int
	}{
		{name: "nfft60", m: 12, fstride: 8},
		{name: "nfft120", m: 24, fstride: 4},
		{name: "nfft240", m: 48, fstride: 2},
		{name: "nfft480", m: 96, fstride: 1},
	}

	for _, tc := range cases {
		b.Run(fmt.Sprintf("%s_m%d_f%d", tc.name, tc.m, tc.fstride), func(b *testing.B) {
			rng := rand.New(rand.NewSource(int64(tc.m*100 + tc.fstride)))
			base := make([]kissCpx, 5*tc.m)
			twLen := 2*tc.fstride*tc.m + 1
			if tw4Len := 4*tc.fstride*(tc.m-1) + 1; tw4Len > twLen {
				twLen = tw4Len
			}
			tw := make([]kissCpx, twLen)
			work := make([]kissCpx, len(base))
			for i := range base {
				base[i] = kissCpx{
					r: rng.Float32()*2 - 1,
					i: rng.Float32()*2 - 1,
				}
			}
			for i := range tw {
				tw[i] = kissCpx{
					r: rng.Float32()*2 - 1,
					i: rng.Float32()*2 - 1,
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				copy(work, base)
				kfBfly5N1CurrentReference(work, tw, tc.m, tc.fstride)
			}
		})
	}
}
