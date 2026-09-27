//go:build gopus_fixed_point

package gopus

import (
	"bytes"
	"testing"

	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/testsignal"
)

func TestPublicFixedAutoSILKHybridSequencesMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []struct {
		name, wantMode string
		sampleRate     int
		channels       int
		frameSize      int
		bitrate        int
		application    Application
		oracleApp      int
	}{
		{
			name: "voip-16k-mono-low-rate", wantMode: "silk",
			sampleRate: 16000, channels: 1, frameSize: 320, bitrate: 12000,
			application: ApplicationVoIP, oracleApp: libopustest.OpusApplicationVoIP,
		},
		{
			name: "voip-48k-mono-medium-rate", wantMode: "hybrid",
			sampleRate: 48000, channels: 1, frameSize: 960, bitrate: 32000,
			application: ApplicationVoIP, oracleApp: libopustest.OpusApplicationVoIP,
		},
	}
	const frameCount = 8
	const resetFrame = 5
	const maxPacketBytes = 4000
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			pcm, err := testsignal.GenerateEncoderSignalVariant(
				testsignal.EncoderVariantSpeechLikeV1,
				tc.sampleRate,
				tc.frameSize*tc.channels*frameCount,
				tc.channels,
			)
			if err != nil {
				t.Fatal(err)
			}
			frames := make([]libopustest.OpusEncodeFixedMixedFrame, frameCount)
			for frame := range frameCount {
				start := frame * tc.frameSize * tc.channels
				frames[frame] = libopustest.OpusEncodeFixedMixedFrame{
					Format: 1, FloatPCM: pcm[start : start+tc.frameSize*tc.channels],
					ResetBefore: frame == resetFrame,
				}
			}
			params := libopustest.OpusEncodeFixedParams{
				SampleRate: tc.sampleRate, Channels: tc.channels, Application: tc.oracleApp,
				MaxPacketBytes: maxPacketBytes, Bitrate: tc.bitrate, Complexity: 10,
				VBR: true, VBRConstraint: false, LSBDepth: 24,
				FrameSize: tc.frameSize, FrameCount: frameCount,
			}
			var want []libopustest.OpusEncodeFixedRecord
			if extsupport.QEXT {
				want, err = libopustest.ProbeOpusEncodeFixedQEXTRuntimeOffMixedRecords(params, frames)
			} else {
				want, err = libopustest.ProbeOpusEncodeFixedMixedRecords(params, frames)
			}
			if err != nil {
				libopustest.HelperUnavailable(t, "persistent fixed automatic SILK/Hybrid encoder", err)
				return
			}
			if len(want) != frameCount {
				t.Fatalf("selected fixed C records=%d, want %d", len(want), frameCount)
			}

			enc, err := NewEncoder(EncoderConfig{
				SampleRate: tc.sampleRate, Channels: tc.channels, Application: tc.application,
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, set := range []func() error{
				func() error { return enc.SetMode(EncoderModeAuto) },
				func() error { return enc.SetBandwidthAuto() },
				func() error { return enc.SetMaxBandwidth(BandwidthFullband) },
				func() error { return enc.SetBitrate(tc.bitrate) },
				func() error { return enc.SetComplexity(10) },
				func() error { return enc.SetBitrateMode(BitrateModeVBR) },
				func() error { return enc.SetLSBDepth(24) },
			} {
				if err := set(); err != nil {
					t.Fatal(err)
				}
			}

			packet := make([]byte, maxPacketBytes)
			seen := map[string]bool{}
			encodeSequence := func(label string) {
				t.Helper()
				for frame, input := range frames {
					if input.ResetBefore {
						enc.Reset()
					}
					n, err := enc.Encode(input.FloatPCM, packet)
					if err != nil {
						t.Fatalf("%s frame %d Encode: %v", label, frame, err)
					}
					if want[frame].Status != 0 {
						t.Fatalf("%s frame %d selected fixed C status=%d", label, frame, want[frame].Status)
					}
					mode := fixedPacketMode(want[frame].Packet)
					seen[mode] = true
					if mode != "silk" && mode != "hybrid" {
						t.Fatalf("%s frame %d selected C mode=%s, want automatic SILK/Hybrid", label, frame, mode)
					}
					if n != len(want[frame].Packet) || enc.FinalRange() != want[frame].FinalRange ||
						!bytes.Equal(packet[:n], want[frame].Packet) {
						first := 0
						for first < n && first < len(want[frame].Packet) && packet[first] == want[frame].Packet[first] {
							first++
						}
						t.Fatalf("%s frame %d auto %s packet/range mismatch: bytes Go/C=%d/%d first=%d range=%08x/%08x",
							label, frame, mode, n, len(want[frame].Packet), first, enc.FinalRange(), want[frame].FinalRange)
					}
				}
			}
			encodeSequence("initial")
			if !seen[tc.wantMode] {
				t.Fatalf("selected C sequence did not cover %s mode; modes=%v", tc.wantMode, seen)
			}

			enc.Reset()
			encodeSequence("reset replay")

			warmPCM := frames[frameCount-1].FloatPCM
			for range 2 {
				if _, err := enc.Encode(warmPCM, packet); err != nil {
					t.Fatalf("warm automatic %s encode: %v", tc.wantMode, err)
				}
			}
			var allocErr error
			allocs := testing.AllocsPerRun(20, func() {
				if _, err := enc.Encode(warmPCM, packet); err != nil {
					allocErr = err
				}
			})
			if allocErr != nil {
				t.Fatalf("warm automatic %s cycle: %v", tc.wantMode, allocErr)
			}
			if allocs != 0 {
				t.Fatalf("warmed automatic %s caller-buffer cycle allocated %g objects", tc.wantMode, allocs)
			}
		})
	}
}

func fixedPacketMode(packet []byte) string {
	if len(packet) == 0 {
		return "empty"
	}
	config := int(packet[0] >> 3)
	switch {
	case config < 12:
		return "silk"
	case config < 16:
		return "hybrid"
	default:
		return "celt"
	}
}
