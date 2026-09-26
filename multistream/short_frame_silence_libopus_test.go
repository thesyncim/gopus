package multistream

import (
	"bytes"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestShortFrameCompositeSilenceMatchesLibopus exercises the CELT silence
// decision on 2.5 and 5 ms frames. libopus celt_encoder.c uses the coded
// channel count for its raw-input max scan while pre-emphasizing the physical
// channel count; these streams include stereo input with a mono-coded child.
func TestShortFrameCompositeSilenceMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	for _, name := range []string{
		"stereo/fs120/br32000/vbrfalse/cfalse/cx0",
		"stereo/fs240/br32000/vbrtrue/ctrue/cx5",
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
				spec.frameSize, spec.frameCount, compositeMaxPacketBytes, pcm)
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
				got, err := enc.EncodeFloat32WithAnalysisMaxBytes(frame, spec.frameSize, frame, compositeMaxPacketBytes)
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

	for _, name := range []string{
		"foa-4ch/fs120/br64000/vbrfalse/cfalse/fmt0",
		"soa-9ch/fs240/br64000/vbrfalse/cfalse/fmt0",
	} {
		t.Run(name, func(t *testing.T) {
			var spec projectionFuzzSpec
			for _, candidate := range buildProjectionFuzzSweep() {
				if candidate.name == name {
					spec = candidate
					break
				}
			}
			if spec.seed == 0 {
				t.Fatal("projection matrix case missing")
			}
			pcm := seededMultichannelPCM(spec.seed, spec.channels, spec.frameSize, spec.frameCount)
			ref, err := encodeLibopusProjection(compositeSampleRate, spec.channels, compositeApplication,
				spec.bitrate, spec.vbr, spec.vbrConstraint, spec.complexity, compositeBandwidthAuto,
				spec.frameSize, spec.frameCount, compositeMaxPacketBytes, 0, pcm, nil)
			if err != nil {
				t.Fatalf("live C projection encode: %v", err)
			}
			if len(ref.packets) != spec.frameCount || len(ref.ranges) != spec.frameCount {
				t.Fatalf("live C records: packets=%d ranges=%d want=%d", len(ref.packets), len(ref.ranges), spec.frameCount)
			}
			enc, err := NewProjectionEncoder(compositeSampleRate, spec.channels)
			if err != nil {
				t.Fatal(err)
			}
			if enc.Streams() != ref.streams || enc.CoupledStreams() != ref.coupledStreams ||
				!bytes.Equal(enc.GetDemixingMatrix(), ref.demixing) || enc.DemixingMatrixGain() != ref.demixingGain {
				t.Fatal("projection layout or demixing differs from live C")
			}
			enc.SetBitrate(spec.bitrate)
			enc.SetVBR(spec.vbr)
			enc.SetVBRConstraint(spec.vbrConstraint)
			enc.SetComplexity(spec.complexity)
			enc.SetBandwidthAuto()
			for f := range spec.frameCount {
				frame := pcm[f*spec.frameSize*spec.channels : (f+1)*spec.frameSize*spec.channels]
				got, err := enc.EncodeFloat32WithAnalysisMaxBytes(frame, spec.frameSize, frame, compositeMaxPacketBytes)
				if err != nil {
					t.Fatalf("frame %d Go projection encode: %v", f, err)
				}
				if !bytes.Equal(got, ref.packets[f]) || enc.GetFinalRange() != ref.ranges[f] {
					t.Fatalf("frame %d: first byte diff=%d len Go/C=%d/%d range Go/C=%08x/%08x",
						f, firstByteMismatch(got, ref.packets[f]), len(got), len(ref.packets[f]), enc.GetFinalRange(), ref.ranges[f])
				}
			}
		})
	}
}
