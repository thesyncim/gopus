//go:build gopus_dred || gopus_osce

package celt

import "testing"

func TestFillPLCUpdate16kMonoDoesNotAllocate(t *testing.T) {
	d := NewDecoder(2)
	left, right := d.DecodeMem(0), d.DecodeMem(1)
	for i := 0; i < plcDecodeBufferSize; i++ {
		left[i] = celtSig(32768 * (0.6 * float64((i%31)-15) / 31))
		right[i] = celtSig(32768 * (0.4 * float64((i%19)-9) / 19))
	}

	var dst [plcUpdateSamples]float32
	if n := d.FillPLCUpdate16kMono(dst[:]); n != len(dst) {
		t.Fatalf("FillPLCUpdate16kMono=%d want %d", n, len(dst))
	}

	allocs := testing.AllocsPerRun(200, func() {
		if n := d.FillPLCUpdate16kMono(dst[:]); n != len(dst) {
			t.Fatalf("FillPLCUpdate16kMono=%d want %d", n, len(dst))
		}
	})
	if allocs != 0 {
		t.Fatalf("AllocsPerRun=%v want 0", allocs)
	}
}
