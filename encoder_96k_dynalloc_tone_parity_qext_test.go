//go:build gopus_qext && !gopus_fixed_point

package gopus

import (
	"bytes"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

type hd96kDynallocToneCase struct {
	channels    int
	frameSize   int
	mode        BitrateMode
	pcm         []float32
}

func hd96kDynallocTonePCM(channels, frameSize, frameCount int) []float32 {
	pcm := make([]float32, channels*frameSize*frameCount)
	for i := range frameSize * frameCount {
		sample := float32(0.5 * math.Sin(2*math.Pi*440*float64(i)/96000))
		for ch := range channels {
			pcm[i*channels+ch] = sample
		}
	}
	return pcm
}

func hd96kDynallocModeName(mode BitrateMode) string {
	if mode == BitrateModeCVBR {
		return "cvbr"
	}
	return "vbr"
}

func TestHD96kDynallocToneScaleMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const bitrate, complexity, frameCount = 195000, 9, 2

	var cases []hd96kDynallocToneCase
	var programs []libopustest.EncodeDiffParams
	for _, frameSize := range []int{1920, 3840} {
		for _, channels := range []int{1, 2} {
			for _, mode := range []BitrateMode{BitrateModeVBR, BitrateModeCVBR} {
				constrained := mode == BitrateModeCVBR
				pcm := hd96kDynallocTonePCM(channels, frameSize, frameCount)
				cases = append(cases, hd96kDynallocToneCase{
					channels: channels, frameSize: frameSize, mode: mode, pcm: pcm,
				})
				programs = append(programs, libopustest.EncodeDiffParams{
					SampleRate: 96000, Channels: channels,
					Application: libopustest.EncodeDiffApplicationAudio,
					ForceMode:   libopustest.EncodeDiffForceModeCELTOnly,
					Bitrate:     bitrate, Complexity: complexity,
					Signal: libopustest.EncodeDiffSignalAuto,
					VBR:    true, VBRConstraint: constrained, LSBDepth: 24,
					FrameSize: frameSize, FrameCount: frameCount, PCM: pcm,
				})
			}
		}
	}
	want, err := libopustest.ProbeEncodeDiffBatch(programs)
	if err != nil {
		t.Fatalf("probe native 96 kHz QEXT-disabled encodes: %v", err)
	}
	if len(want) != len(cases) {
		t.Fatalf("oracle sequences=%d, want %d", len(want), len(cases))
	}

	for i, tc := range cases {
		t.Run(fmt.Sprintf("qext_off/ch%d/frame%d/%s", tc.channels, tc.frameSize, hd96kDynallocModeName(tc.mode)), func(t *testing.T) {
			enc, err := NewEncoder(EncoderConfig{
				SampleRate: 96000, Channels: tc.channels, Application: ApplicationAudio,
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, set := range []func() error{
				func() error { return enc.SetBitrate(bitrate) },
				func() error { return enc.SetComplexity(complexity) },
				func() error { return enc.SetMode(EncoderModeCELT) },
				func() error { return enc.SetBitrateMode(tc.mode) },
				func() error { return enc.SetFrameSize(tc.frameSize) },
				func() error { return enc.SetQEXT(false) },
			} {
				if err := set(); err != nil {
					t.Fatal(err)
				}
			}
			for frame := range frameCount {
				pcm := tc.pcm[frame*tc.frameSize*tc.channels : (frame+1)*tc.frameSize*tc.channels]
				got, err := enc.EncodeFloat32(pcm)
				if err != nil {
					t.Fatalf("frame %d encode: %v", frame, err)
				}
				ref := want[i][frame]
				if ref.Ret <= 0 || !bytes.Equal(got, ref.Packet) || enc.FinalRange() != ref.FinalRange {
					t.Fatalf("frame %d: Go bytes=%d range=%08x; C bytes=%d range=%08x ret=%d",
						frame, len(got), enc.FinalRange(), len(ref.Packet), ref.FinalRange, ref.Ret)
				}
			}
			if tc.channels == 2 && tc.frameSize == 1920 && tc.mode == BitrateModeVBR {
				frame := tc.pcm[:tc.frameSize*tc.channels]
				packet := make([]byte, 8000)
				if allocs := testing.AllocsPerRun(20, func() {
					if _, err := enc.Encode(frame, packet); err != nil {
						t.Fatalf("warm encode: %v", err)
					}
				}); allocs != 0 {
					t.Fatalf("warm native 96 kHz Encode allocs=%g, want 0", allocs)
				}
			}
		})
	}

	for _, frameSize := range []int{1920, 3840} {
		for _, channels := range []int{1, 2} {
			for _, mode := range []BitrateMode{BitrateModeVBR, BitrateModeCVBR} {
				mode := mode
				t.Run(fmt.Sprintf("qext_on/ch%d/frame%d/%s", channels, frameSize, hd96kDynallocModeName(mode)), func(t *testing.T) {
					pcm := hd96kDynallocTonePCM(channels, frameSize, frameCount)
					want, err := libopustest.ProbeQEXTEncode96k(libopustest.QEXTEncode96kParams{
						Channels: channels, FrameSize: frameSize, Bitrate: bitrate,
						Complexity: complexity, VBR: true,
						VBRConstraint: mode == BitrateModeCVBR,
						MaxPacketSize: 8000, PCM: pcm, FrameCount: frameCount,
					})
					if err != nil {
						t.Fatalf("probe native 96 kHz QEXT encode: %v", err)
					}
					enc, err := NewEncoder(EncoderConfig{
						SampleRate: 96000, Channels: channels, Application: ApplicationRestrictedCelt,
					})
					if err != nil {
						t.Fatal(err)
					}
					for _, set := range []func() error{
						func() error { return enc.SetBitrate(bitrate) },
						func() error { return enc.SetComplexity(complexity) },
						func() error { return enc.SetBitrateMode(mode) },
						func() error { return enc.SetFrameSize(frameSize) },
						func() error { return enc.SetQEXT(true) },
					} {
						if err := set(); err != nil {
							t.Fatal(err)
						}
					}
					for frame := range frameCount {
						input := pcm[frame*frameSize*channels : (frame+1)*frameSize*channels]
						got, err := enc.EncodeFloat32(input)
						if err != nil {
							t.Fatalf("frame %d encode: %v", frame, err)
						}
						if !bytes.Equal(got, want.Packets[frame]) || enc.FinalRange() != want.FinalRanges[frame] {
							t.Fatalf("frame %d: Go bytes=%d range=%08x; C bytes=%d range=%08x",
								frame, len(got), enc.FinalRange(), len(want.Packets[frame]), want.FinalRanges[frame])
						}
					}
					if channels == 2 && frameSize == 1920 && mode == BitrateModeVBR {
						packet := make([]byte, 8000)
						if allocs := testing.AllocsPerRun(20, func() {
							if _, err := enc.Encode(pcm[:frameSize*channels], packet); err != nil {
								t.Fatalf("warm encode: %v", err)
							}
						}); allocs != 0 {
							t.Fatalf("warm native 96 kHz QEXT Encode allocs=%g, want 0", allocs)
						}
					}
				})
			}
		}
	}

}
