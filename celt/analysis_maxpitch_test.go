package celt

import (
	"math"
	"testing"
)

func TestSetAnalysisInfoClampsMaxPitchRatio(t *testing.T) {
	enc := NewEncoder(1)

	enc.SetAnalysisInfo(20, [leakBands]uint8{}, 0, 0, 1.5, true)
	if got := enc.analysisMaxPitchRatio; got != 1.0 {
		t.Fatalf("analysisMaxPitchRatio clamp high: got %.2f want 1.00", got)
	}

	enc.SetAnalysisInfo(20, [leakBands]uint8{}, 0, 0, -0.5, true)
	if got := enc.analysisMaxPitchRatio; got != 0.0 {
		t.Fatalf("analysisMaxPitchRatio clamp low: got %.2f want 0.00", got)
	}

	enc.SetAnalysisInfo(0, [leakBands]uint8{}, 0, 0, 0, false)
	if enc.analysisValid {
		t.Fatal("analysis should be invalid after SetAnalysisInfo(..., valid=false)")
	}
	if got := enc.analysisMaxPitchRatio; got != 0.0 {
		t.Fatalf("analysisMaxPitchRatio reset: got %.2f want 0.00", got)
	}
}

func TestEncodeFrameUsesAnalysisMaxPitchRatioWhenValid(t *testing.T) {
	enc := NewEncoder(1)
	enc.SetBitrate(64000)

	enc.SetAnalysisInfo(20, [leakBands]uint8{}, 0, 0, 0, true)

	const frameSize = 960
	pcm := make([]float64, frameSize)
	for i := range pcm {
		pcm[i] = 0.6 * math.Sin(2*math.Pi*220*float64(i)/48000.0)
	}

	packet, err := enc.EncodeFrame(pcm, frameSize)
	if err != nil {
		t.Fatalf("EncodeFrame failed: %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("expected non-empty packet")
	}
	if enc.prefilterGain != 0 {
		t.Fatalf("analysis max pitch ratio should suppress prefilter gain: got %.6f", enc.prefilterGain)
	}
}
