package multistream

import (
	"bytes"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestSurroundLowSpaceFinalRangeMatchesLibopus keeps a stateful low-budget
// witness where the trailing stream emits a padded TOC-only packet after a
// coded frame.
// opus_encode_native clears rangeFinal for that packet on every call.
func TestSurroundLowSpaceFinalRangeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	var spec surroundFuzzSpec
	for _, candidate := range buildSurroundFuzzSweep() {
		if candidate.name == "surround_7_1/fs480/br64000/vbrfalse/cfalse/cx0" {
			spec = candidate
			break
		}
	}
	if spec.frameCount != 5 {
		t.Fatalf("missing 7.1 low-space witness: %+v", spec)
	}
	pcm := seededMultichannelPCM(spec.seed, spec.channels, spec.frameSize, spec.frameCount)
	ref, err := encodeLibopusSurround(compositeSampleRate, spec.channels, 1, compositeApplication,
		spec.bitrate, spec.vbr, spec.vbrConstraint, spec.complexity, compositeBandwidthAuto,
		spec.frameSize, spec.frameCount, compositeMaxPacketBytes, pcm)
	if err != nil {
		t.Fatalf("live C surround encode: %v", err)
	}
	enc, err := NewEncoderDefault(compositeSampleRate, spec.channels)
	if err != nil {
		t.Fatal(err)
	}
	enc.SetBitrate(spec.bitrate)
	enc.SetVBR(spec.vbr)
	enc.SetVBRConstraint(spec.vbrConstraint)
	enc.SetComplexity(spec.complexity)
	enc.SetBandwidthAuto()
	for frame := range spec.frameCount {
		start := frame * spec.frameSize * spec.channels
		input := pcm[start : start+spec.frameSize*spec.channels]
		got, err := enc.EncodeFloat32WithAnalysisMaxBytes(input, spec.frameSize, input, compositeMaxPacketBytes)
		if err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
		if !bytes.Equal(got, ref.packets[frame]) || enc.GetFinalRange() != ref.ranges[frame] {
			t.Errorf("frame %d: firstByte=%d len Go/C=%d/%d range Go/C=%08x/%08x", frame,
				firstByteMismatch(got, ref.packets[frame]), len(got), len(ref.packets[frame]),
				enc.GetFinalRange(), ref.ranges[frame])
		}
		if frame == 1 && enc.encoders[4].FinalRange() == 0 {
			t.Fatal("frame 1 trailing child must establish a nonzero final range")
		}
		if frame >= 2 && (len(enc.streamPacketsScratch[4]) < 1 || len(enc.streamPacketsScratch[4]) > 2 || enc.encoders[4].FinalRange() != 0) {
			t.Fatalf("frame %d low-space child len=%d range=%08x want TOC-only or padded TOC/range 0", frame,
				len(enc.streamPacketsScratch[4]), enc.encoders[4].FinalRange())
		}
	}
}
