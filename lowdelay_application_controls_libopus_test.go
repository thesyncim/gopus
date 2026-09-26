package gopus

import (
	"bytes"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestLowDelayApplicationControlsMatchLibopus checks the application override
// in opus_encoder.c:1467-1473 with explicit bandwidth and channel controls.
// A forced SILK request is reasserted before every C frame and must leave both
// restricted low-delay applications in CELT mode. Reset must restart the same
// byte and range sequence with the controls retained.
func TestLowDelayApplicationControlsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	type controlCase struct {
		name          string
		application   Application
		cApplication  int
		bitrate       int
		bandwidth     Bandwidth
		cBandwidth    int
		forceChannels int
	}
	cases := []controlCase{
		{"lowdelay_wideband_mono", ApplicationLowDelay, libopustest.EncodeDiffApplicationRestrictedLD, 24000, BandwidthWideband, libopustest.EncodeDiffBandwidthWideband, 1},
		{"lowdelay_fullband_stereo", ApplicationLowDelay, libopustest.EncodeDiffApplicationRestrictedLD, 96000, BandwidthFullband, libopustest.EncodeDiffBandwidthFullband, 2},
		{"restricted_celt_wideband_mono", ApplicationRestrictedCelt, 2053, 32000, BandwidthWideband, libopustest.EncodeDiffBandwidthWideband, 1},
		{"restricted_celt_fullband_stereo", ApplicationRestrictedCelt, 2053, 96000, BandwidthFullband, libopustest.EncodeDiffBandwidthFullband, 2},
	}
	const (
		sampleRate = 48000
		channels   = 2
		frameSize  = 960
		frameCount = 6
	)
	pcm := make([]float32, frameSize*frameCount*channels)
	for i := 0; i < frameSize*frameCount; i++ {
		phase := 2 * math.Pi * float64(i) / sampleRate
		pcm[2*i] = float32(.55*math.Sin(173*phase) + .17*math.Sin(763*phase))
		pcm[2*i+1] = float32(.47*math.Sin(211*phase) - .19*math.Sin(997*phase))
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want, err := libopustest.ProbeEncodeDiff(libopustest.EncodeDiffParams{
				SampleRate:    sampleRate,
				Channels:      channels,
				Application:   tc.cApplication,
				ForceMode:     libopustest.EncodeDiffForceModeSILKOnly,
				Bandwidth:     tc.cBandwidth,
				MaxBandwidth:  tc.cBandwidth,
				Bitrate:       tc.bitrate,
				Complexity:    10,
				Signal:        libopustest.EncodeDiffSignalAuto,
				VBR:           true,
				ForceChannels: tc.forceChannels,
				FrameSize:     frameSize,
				FrameCount:    frameCount,
				PCM:           pcm,
			})
			if err != nil {
				t.Fatalf("live C encode: %v", err)
			}
			if len(want) != frameCount {
				t.Fatalf("live C records: got %d, want %d", len(want), frameCount)
			}

			enc, err := NewEncoder(EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: tc.application})
			if err != nil {
				t.Fatal(err)
			}
			if err := enc.SetFrameSize(frameSize); err != nil {
				t.Fatal(err)
			}
			if err := enc.SetMode(EncoderModeSILK); err != nil {
				t.Fatal(err)
			}
			if err := enc.SetBitrate(tc.bitrate); err != nil {
				t.Fatal(err)
			}
			if err := enc.SetComplexity(10); err != nil {
				t.Fatal(err)
			}
			if err := enc.SetSignal(SignalAuto); err != nil {
				t.Fatal(err)
			}
			if err := enc.SetBandwidth(tc.bandwidth); err != nil {
				t.Fatal(err)
			}
			if err := enc.SetMaxBandwidth(tc.bandwidth); err != nil {
				t.Fatal(err)
			}
			if err := enc.SetForceChannels(tc.forceChannels); err != nil {
				t.Fatal(err)
			}
			enc.SetVBR(true)
			enc.SetVBRConstraint(false)

			for pass := 0; pass < 2; pass++ {
				if pass == 1 {
					enc.Reset()
				}
				for f, ref := range want {
					frame := pcm[f*frameSize*channels : (f+1)*frameSize*channels]
					got, err := enc.EncodeFloat32(frame)
					if err != nil {
						t.Fatalf("pass %d frame %d: Go encode: %v", pass, f, err)
					}
					if ref.Ret != len(got) || !bytes.Equal(got, ref.Packet) || enc.FinalRange() != ref.FinalRange {
						t.Fatalf("pass %d frame %d: Go len=%d range=%08x packet=%x; C ret=%d range=%08x packet=%x",
							pass, f, len(got), enc.FinalRange(), got, ref.Ret, ref.FinalRange, ref.Packet)
					}
				}
			}
		})
	}
}
