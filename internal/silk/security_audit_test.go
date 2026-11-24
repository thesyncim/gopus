package silk

import (
	"math"
	"testing"
)

func TestSilkPLCNegativeAndSub8msNoPanic(t *testing.T) {
	dec := NewDecoder()
	out := make([]float32, 960*2)
	if _, err := dec.DecodePLCInto(BandwidthWideband, -10, out); err == nil {
		t.Fatalf("expected error for negative mono PLC frame size")
	}
	if _, err := dec.DecodePLCStereoInto(BandwidthWideband, -10, out); err == nil {
		t.Fatalf("expected error for negative stereo PLC frame size")
	}
	if n, err := dec.DecodePLCStereoInto(BandwidthWideband, 240, out); err != nil || n != 0 {
		t.Fatalf("sub-8ms stereo PLC = (%d, %v), want (0, nil)", n, err)
	}
}

func TestSilkDiv32VarQMinInt32NoPanic(t *testing.T) {
	_ = silkDiv32VarQ(math.MinInt32, 1000, 31)
	_ = silkDiv32VarQ(1000, math.MinInt32, 31)
}
