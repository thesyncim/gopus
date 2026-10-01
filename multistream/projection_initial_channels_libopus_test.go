package multistream

import (
	"bytes"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestProjectionInitialMonoDecisionMatchesLibopus covers the low-rate coupled
// streams that choose mono coding on their first frame. opus_encoder_init and
// OPUS_RESET_STATE leave prev_channels at zero; seeding it to two delays that
// first-frame decision and changes every subsequent stream's CBR byte budget.
func TestProjectionInitialMonoDecisionMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		channels   = 9
		frameSize  = 480
		frameCount = 5
		bitrate    = 64000
		maxPacket  = 4000
		seed       = 0x90c2
	)
	pcm := seededMultichannelPCM(seed, channels, frameSize, frameCount)
	ref, err := encodeLibopusProjection(48000, channels, 2049, bitrate, false, false,
		10, -1000, frameSize, frameCount, maxPacket, 0, pcm, nil)
	if err != nil {
		libopustest.HelperUnavailable(t, "projection first-frame reference", err)
		return
	}
	if len(ref.packets) != frameCount || len(ref.ranges) != frameCount {
		t.Fatalf("C records packets=%d ranges=%d, want %d", len(ref.packets), len(ref.ranges), frameCount)
	}
	enc, err := NewProjectionEncoder(48000, channels)
	if err != nil {
		t.Fatal(err)
	}
	if enc.Streams() != ref.streams || enc.CoupledStreams() != ref.coupledStreams {
		t.Fatalf("layout Go=(%d,%d) C=(%d,%d)", enc.Streams(), enc.CoupledStreams(), ref.streams, ref.coupledStreams)
	}
	enc.SetBitrate(bitrate)
	enc.SetVBR(false)
	enc.SetVBRConstraint(false)
	enc.SetComplexity(10)
	enc.SetBandwidthAuto()

	for pass := range 2 {
		if pass != 0 {
			enc.Reset()
		}
		for frame := range frameCount {
			start := frame * frameSize * channels
			input := pcm[start : start+frameSize*channels]
			got, encodeErr := encodePacketMax(enc, input, frameSize, input, maxPacket)
			if encodeErr != nil {
				t.Fatalf("pass %d frame %d: Go encode: %v", pass, frame, encodeErr)
			}
			want := ref.packets[frame]
			if !bytes.Equal(got, want) {
				t.Errorf("pass %d frame %d: packet len Go=%d C=%d firstByte=%d configs Go=%v C=%v",
					pass, frame, len(got), len(want), firstByteMismatch(got, want),
					perStreamConfigs(got, enc.Streams()), perStreamConfigs(want, ref.streams))
			}
			if gotRange := enc.GetFinalRange(); gotRange != ref.ranges[frame] {
				t.Errorf("pass %d frame %d: final range Go=%08x C=%08x", pass, frame, gotRange, ref.ranges[frame])
			}
			goStreams, goErr := parseMultistreamPacket(got, enc.Streams())
			cStreams, cErr := parseMultistreamPacket(want, ref.streams)
			if goErr != nil || cErr != nil {
				t.Fatalf("pass %d frame %d: parse Go=%v C=%v", pass, frame, goErr, cErr)
			}
			if len(goStreams) != ref.streams || len(cStreams) != ref.streams {
				t.Fatalf("pass %d frame %d: child count Go=%d C=%d want=%d",
					pass, frame, len(goStreams), len(cStreams), ref.streams)
			}
			for stream := range goStreams {
				if !bytes.Equal(goStreams[stream], cStreams[stream]) {
					t.Errorf("pass %d frame %d stream %d: child len Go=%d C=%d firstByte=%d",
						pass, frame, stream, len(goStreams[stream]), len(cStreams[stream]),
						firstByteMismatch(goStreams[stream], cStreams[stream]))
				}
			}
		}
	}
}
