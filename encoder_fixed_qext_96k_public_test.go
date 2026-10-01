//go:build gopus_fixed_point && gopus_qext

package gopus

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestPublicFixedQEXT96kDurationsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		for _, frameSize := range []int{240, 480, 960, 1920} {
			for _, qext := range []bool{false, true} {
				for _, maxBytes := range []int{256, 4000} {
					for _, bitrateMode := range []BitrateMode{BitrateModeCBR, BitrateModeCVBR, BitrateModeVBR} {
						channels, frameSize, qext, maxBytes, bitrateMode := channels, frameSize, qext, maxBytes, bitrateMode
						t.Run(fmt.Sprintf("ch%d/frame%d/qext_%t/cap%d/bitrate_mode_%d", channels, frameSize, qext, maxBytes, bitrateMode), func(t *testing.T) {
							const (
								frameCount = 4
								bitrate    = 256000
							)
							pcm := makeFixedQEXTInventoryPCM(96000, channels, frameSize, frameCount)
							frames := make([]libopustest.OpusEncodeFixedMixedFrame, frameCount)
							for frame := range frames {
								lo := frame * frameSize * channels
								short := pcm[lo : lo+frameSize*channels]
								frames[frame].Format = uint32(frame % 3)
								switch frames[frame].Format {
								case 0:
									frames[frame].ShortPCM = short
								case 1:
									frames[frame].FloatPCM = make([]float32, len(short))
									for i, sample := range short {
										frames[frame].FloatPCM[i] = float32(sample) * (1.0 / 32768.0)
									}
								case 2:
									frames[frame].PCM24 = make([]int32, len(short))
									for i, sample := range short {
										frames[frame].PCM24[i] = int32(sample) << 8
									}
								}
							}
							frames[2].ResetBefore = true
							params := libopustest.OpusEncodeFixedParams{
								SampleRate: 96000, Channels: channels, Application: libopustest.OpusApplicationAudio,
								MaxPacketBytes: maxBytes, ForceMode: libopustest.OpusForceModeCELTOnly,
								Bandwidth: libopustest.OpusBandwidthFullband, Bitrate: bitrate, Complexity: 10,
								VBR: bitrateMode != BitrateModeCBR, VBRConstraint: bitrateMode == BitrateModeCVBR,
								ForceChannels: channels, LSBDepth: 24, FrameSize: frameSize,
							}
							var want []libopustest.OpusEncodeFixedRecord
							var err error
							if qext {
								want, err = libopustest.ProbeOpusEncodeFixedQEXTMixedRecords(params, frames)
							} else {
								want, err = libopustest.ProbeOpusEncodeFixedQEXTRuntimeOffMixedRecords(params, frames)
							}
							if err != nil {
								libopustest.HelperUnavailable(t, "fixed-QEXT native 96 kHz encoder", err)
								return
							}

							enc, err := NewEncoder(EncoderConfig{SampleRate: 96000, Channels: channels, Application: ApplicationAudio})
							if err != nil {
								t.Fatal(err)
							}
							for _, set := range []func() error{
								func() error { return enc.SetMode(EncoderModeCELT) },
								func() error { return enc.SetBandwidth(BandwidthFullband) },
								func() error { return enc.SetMaxBandwidth(BandwidthFullband) },
								func() error { return enc.SetFrameSize(frameSize) },
								func() error { return enc.SetBitrate(bitrate) },
								func() error { return enc.SetComplexity(10) },
								func() error { return enc.SetBitrateMode(bitrateMode) },
								func() error { return enc.SetForceChannels(channels) },
								func() error { return enc.SetLSBDepth(24) },
								func() error { return enc.SetQEXT(qext) },
							} {
								if err := set(); err != nil {
									t.Fatal(err)
								}
							}

							packet := make([]byte, maxBytes)
							for frame := range frameCount {
								if frames[frame].ResetBefore {
									enc.Reset()
								}
								var n int
								switch frames[frame].Format {
								case 0:
									n, err = enc.EncodeInt16(frames[frame].ShortPCM, packet)
								case 1:
									n, err = enc.Encode(frames[frame].FloatPCM, packet)
								case 2:
									n, err = enc.EncodeInt24(frames[frame].PCM24, packet)
								}
								if err != nil {
									t.Fatalf("frame %d EncodeInt16: %v", frame, err)
								}
								got := packet[:n]
								if want[frame].Status != 0 || n != len(want[frame].Packet) ||
									enc.FinalRange() != want[frame].FinalRange || !bytes.Equal(got, want[frame].Packet) {
									t.Fatalf("frame %d: packet{%s}, range got=%08x want=%08x, C status=%d",
										frame, fixedQEXTBytesDiff(got, want[frame].Packet), enc.FinalRange(),
										want[frame].FinalRange, want[frame].Status)
								}
							}

							var encodeErr error
							allocs := testing.AllocsPerRun(20, func() {
								if _, err := enc.EncodeInt16(pcm[:frameSize*channels], packet); err != nil {
									encodeErr = err
								}
							})
							if encodeErr != nil {
								t.Fatal(encodeErr)
							}
							if allocs != 0 {
								t.Fatalf("warm native 96 kHz encode allocs/run = %g, want 0", allocs)
							}
						})
					}
				}
			}
		}
	}
}
