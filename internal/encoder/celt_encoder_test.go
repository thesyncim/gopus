package encoder

import (
	"math"
	"testing"
	"unsafe"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/types"
)

func TestEnsureCELTEncoderDisablesLocalLSBQuantization(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeCELT)
	enc.SetLSBDepth(16)

	enc.ensureCELTEncoder()

	if enc.celtEncoder == nil {
		t.Fatal("ensureCELTEncoder should initialize the CELT encoder")
	}
	if enc.celtEncoder.LSBQuantizationEnabled() {
		t.Fatal("top-level CELT path should not re-quantize LSB depth after Opus ingress processing")
	}
}

func TestCELTEnergyMaskUsesFloat32Storage(t *testing.T) {
	enc := NewEncoder(48000, 2)
	mask := make([]float32, 2*celt.MaxBands)
	for i := range mask {
		mask[i] = float32(i)*0.01 + 0.00000013
	}

	enc.SetCELTEnergyMask(mask)
	if got := unsafe.Sizeof(enc.celtEnergyMask[0]); got != 4 {
		t.Fatalf("celtEnergyMask element size=%d want celt_glog-sized 4", got)
	}

	got := enc.CELTEnergyMask()
	if len(got) != len(mask) {
		t.Fatalf("CELTEnergyMask len=%d want %d", len(got), len(mask))
	}
	for i := range mask {
		if got[i] != mask[i] {
			t.Fatalf("CELTEnergyMask[%d]=%0.10g want %0.10g", i, got[i], mask[i])
		}
	}

	got[0] = 99
	if again := enc.CELTEnergyMask()[0]; again == 99 {
		t.Fatalf("CELTEnergyMask returned an alias")
	}
}

func TestCELTEnergyMaskSyncsRoundedValuesToCELT(t *testing.T) {
	enc := NewEncoder(48000, 2)
	mask := make([]float32, 2*celt.MaxBands)
	for i := range mask {
		mask[i] = -0.75 + float32(i)*0.03125 + 0.00000019
	}

	enc.SetCELTEnergyMask(mask)
	enc.ensureCELTEncoder()
	got := enc.celtEncoder.EnergyMask()
	if len(got) != len(mask) {
		t.Fatalf("CELT EnergyMask len=%d want %d", len(got), len(mask))
	}
	for i := range mask {
		if got[i] != mask[i] {
			t.Fatalf("CELT EnergyMask[%d]=%0.10g want %0.10g", i, got[i], mask[i])
		}
	}

	enc.SetCELTEnergyMask(mask[:celt.MaxBands-1])
	if got := len(enc.CELTEnergyMask()); got != 0 {
		t.Fatalf("invalid mask kept top-level length=%d", got)
	}
	if got := len(enc.celtEncoder.EnergyMask()); got != 0 {
		t.Fatalf("invalid mask kept CELT length=%d", got)
	}
}

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

	pkt, err := encodeTest(enc, pcm, 480)
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
	pkt, err = encodeTest(enc, pcm, 480)
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
	pkt, err = encodeTest(enc, pcm, 480)
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
	if got := enc.autoClampBandwidth(types.BandwidthFullband, ModeCELT, 64000, enc.maxRateForFrame(960, maxSilkPacketBytes)); got != types.BandwidthWideband {
		t.Fatalf("autoClampBandwidth()=%v want %v", got, types.BandwidthWideband)
	}
}
