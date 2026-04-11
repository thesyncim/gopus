package gopus

import (
	"math"
	"testing"

	internalenc "github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/types"
)

func buildSingleFramePacketWithExtensionsForTest(t *testing.T, packet []byte, extensions []packetExtensionData) []byte {
	t.Helper()

	if len(packet) < 2 {
		t.Fatal("packet too short for extension test")
	}
	dst := make([]byte, len(packet)+16)
	n, err := buildPacketWithOptions(packet[0]&0xFC, [][]byte{packet[1:]}, dst, 0, false, extensions, false)
	if err != nil {
		t.Fatalf("buildPacketWithOptions: %v", err)
	}
	return dst[:n]
}

func buildMalformedSingleFrameExtensionPacketForTest(t *testing.T, packet []byte) []byte {
	t.Helper()

	if len(packet) < 2 {
		t.Fatal("packet too short for extension test")
	}
	out := make([]byte, 0, len(packet)+4)
	out = append(out, (packet[0]&0xFC)|0x03)
	out = append(out, 0x41) // one CBR frame with padding
	out = append(out, 0x01) // total padding bytes = 2
	out = append(out, packet[1:]...)
	out = append(out, 0xFF, 0xFF) // invalid long extension: truncated lacing payload
	return out
}

func TestDecoderDecodeValidUnknownExtensionMatchesBasePacket(t *testing.T) {
	base := minimalHybridTestPacket20ms()
	extended := buildSingleFramePacketWithExtensionsForTest(t, base, []packetExtensionData{
		{ID: 60, Frame: 0, Data: []byte{0x42}},
	})

	baseDec := newMonoTestDecoder(t)
	extDec := newMonoTestDecoder(t)

	basePCM := make([]float32, 960)
	extPCM := make([]float32, 960)

	baseN, err := baseDec.Decode(base, basePCM)
	if err != nil {
		t.Fatalf("Decode(base): %v", err)
	}
	extN, err := extDec.Decode(extended, extPCM)
	if err != nil {
		t.Fatalf("Decode(extended): %v", err)
	}
	if extN != baseN {
		t.Fatalf("Decode sample count=%d want %d", extN, baseN)
	}
	for i := range basePCM {
		if extPCM[i] != basePCM[i] {
			t.Fatalf("sample[%d]=%v want %v", i, extPCM[i], basePCM[i])
		}
	}
}

func TestDecoderOpaquePaddingRemainsDecodableInDefaultBuild(t *testing.T) {
	base := minimalHybridTestPacket20ms()
	malformed := buildMalformedSingleFrameExtensionPacketForTest(t, base)

	var (
		wantN   int
		wantPCM []float32
	)
	for _, ignore := range []bool{false, true} {
		dec := newMonoTestDecoder(t)
		dec.SetIgnoreExtensions(ignore)
		gotPCM := make([]float32, 960)
		gotN, err := dec.Decode(malformed, gotPCM)
		if err != nil {
			t.Fatalf("Decode(malformed, ignore=%v): %v", ignore, err)
		}
		if wantPCM == nil {
			wantN = gotN
			wantPCM = append([]float32(nil), gotPCM[:gotN]...)
			continue
		}
		if gotN != wantN {
			t.Fatalf("Decode sample count=%d want %d (ignore=%v)", gotN, wantN, ignore)
		}
		for i := range wantPCM {
			if gotPCM[i] != wantPCM[i] {
				t.Fatalf("sample[%d]=%v want %v (ignore=%v)", i, gotPCM[i], wantPCM[i], ignore)
			}
		}
	}
}

func makeValidCELTPacketForExtensionTest(t *testing.T) []byte {
	t.Helper()

	enc := internalenc.NewEncoder(48000, 2)
	enc.SetMode(internalenc.ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(256000)

	pcm := make([]float64, 960*2)
	for i := 0; i < 960; i++ {
		phase := 2 * math.Pi * 997 * float64(i) / 48000.0
		pcm[2*i] = 0.45 * math.Sin(phase)
		pcm[2*i+1] = 0.35 * math.Sin(phase+0.37)
	}

	packet, err := enc.Encode(pcm, 960)
	if err != nil {
		t.Fatalf("Encode(CELT): %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("Encode(CELT) returned empty packet")
	}
	return packet
}

func TestDecoderCELTOpaquePaddingRemainsDecodable(t *testing.T) {
	base := makeValidCELTPacketForExtensionTest(t)
	malformed := buildMalformedSingleFrameExtensionPacketForTest(t, base)

	for _, ignore := range []bool{false, true} {
		baseDec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
		if err != nil {
			t.Fatalf("NewDecoder(base): %v", err)
		}
		baseDec.SetIgnoreExtensions(ignore)
		basePCM := make([]float32, 960*2)
		baseN, err := baseDec.Decode(base, basePCM)
		if err != nil {
			t.Fatalf("Decode(base, ignore=%v): %v", ignore, err)
		}

		extDec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
		if err != nil {
			t.Fatalf("NewDecoder(extended): %v", err)
		}
		extDec.SetIgnoreExtensions(ignore)
		extPCM := make([]float32, 960*2)
		extN, err := extDec.Decode(malformed, extPCM)
		if err != nil {
			t.Fatalf("Decode(malformed, ignore=%v): %v", ignore, err)
		}

		if extN != baseN {
			t.Fatalf("Decode sample count=%d want %d (ignore=%v)", extN, baseN, ignore)
		}
		for i := 0; i < extN*2; i++ {
			if math.IsNaN(float64(extPCM[i])) || math.IsInf(float64(extPCM[i]), 0) {
				t.Fatalf("sample[%d]=%v invalid (ignore=%v)", i, extPCM[i], ignore)
			}
		}
	}
}

func TestDecoderCELTUnsupportedQEXTExtensionMatchesBasePacket(t *testing.T) {
	if SupportsOptionalExtension(OptionalExtensionQEXT) {
		t.Skip("test covers the default unsupported-QEXT build")
	}

	base := makeValidCELTPacketForExtensionTest(t)
	extended := buildSingleFramePacketWithExtensionsForTest(t, base, []packetExtensionData{
		{ID: qextPacketExtensionID, Frame: 0, Data: []byte{0x29, 0x17, 0xA4, 0x53}},
	})

	for _, ignore := range []bool{false, true} {
		baseDec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
		if err != nil {
			t.Fatalf("NewDecoder(base): %v", err)
		}
		baseDec.SetIgnoreExtensions(ignore)
		basePCM := make([]float32, 960*2)
		baseN, err := baseDec.Decode(base, basePCM)
		if err != nil {
			t.Fatalf("Decode(base, ignore=%v): %v", ignore, err)
		}

		extDec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
		if err != nil {
			t.Fatalf("NewDecoder(extended): %v", err)
		}
		extDec.SetIgnoreExtensions(ignore)
		extPCM := make([]float32, 960*2)
		extN, err := extDec.Decode(extended, extPCM)
		if err != nil {
			t.Fatalf("Decode(extended, ignore=%v): %v", ignore, err)
		}

		if extN != baseN {
			t.Fatalf("Decode sample count=%d want %d (ignore=%v)", extN, baseN, ignore)
		}
		for i := 0; i < extN*2; i++ {
			if extPCM[i] != basePCM[i] {
				t.Fatalf("sample[%d]=%v want %v (ignore=%v)", i, extPCM[i], basePCM[i], ignore)
			}
		}
	}
}
