//go:build gopus_qext

package celt

import (
	"math"
	"testing"
)

func TestHD96kSynthesizeMonoMatchesIMDCTLong(t *testing.T) {
	m := NewHD96kMode()
	frameSize := m.MdctN / 2 // 1920

	spec := hd96kMDCTSeed(frameSize, 7)
	prevRef := make([]float32, m.Overlap)
	for i := range prevRef {
		prevRef[i] = float32((i*13+5)%2048 - 1024)
	}

	// Reference: oracle-verified long IMDCT primitive.
	refFull := m.hd96kIMDCTLong(spec, prevRef)
	if len(refFull) != frameSize+m.Overlap {
		t.Fatalf("hd96kIMDCTLong returned %d, want %d", len(refFull), frameSize+m.Overlap)
	}

	// Scaffolding synthesis path with overlap=240 history.
	prevSig := make([]celtSig, m.Overlap)
	for i := range prevSig {
		prevSig[i] = celtSig(prevRef[i])
	}
	var scratch imdctScratchF32
	shortCoeffs := make([]float32, frameSize)
	out := make([]float32, frameSize+m.Overlap)
	got := m.HD96kSynthesizeMono(spec, prevSig, false, &scratch, shortCoeffs, out)
	if len(got) != frameSize {
		t.Fatalf("HD96kSynthesizeMono returned %d, want %d", len(got), frameSize)
	}

	for i := 0; i < frameSize; i++ {
		if got[i] != refFull[i] {
			t.Fatalf("sample[%d]: got %v want %v", i, got[i], refFull[i])
		}
	}
	// The updated overlap tail must equal the reference tail.
	for i := 0; i < m.Overlap; i++ {
		if float32(prevSig[i]) != refFull[frameSize+i] {
			t.Fatalf("overlap[%d]: got %v want %v", i, float32(prevSig[i]), refFull[frameSize+i])
		}
	}
}

func TestNative96ResetClearsActivePostfilterHistory(t *testing.T) {
	d := NewDecoder(1)
	d.EnableHD96kMode()
	d.postfilterPeriodOld = 120
	d.postfilterPeriod = 120
	d.postfilterGainOld = 0.5
	d.postfilterGain = 0.5

	const frameSize = 1920
	samples := make([]float32, frameSize)
	for i := range samples {
		samples[i] = float32(0.3 * math.Sin(2*math.Pi*330*float64(i)/96000))
	}
	d.postfilterTest(samples, frameSize, 3, 120, 0.5, 0)
	mem := d.DecodeMem(0)
	if len(mem) != d.decodeMemHistoryLen()+240 {
		t.Fatalf("native96 decode_mem len=%d, want %d", len(mem), d.decodeMemHistoryLen()+240)
	}
	active := false
	for _, sample := range mem {
		active = active || sample != 0
	}
	if !active {
		t.Fatal("native96 postfilter did not populate decode_mem")
	}

	backing := &d.decodeMem[0]
	d.Reset()
	if len(d.DecodeMem(0)) != len(mem) {
		t.Fatal("Reset resized native96 decode_mem")
	}
	if &d.decodeMem[0] != backing {
		t.Fatal("Reset replaced native96 decode_mem instead of reusing it")
	}
	for i, sample := range d.decodeMem {
		if sample != 0 {
			t.Fatalf("Reset retained native96 decode_mem at %d: %g", i, sample)
		}
	}

	d.postfilterPeriodOld = 120
	d.postfilterPeriod = 120
	d.postfilterGainOld = 0.5
	d.postfilterGain = 0.5
	allocs := testing.AllocsPerRun(20, func() {
		d.postfilterTest(samples, frameSize, 3, 120, 0.5, 0)
	})
	if allocs != 0 {
		t.Fatalf("warmed native96 postfilter after Reset allocated %g times/call", allocs)
	}
}
