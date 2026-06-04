package encoder

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/types"
)

func TestSILKFECFirstPacketEnablesLBRRState(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeSILK)
	enc.SetBandwidth(types.BandwidthWideband)
	enc.SetBitrate(40000)
	enc.SetComplexity(10)
	enc.SetFEC(true)
	enc.SetPacketLoss(20)

	pcm := make([]float64, 960)
	for i := range pcm {
		tm := float64(i) / 48000.0
		pcm[i] = 0.5 * math.Sin(2*math.Pi*220*tm)
	}

	_, err := encodeWithAnalysisMaxBytesTest(enc, pcm, 960, pcm, 4000)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !enc.lbrrCoded {
		t.Fatal("lbrrCoded=false want true at 40 kbps WB with 20% loss")
	}
	if !enc.silkEncoder.LBRREnabled() {
		t.Fatal("silk LBRR not enabled")
	}
}
