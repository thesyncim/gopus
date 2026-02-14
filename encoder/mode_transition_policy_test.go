package encoder

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/types"
)

func packetModeLabel(pkt []byte) Mode {
	if len(pkt) == 0 {
		return ModeAuto
	}
	cfg := int(pkt[0] >> 3)
	switch {
	case cfg <= 11:
		return ModeSILK
	case cfg <= 15:
		return ModeHybrid
	default:
		return ModeCELT
	}
}

func testToneFrame(frameSize int) []float64 {
	out := make([]float64, frameSize)
	for i := range out {
		phase := 2 * math.Pi * 440 * float64(i) / 48000.0
		out[i] = 0.2 * math.Sin(phase)
	}
	return out
}

func TestApplyCELTTransitionDelayPolicy(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.prevMode = ModeHybrid

	actual, next := enc.applyCELTTransitionDelay(960, ModeCELT)
	if actual != ModeHybrid || next != ModeCELT {
		t.Fatalf("10ms+ delay policy mismatch: actual=%v next=%v", actual, next)
	}

	actual, next = enc.applyCELTTransitionDelay(240, ModeCELT)
	if actual != ModeCELT || next != ModeCELT {
		t.Fatalf("short-frame delay policy mismatch: actual=%v next=%v", actual, next)
	}
}

func TestForcedHybridToCELTTransitionHoldsOneFrame(t *testing.T) {
	for _, frameSize := range []int{960, 1920} {
		t.Run(fmt.Sprintf("frame_%d", frameSize), func(t *testing.T) {
			enc := NewEncoder(48000, 1)
			enc.SetBitrateMode(ModeCBR)
			enc.SetBandwidth(types.BandwidthSuperwideband)
			enc.SetMode(ModeHybrid)

			pcm := testToneFrame(frameSize)
			pkt1, err := enc.Encode(pcm, frameSize)
			if err != nil {
				t.Fatalf("first encode: %v", err)
			}
			if got := packetModeLabel(pkt1); got != ModeHybrid {
				t.Fatalf("first packet mode=%v want=%v", got, ModeHybrid)
			}

			enc.SetBandwidth(types.BandwidthFullband)
			enc.SetMode(ModeCELT)

			pkt2, err := enc.Encode(pcm, frameSize)
			if err != nil {
				t.Fatalf("transition encode: %v", err)
			}
			if got := packetModeLabel(pkt2); got != ModeHybrid {
				t.Fatalf("transition packet mode=%v want=%v", got, ModeHybrid)
			}

			pkt3, err := enc.Encode(pcm, frameSize)
			if err != nil {
				t.Fatalf("post-transition encode: %v", err)
			}
			if got := packetModeLabel(pkt3); got != ModeCELT {
				t.Fatalf("post-transition packet mode=%v want=%v", got, ModeCELT)
			}
		})
	}
}
