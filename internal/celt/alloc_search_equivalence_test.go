package celt

import (
	"math/rand"
	"testing"
)

// TestAllocSearchSumsMatchReference checks the branch-free bisection sums of
// clt_compute_allocation and interp_bits2pulses against the libopus
// celt/rate.c loops written literally.
func TestAllocSearchSumsMatchReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0xa110c))
	for iter := 0; iter < 50000; iter++ {
		n := 1 + rng.Intn(21)
		channels := int32(1 + rng.Intn(2))
		floor := channels << bitRes
		bandScale := make([]int32, n)
		alloc := make([]int32, n)
		trim := make([]int32, n)
		offsets := make([]int32, n)
		thresh := make([]int32, n)
		caps := make([]int32, n)
		bits1 := make([]int32, n)
		bits2 := make([]int32, n)
		for j := range n {
			bandScale[j] = channels * int32(1+rng.Intn(176))
			alloc[j] = int32(rng.Intn(200))
			trim[j] = int32(rng.Intn(4000) - 2000)
			offsets[j] = int32(rng.Intn(600))
			thresh[j] = int32(rng.Intn(400))
			caps[j] = int32(rng.Intn(3000))
			bits1[j] = int32(rng.Intn(3000))
			bits2[j] = int32(rng.Intn(3000))
		}

		want := int32(0)
		done := false
		for j := n - 1; j >= 0; j-- {
			bitsj := (bandScale[j] * alloc[j]) >> 2
			if bitsj > 0 {
				bitsj = max(0, bitsj+trim[j])
			}
			bitsj += offsets[j]
			if bitsj >= thresh[j] || done {
				done = true
				want += min(bitsj, caps[j])
			} else if bitsj >= floor {
				want += floor
			}
		}
		if got := allocSearchSum(bandScale, alloc, trim, offsets, thresh, caps, floor); got != want {
			t.Fatalf("iter %d: allocSearchSum = %d, want %d", iter, got, want)
		}

		mid := int32(rng.Intn(1<<allocSteps + 1))
		want = 0
		done = false
		for j := n - 1; j >= 0; j-- {
			tmp := bits1[j] + ((mid * bits2[j]) >> allocSteps)
			if tmp >= thresh[j] || done {
				done = true
				want += min(tmp, caps[j])
			} else if tmp >= floor {
				want += floor
			}
		}
		if got := interpSearchSum(bits1, bits2, thresh, caps, mid, floor); got != want {
			t.Fatalf("iter %d: interpSearchSum = %d, want %d", iter, got, want)
		}
	}
}

func TestBandDivisionHelpersMatchDivision(t *testing.T) {
	rng := rand.New(rand.NewSource(0xd1f))
	for iter := 0; iter < 200000; iter++ {
		n := int(int32(rng.Uint32()))
		if rng.Intn(2) == 0 {
			n = rng.Intn(20000) - 10000
		}
		d := 1 + rng.Intn(3)
		if got, want := celtSudivBalance(n, d), celtSudiv(n, d); got != want {
			t.Fatalf("celtSudivBalance(%d, %d) = %d, want %d", n, d, got, want)
		}
		m := rng.Intn(1 << 20)
		B := 1 << rng.Intn(4)
		if rng.Intn(8) == 0 {
			B = 1 + rng.Intn(9)
		}
		if got, want := celtUdivBlocks(m, B), celtUdiv(m, B); got != want {
			t.Fatalf("celtUdivBlocks(%d, %d) = %d, want %d", m, B, got, want)
		}
	}
}
