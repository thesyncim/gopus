//go:build gopus_qext

package gopus

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// celt_decoder.c consumes all signaled QEXT bands even when the active 48 kHz
// mode can render only two. The discarded bands still affect entropy and history.
func TestQEXTDiscardedBandsMatchSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, hybridMode := range []bool{false, true} {
		for _, codedChannels := range []int{1, 2} {
			var base []byte
			if hybridMode {
				base = encodeAPIRateHybridPacketFrameSize(t, codedChannels, 960)
			} else {
				base = encodeAPIRateCELTPacketFrameSize(t, codedChannels, 960)
			}
			if base[0]&3 != 0 {
				t.Fatalf("expected one-frame packet, TOC=%02x", base[0])
			}
			for _, length := range []int{16, 32, 64, 128} {
				padding := append([]byte{248}, bytes.Repeat([]byte{255}, length)...)
				packet := append([]byte{base[0] | 3, 0x41, byte(len(padding))}, base[1:]...)
				packet = append(packet, padding...)
				for _, rate := range []int{48000, 96000} {
					for _, channels := range []int{1, 2} {
						t.Run(fmt.Sprintf("hybrid%t/coded%d/fs%d/api%d/bytes%d", hybridMode, codedChannels, rate, channels, length), func(t *testing.T) {
							assertDecoderSequenceFormatsMatchSelectedLibopus(t, rate, channels, [][]byte{packet, base, nil, base})
						})
					}
				}
			}
		}
	}
}
