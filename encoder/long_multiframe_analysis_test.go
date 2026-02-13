package encoder

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/types"
)

func TestLongHybridMultiframeReusesAnalysisCadence(t *testing.T) {
	const frameSize = 1920

	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeHybrid)
	enc.SetBandwidth(types.BandwidthSuperwideband)
	enc.SetBitrate(48000)

	pcm := make([]float64, frameSize)
	for i := range pcm {
		pcm[i] = 0.25 * math.Sin(2*math.Pi*220*float64(i)/48000.0)
	}

	packet, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("encode long hybrid frame: %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("expected encoded packet")
	}
	if enc.analyzer == nil {
		t.Fatal("expected analyzer state")
	}

	if enc.analyzer.Count != 2 {
		t.Fatalf("unexpected analyzer count: got %d want 2", enc.analyzer.Count)
	}
	if enc.analyzer.WritePos != 2 {
		t.Fatalf("unexpected analyzer write pos: got %d want 2", enc.analyzer.WritePos)
	}
	if enc.analyzer.ReadPos != 2 {
		t.Fatalf("unexpected analyzer read pos: got %d want 2", enc.analyzer.ReadPos)
	}
	if enc.analyzer.ReadSubframe != 0 {
		t.Fatalf("unexpected analyzer read subframe: got %d want 0", enc.analyzer.ReadSubframe)
	}
}
