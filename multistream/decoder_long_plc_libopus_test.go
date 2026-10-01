package multistream

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestMultistreamLongPLCBurstMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	rates := []int{48000}
	if extsupport.QEXT {
		rates = append(rates, 96000)
	}
	for _, mode := range []encoder.Mode{encoder.ModeSILK, encoder.ModeHybrid, encoder.ModeCELT} {
		for _, channels := range []int{1, 2} {
			good := encodeModeSwitchSingleStreamPackets(t, channels, 960, []encoder.Mode{mode})[0]
			packets := make([][]byte, 18)
			packets[0] = good
			packets[16] = good
			packets[17] = good
			for _, rate := range rates {
				t.Run(fmt.Sprintf("mode%d/fs%d/ch%d", mode, rate, channels), func(t *testing.T) {
					assertMultistreamSequenceFormatsMatchSelectedLibopus(t, rate, channels, packets)
				})
			}
		}
	}
}
