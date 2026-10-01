//go:build gopus_qext && !gopus_fixed_point

package gopus

import (
	"bytes"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestHD96kPublicFinalRangeMatchesLibopus(t *testing.T) {
	const frameSize = 1920
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		t.Run(map[int]string{1: "mono", 2: "stereo"}[channels], func(t *testing.T) {
			for _, gain := range []float32{1, -0.65} {
				t.Run(map[float32]string{1: "positive", -0.65: "inverted"}[gain], func(t *testing.T) {
					pcm := hd96kParitySine(channels, frameSize)
					for i := range pcm {
						pcm[i] *= gain
					}
					want, err := libopustest.ProbeQEXTEncode96k(libopustest.QEXTEncode96kParams{
						Channels: channels, FrameSize: frameSize, Bitrate: 256000, Complexity: 10,
						VBR: false, MaxPacketSize: 8000, PCM: pcm, FrameCount: 1,
					})
					if err != nil {
						libopustest.HelperUnavailable(t, "native 96 kHz QEXT encoder", err)
						return
					}

					enc, err := NewEncoder(EncoderConfig{
						SampleRate: 96000, Channels: channels, Application: ApplicationRestrictedCelt,
					})
					if err != nil {
						t.Fatal(err)
					}
					for _, set := range []func() error{
						func() error { return enc.SetBitrate(256000) },
						func() error { return enc.SetBitrateMode(BitrateModeCBR) },
						func() error { return enc.SetComplexity(10) },
						func() error { return enc.SetQEXT(true) },
					} {
						if err := set(); err != nil {
							t.Fatal(err)
						}
					}
					packet := make([]byte, 8000)
					n, err := enc.Encode(pcm, packet)
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(packet[:n], want.Packets[0]) {
						t.Fatalf("packet differs: Go len=%d C len=%d", n, len(want.Packets[0]))
					}
					if got, wantRange := enc.FinalRange(), want.FinalRanges[0]; got != wantRange {
						t.Fatalf("final range=%08x want=%08x", got, wantRange)
					}
				})
			}
		})
	}
}
