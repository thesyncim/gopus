//go:build gopus_fixed_point && gopus_qext

package fixedpoint

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

func TestCELTEncodeWithECFixedQEXTReferenceArchive(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		frameSize = 960
		maxBytes  = 64
	)

	for _, channels := range [...]int{1, 2} {
		t.Run(fmt.Sprintf("channels_%d", channels), func(t *testing.T) {
			frames := make([]libopustest.CELTFixedQ8Frame, 2)
			for frame := range frames {
				pcm := make([]int32, frameSize*channels)
				for i := range pcm {
					pcm[i] = int32((i*7919+frame*1237)%65536-32768) * 64
					if i/channels >= 410+frame*7 && i/channels < 438+frame*7 {
						pcm[i] *= 5
					}
				}
				frames[frame] = libopustest.CELTFixedQ8Frame{
					PCM:            pcm,
					MaxBytes:       maxBytes,
					PrefixUniform:  []uint32{0xa5, 0x11, 0xc7},
					SetPrediction:  true,
					Prediction:     []int32{2, 1}[frame],
					SilkSignalType: []int32{0, 2}[frame],
					SilkOffset:     []int32{0, 160}[frame],
				}
			}
			params := libopustest.CELTFixedQ8Params{
				SampleRate: 48000, Channels: channels, StreamChannels: channels, FrameSize: frameSize,
				Start: 17, End: 21, Bitrate: 4000, Complexity: 10,
				LSBDepth: 24, VBR: true, Frames: frames,
			}
			want, err := libopustest.ProbeCELTFixedQEXTQ8(params)
			if err != nil {
				libopustest.HelperUnavailable(t, "combined fixed-QEXT CELT encode", err)
				return
			}

			enc := NewCELTEncoderRate(channels, 48000)
			enc.SetBandRange(17, 21)
			enc.SetBitrate(4000)
			enc.SetComplexity(10)
			enc.SetLSBDepth(24)
			enc.SetVBR(true)
			enc.SetConstrainedVBR(false)
			rng := &rangecoding.Encoder{}
			for frame, input := range frames {
				enc.SetPrediction(input.Prediction)
				enc.SetSilkInfo(input.SilkSignalType, input.SilkOffset)
				buffer := make([]byte, maxBytes)
				rng.Init(buffer)
				for _, symbol := range input.PrefixUniform {
					rng.EncodeUniform(symbol, 256)
				}
				n := enc.EncodeWithECRes(input.PCM, frameSize, rng, maxBytes)
				got := rng.Buffer()[:n]
				if n != len(want[frame].Packet) || rng.Range() != want[frame].FinalRange || !bytes.Equal(got, want[frame].Packet) {
					t.Fatalf("frame %d: fixed-QEXT reference archive CELT differs: len=%d/%d range=%08x/%08x got=% x want=% x",
						frame, n, len(want[frame].Packet), rng.Range(), want[frame].FinalRange, got, want[frame].Packet)
				}
			}
		})
	}
}
