package multistream

import (
	"math"
	"testing"
)

func TestMultistreamDTXSingleGain(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewEncoderDefault: %v", err)
	}

	// Encode a low-level stereo tone so PLC/DTX produces non-zero audio while
	// staying below the 16-bit saturation ceiling in libopus's FIXED_POINT +
	// ENABLE_RES24 decode_gain path (SATURATE(x, 32767) on 24-bit opus_res).
	in := make([]float32, 960*2)
	for i := range 960 {
		v := 0.001 * float32(math.Sin(2*math.Pi*440*float64(i)/48000))
		in[2*i] = v
		in[2*i+1] = v
	}
	pktBuf := make([]byte, 4000)
	var pktLen int
	for range 3 {
		pktLen, err = enc.Encode(in, 960, pktBuf)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
	}
	pkt := pktBuf[:pktLen]

	dec0, err := NewDecoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewDecoderDefault(0): %v", err)
	}
	decGain, err := NewDecoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewDecoderDefault(gain): %v", err)
	}
	// +6.02 dB (2.0x linear gain).
	decGain.SetGain(1536)

	buf0 := make([]float32, 960*2)
	bufGain := make([]float32, 960*2)
	if _, err := dec0.DecodeIntoFloat32(pkt, buf0, 960); err != nil {
		t.Fatalf("dec0 prime: %v", err)
	}
	if _, err := decGain.DecodeIntoFloat32(pkt, bufGain, 960); err != nil {
		t.Fatalf("decGain prime: %v", err)
	}

	// Decode a 1-byte DTX frame (TOC byte only). Gain must be applied once (~2x), not twice (~4x).
	dtx := []byte{pkt[0]}
	if _, err := dec0.DecodeIntoFloat32(dtx, buf0, 960); err != nil {
		t.Fatalf("dec0 DTX: %v", err)
	}
	if _, err := decGain.DecodeIntoFloat32(dtx, bufGain, 960); err != nil {
		t.Fatalf("decGain DTX: %v", err)
	}

	var e0, eGain float64
	for i := range buf0 {
		e0 += float64(buf0[i]) * float64(buf0[i])
		eGain += float64(bufGain[i]) * float64(bufGain[i])
	}
	if e0 == 0 {
		t.Fatal("expected non-zero DTX concealment energy")
	}
	ratio := math.Sqrt(eGain / e0)
	want := math.Pow(10.0, 1536.0/(20.0*256.0))
	if math.Abs(ratio-want) > 0.05 {
		t.Fatalf("DTX gain ratio = %.4f, want %.4f (double gain would be %.4f)", ratio, want, want*want)
	}
}

func TestMultistreamMalformedOversizedPacketDoesNotGrowReframeArena(t *testing.T) {
	dec, err := NewDecoder(48000, 4, 2, 2, []byte{0, 1, 2, 3})
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	// 4 MB malformed packet (code 0 frame exceeding maxOpusFrameBytes).
	bad := make([]byte, 4*1024*1024)
	bad[0] = 0xfc
	out := make([]float32, 960*4)
	if _, err := dec.DecodeIntoFloat32(bad, out, 960); err == nil {
		t.Fatal("DecodeIntoFloat32(4MB malformed) succeeded; want error")
	}
	if got := dec.reframeArena.Cap(); got != 0 {
		t.Fatalf("reframeArena cap = %d after malformed packet, want 0", got)
	}
}

func TestMultistreamInvalidPacketDoesNotCorruptLastPacketDuration(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewEncoderDefault: %v", err)
	}
	dec, err := NewDecoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewDecoderDefault: %v", err)
	}
	pktBuf := make([]byte, 4000)
	pktLen, err := enc.Encode(make([]float32, 960*2), 960, pktBuf)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	pkt := pktBuf[:pktLen]
	out := make([]float32, 960*2)
	if _, err := dec.DecodeIntoFloat32(pkt, out, 960); err != nil {
		t.Fatalf("DecodeIntoFloat32 valid: %v", err)
	}
	if got := dec.LastPacketDuration(); got != 960 {
		t.Fatalf("LastPacketDuration() = %d, want 960", got)
	}

	// Malformed packet must not overwrite lastPacketDuration.
	if _, err := dec.DecodeIntoFloat32([]byte{0x03, 0x00}, out, 960); err == nil {
		t.Fatal("DecodeIntoFloat32(malformed) succeeded; want error")
	}
	if got := dec.LastPacketDuration(); got != 960 {
		t.Fatalf("LastPacketDuration() after malformed packet = %d, want 960", got)
	}
}
