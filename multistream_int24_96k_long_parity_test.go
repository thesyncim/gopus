//go:build gopus_qext

package gopus

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestMultistreamEncodeInt2496kLongFramesMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate     = 96000
		bitrate        = 256000
		complexity     = 9
		packetCapacity = 4000
	)
	frameSizes := []int{7680, 9600, 11520}
	layouts := []multistreamInt24CapacityLayout{
		{name: "explicit_mono", channels: 1, streams: 1, mapping: []byte{0}},
		{name: "default_mono", channels: 1, streams: 1, mapping: []byte{0}, useDefault: true},
		{name: "explicit_stereo", channels: 2, streams: 1, coupledStreams: 1, mapping: []byte{0, 1}},
		{name: "default_stereo", channels: 2, streams: 1, coupledStreams: 1, mapping: []byte{0, 1}, useDefault: true},
	}
	signals := []struct {
		name    string
		nonzero bool
	}{
		{name: "silence"},
		{name: "signed_24bit_low_bits", nonzero: true},
	}

	for _, signal := range signals {
		t.Run(signal.name, func(t *testing.T) {
			for _, layout := range layouts {
				t.Run(layout.name, func(t *testing.T) {
					for _, frameSize := range frameSizes {
						t.Run(fmt.Sprintf("%d_samples", frameSize), func(t *testing.T) {
							enc := newMultistreamInt24CapacityEncoder(t, layout)
							if err := enc.SetFrameSize(frameSize); err != nil {
								t.Fatalf("SetFrameSize(%d): %v", frameSize, err)
							}
							if err := enc.SetBitrate(bitrate); err != nil {
								t.Fatalf("SetBitrate(%d): %v", bitrate, err)
							}
							if err := enc.SetComplexity(complexity); err != nil {
								t.Fatalf("SetComplexity(%d): %v", complexity, err)
							}

							pcm := multistreamInt24CapacityPCM(frameSize, layout.channels, signal.nonzero)
							want, err := libopustest.ProbeMultistreamInt24Encode96k(libopustest.MultistreamInt24Encode96kParams{
								Channels:       layout.channels,
								Streams:        layout.streams,
								CoupledStreams: layout.coupledStreams,
								Mapping:        layout.mapping,
								FrameSize:      frameSize,
								PacketCapacity: packetCapacity,
								Bitrate:        bitrate,
								Complexity:     complexity,
								PCM:            pcm,
							})
							if err != nil {
								libopustest.HelperUnavailable(t, "selected QEXT multistream int24 encode", err)
							}

							packet := make([]byte, packetCapacity)
							n, err := enc.EncodeInt24(pcm, packet)
							if err != nil {
								t.Fatalf("EncodeInt24: %v", err)
							}
							if n != len(want.Packet) || !bytes.Equal(packet[:n], want.Packet) {
								t.Fatalf("packet differs from selected C: Go len=%d C len=%d first diff=%d", n, len(want.Packet), firstByteDiff(packet[:n], want.Packet))
							}
							if want.Samples != frameSize {
								t.Fatalf("selected C samples=%d want %d", want.Samples, frameSize)
							}
							if got := enc.FinalRange(); got != want.FinalRange {
								t.Fatalf("final range Go=%08x selected C=%08x", got, want.FinalRange)
							}

							// Warm the same caller-owned PCM, packet, and encoder state before
							// measuring the public long-frame int24 path.
							if _, err := enc.EncodeInt24(pcm, packet); err != nil {
								t.Fatalf("warm EncodeInt24: %v", err)
							}
							var encodeErr error
							allocs := testing.AllocsPerRun(30, func() {
								_, encodeErr = enc.EncodeInt24(pcm, packet)
							})
							if encodeErr != nil {
								t.Fatalf("EncodeInt24 during allocation measurement: %v", encodeErr)
							}
							if allocs != 0 {
								t.Fatalf("warmed 96 kHz %d-sample EncodeInt24 (%s) allocated %g times", frameSize, signal.name, allocs)
							}
						})
					}
				})
			}
		})
	}
}

type multistreamInt24CapacityLayout struct {
	name           string
	channels       int
	streams        int
	coupledStreams int
	mapping        []byte
	useDefault     bool
}

func multistreamInt24CapacityPCM(frameSize, channels int, nonzero bool) []int32 {
	pcm := make([]int32, frameSize*channels)
	if !nonzero {
		return pcm
	}
	for i := range pcm {
		// The odd step keeps every sample's low bit meaningful across the
		// signed 24-bit range without relying on a generated audio fixture.
		pcm[i] = int32((i*7919+12345)&0x7fffff) - 1<<22
	}
	return pcm
}

func newMultistreamInt24CapacityEncoder(t *testing.T, layout multistreamInt24CapacityLayout) *MultistreamEncoder {
	t.Helper()
	if layout.useDefault {
		enc, err := NewMultistreamEncoderDefault(96000, layout.channels, ApplicationAudio)
		if err != nil {
			t.Fatalf("NewMultistreamEncoderDefault(96000, %d): %v", layout.channels, err)
		}
		return enc
	}
	enc, err := NewMultistreamEncoder(96000, layout.channels, layout.streams, layout.coupledStreams, layout.mapping, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewMultistreamEncoder(96000, %d, %d, %d): %v", layout.channels, layout.streams, layout.coupledStreams, err)
	}
	return enc
}
