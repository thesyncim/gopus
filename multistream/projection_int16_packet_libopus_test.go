package multistream

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestProjectionInt16PacketRangeMatchesLibopus drives the actual short public
// entry in libopus and the short callback path in gopus with identical PCM and
// controls. The matrix arithmetic oracle checks only the callback; this test
// also covers table selection, child analysis, packet framing, and final range.
func TestProjectionInt16PacketRangeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameCount = 24
	for _, tc := range []struct {
		channels  int
		frameSize int
		bitrate   int
		vbr       bool
	}{
		{4, 960, 256000, true},
		{9, 960, 256000, true},
		{16, 960, 384000, true},
		{4, 240, 128000, false},
	} {
		t.Run(fmt.Sprintf("ch%d_fs%d_br%d_vbr%t", tc.channels, tc.frameSize, tc.bitrate, tc.vbr), func(t *testing.T) {
			pcm := floatToInt16(generateAmbisonicsSweep(tc.channels, tc.frameSize, frameCount))
			ref, err := encodeLibopusProjection(48000, tc.channels, 2049, tc.bitrate, tc.vbr, true,
				10, -1000, tc.frameSize, frameCount, 4000, 1, nil, pcm)
			if err != nil {
				t.Fatalf("live C projection encode: %v", err)
			}
			enc, err := NewProjectionEncoder(48000, tc.channels)
			if err != nil {
				t.Fatal(err)
			}
			if enc.Streams() != ref.streams || enc.CoupledStreams() != ref.coupledStreams {
				t.Fatalf("layout Go=(%d,%d) C=(%d,%d)", enc.Streams(), enc.CoupledStreams(), ref.streams, ref.coupledStreams)
			}
			enc.SetBitrate(tc.bitrate)
			enc.SetVBR(tc.vbr)
			enc.SetVBRConstraint(true)
			enc.SetComplexity(10)
			enc.SetBandwidthAuto()
			out := make([]byte, 4000)
			for frame := range frameCount {
				start := frame * tc.frameSize * tc.channels
				input := pcm[start : start+tc.frameSize*tc.channels]
				n, err := enc.EncodeInt16WithAnalysis(input, tc.frameSize, input, out)
				if err != nil {
					t.Fatalf("frame %d: %v", frame, err)
				}
				if !bytes.Equal(out[:n], ref.packets[frame]) || enc.GetFinalRange() != ref.ranges[frame] {
					t.Errorf("frame %d: firstByte=%d GoLen=%d CLen=%d GoRange=%08x CRange=%08x GoCfg=%v CCfg=%v", frame,
						firstByteMismatch(out[:n], ref.packets[frame]), n, len(ref.packets[frame]), enc.GetFinalRange(), ref.ranges[frame],
						perStreamConfigs(out[:n], enc.Streams()), perStreamConfigs(ref.packets[frame], ref.streams))
				}
			}
			last := pcm[(frameCount-1)*tc.frameSize*tc.channels:]
			if _, err := enc.EncodeInt16WithAnalysis(last, tc.frameSize, last, out); err != nil {
				t.Fatalf("warm short encode: %v", err)
			}
			if allocs := testing.AllocsPerRun(20, func() {
				if _, err := enc.EncodeInt16WithAnalysis(last, tc.frameSize, last, out); err != nil {
					t.Fatalf("short encode: %v", err)
				}
			}); allocs != 0 {
				t.Fatalf("warm short caller-buffer allocations=%g want 0", allocs)
			}
		})
	}
}

func TestMultistreamShortCodingAndAnalysisInputsRemainSeparate(t *testing.T) {
	enc, err := NewEncoder(48000, 2, 1, 1, []byte{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	const frameSize = 960
	coding := make([]int16, 2*frameSize)
	analysis := make([]int16, 2*frameSize)
	coding[0], coding[1] = 12345, -23456
	analysis[0], analysis[1] = -11111, 22222
	if _, err := enc.EncodeInt16WithAnalysis(coding, frameSize, analysis, make([]byte, 4000)); err != nil {
		t.Fatal(err)
	}
	const scale = float32(1.0 / 32768.0)
	for ch := range 2 {
		if got, want := enc.streamInputScratch[0][ch], float32(coding[ch])*scale; got != want {
			t.Errorf("coding channel %d=%g want %g", ch, got, want)
		}
		if got, want := enc.analysisInputScratch[0][ch], float32(analysis[ch])*scale; got != want {
			t.Errorf("analysis channel %d=%g want %g", ch, got, want)
		}
	}
}

func TestProjectionLongFloatMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize, frameCount = 960, 24
	for _, channels := range []int{9, 16} {
		pcm16 := floatToInt16(generateAmbisonicsSweep(channels, frameSize, frameCount))
		pcm := make([]float32, len(pcm16))
		for i, s := range pcm16 {
			pcm[i] = float32(s) / 32768
		}
		bitrate := 256000
		if channels == 16 {
			bitrate = 384000
		}
		ref, err := encodeLibopusProjection(48000, channels, 2049, bitrate, true, true, 10, -1000, frameSize, frameCount, 4000, 0, pcm, nil)
		if err != nil {
			t.Fatal(err)
		}
		enc, err := NewProjectionEncoder(48000, channels)
		if err != nil {
			t.Fatal(err)
		}
		enc.SetBitrate(bitrate)
		enc.SetVBR(true)
		enc.SetVBRConstraint(true)
		enc.SetComplexity(10)
		enc.SetBandwidthAuto()
		for f := range frameCount {
			p := pcm[f*frameSize*channels : (f+1)*frameSize*channels]
			got, err := encodePacketMax(enc, p, frameSize, p, 4000)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, ref.packets[f]) || enc.GetFinalRange() != ref.ranges[f] {
				t.Fatalf("channels=%d frame=%d byte=%d GoLen=%d CLen=%d GoRange=%08x CRange=%08x", channels, f, firstByteMismatch(got, ref.packets[f]), len(got), len(ref.packets[f]), enc.GetFinalRange(), ref.ranges[f])
			}
		}
	}
}
