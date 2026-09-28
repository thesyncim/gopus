//go:build gopus_qext

package gopus

import (
	"bytes"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// Supported fullband modes in celt_decoder.c consume the QEXT header.
// Other bandwidths use the extension coder directly for main-band refinement.
func TestQEXTNonFullbandHeaderMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		pcm := make([]float32, 960*channels)
		for i := range 960 {
			for channel := range channels {
				pcm[i*channels+channel] = float32(0.28*math.Sin(2*math.Pi*float64(173+97*channel)*float64(i)/48000) +
					0.13*math.Sin(2*math.Pi*float64(1773+423*channel)*float64(i)/48000+0.17))
			}
		}
		for _, config := range []struct{ mode, bandwidth int }{
			{1002, 1101}, {1002, 1103}, {1002, 1104}, {1002, 1105},
			{1001, 1104}, {1001, 1105},
		} {
			encoded, err := libopustest.ProbeEncodeDiff(libopustest.EncodeDiffParams{
				SampleRate: 48000, Channels: channels, Application: 2049,
				ForceMode: config.mode, Bandwidth: config.bandwidth, MaxBandwidth: config.bandwidth,
				Bitrate: 64000, Complexity: 10, Signal: 3002, VBR: true,
				ForceChannels: channels, FrameSize: 960, FrameCount: 1, PCM: pcm,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(encoded) != 1 || len(encoded[0].Packet) < 2 || encoded[0].Packet[0]&3 != 0 {
				t.Fatal("selected C encoder did not return one elementary frame")
			}
			base := encoded[0].Packet
			for _, fill := range []byte{0, 255} {
				padding := append([]byte{248}, bytes.Repeat([]byte{fill}, 128)...)
				packet := append([]byte{base[0] | 3, 0x41, byte(len(padding))}, base[1:]...)
				packet = append(packet, padding...)
				for _, rate := range []int{48000, 96000} {
					t.Run(fmt.Sprintf("mode%d/bw%d/ch%d/fill%d/fs%d", config.mode, config.bandwidth, channels, fill, rate), func(t *testing.T) {
						assertDecoderSequenceFormatsMatchSelectedLibopus(t, rate, channels, [][]byte{packet, base, nil, packet})
					})
				}
			}
		}
	}
}
