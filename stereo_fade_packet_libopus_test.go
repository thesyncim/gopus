package gopus

import (
	"bytes"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestStereoFadeTransitionPacketMatchesLibopus locks the first public packet
// that is sensitive to the stereo width fade's float32 rounding order.
func TestStereoFadeTransitionPacketMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		name       = "xfr_auto_ch2_20ms_16000bps_vbr0_cx5_fecfalse_dtxfalse"
		frameCount = 40
	)
	var spec encXfrSpec
	found := false
	for _, candidate := range buildEncXfrSweep() {
		if candidate.name == name {
			spec, found = candidate, true
			break
		}
	}
	if !found {
		t.Fatalf("missing transition configuration %q", name)
	}
	frameSize := encFrameSamples48k(spec.frameMs)
	pcm, err := encXfrBuildTransitionPCM(frameSize, spec.channels, frameCount, 5)
	if err != nil {
		t.Fatal(err)
	}
	vbr, constrained := vbrFlags(spec.vbr)
	records, err := libopustest.ProbeEncodeDiff(libopustest.EncodeDiffParams{
		SampleRate:    48000,
		Channels:      spec.channels,
		Application:   libopustest.EncodeDiffApplicationAudio,
		ForceMode:     spec.forceMode,
		Bandwidth:     spec.bwCode,
		MaxBandwidth:  spec.bwCode,
		Bitrate:       spec.bitrate,
		Complexity:    spec.complexity,
		Signal:        spec.signal,
		VBR:           vbr,
		VBRConstraint: constrained,
		ForceChannels: spec.channels,
		InbandFEC:     0,
		PacketLoss:    0,
		DTX:           spec.dtx,
		FrameSize:     frameSize,
		FrameCount:    frameCount,
		PCM:           pcm,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo fade packet", err)
		return
	}
	if len(records) != frameCount {
		t.Fatalf("C records=%d want=%d", len(records), frameCount)
	}
	enc, ok := configureEncXfr(spec)
	if !ok {
		t.Fatal("configure Go encoder")
	}
	for frame := range frameCount {
		input := pcm[frame*frameSize*spec.channels : (frame+1)*frameSize*spec.channels]
		got, err := encDiffEncodeOneFrame(enc, input)
		if err != nil {
			t.Fatalf("frame %d: Go encode: %v", frame, err)
		}
		want := records[frame]
		if want.Ret < 0 || want.Ret != len(want.Packet) {
			t.Fatalf("frame %d: invalid C record ret=%d len=%d", frame, want.Ret, len(want.Packet))
		}
		if !bytes.Equal(got, want.Packet) {
			t.Fatalf("frame %d: packet length Go=%d C=%d first byte difference=%d; Go=%x C=%x",
				frame, len(got), len(want.Packet), firstByteDiff(got, want.Packet), got, want.Packet)
		}
		if gotRange := enc.FinalRange(); gotRange != want.FinalRange {
			t.Fatalf("frame %d: final range Go=%08x C=%08x", frame, gotRange, want.FinalRange)
		}
	}
}
