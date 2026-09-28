//go:build gopus_fixed_point

package fixedpoint

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// probeCELTFixedQ8ForBuild selects the archive matching CELTEncoder's
// compile-time arithmetic. The QEXT build uses Q31 CELT coefficients even
// when the runtime extension is disabled, so its oracle still comes from the
// FIXED_POINT+ENABLE_QEXT archive.
func probeCELTFixedQ8ForBuild(p libopustest.CELTFixedQ8Params) ([]libopustest.CELTFixedQ8Record, error) {
	if fixedQEXTBuild {
		return libopustest.ProbeCELTFixedQEXTQ8(p)
	}
	return libopustest.ProbeCELTFixedRawQ8(p)
}

func TestCELTHybridEncodeWithECSeededOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		frameSize = 960
		start     = 17
		end       = 21
		maxBytes  = 64
	)
	prefix := []uint32{0xa5, 0x11, 0xc7}

	for _, stream := range []struct {
		channels       int
		streamChannels int
	}{{channels: 1, streamChannels: 1}, {channels: 2, streamChannels: 1}, {channels: 2, streamChannels: 2}} {
		stream := stream
		t.Run(fmt.Sprintf("channels_%d/stream_%d", stream.channels, stream.streamChannels), func(t *testing.T) {
			frames := make([]libopustest.CELTFixedQ8Frame, 4)
			for frame := range frames {
				pcm := make([]int32, frameSize*stream.channels)
				for i := range pcm {
					// A steady tone with a short transient makes the low-rate weak
					// transient branch observable without using float input.
					v := int32((i*7919+frame*1237)%65536 - 32768)
					pcm[i] = v * 64
					if i/stream.channels >= 410+frame*7 && i/stream.channels < 438+frame*7 {
						pcm[i] *= 5
					}
				}
				frames[frame] = libopustest.CELTFixedQ8Frame{
					PCM:            pcm,
					MaxBytes:       maxBytes,
					PrefixUniform:  append([]uint32(nil), prefix...),
					ResetBefore:    frame == 3,
					SetPrediction:  true,
					Prediction:     []int32{2, 2, 1, 0}[frame],
					SilkSignalType: []int32{0, 2, 0, 2}[frame],
					SilkOffset:     []int32{0, 0, 160, 160}[frame],
				}
				if frame < 3 {
					mask := make([]int32, stream.channels*21)
					lo := -gconstF(3.0)
					span := gconstF(0.5) - lo
					for i := range mask {
						mask[i] = lo + int32(int64(i*7919+frame*1237)%int64(span+1))
					}
					frames[frame].EnergyMask = mask
				}
			}

			want, err := probeCELTFixedQ8ForBuild(libopustest.CELTFixedQ8Params{
				SampleRate: 48000, Channels: stream.channels, StreamChannels: stream.streamChannels, FrameSize: frameSize,
				Start: start, End: end, Bitrate: 4000, Complexity: 10,
				LSBDepth: 24, VBR: true, ConstrainedVBR: false, Frames: frames,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "seeded fixed Hybrid CELT encode", err)
				return
			}

			enc := NewCELTEncoderRate(stream.channels, 48000)
			enc.SetStreamChannels(int32(stream.streamChannels))
			enc.SetBandRange(start, end)
			enc.SetBitrate(4000)
			enc.SetComplexity(10)
			enc.SetLSBDepth(24)
			enc.SetVBR(true)
			enc.SetConstrainedVBR(false)
			rng := &rangecoding.Encoder{}
			for frame, input := range frames {
				if input.ResetBefore {
					enc.Reset()
				}
				if len(input.EnergyMask) != 0 {
					enc.SetEnergyMask(input.EnergyMask)
				}
				buffer := make([]byte, maxBytes)
				rng.Init(buffer)
				for _, symbol := range input.PrefixUniform {
					rng.EncodeUniform(symbol, 256)
				}
				enc.SetPrediction(input.Prediction)
				enc.SetSilkInfo(input.SilkSignalType, input.SilkOffset)
				n := enc.EncodeWithECRes(input.PCM, frameSize, rng, maxBytes)
				got := rng.Buffer()[:n]
				if n != len(want[frame].Packet) || rng.Range() != want[frame].FinalRange ||
					!bytes.Equal(got, want[frame].Packet) {
					first := 0
					for first < n && first < len(want[frame].Packet) && got[first] == want[frame].Packet[first] {
						first++
					}
					t.Fatalf("frame %d: shared-coder Hybrid CELT differs: len=%d/%d first=%d range=%08x/%08x\n got=% x\nwant=% x",
						frame, n, len(want[frame].Packet), first, rng.Range(), want[frame].FinalRange, got, want[frame].Packet)
				}
			}
		})
	}
}

func TestCELTResetClearsEnergyMaskOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		frameSize = 960
		maxBytes  = 640
	)

	for _, channels := range [...]int{1, 2} {
		t.Run(fmt.Sprintf("channels_%d", channels), func(t *testing.T) {
			frames := make([]libopustest.CELTFixedQ8Frame, 4)
			for frame := range frames {
				pcm := make([]int32, frameSize*channels)
				for i := range pcm {
					pcm[i] = int32((i*7919+frame*1237)%65536-32768) * 64
				}
				frames[frame] = libopustest.CELTFixedQ8Frame{
					PCM:           pcm,
					MaxBytes:      maxBytes,
					ResetBefore:   frame == 3,
					SetPrediction: true,
					Prediction:    2,
				}
				if frame < 3 {
					mask := make([]int32, channels*21)
					lo := -gconstF(3.0)
					span := gconstF(0.5) - lo
					for i := range mask {
						mask[i] = lo + int32(int64(i*7919+frame*1237)%int64(span+1))
					}
					frames[frame].EnergyMask = mask
				}
			}

			want, err := probeCELTFixedQ8ForBuild(libopustest.CELTFixedQ8Params{
				SampleRate: 48000, Channels: channels, StreamChannels: channels, FrameSize: frameSize,
				Start: 0, End: 21, Bitrate: 256000, Complexity: 10,
				LSBDepth: 24, VBR: true, ConstrainedVBR: false, Frames: frames,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "CELT reset energy mask", err)
				return
			}

			enc := NewCELTEncoderRate(channels, 48000)
			enc.SetBandRange(0, 21)
			enc.SetBitrate(256000)
			enc.SetComplexity(10)
			enc.SetLSBDepth(24)
			enc.SetVBR(true)
			enc.SetConstrainedVBR(false)
			rng := &rangecoding.Encoder{}
			for frame, input := range frames {
				if input.ResetBefore {
					enc.Reset()
				}
				if len(input.EnergyMask) != 0 {
					enc.SetEnergyMask(input.EnergyMask)
				}
				enc.SetPrediction(input.Prediction)
				buffer := make([]byte, maxBytes)
				rng.Init(buffer)
				n := enc.EncodeWithECRes(input.PCM, frameSize, rng, maxBytes)
				got := rng.Buffer()[:n]
				if n != len(want[frame].Packet) || rng.Range() != want[frame].FinalRange ||
					!bytes.Equal(got, want[frame].Packet) {
					t.Fatalf("frame %d: reset energy mask differs: len=%d/%d range=%08x/%08x got=% x want=% x",
						frame, n, len(want[frame].Packet), rng.Range(), want[frame].FinalRange, got, want[frame].Packet)
				}
			}
		})
	}
}
