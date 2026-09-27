//go:build gopus_qext && !gopus_fixed_point

package gopus

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/benchutil"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestPublicQEXTHybridNative96TransitionsMatchSelectedFloatReference(t *testing.T) {
	libopustest.RequireOracle(t)
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "QEXT-enabled opus_demo", err)
		return
	}

	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(map[int]string{1: "mono", 2: "stereo"}[channels], func(t *testing.T) {
			celtPacket := makeFloatQEXTCELTPacketForNative96Transition(t, opusDemo, channels)
			hybridPacket := makeHybridQEXTPacketForTest(t, opusDemo, channels)
			for _, sequence := range []struct {
				name    string
				packets [][]byte
			}{
				{name: "received-hybrid", packets: [][]byte{hybridPacket, hybridPacket, hybridPacket}},
				{name: "celt-loss", packets: [][]byte{celtPacket, celtPacket, nil}},
				{name: "celt-hybrid-transition", packets: [][]byte{celtPacket, celtPacket, hybridPacket, hybridPacket, hybridPacket, hybridPacket, celtPacket, celtPacket, celtPacket}},
			} {
				t.Run(sequence.name, func(t *testing.T) {
					for frame, packet := range sequence.packets {
						if len(packet) == 0 {
							continue
						}
						wantMode := ModeHybrid
						switch sequence.name {
						case "celt-loss":
							wantMode = ModeCELT
						case "celt-hybrid-transition":
							if frame < 2 || frame > 5 {
								wantMode = ModeCELT
							}
						}
						if got := ParseTOC(packet[0]).Mode; got != wantMode {
							t.Fatalf("frame %d mode=%v, want %v", frame, got, wantMode)
						}
					}
					assertFloatQEXTHybridNative96Sequence(t, channels, sequence.packets)
				})
			}
		})
	}
}

func assertFloatQEXTHybridNative96Sequence(t *testing.T, channels int, packets [][]byte) {
	t.Helper()
	const frameSize = 1920
	want, err := libopustest.ProbeQEXTDecode96k(libopustest.QEXTDecode96kParams{
		SampleFormat: libopustest.QEXTDecode96kFormatFloat32,
		Channels:     channels,
		MaxFrameSize: frameSize,
		Packets:      packets,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "selected native-96 float-QEXT decoder", err)
		return
	}
	if len(packets) > 6 && ParseTOC(packets[6][0]).Mode == ModeCELT && ParseTOC(packets[5][0]).Mode == ModeHybrid {
		probe, probeErr := NewDecoder(DefaultDecoderConfig(96000, channels))
		if probeErr != nil {
			t.Fatal(probeErr)
		}
		probeFrame := make([]float32, frameSize*channels)
		for frame := 0; frame < 6; frame++ {
			if _, probeErr := probe.Decode(packets[frame], probeFrame); probeErr != nil {
				t.Fatalf("probe frame %d: %v", frame, probeErr)
			}
		}
		const transitionFrameSize = 480 // the CELT-mode transition conceals 5 ms at 96 kHz
		plc := make([]float32, transitionFrameSize*channels)
		if probeErr := probe.hybridDecoder.DecodePLCToFloat32WithPacketStereoInto(transitionFrameSize, channels == 2, plc); probeErr != nil {
			t.Fatalf("direct Hybrid PLC probe: %v", probeErr)
		}
		for i := 0; i < 120*channels; i++ {
			if got, expected := math.Float32bits(plc[i]), math.Float32bits(want.PCM[6*frameSize*channels+i]); got != expected {
				t.Fatalf("direct Hybrid PLC transition prefix[%d]=%08x C=%08x", i, got, expected)
			}
		}
	}
	dec, err := NewDecoder(DefaultDecoderConfig(96000, channels))
	if err != nil {
		t.Fatal(err)
	}
	out := make([]float32, frameSize*channels)
	run := func() {
		for frame, packet := range packets {
			n, err := dec.Decode(packet, out)
			if err != nil || n != frameSize {
				t.Fatalf("frame %d decode=(%d,%v), want (%d,nil)", frame, n, err, frameSize)
			}
			if got, expected := dec.FinalRange(), want.FinalRanges[frame]; got != expected {
				t.Fatalf("frame %d FinalRange=%08x C=%08x", frame, got, expected)
			}
			start := frame * len(out)
			for i, sample := range out {
				if got, expected := math.Float32bits(sample), math.Float32bits(want.PCM[start+i]); got != expected {
					t.Fatalf("frame %d float32[%d]=%08x C=%08x", frame, i, got, expected)
				}
			}
		}
	}
	run()
	dec.Reset()
	run()
	var decodeErr error
	allocs := testing.AllocsPerRun(20, func() {
		_, decodeErr = dec.Decode(packets[len(packets)-1], out)
	})
	if decodeErr != nil {
		t.Fatalf("warmed native-96 Hybrid decode: %v", decodeErr)
	}
	if allocs != 0 {
		t.Fatalf("warmed native-96 Hybrid decode allocated %g times/call", allocs)
	}
}

func makeFloatQEXTCELTPacketForNative96Transition(t *testing.T, opusDemo string, channels int) []byte {
	t.Helper()
	pcm := make([]float32, 960*channels)
	for i := 0; i < 960; i++ {
		tm := float64(i) / 48000
		left := float32(0.31*math.Sin(2*math.Pi*6200*tm) + 0.19*math.Sin(2*math.Pi*21800*tm))
		pcm[i*channels] = left
		if channels == 2 {
			pcm[i*channels+1] = float32(float64(left) * 0.87)
		}
	}
	return encodeLibopusPacketAtBitrate(t, opusDemo, channels, pcm, true, true, 256000)
}
