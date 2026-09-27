//go:build gopus_dred || gopus_osce

package gopus

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// The loss-only splice contains every concealed frame in the 220-frame
// sequence, in playback order. Each sample is compared with the selected
// libopus decoder at the same feature set and ISA.
func TestDREDLongLossPCMMatchesLibopusRawBits(t *testing.T) {
	libopustest.RequireOracle(t)
	encoderBlob := requireLibopusEncoderNeuralModelBlob(t)
	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)
	dredDecoderBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "DRED decoder model", err)
	}
	goDecoderBlob := append(append([]byte(nil), decoderBlob...), dredDecoderBlob...)

	reference, packets := encodeDREDQualityPackets(t, encoderBlob)
	got := decodeDREDQualityPackets(t, packets, reference, goDecoderBlob, true)
	want := decodeLibopusDREDQualityPackets(t, packets, reference, decoderBlob, dredDecoderBlob, true)
	if len(packets) != 220 || got.lossFrames != 119 || want.lossFrames != 119 {
		t.Fatalf("sequence shape packets=%d loss frames Go=%d C=%d want 220/119/119", len(packets), got.lossFrames, want.lossFrames)
	}
	if got.dredFrames != want.dredFrames || got.fallbackFrames != want.fallbackFrames {
		t.Fatalf("concealment kinds DRED Go/C=%d/%d fallback Go/C=%d/%d", got.dredFrames, want.dredFrames, got.fallbackFrames, want.fallbackFrames)
	}
	if len(got.lossDecoded) != len(want.lossDecoded) {
		t.Fatalf("concealed sample count Go=%d C=%d", len(got.lossDecoded), len(want.lossDecoded))
	}
	if len(got.lossDecoded) != 119*dredQualityFrameSize*dredQualityChannels {
		t.Fatalf("concealed sample count=%d want %d", len(got.lossDecoded), 119*dredQualityFrameSize*dredQualityChannels)
	}
	for i := range got.lossDecoded {
		gb, cb := math.Float32bits(got.lossDecoded[i]), math.Float32bits(want.lossDecoded[i])
		if gb != cb {
			t.Fatalf("lost frame=%d sample=%d Go=%08x C=%08x", i/dredQualityFrameSize, i%dredQualityFrameSize, gb, cb)
		}
	}
}
