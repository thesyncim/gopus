package dred

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

func TestFeatureWindowExtremeOffsetsNoOverflow(t *testing.T) {
	res := Result{
		Request:      Request{SampleRate: 48000},
		Availability: Availability{OffsetSamples: -960, MaxLatents: 4},
	}
	w := res.FeatureWindow(math.MinInt, 960, 2)
	if w.FeatureOffsetBase > 0 {
		t.Fatalf("FeatureOffsetBase overflowed to positive: %d", w.FeatureOffsetBase)
	}
}

func TestEncodeLaplaceP0MinInt32Terminates(t *testing.T) {
	var enc rangecoding.Encoder
	buf := make([]byte, 32)
	enc.Init(buf)
	encodeLaplaceP0(&enc, math.MinInt32, 16384, 16384)
}
