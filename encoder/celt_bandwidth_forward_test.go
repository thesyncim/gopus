package encoder

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/types"
)

func TestCELTBandwidthForwardingAndMaxClamp(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeCELT)
	enc.SetBitrateMode(ModeCBR)
	enc.SetBitrate(48000)
	enc.SetBandwidth(types.BandwidthSuperwideband)

	pcm := make([]float64, 480)
	for i := range pcm {
		pcm[i] = 0.4 * math.Sin(2*math.Pi*440*float64(i)/48000.0)
	}

	pkt, err := enc.Encode(pcm, 480)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	if len(pkt) == 0 {
		t.Fatal("empty packet")
	}
	if enc.celtEncoder == nil {
		t.Fatal("celt encoder not initialized")
	}
	if got := enc.celtEncoder.Bandwidth(); got != celt.CELTSuperwideband {
		t.Fatalf("celt bandwidth mismatch: got=%v want=%v", got, celt.CELTSuperwideband)
	}

	enc.SetMaxBandwidth(types.BandwidthWideband)
	pkt, err = enc.Encode(pcm, 480)
	if err != nil {
		t.Fatalf("encode after max clamp failed: %v", err)
	}
	if len(pkt) == 0 {
		t.Fatal("empty packet after max clamp")
	}
	if got := enc.celtEncoder.Bandwidth(); got != celt.CELTSuperwideband {
		t.Fatalf("celt forced-bandwidth mismatch after max clamp: got=%v want=%v", got, celt.CELTSuperwideband)
	}

	enc.SetBandwidthAuto()
	pkt, err = enc.Encode(pcm, 480)
	if err != nil {
		t.Fatalf("encode after bandwidth auto failed: %v", err)
	}
	if len(pkt) == 0 {
		t.Fatal("empty packet after bandwidth auto")
	}
	if got := enc.celtEncoder.Bandwidth(); got != celt.CELTWideband {
		t.Fatalf("celt auto max-bandwidth clamp mismatch: got=%v want=%v", got, celt.CELTWideband)
	}
}

func TestSetBandwidthAutoRestoresAutoClamp(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetBandwidth(types.BandwidthFullband)
	if !enc.userBandwidthSet {
		t.Fatal("SetBandwidth should mark user bandwidth forced")
	}

	enc.SetBandwidthAuto()
	if enc.userBandwidthSet {
		t.Fatal("SetBandwidthAuto left user bandwidth forced")
	}
	if got := enc.Bandwidth(); got != types.BandwidthFullband {
		t.Fatalf("Bandwidth()=%v want last concrete bandwidth %v", got, types.BandwidthFullband)
	}

	enc.SetMaxBandwidth(types.BandwidthWideband)
	if got := enc.autoClampBandwidth(types.BandwidthFullband, ModeCELT, 64000); got != types.BandwidthWideband {
		t.Fatalf("autoClampBandwidth()=%v want %v", got, types.BandwidthWideband)
	}
}
