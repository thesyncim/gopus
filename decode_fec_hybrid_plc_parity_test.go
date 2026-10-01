package gopus

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestDecodeFECNoPacketLossChannelRoutingMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	modeCases := []struct {
		name   string
		mode   Mode
		packet func(*testing.T, int, int) []byte
	}{
		{
			name: "hybrid",
			mode: ModeHybrid,
			packet: func(t *testing.T, channels, frameSize int) []byte {
				return encodeAPIRateHybridPacketFrameSize(t, channels, frameSize)
			},
		},
		{
			name: "silk",
			mode: ModeSILK,
			packet: func(t *testing.T, channels, frameSize int) []byte {
				return encodeAPIRateSILKPacketFrameSize(t, channels, frameSize)
			},
		},
	}

	for _, modeCase := range modeCases {
		for _, codedChannels := range []int{1, 2} {
			packet := modeCase.packet(t, codedChannels, 960)
			toc := ParseTOC(packet[0])
			if toc.Mode != modeCase.mode || toc.Stereo != (codedChannels == 2) {
				t.Fatalf("%s coded channels=%d TOC=(%v, stereo=%v), want (%v, stereo=%v)",
					modeCase.name, codedChannels, toc.Mode, toc.Stereo, modeCase.mode, codedChannels == 2)
			}
			for _, apiChannels := range []int{1, 2} {
				for _, plcFrameSize := range []int{240, 480, 960, 1920} {
					for _, fec := range []bool{false, true} {
						name := modeCase.name + "/coded_" + itoaSmall(codedChannels) +
							"_api_" + itoaSmall(apiChannels) +
							"_plc_" + itoaSmall(plcFrameSize) +
							"_fec_" + itoaSmall(boolInt(fec))
						t.Run(name, func(t *testing.T) {
							decodeNoPacketLossSequenceMatchesLibopus(t, packet, apiChannels, plcFrameSize, fec)
						})
					}
				}
			}
		}
	}
}

func decodeNoPacketLossSequenceMatchesLibopus(t *testing.T, packet []byte, channels, plcFrameSize int, fec bool) {
	t.Helper()
	const primeFrameSize = 960
	steps := []libopustest.DecodeDiffCase{
		{Packet: packet, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: primeFrameSize},
		{Format: libopustest.DecodeDiffFormatFloat32, FrameSize: uint32(plcFrameSize), DecodeFEC: fec},
		{Packet: packet, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: primeFrameSize},
	}
	want, err := libopustest.ProbeDecodeSequence(48000, channels, steps)
	if err != nil {
		libopustest.HelperUnavailable(t, "stateful selected-libopus PLC sequence", err)
	}
	if len(want) != len(steps) {
		t.Fatalf("libopus returned %d sequence results, want %d", len(want), len(steps))
	}
	for i := range want {
		if want[i].Code <= 0 {
			t.Fatalf("libopus step %d returned %d", i, want[i].Code)
		}
	}

	dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	frame := make([]float32, primeFrameSize*channels)
	decodeAndCompare := func(step int, decode func([]float32) (int, error), out []float32) {
		t.Helper()
		gotSamples, err := decode(out)
		if err != nil {
			t.Fatalf("Go decode step %d: %v", step, err)
		}
		if gotSamples != int(want[step].Code) {
			t.Fatalf("Go step %d returned %d samples, want %d", step, gotSamples, want[step].Code)
		}
		gotRange, wantRange := dec.FinalRange(), want[step].FinalRange
		if gotRange != wantRange {
			t.Fatalf("Go step %d final range=%08x, C=%08x", step, gotRange, wantRange)
		}
		wantPCM := want[step].Float32()
		if len(out) < len(wantPCM) {
			t.Fatalf("Go step %d output length=%d, C PCM length=%d", step, len(out), len(wantPCM))
		}
		for i := range wantPCM {
			if gotBits, wantBits := math.Float32bits(out[i]), math.Float32bits(wantPCM[i]); gotBits != wantBits {
				t.Fatalf("step %d PCM[%d]=%08x, C=%08x", step, i, gotBits, wantBits)
			}
		}
	}
	decodeAndCompare(0, func(out []float32) (int, error) { return dec.Decode(packet, out) }, frame)

	plc := make([]float32, plcFrameSize*channels)
	decodeLoss := func(out []float32) (int, error) {
		if fec {
			return dec.DecodeWithFEC(nil, out, true)
		}
		return dec.Decode(nil, out)
	}
	decodeAndCompare(1, decodeLoss, plc)
	decodeAndCompare(2, func(out []float32) (int, error) { return dec.Decode(packet, out) }, frame)

	// The same public loss path remains allocation-free after its lazy scratch
	// buffers and channel-specific resamplers have been warmed.
	if _, err := decodeLoss(plc); err != nil {
		t.Fatalf("warm PLC decode: %v", err)
	}
	if _, err := decodeLoss(plc); err != nil {
		t.Fatalf("second warm PLC decode: %v", err)
	}
	allocs := testing.AllocsPerRun(10, func() {
		if _, err := decodeLoss(plc); err != nil {
			panic(err)
		}
	})
	if allocs != 0 {
		t.Fatalf("warmed PLC allocations=%g, want 0", allocs)
	}
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
