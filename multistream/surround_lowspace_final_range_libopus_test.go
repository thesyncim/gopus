package multistream

import (
	"bytes"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestSurroundLowSpaceFinalRangeMatchesLibopus keeps a stateful low-budget
// witness where the trailing stream emits both bare and padded empty packets.
// opus_encode_native clears rangeFinal for each empty packet.
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
		spec.frameSize, spec.frameCount, compositeMaxPacketBytes, pcm, false)
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
	var sawBare, sawPadded bool
	for frame := range spec.frameCount {
		start := frame * spec.frameSize * spec.channels
		input := pcm[start : start+spec.frameSize*spec.channels]
		got, err := encodePacketMax(enc, input, spec.frameSize, input, compositeMaxPacketBytes)
		if err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
		if !bytes.Equal(got, ref.packets[frame]) || enc.GetFinalRange() != ref.ranges[frame] {
			t.Errorf("frame %d: firstByte=%d len Go/C=%d/%d range Go/C=%08x/%08x", frame,
				firstByteMismatch(got, ref.packets[frame]), len(got), len(ref.packets[frame]),
				enc.GetFinalRange(), ref.ranges[frame])
		}
		streams, err := parseMultistreamPacket(ref.packets[frame], ref.streams)
		if err != nil {
			t.Fatalf("frame %d: parse reference: %v", frame, err)
		}
		child := streams[4]
		parsed, err := parseOpusPacket(child, false)
		if err != nil {
			t.Fatalf("frame %d: parse trailing reference: %v", frame, err)
		}
		empty := true
		for _, payload := range parsed.frames {
			if len(payload) != 0 {
				empty = false
			}
		}
		if empty {
			if enc.encoders[4].FinalRange() != 0 {
				t.Fatalf("frame %d empty trailing child range=%08x want 0", frame, enc.encoders[4].FinalRange())
			}
			if len(child) == 1 {
				sawBare = true
			} else if len(child) == 2 {
				sawPadded = true
			}
		} else if enc.encoders[4].FinalRange() == 0 {
			t.Fatalf("frame %d coded trailing child has zero final range", frame)
		}
	}
	if !sawBare || !sawPadded {
		t.Fatalf("selected C trailing stream lacks bare/padded empty packet pair (bare=%t padded=%t)", sawBare, sawPadded)
	}
}
