package celt

import (
	"math/rand"
	"slices"
	"testing"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

// decodeBandAllocationRef is the generic tf_decode, dynalloc and
// clt_compute_allocation sequence that decodeBandAllocationStd fuses.
func decodeBandAllocationRef(rd *rangecoding.Decoder, totalBits, start, end, lm int, transient bool, channels int) decodedBandAllocation {
	a := decodedBandAllocation{spread: spreadNormal}
	a.tfRes = make([]int32, end)
	tfDecode(start, end, transient, a.tfRes, lm, rd)
	if rd.Tell()+4 <= totalBits {
		a.spread = rd.DecodeICDF(spreadICDF, 5)
	}
	caps := make([]int32, end)
	initCapsInto(caps, end, lm, channels)
	a.offsets = make([]int32, end)
	totalBitsQ3, tellFrac := decodeDynallocOffsets(rd, a.offsets, caps, EBands[:], start, end, lm, channels, totalBits<<bitRes)
	a.allocTrim = 5
	if tellFrac+(6<<bitRes) <= totalBitsQ3 {
		a.allocTrim = rd.DecodeICDF(trimICDF, 7)
	}
	bitsQ3 := (totalBits << bitRes) - rd.TellFrac() - 1
	if transient && lm >= 2 && bitsQ3 >= (lm+2)<<bitRes {
		a.antiCollapseRsv = 1 << bitRes
	}
	bitsQ3 -= a.antiCollapseRsv
	a.pulses = make([]int32, end)
	a.fineQuant = make([]int32, end)
	a.finePriority = make([]int32, end)
	a.codedBands = cltComputeAllocation(start, end, a.offsets, caps, a.allocTrim, &a.intensity, &a.dualStereo,
		bitsQ3, &a.balance, a.pulses, a.fineQuant, a.finePriority, channels, lm, rd)
	return a
}

func TestDecodeBandAllocationStdMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(18))
	buf := make([]byte, 1275)
	for iter := range 20000 {
		n := 2 + rng.Intn(160)
		if iter%7 == 0 {
			n = 2 + rng.Intn(1200)
		}
		for i := range buf[:n] {
			buf[i] = byte(rng.Intn(256))
		}
		if iter%5 == 0 {
			// Long runs of zero or one bits drive dynalloc boosts and
			// band skips to their extremes.
			fill := byte(0)
			if iter%10 == 0 {
				fill = 0xff
			}
			for i := rng.Intn(n); i < n; i++ {
				buf[i] = fill
			}
		}
		lm := rng.Intn(4)
		channels := 1 + rng.Intn(2)
		start := 0
		if iter%3 == 0 {
			start = 17
		}
		end := MaxBands - rng.Intn(4)
		if start == 17 {
			end = MaxBands
		}
		transient := rng.Intn(3) == 0
		totalBits := n * 8

		var want, got rangecoding.Decoder
		want.Init(buf[:n])
		got.Init(buf[:n])
		// Consume a few header symbols so allocation starts mid-stream.
		skip := rng.Intn(40)
		for range skip {
			want.DecodeBit(1)
			got.DecodeBit(1)
		}
		ref := decodeBandAllocationRef(&want, totalBits, start, end, lm, transient, channels)
		d := &Decoder{}
		res := d.decodeBandAllocationStd(&got, totalBits, start, end, lm, transient, channels)

		wr, wv := want.State()
		gr, gv := got.State()
		if wr != gr || wv != gv || want.Tell() != got.Tell() {
			t.Fatalf("iter %d: range state got (%d,%d,%d) want (%d,%d,%d)", iter, gr, gv, got.Tell(), wr, wv, want.Tell())
		}
		if res.spread != ref.spread || res.allocTrim != ref.allocTrim || res.intensity != ref.intensity ||
			res.dualStereo != ref.dualStereo || res.balance != ref.balance || res.codedBands != ref.codedBands ||
			res.antiCollapseRsv != ref.antiCollapseRsv {
			t.Fatalf("iter %d (lm=%d C=%d start=%d end=%d): scalars got %+v want %+v", iter, lm, channels, start, end, res, ref)
		}
		for _, p := range []struct {
			name      string
			got, want []int32
		}{
			{"tfRes", res.tfRes, ref.tfRes},
			{"offsets", res.offsets, ref.offsets},
			{"pulses", res.pulses, ref.pulses},
			{"fineQuant", res.fineQuant, ref.fineQuant},
			{"finePriority", res.finePriority, ref.finePriority},
		} {
			if !slices.Equal(p.got[start:end], p.want[start:end]) {
				t.Fatalf("iter %d (lm=%d C=%d start=%d end=%d): %s got %v want %v", iter, lm, channels, start, end, p.name, p.got[start:end], p.want[start:end])
			}
		}
	}
}

func TestDecodeBandAllocationStdAllocs(t *testing.T) {
	buf := make([]byte, 200)
	for i := range buf {
		buf[i] = byte(i*37 + 11)
	}
	d := &Decoder{}
	var rd rangecoding.Decoder
	run := func() {
		rd.Init(buf)
		d.decodeBandAllocationStd(&rd, len(buf)*8, 0, MaxBands, 3, true, 2)
	}
	run()
	if n := testing.AllocsPerRun(100, run); n != 0 {
		t.Fatalf("decodeBandAllocationStd allocs = %v, want 0", n)
	}
}
