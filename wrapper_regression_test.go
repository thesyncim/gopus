package gopus

import (
	"testing"

	"github.com/thesyncim/gopus/multistream"
)

func assertEncodeInt16InvalidFrameSizeNoPanic(t *testing.T, name string, fn func() (int, error)) {
	t.Helper()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%s panicked: %v", name, r)
		}
	}()

	if n, err := fn(); err != ErrInvalidFrameSize || n != 0 {
		t.Fatalf("%s = (%d, %v), want (0, %v)", name, n, err, ErrInvalidFrameSize)
	}
}

func TestEncoderEncodeInt16RejectsOversizedInputWithoutPanic(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)
	data := make([]byte, 4000)
	oversized := make([]int16, 5760*enc.Channels()+1)

	assertEncodeInt16InvalidFrameSizeNoPanic(t, "EncodeInt16", func() (int, error) {
		return enc.EncodeInt16(oversized, data)
	})
}

func TestMultistreamEncoderEncodeInt16RejectsOversizedInputWithoutPanic(t *testing.T) {
	enc := mustNewDefaultMultistreamEncoder(t, 48000, 3, ApplicationAudio)
	data := make([]byte, 4000*enc.Streams())
	oversized := make([]int16, 5760*enc.Channels()+1)

	assertEncodeInt16InvalidFrameSizeNoPanic(t, "MultistreamEncoder.EncodeInt16", func() (int, error) {
		return enc.EncodeInt16(oversized, data)
	})
}

func TestDecoderResetClearsReportedState(t *testing.T) {
	dec := newMonoTestDecoder(t)
	dec.lastDataLen = 2
	dec.mainDecodeRng = 0x12345678
	dec.redundantRng = 0x01020304
	dec.lastFrameSize = 480
	dec.lastPacketDuration = 480

	if !dec.InDTX() {
		t.Fatal("InDTX() = false before Reset, want true")
	}
	if got := dec.FinalRange(); got == 0 {
		t.Fatal("FinalRange() = 0 before Reset, want non-zero test state")
	}

	dec.Reset()

	if dec.InDTX() {
		t.Fatal("InDTX() = true after Reset, want false")
	}
	if got := dec.FinalRange(); got != 0 {
		t.Fatalf("FinalRange() = 0x%08X after Reset, want 0", got)
	}
	if got := dec.LastPacketDuration(); got != 0 {
		t.Fatalf("LastPacketDuration() = %d after Reset, want 0", got)
	}
}

func TestMultistreamDecoderFirstPacketDurationMatrix(t *testing.T) {
	const sampleRate = 48000
	const channels = 3

	type decodeCase struct {
		name   string
		decode func(*MultistreamDecoder, []byte, int) (int, error)
	}

	decodeCases := []decodeCase{
		{
			name: "float32",
			decode: func(dec *MultistreamDecoder, packet []byte, frameSize int) (int, error) {
				pcm := make([]float32, frameSize*channels)
				return dec.Decode(packet, pcm)
			},
		},
		{
			name: "int16",
			decode: func(dec *MultistreamDecoder, packet []byte, frameSize int) (int, error) {
				pcm := make([]int16, frameSize*channels)
				return dec.DecodeInt16(packet, pcm)
			},
		},
	}

	frameSizes := []int{120, 240, 1920, 2880, 5760}
	for _, fs := range frameSizes {
		fs := fs
		for _, dc := range decodeCases {
			dc := dc
			t.Run(dc.name+"_"+frameSizeLabel(fs), func(t *testing.T) {
				enc := mustNewDefaultMultistreamEncoder(t, sampleRate, channels, ApplicationAudio)
				dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)

				if err := enc.SetFrameSize(fs); err != nil {
					t.Fatalf("SetFrameSize(%d) error: %v", fs, err)
				}

				packet, err := enc.EncodeFloat32(generateSurroundTestSignal(sampleRate, fs, channels))
				if err != nil {
					t.Fatalf("EncodeFloat32(%d) error: %v", fs, err)
				}

				n, err := dc.decode(dec, packet, fs)
				if err != nil {
					t.Fatalf("%s decode error: %v", dc.name, err)
				}
				if n != fs {
					t.Fatalf("%s decoded %d samples, want %d", dc.name, n, fs)
				}
			})
		}
	}
}

func TestMultistreamDecoderTracksPacketDurationChanges(t *testing.T) {
	const sampleRate = 48000
	const channels = 3

	enc := mustNewDefaultMultistreamEncoder(t, sampleRate, channels, ApplicationAudio)
	dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)

	frameSizes := []int{960, 120, 240, 1920, 2880, 5760, 480}
	for _, fs := range frameSizes {
		if err := enc.SetFrameSize(fs); err != nil {
			t.Fatalf("SetFrameSize(%d) error: %v", fs, err)
		}

		packet, err := enc.EncodeFloat32(generateSurroundTestSignal(sampleRate, fs, channels))
		if err != nil {
			t.Fatalf("EncodeFloat32(%d) error: %v", fs, err)
		}

		pcm := make([]float32, fs*channels)
		n, err := dec.Decode(packet, pcm)
		if err != nil {
			t.Fatalf("Decode(%d) error: %v", fs, err)
		}
		if n != fs {
			t.Fatalf("Decode(%d) = %d, want %d", fs, n, fs)
		}
	}

	plcFrameSize := frameSizes[len(frameSizes)-1]
	plcPCM := make([]float32, plcFrameSize*channels)
	n, err := dec.Decode(nil, plcPCM)
	if err != nil {
		t.Fatalf("Decode(nil) error: %v", err)
	}
	if n != plcFrameSize {
		t.Fatalf("Decode(nil) = %d, want %d", n, plcFrameSize)
	}
}

func TestMultistreamDecoderEmptyPacketRejected(t *testing.T) {
	dec := mustNewDefaultMultistreamDecoder(t, 48000, 3)
	pcm := make([]float32, 960*dec.Channels())

	if _, err := dec.Decode([]byte{}, pcm); err != multistream.ErrPacketTooShort {
		t.Fatalf("Decode(empty) error = %v, want %v", err, multistream.ErrPacketTooShort)
	}
}

func frameSizeLabel(frameSize int) string {
	switch frameSize {
	case 120:
		return "2p5ms"
	case 240:
		return "5ms"
	case 480:
		return "10ms"
	case 960:
		return "20ms"
	case 1920:
		return "40ms"
	case 2880:
		return "60ms"
	case 5760:
		return "120ms"
	default:
		return "fs"
	}
}
