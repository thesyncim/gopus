package multistream

import (
	"bytes"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestProjectionAnalysisMatchesLibopus uses the original caller channels for
// analysis and matrix-mixed channels for coding, as opus_projection_encode_float
// passes them to opus_multistream_encode_native. Every packet and final range
// comes from an independently encoded, same-input libopus sequence.
func TestProjectionAnalysisMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		channels       = 4
		frameCount     = 6
		bitrate        = 384000
		maxPacketBytes = 4000
	)
	cases := []struct {
		name       string
		frameSize  int
		vbr        bool
		constraint bool
	}{
		{name: "cvbr_20ms", frameSize: 960, vbr: true, constraint: true},
		{name: "cbr_40ms", frameSize: 1920},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pcm := generateAmbisonicsSweep(channels, tc.frameSize, frameCount)
			ref, err := encodeLibopusProjection(48000, channels, 2049, bitrate, tc.vbr, tc.constraint,
				10, -1000, tc.frameSize, frameCount, maxPacketBytes, 0, pcm, nil)
			if err != nil {
				libopustest.HelperUnavailable(t, "projection analysis reference", err)
				return
			}
			if len(ref.packets) != frameCount || len(ref.ranges) != frameCount {
				t.Fatalf("oracle records packets=%d ranges=%d want=%d", len(ref.packets), len(ref.ranges), frameCount)
			}
			enc, err := NewProjectionEncoder(48000, channels)
			if err != nil {
				t.Fatal(err)
			}
			if enc.Streams() != ref.streams || enc.CoupledStreams() != ref.coupledStreams {
				t.Fatalf("stream layout Go=(%d,%d) C=(%d,%d)", enc.Streams(), enc.CoupledStreams(), ref.streams, ref.coupledStreams)
			}
			enc.SetBitrate(bitrate)
			enc.SetVBR(tc.vbr)
			enc.SetVBRConstraint(tc.constraint)
			enc.SetComplexity(10)
			enc.SetBandwidthAuto()
			for frame := range frameCount {
				start := frame * channels * tc.frameSize
				input := pcm[start : start+channels*tc.frameSize]
				got, err := enc.EncodeFloat32WithAnalysisMaxBytes(input, tc.frameSize, input, maxPacketBytes)
				if err != nil {
					t.Fatalf("frame %d: %v", frame, err)
				}
				if !bytes.Equal(got, ref.packets[frame]) || enc.GetFinalRange() != ref.ranges[frame] {
					t.Errorf("frame %d: firstByte=%d GoLen=%d CLen=%d GoRange=%08x CRange=%08x", frame,
						firstByteMismatch(got, ref.packets[frame]), len(got), len(ref.packets[frame]), enc.GetFinalRange(), ref.ranges[frame])
				}
			}
		})
	}
}
