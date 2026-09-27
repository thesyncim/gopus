package multistream

import (
	"bytes"
	"github.com/thesyncim/gopus/internal/libopustest"
	"testing"
)

// Narrowband child encoders retain full mode-strided energy history across frames.
func TestSurroundTransientHistoryStrideMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, name := range []string{
		"quad/fs240/br32000/vbrfalse/cfalse/cx5",
	} {
		t.Run(name, func(t *testing.T) {
			var spec surroundFuzzSpec
			for _, candidate := range buildSurroundFuzzSweep() {
				if candidate.name == name {
					spec = candidate
					break
				}
			}
			if spec.seed == 0 {
				t.Fatal("surround matrix case missing")
			}
			pcm := seededMultichannelPCM(spec.seed, spec.channels, spec.frameSize, spec.frameCount)
			ref, err := encodeLibopusSurround(compositeSampleRate, spec.channels, 1, compositeApplication,
				spec.bitrate, spec.vbr, spec.vbrConstraint, spec.complexity, compositeBandwidthAuto,
				spec.frameSize, spec.frameCount, compositeMaxPacketBytes, pcm, false)
			if err != nil {
				t.Fatalf("live C surround encode: %v", err)
			}
			if len(ref.packets) != spec.frameCount || len(ref.ranges) != spec.frameCount {
				t.Fatalf("live C records: packets=%d ranges=%d want=%d", len(ref.packets), len(ref.ranges), spec.frameCount)
			}
			enc, err := NewEncoderDefault(compositeSampleRate, spec.channels)
			if err != nil {
				t.Fatal(err)
			}
			if enc.Streams() != ref.streams || enc.CoupledStreams() != ref.coupledStreams {
				t.Fatalf("stream layout Go=%d/%d C=%d/%d", enc.Streams(), enc.CoupledStreams(), ref.streams, ref.coupledStreams)
			}
			enc.SetBitrate(spec.bitrate)
			enc.SetVBR(spec.vbr)
			enc.SetVBRConstraint(spec.vbrConstraint)
			enc.SetComplexity(spec.complexity)
			enc.SetBandwidthAuto()
			for f := range spec.frameCount {
				frame := pcm[f*spec.frameSize*spec.channels : (f+1)*spec.frameSize*spec.channels]
				got, err := encodePacketMax(enc, frame, spec.frameSize, frame, compositeMaxPacketBytes)
				if err != nil {
					t.Fatalf("frame %d Go surround encode: %v", f, err)
				}
				if !bytes.Equal(got, ref.packets[f]) || enc.GetFinalRange() != ref.ranges[f] {
					t.Fatalf("frame %d: first byte diff=%d len Go/C=%d/%d range Go/C=%08x/%08x",
						f, firstByteMismatch(got, ref.packets[f]), len(got), len(ref.packets[f]), enc.GetFinalRange(), ref.ranges[f])
				}
			}
		})
	}

}
