package encoder

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/types"
)

func TestLFEEffectiveBandwidthClamp(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetBandwidth(types.BandwidthFullband)

	if got := enc.effectiveBandwidth(); got != types.BandwidthFullband {
		t.Fatalf("effectiveBandwidth() before LFE = %v, want %v", got, types.BandwidthFullband)
	}

	enc.SetLFE(true)
	if got := enc.effectiveBandwidth(); got != types.BandwidthNarrowband {
		t.Fatalf("effectiveBandwidth() with LFE = %v, want %v", got, types.BandwidthNarrowband)
	}

	enc.SetLFE(false)
	if got := enc.effectiveBandwidth(); got != types.BandwidthFullband {
		t.Fatalf("effectiveBandwidth() after clearing LFE = %v, want %v", got, types.BandwidthFullband)
	}
}

func TestLFEModeForcesCELTPath(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeHybrid)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetLFE(true)

	frameSize := 960
	pcm := make([]float64, frameSize)
	for i := 0; i < frameSize; i++ {
		tm := float64(i) / 48000.0
		pcm[i] = 0.8 * math.Sin(2*math.Pi*70*tm)
	}

	packet, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	if len(packet) == 0 {
		t.Fatalf("Encode returned empty packet")
	}
	if enc.silkEncoder != nil {
		t.Fatalf("silkEncoder should remain nil for LFE CELT-only path")
	}
	if enc.celtEncoder == nil {
		t.Fatalf("celtEncoder should be initialized for LFE encode")
	}
	if !enc.celtEncoder.LFE() {
		t.Fatalf("celtEncoder.LFE() = false, want true")
	}
}
