package multistream

import (
	"bytes"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// The LFE stream emits an empty first packet at this budget, then a real
// CELT packet. libopus leaves its input high-pass memory untouched by the
// low-space return, so the following packet compares the resumed input state.
func TestSurroundLowSpaceThenRealFrameMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		channels   = 8
		frameSize  = 480
		frameCount = 6
		bitrate    = 64000
		complexity = 0
	)
	pcm := generateSurroundSweep(channels, frameSize, frameCount)
	ref, err := encodeLibopusSurround(compositeSampleRate, channels, 1, compositeApplication,
		bitrate, false, false, complexity, compositeBandwidthAuto,
		frameSize, frameCount, compositeMaxPacketBytes, pcm)
	if err != nil {
		t.Fatalf("live C surround encode: %v", err)
	}
	if ref.streams != 5 {
		t.Fatalf("reference streams=%d want 5", ref.streams)
	}
	firstStreams, err := parseMultistreamPacket(ref.packets[0], ref.streams)
	if err != nil {
		t.Fatalf("parse first reference packet: %v", err)
	}
	if got := firstStreams[len(firstStreams)-1]; !bytes.Equal(got, []byte{0x73, 0x01}) {
		t.Fatalf("first LFE packet=%x want empty code-3 packet", got)
	}
	enc, err := NewEncoderDefault(compositeSampleRate, channels)
	if err != nil {
		t.Fatal(err)
	}
	enc.SetBitrate(bitrate)
	enc.SetVBR(false)
	enc.SetVBRConstraint(false)
	enc.SetComplexity(complexity)
	enc.SetBandwidthAuto()
	for frame := range frameCount {
		start := frame * frameSize * channels
		input := pcm[start : start+frameSize*channels]
		got, err := enc.EncodeFloat32WithAnalysisMaxBytes(input, frameSize, input, compositeMaxPacketBytes)
		if err != nil {
			t.Fatalf("frame %d Go encode: %v", frame, err)
		}
		if !bytes.Equal(got, ref.packets[frame]) || enc.GetFinalRange() != ref.ranges[frame] {
			t.Fatalf("frame %d firstByte=%d len Go/C=%d/%d range Go/C=%08x/%08x",
				frame, firstByteMismatch(got, ref.packets[frame]), len(got), len(ref.packets[frame]),
				enc.GetFinalRange(), ref.ranges[frame])
		}
	}
}
