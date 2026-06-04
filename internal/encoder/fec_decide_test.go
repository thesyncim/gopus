package encoder

import (
	"testing"

	"github.com/thesyncim/gopus/types"
)

func TestDecideFECEnabledAtDREDSilkBitrate(t *testing.T) {
	bw := types.BandwidthWideband
	equiv := (&Encoder{bitrate: 40000, channels: 1, complexity: 10, bitrateMode: ModeCVBR}).computeEquivRate(
		40000, 1, 50, true, ModeSILK, 10, 20,
	)
	if equiv <= 0 {
		t.Fatalf("equivRate=%d", equiv)
	}
	if !decideFEC(true, 20, false, ModeSILK, &bw, equiv) {
		t.Fatalf("decideFEC=false at equiv=%d bw=%v", equiv, bw)
	}
}
