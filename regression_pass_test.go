package gopus

import (
	"bytes"
	"math"
	"testing"
)

func TestDecodeWithFECZeroByteFirstFrameDTX(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	// Prime decoder with a valid SILK frame so lastPacketMode is ModeSILK.
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	pcmIn := make([]float32, 960)
	for i := range pcmIn {
		pcmIn[i] = 0.25 * float32(math.Sin(2*math.Pi*440*float64(i)/48000))
	}
	pktBuf := make([]byte, 4000)
	n, err := enc.Encode(pcmIn, pktBuf)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	pcmOut := make([]float32, 960)
	if _, err := dec.Decode(pktBuf[:n], pcmOut); err != nil {
		t.Fatalf("Decode primer: %v", err)
	}
	prevMode := dec.lastPacketMode

	// Malformed CELT packet (code 3, frame count 0) must not mutate lastPacketMode.
	if _, err := dec.DecodeWithFEC([]byte{0xff, 0x00}, pcmOut, true); err == nil {
		t.Fatal("DecodeWithFEC(malformed CELT code-3 count=0) succeeded; want error")
	}
	if dec.lastPacketMode != prevMode {
		t.Fatalf("DecodeWithFEC mutated lastPacketMode on error: got %v, want %v", dec.lastPacketMode, prevMode)
	}

	// Valid 0-byte DTX first frames across Code 1, Code 2, and Code 3 CBR/VBR.
	dtxPackets := []struct {
		name string
		data []byte
	}{
		{name: "code1_zero_frames", data: []byte{0x01}},
		{name: "code2_zero_first_frame", data: []byte{0x02, 0x00}},
		{name: "code3_cbr_zero_frames", data: []byte{0x03, 0x01}},
		{name: "code3_vbr_zero_first_frame", data: []byte{0x03, 0x82, 0x00}},
	}
	for _, tc := range dtxPackets {
		t.Run(tc.name, func(t *testing.T) {
			nDecoded, err := dec.DecodeWithFEC(tc.data, pcmOut, true)
			if err != nil {
				t.Fatalf("DecodeWithFEC(%x): %v", tc.data, err)
			}
			if nDecoded <= 0 {
				t.Fatalf("DecodeWithFEC(%x) decoded %d samples, want > 0", tc.data, nDecoded)
			}
		})
	}
}

func TestCode3HugePaddingEarlyExit(t *testing.T) {
	// Huge malformed padding stream must fail cleanly with ErrInvalidPacket.
	huge := make([]byte, 256*1024)
	huge[0] = 0x4b // Code 3
	huge[1] = 0x41 // 1 frame, padding flag set
	for i := 2; i < len(huge); i++ {
		huge[i] = 0xff
	}
	if _, err := ParsePacket(huge); err != ErrInvalidPacket {
		t.Fatalf("ParsePacket(huge 0xff padding) err = %v, want %v", err, ErrInvalidPacket)
	}
	if err := validatePacketFraming(huge); err != ErrInvalidPacket {
		t.Fatalf("validatePacketFraming(huge 0xff padding) err = %v, want %v", err, ErrInvalidPacket)
	}
	if _, err := extractFirstFramePayload(huge, ParseTOC(huge[0])); err != ErrInvalidPacket {
		t.Fatalf("extractFirstFramePayload(huge 0xff padding) err = %v, want %v", err, ErrInvalidPacket)
	}

	// Truncated single 0xff padding indicator returns ErrPacketTooShort.
	trunc := []byte{0x4b, 0x41, 0xff}
	if _, err := ParsePacket(trunc); err != ErrPacketTooShort {
		t.Fatalf("ParsePacket(truncated 0xff) err = %v, want %v", err, ErrPacketTooShort)
	}
	if err := validatePacketFraming(trunc); err != ErrPacketTooShort {
		t.Fatalf("validatePacketFraming(truncated 0xff) err = %v, want %v", err, ErrPacketTooShort)
	}
}

func TestMultistreamPacketPadZeroAlloc(t *testing.T) {
	packet := []byte{0x48, 0x11, 0x22, 0x33}
	buf := make([]byte, len(packet), 32)
	copy(buf, packet)

	// Warm up.
	if err := MultistreamPacketPad(buf, len(packet), 24, 1); err != nil {
		t.Fatalf("MultistreamPacketPad warmup: %v", err)
	}
	if _, err := MultistreamPacketUnpad(buf[:24], 24, 1); err != nil {
		t.Fatalf("MultistreamPacketUnpad warmup: %v", err)
	}

	allocs := testing.AllocsPerRun(50, func() {
		copy(buf, packet)
		if err := MultistreamPacketPad(buf, len(packet), 24, 1); err != nil {
			t.Fatalf("MultistreamPacketPad: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("MultistreamPacketPad allocs = %.1f, want 0", allocs)
	}
}

func TestMultistreamDecodeInt24SliceLongPacket(t *testing.T) {
	enc, err := NewMultistreamEncoderDefault(48000, 2, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewMultistreamEncoderDefault: %v", err)
	}
	dec, err := NewMultistreamDecoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewMultistreamDecoderDefault: %v", err)
	}

	// Encode a 20ms frame and verify exact-size allocation (not 60ms).
	pcm20 := make([]float32, 960*2)
	pkt20 := make([]byte, 4000)
	n20, err := enc.Encode(pcm20, pkt20)
	if err != nil {
		t.Fatalf("Encode 20ms: %v", err)
	}
	out20, err := dec.DecodeInt24Slice(pkt20[:n20])
	if err != nil {
		t.Fatalf("DecodeInt24Slice(20ms): %v", err)
	}
	if len(out20) != 960*2 {
		t.Fatalf("DecodeInt24Slice(20ms) len=%d, want %d", len(out20), 960*2)
	}

	// Build a valid 100ms (5 x 20ms) packet using Repacketizer and decode via DecodeInt24Slice.
	rp := NewRepacketizer()
	for range 5 {
		if err := rp.Cat(pkt20[:n20]); err != nil {
			t.Fatalf("Repacketizer.Cat: %v", err)
		}
	}
	pkt100 := make([]byte, 4000)
	n100, err := rp.Out(pkt100)
	if err != nil {
		t.Fatalf("Repacketizer.Out: %v", err)
	}
	out100, err := dec.DecodeInt24Slice(pkt100[:n100])
	if err != nil {
		t.Fatalf("DecodeInt24Slice(100ms): %v", err)
	}
	if len(out100) != 4800*2 {
		t.Fatalf("DecodeInt24Slice(100ms) len=%d, want %d", len(out100), 4800*2)
	}
}

func TestStreamWriterLargeWriteDoesNotRetainOversizedBuffer(t *testing.T) {
	sink := &slicePacketSink{}
	w, err := NewWriter(48000, 2, sink, FormatFloat32LE, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	// Write 10 full 20ms frames (19,200 float32 samples = 76,800 bytes) in one call.
	const frames = 10
	frameBytes := 960 * 2 * 4
	raw := bytes.Repeat([]byte{0, 0, 0, 0}, 960*2*frames)
	n, err := w.Write(raw)
	if err != nil {
		t.Fatalf("Writer.Write: %v", err)
	}
	if n != len(raw) {
		t.Fatalf("Writer.Write n=%d, want %d", n, len(raw))
	}
	if len(sink.packets) != frames {
		t.Fatalf("packets written = %d, want %d", len(sink.packets), frames)
	}
	if cap(w.sampleBuf) > frameBytes {
		t.Fatalf("Writer.sampleBuf cap = %d, want <= %d", cap(w.sampleBuf), frameBytes)
	}
}
