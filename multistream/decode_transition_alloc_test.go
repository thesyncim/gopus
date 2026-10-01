package multistream

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestMultistreamModeTransitionsWarmZeroAllocs(t *testing.T) {
	libopustest.RequireOracle(t)
	spec, ref := encodeTransitionGainRegressionInput(t)
	_, _, previous, next := firstTransitionStreamPackets(t, ref.packets)
	if previous.mode != streamModeCELT || next.mode != streamModeHybrid {
		t.Fatalf("transition modes=%d→%d, want CELT→Hybrid", previous.mode, next.mode)
	}
	dec, err := NewDecoder(transitionStageSampleRate, spec.channels, ref.streams, ref.coupledStreams, ref.mapping)
	if err != nil {
		t.Fatal(err)
	}
	if err := dec.SetGain(transitionStageGainQ8); err != nil {
		t.Fatal(err)
	}
	output := make([]float32, spec.frameSize*spec.channels)
	decode := func() {
		for _, packet := range ref.packets {
			if n, err := dec.DecodeIntoFloat32(packet, output, spec.frameSize); err != nil || n != spec.frameSize {
				t.Fatalf("DecodeIntoFloat32=%d, %v", n, err)
			}
		}
	}
	decode()
	decode()
	if allocs := testing.AllocsPerRun(20, decode); allocs != 0 {
		t.Fatalf("warm mode-transition allocations=%g, want 0", allocs)
	}
}
