//go:build gopus_qext

package celt

import "testing"

// TestHD96kSynthesizeMonoMatchesIMDCTLong pins the native 96 kHz synthesis
// scaffolding (HD96kSynthesizeMono, long/non-transient case) to the
// oracle-verified HD96k long-block IMDCT primitive (hd96kIMDCTLong). The two
// must agree bit-for-bit since both drive the same size-driven kernel at
// overlap=240, N=3840; this guards the overlap=240 threading the 2b decode
// driver depends on.
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
